package server

import (
	"fmt"
	"net/http"

	"github.com/openfaas-incubator/of-watchdog/config"
	"github.com/openfaas-incubator/of-watchdog/fprocess"
)

func New(config config.WatchdogConfig) fprocess.Handler {
	commandName, arguments := config.Process()
	functionInvoker := AfterBurnFunctionRunner{
		Process:     commandName,
		ProcessArgs: arguments,
	}
	fmt.Printf("Forking - %s %s\n", commandName, arguments)

	functionInvoker.Start()

	return &handler{
		config:          config,
		functionInvoker: functionInvoker,
	}
}

type handler struct {
	config          config.WatchdogConfig
	functionInvoker AfterBurnFunctionRunner
}

func (pr *handler) HandleRun(w http.ResponseWriter, r *http.Request) {

	req := fprocess.FunctionRequest{
		InputReader:  r.Body,
		OutputWriter: w,
	}

	pr.functionInvoker.Mutex.Lock()
	defer pr.functionInvoker.Mutex.Unlock()

	err := pr.functionInvoker.Run(req, r.ContentLength, r, w)

	if err != nil {
		w.WriteHeader(500)
		w.Write([]byte(err.Error()))
	}
}

func (pr *handler) HandleStderr(w http.ResponseWriter, r *http.Request) {
	panic("implement me")
}
