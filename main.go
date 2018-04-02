package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/openfaas-incubator/of-watchdog/config"
	"github.com/openfaas-incubator/of-watchdog/executor"
)

func main() {
	watchdogConfig, configErr := config.New(os.Environ())
	if configErr != nil {
		fmt.Fprintf(os.Stderr, configErr.Error())
		os.Exit(-1)
	}

	if len(watchdogConfig.FunctionProcess) == 0 {
		fmt.Fprintf(os.Stderr, "Provide a \"function_process\" or \"fprocess\" environmental variable for your function.\n")
		os.Exit(-1)
	}

	s := &http.Server{
		Addr:           fmt.Sprintf(":%d", watchdogConfig.TCPPort),
		ReadTimeout:    watchdogConfig.HTTPReadTimeout,
		WriteTimeout:   watchdogConfig.HTTPWriteTimeout,
		MaxHeaderBytes: 1 << 20, // Max header of 1MB
	}

	requestHandler := buildRequestHandler(watchdogConfig)

	log.Printf("OperationalMode: %s\n", config.WatchdogMode(watchdogConfig.OperationalMode))

	if err := lock(); err != nil {
		log.Panic(err.Error())
	}

	http.HandleFunc("/", requestHandler)
	log.Fatal(s.ListenAndServe())
}

func buildRequestHandler(watchdogConfig config.WatchdogConfig) http.HandlerFunc {
	var requestHandler http.HandlerFunc

	switch watchdogConfig.OperationalMode {
	case config.ModeStreaming:
		requestHandler = makeStreamingForkFunctionRunner(watchdogConfig)
		break
	case config.ModeSerializing:
		requestHandler = makeSerializingForkRequestHandler(watchdogConfig)
		break
	case config.ModeAfterBurn:
		log.Fatalln("Afterburn mode has been deprecated. Please see the new 'http' mode.")
		break
	case config.ModeHTTP:
		requestHandler = makeHTTPRequestHandler(watchdogConfig)
		break
	default:
		log.Panicf("unknown watchdog mode: %d", watchdogConfig.OperationalMode)
		break
	}

	return requestHandler
}

func lock() error {
	lockFile := filepath.Join(os.TempDir(), ".lock")
	log.Printf("Writing lock file at: %s", lockFile)
	return ioutil.WriteFile(lockFile, nil, 0600)

}

func makeSerializingForkRequestHandler(watchdogConfig config.WatchdogConfig) func(http.ResponseWriter, *http.Request) {
	functionInvoker := executor.SerializingForkFunctionRunner{
		ExecTimeout: watchdogConfig.ExecTimeout,
	}

	return func(w http.ResponseWriter, r *http.Request) {

		var environment []string

		if watchdogConfig.InjectCGIHeaders {
			environment = getEnvironment(r)
		}

		commandName, arguments := watchdogConfig.Process()
		req := executor.FunctionRequest{
			Process:       commandName,
			ProcessArgs:   arguments,
			InputReader:   r.Body,
			ContentLength: &r.ContentLength,
			OutputWriter:  w,
			Environment:   environment,
		}

		w.Header().Set("Content-Type", watchdogConfig.ContentType)
		start := time.Now()
		err := functionInvoker.Run(req, w)

		taken := time.Since(start).Seconds()
		method := "Streaming"

		if err != nil {
			log.Printf("%s: %s %s (%s) - %fs (ERR)", method, r.Method, r.URL, req.Process, taken)
			log.Println(err)
		} else {
			log.Printf("%s: %s %s (%s) - %fs", method, r.Method, r.URL, req.Process, taken)
		}
	}
}

func makeStreamingForkFunctionRunner(watchdogConfig config.WatchdogConfig) func(http.ResponseWriter, *http.Request) {
	functionInvoker := executor.StreamingForkFunctionRunner{
		ExecTimeout:           watchdogConfig.ExecTimeout,
		StderrBufferSizeBytes: watchdogConfig.StderrBufferSizeBytes,
	}

	return func(w http.ResponseWriter, r *http.Request) {

		var environment []string

		if watchdogConfig.InjectCGIHeaders {
			environment = getEnvironment(r)
		}

		commandName, arguments := watchdogConfig.Process()
		req := executor.FunctionRequest{
			Process:      commandName,
			ProcessArgs:  arguments,
			InputReader:  r.Body,
			OutputWriter: w,
			Environment:  environment,
		}

		w.Header().Set("Content-Type", watchdogConfig.ContentType)
		start := time.Now()
		err := functionInvoker.Run(req)

		taken := time.Since(start).Seconds()
		method := "Streaming"

		if err != nil {
			log.Printf("%s: %s %s (%s) - %fs (ERR)", method, r.Method, r.URL, req.Process, taken)
			log.Println(err.Error())

			// Probably cannot write to client if we already have written a header
			w.WriteHeader(500)
			w.Write([]byte(err.Error()))
		} else {
			log.Printf("%s: %s %s (%s) - %fs", method, r.Method, r.URL, req.Process, taken)

		}
	}
}

func getEnvironment(r *http.Request) []string {
	var envs []string

	envs = os.Environ()
	for k, v := range r.Header {
		kv := fmt.Sprintf("Http_%s=%s", strings.Replace(k, "-", "_", -1), v[0])
		envs = append(envs, kv)
	}
	envs = append(envs, fmt.Sprintf("Http_Method=%s", r.Method))

	if len(r.URL.RawQuery) > 0 {
		envs = append(envs, fmt.Sprintf("Http_Query=%s", r.URL.RawQuery))
	}

	if len(r.URL.Path) > 0 {
		envs = append(envs, fmt.Sprintf("Http_Path=%s", r.URL.Path))
	}

	return envs
}

func makeHTTPRequestHandler(watchdogConfig config.WatchdogConfig) func(http.ResponseWriter, *http.Request) {
	commandName, arguments := watchdogConfig.Process()
	functionInvoker := executor.HTTPFunctionRunner{
		ExecTimeout:           watchdogConfig.ExecTimeout,
		Process:               commandName,
		ProcessArgs:           arguments,
		StderrBufferSizeBytes: watchdogConfig.StderrBufferSizeBytes,
		StdoutBufferSizeBytes: watchdogConfig.StdoutBufferSizeBytes,
	}

	fmt.Printf("Forking - %s %s\n", commandName, arguments)
	functionInvoker.Start()

	return func(w http.ResponseWriter, r *http.Request) {

		req := executor.FunctionRequest{
			Process:      commandName,
			ProcessArgs:  arguments,
			InputReader:  r.Body,
			OutputWriter: w,
		}

		err := functionInvoker.Run(req, r.ContentLength, r, w)

		if err != nil {
			w.WriteHeader(500)
			w.Write([]byte(err.Error()))
		}

	}
}
