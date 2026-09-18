package oauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// exchangeTimeout bounds the token exchange request.
	exchangeTimeout = 15 * time.Second

	// maxBodyBytes bounds the token endpoint response body.
	maxBodyBytes = 1 << 20
)

// AuthorizationClient provides the authorization-code flow used by the handler.
// OIDCClient verifies the ID token before returning from Exchange.
type AuthorizationClient interface {
	AuthorizationURL(state, challenge string) string
	Exchange(ctx context.Context, code, verifier string) (Token, error)
}

// NewClient selects OIDC discovery/verification when an issuer is configured.
func NewClient(cfg Config, client *http.Client) (AuthorizationClient, error) {
	if cfg.IssuerURL != "" {
		return NewOIDCClient(cfg, client)
	}
	return NewOAuthClient(cfg, client)
}

// OAuthClient performs the OAuth authorization-code flow against the
// configured provider: building the authorization URL and exchanging the
// authorization code for tokens.
type OAuthClient struct {
	authorizationEndpoint *url.URL
	tokenEndpoint         *url.URL
	clientID              string
	clientSecret          string
	tokenAuthMethod       string
	redirectURL           string
	scopes                []string
	httpClient            *http.Client
}

// NewOAuthClient builds a client from the configuration. When httpClient is
// nil, http.DefaultClient is used. Provider endpoints must use HTTPS unless
// AllowHTTP is enabled for development.
func NewOAuthClient(cfg Config, httpClient *http.Client) (*OAuthClient, error) {
	for name, endpoint := range map[string]*url.URL{
		"authorization endpoint": cfg.AuthorizationEndpoint,
		"token endpoint":         cfg.TokenEndpoint,
	} {
		if endpoint == nil {
			return nil, fmt.Errorf("OAuth %s is required", name)
		}
		if _, err := parseProviderURL(endpoint.String(), cfg.AllowHTTP); err != nil {
			return nil, fmt.Errorf("OAuth %s: %w", name, err)
		}
	}
	return &OAuthClient{
		authorizationEndpoint: cfg.AuthorizationEndpoint,
		tokenEndpoint:         cfg.TokenEndpoint,
		clientID:              cfg.ClientID,
		clientSecret:          cfg.ClientSecret,
		tokenAuthMethod:       cfg.TokenAuthMethod,
		redirectURL:           cfg.redirectURL(),
		scopes:                cfg.Scopes,
		httpClient:            providerHTTPClient(httpClient, cfg.AllowHTTP),
	}, nil
}

// AuthorizationURL builds the authorization request URL from a copy of the
// authorization endpoint, including an S256 PKCE challenge.
// OIDCClient populates the endpoints through discovery.
func (c *OAuthClient) AuthorizationURL(state, challenge string) string {
	u := *c.authorizationEndpoint
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", c.clientID)
	q.Set("redirect_uri", c.redirectURL)
	q.Set("scope", strings.Join(c.scopes, " "))
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	u.RawQuery = q.Encode()
	return u.String()
}

// Token is the token endpoint response. OIDC returns an ID token, plain
// OAuth only an access token; either may be a JWT.
type Token struct {
	IDToken     string `json:"id_token,omitempty"`
	AccessToken string `json:"access_token,omitempty"`
	ExpiresIn   int64  `json:"expires_in,omitempty"`
}

type tokenResponse struct {
	Token
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// Exchange trades the authorization code and PKCE verifier for tokens.
// Without a client secret, it sends the client ID without client authentication.
func (c *OAuthClient) Exchange(ctx context.Context, code, verifier string) (Token, error) {
	if verifier == "" {
		return Token{}, errors.New("PKCE verifier is required")
	}
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {c.redirectURL},
		"code_verifier": {verifier},
	}
	if c.clientSecret == "" || c.tokenAuthMethod == "client_secret_post" {
		form.Set("client_id", c.clientID)
	}
	if c.clientSecret != "" && c.tokenAuthMethod == "client_secret_post" {
		form.Set("client_secret", c.clientSecret)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenEndpoint.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, fmt.Errorf("create OAuth token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if c.clientSecret != "" && c.tokenAuthMethod != "client_secret_post" {
		req.SetBasicAuth(url.QueryEscape(c.clientID), url.QueryEscape(c.clientSecret))
	}

	res, err := c.httpClient.Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("OAuth token exchange: %w", err)
	}
	defer res.Body.Close()

	var resp tokenResponse
	decodeErr := json.NewDecoder(io.LimitReader(res.Body, maxBodyBytes)).Decode(&resp)
	if res.StatusCode != http.StatusOK || resp.Error != "" {
		if resp.Error != "" {
			return Token{}, fmt.Errorf("OAuth token exchange failed: status=%d error=%q description=%q", res.StatusCode, resp.Error, resp.ErrorDescription)
		}
		return Token{}, fmt.Errorf("OAuth token exchange failed: status=%d", res.StatusCode)
	}
	if decodeErr != nil {
		return Token{}, fmt.Errorf("decode OAuth token response (status=%d): %w", res.StatusCode, decodeErr)
	}
	if resp.IDToken == "" && resp.AccessToken == "" {
		return Token{}, errors.New("OAuth token response contains no tokens")
	}

	return resp.Token, nil
}

// providerHTTPClient applies the endpoint policy to redirects without changing
// the caller's client or its TLS certificate verification settings.
func providerHTTPClient(client *http.Client, allowHTTP bool) *http.Client {
	if client == nil {
		client = http.DefaultClient
	}
	providerClient := *client
	providerClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if _, err := parseProviderURL(req.URL.String(), allowHTTP); err != nil {
			return err
		}
		if client.CheckRedirect != nil {
			return client.CheckRedirect(req, via)
		}
		if len(via) >= 10 {
			return errors.New("too many provider redirects")
		}
		return nil
	}
	return &providerClient
}
