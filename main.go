// Copyright (c) OpenFaaS Author(s) 2021. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	units "github.com/docker/go-units"

	limiter "github.com/openfaas/faas-middleware/concurrency-limiter"
	"github.com/openfaas/of-watchdog/auth"
	"github.com/openfaas/of-watchdog/config"
	"github.com/openfaas/of-watchdog/executor"
	"github.com/openfaas/of-watchdog/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

var (
	acceptingConnections int32
)

func main() {
	var runHealthcheck bool
	var versionFlag bool

	flag.BoolVar(&versionFlag, "version", false, "Print the version and exit")
	flag.BoolVar(&runHealthcheck,
		"run-healthcheck",
		false,
		"Check for the a lock-file, when using an exec healthcheck. Exit 0 for present, non-zero when not found.")

	flag.Parse()

	printVersion()

	if versionFlag {
		return
	}

	if runHealthcheck {
		if lockFilePresent() {
			os.Exit(0)
		}

		fmt.Fprintf(os.Stderr, "unable to find lock file.\n")
		os.Exit(1)
	}

	atomic.StoreInt32(&acceptingConnections, 0)

	watchdogConfig, err := config.New(os.Environ())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %s", err.Error())
		os.Exit(1)
	}

	// baseFunctionHandler is the function invoker without any other middlewares.
	// It is used to provide a generic way to implement the readiness checks regardless
	// of the request mode.
	baseFunctionHandler := buildRequestHandler(watchdogConfig, watchdogConfig.PrefixLogs)
	requestHandler := baseFunctionHandler
	// add middlewares here
	policyPath := os.Getenv(auth.OPAPolicyEnv)
	if policyPath != "" {
		log.Printf("loading OPA policy(s): %q", policyPath)
		// TODO: support remote authorizer if it starts with http:// or https://
		authorizer, err := auth.NewAuthorizer(policyPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s", err.Error())
			os.Exit(1)
		}

		inputCfg, err := auth.InputConfigFromEnv()
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s", err.Error())
			os.Exit(1)
		}

		authMiddleware := auth.New(authorizer, inputCfg)
		requestHandler = authMiddleware(requestHandler)
	}

	var limit limiter.Limiter
	if watchdogConfig.MaxInflight > 0 {
		requestLimiter := limiter.NewConcurrencyLimiter(requestHandler, watchdogConfig.MaxInflight)
		requestHandler = requestLimiter.Handler()
		limit = requestLimiter
	}

	log.Printf("Watchdog mode: %s\tfprocess: %q\n", config.WatchdogMode(watchdogConfig.OperationalMode), watchdogConfig.FunctionProcess)

	httpMetrics := metrics.NewHttp()
	http.HandleFunc("/", metrics.InstrumentHandler(requestHandler, httpMetrics))
	http.HandleFunc("/_/health", makeHealthHandler())
	http.Handle("/_/ready", &readiness{
		// make sure to pass original handler, before it's been wrapped by
		// the limiter
		functionHandler: baseFunctionHandler,
		endpoint:        watchdogConfig.ReadyEndpoint,
		lockCheck:       lockFilePresent,
		limiter:         limit,
	})

	metricsServer := metrics.MetricsServer{}
	metricsServer.Register(watchdogConfig.MetricsPort)

	cancel := make(chan bool)

	go metricsServer.Serve(cancel)

	s := &http.Server{
		Addr:           fmt.Sprintf(":%d", watchdogConfig.TCPPort),
		ReadTimeout:    watchdogConfig.HTTPReadTimeout,
		WriteTimeout:   watchdogConfig.HTTPWriteTimeout,
		MaxHeaderBytes: 1 << 20, // Max header of 1MB
	}

	log.Printf("Timeouts: read: %s write: %s hard: %s health: %s\n",
		watchdogConfig.HTTPReadTimeout,
		watchdogConfig.HTTPWriteTimeout,
		watchdogConfig.ExecTimeout,
		watchdogConfig.HealthcheckInterval)

	log.Printf("Listening on port: %d\n", watchdogConfig.TCPPort)

	listenUntilShutdown(s,
		watchdogConfig.HealthcheckInterval,
		watchdogConfig.HTTPWriteTimeout,
		watchdogConfig.SuppressLock,
		&httpMetrics)
}

func markUnhealthy() error {
	atomic.StoreInt32(&acceptingConnections, 0)

	path := filepath.Join(os.TempDir(), ".lock")
	log.Printf("Removing lock-file : %s\n", path)
	removeErr := os.Remove(path)
	return removeErr
}

