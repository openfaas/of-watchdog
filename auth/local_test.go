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

	"github.com/stretchr/testify/require"

	_ "embed"
)

func TestOPAAuthorizer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	cases := []struct {
		name     string
		cfg      OPAConfig
		policy   string
		input    Input
		expected bool
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
			expected: true,
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
			expected: true,
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
			expected: false,
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
			expected: true,
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
			expected: true,
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
			expected: true,
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
			expected: true,
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
			expected: true,
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
			expected: true,
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
			expected: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy := LoadPolicy(tc.policy)

			opa, err := NewLocalAuthorizer(policy, tc.cfg)
			require.NoError(t, err)

			result, err := opa.Allowed(ctx, tc.input)
			require.NoError(t, err)
			require.Equal(t, tc.expected, result)
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
