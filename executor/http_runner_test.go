package executor

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGetTimeout_PicksLowerOverride(t *testing.T) {
	override := time.Second * 10
	r := httptest.NewRequest("GET", "http://localhost:8080", nil)
	r.Header.Add("X-Timeout", override.Round(time.Second).String())

	defaultTimeout := time.Second * 20
	got := getTimeout(r, defaultTimeout)
	want := time.Second * 10

	if got != want {
		t.Errorf("getTimeout() got: %v, want %v", got, want)
	}
}

func TestGetTimeout_CapsOverrideToDefaultValue(t *testing.T) {
	override := time.Second * 21
	r := httptest.NewRequest("GET", "http://localhost:8080", nil)
	r.Header.Add("X-Timeout", override.Round(time.Second).String())

	defaultTimeout := time.Second * 20
	got := getTimeout(r, defaultTimeout)
	want := time.Second * 20

	if got != want {
		t.Errorf("getTimeout() got: %v, want %v", got, want)
	}
}

func TestGetTimeout_NoDefaultMeansNoOverride(t *testing.T) {
	override := time.Second * 10
	r := httptest.NewRequest("GET", "http://localhost:8080", nil)
	r.Header.Add("X-Timeout", override.Round(time.Second).String())

	defaultTimeout := time.Nanosecond * 0
	got := getTimeout(r, defaultTimeout)
	want := time.Nanosecond * 0

	if got != want {
		t.Errorf("getTimeout() got: %v, want %v", got, want)
	}
}

func Test_requiresStdlibProxy(t *testing.T) {
	testCases := []struct {
		name    string
		headers map[string]string
		want    bool
	}{
		{
			name:    "SSE request",
			headers: map[string]string{"Accept": "text/event-stream"},
			want:    true,
		},
		{
			name:    "SSE request with multiple accept values",
			headers: map[string]string{"Accept": "application/json, text/event-stream;q=0.9, text/plain"},
			want:    true,
		},
		{
			name:    "NDJSON request",
			headers: map[string]string{"Accept": "application/x-ndjson"},
			want:    true,
		},
		{
			name:    "NDJSON request with multiple accept values",
			headers: map[string]string{"Accept": "text/plain, application/x-ndjson;q=0.9, application/json;q=0.8"},
			want:    true,
		},
		{
			name:    "WebSocket request",
			headers: map[string]string{"Upgrade": "websocket"},
			want:    true,
		},
		{
			name:    "Regular JSON request",
			headers: map[string]string{"Accept": "application/json"},
			want:    false,
		},
		{
			name:    "Regular request with multiple values",
			headers: map[string]string{"Accept": "text/plain, application/json;q=0.9"},
			want:    false,
		},
		{
			name:    "Request without headers",
			headers: map[string]string{},
			want:    false,
		},
		{
			name:    "Request with non-websocket Upgrade header",
			headers: map[string]string{"Accept": "application/json", "Upgrade": "h2c"},
			want:    false,
		},

		{
			name:    "Case insensitive headers",
			headers: map[string]string{"Accept": "APPLICATION/X-NDJSON"},
			want:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", "/test", nil)
			if err != nil {
				t.Fatal(err)
			}

			// Set headers from test case
			for key, value := range tc.headers {
				req.Header.Set(key, value)
			}

			got := requiresStdlibProxy(req)

			if got != tc.want {
				t.Errorf("Want %t, got %t", tc.want, got)
			}
		})
	}
}
