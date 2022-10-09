package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestReadinessHandler(t *testing.T) {
	cases := []struct {
		name                 string
		endpoint             string
		limitMet             bool
		acceptingConnections int32
		readyResponseCode    int
		expectedCode         int
	}{
		{
			name:                 "return 503 when not accepting connections",
			acceptingConnections: 0,
			expectedCode:         http.StatusServiceUnavailable,
		},
		{
			name:                 "returns 200 when no upstream endpoint and no limiter",
			acceptingConnections: 1,
			expectedCode:         http.StatusOK,
		},
		{
			name:                 "returns the upstream endpoint response code when no limiter",
			acceptingConnections: 1,
			endpoint:             "/custom/ready",
			readyResponseCode:    http.StatusNoContent,
			expectedCode:         http.StatusNoContent,
		},
		{
			name:                 "return 429 when limiter is met",
			limitMet:             true,
			acceptingConnections: 1,
			expectedCode:         http.StatusTooManyRequests,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			upstream := testUpstreamHandler(tc.endpoint, tc.readyResponseCode)
			handler := &readiness{
				functionHandler: upstream,
				endpoint:        tc.endpoint,
				lockCheck:       func() bool { return true },
				limiter:         &testLimiter{met: tc.limitMet},
			}

			rr := httptest.NewRecorder()
			req, err := http.NewRequest(http.MethodGet, "/_/ready", nil)
			if err != nil {
				t.Fatal(err)
			}

			acceptingConnections = tc.acceptingConnections
			handler.ServeHTTP(rr, req)

			if status := rr.Code; status != tc.expectedCode {
				t.Errorf("handler returned wrong status code - want: %v, got: %v", tc.expectedCode, status)
			}
		})
	}
}

func testUpstreamHandler(endpoint string, status int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != endpoint {
			w.WriteHeader(http.StatusNotFound)
			return
		}

		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		w.WriteHeader(status)
	})
}

type testLimiter struct {
	met bool
}

func (t *testLimiter) Met() bool {
	if t == nil {
		return false
	}
	return t.met
}
