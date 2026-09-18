package oauth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// parseJWT decodes claims without checking the signature. In OIDC mode,
// OIDCClient.Exchange has already verified the ID token.
func parseJWT(raw string) (jwt.MapClaims, error) {
	claims := jwt.MapClaims{}
	if _, _, err := jwt.NewParser().ParseUnverified(raw, claims); err != nil {
		return nil, err
	}
	subject, _ := claims["sub"].(string)
	if subject == "" {
		return nil, errors.New("JWT missing subject")
	}
	return claims, nil
}

// sessionExpiry chooses one timestamp for the wrapper JWT and browser cookie.
func (h *OAuthHandler) sessionExpiry(token Token, now time.Time) (time.Time, error) {
	expires := now.Add(h.sessionDefaultTTL)
	if token.IDToken != "" {
		claims, err := parseJWT(token.IDToken)
		if err != nil {
			return time.Time{}, err
		}
		exp, err := claims.GetExpirationTime()
		if err != nil || exp == nil || !exp.Time.After(now) {
			return time.Time{}, errors.New("invalid ID token expiry")
		}
		expires = exp.Time
	} else if token.ExpiresIn != 0 {
		// Guard conversion from seconds to time.Duration against overflow.
		if token.ExpiresIn < 0 || token.ExpiresIn > int64((1<<63-1)/time.Second) {
			return time.Time{}, errors.New("invalid OAuth token lifetime")
		}
		expires = now.Add(time.Duration(token.ExpiresIn) * time.Second)
	}
	if h.sessionTTL > 0 {
		// Explicit operator opt-in: keep users signed in to this function even
		// when the provider issues short-lived tokens. After login, the signed
		// wrapper's expiry governs function access; the embedded token is not
		// refreshed or revalidated on each request and may expire independently.
		// This is deliberately an override, not min(provider expiry, TTL).
		// Without the override, the provider expiry selected above is retained.
		expires = now.Add(h.sessionTTL)
	}
	expires = expires.Truncate(time.Second)
	if !expires.After(now) {
		return time.Time{}, errors.New("session has expired")
	}
	return expires, nil
}
