package executor

import (
	"crypto"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/rakutentech/jwk-go/jwk"

	"github.com/golang-jwt/jwt/v5"
)

const functionRealm = "IAM function invoke"

func NewJWTAuthMiddleware(next http.Handler) (http.Handler, error) {
	var authority = "http://gateway.openfaas:8080/.well-known/openid-configuration"
	if v, ok := os.LookupEnv("jwt_auth_local"); ok && (v == "true" || v == "1") {
		authority = "http://127.0.0.1:8000/.well-known/openid-configuration"
	}

	jwtAuthDebug := false
	if val, ok := os.LookupEnv("jwt_auth_debug"); ok && val == "true" || val == "1" {
		jwtAuthDebug = true
	}

	config, err := getConfig(authority)
	if err != nil {
		return nil, err
	}

	if jwtAuthDebug {
		log.Printf("[JWT Auth] Issuer: %s\tJWKS URI: %s", config.Issuer, config.JWKSURI)
	}

	keyset, err := getKeyset(config.JWKSURI)
	if err != nil {
		return nil, err
	}

	if jwtAuthDebug {
		for _, key := range keyset.Keys {
			log.Printf("[JWT Auth] Key: %s", key.KeyID)
		}
	}

	issuer := config.Issuer

	namespace, err := getFnNamespace()
	if err != nil {
		return nil, fmt.Errorf("failed to get function namespace: %s", err)
	}
	name, err := getFnName()
	if err != nil {
		return nil, fmt.Errorf("failed to get function name: %s", err)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st := time.Now()
		for _, key := range keyset.Keys {
			log.Printf("%s: %v", issuer, key.KeyID)
		}

		var bearer string
		if v := r.Header.Get("Authorization"); v != "" {
			bearer = strings.TrimPrefix(v, "Bearer ")
		}

		if bearer == "" {
			httpUnauthorized(w, "Bearer must be present in Authorization header")
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
			if jwtAuthDebug {
				log.Printf("[JWT Auth] Token: audience: %v\tissuer: %v", functionClaims.Audience, functionClaims.Issuer)
			}

			kid, ok := token.Header["kid"].(string)
			if !ok {
				return nil, fmt.Errorf("invalid kid: %v", token.Header["kid"])
			}
			var key *jwk.KeySpec
			for _, k := range keyset.Keys {
				if k.KeyID == kid {
					key = &k
					break
				}
			}

			if key == nil {
				return nil, fmt.Errorf("invalid kid: %s", kid)
			}
			return key.Key.(crypto.PublicKey), nil
		}, parseOptions...)
		if err != nil {
			httpUnauthorized(w, fmt.Sprintf("failed to parse JWT token: %s", err))
			log.Printf("%s %s - %d ACCESS DENIED - (%s)", r.Method, r.URL.Path, http.StatusUnauthorized, time.Since(st).Round(time.Millisecond))
			return
		}

		if !token.Valid {
			httpUnauthorized(w, fmt.Sprintf("invalid JWT token: %s", bearer))

			log.Printf("%s %s - %d ACCESS DENIED - (%s)", r.Method, r.URL.Path, http.StatusUnauthorized, time.Since(st).Round(time.Millisecond))
			return
		}

		if !isAuthorized(functionClaims.Authentication, namespace, name) {
			http.Error(w, "insufficient permissions", http.StatusForbidden)

			log.Printf("%s %s - %d ACCESS DENIED - (%s)", r.Method, r.URL.Path, http.StatusForbidden, time.Since(st).Round(time.Millisecond))
			return
		}

		next.ServeHTTP(w, r)
	}), nil
}

// httpUnauthorized replies to the request with the specified error message and 401 HTTP code.
// It sets the WWW-Authenticate header.
// It does not otherwise end the request; the caller should ensure no further writes are done to w.
// The error message should be plain text.
func httpUnauthorized(w http.ResponseWriter, err string) {
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

func getConfig(jwksURL string) (OpenIDConfiguration, error) {
	var config OpenIDConfiguration

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

type OpenIDConfiguration struct {
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

func getFnName() (string, error) {
	name, ok := os.LookupEnv("OPENFAAS_NAME")
	if !ok || len(name) == 0 {
		return "", fmt.Errorf("env variable 'OPENFAAS_NAME' not set")
	}

	return name, nil
}

// getFnNamespace gets the namespace name from the env variable OPENFAAS_NAMESPACE
// or reads it from the service account if the env variable is not present
func getFnNamespace() (string, error) {
	if namespace, ok := os.LookupEnv("OPENFAAS_NAMESPACE"); ok {
		return namespace, nil
	}

	nsVal, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return "", err
	}
	return string(nsVal), nil
}
