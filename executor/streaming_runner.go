// Copyright (c) OpenFaaS Author(s) 2021. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package executor

import (
	"context"
	"os"
	"os/exec"
	"time"
)

// StreamingFunctionRunner forks a process for each invocation
type StreamingFunctionRunner struct {
	ExecTimeout   time.Duration
	LogPrefix     bool
	LogBufferSize int
}

// Run run a fork for each invocation
func (f *StreamingFunctionRunner) Run(req FunctionRequest) error {

	var cmd *exec.Cmd
	ctx := context.Background()
	if f.ExecTimeout.Nanoseconds() > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, f.ExecTimeout)
		defer cancel()
	}

	cmd = exec.CommandContext(ctx, req.Process, req.ProcessArgs...)
	if req.InputReader != nil {
		defer req.InputReader.Close()
		cmd.Stdin = req.InputReader
	}

	cmd.Env = req.Environment
	cmd.Stdout = req.OutputWriter

	errPipe, _ := cmd.StderrPipe()

	// Prints stderr to console and is picked up by container logging driver.
	bindLoggingPipe("stderr", errPipe, os.Stderr, f.LogPrefix, f.LogBufferSize)

	if err := cmd.Start(); err != nil {
		return err
	}

	return cmd.Wait()
}
