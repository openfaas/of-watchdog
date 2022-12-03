package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/bcrypt"

	"github.com/open-policy-agent/opa/ast"
	"github.com/open-policy-agent/opa/rego"
	"github.com/open-policy-agent/opa/topdown"
	"github.com/open-policy-agent/opa/types"
)

// Policy is the OPA policy configuration method, which is returned from
// rego.Module, rego.LoadBundle, or rego.Load
type Policy func(r *rego.Rego)

// LoadPolicy creates an OPA Policy loader for the given path
func LoadPolicy(name string) Policy {
	if strings.HasSuffix(name, "tar.gz") {
		// optionally use rego.LoadBundle if name ends with tar.gz,
		// this method can read a compressed bundle.tar.gz
		return rego.LoadBundle(name)
	}

	paths := strings.Split(name, ",")
	for idx, value := range paths {
		if strings.Contains(value, "/") {
			// if it contains a slash, then it is already a path
			continue
		}
		secretPath := filepath.Join(secretDir, value)
		log.Printf("auth policy looks like secret name, loading from %q", secretPath)
		paths[idx] = secretPath
	}
	return rego.Load(paths, nil)
}

// OPAConfig controls the OPA authorizer options.
type OPAConfig struct {
	// Debug enables debug logging of the query result.
	Debug bool
	// Query is the OPA query to evaluate.
	Query string
}

func OPAConfigFromEnv() (cfg OPAConfig) {
	cfg.Debug = truthy("OPA_DEBUG", "false")
	cfg.Query = os.Getenv("OPA_QUERY")
	return cfg
}

// NewLocalAuthorizer creates a OPA Authorizer instance for the given Policy.
//
// This method also exposes custom functions for policies to use. Currently
// it exposes:
//   - bcrypt_eq
//   - constant_compare
//
// Additionally, it modifies the logging so that it will use the default log writer
// when the OPA_DEBUG environment variable is set to true.
func NewLocalAuthorizer(policy Policy, cfg OPAConfig) (_ Authorizer, err error) {
	auth := opa{
		cfg: cfg,
	}
	r := rego.New(
		rego.Query(cfg.Query),
		policy,
		rego.Function2(
			&rego.Function{
				// expose bcrypt.CompareHashAndPassword to policies
				// so that they can do do secure basic auth
				Name: "bcrypt_eq",
				Decl: types.NewFunction(types.Args(types.S, types.S), types.B),
			},
			func(_ rego.BuiltinContext, hash *ast.Term, pwd *ast.Term) (*ast.Term, error) {
				hashStr, ok := hash.Value.(ast.String)
				if !ok {
					return nil, errors.New("Hash must be a string")
				}

				pwdStr, ok := pwd.Value.(ast.String)
				if !ok {
					return nil, errors.New("Password must be a string")
				}

				err := bcrypt.CompareHashAndPassword([]byte(hashStr), []byte(pwdStr))
				return ast.BooleanTerm(err == nil), nil
			},
		),
		rego.Function2(
			&rego.Function{
				// expose subtle.constant_compare to policies
				// so that they can do secure string comparisons
				Name: "constant_compare",
				Decl: types.NewFunction(types.Args(types.S, types.S), types.B),
			},
			func(_ rego.BuiltinContext, value1Term *ast.Term, value2Term *ast.Term) (*ast.Term, error) {
				value1, ok := value1Term.Value.(ast.String)
				if !ok {
					return nil, errors.New("Value 1 must be a string")
				}

				value2, ok := value2Term.Value.(ast.String)
				if !ok {
					return nil, errors.New("Value 2 must be a string")
				}

				return ast.BooleanTerm(subtle.ConstantTimeCompare([]byte(value1), []byte(value2)) == 1), nil
			},
		),
		rego.EnablePrintStatements(cfg.Debug),
		rego.PrintHook(topdown.NewPrintHook(log.Writer())),
	)
	auth.query, err = r.PrepareForEval(context.Background())
	return auth, err
}

type opa struct {
	query rego.PreparedEvalQuery
	cfg   OPAConfig
}

// Allowed implements the Authorizer interface and validates the given input against
// the configured OPA policy.
func (a opa) Allowed(ctx context.Context, input Input) (_ bool, err error) {
	result, err := a.query.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		return false, fmt.Errorf("can not evaluate OPA query: %w", err)
	}

	// this block allows us to inspect the result
	// it will also be useful if we want to support
	// complex result sets. See the TODO below.
	if a.cfg.Debug {
		data, _ := json.Marshal(result)
		log.Printf("OPA query result: %s", string(data))

		if len(result) == 1 && len(result[0].Bindings) == 0 {
			if exprs := result[0].Expressions; len(exprs) == 1 {
				value := exprs[0].Value
				log.Printf("OPA result value: %v\n OPA Result type: %T", value, value)
			}
		}

	}

	// this only allows policies that have a single boolean result
	// policies that return complex objects will be treated as false
	//
	// TODO: allow policies to return complex objects which are then
	//       integrated into the request as X_AUTH headers.
	//       this will allow, for example, policies to pass information
	//       parsed from token.
	return result.Allowed(), nil
}

// truthy converts the given env variable to a boolean.
func truthy(name string, fallback string) bool {
	value, ok := os.LookupEnv(name)
	if !ok {
		value = fallback
	}
	switch strings.ToLower(value) {
	case "true", "yes", "on", "1":
		return true
	}
	return false
}
