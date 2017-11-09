package fprocess

import (
	"io"
	"net/http"
	"os/exec"
)

type Handler interface {
	HandleRun(w http.ResponseWriter, r *http.Request)
	HandleStderr(w http.ResponseWriter, r *http.Request)
}

// FunctionRequest stores request for function execution
type FunctionRequest struct {
	Cmd *exec.Cmd

	InputReader   io.ReadCloser
	OutputWriter  io.Writer
	ErrorWriter   io.Writer
	ErrorReader   io.Reader
	ContentLength *int64

	WaitErr chan error
}
