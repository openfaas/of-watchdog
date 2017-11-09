package main

import (
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/openfaas-incubator/of-watchdog/config"
	"github.com/openfaas-incubator/of-watchdog/fprocess"
	"github.com/openfaas-incubator/of-watchdog/fprocess/fork"
	"github.com/openfaas-incubator/of-watchdog/fprocess/server"
)

var newHandler = map[int]func(config.WatchdogConfig) fprocess.Handler{
	config.ModeFork:   fork.New,
	config.ModeServer: server.New,
}

func main() {
	watchdogConfig := config.New(os.Environ())

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

	handler := newHandler[watchdogConfig.OperationalMode](watchdogConfig)

	if err := lock(); err != nil {
		log.Panic(err.Error())
	}

	http.HandleFunc("/", handler.HandleRun)
	http.HandleFunc("/stderr/", handler.HandleStderr)
	log.Fatal(s.ListenAndServe())
}

func lock() error {
	lockFile := filepath.Join(os.TempDir(), ".lock")
	log.Printf("Writing lock file at: %s", lockFile)
	return ioutil.WriteFile(lockFile, nil, 0600)

}
