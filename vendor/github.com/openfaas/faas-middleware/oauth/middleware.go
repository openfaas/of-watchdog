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
	cookies, err := NewCookieCodec(cfg.CookieSecret, cfg.BaseURL.String())
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
			redirectToLogin(w, r, cfg)
			return
		}
		var token Token
		if err := cookies.Decode(cfg.CookieName, supplied[0].Value, &token); err != nil || (token.IDToken == "" && token.AccessToken == "") {
			redirectToLogin(w, r, cfg)
			return
		}
		next.ServeHTTP(w, r)
	}), nil
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
