package executor

import (
	"crypto"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/rakutentech/jwk-go/jwk"

	"github.com/golang-jwt/jwt/v4"
)

func NewJWTAuthMiddleware(next http.Handler) (http.Handler, error) {

	var authority = "https://gateway.openfaas:8080/.well-known/openid-configuration"
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
			http.Error(w, "Bearer must be present in Authorization header", http.StatusUnauthorized)
			log.Printf("%s %s - %d ACCESS DENIED - (%s)", r.Method, r.URL.Path, http.StatusUnauthorized, time.Since(st).Round(time.Millisecond))
			return
		}

		mapClaims := jwt.MapClaims{}

		token, err := jwt.ParseWithClaims(bearer, &mapClaims, func(token *jwt.Token) (interface{}, error) {
			if jwtAuthDebug {
				log.Printf("[JWT Auth] Token: audience: %v\tissuer: %v", mapClaims["aud"], mapClaims["iss"])
			}

			if mapClaims["iss"] != issuer {
				return nil, fmt.Errorf("invalid issuer: %s", mapClaims["iss"])
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
		})
		if err != nil {
			http.Error(w, fmt.Sprintf("failed to parse JWT token: %s", err), http.StatusUnauthorized)

			log.Printf("%s %s - %d ACCESS DENIED - (%s)", r.Method, r.URL.Path, http.StatusUnauthorized, time.Since(st).Round(time.Millisecond))
			return
		}

		if !token.Valid {
			http.Error(w, fmt.Sprintf("invalid JWT token: %s", bearer), http.StatusUnauthorized)

			log.Printf("%s %s - %d ACCESS DENIED - (%s)", r.Method, r.URL.Path, http.StatusUnauthorized, time.Since(st).Round(time.Millisecond))
			return
		}

		next.ServeHTTP(w, r)
	}), nil
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
