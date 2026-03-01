package pkg

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/docker/go-units"
	"github.com/openfaas/faas-middleware/auth"
	limiter "github.com/openfaas/faas-middleware/concurrency-limiter"
	"github.com/openfaas/of-watchdog/config"
	"github.com/openfaas/of-watchdog/executor"
	"github.com/openfaas/of-watchdog/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

var (
	acceptingConnections int32
)

type Watchdog struct {
	config      config.WatchdogConfig
	shutdownCtx context.Context
}

func NewWatchdog(config config.WatchdogConfig) *Watchdog {
	return &Watchdog{
		config: config,
	}
}

func (w *Watchdog) Start(ctx context.Context) error {

	atomic.StoreInt32(&acceptingConnections, 0)

	// baseFunctionHandler is the function invoker without any other middlewares.
	// It is used to provide a generic way to implement the readiness checks regardless
	// of the request mode.
	baseFunctionHandler := buildRequestHandler(w.config, w.config.PrefixLogs)
	requestHandler := baseFunctionHandler

	drainCh := make(chan string, 1)
	var drainOnce sync.Once
	startDrain := func(reason string) {
		drainOnce.Do(func() {
			if err := markUnhealthy(); err != nil {
				log.Printf("Unable to mark server as unhealthy: %s\n", err.Error())
			}

			log.Printf("Scheduling graceful shutdown: %s\n", reason)
			drainCh <- reason
		})
	}

	if w.config.OneShot {
		requestHandler = makeOneShotHandler(requestHandler, w.config.ReadyEndpoint, startDrain)
	}

	if w.config.JWTAuthentication {
		handler, err := makeJWTAuthHandler(w.config, requestHandler)
		if err != nil {
			return fmt.Errorf("error creating JWTAuthMiddleware: %w", err)
		}

		requestHandler = handler
	}

	var limit limiter.Limiter
	if w.config.MaxInflight > 0 {
		requestLimiter := limiter.NewConcurrencyLimiter(requestHandler, w.config.MaxInflight)
		requestHandler = requestLimiter.Handler()
		limit = requestLimiter
	}

	log.Printf("Watchdog mode: %s\tfprocess: %q\n", config.WatchdogMode(w.config.OperationalMode), w.config.FunctionProcess)

	httpMetrics := metrics.NewHttp()
	http.HandleFunc("/", metrics.InstrumentHandler(requestHandler, httpMetrics))
	http.HandleFunc("/_/health", makeHealthHandler(w.LockFilePresent))
	http.Handle("/_/ready", &readiness{
		// make sure to pass original handler, before it's been wrapped by
		// the limiter
		functionHandler: baseFunctionHandler,
		endpoint:        w.config.ReadyEndpoint,
		lockCheck:       w.LockFilePresent,
		limiter:         limit,
	})

	metricsServer := metrics.MetricsServer{}
	metricsServer.Register(w.config.MetricsPort)

	cancel := make(chan bool)

	go metricsServer.Serve(cancel)

	s := &http.Server{
		Addr:           fmt.Sprintf(":%d", w.config.TCPPort),
		ReadTimeout:    w.config.HTTPReadTimeout,
		WriteTimeout:   w.config.HTTPWriteTimeout,
		MaxHeaderBytes: 1 << 20, // Max header of 1MB
	}

	log.Printf("Timeouts: read: %s write: %s hard: %s health: %s\n",
		w.config.HTTPReadTimeout,
		w.config.HTTPWriteTimeout,
		w.config.ExecTimeout,
		w.config.HealthcheckInterval)

	if w.config.JWTAuthentication {
		log.Printf("JWT Auth: %v\n", w.config.JWTAuthentication)
	}

	log.Printf("Listening on port: %d\n", w.config.TCPPort)

	listenUntilShutdown(s,
		ctx,
		w.config.HealthcheckInterval,
		w.config.HTTPWriteTimeout,
		w.config.SuppressLock,
		drainCh,
		&httpMetrics)

	return nil
}

func markUnhealthy() error {
	atomic.StoreInt32(&acceptingConnections, 0)

	path := filepath.Join(os.TempDir(), ".lock")
	log.Printf("Removing lock-file : %s\n", path)
	removeErr := os.Remove(path)
	if errors.Is(removeErr, os.ErrNotExist) {
		return nil
	}
	return removeErr
}

