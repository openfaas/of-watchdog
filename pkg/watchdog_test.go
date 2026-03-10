package pkg

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
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
