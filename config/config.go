package config

import (
	"fmt"
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
	GetStderrTimeout time.Duration
	OperationalMode  int
}

func (w WatchdogConfig) Process() (string, []string) {
	parts := strings.Split(w.FunctionProcess, " ")
	return parts[0], parts[1:]
}

func New(env []string) WatchdogConfig {
	config := WatchdogConfig{
		TCPPort:          8080,
		HTTPReadTimeout:  time.Second * 10,
		HTTPWriteTimeout: time.Second * 10,
		FunctionProcess:  os.Getenv("fprocess"),
		InjectCGIHeaders: true,
		HardTimeout:      30 * time.Second, // TODO set from env var
		GetStderrTimeout: 5 * time.Second,  // TODO set from env var
		OperationalMode:  ModeFork,
	}

	envMap := mapEnv(env)
	if val := envMap["mode"]; len(val) > 0 {
		config.OperationalMode = WatchdogModeConst(val)
	}

	return config
}

func mapEnv(env []string) map[string]string {
	mapped := map[string]string{}

	for _, val := range env {
		parts := strings.Split(val, "=")
		if len(parts) < 2 {
			fmt.Println("Bad environment: " + val)
		}
		mapped[parts[0]] = parts[1]
	}

	return mapped
}

const (
	ModeFork        = 1
	ModeSerializing = 2
	ModeServer      = 3
)

func WatchdogModeConst(mode string) int {
	switch mode {
	case "streaming", "fork":
		return ModeFork
	case "afterburn", "server":
		return ModeServer
	default:
		return 0
	}
}
