package oauth

import (
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

var errCookieTooLarge = errors.New("auth cookie exceeds size limit")

// cookieCodec issues and verifies signed JWTs stored in cookies. The claims are
// readable; the signature protects their integrity, not their confidentiality.
// A codec can be shared by concurrent requests.
type cookieCodec struct {
	signingKey []byte
	issuer     string
	audience   string
}

type typedClaims interface {
	jwt.Claims
	tokenType() string
}

// newCookieCodec requires a random 32-byte secret shared by a function's
// replicas. The issuer and audience are configuration, never taken from a JWT.
func newCookieCodec(secret []byte, scope string) (*cookieCodec, error) {
	if len(secret) != 32 {
		return nil, errors.New("cookie secret must contain exactly 32 bytes")
	}
	u, err := url.Parse(scope)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("cookie scope must be an absolute HTTP(S) URL without userinfo, query or fragment")
	}
	return &cookieCodec{
		signingKey: append([]byte(nil), secret...),
		issuer:     scope,
		audience:   scope,
	}, nil
}

// claims returns the registered claims common to locally issued JWTs.
func (c *cookieCodec) claims(expires time.Time) (jwt.RegisteredClaims, error) {
	now := time.Now()
	if expires.Unix() <= now.Unix() {
		return jwt.RegisteredClaims{}, ErrInvalidCookie
	}
	return jwt.RegisteredClaims{
		Issuer:    c.issuer,
		Audience:  jwt.ClaimStrings{c.audience},
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(expires),
	}, nil
}

// encode signs the supplied top-level claims with HS256.
func (c *cookieCodec) encode(claims typedClaims) (string, error) {
	if claims.tokenType() == "" {
		return "", ErrInvalidCookie
	}
	encoded, err := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(c.signingKey)
	if err != nil {
		return "", err
	}
	if len(encoded) > maxCookieValueBytes {
		return "", fmt.Errorf("%w: bytes=%d limit=%d", errCookieTooLarge, len(encoded), maxCookieValueBytes)
	}
	return encoded, nil
}

// decode verifies the JWT signature, issuer, audience, expiry and issue time.
func (c *cookieCodec) decode(encoded string, claims typedClaims, tokenType string) error {
	if len(encoded) > maxCookieValueBytes {
		return ErrInvalidCookie
	}
	_, err := jwt.ParseWithClaims(
		encoded,
		claims,
		func(*jwt.Token) (any, error) { return c.signingKey, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(c.issuer),
		jwt.WithAudience(c.audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithStrictDecoding(),
	)
	if err != nil {
		return ErrInvalidCookie
	}
	if claims.tokenType() != tokenType {
		return ErrInvalidCookie
	}
	issuedAt, err := claims.GetIssuedAt()
	if err != nil || issuedAt == nil {
		return ErrInvalidCookie
	}
	audience, err := claims.GetAudience()
	if err != nil || len(audience) != 1 || audience[0] != c.audience {
		return ErrInvalidCookie
	}
	return nil
}
