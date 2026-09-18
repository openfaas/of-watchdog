package oauth

import (
	"context"
	"crypto/ecdsa"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rakutentech/jwk-go/jwk"
)

// ErrInvalidIDToken indicates that an OIDC exchange did not return a valid identity.
var ErrInvalidIDToken = errors.New("invalid OIDC ID token")

const jwksCacheLifetime = 30 * time.Second

type openIDConfiguration struct {
	Issuer                string   `json:"issuer"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	JWKSURI               string   `json:"jwks_uri"`
	SigningAlgorithms     []string `json:"id_token_signing_alg_values_supported"`
}

// OIDCClient adds issuer discovery and ID-token verification to OAuthClient.
// The embedded OAuth client remains responsible for the token endpoint request.
type OIDCClient struct {
	*OAuthClient
	issuer     string
	algorithms []string
	keys       *issuerKeys
}

// NewOIDCClient discovers the issuer's endpoints. Discovered metadata must
// identify the configured issuer exactly; explicit OAuth endpoints are replaced.
// Discovery is bounded by exchangeTimeout and any shorter HTTP client timeout.
func NewOIDCClient(cfg Config, client *http.Client) (*OIDCClient, error) {
	issuer, err := parseProviderURL(cfg.IssuerURL, cfg.AllowHTTP)
	if err != nil {
		return nil, err
	}
	if issuer.RawQuery != "" || issuer.ForceQuery {
		return nil, errors.New("OIDC issuer must not contain a query")
	}
	if cfg.ClientID == "" {
		return nil, errors.New("OIDC requires a client ID")
	}
	secureClient := providerHTTPClient(client, cfg.AllowHTTP)
	var metadata openIDConfiguration
	discovery := strings.TrimRight(cfg.IssuerURL, "/") + "/.well-known/openid-configuration"
	if err := getOIDCJSON(context.Background(), secureClient, discovery, &metadata); err != nil {
		return nil, fmt.Errorf("OIDC discovery: %w", err)
	}
	if metadata.Issuer != cfg.IssuerURL {
		return nil, errors.New("OIDC discovery issuer does not match configured issuer")
	}
	if cfg.AuthorizationEndpoint, err = parseProviderURL(metadata.AuthorizationEndpoint, cfg.AllowHTTP); err != nil {
		return nil, fmt.Errorf("OIDC authorization endpoint: %w", err)
	}
	if cfg.TokenEndpoint, err = parseProviderURL(metadata.TokenEndpoint, cfg.AllowHTTP); err != nil {
		return nil, fmt.Errorf("OIDC token endpoint: %w", err)
	}
	if _, err := parseProviderURL(metadata.JWKSURI, cfg.AllowHTTP); err != nil {
		return nil, fmt.Errorf("OIDC JWKS URI: %w", err)
	}
	// Only asymmetric signatures are accepted from the provider's public JWKS.
	var algorithms []string
	for _, alg := range []string{"RS256", "RS384", "RS512", "PS256", "PS384", "PS512", "ES256", "ES384", "ES512"} {
		if slices.Contains(metadata.SigningAlgorithms, alg) {
			algorithms = append(algorithms, alg)
		}
	}
	if len(algorithms) == 0 {
		return nil, errors.New("OIDC provider advertises no supported ID-token signing algorithms")
	}
	if !slices.Contains(cfg.Scopes, "openid") {
		cfg.Scopes = append([]string{"openid"}, cfg.Scopes...)
	}
	oauthClient, err := NewOAuthClient(cfg, client)
	if err != nil {
		return nil, err
	}
	return &OIDCClient{
		OAuthClient: oauthClient,
		issuer:      cfg.IssuerURL,
		algorithms:  algorithms,
		keys:        &issuerKeys{client: secureClient, uri: metadata.JWKSURI},
	}, nil
}

// Exchange returns tokens only after the ID token has passed verification.
// An access token is never used as a substitute for an OIDC ID token.
func (c *OIDCClient) Exchange(ctx context.Context, code, verifier string) (Token, error) {
	token, err := c.OAuthClient.Exchange(ctx, code, verifier)
	if err != nil {
		return Token{}, err
	}
	if _, err := c.VerifyIDToken(ctx, token.IDToken); err != nil {
		return Token{}, fmt.Errorf("%w: %w", ErrInvalidIDToken, err)
	}
	return token, nil
}

// VerifyIDToken verifies the signature, issuer, client audience, lifetime and
// identity claims. This client uses code flow without requesting a nonce.
func (c *OIDCClient) VerifyIDToken(ctx context.Context, raw string) (jwt.MapClaims, error) {
	claims := jwt.MapClaims{}
	_, err := jwt.ParseWithClaims(raw, claims, func(token *jwt.Token) (any, error) {
		kid, ok := token.Header["kid"].(string)
		if !ok || kid == "" {
			return nil, errors.New("ID token missing key ID")
		}
		return c.keys.get(ctx, kid, token.Method.Alg())
	}, jwt.WithValidMethods(c.algorithms), jwt.WithIssuer(c.issuer),
		jwt.WithAudience(c.clientID), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if err != nil {
		return nil, err
	}
	if sub, ok := claims["sub"].(string); !ok || sub == "" {
		return nil, errors.New("ID token missing subject")
	}
	if issued, err := claims.GetIssuedAt(); err != nil || issued == nil {
		return nil, errors.New("ID token missing issued-at time")
	}
	audience, _ := claims.GetAudience()
	azp, present := claims["azp"]
	if present || len(audience) > 1 {
		if authorizedParty, ok := azp.(string); !ok || authorizedParty != c.clientID {
			return nil, errors.New("ID token authorized party does not match client")
		}
	}
	return claims, nil
}

func getOIDCJSON(ctx context.Context, client *http.Client, endpoint string, value any) error {
	ctx, cancel := context.WithTimeout(ctx, exchangeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("OIDC endpoint returned HTTP %d", res.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(res.Body, maxBodyBytes+1))
	if err != nil {
		return fmt.Errorf("read OIDC response: %w", err)
	}
	if len(data) > maxBodyBytes {
		return errors.New("OIDC response exceeds size limit")
	}
	if err := json.Unmarshal(data, value); err != nil {
		return fmt.Errorf("decode OIDC response: %w", err)
	}
	return nil
}

// issuerKeys caches a provider's signing keys. Expired caches are refreshed even
// for known key IDs, so removed keys do not remain trusted indefinitely. The
// refresh interval also bounds requests triggered by unknown key IDs.
type issuerKeys struct {
	client  *http.Client
	uri     string
	mu      sync.Mutex
	keys    map[string]jwk.KeySpec
	updated time.Time
}

func (c *issuerKeys) get(ctx context.Context, kid, alg string) (any, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.updated.IsZero() || time.Since(c.updated) >= jwksCacheLifetime {
		var set jwk.KeySpecSet
		if err := getOIDCJSON(ctx, c.client, c.uri, &set); err != nil {
			return nil, fmt.Errorf("refresh OIDC JWKS: %w", err)
		}
		keys := make(map[string]jwk.KeySpec)
		for _, key := range set.Keys {
			if key.Use != "" && key.Use != "sig" {
				continue
			}
			switch key.Key.(type) {
			case *rsa.PublicKey, *ecdsa.PublicKey:
				if _, exists := keys[key.KeyID]; exists {
					return nil, errors.New("duplicate signing key ID in JWKS")
				}
				keys[key.KeyID] = key
			}
		}
		c.keys = keys
		c.updated = time.Now()
	}
	key, ok := c.keys[kid]
	if !ok {
		return nil, errors.New("no matching signing key in issuer JWKS")
	}
	if key.Algorithm != "" && key.Algorithm != alg {
		return nil, errors.New("JWK algorithm does not match ID token")
	}
	if !key.ExpiresAt.IsZero() && !time.Now().Before(key.ExpiresAt) {
		return nil, errors.New("issuer signing key expired")
	}
	return key.Key, nil
}
