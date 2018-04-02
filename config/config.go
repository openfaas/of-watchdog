package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// WatchdogConfig configuration for a watchdog.
type WatchdogConfig struct {
	TCPPort          int
	HTTPReadTimeout  time.Duration
	HTTPWriteTimeout time.Duration
	ExecTimeout      time.Duration

	FunctionProcess  string
	ContentType      string
	InjectCGIHeaders bool
	OperationalMode  int

	StderrBufferSizeBytes int
	StdoutBufferSizeBytes int
}

// Process returns a string for the process and a slice for the arguments from the FunctionProcess.
func (w WatchdogConfig) Process() (string, []string) {
	parts := strings.Split(w.FunctionProcess, " ")

	if len(parts) > 1 {
		return parts[0], parts[1:]
	}

	return parts[0], []string{}
}

// New create config based upon environmental variables.
func New(env []string) (WatchdogConfig, error) {

	envMap := mapEnv(env)

	var functionProcess string
	if val, exists := envMap["fprocess"]; exists {
		functionProcess = val
	}

	if val, exists := envMap["function_process"]; exists {
		functionProcess = val
	}

	contentType := "application/octet-stream"
	if val, exists := envMap["content_type"]; exists {
		contentType = val
	}

	defaultBytes := 1024
	defaultTimeout := time.Second * 10
	defaultHTTPPort := 8080

	config := WatchdogConfig{
		TCPPort:               getInt(envMap, "port", defaultHTTPPort),
		HTTPReadTimeout:       getDuration(envMap, "read_timeout", defaultTimeout),
		HTTPWriteTimeout:      getDuration(envMap, "write_timeout", defaultTimeout),
		FunctionProcess:       functionProcess,
		InjectCGIHeaders:      true,
		ExecTimeout:           getDuration(envMap, "exec_timeout", defaultTimeout),
		OperationalMode:       ModeStreaming,
		ContentType:           contentType,
		StderrBufferSizeBytes: getInt(envMap, "stderr_buffer_bytes", defaultBytes),
		StdoutBufferSizeBytes: getInt(envMap, "stdout_buffer_bytes", defaultBytes),
	}

	if val := envMap["mode"]; len(val) > 0 {
		config.OperationalMode = WatchdogModeConst(val)
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

func getDuration(env map[string]string, key string, defaultValue time.Duration) time.Duration {
	result := defaultValue
	if val, exists := env[key]; exists {
		parsed, _ := time.ParseDuration(val)
		result = parsed

	}

	return result
}

func getInt(env map[string]string, key string, defaultValue int) int {
	result := defaultValue
	if val, exists := env[key]; exists {
		parsed, _ := strconv.Atoi(val)
		result = parsed

	}

	return result
}
