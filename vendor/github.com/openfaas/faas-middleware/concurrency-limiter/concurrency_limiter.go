package limiter

import (
	"fmt"
	"net/http"
	"sync/atomic"
)

// Limiter is an interface that can be used to check if a limit has been met.
type Limiter interface {
	Met() bool
}

type ConcurrencyLimiter struct {
	backendHTTPHandler http.Handler
	/*
		We keep two counters here in order to make it so that we can know when a request has gone to completed
		in the tests. We could wrap these up in a condvar, so there's no need to spinlock, but that seems overkill
		for testing.

		This is effectively a very fancy semaphore built for optimistic concurrency only, and with spinlocks. If
		you want to add timeouts here / pessimistic concurrency, signaling needs to be added and/or a condvar esque
		sorta thing needs to be done to wake up waiters who are waiting post-spin.

		Otherwise, there's all sorts of futzing in order to make sure that the concurrency limiter handler
		has completed
		The math works on overflow:
			var x, y uint64
			x = (1 << 64 - 1)
			y = (1 << 64 - 1)
			x++
			fmt.Println(x)
			fmt.Println(y)
			fmt.Println(x - y)
		Prints:
			0
			18446744073709551615
			1
	*/
	requestsStarted   uint64
	requestsCompleted uint64

	maxInflightRequests uint64
}

func (cl *ConcurrencyLimiter) Met() bool {
	if cl == nil {
		return false
	}

	// We should not have any ConcurrencyLimiter created with a limit of 0
	// but return early if that's the case.
	if cl.maxInflightRequests == 0 {
		return false
	}

	requestsStarted := atomic.LoadUint64(&cl.requestsStarted)
	completedRequested := atomic.LoadUint64(&cl.requestsCompleted)
	return requestsStarted-completedRequested >= cl.maxInflightRequests
}

func (cl *ConcurrencyLimiter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// We should not have any ConcurrencyLimiter created with a limit of 0
	// but we'll check anyway and return early.
	if cl.maxInflightRequests == 0 {
		cl.backendHTTPHandler.ServeHTTP(w, r)
		return
	}

	requestsStarted := atomic.AddUint64(&cl.requestsStarted, 1)
	completedRequested := atomic.LoadUint64(&cl.requestsCompleted)
	if requestsStarted-completedRequested > cl.maxInflightRequests {
		// This is a failure pathway, and we do not want to block on the write to finish
		atomic.AddUint64(&cl.requestsCompleted, 1)

		// Some APIs only return JSON, since we can interfere here and send a plain/text
		// message, let's do the right thing so that downstream users can consume it.
		w.Header().Add("Content-Type", "text/plain")
		w.Header().Add("X-OpenFaaS-Internal", "faas-middleware")

		w.WriteHeader(http.StatusTooManyRequests)

		fmt.Fprintf(w, "Concurrent request limit exceeded. Max concurrent requests: %d\n", cl.maxInflightRequests)
		return
	}

	cl.backendHTTPHandler.ServeHTTP(w, r)
	atomic.AddUint64(&cl.requestsCompleted, 1)
}

// NewConcurrencyLimiter creates NewConcurrencyLimiter with a Handler() function that returns a
// handler which limits the active number of active, concurrent requests.
//
// If the concurrency limit is less than, or equal to 0, then it will just return the handler
// passed to it.
//
// The Met() function will return true if the concurrency limit is exceeded within the handler
// at the time of the call.
func NewConcurrencyLimiter(handler http.Handler, concurrencyLimit int) *ConcurrencyLimiter {
	return &ConcurrencyLimiter{
		backendHTTPHandler:  handler,
		maxInflightRequests: uint64(concurrencyLimit),
	}
}

func (cl *ConcurrencyLimiter) Handler() http.Handler {
	return cl
}
