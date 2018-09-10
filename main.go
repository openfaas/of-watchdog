package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/openfaas-incubator/of-watchdog/config"
	"github.com/openfaas-incubator/of-watchdog/executor"
)

var acceptingConnections bool

func main() {
	acceptingConnections = false
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

	if !watchdogConfig.SuppressLock {
		path, lockErr := lock()

		if lockErr != nil {
			log.Panicf("Cannot write %s. To disable lock-file set env suppress_lock=true.\n Error: %s.\n", path, lockErr.Error())
		} else {
			acceptingConnections = true
		}
	} else {
		log.Println("Warning: \"suppress_lock\" is enabled. No automated health-checks will be in place for your function.")
		acceptingConnections = true
	}

	http.HandleFunc("/", requestHandler)
	// log.Fatal(s.ListenAndServe())
	listenUntilShutdown(watchdogConfig.ExecTimeout, s)
}

func buildRequestHandler(watchdogConfig config.WatchdogConfig) http.HandlerFunc {
	var requestHandler http.HandlerFunc

	switch watchdogConfig.OperationalMode {
	case config.ModeStreaming:
		requestHandler = makeForkRequestHandler(watchdogConfig)
		break
	case config.ModeSerializing:
		requestHandler = makeSerializingForkRequestHandler(watchdogConfig)
		break
	case config.ModeAfterBurn:
		requestHandler = makeAfterBurnRequestHandler(watchdogConfig)
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

func lock() (string, error) {
	lockFile := filepath.Join(os.TempDir(), ".lock")
	log.Printf("Writing lock file at: %s", lockFile)
	writeErr := ioutil.WriteFile(lockFile, nil, 0600)
	acceptingConnections = true
	return lockFile, writeErr
}

func makeAfterBurnRequestHandler(watchdogConfig config.WatchdogConfig) func(http.ResponseWriter, *http.Request) {

	commandName, arguments := watchdogConfig.Process()
	functionInvoker := executor.AfterBurnFunctionRunner{
		Process:     commandName,
		ProcessArgs: arguments,
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

		functionInvoker.Mutex.Lock()

		err := functionInvoker.Run(req, r.ContentLength, r, w)

		if err != nil {
			w.WriteHeader(500)
			w.Write([]byte(err.Error()))
		}

		functionInvoker.Mutex.Unlock()
	}
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
		err := functionInvoker.Run(req, w)
		if err != nil {
			log.Println(err)
		}
	}
}

func makeForkRequestHandler(watchdogConfig config.WatchdogConfig) func(http.ResponseWriter, *http.Request) {
	functionInvoker := executor.ForkFunctionRunner{
		ExecTimeout: watchdogConfig.ExecTimeout,
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
		err := functionInvoker.Run(req)
		if err != nil {
			log.Println(err.Error())

			// Probably cannot write to client if we already have written a header
			// w.WriteHeader(500)
			// w.Write([]byte(err.Error()))
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
		ExecTimeout: watchdogConfig.ExecTimeout,
		Process:     commandName,
		ProcessArgs: arguments,
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

		if r.Body != nil {
			defer r.Body.Close()
		}

		err := functionInvoker.Run(req, r.ContentLength, r, w)

		if err != nil {
			w.WriteHeader(500)
			w.Write([]byte(err.Error()))
		}

	}
}

func listenUntilShutdown(shutdownTimeout time.Duration, s *http.Server) {
	idleConnsClosed := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM)

		<-sig

		log.Printf("SIGTERM received.. shutting down server")

		acceptingConnections = false

		if err := s.Shutdown(context.Background()); err != nil {
			// Error from closing listeners, or context timeout:
			log.Printf("Error in Shutdown: %v", err)
		}

		<-time.Tick(shutdownTimeout)

		close(idleConnsClosed)
	}()

	if err := s.ListenAndServe(); err != http.ErrServerClosed {
		log.Printf("Error ListenAndServe: %v", err)
		close(idleConnsClosed)
	}

	<-idleConnsClosed
}
