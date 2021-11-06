// Copyright (c) OpenFaaS Author(s) 2021. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package config

import (
	"bufio"
	"fmt"
	"testing"
	"time"
)

func TestNew(t *testing.T) {
	defaults, _ := New([]string{})
	if defaults.TCPPort != 8080 {
		t.Errorf("Want TCPPort: 8080, got: %d", defaults.TCPPort)
	}

}

func Test_LogBufferSize_Default(t *testing.T) {
	env := []string{}

	actual, _ := New(env)
	want := bufio.MaxScanTokenSize
	if actual.LogBufferSize != want {
		t.Errorf("Want %v. got: %v", want, actual.LogBufferSize)
	}
}

func Test_LogBufferSize_Override(t *testing.T) {
	env := []string{"log_buffer_size=1024"}

	actual, _ := New(env)
	want := 1024
	if actual.LogBufferSize != want {
		t.Errorf("Want %v. got: %v", want, actual.LogBufferSize)
	}
}

func Test_OperationalMode_Default(t *testing.T) {
	defaults, _ := New([]string{})
	if defaults.OperationalMode != ModeStreaming {
		t.Errorf("Want %s. got: %s", WatchdogMode(ModeStreaming), WatchdogMode(defaults.OperationalMode))
	}
}

func Test_BufferHttpModeDefaultsToFalse(t *testing.T) {
	env := []string{}

	actual, _ := New(env)
	want := false
	if actual.BufferHTTPBody != want {
		t.Errorf("Want %v. got: %v", want, actual.BufferHTTPBody)
	}
}

func Test_UpstreamURL(t *testing.T) {
	urlVal := "http://127.0.0.1:8082"
	env := []string{
		fmt.Sprintf("upstream_url=%s", urlVal),
	}

	actual, _ := New(env)
	want := urlVal
	if actual.UpstreamURL != want {
		t.Errorf("Want %v. got: %v", want, actual.UpstreamURL)
	}
}

func Test_UpstreamURLVerbose(t *testing.T) {
	urlVal := "http://127.0.0.1:8082"
	env := []string{
		fmt.Sprintf("http_upstream_url=%s", urlVal),
	}

	actual, _ := New(env)
	want := urlVal
	if actual.UpstreamURL != want {
		t.Errorf("Want %v. got: %v", want, actual.UpstreamURL)
	}
}

func Test_BufferHttpMode_CanBeSetToTrue(t *testing.T) {
	env := []string{
		"http_buffer_req_body=true",
	}

	actual, _ := New(env)
	want := true
	if actual.BufferHTTPBody != want {
		t.Errorf("Want %v. got: %v", want, actual.BufferHTTPBody)
	}
}

func Test_OperationalMode_AfterBurn(t *testing.T) {
	env := []string{
		"mode=afterburn",
	}

	actual, _ := New(env)

	if actual.OperationalMode != ModeAfterBurn {
		t.Errorf("Want %s. got: %s", WatchdogMode(ModeAfterBurn), WatchdogMode(actual.OperationalMode))
	}
}

func Test_OperationalMode_Static(t *testing.T) {
	env := []string{
		"mode=static",
	}

	actual, _ := New(env)

	if actual.OperationalMode != ModeStatic {
		t.Errorf("Want %s. got: %s", WatchdogMode(ModeStatic), WatchdogMode(actual.OperationalMode))
	}
}

func Test_ContentType_Default(t *testing.T) {
	env := []string{}

	actual, _ := New(env)

	if actual.ContentType != "application/octet-stream" {
		t.Errorf("Default (ContentType) Want %s. got: %s", actual.ContentType, "octet-stream")
	}
}

func Test_ContentType_Override(t *testing.T) {
	env := []string{
		"content_type=application/json",
	}

	actual, _ := New(env)

	if actual.ContentType != "application/json" {
		t.Errorf("(ContentType) Want %s. got: %s", actual.ContentType, "application/json")
	}
}

func Test_FunctionProcessLegacyName(t *testing.T) {
	env := []string{
		"fprocess=env",
	}

	actual, _ := New(env)

	if actual.FunctionProcess != "env" {
		t.Errorf("Want %s. got: %s", "env", actual.FunctionProcess)
	}
}

func Test_FunctionProcessAlternativeName(t *testing.T) {
	env := []string{
		"function_process=env",
	}

	actual, _ := New(env)

	if actual.FunctionProcess != "env" {
		t.Errorf("Want %s. got: %s", "env", actual.FunctionProcess)
	}
}

func Test_FunctionProcess_Arguments(t *testing.T) {

	cases := []struct {
		scenario      string
		env           string
		wantProcess   string
		wantArguments []string
	}{
		{
			scenario:      "no argument",
			env:           `fprocess=node`,
			wantProcess:   "node",
			wantArguments: []string{},
		},
		{
			scenario:      "one argument",
			env:           `fprocess=node index.js`,
			wantProcess:   "node",
			wantArguments: []string{"index.js"},
		},
		{
			scenario:      "multiple items with flag and value",
			env:           `fprocess=node index.js --this-is-a-flag=1234`,
			wantProcess:   "node",
			wantArguments: []string{"index.js", "--this-is-a-flag=1234"},
		},
		{
			scenario:      "one flag without value",
			env:           "fprocess=node --this-is-a-flag",
			wantProcess:   "node",
			wantArguments: []string{"--this-is-a-flag"},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.scenario, func(t *testing.T) {
			actual, _ := New([]string{testCase.env})

			process, args := actual.Process()
			if process != testCase.wantProcess {
				t.Errorf("Want process %v, got: %v", testCase.wantProcess, process)
			}

			if len(args) != len(testCase.wantArguments) {
				t.Errorf("Want %d args, got: %d args", len(testCase.wantArguments), len(args))
				t.Fail()
			} else {

				for i, wantArg := range testCase.wantArguments {
					if args[i] != wantArg {
						t.Errorf("Want arg%d: %s, got: %s", i, wantArg, args[i])
					}
				}

			}
		})
	}
}

