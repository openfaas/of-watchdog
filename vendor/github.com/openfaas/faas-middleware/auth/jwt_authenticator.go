package auth

import (
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
)

const (
	authorityURL      = "http://gateway.openfaas:8080/.well-known/openid-configuration"
	localAuthorityURL = "http://127.0.0.1:8000/.well-known/openid-configuration"
	functionRealm     = "IAM function invoke"
)

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
		bearer = strings.TrimPrefix(v, "Bearer ")
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
	}

	functionClaims := FunctionClaims{}
	token, err := jwt.ParseWithClaims(bearer, &functionClaims, func(token *jwt.Token) (interface{}, error) {
		if a.opts.Debug {
			log.Printf("[JWT Auth] Token: audience: %v\tissuer: %v", functionClaims.Audience, functionClaims.Issuer)
		}

		kid, ok := token.Header["kid"].(string)
		if !ok {
			return nil, fmt.Errorf("invalid kid: %v", token.Header["kid"])
		}

		// HV: Consider caching and refreshing the keyset to handle key rotations.
		var key *jwk.KeySpec
		for _, k := range a.keySet.Keys {
			if k.KeyID == kid {
				key = &k
				break
			}
		}

		if key == nil {
			return nil, fmt.Errorf("invalid kid: %s", kid)
		}
		return key.Key, nil
	}, parseOptions...)
	if err != nil {
		writeUnauthorized(w, fmt.Sprintf("failed to parse JWT token: %s", err))
		log.Printf("%s %s - %d ACCESS DENIED - (%s)", r.Method, r.URL.Path, http.StatusUnauthorized, time.Since(st).Round(time.Millisecond))
		return
	}

	if !token.Valid {
		writeUnauthorized(w, fmt.Sprintf("invalid JWT token: %s", bearer))

		log.Printf("%s %s - %d ACCESS DENIED - (%s)", r.Method, r.URL.Path, http.StatusUnauthorized, time.Since(st).Round(time.Millisecond))
		return
	}

	if !isAuthorized(functionClaims.Authentication, a.opts.Namespace, a.opts.Name) {
		w.Header().Set("X-OpenFaaS-Internal", "faas-middleware")
		http.Error(w, "insufficient permissions", http.StatusForbidden)

		log.Printf("%s %s - %d ACCESS DENIED - (%s)", r.Method, r.URL.Path, http.StatusForbidden, time.Since(st).Round(time.Millisecond))
		return
	}

	a.next.ServeHTTP(w, r)
}

// NewJWTAuthMiddleware creates a new middleware handler to handle authentication with OpenFaaS function
// access tokens.
func NewJWTAuthMiddleware(opts JWTAuthOptions, next http.Handler) (http.Handler, error) {
	authority := authorityURL
	if opts.LocalAuthority {
		authority = localAuthorityURL
	}

	config, err := getConfig(authority)
	if err != nil {
		return nil, err
	}

	if opts.Debug {
		log.Printf("[JWT Auth] Issuer: %s\tJWKS URI: %s", config.Issuer, config.JWKSURI)
	}

	keySet, err := getKeyset(config.JWKSURI)
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

// writeUnauthorized replies to the request with the specified error message and 401 HTTP code.
// It sets the WWW-Authenticate header.
// It does not otherwise end the request; the caller should ensure no further writes are done to w.
// The error message should be plain text.
func writeUnauthorized(w http.ResponseWriter, err string) {
	w.Header().Set("X-OpenFaaS-Internal", "faas-middleware")
	w.Header().Set("WWW-Authenticate", fmt.Sprintf("Bearer realm=%s", functionRealm))
	http.Error(w, err, http.StatusUnauthorized)
}

func getKeyset(uri string) (jwk.KeySpecSet, error) {
	var set jwk.KeySpecSet
	req, err := http.NewRequest(http.MethodGet, uri, nil)
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
		body, _ = io.ReadAll(res.Body)
	}

	if res.StatusCode != http.StatusOK {
		return set, fmt.Errorf("failed to get keyset from %s, status code: %d, body: %s", uri, res.StatusCode, string(body))
	}

	if err := json.Unmarshal(body, &set); err != nil {
		return set, err
	}

	return set, nil
}

func getConfig(jwksURL string) (openIDConfiguration, error) {
	var config openIDConfiguration

	req, err := http.NewRequest(http.MethodGet, jwksURL, nil)
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
		body, _ = io.ReadAll(res.Body)
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
func wildCardToRegexp(pattern string) string {
	var result strings.Builder
	for i, literal := range strings.Split(pattern, "*") {

		// Replace * with .*
		if i > 0 {
			result.WriteString(".*")
		}

		// Quote any regular expression meta characters in the
		// literal text.
		result.WriteString(regexp.QuoteMeta(literal))
	}
	return result.String()
}
