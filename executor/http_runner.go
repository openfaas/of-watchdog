// Copyright (c) OpenFaaS Author(s) 2021. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	units "github.com/docker/go-units"
	fhttputil "github.com/openfaas/faas-provider/httputil"
)

// HTTPFunctionRunner creates and maintains one process responsible for handling all calls
type HTTPFunctionRunner struct {
	ExecTimeout    time.Duration // ExecTimeout the maximum duration or an upstream function call
	ReadTimeout    time.Duration // ReadTimeout for HTTP server
	WriteTimeout   time.Duration // WriteTimeout for HTTP Server
	Process        string        // Process to run as fprocess
	ProcessArgs    []string      // ProcessArgs to pass to command
	Command        *exec.Cmd
	StdinPipe      io.WriteCloser
	StdoutPipe     io.ReadCloser
	Client         *http.Client
	UpstreamURL    *url.URL
	BufferHTTPBody bool
	LogPrefix      bool
	LogBufferSize  int
	LogCallId      bool
	ReverseProxy   *httputil.ReverseProxy
}

// Start forks the process used for processing incoming requests
func (f *HTTPFunctionRunner) Start() error {
	cmd := exec.Command(f.Process, f.ProcessArgs...)

	var stdinErr error
	var stdoutErr error

	f.Command = cmd
	f.StdinPipe, stdinErr = cmd.StdinPipe()
	if stdinErr != nil {
		return stdinErr
	}

	f.StdoutPipe, stdoutErr = cmd.StdoutPipe()
	if stdoutErr != nil {
		return stdoutErr
	}

	errPipe, _ := cmd.StderrPipe()

	// Logs lines from stderr and stdout to the stderr and stdout of this process
	bindLoggingPipe("stderr", errPipe, os.Stderr, f.LogPrefix, f.LogBufferSize)
	bindLoggingPipe("stdout", f.StdoutPipe, os.Stdout, f.LogPrefix, f.LogBufferSize)

	f.Client = makeProxyClient(f.ExecTimeout)

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM)

		<-sig
		cmd.Process.Signal(syscall.SIGTERM)
	}()

	err := cmd.Start()
	go func() {
		err := cmd.Wait()
		if err != nil {
			log.Fatalf("Forked function has terminated: %s", err.Error())
		}
	}()

	return err
}

// Run a function with a long-running process with a HTTP protocol for communication
func (f *HTTPFunctionRunner) Run(req FunctionRequest, contentLength int64, r *http.Request, w http.ResponseWriter) error {
	startedTime := time.Now()

	upstreamURL := f.UpstreamURL.String()

	if len(r.RequestURI) > 0 {
		upstreamURL += r.RequestURI
	}

	body := r.Body

	if f.BufferHTTPBody {
		reqBody, _ := io.ReadAll(r.Body)
		body = io.NopCloser(bytes.NewReader(reqBody))
	}

	request, err := http.NewRequest(r.Method, upstreamURL, body)
	if err != nil {
		return err
	}

	for h := range r.Header {
		request.Header.Set(h, r.Header.Get(h))
	}

	request.Host = r.Host
	copyHeaders(request.Header, &r.Header)

	execTimeout := getTimeout(r, f.ExecTimeout)

	var reqCtx context.Context
	var cancel context.CancelFunc

	if execTimeout.Nanoseconds() > 0 {
		reqCtx, cancel = context.WithTimeout(r.Context(), execTimeout)
	} else {
		reqCtx = r.Context()
		cancel = func() {
		}
	}
	defer cancel()

	if strings.HasPrefix(r.Header.Get("Accept"), "text/event-stream") ||
		r.Header.Get("Upgrade") == "websocket" {
		ww := fhttputil.NewHttpWriteInterceptor(w)

		f.ReverseProxy.ServeHTTP(w, r)
		done := time.Since(startedTime)

		log.Printf("%s %s - %d - Bytes: %s (%.4fs)", r.Method, r.RequestURI, ww.Status(), units.HumanSize(float64(ww.BytesWritten())), done.Seconds())
	} else {

		res, err := f.Client.Do(request.WithContext(reqCtx))
		if err != nil {
			log.Printf("Upstream HTTP request error: %s\n", err.Error())

			// Error unrelated to context / deadline
			if reqCtx.Err() == nil {
				w.Header().Set("X-Duration-Seconds", fmt.Sprintf("%f", time.Since(startedTime).Seconds()))
				w.Header().Add("X-OpenFaaS-Internal", "of-watchdog")

				w.WriteHeader(http.StatusInternalServerError)

				return nil
			}

			<-reqCtx.Done()

			if reqCtx.Err() != nil {
				// Error due to timeout / deadline
				log.Printf("Upstream HTTP killed due to exec_timeout: %s\n", f.ExecTimeout)
				w.Header().Set("X-Duration-Seconds", fmt.Sprintf("%f", time.Since(startedTime).Seconds()))
				w.Header().Add("X-OpenFaaS-Internal", "of-watchdog")

				w.WriteHeader(http.StatusGatewayTimeout)
				return nil
			}

			w.Header().Set("X-Duration-Seconds", fmt.Sprintf("%f", time.Since(startedTime).Seconds()))
			w.Header().Add("X-OpenFaaS-Internal", "of-watchdog")

			w.WriteHeader(http.StatusInternalServerError)
			return err
		}

		copyHeaders(w.Header(), &res.Header)
		done := time.Since(startedTime)

		w.Header().Set("X-Duration-Seconds", fmt.Sprintf("%f", done.Seconds()))

		w.WriteHeader(res.StatusCode)
		if res.Body != nil {
			defer res.Body.Close()

			if _, err := io.Copy(w, res.Body); err != nil {
				log.Printf("Error copying response body: %s", err)
			}
		}

		// Exclude logging for health check probes from the kubelet which can spam
		// log collection systems.
		if !strings.HasPrefix(r.UserAgent(), "kube-probe") {
			if f.LogCallId {
				callId := r.Header.Get("X-Call-Id")
				if callId == "" {
					callId = "none"
				}

				log.Printf("%s %s - %s - ContentLength: %s (%.4fs) [%s]", r.Method, r.RequestURI, res.Status, units.HumanSize(float64(res.ContentLength)), done.Seconds(), callId)
			} else {
				log.Printf("%s %s - %s - ContentLength: %s (%.4fs)", r.Method, r.RequestURI, res.Status, units.HumanSize(float64(res.ContentLength)), done.Seconds())
			}
		}
	}

	return nil
}

func getTimeout(r *http.Request, defaultTimeout time.Duration) time.Duration {
	execTimeout := defaultTimeout
	if v := r.Header.Get("X-Timeout"); len(v) > 0 {
		dur, err := time.ParseDuration(v)
		if err == nil {
			if dur <= defaultTimeout {
				execTimeout = dur
			}
		}
	}

	return execTimeout
}

func copyHeaders(destination http.Header, source *http.Header) {
	for k, v := range *source {
		vClone := make([]string, len(v))
		copy(vClone, v)
		(destination)[k] = vClone
	}
}

func makeProxyClient(dialTimeout time.Duration) *http.Client {
	proxyClient := http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   dialTimeout,
				KeepAlive: 10 * time.Second,
			}).DialContext,
			MaxIdleConns:          100,
			MaxIdleConnsPerHost:   100,
			DisableKeepAlives:     false,
			IdleConnTimeout:       500 * time.Millisecond,
			ExpectContinueTimeout: 1500 * time.Millisecond,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	return &proxyClient
}
