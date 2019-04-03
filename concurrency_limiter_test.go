package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeHandler struct {
	ctx                         context.Context
	completeInFlightRequestChan chan struct{}
}

func (f *fakeHandler) ServerHTTP(w http.ResponseWriter, r *http.Request) {

	select {
	case <-f.ctx.Done():
		w.WriteHeader(http.StatusServiceUnavailable)
	case <-f.completeInFlightRequestChan:
		w.WriteHeader(http.StatusOK)
	}

}

func doRRandRequest(ctx context.Context, t *testing.T, wg *sync.WaitGroup, handler http.Handler, fh *fakeHandler) *httptest.ResponseRecorder {
	// If wait for handler is true, it waits until the code is in the handler function
	rr := httptest.NewRecorder()
	req, err := http.NewRequest("GET", "/", nil)
	if err != nil {
		t.Fatal(err)
	}
	req = req.WithContext(ctx)

	wg.Add(1)
	go func() {
		// If this code path is meant to make it into the handler, we need a way to figure out if it's there or not
		handler.ServeHTTP(rr, req)
		// If the request was aborted, unblock any waiting goroutines
		wg.Done()
	}()

	return rr
}

func TestConcurrencyLimitUnderLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	handler := &fakeHandler{ctx: ctx, completeInFlightRequestChan: make(chan struct{})}
	cl := &concurrencyLimiter{
		handler:          handler.ServerHTTP,
		concurrencyLimit: 2,
	}

	wg := &sync.WaitGroup{}
	rr1 := doRRandRequest(ctx, t, wg, cl, handler)
	// This will "release" the request rr1
	handler.completeInFlightRequestChan <- struct{}{}

	// This should never take more than the timeout
	wg.Wait()

	// We want to access the response recorder directly, so we don't accidentally get an implicitly correct answer
	if rr1.Code != http.StatusOK {
		t.Fatalf("Received response code: %s", http.StatusText(rr1.Code))
	}
}

func TestConcurrencyLimitAtLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	handler := &fakeHandler{ctx: ctx, completeInFlightRequestChan: make(chan struct{})}

	cl := &concurrencyLimiter{
		handler:          handler.ServerHTTP,
		concurrencyLimit: 2,
	}

	wg := &sync.WaitGroup{}
	rr1 := doRRandRequest(ctx, t, wg, cl, handler)
	rr2 := doRRandRequest(ctx, t, wg, cl, handler)

	handler.completeInFlightRequestChan <- struct{}{}
	handler.completeInFlightRequestChan <- struct{}{}

	wg.Wait()

	if rr1.Code != http.StatusOK {
		t.Fatalf("Received response code: %s", http.StatusText(rr1.Code))
	}
	if rr2.Code != http.StatusOK {
		t.Fatalf("Received response code: %s", http.StatusText(rr2.Code))
	}
}

func count(r *httptest.ResponseRecorder, code200s, code429s *int) {
	switch r.Code {
	case http.StatusTooManyRequests:
		*code429s = *code429s + 1
	case http.StatusOK:
		*code200s = *code200s + 1
	default:
		panic(fmt.Sprintf("Unknown code: %d", r.Code))
	}
}

func TestConcurrencyLimitOverLimit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	handler := &fakeHandler{ctx: ctx, completeInFlightRequestChan: make(chan struct{}, 3)}
	cl := &concurrencyLimiter{
		handler:          handler.ServerHTTP,
		concurrencyLimit: 2,
	}

	wg := &sync.WaitGroup{}

	rr1 := doRRandRequest(ctx, t, wg, cl, handler)
	rr2 := doRRandRequest(ctx, t, wg, cl, handler)
	for ctx.Err() == nil {
		if requestsStarted := atomic.LoadUint64(&cl.requestsStarted); requestsStarted == 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	rr3 := doRRandRequest(ctx, t, wg, cl, handler)
	for ctx.Err() == nil {
		if requestsStarted := atomic.LoadUint64(&cl.requestsStarted); requestsStarted == 3 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	handler.completeInFlightRequestChan <- struct{}{}
	handler.completeInFlightRequestChan <- struct{}{}
	handler.completeInFlightRequestChan <- struct{}{}

	wg.Wait()

	code200s := 0
	code429s := 0
	count(rr1, &code200s, &code429s)
	count(rr2, &code200s, &code429s)
	count(rr3, &code200s, &code429s)
	if code200s != 2 || code429s != 1 {
		t.Fatalf("code 200s: %d, and code429s: %d", code200s, code429s)
	}
}

func TestConcurrencyLimitOverLimitAndRecover(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	handler := &fakeHandler{ctx: ctx, completeInFlightRequestChan: make(chan struct{}, 3)}
	cl := &concurrencyLimiter{
		handler:          handler.ServerHTTP,
		concurrencyLimit: 2,
	}

	wg := &sync.WaitGroup{}

	rr1 := doRRandRequest(ctx, t, wg, cl, handler)
	rr2 := doRRandRequest(ctx, t, wg, cl, handler)
	for ctx.Err() == nil {
		if requestsStarted := atomic.LoadUint64(&cl.requestsStarted); requestsStarted == 2 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	// This will 429
	rr3 := doRRandRequest(ctx, t, wg, cl, handler)
	for ctx.Err() == nil {
		if requestsStarted := atomic.LoadUint64(&cl.requestsStarted); requestsStarted == 3 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	handler.completeInFlightRequestChan <- struct{}{}
	handler.completeInFlightRequestChan <- struct{}{}
	handler.completeInFlightRequestChan <- struct{}{}
	// Although we could do another wg.Wait here, I don't think we should because
	// it might provide a false sense of confidence
	for ctx.Err() == nil {
		if requestsCompleted := atomic.LoadUint64(&cl.requestsCompleted); requestsCompleted == 3 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	rr4 := doRRandRequest(ctx, t, wg, cl, handler)
	handler.completeInFlightRequestChan <- struct{}{}
	wg.Wait()

	code200s := 0
	code429s := 0
	count(rr1, &code200s, &code429s)
	count(rr2, &code200s, &code429s)
	count(rr3, &code200s, &code429s)
	count(rr4, &code200s, &code429s)

	if code200s != 3 || code429s != 1 {
		t.Fatalf("code 200s: %d, and code429s: %d", code200s, code429s)
	}
}
