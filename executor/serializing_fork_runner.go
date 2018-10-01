package executor

import (
	"errors"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// SerializingForkFunctionRunner forks a process for each invocation
type SerializingForkFunctionRunner struct {
	ExecTimeout time.Duration
}

// Run run a fork for each invocation
func (f *SerializingForkFunctionRunner) Run(req FunctionRequest, w http.ResponseWriter) error {
	functionBytes, err := serializeFunction(req, f)
	if err != nil {
		w.WriteHeader(500)
		w.Write([]byte(err.Error()))
		return err
	}

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

	functionRes, err := pipeToProcess(stdin, stdout, &data)

	if err != nil {
		return nil, err
	}

	waitErr := cmd.Wait()
	if waitErr != nil {
		return nil, err
	}

	done := time.Since(start)
	log.Printf("Took %f secs", done.Seconds())

	return functionRes, nil
}

func pipeToProcess(stdin io.WriteCloser, stdout io.Reader, data *[]byte) (*[]byte, error) {
	var functionResult *[]byte
	errorsSlice := make([]error, 2)
	hasError := false

	wg := sync.WaitGroup{}
	wg.Add(2)

	go func() {
		_, err := stdin.Write(*data)
		stdin.Close()

		if err != nil {
			errorsSlice[0] = err
			hasError = true
		}

		wg.Done()
	}()

	go func() {
		var err error
		result, err := ioutil.ReadAll(stdout)
		functionResult = &result
		if err != nil {
			errorsSlice[1] = err
			hasError = true
		}

		wg.Done()
	}()

	wg.Wait()

	if !hasError {
		return functionResult, nil
	}

	// Remove nil errors
	errorStrings := []string{}

	for _, err := range errorsSlice {
		if err == nil {
			continue
		}
		errorStrings = append(errorStrings, err.Error())
	}

	outputError := errors.New(strings.Join(errorStrings, " and "))

	return functionResult, outputError
}
