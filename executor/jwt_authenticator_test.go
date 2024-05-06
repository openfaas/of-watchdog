package executor

import (
	"testing"
)

func Test_isAuthorized(t *testing.T) {
	tests := []struct {
		name        string
		want        bool
		permissions AuthPermissions
		namespace   string
		function    string
	}{
		{
			name: "deny empty permission list",
			want: false,
			permissions: AuthPermissions{
				Permissions: []string{},
			},
			namespace: "staging",
			function:  "env",
		},
		{
			name: "allow empty audience list",
			want: true,
			permissions: AuthPermissions{
				Permissions: []string{"staging:env"},
			},
			namespace: "staging",
			function:  "env",
		},
		{
			name: "allow cluster wildcard",
			want: true,
			permissions: AuthPermissions{
				Permissions: []string{"*"},
			},
			namespace: "staging",
			function:  "figlet",
		},
		{
			name: "allow function wildcard",
			want: true,
			permissions: AuthPermissions{
				Permissions: []string{"dev:*"},
			},
			namespace: "dev",
			function:  "figlet",
		},
		{
			name: "allow namespace wildcard",
			want: true,
			permissions: AuthPermissions{
				Permissions: []string{"*:env"},
			},
			namespace: "openfaas-fn",
			function:  "env",
		},
		{
			name: "allow function",
			want: true,
			permissions: AuthPermissions{
				Permissions: []string{"openfaas-fn:env"},
			},
			namespace: "openfaas-fn",
			function:  "env",
		},
		{
			name: "deny function",
			want: false,
			permissions: AuthPermissions{
				Permissions: []string{"openfaas-fn:env"},
			},
			namespace: "openfaas-fn",
			function:  "figlet",
		},
		{
			name: "deny namespace",
			want: false,
			permissions: AuthPermissions{
				Permissions: []string{"openfaas-fn:*"},
			},
			namespace: "staging",
			function:  "env",
		},
		{
			name: "deny namespace wildcard",
			want: false,
			permissions: AuthPermissions{
				Permissions: []string{"*:figlet"},
			},
			namespace: "staging",
			function:  "env",
		},
		{
			name: "multiple permissions allow function",
			want: true,
			permissions: AuthPermissions{
				Permissions: []string{"openfaas-fn:*", "staging:env"},
			},
			namespace: "staging",
			function:  "env",
		},
		{
			name: "multiple permissions deny function",
			want: false,
			permissions: AuthPermissions{
				Permissions: []string{"openfaas-fn:figlet", "staging-*:env"},
			},
			namespace: "staging",
			function:  "env",
		},
		{
			name: "allow audience",
			want: true,
			permissions: AuthPermissions{
				Permissions: []string{"openfaas-fn:*"},
				Audience:    []string{"openfaas-fn:env"},
			},
			namespace: "openfaas-fn",
			function:  "env",
		},
		{
			name: "deny audience",
			want: false,
			permissions: AuthPermissions{
				Permissions: []string{"openfaas-fn:*"},
				Audience:    []string{"openfaas-fn:env"},
			},
			namespace: "openfaas-fn",
			function:  "figlet",
		},
		{
			name: "allow audience function wildcard",
			want: true,
			permissions: AuthPermissions{
				Permissions: []string{"openfaas-fn:figlet"},
				Audience:    []string{"openfaas-fn:*"},
			},
			namespace: "openfaas-fn",
			function:  "figlet",
		},
		{
			name: "deny audience function wildcard",
			want: false,
			permissions: AuthPermissions{
				Permissions: []string{"openfaas-fn:figlet", "dev:env"},
				Audience:    []string{"openfaas-fn:*"},
			},
			namespace: "dev",
			function:  "env",
		},
		{
			name: "deny audience namespace wildcard",
			want: false,
			permissions: AuthPermissions{
				Permissions: []string{"openfaas-fn:*", "dev:*"},
				Audience:    []string{"*:env"},
			},
			namespace: "dev",
			function:  "figlet",
		},
		{
			name: "allow audience namespace wildcard",
			want: true,
			permissions: AuthPermissions{
				Permissions: []string{"openfaas-fn:*", "dev:*"},
				Audience:    []string{"*:env"},
			},
			namespace: "openfaas-fn",
			function:  "env",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			want := test.want
			got := isAuthorized(test.permissions, test.namespace, test.function)

			if want != got {
				t.Errorf("want: %t, got: %t", want, got)
			}
		})
	}
}