func listenUntilShutdown(s *http.Server, shutdownCtx context.Context, healthcheckInterval time.Duration, writeTimeout time.Duration, suppressLock bool, drain <-chan string, httpMetrics *metrics.Http) error {

	idleConnsClosed := make(chan struct{})
	go func() {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM)

		reason := ""
		drainDelay := healthcheckInterval

		select {
		case <-sig:
			reason = "SIGTERM"
		case <-shutdownCtx.Done():
			reason = "Context cancelled"
		case reason = <-drain:
			drainDelay = 0
		}

		if drainDelay > 0 {
			log.Printf("%s: no new connections in %s\n", reason, drainDelay.String())
		} else {
			log.Printf("%s: no new connections allowed immediately\n", reason)
		}

		if err := markUnhealthy(); err != nil {
			log.Printf("Unable to mark server as unhealthy: %s\n", err.Error())
		}

		if drainDelay > 0 {
			timer := time.NewTimer(drainDelay)
			defer timer.Stop()
			<-timer.C
		}

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
			return fmt.Errorf("cannot write %s. To disable lock-file set env suppress_lock=true: %w", path, writeErr)
		}
	} else {
		log.Println("Warning: \"suppress_lock\" is enabled. No automated health-checks will be in place for your function.")

		atomic.StoreInt32(&acceptingConnections, 1)
	}

	<-idleConnsClosed

	return nil
}

func makeOneShotHandler(next http.Handler, readyEndpoint string, startDrain func(string)) http.Handler {
	var served int32

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL != nil {
			switch r.URL.Path {
			case "/_/health", "/_/ready":
				next.ServeHTTP(w, r)
				return
			case readyEndpoint:
				if readyEndpoint != "" {
					next.ServeHTTP(w, r)
					return
				}
			}
		}

		if !atomic.CompareAndSwapInt32(&served, 0, 1) {
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte("watchdog is draining after serving a request"))
			return
		}

		startDrain("one_shot")
		next.ServeHTTP(w, r)
	})
}

func buildRequestHandler(cfg config.WatchdogConfig, prefixLogs bool) http.Handler {
	var requestHandler http.HandlerFunc

	switch cfg.OperationalMode {
	case config.ModeStreaming:
		requestHandler = makeStreamingRequestHandler(cfg, prefixLogs, cfg.LogBufferSize)
	case config.ModeSerializing:
		requestHandler = makeSerializingForkRequestHandler(cfg, prefixLogs)
	case config.ModeHTTP:
		requestHandler = makeHTTPRequestHandler(cfg, prefixLogs, cfg.LogBufferSize)
	case config.ModeStatic:
		requestHandler = makeStaticRequestHandler(cfg)
	case config.ModeInproc:
		requestHandler = makeInprocRequestHandler(cfg, prefixLogs, cfg.LogBufferSize)
	default:
		log.Panicf("unknown watchdog mode: %d", cfg.OperationalMode)
	}

	return requestHandler
}

// createLockFile returns a path to a lock file and/or an error
// if the file could not be created.
func createLockFile() (string, error) {
	path := filepath.Join(os.TempDir(), ".lock")
	log.Printf("Writing lock-file to: %s\n", path)

	if err := os.MkdirAll(os.TempDir(), os.ModePerm); err != nil {
		return path, err
	}

	if err := os.WriteFile(path, []byte{}, 0660); err != nil {
		return path, err
	}

	atomic.StoreInt32(&acceptingConnections, 1)
	return path, nil
}

func makeSerializingForkRequestHandler(cfg config.WatchdogConfig, logPrefix bool) func(http.ResponseWriter, *http.Request) {
	functionInvoker := executor.SerializingForkFunctionRunner{
		ExecTimeout:   cfg.ExecTimeout,
		LogPrefix:     logPrefix,
		LogBufferSize: cfg.LogBufferSize,
	}

	return func(w http.ResponseWriter, r *http.Request) {

		var environment []string

		if cfg.InjectCGIHeaders {
			environment = getEnvironment(r)
		}

		commandName, arguments := cfg.Process()
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

		w.Header().Set("Content-Type", cfg.ContentType)
		err := functionInvoker.Run(req, w)
		if err != nil {
			log.Println(err)
		}
	}
}

