package oauth

import (
	"errors"
	"net/http"
	"strings"
)

// NewOAuthMiddleware verifies signed JWT session cookies before they
// reach the function. Requests without a session cookie pass through so the
// upstream can serve public pages. Invalid, expired or duplicate session cookies
// return 401. Valid requests, including their signed cookies, pass through unchanged.
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
			next.ServeHTTP(w, r)
			return
		}

		supplied := r.CookiesNamed(cfg.CookieName)
		if count != 1 || len(supplied) != 1 {
			rejectSession(w)
			return
		}
		var token Token
		if err := cookies.Decode(cfg.CookieName, supplied[0].Value, &token); err != nil || (token.IDToken == "" && token.AccessToken == "") {
			rejectSession(w)
			return
		}
		next.ServeHTTP(w, r)
	}), nil
}

func cookiePartName(part string) string {
	name, _, _ := strings.Cut(part, "=")
	return strings.TrimSpace(name)
}

func rejectSession(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, "Invalid or expired session. Sign in again.", http.StatusUnauthorized)
}
