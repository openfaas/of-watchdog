package main

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openfaas-incubator/of-watchdog/config"
)

func TestSerializingForkHandler_HasCustomHeaderInFunction_WithCGI(t *testing.T) {
	rr := httptest.NewRecorder()

	body := ""
	req, err := http.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req.Header.Add("custom-header", "value")

	if err != nil {
		t.Fatal(err)
	}

	config := config.WatchdogConfig{
		FunctionProcess:  "env",
		InjectCGIHeaders: true,
	}
	handler := makeSerializingForkRequestHandler(config)
	handler(rr, req)

	required := http.StatusOK

	if status := rr.Code; status != required {
		t.Errorf("handler returned wrong status code - got: %v, want: %v",
			status, required)
	}

	read, _ := ioutil.ReadAll(rr.Body)
	val := string(read)
	if !strings.Contains(val, "Http_ContentLength=0") {
		t.Errorf(config.FunctionProcess+" should print: Http_ContentLength=0, got: %s\n", val)
	}
	if !strings.Contains(val, "Http_Custom_Header=value") {
		t.Errorf(config.FunctionProcess+" should print: Http_Custom_Header, got: %s\n", val)
	}
	seconds := rr.Header().Get("X-Duration-Seconds")
	if len(seconds) == 0 {
		t.Errorf(config.FunctionProcess + " should have given a duration as an X-Duration-Seconds header\n")
	}
}

func TestSerializingForkHandler_HasCustomHeaderInFunction_WithBody_WithCGI(t *testing.T) {
	rr := httptest.NewRecorder()

	body := "test"
	req, err := http.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req.Header.Add("custom-header", "value")

	if err != nil {
		t.Fatal(err)
	}

	config := config.WatchdogConfig{
		FunctionProcess:  "env",
		InjectCGIHeaders: true,
	}
	handler := makeSerializingForkRequestHandler(config)
	handler(rr, req)

	required := http.StatusOK

	if status := rr.Code; status != required {
		t.Errorf("handler returned wrong status code - got: %v, want: %v",
			status, required)
	}

	read, _ := ioutil.ReadAll(rr.Body)
	val := string(read)
	if !strings.Contains(val, fmt.Sprintf("Http_ContentLength=%d", len(body))) {
		t.Errorf("'env' should printed: Http_ContentLength=0, got: %s\n", val)
	}
	if !strings.Contains(val, "Http_Custom_Header") {
		t.Errorf("'env' should printed: Http_Custom_Header, got: %s\n", val)
	}

	seconds := rr.Header().Get("X-Duration-Seconds")
	if len(seconds) == 0 {
		t.Errorf("Exec of cat should have given a duration as an X-Duration-Seconds header\n")
	}
}

func TestSerializingForkHandler_HasHostHeaderWhenSet_WithCGI(t *testing.T) {
	rr := httptest.NewRecorder()

	body := "test"
	req, err := http.NewRequest(http.MethodPost, "http://gateway/function", bytes.NewBufferString(body))

	if err != nil {
		t.Fatal(err)
	}

	config := config.WatchdogConfig{
		FunctionProcess:  "env",
		InjectCGIHeaders: true,
	}
	handler := makeSerializingForkRequestHandler(config)
	handler(rr, req)

	required := http.StatusOK

	if status := rr.Code; status != required {
		t.Errorf("handler returned wrong status code - got: %v, want: %v",
			status, required)
	}

	read, _ := ioutil.ReadAll(rr.Body)
	val := string(read)
	if !strings.Contains(val, fmt.Sprintf("Http_Host=%s", req.URL.Host)) {
		t.Errorf("'env' should have printed: Http_Host=0, got: %s\n", val)
	}
}

