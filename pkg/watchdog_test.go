package pkg

import (
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openfaas/faas-middleware/oauth"
	"github.com/openfaas/of-watchdog/executor"
)

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
	for _, accept := range []string{"application/json", "text/event-stream"} {
		for _, valid := range []bool{true, false} {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.Header.Set("Accept", accept)
			value, want := session, http.StatusNoContent
			if !valid {
				value, want = "tampered", http.StatusUnauthorized
			}
			req.AddCookie(&http.Cookie{Name: cfg.CookieName, Value: value})
			res := httptest.NewRecorder()
			handler.ServeHTTP(res, req)
			if res.Code != want {
				t.Fatalf("accept=%s valid=%v: got %d, want %d", accept, valid, res.Code, want)
			}
			if len(res.Result().Cookies()) != 0 {
				t.Fatal("proxy rewrote the browser cookie")
			}
		}
	}
	if calls.Load() != 2 {
		t.Fatal("only valid sessions should reach the upstream")
	}
}
