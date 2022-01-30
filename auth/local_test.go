package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
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
