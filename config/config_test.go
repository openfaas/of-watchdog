package config

import "testing"

func TestNew(t *testing.T) {
	defaults, err := New([]string{})
	if err != nil {
		t.Errorf("Expected no errors")
	}
	if defaults.TCPPort != 8080 {
		t.Errorf("Want TCPPort: 8080, got: %d", defaults.TCPPort)
	}
}

var modes = map[int]string{
	ModeFork:        "fork",
	ModeServer:      "server",
	ModeSerializing: "serializing",
}

func watchdogMode(mode int) string {
	if result, ok := modes[mode]; ok {
		return result
	}
	return "unknown"
}

func TestWatchdogConfig_Process(t *testing.T) {
	w := WatchdogConfig{FunctionProcess: "qqq"}
	cmd, args := w.Process()
	if cmd != "qqq" || len(args) != 0 {
		t.Error("should have returned 0-len args")
	}
}

func Test_OperationalMode_Default(t *testing.T) {
	defaults, err := New([]string{})
	if err != nil {
		t.Errorf("Expected no errors")
	}
	if defaults.OperationalMode != ModeFork {
		t.Errorf("Want %s. got: %s", watchdogMode(ModeFork), watchdogMode(defaults.OperationalMode))
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

	if actual.OperationalMode != ModeServer {
		t.Errorf("Want %s. got: %s", watchdogMode(ModeServer), watchdogMode(actual.OperationalMode))
	}
}
