package config

import (
	"fmt"
	"io/ioutil"
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
	TempDirectory    string
}

func (w WatchdogConfig) Process() (string, []string) {
	parts := strings.Split(w.FunctionProcess, " ")

	if len(parts) > 1 {
		return parts[0], parts[1:]
	}

	return parts[0], []string{}
}

func New(env []string) (WatchdogConfig, error) {
	config := WatchdogConfig{
		TCPPort:          8081,
		HTTPReadTimeout:  time.Second * 10,
		HTTPWriteTimeout: time.Second * 10,
		FunctionProcess:  os.Getenv("fprocess"),
		InjectCGIHeaders: true,
		HardTimeout:      5 * time.Second,
		OperationalMode:  ModeStreaming,
		TempDirectory:    os.TempDir(),
	}

	envMap := mapEnv(env)
	if val := envMap["mode"]; len(val) > 0 {
		config.OperationalMode = WatchdogModeConst(val)
	}

	// Try to make a subdir, otherwise use the global tmpdir
	dir, err := ioutil.TempDir(os.TempDir(), "")
	fmt.Println(err)
	if err == nil {
		config.TempDirectory = dir
	}

	return config, nil
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
	ModeStreaming   = 1
	ModeSerializing = 2
	ModeAfterBurn   = 3
)

func WatchdogModeConst(mode string) int {
	switch mode {
	case "streaming":
		return ModeStreaming
	case "afterburn":
		return ModeAfterBurn
	case "serializing":
		return ModeSerializing
	default:
		return 0
	}
}

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
