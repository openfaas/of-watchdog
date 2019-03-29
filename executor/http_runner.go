package executor

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
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
	Stderr         io.Writer
	Client         *http.Client
	UpstreamURL    *url.URL
	BufferHTTPBody bool
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

	// Prints stderr to console and is picked up by container logging driver.
	go func() {
		log.Println("Started logging stderr from function.")
		for {
			errBuff := make([]byte, 256)

			_, err := errPipe.Read(errBuff)
			if err != nil {
				log.Fatalf("Error reading stderr: %s", err)

			} else {
				log.Printf("stderr: %s", errBuff)
			}
		}
	}()

	go func() {
		log.Println("Started logging stdout from function.")
		for {
			errBuff := make([]byte, 256)

			_, err := f.StdoutPipe.Read(errBuff)
			if err != nil {
				log.Fatalf("Error reading stdout: %s", err)

			} else {
				log.Printf("stdout: %s", errBuff)
			}
		}
	}()

	f.Client = makeProxyClient(f.ExecTimeout)

	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM)

		<-sig
		cmd.Process.Signal(syscall.SIGTERM)

	}()

	return cmd.Start()
}

// Run a function with a long-running process with a HTTP protocol for communication
func (f *HTTPFunctionRunner) Run(req FunctionRequest, contentLength int64, r *http.Request, w http.ResponseWriter) error {
	startedTime := time.Now()

	upstreamURL := f.UpstreamURL.String()

	if len(r.RequestURI) > 0 {
		upstreamURL += r.RequestURI
	}

	var body io.Reader
	if f.BufferHTTPBody {
		reqBody, _ := ioutil.ReadAll(r.Body)
		body = bytes.NewReader(reqBody)
	} else {
		body = r.Body
	}

	request, _ := http.NewRequest(r.Method, upstreamURL, body)
	for h := range r.Header {
		request.Header.Set(h, r.Header.Get(h))
	}

	request.Host = r.Host
	copyHeaders(request.Header, &r.Header)

	var reqCtx context.Context
	var cancel context.CancelFunc

	if f.ExecTimeout.Nanoseconds() > 0 {
		reqCtx, cancel = context.WithTimeout(context.Background(), f.ExecTimeout)
	} else {
		reqCtx = context.Background()
		cancel = func() {

		}
	}

	defer cancel()

	res, err := f.Client.Do(request.WithContext(reqCtx))

	if err != nil {
		log.Printf("Upstream HTTP request error: %s\n", err.Error())

		// Error unrelated to context / deadline
		if reqCtx.Err() == nil {
			w.Header().Set("X-Duration-Seconds", fmt.Sprintf("%f", time.Since(startedTime).Seconds()))

			w.WriteHeader(http.StatusInternalServerError)

			return nil
		}

		select {
		case <-reqCtx.Done():
			{
				if reqCtx.Err() != nil {
					// Error due to timeout / deadline
					log.Printf("Upstream HTTP killed due to exec_timeout: %s\n", f.ExecTimeout)
					w.Header().Set("X-Duration-Seconds", fmt.Sprintf("%f", time.Since(startedTime).Seconds()))

					w.WriteHeader(http.StatusGatewayTimeout)
					return nil
				}

			}
		}

		w.Header().Set("X-Duration-Seconds", fmt.Sprintf("%f", time.Since(startedTime).Seconds()))
		w.WriteHeader(http.StatusInternalServerError)
		return err
	}

	copyHeaders(w.Header(), &res.Header)

	w.Header().Set("X-Duration-Seconds", fmt.Sprintf("%f", time.Since(startedTime).Seconds()))

	w.WriteHeader(res.StatusCode)
	if res.Body != nil {
		defer res.Body.Close()

		bodyBytes, bodyErr := ioutil.ReadAll(res.Body)
		if bodyErr != nil {
			log.Println("read body err", bodyErr)
		}
		w.Write(bodyBytes)
	}

	log.Printf("%s %s - %s - ContentLength: %d", r.Method, r.RequestURI, res.Status, res.ContentLength)

	return nil
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
