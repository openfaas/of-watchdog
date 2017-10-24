package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/openfaas-incubator/of-watchdog/config"
	"github.com/openfaas-incubator/of-watchdog/functions"
)

func main() {
	watchdogConfig, configErr := config.New()
	if configErr != nil {
		fmt.Fprintf(os.Stderr, configErr.Error())
		os.Exit(-1)
	}

	if len(watchdogConfig.FunctionProcess) == 0 {
		fmt.Fprintf(os.Stderr, "Provide a fprocess environmental variable for your function.\n")
		os.Exit(-1)
	}

	s := &http.Server{
		Addr:           fmt.Sprintf(":%d", watchdogConfig.TCPPort),
		ReadTimeout:    watchdogConfig.HTTPReadTimeout,
		WriteTimeout:   watchdogConfig.HTTPWriteTimeout,
		MaxHeaderBytes: 1 << 20, // Max header of 1MB
	}

	requestHandler := makeForkRequestHandler(watchdogConfig)
	http.HandleFunc("/", requestHandler)
	log.Fatal(s.ListenAndServe())
}

func makeForkRequestHandler(watchdogConfig config.WatchdogConfig) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {

		var environment []string

		if watchdogConfig.InjectCGIHeaders {
			environment = getEnvironment(r)
		}

		commandName, arguments := watchdogConfig.Process()
		req := functions.FunctionRequest{
			Process:      commandName,
			ProcessArgs:  arguments,
			InputReader:  r.Body,
			OutputWriter: w,
			Environment:  environment,
		}

		functionInvoker := functions.ForkFunctionRunner{
			HardTimeout: watchdogConfig.HardTimeout,
		}

		err := functionInvoker.Run(req)
		if err != nil {
			w.WriteHeader(500)
			w.Write([]byte(err.Error()))
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
