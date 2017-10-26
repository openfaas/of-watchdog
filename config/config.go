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
	OperationalMode  int
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
		OperationalMode:  ModeSerializing,
	}

	return config, nil
}

const (
	ModeStreaming   = 1
	ModeSerializing = 2
	ModeAfterBurn   = 3
)

func WatchdogMode(mode int) string {
	switch mode {
	case ModeStreaming:
		return "streaming"
	case ModeAfterBurn:
		return "afterburn"
	case ModeSerializing:
		return "serializing"
	default:
		return "unknown"
	}
}
