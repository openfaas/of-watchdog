package pkg

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openfaas/faas-middleware/oauth"
	"github.com/openfaas/of-watchdog/config"
	"github.com/openfaas/of-watchdog/executor"
)

func TestJWTAuthIssuerOverride(t *testing.T) {
	t.Setenv("OPENFAAS_NAME", "test-function")
	t.Setenv("OPENFAAS_NAMESPACE", "openfaas-fn")

	for _, local := range []bool{false, true} {
		for _, tc := range []struct {
			name          string
			issuerPath    string
			discoveryPath string
		}{
			{"root", "", "/.well-known/openid-configuration"},
			{"trailing-slash", "/", "/.well-known/openid-configuration"},
			{"base-path", "/tenant", "/tenant/.well-known/openid-configuration"},
			{"base-path-trailing-slash", "/tenant/", "/tenant/.well-known/openid-configuration"},
		} {
			t.Run(fmt.Sprintf("local=%t/%s", local, tc.name), func(t *testing.T) {
				var discoveryCalls, keyCalls atomic.Int32
				keys := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					keyCalls.Add(1)
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"keys":[]}`)
				}))
				defer keys.Close()

				discovery := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != tc.discoveryPath {
						http.NotFound(w, r)
						return
					}
					discoveryCalls.Add(1)
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"issuer":"https://gateway.example.com","jwks_uri":%q}`, keys.URL)
				}))
				defer discovery.Close()

				cfg, err := config.New([]string{
					"fprocess=echo test",
					"jwt_auth=true",
					fmt.Sprintf("jwt_auth_local=%t", local),
					"jwt_auth_issuer=" + discovery.URL + tc.issuerPath,
				})
				if err != nil {
					t.Fatal(err)
				}
				handler, err := makeJWTAuthHandler(context.Background(), cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					t.Error("unauthenticated request reached the function")
				}))
				if err != nil {
					t.Fatal(err)
				}
				if discoveryCalls.Load() != 1 || keyCalls.Load() != 1 {
					t.Fatalf("expected discovery and separate JWKS endpoint to be fetched once, got %d and %d", discoveryCalls.Load(), keyCalls.Load())
				}

				res := httptest.NewRecorder()
				handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "/", nil))
				if res.Code != http.StatusUnauthorized {
					t.Fatalf("expected HTTP 401 without a token, got %d", res.Code)
				}
			})
		}
	}
}

func TestMakeOneShotHandlerDrainsAndRejectsSubsequentRequests(t *testing.T) {
	var calls int32
	var drains int32

	handler := makeOneShotHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte("ok"))
	}), "", func(reason string) {
		atomic.AddInt32(&drains, 1)
	})

	firstReq := httptest.NewRequest(http.MethodPost, "/", nil)
	firstRes := httptest.NewRecorder()
	handler.ServeHTTP(firstRes, firstReq)

	if firstRes.Code != http.StatusAccepted {
		t.Fatalf("expected first request to pass through, got status %d", firstRes.Code)
	}

	secondReq := httptest.NewRequest(http.MethodPost, "/", nil)
	secondRes := httptest.NewRecorder()
	handler.ServeHTTP(secondRes, secondReq)

	if secondRes.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected second request to be rejected, got status %d", secondRes.Code)
	}

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("expected next handler to be called once, got %d", got)
	}

	if got := atomic.LoadInt32(&drains); got != 1 {
		t.Fatalf("expected drain to be scheduled once, got %d", got)
	}
}

func TestMakeOneShotHandlerIgnoresReadyEndpoint(t *testing.T) {
	var calls int32
	var drains int32

	handler := makeOneShotHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusOK)
	}), "/ready", func(reason string) {
		atomic.AddInt32(&drains, 1)
	})

	readyReq := httptest.NewRequest(http.MethodGet, "/ready", nil)
	readyRes := httptest.NewRecorder()
	handler.ServeHTTP(readyRes, readyReq)

	firstReq := httptest.NewRequest(http.MethodPost, "/", nil)
	firstRes := httptest.NewRecorder()
	handler.ServeHTTP(firstRes, firstReq)

	if firstRes.Code != http.StatusOK {
		t.Fatalf("expected first real request to pass through, got status %d", firstRes.Code)
	}

	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Fatalf("expected handler to be called for readiness and first invoke, got %d", got)
	}

	if got := atomic.LoadInt32(&drains); got != 1 {
		t.Fatalf("expected drain to be scheduled once for the real request, got %d", got)
	}
}

func TestOAuthSessionThroughHTTPRunner(t *testing.T) {
	baseURL, _ := url.Parse("https://example.com/function/my-fn")
	cfg := oauth.Config{
		BaseURL: baseURL, CookieName: "of_session",
		CookieSecret: []byte("0123456789abcdef0123456789abcdef"),
	}
	codec, err := oauth.NewCookieCodec(cfg.CookieSecret, cfg.BaseURL.String())
	if err != nil {
		t.Fatal(err)
	}
	session, err := codec.Encode(cfg.CookieName, oauth.Token{AccessToken: "access-token"}, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		cookie, err := r.Cookie(cfg.CookieName)
		if err != nil || cookie.Value != session {
			t.Error("runner did not forward the original signed session cookie")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	runner := &executor.HTTPFunctionRunner{
		Client: upstream.Client(), UpstreamURL: target,
		ReverseProxy: httputil.NewSingleHostReverseProxy(target),
	}
	handler, err := oauth.NewOAuthMiddleware(cfg, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := runner.Run(executor.FunctionRequest{}, r.ContentLength, r, w); err != nil {
			t.Error(err)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	// Exercise the HTTP runner's ordinary and streaming proxy branches.
	loginURL := cfg.BaseURL.JoinPath("/auth/login").String()
	for _, accept := range []string{"application/json", "text/event-stream"} {
		for _, tc := range []struct {
			name  string
			value string
			add   bool
			want  int
		}{
			{name: "valid", value: session, add: true, want: http.StatusNoContent},
			{name: "tampered", value: "tampered", add: true, want: http.StatusSeeOther},
			{name: "missing", add: false, want: http.StatusSeeOther},
		} {
			t.Run(accept+"/"+tc.name, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				req.Header.Set("Accept", accept)
				if tc.add {
					req.AddCookie(&http.Cookie{Name: cfg.CookieName, Value: tc.value})
				}
				res := httptest.NewRecorder()
				handler.ServeHTTP(res, req)
				if res.Code != tc.want {
					t.Fatalf("got %d, want %d", res.Code, tc.want)
				}
				if len(res.Result().Cookies()) != 0 {
					t.Fatal("proxy rewrote the browser cookie")
				}
				if tc.want == http.StatusSeeOther {
					if location := res.Header().Get("Location"); location != loginURL {
						t.Fatalf("redirect to %q, want %q", location, loginURL)
					}
				}
			})
		}
	}
	if calls.Load() != 2 {
		t.Fatal("only valid sessions should reach the upstream")
	}
}