func listenUntilShutdown(s *http.Server, healthcheckInterval time.Duration, writeTimeout time.Duration, suppressLock bool, httpMetrics *metrics.Http) {

	idleConnsClosed := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM)

		<-sig

		log.Printf("SIGTERM: no new connections in %s\n", healthcheckInterval.String())

		if err := markUnhealthy(); err != nil {
			log.Printf("Unable to mark server as unhealthy: %s\n", err.Error())
		}

		<-time.Tick(healthcheckInterval)

		connections := int64(testutil.ToFloat64(httpMetrics.InFlight))
		log.Printf("No new connections allowed, draining: %d requests\n", connections)

		// The maximum time to wait for active connections whilst shutting down is
		// equivalent to the maximum execution time i.e. writeTimeout.
		ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
		defer cancel()

		if err := s.Shutdown(ctx); err != nil {
			log.Printf("Error in Shutdown: %v", err)
		}

		connections = int64(testutil.ToFloat64(httpMetrics.InFlight))

		log.Printf("Exiting. Active connections: %d\n", connections)

		close(idleConnsClosed)
	}()

	// Run the HTTP server in a separate go-routine.
	go func() {
		if err := s.ListenAndServe(); err != http.ErrServerClosed {
			log.Printf("Error ListenAndServe: %v", err)
			close(idleConnsClosed)
		}
	}()

	if suppressLock == false {
		path, writeErr := createLockFile()

		if writeErr != nil {
			log.Panicf("Cannot write %s. To disable lock-file set env suppress_lock=true.\n Error: %s.\n", path, writeErr.Error())
		}
	} else {
		log.Println("Warning: \"suppress_lock\" is enabled. No automated health-checks will be in place for your function.")

		atomic.StoreInt32(&acceptingConnections, 1)
	}

	<-idleConnsClosed
}

func buildRequestHandler(watchdogConfig config.WatchdogConfig, prefixLogs bool) http.Handler {
	var requestHandler http.HandlerFunc

	switch watchdogConfig.OperationalMode {
	case config.ModeStreaming:
		requestHandler = makeStreamingRequestHandler(watchdogConfig, prefixLogs, watchdogConfig.LogBufferSize)
	case config.ModeSerializing:
		requestHandler = makeSerializingForkRequestHandler(watchdogConfig, prefixLogs)
	case config.ModeHTTP:
		requestHandler = makeHTTPRequestHandler(watchdogConfig, prefixLogs, watchdogConfig.LogBufferSize)
	case config.ModeStatic:
		requestHandler = makeStaticRequestHandler(watchdogConfig)
	default:
		log.Panicf("unknown watchdog mode: %d", watchdogConfig.OperationalMode)
	}

	return requestHandler
}

// createLockFile returns a path to a lock file and/or an error
// if the file could not be created.
func createLockFile() (string, error) {
	path := filepath.Join(os.TempDir(), ".lock")
	log.Printf("Writing lock-file to: %s\n", path)

	mkdirErr := os.MkdirAll(os.TempDir(), os.ModePerm)
	if mkdirErr != nil {
		return path, mkdirErr
	}

	writeErr := ioutil.WriteFile(path, []byte{}, 0660)
	if writeErr != nil {
		return path, writeErr
	}

	atomic.StoreInt32(&acceptingConnections, 1)
	return path, nil
}

func makeSerializingForkRequestHandler(watchdogConfig config.WatchdogConfig, logPrefix bool) func(http.ResponseWriter, *http.Request) {
	functionInvoker := executor.SerializingForkFunctionRunner{
		ExecTimeout: watchdogConfig.ExecTimeout,
		LogPrefix:   logPrefix,
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
			RequestURI:    r.RequestURI,
			Method:        r.Method,
			UserAgent:     r.UserAgent(),
		}

		w.Header().Set("Content-Type", watchdogConfig.ContentType)
		err := functionInvoker.Run(req, w)
		if err != nil {
			log.Println(err)
		}
	}
}

