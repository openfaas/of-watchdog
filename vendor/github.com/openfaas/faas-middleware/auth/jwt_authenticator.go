package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rakutentech/jwk-go/jwk"
	"github.com/rakutentech/jwk-go/jwktypes"
)

const (
	authorityURL      = "http://gateway.openfaas:8080/.well-known/openid-configuration"
	localAuthorityURL = "http://127.0.0.1:8080/.well-known/openid-configuration"
	functionRealm     = "IAM function invoke"

	bearerScheme = "Bearer "

	// authorityTimeout bounds each request made to the OpenID
	// configuration and JWKS endpoints.
	authorityTimeout = 10 * time.Second

	maxBodyBytes = 1 << 20 // 1 MiB
)

// validSigningMethods pins the set of algorithms accepted for function tokens.
// Only asymmetric algorithms are allowed; HS* and 'none' are rejected to
// prevent algorithm confusion attacks.
var validSigningMethods = []string{
	"RS256", "RS384", "RS512",
	"PS256", "PS384", "PS512",
	"ES256", "ES384", "ES512",
	"EdDSA",
}

type jwtAuth struct {
	next http.Handler
	opts JWTAuthOptions

	keySet jwk.KeySpecSet
	issuer string
}

// JWTAuthOptions stores the configuration for JWT based function authentication
type JWTAuthOptions struct {
	Name           string
	Namespace      string
	LocalAuthority bool
	Debug          bool

	// Authority overrides the OpenID configuration endpoint used to
	// discover the issuer and JWKS URI. When empty, the in-cluster
	// gateway is used, or a local gateway for LocalAuthority.
	// Tokens are still expected to carry the OpenFaaS function claim
	// shape and must be signed by a key in the authority's JWKS.
	Authority string
}

func (a jwtAuth) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	issuer := a.issuer

	st := time.Now()

	if a.opts.Debug {
		for _, key := range a.keySet.Keys {
			log.Printf("%s: %v", issuer, key.KeyID)
		}
	}

	var bearer string
	if v := r.Header.Get("Authorization"); v != "" {
		if len(v) > len(bearerScheme) && strings.EqualFold(v[:len(bearerScheme)], bearerScheme) {
			bearer = strings.TrimSpace(v[len(bearerScheme):])
		}
	}

	if bearer == "" {
		writeUnauthorized(w, "Bearer must be present in Authorization header")
		log.Printf("%s %s - %d ACCESS DENIED - (%s)", r.Method, r.URL.Path, http.StatusUnauthorized, time.Since(st).Round(time.Millisecond))
		return
	}

	parseOptions := []jwt.ParserOption{
		jwt.WithIssuer(issuer),
		// The OpenFaaS gateway is the expected audience but we can use the issuer url
		// since the gateway is also the issuer of function tokens and thus has the same url.
		jwt.WithAudience(issuer),
		jwt.WithLeeway(time.Second * 1),
		jwt.WithValidMethods(validSigningMethods),
	}

	functionClaims := FunctionClaims{}
	token, err := jwt.ParseWithClaims(bearer, &functionClaims, func(token *jwt.Token) (interface{}, error) {
		if a.opts.Debug {
			log.Printf("[JWT Auth] Token: audience: %v\tissuer: %v", functionClaims.Audience, functionClaims.Issuer)
		}

		kid, ok := token.Header["kid"].(string)
		if !ok {
			return nil, fmt.Errorf("invalid kid")
		}

		var key *jwk.KeySpec
		for i := range a.keySet.Keys {
			if a.keySet.Keys[i].KeyID == kid {
				key = &a.keySet.Keys[i]
				break
			}
		}

		if key == nil {
			return nil, fmt.Errorf("invalid kid: %s", kid)
		}

		if err := validateKeyType(token.Method, key); err != nil {
			return nil, err
		}

		return key.Key, nil
	}, parseOptions...)
	if err != nil {
		writeUnauthorized(w, "failed to verify JWT token")
		log.Printf("%s %s - %d ACCESS DENIED - (%s) (%s)", r.Method, r.URL.Path, http.StatusUnauthorized, time.Since(st).Round(time.Millisecond), err.Error())
		return
	}

	if !token.Valid {
		writeUnauthorized(w, "invalid JWT token")

		log.Printf("%s %s - %d ACCESS DENIED - (%s)", r.Method, r.URL.Path, http.StatusUnauthorized, time.Since(st).Round(time.Millisecond))
		return
	}

	if !isAuthorized(functionClaims.Authentication, a.opts.Namespace, a.opts.Name) {
		w.Header().Set("X-OpenFaaS-Internal", "faas-middleware")
		http.Error(w, "insufficient permissions", http.StatusForbidden)

		log.Printf("%s %s - %d ACCESS DENIED - (%s)", r.Method, r.URL.Path, http.StatusForbidden, time.Since(st).Round(time.Millisecond))
		return
	}

	r.Header.Set("X-Auth-Seconds", fmt.Sprintf("%f", time.Since(st).Seconds()))

	a.next.ServeHTTP(w, r)
}

