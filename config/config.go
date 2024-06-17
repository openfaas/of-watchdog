// Copyright (c) OpenFaaS Author(s) 2021. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package config

import (
	"bufio"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
)

// WatchdogConfig configuration for a watchdog.
type WatchdogConfig struct {
	TCPPort             int
	HTTPReadTimeout     time.Duration
	HTTPWriteTimeout    time.Duration
	ExecTimeout         time.Duration
	HealthcheckInterval time.Duration

	FunctionProcess  string
	ContentType      string
	InjectCGIHeaders bool
	OperationalMode  int
	SuppressLock     bool
	UpstreamURL      string
	StaticPath       string

	// BufferHTTPBody buffers the HTTP body in memory
	// to prevent transfer type of chunked encoding
	// which some servers do not support.
	BufferHTTPBody bool

	// MetricsPort TCP port on which to serve HTTP Prometheus metrics
	MetricsPort int

	// MaxInflight limits the number of simultaneous
	// requests that the watchdog allows concurrently.
	// Any request which exceeds this limit will
	// have an immediate response of 429.
	MaxInflight int

	// PrefixLogs adds a date time stamp and the stdio name to any
	// logging from executing functions
	PrefixLogs bool

	// LogBufferSize is the size for scanning logs for stdout/stderr
	LogBufferSize int

	// ReadyEndpoint is the custom readiness path for the watchdog. When non-empty
	// the /_/ready endpoint with proxy the request to this path.
	ReadyEndpoint string

	// JWTAuthentication enables JWT authentication for the watchdog
	// using the OpenFaaS gateway as the issuer.
	JWTAuthentication bool

	// JWTAuthDebug enables debug logging for the JWT authentication middleware.
	JWTAuthDebug bool

	// JWTAuthLocal indicates wether the JWT authentication middleware should use a port-forwarded or
	// local gateway running at `http://127.0.0.1:8000` instead of attempting to reach it via an in-cluster service
	JWTAuthLocal bool

	// LogCallId includes a prefix of the X-Call-Id in any log statements in
	// HTTP mode.
	LogCallId bool
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
	defaultTimeout := time.Second * 30

	envMap := mapEnv(env)

	var (
		functionProcess string
		upstreamURL     string
	)

	logBufferSize := bufio.MaxScanTokenSize

	// default behaviour for backwards compatibility
	prefixLogs := true
	if val, exists := envMap["prefix_logs"]; exists {
		res, err := strconv.ParseBool(val)
		if err == nil {
			prefixLogs = res
		}
	}

	if val, exists := envMap["fprocess"]; exists {
		functionProcess = val
	}

	if val, exists := envMap["function_process"]; exists {
		functionProcess = val
	}

	if val, exists := envMap["upstream_url"]; exists {
		upstreamURL = val
	}

	if val, exists := envMap["http_upstream_url"]; exists {
		upstreamURL = val
	}

	contentType := "application/octet-stream"
	if val, exists := envMap["content_type"]; exists {
		contentType = val
	}

	staticPath := "/home/app/public"
	if val, exists := envMap["static_path"]; exists {
		staticPath = val
	}

	writeTimeout := getDuration(envMap, "write_timeout", defaultTimeout)
	healthcheckInterval := writeTimeout
	if val, exists := envMap["healthcheck_interval"]; exists {
		healthcheckInterval = parseIntOrDurationValue(val, writeTimeout)
	}

	if val, exists := envMap["log_buffer_size"]; exists {
		var err error
		if logBufferSize, err = strconv.Atoi(val); err != nil {
			return WatchdogConfig{}, fmt.Errorf("invalid log_buffer_size value: %s, error: %w", val, err)
		}
	}

	var logCallId bool
	if val, exists := envMap["log_callid"]; exists {
		if val == "1" {
			logCallId = true
		} else {
			logCallId, _ = strconv.ParseBool(val)
		}
	}

	c := WatchdogConfig{
		TCPPort:             getInt(envMap, "port", 8080),
		HTTPReadTimeout:     getDuration(envMap, "read_timeout", defaultTimeout),
		HTTPWriteTimeout:    writeTimeout,
		HealthcheckInterval: healthcheckInterval,
		FunctionProcess:     functionProcess,
		StaticPath:          staticPath,
		InjectCGIHeaders:    true,
		ExecTimeout:         getDuration(envMap, "exec_timeout", defaultTimeout),
		OperationalMode:     ModeStreaming,
		ContentType:         contentType,
		SuppressLock:        getBool(envMap, "suppress_lock"),
		UpstreamURL:         upstreamURL,
		BufferHTTPBody:      getBools(envMap, "buffer_http", "http_buffer_req_body"),
		MetricsPort:         8081,
		MaxInflight:         getInt(envMap, "max_inflight", 0),
		PrefixLogs:          prefixLogs,
		LogBufferSize:       logBufferSize,
		ReadyEndpoint:       envMap["ready_path"],
		LogCallId:           logCallId,
	}

	if val := envMap["mode"]; len(val) > 0 {
		c.OperationalMode = WatchdogModeConst(val)
	}

	if writeTimeout == 0 {
		return c, fmt.Errorf("HTTP write timeout must be over 0s")
	}

	if len(c.FunctionProcess) == 0 && c.OperationalMode != ModeStatic {
		return c, fmt.Errorf(`provide a "function_process" or "fprocess" environmental variable for your function`)
	}

	c.JWTAuthentication = getBool(envMap, "jwt_auth")
	c.JWTAuthDebug = getBool(envMap, "jwt_auth_debug")
	c.JWTAuthLocal = getBool(envMap, "jwt_auth_local")

	return c, nil
}

func mapEnv(env []string) map[string]string {
	mapped := map[string]string{}

	for _, val := range env {
		sep := strings.Index(val, "=")

		if sep > 0 {
			key := val[0:sep]
			value := val[sep+1:]
			mapped[key] = value
		} else {
			log.Printf("Bad environment: %s" + val)
		}
	}

	return mapped
}

func getDuration(env map[string]string, key string, defaultValue time.Duration) time.Duration {
	if val, exists := env[key]; exists {
		return parseIntOrDurationValue(val, defaultValue)
	}

	return defaultValue
}

func parseIntOrDurationValue(val string, fallback time.Duration) time.Duration {
	if len(val) > 0 {
		parsedVal, parseErr := strconv.Atoi(val)
		if parseErr == nil && parsedVal >= 0 {
			return time.Duration(parsedVal) * time.Second
		}
	}

	duration, durationErr := time.ParseDuration(val)
	if durationErr != nil {
		return fallback
	}
	return duration
}

func getInt(env map[string]string, key string, defaultValue int) int {
	result := defaultValue
	if val, exists := env[key]; exists {
		parsed, _ := strconv.Atoi(val)
		result = parsed

	}

	return result
}

func getBool(env map[string]string, key string) bool {
	if env[key] == "true" || env[key] == "1" {
		return true
	}

	return false
}

func getBools(env map[string]string, key ...string) bool {
	v := false
	for _, k := range key {
		if getBool(env, k) == true {
			v = true
			break
		}
	}
	return v
}
