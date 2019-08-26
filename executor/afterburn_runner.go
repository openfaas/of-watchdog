package executor

import (
	"bufio"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/exec"
	"sync"
)

// AfterBurnFunctionRunner creates and maintains one process responsible for handling all calls
type AfterBurnFunctionRunner struct {
	Process     string
	ProcessArgs []string
	Command     *exec.Cmd
	StdinPipe   io.WriteCloser
	StdoutPipe  io.ReadCloser
	Stderr      io.Writer
	Mutex       sync.Mutex
}

// Start forks the process used for processing incoming requests
func (f *AfterBurnFunctionRunner) Start() error {
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
	bindLoggingPipe("stderr", errPipe, os.Stderr)

	return cmd.Start()
}

// Run a function with a long-running process with a HTTP protocol for communication
func (f *AfterBurnFunctionRunner) Run(req FunctionRequest, contentLength int64, r *http.Request, w http.ResponseWriter) error {

	// Submit body to function via stdin
	writeErr := r.Write(f.StdinPipe)

	if writeErr != nil {
		return writeErr
	}

	var processRes *http.Response

	// Read response back from stdout
	buffReader := bufio.NewReader(f.StdoutPipe)
	var err1 error
	processRes, err1 = http.ReadResponse(buffReader, r)
	if err1 != nil {
		return err1
	}

	for h := range processRes.Header {
		w.Header().Set(h, processRes.Header.Get(h))
	}

	w.WriteHeader(processRes.StatusCode)
	if processRes.Body != nil {
		defer processRes.Body.Close()
		bodyBytes, bodyErr := ioutil.ReadAll(processRes.Body)
		if bodyErr != nil {
			log.Println("read body err", bodyErr)
		}

		// processRes.Write(w)
		w.Write(bodyBytes)
	}

	if processRes != nil {
		log.Printf("%s %s - %s - ContentLength: %d\n", r.Method, r.RequestURI, processRes.Status, processRes.ContentLength)
	}

	return nil
}
