package executor

import (
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
