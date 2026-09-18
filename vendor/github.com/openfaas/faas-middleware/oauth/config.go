// Package auth provides a generic OAuth authentication layer for the
// watchdog. It provides login routes and issues a session cookie through the
// authorization-code flow.
//
// OIDC mode discovers provider endpoints and verifies ID tokens before issuing
// cookies. Session and login-state cookies are signed JWTs with readable values.
package oauth

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// defaultSessionName is the session cookie name.
	defaultSessionName = "of_session"

	// defaultLoginName is the login state cookie name.
	defaultLoginName = "of_login"
)

// Config configures the auth handlers.
type Config struct {
	// AllowHTTP permits HTTP provider endpoints for development. HTTPS is required by default.
	AllowHTTP bool

	// SessionDefaultTTL applies when an OAuth response supplies no expiry.
	SessionDefaultTTL time.Duration
	// SessionTTL is an explicit local-session lifetime override, not a cap.
	// Zero preserves provider expiry (or SessionDefaultTTL when absent).
	// Nonzero permits function access beyond provider token expiry without
	// refreshing or revalidating that token; its provider-side expiry is unchanged.
	SessionTTL time.Duration

	// BaseURL is the public base URL of the function, e.g.
	// https://gateway.example.com/function/my-fn. The callback is derived
	// as BaseURL + "/auth/callback".
	BaseURL *url.URL

	// IssuerURL enables OIDC discovery and ID-token verification when set.
	IssuerURL string

	// ClientID identifies the OAuth client. ClientSecret is optional for public clients.
	ClientID     string
	ClientSecret string

	// TokenAuthMethod selects client_secret_basic (default) or client_secret_post.
	// It is unused when ClientSecret is empty.
	TokenAuthMethod string

	// AuthorizationEndpoint is the provider's authorization URL, e.g.
	// https://issuer.example.com/oauth2/authorize. OIDC discovery populates
	// this from the issuer when IssuerURL is set.
	AuthorizationEndpoint *url.URL

	// TokenEndpoint is the provider's token URL, e.g.
	// https://issuer.example.com/oauth2/token.
	TokenEndpoint *url.URL

	// Scopes requested in the authorization request. Defaults to ["openid"].
	Scopes []string

	// CookieSecret is the random 32-byte HS256 signing key loaded from the
	// base64-encoded file named by oauth_signing_key. Replicas must share the same key.
	CookieSecret []byte

	// CookieName and LoginCookie override the cookie names.
	CookieName  string
	LoginCookie string

	// LoginRedirect is where the browser is sent after a successful login.
	// Defaults to BaseURL.
	LoginRedirect string

	// LogoutRedirect is where the browser is sent after logout. Defaults to
	// BaseURL + "/auth/login".
	LogoutRedirect string

	// ErrorRedirect is an optional destination for login and callback failures.
	// When empty, failures return an HTTP error response.
	ErrorRedirect string
}

