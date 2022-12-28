package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/stretchr/testify/require"

	_ "embed"
)

func TestOPAAuthorizer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	const jwtSecretKey = "secret"

	cases := []struct {
		name    string
		cfg     OPAConfig
		policy  string
		input   Input
		allow   bool
		headers map[string]string
	}{
		{
			name: "correctly loads and permits request when the default is true",
			cfg: OPAConfig{
				Debug: false,
				Query: "data.api.authz.allow",
			},
			policy: "testdata/allow_all.rego",
			input: Input{
				Method: http.MethodGet,
				Path:   "/api/endpoint",
			},
			allow: true,
		},
		{
			name: "allow GET request on the public path",
			cfg: OPAConfig{
				Debug: false,
				Query: "data.api.authz.allow",
			},
			policy: "testdata/policy.rego",
			input: Input{
				Method: http.MethodGet,
				Path:   "/api/public",
			},
			allow: true,
		},
		{
			name: "block unauthenticated POST request on the public path",
			cfg: OPAConfig{
				Debug: false,
				Query: "data.api.authz.allow",
			},
			policy: "testdata/policy.rego",
			input: Input{
				Method: http.MethodPost,
				Path:   "/api/public",
			},
			allow: false,
		},
		{
			name: "allow POST request on the public path authenticated as admin",
			cfg: OPAConfig{
				Debug: false,
				Query: "data.api.authz.allow",
			},
			policy: "testdata/policy.rego",
			input: Input{
				Method:        http.MethodPost,
				Path:          "/api/public",
				Authorization: "admin",
			},
			allow: true,
		},
		{
			name: "policy can inspect the rawBody value correctly and returns true",
			cfg: OPAConfig{
				Debug: false,
				Query: "data.api.authz.allow",
			},
			policy: "testdata/policy.rego",
			input: Input{
				Method:  http.MethodPost,
				Path:    "/api/public",
				RawBody: "permitted",
			},
			allow: true,
		},
		{
			name: "policy can inspect the json body value correctly and returns true",
			cfg: OPAConfig{
				Debug: false,
				Query: "data.api.authz.allow",
			},
			policy: "testdata/policy.rego",
			input: Input{
				Method: http.MethodPost,
				Path:   "/api/public",
				Body:   json.RawMessage(`{"override": "permitted"}`),
			},
			allow: true,
		},
		{
			name: "policy can implement basic auth",
			cfg: OPAConfig{
				Debug: false,
				Query: "data.api.authz.basic.allow",
			},
			policy: "testdata/basic_auth.rego",
			input: Input{
				Method:        http.MethodPost,
				Path:          "/api/private",
				Authorization: "Basic " + base64.StdEncoding.EncodeToString([]byte("bob:secretvalue")),
			},
			allow: true,
		},
		{
			name: "can load and merge multiple policies, can evaluate basic auth",
			cfg: OPAConfig{
				Debug: false,
				Query: "data.api.authz.basic.allow",
			},
			policy: "testdata/basic_auth.rego,testdata/policy.rego",
			input: Input{
				Method:        http.MethodPost,
				Path:          "/api/private",
				Authorization: "Basic " + base64.StdEncoding.EncodeToString([]byte("bob:secretvalue")),
			},
			allow: true,
		},
		{
			name: "can load and merge multiple policies, can evaluate token auth",
			cfg: OPAConfig{
				Debug: false,
				Query: "data.api.authz.allow",
			},
			policy: "testdata/basic_auth.rego,testdata/policy.rego",
			input: Input{
				Method:        http.MethodPost,
				Path:          "/api/private",
				Authorization: "admin",
			},
			allow: true,
		},
		{
			name: "can apply HMAC auth policy",
			cfg: OPAConfig{
				Debug: true,
				Query: "data.api.authz.hmac.allow",
			},
			policy: "testdata/hmac_auth.rego",
			input: Input{
				Method:        http.MethodPost,
				Path:          "/api/private",
				RawBody:       `{"message": "hello world"}`,
				Authorization: generateHMAC("secretvalue", `ts=2022-01-01T00:00:00Z,path=/api/private,method=POST,body={"message": "hello world"}`),
				Headers: http.Header{
					"X-Auth-Timestamp": []string{"2022-01-01T00:00:00Z"},
				},
				// we load the key from a secret named "secretKey"
				Data: map[string]string{
					"secretKey": "secretvalue",
				},
			},
			allow: true,
		},
		{
			name: "can apply custom JWT auth policy",
			cfg: OPAConfig{
				Debug: true,
				Query: "data.api.jwt.allow",
			},
			policy: "testdata/jwt.rego",
			input: Input{
				Method:        http.MethodPost,
				Path:          "/api/private",
				RawBody:       `{"message": "hello world"}`,
				Authorization: bearerJWT(jwtSecretKey, map[string]interface{}{"sub": "alice", "email": "alice@test.example.com"}),
				Data: map[string]string{
					"jwt_key": jwtSecretKey,
					// allow tokens with gmail.com or test.example.com in the email field
					"allowed_domains": `{**@gmail.com,**@test.example.com}`,
					"email_field":     "email",
				},
			},
			allow: true,
			headers: map[string]string{
				"X-User-Email": "alice@test.example.com",
			},
		},
		{
			name: "JWT policy rejects non-matching domains",
			cfg: OPAConfig{
				Debug: true,
				Query: "data.api.jwt.allow",
			},
			policy: "testdata/jwt.rego",
			input: Input{
				Method:        http.MethodPost,
				Path:          "/api/private",
				RawBody:       `{"message": "hello world"}`,
				Authorization: bearerJWT(jwtSecretKey, map[string]interface{}{"sub": "alice", "email": "alice@test.example.com"}),
				Data: map[string]string{
					"jwt_key": jwtSecretKey,
					// allow tokens with gmail.com or test.example.com in the email field
					"allowed_domains": `{**@gmail.com,**@company.example.com}`,
					"email_field":     "email",
				},
			},
			allow: false,
		},
		// OICD auth can be seen in the testdata/oidc.rego file
		// but is not possible to include the unit tests because
		// it requires a running OIDC server.
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := LoadPolicy(tc.policy)

			opa, err := NewLocalAuthorizer(policy, tc.cfg)
			require.NoError(t, err)

			result, err := opa.Allowed(ctx, tc.input)
			require.NoError(t, err)
			require.Equal(t, tc.allow, result.Allow)
			require.Len(t, result.Headers, len(tc.headers))
			if tc.headers != nil {
				for k, v := range tc.headers {
					require.Equal(t, v, result.Headers[k])
				}
			}
		})
	}
}

func generateHMAC(key, data string) string {
	h := hmac.New(sha256.New, []byte(key))
	h.Write([]byte(data))
	digest := string(h.Sum(nil))
	out := fmt.Sprintf("%x", digest)
	fmt.Printf("data: %s\nhmac: %s\n---\n", data, out)

	return out
}

func generateTestJWT(key string, claims map[string]interface{}) (string, error) {
	claims["exp"] = time.Now().Add(time.Hour).Unix()
	claims["iat"] = time.Now().Unix()
	claims["nbf"] = time.Now().Unix()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims(claims))

	return token.SignedString([]byte(key))
}

func bearerJWT(key string, claim map[string]interface{}) string {
	token, err := generateTestJWT(key, claim)
	if err != nil {
		panic(err)
	}

	return "Bearer " + token
}
