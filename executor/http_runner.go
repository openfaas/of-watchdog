package executor

import (
	"context"
	"io"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"sync"
	"time"
)

// HTTPFunctionRunner creates and maintains one process responsible for handling all calls
type HTTPFunctionRunner struct {
	ExecTimeout  time.Duration // ExecTimeout the maxmium duration or an upstream function call
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	Process      string
	ProcessArgs  []string
	Command      *exec.Cmd
	StdinPipe    io.WriteCloser
	StdoutPipe   io.ReadCloser
	Stderr       io.Writer
	Mutex        sync.Mutex
	Client       *http.Client
	UpstreamURL  *url.URL
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

	urlValue, upstreamURLErr := url.Parse(os.Getenv("upstream_url"))
	if upstreamURLErr != nil {
		log.Fatal(upstreamURLErr)
	}

	f.UpstreamURL = urlValue

	return cmd.Start()
}

// Run a function with a long-running process with a HTTP protocol for communication
func (f *HTTPFunctionRunner) Run(req FunctionRequest, contentLength int64, r *http.Request, w http.ResponseWriter) error {

	upstreamURL := f.UpstreamURL.String()

	if len(r.URL.RawQuery) > 0 {
		upstreamURL += "?" + r.URL.RawQuery
	}

	request, _ := http.NewRequest(r.Method, upstreamURL, r.Body)
	for h := range r.Header {
		request.Header.Set(h, r.Header.Get(h))
	}

	request.Host = r.Host
	copyHeaders(request.Header, &r.Header)

	ctx, cancel := context.WithTimeout(context.Background(), f.ExecTimeout)

	defer cancel()

	res, err := f.Client.Do(request.WithContext(ctx))

	if err != nil {
		log.Printf("Upstream HTTP request error: %s\n", err.Error())

		// Error unrelated to context / deadline
		if ctx.Err() == nil {
			w.WriteHeader(http.StatusInternalServerError)

			return nil
		}

		select {
		case <-ctx.Done():
			{
				if ctx.Err() != nil {
					// Error due to timeout / deadline
					log.Printf("Upstream HTTP killed due to exec_timeout: %s\n", f.ExecTimeout)

					w.WriteHeader(http.StatusGatewayTimeout)
					return nil
				}

			}
		}

		w.WriteHeader(http.StatusInternalServerError)
		return err
	}

	copyHeaders(w.Header(), &res.Header)

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
	}

	return &proxyClient
}
