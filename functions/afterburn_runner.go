package functions

import (
	"bufio"
	"io"
	"log"
	"net/http"
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

	return cmd.Start()
}

// Run a function with a long-running process with a HTTP protocol for communication
func (f *AfterBurnFunctionRunner) Run(req FunctionRequest, contentLength int64, r *http.Request, w http.ResponseWriter) error {
	buffReader := bufio.NewReader(f.StdoutPipe)

	writeErr := r.Write(f.StdinPipe)
	if writeErr != nil {
		return writeErr
	}

	processRes, err := http.ReadResponse(buffReader, r)
	if err != nil {
		return err
	}

	if processRes.Body != nil {
		defer processRes.Body.Close()
	}

	w.WriteHeader(processRes.StatusCode)
	processRes.Write(w)

	log.Printf("%s %s - %s - ContentLength: %d\n", r.Method, r.RequestURI, processRes.Status, processRes.ContentLength)

	return nil
}
