package executor

import (
	"fmt"
	"io"
	"log"
	"os/exec"
	"time"
)

// FunctionRunner runs a function
type FunctionRunner interface {
	Run(f FunctionRequest) error
}

// FunctionRequest stores request for function execution
type FunctionRequest struct {
	Process     string
	ProcessArgs []string
	Environment []string

	InputReader   io.ReadCloser
	OutputWriter  io.Writer
	ContentLength *int64
}

// StreamingForkFunctionRunner forks a process for each invocation
type StreamingForkFunctionRunner struct {
	ExecTimeout           time.Duration
	StderrBufferSizeBytes int
}

// Run run a fork for each invocation
func (f *StreamingForkFunctionRunner) Run(req FunctionRequest) error {

	cmd := exec.Command(req.Process, req.ProcessArgs...)
	cmd.Env = req.Environment

	var cancel *time.Timer

	if f.ExecTimeout > time.Millisecond*0 {
		cancel = time.NewTimer(f.ExecTimeout)

		go func() {
			<-cancel.C

			log.Printf("Function was killed by ExecTimeout: %s\n", f.ExecTimeout.String())
			killErr := cmd.Process.Kill()
			if killErr != nil {
				fmt.Println("Error killing function due to ExecTimeout", killErr)
			}
		}()
	}

	if cancel != nil {
		defer cancel.Stop()
	}

	if req.InputReader != nil {
		defer req.InputReader.Close()
		cmd.Stdin = req.InputReader
	}

	cmd.Stdout = req.OutputWriter

	errPipe, getErrPipe := cmd.StderrPipe()

	if getErrPipe != nil {
		log.Println("getErrPipe - ", getErrPipe)
	}

	// Prints stderr to console and is picked up by container logging driver.
	go func() {
		// log.Println("Started logging stderr from function.")
		for {
			errBuff := make([]byte, f.StderrBufferSizeBytes)

			n, err := errPipe.Read(errBuff)
			if err != nil {
				if err != io.EOF {
					log.Printf("Error reading stderr: %s", err)
				}
				break
			} else {
				if n > 0 {
					log.Printf("stderr: %s", errBuff)
				}
			}
		}
	}()

	startErr := cmd.Start()

	if startErr != nil {
		return fmt.Errorf("startErr: %s", startErr)
	}

	if cancel != nil {
		cancel.Stop()
	}

	req.InputReader.Close()

	_, waitErr := cmd.Process.Wait()

	return waitErr
}
