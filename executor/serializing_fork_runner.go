// Copyright (c) OpenFaaS Author(s) 2021. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package executor

import (
	"context"
	"fmt"
	"io"
	"io/ioutil"
	"os"

	units "github.com/docker/go-units"

	"log"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// SerializingForkFunctionRunner forks a process for each invocation
type SerializingForkFunctionRunner struct {
	ExecTimeout   time.Duration
	LogPrefix     bool
	LogBufferSize int
}

// Run run a fork for each invocation
func (f *SerializingForkFunctionRunner) Run(req FunctionRequest, w http.ResponseWriter) error {
	start := time.Now()
	body, err := serializeFunction(req, f)
	if err != nil {
		w.Header().Set("X-Duration-Seconds", fmt.Sprintf("%f", time.Since(start).Seconds()))
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(err.Error()))

		done := time.Since(start)

		if !strings.HasPrefix(req.UserAgent, "kube-probe") {
			log.Printf("%s %s - %d - ContentLength: %s (%.4fs)", req.Method, req.RequestURI, http.StatusOK, units.HumanSize(float64(len(err.Error()))), done.Seconds())
		}

		return err
	}

	w.Header().Set("X-Duration-Seconds", fmt.Sprintf("%f", time.Since(start).Seconds()))
	w.WriteHeader(200)

	bodyLen := 0
	if body != nil {
		_, err = w.Write(*body)
		bodyLen = len(*body)
	}

	done := time.Since(start)

	if !strings.HasPrefix(req.UserAgent, "kube-probe") {
		log.Printf("%s %s - %d - ContentLength: %s (%.4fs)", req.Method, req.RequestURI, http.StatusOK, units.HumanSize(float64(bodyLen)), done.Seconds())
	}

	return err
}

func serializeFunction(req FunctionRequest, f *SerializingForkFunctionRunner) (*[]byte, error) {

	if req.InputReader != nil {
		defer req.InputReader.Close()
	}

	var cmd *exec.Cmd
	ctx := context.Background()
	if f.ExecTimeout.Nanoseconds() > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, f.ExecTimeout)
		defer cancel()
	}

	cmd = exec.CommandContext(ctx, req.Process, req.ProcessArgs...)
	cmd.Env = req.Environment

	var data []byte

	if req.InputReader != nil {
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

	}

	stdout, _ := cmd.StdoutPipe()
	stdin, _ := cmd.StdinPipe()
	stderr, _ := cmd.StderrPipe()

	if err := cmd.Start(); err != nil {
		return nil, err
	}

	bindLoggingPipe("stderr", stderr, os.Stderr, f.LogPrefix, f.LogBufferSize)

	functionRes, errors := pipeToProcess(stdin, stdout, &data)
	if len(errors) > 0 {
		return nil, errors[0]
	}

	err := cmd.Wait()

	return functionRes, err
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
