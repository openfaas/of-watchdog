package oauth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const sessionTokenType = "session"

// sessionClaims are the only state kept in the session cookie. Provider tokens
// are discarded once login completes. Imported provider claims use the fed:
// namespace; the standard subject remains compatible with OpenFaaS IAM.
type sessionClaims struct {
	jwt.RegisteredClaims

	Type            string   `json:"typ"`
	FederatedIssuer string   `json:"fed:iss,omitempty"`
	FederatedEmail  string   `json:"fed:email,omitempty"`
	EmailVerified   *bool    `json:"fed:email_verified,omitempty"`
	FederatedName   string   `json:"fed:name,omitempty"`
	FederatedGroups []string `json:"fed:groups,omitempty"`
	GroupsTruncated bool     `json:"fed:groups_truncated,omitempty"`
}

func (s *sessionClaims) tokenType() string { return s.Type }

// newSession copies only identity claims already verified by the OIDC client,
// never claims parsed directly from provider tokens.
func newSession(token Token) (sessionClaims, error) {
	if token.verifiedClaims == nil {
		return sessionClaims{Type: sessionTokenType}, nil
	}
	claims := token.verifiedClaims
	subject, _ := claims.GetSubject()
	issuer, _ := claims.GetIssuer()
	session := sessionClaims{
		Type:            sessionTokenType,
		FederatedIssuer: issuer,
	}
	session.Subject = "fed:" + subject
	session.FederatedEmail, _ = claims["email"].(string)
	session.FederatedName, _ = claims["name"].(string)
	if verified, ok := claims["email_verified"].(bool); ok {
		session.EmailVerified = &verified
	}
	session.FederatedGroups, session.GroupsTruncated = stringClaimValues(claims["groups"])
	return session, nil
}

func stringClaimValues(value any) ([]string, bool) {
	var values []string
	truncated := false
	switch groups := value.(type) {
	case nil:
	case []string:
		values = append(values, groups...)
	case []any:
		for _, value := range groups {
			if group, ok := value.(string); ok {
				values = append(values, group)
			} else {
				truncated = true
			}
		}
	default:
		truncated = true
	}
	return values, truncated
}

// sessionExpiry chooses one timestamp for the session JWT and browser cookie.
func (h *OAuthHandler) sessionExpiry(token Token, now time.Time) (time.Time, error) {
	expires := now.Add(h.sessionDefaultTTL)
	if token.verifiedClaims != nil {
		claims := token.verifiedClaims
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
		// wrapper's expiry governs function access; the provider is not
		// contacted again and its tokens may expire independently.
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