func Test_PortOverride(t *testing.T) {
	env := []string{
		"port=8081",
	}

	actual, _ := New(env)

	if actual.TCPPort != 8081 {
		t.Errorf("Want %d. got: %d", 8081, actual.TCPPort)
	}
}

func Test_Timeouts(t *testing.T) {
	cases := []struct {
		readTimeout  time.Duration
		writeTimeout time.Duration
		execTimeout  time.Duration
		env          []string
		name         string
	}{
		{
			name:         "Defaults",
			readTimeout:  time.Second * 10,
			writeTimeout: time.Second * 10,
			execTimeout:  time.Second * 10,
			env:          []string{},
		},
		{
			name:         "Custom read-timeout",
			readTimeout:  time.Second * 5,
			writeTimeout: time.Second * 10,
			execTimeout:  time.Second * 10,
			env:          []string{"read_timeout=5s"},
		},
		{
			name:         "Custom write-timeout",
			readTimeout:  time.Second * 10,
			writeTimeout: time.Second * 5,
			execTimeout:  time.Second * 10,
			env:          []string{"write_timeout=5s"},
		},
		{
			name:         "Custom exec-timeout",
			readTimeout:  time.Second * 10,
			writeTimeout: time.Second * 10,
			execTimeout:  time.Second * 5,
			env:          []string{"exec_timeout=5s"},
		},
	}

	for _, testCase := range cases {
		actual, _ := New(testCase.env)
		if testCase.readTimeout != actual.HTTPReadTimeout {
			t.Errorf("(%s) HTTPReadTimeout want: %s, got: %s", testCase.name, actual.HTTPReadTimeout, testCase.readTimeout)
		}
		if testCase.writeTimeout != actual.HTTPWriteTimeout {
			t.Errorf("(%s) HTTPWriteTimeout want: %s, got: %s", testCase.name, actual.HTTPWriteTimeout, testCase.writeTimeout)
		}
		if testCase.execTimeout != actual.ExecTimeout {
			t.Errorf("(%s) ExecTimeout want: %s, got: %s", testCase.name, actual.ExecTimeout, testCase.execTimeout)
		}

	}

}

func Test_TestNonDurationValue_getDuration(t *testing.T) {
	want := 10 * time.Second
	env := map[string]string{"time": "10"}
	got := getDuration(env, "time", 5*time.Second)

	if want != got {
		t.Error(fmt.Sprintf("want: %q got: %q", want, got))
	}
}

func Test_TestDurationValue_getDuration(t *testing.T) {
	want := 10 * time.Second
	env := map[string]string{"time": "10s"}
	got := getDuration(env, "time", 5*time.Second)

	if want != got {
		t.Error(fmt.Sprintf("want: %q got: %q", want, got))
	}
}
func Test_TestNonParsableValue_getDuration(t *testing.T) {
	want := 5 * time.Second
	env := map[string]string{"time": "this is bad"}
	got := getDuration(env, "time", 5*time.Second)

	if want != got {
		t.Error(fmt.Sprintf("want: %q got: %q", want, got))
	}
}

func Test_TestMissingMapValue_getDuration(t *testing.T) {
	want := 5 * time.Second
	env := map[string]string{"time_is_missing": "10"}
	got := getDuration(env, "time", 5*time.Second)

	if want != got {
		t.Error(fmt.Sprintf("want: %q got: %q", want, got))
	}
}

func Test_IntAsString_parseIntOrDurationValue(t *testing.T) {
	want := 10 * time.Second

	got := parseIntOrDurationValue("10", 5*time.Second)
	if want != got {
		t.Error(fmt.Sprintf("want: %q got: %q", want, got))
	}
}

func Test_Duration_parseIntOrDurationValue(t *testing.T) {
	want := 10 * time.Second

	got := parseIntOrDurationValue("10s", 5*time.Second)
	if want != got {
		t.Error(fmt.Sprintf("want: %q got: %q", want, got))
	}

}

func Test_EmptyString_parseIntOrDurationValue(t *testing.T) {
	want := 5 * time.Second

	got := parseIntOrDurationValue("", 5*time.Second)
	if want != got {
		t.Error(fmt.Sprintf("want: %q got: %q", want, got))
	}
}

func Test_ZeroAsString_parseIntOrDurationValue(t *testing.T) {
	want := 0 * time.Second

	got := parseIntOrDurationValue("0", 5*time.Second)
	if want != got {
		t.Error(fmt.Sprintf("want: %q got: %q", want, got))
	}
}

func Test_NonParsableString_parseIntOrDurationValue(t *testing.T) {
	want := 5 * time.Second

	got := parseIntOrDurationValue("this is not good", 5*time.Second)
	if want != got {
		t.Error(fmt.Sprintf("want: %q got: %q", want, got))
	}
}
