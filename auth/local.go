package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/open-policy-agent/opa/rego"
	"github.com/open-policy-agent/opa/topdown"
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

func NewLocalAuthorizer(policy Policy, cfg OPAConfig) (_ Authorizer, err error) {
	auth := opa{
		cfg: cfg,
	}
	r := rego.New(
		rego.Query(cfg.Query),
		policy,
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

func (a opa) Allowed(ctx context.Context, input Input) (_ bool, err error) {
	result, err := a.query.Eval(ctx, rego.EvalInput(input))
	if err != nil {
		return false, fmt.Errorf("can not evaluate OPA query: %w", err)
	}

	if a.cfg.Debug {
		data, _ := json.Marshal(result)
		log.Printf("OPA query result: %s", string(data))
	}

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
