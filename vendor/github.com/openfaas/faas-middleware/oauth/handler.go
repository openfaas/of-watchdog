package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
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

// OAuthHandler serves the OAuth authorization-code flow routes:
// /auth/login, /auth/callback and /auth/logout.
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

	// logoutRedirect is where the browser is sent after logout.
	logoutRedirect string

	// errorRedirect is an optional destination for login and callback failures.
	errorRedirect string
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
		logoutRedirect:    cfg.LogoutRedirect,
		errorRedirect:     cfg.ErrorRedirect,
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
	if h.logoutRedirect == "" {
		h.logoutRedirect = cfg.BaseURL.JoinPath("/auth/login").String()
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

// login starts the authorization-code flow. It stores the OAuth state in a
// cookie together with the PKCE verifier and redirects the browser to the provider.
func (h *OAuthHandler) login(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
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

	http.Redirect(w, r, h.logoutRedirect, http.StatusSeeOther)
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

// loginError logs a request failure once, then sends a generic response or redirect.
func (h *OAuthHandler) loginError(w http.ResponseWriter, r *http.Request, message string, status int, err error) {
	log.Printf("OAuth login failed: status=%d reason=%q error=%q\n", status, message, err)
	if h.errorRedirect != "" {
		w.Header().Set("Cache-Control", "no-store")
		http.Redirect(w, r, h.errorRedirect, http.StatusSeeOther)
		return
	}
	http.Error(w, message, status)
}
