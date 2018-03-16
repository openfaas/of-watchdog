package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestHealthHandler_StatusOK_LockFilePresent(t *testing.T) {
	rr := httptest.NewRecorder()

	present := lockFilePresent()

	if present == false {
		if err := lock(); err != nil {
			t.Fatal(err)
		}
	}

	req, err := http.NewRequest(http.MethodGet, "/_/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := makeHealthHandler()
	handler(rr, req)

	required := http.StatusOK
	if status := rr.Code; status != required {
		t.Errorf("handler returned wrong status code - want: %v, got: %v", required, status)
	}

}

func TestHealthHandler_StatusInternalServerError_LockFileNotPresent(t *testing.T) {
	rr := httptest.NewRecorder()

	if lockFilePresent() == true {
		if err := removeLockFile(); err != nil {
			t.Fatal(err)
		}
	}

	req, err := http.NewRequest(http.MethodGet, "/_/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	handler := makeHealthHandler()
	handler(rr, req)

	required := http.StatusInternalServerError
	if status := rr.Code; status != required {
		t.Errorf("handler returned wrong status code - want: %v, got: %v", required, status)
	}
}

func TestHealthHandler_StatusMethodNotAllowed_ForWriteableVerbs(t *testing.T) {
	rr := httptest.NewRecorder()

	verbs := []string{http.MethodPost, http.MethodPut, http.MethodDelete}

	for _, verb := range verbs {
		req, err := http.NewRequest(verb, "/_/health", nil)
		if err != nil {
			t.Fatal(err)
		}

		handler := makeHealthHandler()
		handler(rr, req)

		required := http.StatusMethodNotAllowed
		if status := rr.Code; status != required {
			t.Errorf("handler returned wrong status code -  want: %v, got: %v", required, status)
		}
	}
}

func removeLockFile() error {
	path := filepath.Join(os.TempDir(), ".lock")
	removeErr := os.Remove(path)
	return removeErr
}
