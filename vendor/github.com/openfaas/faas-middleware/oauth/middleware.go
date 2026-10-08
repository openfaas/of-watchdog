package oauth

import (
	"errors"
	"net/http"
	"strings"
)

// NewOAuthMiddleware verifies the signed session cookie on every request and
// redirects missing or invalid ones to the login page, so no public pages are
// served when auth is enabled. Valid requests pass through unchanged.
func NewOAuthMiddleware(cfg Config, next http.Handler) (http.Handler, error) {
	if cfg.CookieName == "" {
		return nil, errors.New("session cookie name is required")
	}
	tokens, err := newCookieCodec(cfg.CookieSecret, cfg.BaseURL.String())
	if err != nil {
		return nil, err
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Count session cookies in the raw headers to distinguish a missing cookie
		// from a malformed one and detect duplicates. net/http silently skips malformed
		// cookies; relying only on its parser could treat an invalid session as an
		// anonymous request and allow it through instead of rejecting it.
		count := 0
		for _, header := range r.Header.Values("Cookie") {
			for _, part := range strings.Split(header, ";") {
				if cookiePartName(part) == cfg.CookieName {
					count++
				}
			}
		}
		if count == 0 {
			redirectToLogin(w, r, cfg)
			return
		}

		supplied := r.CookiesNamed(cfg.CookieName)
		if count != 1 || len(supplied) != 1 {
			if count > 1 {
				// A stale root-path cookie can coexist with the function's cookie.
				// Expire both before login, otherwise the callback only replaces the
				// function cookie and the browser remains in a redirect loop.
				// Cookie headers do not reveal paths or domains; only clear these
				// known host-only scopes, leaving other functions' paths alone.
				paths := []string{"/"}
				if path := strings.TrimRight(cfg.BaseURL.EscapedPath(), "/"); path != "" {
					paths = append(paths, path)
				}
				for _, path := range paths {
					http.SetCookie(w, &http.Cookie{
						Name: cfg.CookieName, Value: "", Path: path, HttpOnly: true,
						Secure: cfg.BaseURL.Scheme == "https", SameSite: http.SameSiteLaxMode, MaxAge: -1,
					})
				}
			}
			redirectToLogin(w, r, cfg)
			return
		}
		var session sessionClaims
		if err := tokens.decode(supplied[0].Value, &session, sessionTokenType); err != nil {
			redirectToLogin(w, r, cfg)
			return
		}
		next.ServeHTTP(w, r)
	}), nil
}

// readSingleCookie rejects missing, malformed and duplicate named cookies.
func readSingleCookie(r *http.Request, name string) (string, error) {
	count := 0
	for _, header := range r.Header.Values("Cookie") {
		for _, part := range strings.Split(header, ";") {
			if cookiePartName(part) == name {
				count++
			}
		}
	}
	cookies := r.CookiesNamed(name)
	if count != 1 || len(cookies) != 1 || cookies[0].Value == "" {
		return "", ErrInvalidCookie
	}
	return cookies[0].Value, nil
}

func cookiePartName(part string) string {
	name, _, _ := strings.Cut(part, "=")
	return strings.TrimSpace(name)
}

// redirectToLogin redirects to the login page.
func redirectToLogin(w http.ResponseWriter, r *http.Request, cfg Config) {
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, cfg.BaseURL.JoinPath("/auth/login").String(), http.StatusSeeOther)
}
