package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strings"
	"time"
)

const (
	// defaultStateLifetime bounds how long a login attempt stays valid.
	defaultStateLifetime = 10 * time.Minute
)

type loginSession struct {
	State    string `json:"state"`
	Verifier string `json:"code_verifier"`
}

// OAuthHandler serves the OAuth authorization-code flow routes: the login page
// at /auth/login, plus /auth/callback and /auth/logout.
type OAuthHandler struct {
	sessionCookie     string
	loginCookie       string
	cookiePath        string
	sessionDefaultTTL time.Duration
	sessionTTL        time.Duration
	secure            bool
	client            AuthorizationClient
	mux               *http.ServeMux
	cookies           *CookieCodec

	// loginRedirect is where the browser is sent after a successful login.
	loginRedirect string

	// loginURL is the public login page for unauthenticated visitors.
	loginURL string
}

// NewOAuthHandler builds an OAuthHandler from the configuration and the given
// client. The handler keeps only the fields it needs; the OAuth flow itself
// lives in the client, which is constructed by the caller so it can supply
// its own http.Client.
func NewOAuthHandler(cfg Config, client AuthorizationClient) (*OAuthHandler, error) {
	cookies, err := NewCookieCodec(cfg.CookieSecret, cfg.BaseURL.String())
	if err != nil {
		return nil, err
	}
	if err := cfg.validateCookieNames(); err != nil {
		return nil, err
	}
	h := &OAuthHandler{
		cookies:           cookies,
		sessionDefaultTTL: cfg.SessionDefaultTTL,
		sessionTTL:        cfg.SessionTTL,
		sessionCookie:     cfg.CookieName,
		loginCookie:       cfg.LoginCookie,
		cookiePath:        strings.TrimRight(cfg.BaseURL.EscapedPath(), "/"),
		secure:            cfg.BaseURL.Scheme == "https",
		client:            client,
		loginRedirect:     cfg.LoginRedirect,
		loginURL:          cfg.BaseURL.JoinPath("/auth/login").String(),
	}
	if h.sessionDefaultTTL == 0 {
		h.sessionDefaultTTL = time.Hour
	}
	if h.cookiePath == "" {
		h.cookiePath = "/"
	}
	if h.loginRedirect == "" {
		h.loginRedirect = cfg.BaseURL.String()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/auth/login", h.login)
	mux.HandleFunc("/auth/callback", h.callback)
	mux.HandleFunc("/auth/logout", h.logout)
	h.mux = mux
	return h, nil
}

// ServeHTTP serves the registered auth routes.
func (h *OAuthHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.mux.ServeHTTP(w, r)
}

// login serves /auth/login: the login page on GET/HEAD, the flow start on POST.
func (h *OAuthHandler) login(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		h.loginPage(w, r)
	case http.MethodPost:
		h.startLogin(w, r)
	default:
		w.Header().Set("Allow", "GET, HEAD, POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// loginPage renders the built-in login page. Visiting it never contacts the
// provider; the button POSTs to start the flow.
func (h *OAuthHandler) loginPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	_ = authPage.Execute(w, h.loginURL)
}

// startLogin sets the OAuth state and PKCE verifier cookie, then redirects to
// the provider.
func (h *OAuthHandler) startLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	verifier := make([]byte, 32)
	// In Go 1.26, rand.Read fills the buffer or terminates the process.
	// It never returns an error or continues with an unfilled verifier.
	rand.Read(verifier)
	session := loginSession{
		State:    rand.Text(),
		Verifier: base64.RawURLEncoding.EncodeToString(verifier),
	}
	challenge := sha256.Sum256([]byte(session.Verifier))
	expires := time.Now().Add(defaultStateLifetime).Truncate(time.Second)
	value, err := h.cookies.Encode(h.loginCookie, session, expires)
	if err != nil {
		h.loginError(w, r, "login creation failed", http.StatusInternalServerError, err)
		return
	}
	h.setCookie(w, h.loginCookie, value, expires)
	http.Redirect(w, r, h.client.AuthorizationURL(session.State, base64.RawURLEncoding.EncodeToString(challenge[:])), http.StatusFound)
}

// callback completes the authorization-code flow: it verifies the state,
// exchanges the code, validates the returned JWT and issues a session cookie.
func (h *OAuthHandler) callback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	var session loginSession
	err := h.cookies.Decode(h.loginCookie, h.readCookie(r, h.loginCookie), &session)
	if err != nil || state == "" || state != session.State {
		if err == nil {
			err = errors.New("login state missing or mismatched")
		}
		h.loginError(w, r, "invalid or expired login", http.StatusBadRequest, err)
		return
	}
	if session.Verifier == "" {
		h.loginError(w, r, "invalid or expired login", http.StatusBadRequest, errors.New("PKCE verifier missing from login cookie"))
		return
	}
	if providerError := r.URL.Query().Get("error"); providerError != "" {
		err := fmt.Errorf("OAuth authorization failed: error=%q description=%q", providerError, r.URL.Query().Get("error_description"))
		h.loginError(w, r, "invalid or expired login", http.StatusBadRequest, err)
		return
	}
	if code == "" {
		h.loginError(w, r, "invalid or expired login", http.StatusBadRequest, errors.New("authorization code missing"))
		return
	}
	h.clearCookie(w, h.loginCookie)

	ctx, cancel := context.WithTimeout(r.Context(), exchangeTimeout)
	defer cancel()

	token, err := h.client.Exchange(ctx, code, session.Verifier)
	if err != nil {
		if errors.Is(err, ErrInvalidIDToken) {
			h.loginError(w, r, "identity verification failed; sign in again", http.StatusUnauthorized, err)
			return
		}
		h.loginError(w, r, "OAuth login failed", http.StatusBadGateway, err)
		return
	}
	expires, err := h.sessionExpiry(token, time.Now())
	if err != nil {
		h.loginError(w, r, "identity verification failed; sign in again", http.StatusUnauthorized, err)
		return
	}
	// Preserve the provider's tokens inside a signed JWT cookie.
	value, err := h.cookies.Encode(h.sessionCookie, token, expires)
	if err != nil {
		h.loginError(w, r, "session creation failed", http.StatusInternalServerError, err)
		return
	}
	h.setCookie(w, h.sessionCookie, value, expires)

	http.Redirect(w, r, h.loginRedirect, http.StatusSeeOther)
}