func TestSerializingForkHandler_HostHeader_Empty_WheNotSet_WithCGI(t *testing.T) {
	rr := httptest.NewRecorder()

	body := "test"
	req, err := http.NewRequest(http.MethodPost, "/function", bytes.NewBufferString(body))

	if err != nil {
		t.Fatal(err)
	}

	config := config.WatchdogConfig{
		FunctionProcess:  "env",
		InjectCGIHeaders: true,
	}
	handler := makeSerializingForkRequestHandler(config)
	handler(rr, req)

	required := http.StatusOK
	if status := rr.Code; status != required {
		t.Errorf("handler returned wrong status code - got: %v, want: %v",
			status, required)
	}

	read, _ := ioutil.ReadAll(rr.Body)
	val := string(read)
	if strings.Contains(val, fmt.Sprintf("Http_Host=%s", req.URL.Host)) {
		t.Errorf("Http_Host should not have been given, but was: %s\n", val)
	}
}

func TestSerializingForkHandler_DoesntHaveCustomHeaderInFunction_WithoutCGI(t *testing.T) {
	rr := httptest.NewRecorder()

	body := ""
	req, err := http.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	req.Header.Add("custom-header", "value")
	if err != nil {
		t.Fatal(err)
	}

	config := config.WatchdogConfig{
		FunctionProcess:  "env",
		InjectCGIHeaders: false,
	}
	handler := makeSerializingForkRequestHandler(config)
	handler(rr, req)

	required := http.StatusOK
	if status := rr.Code; status != required {
		t.Errorf("handler returned wrong status code - got: %v, want: %v",
			status, required)
	}

	read, _ := ioutil.ReadAll(rr.Body)
	val := string(read)
	if strings.Contains(val, "Http_Custom_Header") {
		t.Errorf("'env' should not have printed: Http_Custom_Header, got: %s\n", val)
	}

	seconds := rr.Header().Get("X-Duration-Seconds")
	if len(seconds) == 0 {
		t.Errorf("Exec of cat should have given a duration as an X-Duration-Seconds header\n")
	}
}

func TestSerializingForkHandler_HasXDurationSecondsHeader(t *testing.T) {
	rr := httptest.NewRecorder()

	body := "hello"
	req, err := http.NewRequest(http.MethodPost, "/", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}

	config := config.WatchdogConfig{
		FunctionProcess: "cat",
	}
	handler := makeSerializingForkRequestHandler(config)
	handler(rr, req)

	required := http.StatusOK
	if status := rr.Code; status != required {
		t.Errorf("handler returned wrong status code - got: %v, want: %v",
			status, required)
	}

	seconds := rr.Header().Get("X-Duration-Seconds")
	if len(seconds) == 0 {
		t.Errorf("Exec of " + config.FunctionProcess + " should have given a duration as an X-Duration-Seconds header")
	}
}

func TestSerializingForkHandler_HasFullPathAndQueryInFunction_WithCGI(t *testing.T) {
	rr := httptest.NewRecorder()

	body := ""
	wantPath := "/my/full/path"
	wantQuery := "q=x"
	requestURI := wantPath + "?" + wantQuery
	req, err := http.NewRequest(http.MethodPost, requestURI, bytes.NewBufferString(body))

	if err != nil {
		t.Fatal(err)
	}

	config := config.WatchdogConfig{
		FunctionProcess:  "env",
		InjectCGIHeaders: true,
	}
	handler := makeSerializingForkRequestHandler(config)
	handler(rr, req)

	required := http.StatusOK
	if status := rr.Code; status != required {
		t.Errorf("handler returned wrong status code - got: %v, want: %v",
			status, required)
	}

	read, _ := ioutil.ReadAll(rr.Body)
	val := string(read)
	if !strings.Contains(val, "Http_Path="+wantPath) {
		t.Errorf(config.FunctionProcess+" should print: Http_Path="+wantPath+", got: %s\n", val)
	}

	if !strings.Contains(val, "Http_Query="+wantQuery) {
		t.Errorf(config.FunctionProcess+" should print: Http_Query="+wantQuery+", got: %s\n", val)
	}
}
