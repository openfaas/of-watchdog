package fork

import (
	"fmt"
	"log"
	"time"

	"github.com/openfaas-incubator/of-watchdog/fprocess"
)

// ForkFunctionRunner forks a process for each invocation
type ForkFunctionRunner struct {
	HardTimeout time.Duration
}

// Run run a fork for each invocation
func (f *ForkFunctionRunner) Run(req *fprocess.FunctionRequest) error {
	log.Printf("Running %s", req.Cmd.Args[0])

	req.Cmd.Stdin = req.InputReader
	req.Cmd.Stdout = req.OutputWriter
	req.Cmd.Stderr = req.ErrorWriter

	if err := <-req.WaitErr; err != nil {
		return err
	}

	start := time.Now()
	defer func() {
		log.Printf("Took %f secs", time.Since(start).Seconds())
	}()

	if err := req.Cmd.Start(); err != nil {
		return err
	}

	if f.HardTimeout > 0 {
		timer := time.AfterFunc(f.HardTimeout, func() {
			fmt.Printf("Function was killed by HardTimeout: %d\n", f.HardTimeout)
			killErr := req.Cmd.Process.Kill()
			if killErr != nil {
				fmt.Println("Error killing function due to HardTimeout", killErr)
			}
		})
		defer timer.Stop()
	}

	return req.Cmd.Wait()
}
