// Copyright (c) OpenFaaS Author(s) 2021. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package executor

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os/exec"
	"sync"
	"time"
)

// SerializingForkFunctionRunner forks a process for each invocation
type SerializingForkFunctionRunner struct {
	ExecTimeout time.Duration
	LogPrefix   bool
}

// Run run a fork for each invocation
func (f *SerializingForkFunctionRunner) Run(req FunctionRequest, w http.ResponseWriter) error {
	start := time.Now()
	body, err := serializeFunction(req, f)
	if err != nil {
		w.Header().Set("X-Duration-Seconds", fmt.Sprintf("%f", time.Since(start).Seconds()))
		w.WriteHeader(500)
		w.Write([]byte(err.Error()))
		return err
	}

	w.Header().Set("X-Duration-Seconds", fmt.Sprintf("%f", time.Since(start).Seconds()))
	w.WriteHeader(200)

	if body != nil {
		_, err = w.Write(*body)
	}

	return err
}

func serializeFunction(req FunctionRequest, f *SerializingForkFunctionRunner) (*[]byte, error) {
	log.Printf("Running: %s", req.Process)

	if req.InputReader != nil {
		defer req.InputReader.Close()
	}

	start := time.Now()

	var cmd *exec.Cmd
	ctx := context.Background()
	if f.ExecTimeout.Nanoseconds() > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, f.ExecTimeout)
		defer cancel()
	}

	cmd = exec.CommandContext(ctx, req.Process, req.ProcessArgs...)

	var data []byte

	reader := req.InputReader.(io.Reader)

	// Limit read to the Content-Length header, if provided
	if req.ContentLength != nil && *req.ContentLength > 0 {
		reader = io.LimitReader(req.InputReader, *req.ContentLength)
	}

	var err error
	data, err = ioutil.ReadAll(reader)

	if err != nil {
		return nil, err
	}

	stdout, _ := cmd.StdoutPipe()
	stdin, _ := cmd.StdinPipe()

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	functionRes, errors := pipeToProcess(stdin, stdout, &data)
	if len(errors) > 0 {
		return nil, errors[0]
	}

	err = cmd.Wait()
	done := time.Since(start)
	if err != nil {
		return nil, fmt.Errorf("%s exited: after %.2fs, error: %s", req.Process, done.Seconds(), err)
	}

	log.Printf("%s done: %.2fs secs", req.Process, done.Seconds())

	return functionRes, nil
}

func pipeToProcess(stdin io.WriteCloser, stdout io.Reader, data *[]byte) (*[]byte, []error) {
	var functionResult *[]byte
	var errors []error

	errChannel := make(chan error)

	go func() {
		for goErr := range errChannel {
			errors = append(errors, goErr)
		}
		close(errChannel)
	}()

	wg := sync.WaitGroup{}
	wg.Add(2)

	go func(c chan error) {
		_, err := stdin.Write(*data)
		stdin.Close()

		if err != nil {
			c <- err
		}

		wg.Done()
	}(errChannel)

	go func(c chan error) {
		var err error
		result, err := ioutil.ReadAll(stdout)
		functionResult = &result
		if err != nil {
			c <- err
		}

		wg.Done()
	}(errChannel)

	wg.Wait()

	return functionResult, errors
}