func makeStreamingRequestHandler(watchdogConfig config.WatchdogConfig, prefixLogs bool, logBufferSize int) func(http.ResponseWriter, *http.Request) {
	functionInvoker := executor.StreamingFunctionRunner{
		ExecTimeout:   watchdogConfig.ExecTimeout,
		LogPrefix:     prefixLogs,
		LogBufferSize: logBufferSize,
	}

	return func(w http.ResponseWriter, r *http.Request) {

		var environment []string

		if watchdogConfig.InjectCGIHeaders {
			environment = getEnvironment(r)
		}

		ww := WriterCounter{}
		ww.setWriter(w)
		start := time.Now()
		commandName, arguments := watchdogConfig.Process()
		req := executor.FunctionRequest{
			Process:      commandName,
			ProcessArgs:  arguments,
			InputReader:  r.Body,
			OutputWriter: &ww,
			Environment:  environment,
			RequestURI:   r.RequestURI,
			Method:       r.Method,
			UserAgent:    r.UserAgent(),
		}

		w.Header().Set("Content-Type", watchdogConfig.ContentType)
		err := functionInvoker.Run(req)
		if err != nil {
			log.Println(err.Error())

			// Cannot write a status code to the client because we
			// already have written a header
			done := time.Since(start)
			if !strings.HasPrefix(req.UserAgent, "kube-probe") {
				log.Printf("%s %s - %d - ContentLength: %s (%.4fs)", req.Method, req.RequestURI, http.StatusInternalServerError, units.HumanSize(float64(ww.Bytes())), done.Seconds())
				return
			}
		}

		done := time.Since(start)
		if !strings.HasPrefix(req.UserAgent, "kube-probe") {
			log.Printf("%s %s - %d - ContentLength: %s (%.4fs)", req.Method, req.RequestURI, http.StatusOK, units.HumanSize(float64(ww.Bytes())), done.Seconds())
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

	if len(r.TransferEncoding) > 0 {
		envs = append(envs, fmt.Sprintf("Http_Transfer_Encoding=%s", r.TransferEncoding[0]))
	}

	return envs
}

func makeHTTPRequestHandler(watchdogConfig config.WatchdogConfig, prefixLogs bool, logBufferSize int) func(http.ResponseWriter, *http.Request) {
	commandName, arguments := watchdogConfig.Process()
	functionInvoker := executor.HTTPFunctionRunner{
		ExecTimeout:    watchdogConfig.ExecTimeout,
		Process:        commandName,
		ProcessArgs:    arguments,
		BufferHTTPBody: watchdogConfig.BufferHTTPBody,
		LogPrefix:      prefixLogs,
		LogBufferSize:  logBufferSize,
	}

	if len(watchdogConfig.UpstreamURL) == 0 {
		log.Fatal(`For "mode=http" you must specify a valid URL for "http_upstream_url"`)
	}

	urlValue, err := url.Parse(watchdogConfig.UpstreamURL)
	if err != nil {
		log.Fatalf(`For "mode=http" you must specify a valid URL for "http_upstream_url", error: %s`, err)
	}

	functionInvoker.UpstreamURL = urlValue

	log.Printf("Forking: %s, arguments: %s", commandName, arguments)
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

		if err := functionInvoker.Run(req, r.ContentLength, r, w); err != nil {
			w.WriteHeader(500)
			w.Write([]byte(err.Error()))
		}
	}
}

func makeStaticRequestHandler(watchdogConfig config.WatchdogConfig) http.HandlerFunc {
	if watchdogConfig.StaticPath == "" {
		log.Fatal(`For mode=static you must specify the "static_path" to serve`)
	}

	log.Printf("Serving files at: %s", watchdogConfig.StaticPath)
	return http.FileServer(http.Dir(watchdogConfig.StaticPath)).ServeHTTP
}

func lockFilePresent() bool {
	path := filepath.Join(os.TempDir(), ".lock")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false
	}

	return true
}

func makeHealthHandler() func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if atomic.LoadInt32(&acceptingConnections) == 0 || lockFilePresent() == false {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}

			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))

		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

func printVersion() {
	sha := "unknown"
	if len(GitCommit) > 0 {
		sha = GitCommit
	}

	log.Printf("Version: %v\tSHA: %v\n", BuildVersion(), sha)
}

type WriterCounter struct {
	w     io.Writer
	bytes int64
}

func (nc *WriterCounter) setWriter(w io.Writer) {
	nc.w = w
}

func (nc *WriterCounter) Bytes() int64 {
	return nc.bytes
}

func (nc *WriterCounter) Write(p []byte) (int, error) {
	n, err := nc.w.Write(p)
	if err != nil {
		return n, err
	}

	nc.bytes += int64(n)
	return n, err
}
