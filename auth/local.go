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
	cfg.Debug = truthy(OPADebugEnv, "false")
	cfg.Query = os.Getenv(OPAQueryEnv)
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
// when the opa_debug environment variable is set to true.
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
func (a opa) Allowed(ctx context.Context, input Input) (_ AuthResult, err error) {
	resp := AuthResult{}

	result, err := a.query.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		return resp, fmt.Errorf("can not evaluate OPA query: %w", err)
	}

	if a.cfg.Debug {
		data, _ := json.Marshal(result)
		log.Printf("OPA query result: %s", string(data))
	}

	allowed, ok := checkSimpleResponse(result)
	// this is a simple response that only has a single boolean result
	if ok {
		resp.Allow = allowed
		return resp, nil
	}

	// Parse the structured result set
	expr := findExpression(result, a.cfg.Query)
	if expr == nil {
		return resp, fmt.Errorf("can not find query in policy result: %q", a.cfg.Query)
	}

	resp, err = parseExpression(expr)
	if a.cfg.Debug {
		log.Printf("OPA query result: %+v", resp)
	}

	return resp, err
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

// checkSimpleResponse is a duplicate of the ResultSet.Allowed() method, but
// it also returns a second boolean indicating if the result is a simple boolean
// or a more complex expressions.
func checkSimpleResponse(rs rego.ResultSet) (bool, bool) {
	if len(rs) == 1 && len(rs[0].Bindings) == 0 {
		if exprs := rs[0].Expressions; len(exprs) == 1 {
			if b, ok := exprs[0].Value.(bool); ok {
				return b, true
			}
		}
	}
	return false, false
}

func findExpression(result rego.ResultSet, query string) *rego.ExpressionValue {
	for _, r := range result {
		for _, e := range r.Expressions {
			if e.Text == query {
				return e
			}
		}
	}
	return nil
}

func parseExpression(exp *rego.ExpressionValue) (AuthResult, error) {
	var result AuthResult
	data, err := json.Marshal(exp.Value)
	if err != nil {
		return result, err
	}
	err = json.Unmarshal(data, &result)
	return result, err
}