// ReadConfig builds a Config from defaults, environment variables and mounted
// secret files. readFile is used to resolve the client and cookie secret files
// and is injected for testability; when nil, os.ReadFile is used.
func ReadConfig(readFile func(string) ([]byte, error)) (Config, error) {
	cfg := Config{
		Scopes:            []string{"openid"},
		SessionDefaultTTL: time.Hour,
		CookieName:        defaultSessionName,
		LoginCookie:       defaultLoginName,
	}

	if raw := os.Getenv("oauth_allow_http"); raw != "" {
		allow, err := strconv.ParseBool(raw)
		if err != nil {
			return Config{}, fmt.Errorf("invalid oauth_allow_http: %w", err)
		}
		cfg.AllowHTTP = allow
	}

	for name, target := range map[string]*time.Duration{
		"oauth_session_default_ttl": &cfg.SessionDefaultTTL,
		"oauth_session_ttl":         &cfg.SessionTTL,
	} {
		if raw := os.Getenv(name); raw != "" {
			duration, err := time.ParseDuration(raw)
			if err != nil || duration < time.Second || duration%time.Second != 0 {
				return Config{}, fmt.Errorf("%s must be a positive duration in whole seconds, such as 30m or 1h", name)
			}
			*target = duration
		}
	}

	baseURL, err := url.Parse(os.Getenv("oauth_base_url"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid oauth_base_url: %w", err)
	}
	cfg.BaseURL = baseURL

	cfg.IssuerURL = os.Getenv("oauth_issuer_url")
	if cfg.IssuerURL == "" {
		authEndpoint, err := url.Parse(os.Getenv("oauth_authorization_endpoint"))
		if err != nil {
			return Config{}, fmt.Errorf("invalid oauth_authorization_endpoint: %w", err)
		}
		cfg.AuthorizationEndpoint = authEndpoint

		tokenEndpoint, err := url.Parse(os.Getenv("oauth_token_endpoint"))
		if err != nil {
			return Config{}, fmt.Errorf("invalid oauth_token_endpoint: %w", err)
		}
		cfg.TokenEndpoint = tokenEndpoint
	}

	cfg.ClientID = os.Getenv("oauth_client_id")
	cfg.TokenAuthMethod = os.Getenv("oauth_token_auth_method")
	if cfg.TokenAuthMethod != "" && cfg.TokenAuthMethod != "client_secret_basic" && cfg.TokenAuthMethod != "client_secret_post" {
		return Config{}, errors.New("oauth_token_auth_method must be client_secret_basic or client_secret_post")
	}

	if v := os.Getenv("oauth_scopes"); v != "" {
		cfg.Scopes = splitScopes(v)
	}
	if v := os.Getenv("oauth_cookie_name"); v != "" {
		cfg.CookieName = v
	}
	if v := os.Getenv("oauth_login_cookie_name"); v != "" {
		cfg.LoginCookie = v
	}
	if err := cfg.validateCookieNames(); err != nil {
		return Config{}, err
	}
	cfg.LoginRedirect = os.Getenv("oauth_login_redirect")
	cfg.LogoutRedirect = os.Getenv("oauth_logout_redirect")
	cfg.ErrorRedirect = os.Getenv("oauth_error_redirect")

	if readFile == nil {
		readFile = os.ReadFile
	}
	secret, err := readSecret("oauth_signing_key", readFile)
	if err != nil {
		return Config{}, err
	}
	cfg.CookieSecret, err = base64.StdEncoding.DecodeString(strings.TrimSpace(string(secret)))
	if err != nil || len(cfg.CookieSecret) != 32 {
		return Config{}, errors.New("oauth_signing_key must name a file containing a base64-encoded 32-byte key")
	}
	if os.Getenv("oauth_client_secret") != "" {
		data, err := readSecret("oauth_client_secret", readFile)
		if err != nil {
			return Config{}, err
		}
		cfg.ClientSecret = strings.TrimSpace(string(data))
		if cfg.ClientSecret == "" {
			return Config{}, errors.New("oauth_client_secret file must not be empty")
		}
	}

	if cfg.BaseURL.Host == "" || cfg.ClientID == "" {
		return Config{}, errors.New("auth requires base_url and client_id")
	}
	if cfg.IssuerURL != "" {
		issuer, err := parseProviderURL(cfg.IssuerURL, cfg.AllowHTTP)
		if err != nil {
			return Config{}, fmt.Errorf("invalid oauth_issuer_url: %w", err)
		}
		if issuer.RawQuery != "" || issuer.ForceQuery {
			return Config{}, errors.New("oauth_issuer_url must not contain a query")
		}
		return cfg, nil
	}
	if cfg.AuthorizationEndpoint.Host == "" || cfg.TokenEndpoint.Host == "" {
		return Config{}, errors.New("auth requires authorization and token endpoints")
	}
	for name, u := range map[string]*url.URL{
		"authorization endpoint": cfg.AuthorizationEndpoint,
		"token endpoint":         cfg.TokenEndpoint,
	} {
		if _, err := parseProviderURL(u.String(), cfg.AllowHTTP); err != nil {
			return Config{}, fmt.Errorf("auth %s: %w", name, err)
		}
	}

	return cfg, nil
}

// redirectURL returns the public callback URL derived from the base URL.
func (c Config) redirectURL() string {
	return c.BaseURL.JoinPath("/auth/callback").String()
}

// splitScopes splits a scope list on spaces and commas.
func splitScopes(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ' ' || r == ','
	})
	scopes := fields[:0]
	for _, f := range fields {
		if f != "" {
			scopes = append(scopes, f)
		}
	}
	return scopes
}

// readSecret resolves a secret filename in the fixed OpenFaaS secrets directory.
func readSecret(env string, readFile func(string) ([]byte, error)) ([]byte, error) {
	name := os.Getenv(env)
	if name == "" {
		return nil, fmt.Errorf("auth requires %s", env)
	}
	if name == "." || name == ".." || filepath.Base(name) != name || strings.ContainsAny(name, `/\`) {
		return nil, fmt.Errorf("%s must be a secret filename, not a path", env)
	}
	data, err := readFile(filepath.Join("/var/openfaas/secrets", name))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", env, err)
	}
	return data, nil
}

// validateCookieNames rejects names that http.SetCookie would silently omit.
func (c Config) validateCookieNames() error {
	if err := (&http.Cookie{Name: c.CookieName}).Valid(); err != nil {
		return fmt.Errorf("invalid oauth_cookie_name: %w", err)
	}
	if err := (&http.Cookie{Name: c.LoginCookie}).Valid(); err != nil {
		return fmt.Errorf("invalid oauth_login_cookie_name: %w", err)
	}
	if c.CookieName == c.LoginCookie {
		return errors.New("session and login cookie names must differ")
	}
	return nil
}

func parseProviderURL(raw string, allowHTTP bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.Fragment != "" {
		return nil, errors.New("expected an absolute provider URL without userinfo or fragment")
	}
	if u.Scheme != "https" && !(allowHTTP && u.Scheme == "http") {
		return nil, errors.New("provider URL must use HTTPS unless oauth_allow_http is enabled for development")
	}
	return u, nil
}
