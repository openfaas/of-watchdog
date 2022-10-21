package main

import (
	"log"
	"net/http"
	"net/url"
	"sync/atomic"

	limiter "github.com/openfaas/faas-middleware/concurrency-limiter"
)

type readiness struct {
	// functionHandler is the function invoke HTTP Handler. Using this allows
	// custom ready checks in all invoke modes. For example, in forking mode
	// the handler implementation (a bash script) can check the path in the env
	// and respond accordingly, exit non-zero when not ready.
	functionHandler http.Handler
	endpoint        string
	lockCheck       func() bool
	limiter         limiter.Limiter
}

// LimitMet returns true if the concurrency limit has been reached
// or false if no limiter has been used
func (r *readiness) LimitMet() bool {
	if r.limiter == nil {
		return false
	}
	return r.limiter.Met()
}

func (r *readiness) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	switch req.Method {
	case http.MethodGet:
		status := http.StatusOK

		switch {
		case atomic.LoadInt32(&acceptingConnections) == 0, !r.lockCheck():
			status = http.StatusServiceUnavailable
		case r.LimitMet():
			status = http.StatusTooManyRequests
		case r.endpoint != "":
			upstream := url.URL{
				Scheme: req.URL.Scheme,
				Host:   req.URL.Host,
				Path:   r.endpoint,
			}

			readyReq, err := http.NewRequestWithContext(req.Context(), http.MethodGet, upstream.String(), nil)
			if err != nil {
				log.Printf("Error creating readiness request to: %s : %s", upstream.String(), err)
				status = http.StatusInternalServerError
				break
			}

			// we need to set the raw RequestURI for the function invoker to see our URL path,
			// otherwise it will just route to `/`, typically this shouldn't be used or set
			readyReq.RequestURI = r.endpoint
			readyReq.Header = req.Header.Clone()

			// Instead of calling http.DefaultClient.Do(), which only works with http mode
			// calling this handler can fork a process to run a request, such as when
			// using bash as the function.
			r.functionHandler.ServeHTTP(w, readyReq)
			return
		}

		w.WriteHeader(status)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