func makeStreamingRequestHandler(cfg config.WatchdogConfig, prefixLogs bool, logBufferSize int) func(http.ResponseWriter, *http.Request) {
	functionInvoker := executor.StreamingFunctionRunner{
		ExecTimeout:   cfg.ExecTimeout,
		LogPrefix:     prefixLogs,
		LogBufferSize: logBufferSize,
	}

	return func(w http.ResponseWriter, r *http.Request) {

		var environment []string

		if cfg.InjectCGIHeaders {
			environment = getEnvironment(r)
		}

		ww := WriterCounter{}
		ww.setWriter(w)
		start := time.Now()
		commandName, arguments := cfg.Process()
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

		w.Header().Set("Content-Type", cfg.ContentType)
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

func makeInprocRequestHandler(cfg config.WatchdogConfig, prefixLogs bool, logBufferSize int) func(http.ResponseWriter, *http.Request) {
	runner := executor.NewInprocRunner(cfg.Handler,
		prefixLogs,
		logBufferSize,
		cfg.LogCallId,
		cfg.ExecTimeout,
	)

	if err := runner.Start(); err != nil {
		log.Fatalf("Failed to start in-process runner: %v", err)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		runner.Run(w, r)
	}
}

func makeHTTPRequestHandler(cfg config.WatchdogConfig, prefixLogs bool, logBufferSize int) func(http.ResponseWriter, *http.Request) {
	upstreamURL, _ := url.Parse(cfg.UpstreamURL)

	commandName, arguments := cfg.Process()
	functionInvoker := executor.HTTPFunctionRunner{
		ExecTimeout:    cfg.ExecTimeout,
		Process:        commandName,
		ProcessArgs:    arguments,
		BufferHTTPBody: cfg.BufferHTTPBody,
		LogPrefix:      prefixLogs,
		LogBufferSize:  logBufferSize,
		LogCallId:      cfg.LogCallId,
		ReverseProxy: &httputil.ReverseProxy{
			Director: func(req *http.Request) {
				req.URL.Host = upstreamURL.Host
				req.URL.Scheme = "http"
			},
			ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			},
			ErrorLog: log.New(io.Discard, "", 0),
		},
	}

	if len(cfg.UpstreamURL) == 0 {
		log.Fatal(`For "mode=http" you must specify a valid URL for "http_upstream_url"`)
	}

	urlValue, err := url.Parse(cfg.UpstreamURL)
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

func makeStaticRequestHandler(cfg config.WatchdogConfig) http.HandlerFunc {
	if cfg.StaticPath == "" {
		log.Fatal(`For mode=static you must specify the "static_path" to serve`)
	}

	log.Printf("Serving files at: %s", cfg.StaticPath)
	return http.FileServer(http.Dir(cfg.StaticPath)).ServeHTTP
}

func (w *Watchdog) LockFilePresent() bool {
	return lockFilePresent()
}

func lockFilePresent() bool {
	path := filepath.Join(os.TempDir(), ".lock")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return false
	}

	return true
}

func makeHealthHandler(lockPresent func() bool) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			if atomic.LoadInt32(&acceptingConnections) == 0 || lockPresent() == false {
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

func makeJWTAuthHandler(cfg config.WatchdogConfig, next http.Handler) (http.Handler, error) {
	namespace, err := getFnNamespace()
	if err != nil {
		return nil, fmt.Errorf("failed to get function namespace: %w", err)
	}
	name, err := getFnName()
	if err != nil {
		return nil, fmt.Errorf("failed to get function name: %w", err)
	}

	authOpts := auth.JWTAuthOptions{
		Name:           name,
		Namespace:      namespace,
		LocalAuthority: cfg.JWTAuthLocal,
		Debug:          cfg.JWTAuthDebug,
	}

	return auth.NewJWTAuthMiddleware(authOpts, next)
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

func getFnName() (string, error) {
	name, ok := os.LookupEnv("OPENFAAS_NAME")
	if !ok || len(name) == 0 {
		return "", fmt.Errorf("env variable 'OPENFAAS_NAME' not set")
	}

	return name, nil
}

// getFnNamespace gets the namespace name from the env variable OPENFAAS_NAMESPACE
// or reads it from the service account if the env variable is not present
func getFnNamespace() (string, error) {
	if namespace, ok := os.LookupEnv("OPENFAAS_NAMESPACE"); ok {
		return namespace, nil
	}

	nsVal, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace")
	if err != nil {
		return "", err
	}
	return string(nsVal), nil
}
