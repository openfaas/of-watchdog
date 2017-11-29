package functions

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

// ForkFunctionRunner forks a process for each invocation
type ForkFunctionRunner struct {
	HardTimeout time.Duration
}

// Run run a fork for each invocation
func (f *ForkFunctionRunner) Run(req FunctionRequest) error {
	log.Printf("Running %s", req.Process)
	start := time.Now()
	cmd := exec.Command(req.Process, req.ProcessArgs...)
	cmd.Env = req.Environment

	var timer *time.Timer
	if f.HardTimeout > time.Millisecond*0 {
		timer = time.NewTimer(f.HardTimeout)

		go func() {
			<-timer.C

			fmt.Printf("Function was killed by HardTimeout: %d\n", f.HardTimeout)
			killErr := cmd.Process.Kill()
			if killErr != nil {
				fmt.Println("Error killing function due to HardTimeout", killErr)
			}
		}()
	}

	if req.InputReader != nil {
		defer req.InputReader.Close()
		cmd.Stdin = req.InputReader
	}

	cmd.Stdout = req.OutputWriter

	errPipe, _ := cmd.StderrPipe()

	// Prints stderr to console and is picked up by container logging driver.
	go func() {
		log.Println("Started logging stderr from function.")
		for {
			errBuff := make([]byte, 256)

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
		return startErr
	}

	waitErr := cmd.Wait()
	done := time.Since(start)
	log.Printf("Took %f secs", done.Seconds())
	if timer != nil {
		timer.Stop()
	}

	req.InputReader.Close()

	if waitErr != nil {
		return waitErr
	}

	return nil
}
