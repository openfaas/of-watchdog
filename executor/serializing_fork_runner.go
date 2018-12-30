package executor

import (
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
}

// Run run a fork for each invocation
func (f *SerializingForkFunctionRunner) Run(req FunctionRequest, w http.ResponseWriter) error {
	start := time.Now()
	functionBytes, err := serializeFunction(req, f)
	if err != nil {
		w.Header().Set("X-Duration-Seconds", fmt.Sprintf("%f", time.Since(start).Seconds()))
		w.WriteHeader(500)
		w.Write([]byte(err.Error()))
		return err
	}
	w.Header().Set("X-Duration-Seconds", fmt.Sprintf("%f", time.Since(start).Seconds()))
	w.WriteHeader(200)

	if functionBytes != nil {
		_, err = w.Write(*functionBytes)
	} else {
		log.Println("Empty function response.")
	}

	return err
}

func serializeFunction(req FunctionRequest, f *SerializingForkFunctionRunner) (*[]byte, error) {
	log.Printf("Running %s", req.Process)

	start := time.Now()
	cmd := exec.Command(req.Process, req.ProcessArgs...)
	cmd.Env = req.Environment

	var timer *time.Timer
	if f.ExecTimeout > time.Millisecond*0 {

		timer = time.NewTimer(f.ExecTimeout)
		go func() {
			<-timer.C

			log.Printf("Function was killed by ExecTimeout: %s\n", f.ExecTimeout.String())
			killErr := cmd.Process.Kill()
			if killErr != nil {
				log.Println("Error killing function due to ExecTimeout", killErr)
			}
		}()
	}

	if timer != nil {
		defer timer.Stop()
	}

	var data []byte

	// Read request if present.
	if req.ContentLength != nil {
		defer req.InputReader.Close()
		limitReader := io.LimitReader(req.InputReader, *req.ContentLength)
		var err error
		data, err = ioutil.ReadAll(limitReader)

		if err != nil {
			return nil, err
		}

	}

	stdout, _ := cmd.StdoutPipe()
	stdin, _ := cmd.StdinPipe()

	err := cmd.Start()
	if err != nil {
		return nil, err
	}

	functionRes, errors := pipeToProcess(stdin, stdout, &data)

	if len(errors) > 0 {
		return nil, errors[0]
	}

	waitErr := cmd.Wait()
	if waitErr != nil {
		return nil, err
	}

	done := time.Since(start)
	log.Printf("Took %f secs", done.Seconds())

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
