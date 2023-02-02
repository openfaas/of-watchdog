package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const secretDir = "/var/openfaas/secrets"

// AuthResult is the parsed response from the OPA policy.
type AuthResult struct {
	// Allow indicates if the request is allowed.
	Allow  bool `json:"allow"`
	Status int  `json:"status,omitempty"`
	// Headers are added to the request and passed to the handler, when the request is allowed.
	// If the request is not allowed, the headers are ignored.
	Headers map[string]string `json:"headers,omitempty"`
}

// Authorizer is the generic request authorizer interface.
type Authorizer interface {
	Allowed(context.Context, Input) (AuthResult, error)
}

type InputConfig struct {
	// IncludeJSONBody controls whether the request body is included in the OPA query,
	// when true the inpug contains the `body` key as parsed JSON.
	IncludeJSONBody bool
	// IncludeRawBody controls whether the request body is included in the OPA query,
	// when true the input contains the `rawBody` key as the raw request body.
	IncludeRawBody bool
	// IncludeHeaders controls if the raw request headers are included in the OPA query,
	// when true the input contains the `header` key which is a map[string][]string
	IncludeHeaders bool
	// ErrorContentType is the content type used for the error message when the
	// authorizer rejects the request.
	ErrorContentType string
	AdditionalData   map[string]string
	SkipPaths        map[string]struct{}
}

type Input struct {
	Method        string            `json:"method,omitempty"`
	Path          string            `json:"path,omitempty"`
	Headers       http.Header       `json:"headers,omitempty"`
	RawBody       string            `json:"rawBody,omitempty"`
	Authorization string            `json:"authorization,omitempty"`
	Body          json.RawMessage   `json:"body,omitempty"`
	Data          map[string]string `json:"data,omitempty"`
}

func InputConfigFromEnv() (cfg InputConfig, err error) {
	cfg.ErrorContentType = os.Getenv(OPAErrorContentTypeEnv)
	if cfg.ErrorContentType == "" {
		cfg.ErrorContentType = "text/plain"
	}

	cfg.IncludeJSONBody = truthy(OPAIncludeBodyEnv, "false")
	cfg.IncludeRawBody = truthy(OPAIncludeRawBodyEnv, "false")
	cfg.IncludeHeaders = truthy(OPAIncludeHeadersEnv, "false")

	cfg.SkipPaths = make(map[string]struct{})
	paths := os.Getenv(OPASkipPathsEnv)
	for _, path := range strings.Split(paths, ",") {
		cfg.SkipPaths[path] = struct{}{}
	}

	//TODO: add SkipPattern and use a radix tree to support skip patterns? see go-chi and https://github.com/armon/go-radix

	env := authEnviron()
	cfg.AdditionalData, err = loadAdditionalData(env)

	return cfg, err
}

type Middleware func(next http.Handler) http.Handler

func New(impl Authorizer, cfg InputConfig) Middleware {
	var errorWriter func(w http.ResponseWriter, msg string, status int) = http.Error
	if strings.Contains(cfg.ErrorContentType, "json") {
		errorWriter = jsonError
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, ok := cfg.SkipPaths[r.URL.Path]; ok {
				next.ServeHTTP(w, r)
				return
			}

			input := Input{
				Method:        r.Method,
				Path:          r.URL.Path,
				Authorization: r.Header.Get("Authorization"),
				Data:          cfg.AdditionalData,
			}

			if cfg.IncludeHeaders {
				input.Headers = r.Header
			}

			var err error
			var body []byte
			if cfg.IncludeRawBody || cfg.IncludeJSONBody {
				body, err = safeReadBody(r)
				if err != nil {
					errorWriter(w, "can not read request body", http.StatusInternalServerError)
					return
				}
			}

			if cfg.IncludeRawBody {
				input.RawBody = string(body)
			}

			if cfg.IncludeJSONBody {
				input.Body = json.RawMessage(body)
			}

			result, err := impl.Allowed(r.Context(), input)
			if err != nil {
				errorWriter(w, "can not process authentication", http.StatusInternalServerError)
				return
			}

			if result.Allow {
				for k, v := range result.Headers {
					r.Header.Set(k, v)
				}
				next.ServeHTTP(w, r)
				return
			}

			status := http.StatusUnauthorized
			if result.Status != 0 {
				status = result.Status
			}

			errorWriter(w, "unauthorized", status)
			return
		})
	}
}

// jsonError writes the error msg as a JSON object to the response writer
func jsonError(w http.ResponseWriter, msg string, status int) {
	w.Header().Set("Content-Type", "application/json")
	http.Error(w, fmt.Sprintf(`{"error": %q}`, msg), http.StatusInternalServerError)
}

// return a copy of the request body and then reset the request body
func safeReadBody(r *http.Request) ([]byte, error) {
	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	defer r.Body.Close()

	r.Body = io.NopCloser(bytes.NewReader(body))

	return body, nil
}

// NewAuthorizer constructs an authorizer from the environment
func NewAuthorizer(path string) (Authorizer, error) {
	cfg := OPAConfigFromEnv()

	switch {
	case !strings.Contains(path, "://"):
		policy := LoadPolicy(path)
		return NewLocalAuthorizer(policy, cfg)
	case strings.HasPrefix(path, "http://"), strings.HasPrefix(path, "https://"):
		return nil, fmt.Errorf("remote auth is not implemented")
	default:
		return nil, fmt.Errorf("unsupported auth path")
	}
}

func loadAdditionalData(options map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for name, value := range options {
		if !strings.HasPrefix(name, OPAInputPrefixEnv) {
			continue
		}
		if name == OPAInputSecretsEnv {
			continue
		}

		out[strings.TrimPrefix(name, OPAInputPrefixEnv)] = value
	}

	names := options[OPAInputSecretsEnv]
	if names == "" {
		return out, nil
	}

	requiredSecrets := strings.Split(names, ",")
	for _, name := range requiredSecrets {
		secretPath := filepath.Join(secretDir, name)
		data, err := os.ReadFile(secretPath)
		if err != nil {
			return nil, err
		}
		out[name] = string(data)
	}

	return out, nil
}

func authEnviron() map[string]string {
	out := map[string]string{}
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, OPAInputPrefixEnv) {
			name, value, ok := cut(env, "=")
			if !ok {
				continue
			}
			out[name] = value
		}
	}

	return out
}

func cut(s, sep string) (before, after string, found bool) {
	if i := strings.Index(s, sep); i >= 0 {
		return s[:i], s[i+len(sep):], true
	}
	return s, "", false
}
