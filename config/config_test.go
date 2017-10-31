package config

import "testing"

func TestNew(t *testing.T) {
	defaults, err := New([]string{})
	if err != nil {
		t.Errorf("Expected no errors")
	}
	if defaults.TCPPort != 8081 {
		t.Errorf("Want TCPPort: 8081, got: %d", defaults.TCPPort)
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

	defaults, err := New(env)
	if err != nil {
		t.Errorf("Expected no errors")
	}
	if defaults.OperationalMode != ModeStreaming {
		t.Errorf("Want %s. got: %s", WatchdogMode(ModeStreaming), WatchdogMode(defaults.OperationalMode))
	}
}