// logout removes the issued cookies.
func (h *OAuthHandler) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	h.clearCookie(w, h.sessionCookie)
	h.clearCookie(w, h.loginCookie)

	http.Redirect(w, r, h.loginURL, http.StatusSeeOther)
}

// setCookie writes an HttpOnly cookie with the same expiry as its JWT.
func (h *OAuthHandler) setCookie(w http.ResponseWriter, name, value string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     h.cookiePath,
		HttpOnly: true,
		Secure:   h.secure,
		SameSite: http.SameSiteLaxMode,
		Expires:  expires,
	})
}

// clearCookie removes a cookie.
func (h *OAuthHandler) clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name: name, Value: "", Path: h.cookiePath, HttpOnly: true,
		Secure: h.secure, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
}

// readCookie returns the value of the named cookie, or "" when absent.
func (h *OAuthHandler) readCookie(r *http.Request, name string) string {
	cookie, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return cookie.Value
}

// loginError logs a request failure once, then sends a generic built-in page.
func (h *OAuthHandler) loginError(w http.ResponseWriter, r *http.Request, message string, status int, err error) {
	log.Printf("OAuth login failed: status=%d reason=%q error=%q\n", status, message, err)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	_ = authErrorPage.Execute(w, loginErrorData{
		Message:  message,
		LoginURL: h.loginURL,
	})
}

type loginErrorData struct {
	Message  string
	LoginURL string
}

// authPageStyles is shared by the login and error pages.
const authPageStyles = `<style>
:root{--bg:#10141c;--card:#161c28;--border:#34445c;--text:#ecf2ff;--text-2:#b8c7dc;--accent:#9dd6ff;--accent-ink:#102033}
*{box-sizing:border-box}
html,body{margin:0}
body{margin:0;min-height:100vh;display:flex;align-items:center;justify-content:center;padding:24px;background:radial-gradient(circle at top left,#273c58,transparent 42%),#10141c;color:var(--text);font-family:system-ui,-apple-system,sans-serif}
.card{width:min(100%,440px);padding:40px;border:1px solid var(--border);border-radius:16px;background:rgba(16,20,28,.92);box-shadow:0 28px 76px #0006;text-align:center}
h1{margin:0 0 6px;font-size:clamp(1.4rem,5vw,1.75rem);letter-spacing:-.04em}
p{margin:8px 0 26px;color:var(--text-2);line-height:1.6}
button,a.btn{min-height:44px;width:100%;border:0;border-radius:8px;background:var(--accent);color:var(--accent-ink);cursor:pointer;font:inherit;font-weight:800}
button:hover,a.btn:hover{background:#b8e3ff}
button:focus-visible,a.btn:focus-visible{outline:3px solid var(--accent);outline-offset:2px}
a.btn{display:block;text-align:center;line-height:44px;text-decoration:none}
@media (max-width:480px){.card{padding:28px 22px}}
</style>`

var authPage = template.Must(template.New("login").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign in</title>
` + authPageStyles + `
</head>
<body>
<main class="card">
<h1>Authentication required</h1>
<p>Sign in to continue.</p>
<form method="post" action="{{.}}"><button type="submit">Sign in</button></form>
</main>
</body>
</html>
`))

var authErrorPage = template.Must(template.New("loginError").Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sign in failed</title>
` + authPageStyles + `
</head>
<body>
<main class="card">
<h1>Sign in failed</h1>
<p>{{.Message}}</p>
<a class="btn" href="{{.LoginURL}}">Try Again</a>
</main>
</body>
</html>
`))
