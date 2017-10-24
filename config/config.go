package config

import (
	"os"
	"strings"
	"time"
)

type WatchdogConfig struct {
	TCPPort          int
	HTTPReadTimeout  time.Duration
	HTTPWriteTimeout time.Duration
	FunctionProcess  string
	InjectCGIHeaders bool
	HardTimeout      time.Duration
}

func (w WatchdogConfig) Process() (string, []string) {
	parts := strings.Split(w.FunctionProcess, " ")

	if len(parts) > 1 {
		return parts[0], parts[1:]
	}

	return parts[0], []string{}
}

func New() (WatchdogConfig, error) {
	config := WatchdogConfig{
		TCPPort:          8081,
		HTTPReadTimeout:  time.Second * 10,
		HTTPWriteTimeout: time.Second * 10,
		FunctionProcess:  os.Getenv("fprocess"),
		InjectCGIHeaders: true,
		HardTimeout:      5 * time.Second,
	}

	return config, nil
}
