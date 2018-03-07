package executor

import (
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
	Process     string
	ProcessArgs []string
	Command     *exec.Cmd
	StdinPipe   io.WriteCloser
	StdoutPipe  io.ReadCloser
	Stderr      io.Writer
	Mutex       sync.Mutex
	Client      *http.Client
	UpstreamURL *url.URL
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

	dialTimeout := 3 * time.Second
	f.Client = makeProxyClient(dialTimeout)

	urlValue, upstreamURLErr := url.Parse(os.Getenv("upstream_url"))
	if upstreamURLErr != nil {
		log.Fatal(upstreamURLErr)
	}

	f.UpstreamURL = urlValue

	return cmd.Start()
}

// Run a function with a long-running process with a HTTP protocol for communication
func (f *HTTPFunctionRunner) Run(req FunctionRequest, contentLength int64, r *http.Request, w http.ResponseWriter) error {

	request, _ := http.NewRequest(r.Method, f.UpstreamURL.String(), r.Body)
	for h := range r.Header {
		request.Header.Set(h, r.Header.Get(h))
	}

	res, err := f.Client.Do(request)

	if err != nil {
		log.Println(err)
	}

	for h := range res.Header {
		w.Header().Set(h, res.Header.Get(h))
	}

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
