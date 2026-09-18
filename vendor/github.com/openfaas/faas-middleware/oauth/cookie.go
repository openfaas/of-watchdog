package oauth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Leave room for the cookie name and attributes within a 4096-byte cookie.
const maxCookieValueBytes = 3800

// ErrInvalidCookie covers malformed, unauthenticated and expired cookies.
var ErrInvalidCookie = errors.New("invalid or expired auth cookie")

// CookieCodec issues and verifies signed JWT cookies. Values are readable
// JSON claims; the signature protects their integrity, not their confidentiality.
// A codec can be shared by concurrent requests.
type CookieCodec struct {
	signingKey []byte
	issuer     string
	audience   string
}

type cookieClaims struct {
	jwt.RegisteredClaims
	CookieName string          `json:"cookie_name"`
	Value      json.RawMessage `json:"value"`
}

// NewCookieCodec requires a random 32-byte secret shared by a function's
// replicas. The public function URL is both the issuer and expected audience.
// These values are configuration, never taken from a cookie.
func NewCookieCodec(secret []byte, scope string) (*CookieCodec, error) {
	if len(secret) != 32 {
		return nil, errors.New("cookie secret must contain exactly 32 bytes")
	}
	base, err := url.Parse(scope)
	if err != nil || base.Host == "" || (base.Scheme != "https" && base.Scheme != "http") || base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, errors.New("cookie scope must be an absolute function HTTP(S) URL without userinfo, query or fragment")
	}
	return &CookieCodec{
		signingKey: append([]byte(nil), secret...),
		issuer:     scope,
		audience:   scope,
	}, nil
}

// Encode embeds the value as JSON and signs the JWT with HS256.
// The signature covers the value, cookie name, issuer, audience and expiry.
func (c *CookieCodec) Encode(name string, value any, expires time.Time) (string, error) {
	now := time.Now()
	if expires.Unix() <= now.Unix() {
		return "", ErrInvalidCookie
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	if len(raw) > maxCookieValueBytes {
		return "", fmt.Errorf("auth cookie payload exceeds size limit: bytes=%d limit=%d", len(raw), maxCookieValueBytes)
	}
	claims := cookieClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    c.issuer,
			Audience:  jwt.ClaimStrings{c.audience},
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expires),
		},
		CookieName: name,
		Value:      raw,
	}
	encoded, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(c.signingKey)
	if err != nil {
		return "", err
	}
	if len(encoded) > maxCookieValueBytes {
		return "", fmt.Errorf("auth cookie JWT exceeds size limit: bytes=%d limit=%d", len(encoded), maxCookieValueBytes)
	}
	return encoded, nil
}

// Decode verifies the JWT signature, issuer, function audience, cookie name and
// expiry before decoding the value. It never trusts claims from an unsigned JWT.
func (c *CookieCodec) Decode(name, encoded string, value any) error {
	if len(encoded) > maxCookieValueBytes {
		return ErrInvalidCookie
	}
	claims := cookieClaims{}
	_, err := jwt.ParseWithClaims(
		encoded,
		&claims,
		func(*jwt.Token) (any, error) { return c.signingKey, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(c.issuer),
		jwt.WithAudience(c.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithStrictDecoding(),
	)
	if err != nil || claims.IssuedAt == nil || claims.CookieName != name || len(claims.Audience) != 1 {
		return ErrInvalidCookie
	}
	if len(claims.Value) == 0 || string(claims.Value) == "null" {
		return ErrInvalidCookie
	}
	if err := json.Unmarshal(claims.Value, value); err != nil {
		return ErrInvalidCookie
	}
	return nil
}