// NewJWTAuthMiddleware creates a new middleware handler to handle authentication with OpenFaaS function
// access tokens.
func NewJWTAuthMiddleware(ctx context.Context, opts JWTAuthOptions, next http.Handler) (http.Handler, error) {
	authority := authorityURL
	if opts.Authority != "" {
		authority = opts.Authority
	} else if opts.LocalAuthority {
		authority = localAuthorityURL
	}

	configCtx, cancel := context.WithTimeout(ctx, authorityTimeout)
	config, err := getConfig(configCtx, authority)
	cancel()
	if err != nil {
		return nil, err
	}

	if opts.Debug {
		log.Printf("[JWT Auth] Issuer: %s\tJWKS URI: %s", config.Issuer, config.JWKSURI)
	}

	keySetCtx, cancel := context.WithTimeout(ctx, authorityTimeout)
	keySet, err := getKeyset(keySetCtx, config.JWKSURI)
	cancel()
	if err != nil {
		return nil, err
	}

	if opts.Debug {
		for _, key := range keySet.Keys {
			log.Printf("[JWT Auth] Key: %s", key.KeyID)
		}
	}

	return jwtAuth{
		next:   next,
		opts:   opts,
		keySet: keySet,
		issuer: config.Issuer,
	}, nil
}

// validateKeyType ensures the token's signing algorithm family matches the
// key type found in the JWKS. This prevents algorithm confusion attacks, for
// example a token signed with HS256 when the JWKS contains an asymmetric key.
func validateKeyType(method jwt.SigningMethod, key *jwk.KeySpec) error {
	kty, _, _ := key.KeyType()

	switch {
	case strings.HasPrefix(method.Alg(), "RS"), strings.HasPrefix(method.Alg(), "PS"):
		if kty == jwktypes.RSA {
			return nil
		}
	case strings.HasPrefix(method.Alg(), "ES"):
		if kty == jwktypes.EC {
			return nil
		}
	case method.Alg() == "EdDSA":
		if kty == jwktypes.OKP {
			return nil
		}
	}

	return fmt.Errorf("signing method %s does not match key type %s", method.Alg(), kty)
}

// writeUnauthorized replies to the request with the specified error message and 401 HTTP code.
// It sets the WWW-Authenticate header.
// It does not otherwise end the request; the caller should ensure no further writes are done to w.
// The error message should be plain text.
func writeUnauthorized(w http.ResponseWriter, err string) {
	w.Header().Set("X-OpenFaaS-Internal", "faas-middleware")
	w.Header().Set("WWW-Authenticate", fmt.Sprintf("Bearer realm=%s", functionRealm))
	http.Error(w, err, http.StatusUnauthorized)
}

func getKeyset(ctx context.Context, uri string) (jwk.KeySpecSet, error) {
	var set jwk.KeySpecSet
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return set, err
	}

	req.Header.Add("User-Agent", "openfaas-watchdog")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return set, err
	}

	var body []byte

	if res.Body != nil {
		defer res.Body.Close()
		body, _ = io.ReadAll(io.LimitReader(res.Body, maxBodyBytes))
	}

	if res.StatusCode != http.StatusOK {
		return set, fmt.Errorf("failed to get keyset from %s, status code: %d, body: %s", uri, res.StatusCode, string(body))
	}

	if err := json.Unmarshal(body, &set); err != nil {
		return set, err
	}

	return set, nil
}

func getConfig(ctx context.Context, jwksURL string) (openIDConfiguration, error) {
	var config openIDConfiguration

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, jwksURL, nil)
	if err != nil {
		return config, err
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return config, err
	}

	var body []byte
	if res.Body != nil {
		defer res.Body.Close()
		body, _ = io.ReadAll(io.LimitReader(res.Body, maxBodyBytes))
	}

	if res.StatusCode != http.StatusOK {
		return config, fmt.Errorf("failed to get config from %s, status code: %d, body: %s", jwksURL, res.StatusCode, string(body))
	}

	if err := json.Unmarshal(body, &config); err != nil {
		return config, err
	}

	return config, nil
}

type openIDConfiguration struct {
	Issuer  string `json:"issuer"`
	JWKSURI string `json:"jwks_uri"`
}

type FunctionClaims struct {
	jwt.RegisteredClaims

	Authentication AuthPermissions `json:"function"`
}

type AuthPermissions struct {
	Permissions []string `json:"permissions"`
	Audience    []string `json:"audience,omitempty"`
}

func isAuthorized(auth AuthPermissions, namespace, fn string) bool {
	functionRef := fmt.Sprintf("%s:%s", namespace, fn)

	return matchResource(auth.Audience, functionRef, false) &&
		matchResource(auth.Permissions, functionRef, true)
}

// matchResources checks if ref matches one of the resources.
// The function will return true if a match is found.
// If required is false, this function will return true if a match is found or the resource list is empty.
func matchResource(resources []string, ref string, req bool) bool {
	if !req {
		if len(resources) == 0 {
			return true
		}
	}

	for _, res := range resources {
		if res == "*" {
			return true
		}

		if matchString(res, ref) {
			return true
		}
	}

	return false
}

func matchString(pattern string, value string) bool {
	if len(pattern) > 0 {
		result, _ := regexp.MatchString(wildCardToRegexp(pattern), value)
		return result
	}

	return pattern == value
}

// wildCardToRegexp converts a wildcard pattern to a regular expression pattern.
// The pattern is anchored so that only a full match of the resource reference
// is accepted, with '*' expanding to any sequence of characters. Wildcard
// scopes such as '*' or 'ns:fn*' are preserved.
func wildCardToRegexp(pattern string) string {
	var result strings.Builder
	result.WriteString("^")
	for i, literal := range strings.Split(pattern, "*") {

		// Replace * with .*
		if i > 0 {
			result.WriteString(".*")
		}

		// Quote any regular expression meta characters in the
		// literal text.
		result.WriteString(regexp.QuoteMeta(literal))
	}
	result.WriteString("$")
	return result.String()
}
