package config

import "testing"
import "time"

func TestNew(t *testing.T) {
	defaults, err := New([]string{})
	if err != nil {
		t.Errorf("Expected no errors")
	}
	if defaults.TCPPort != 8080 {
		t.Errorf("Want TCPPort: 8080, got: %d", defaults.TCPPort)
	}
}

func Test_OperationalMode_Default(t *testing.T) {
	defaults, err := New([]string{})
	if err != nil {
		t.Errorf("Expected no errors")
	}
	if defaults.OperationalMode != ModeStreaming {
		t.Errorf("Want %s. got: %s", WatchdogMode(ModeStreaming), WatchdogMode(defaults.OperationalMode))
	}
}

func Test_OperationalMode_AfterBurn(t *testing.T) {
	env := []string{
		"mode=afterburn",
	}

	actual, err := New(env)
	if err != nil {
		t.Errorf("Expected no errors")
	}

	if actual.OperationalMode != ModeAfterBurn {
		t.Errorf("Want %s. got: %s", WatchdogMode(ModeAfterBurn), WatchdogMode(actual.OperationalMode))
	}

}

func Test_ContentType_Default(t *testing.T) {
	env := []string{}

	actual, err := New(env)
	if err != nil {
		t.Errorf("Expected no errors")
	}

	if actual.ContentType != "application/octet-stream" {
		t.Errorf("Default (ContentType) Want %s. got: %s", actual.ContentType, "octet-stream")
	}
}

func Test_ContentType_Override(t *testing.T) {
	env := []string{
		"content_type=application/json",
	}

	actual, err := New(env)
	if err != nil {
		t.Errorf("Expected no errors")
	}

	if actual.ContentType != "application/json" {
		t.Errorf("(ContentType) Want %s. got: %s", actual.ContentType, "application/json")
	}
}

func Test_FunctionProcessLegacyName(t *testing.T) {
	env := []string{
		"fprocess=env",
	}

	actual, err := New(env)
	if err != nil {
		t.Errorf("Expected no errors")
	}

	if actual.FunctionProcess != "env" {
		t.Errorf("Want %s. got: %s", "env", actual.FunctionProcess)
	}
}

func Test_FunctionProcessAlternativeName(t *testing.T) {
	env := []string{
		"function_process=env",
	}

	actual, err := New(env)
	if err != nil {
		t.Errorf("Expected no errors")
	}

	if actual.FunctionProcess != "env" {
		t.Errorf("Want %s. got: %s", "env", actual.FunctionProcess)
	}
}

func Test_PortOverride(t *testing.T) {
	env := []string{
		"port=8081",
	}

	actual, err := New(env)
	if err != nil {
		t.Errorf("Expected no errors")
	}

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
		actual, err := New(testCase.env)
		if err != nil {
			t.Errorf("(%s) Expected no errors", testCase.name)
		}
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
