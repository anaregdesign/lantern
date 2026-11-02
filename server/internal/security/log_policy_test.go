package security

import "testing"

func TestLogPolicyClosedValues(t *testing.T) {
	for _, level := range []LogLevel{LogLevelDebug, LogLevelInfo, LogLevelWarn, LogLevelError} {
		policy := &LogPolicy{Version: LogPolicyVersion, Generation: 1, Level: level}
		change := &LogPolicyChange{Version: LogPolicyVersion, Level: level}
		if ValidateLogPolicyConfiguration(policy) != nil || ValidateLogPolicyChangeConfiguration(change) != nil {
			t.Fatal("canonical level rejected", level)
		}
	}
	for _, level := range []LogLevel{"", "INFO", "warning", "info+1", "trace", " debug", "debug\n"} {
		if ValidateLogPolicyConfiguration(&LogPolicy{Version: LogPolicyVersion, Generation: 1, Level: level}) == nil ||
			ValidateLogPolicyChangeConfiguration(&LogPolicyChange{Version: LogPolicyVersion, Level: level}) == nil {
			t.Fatal("unsupported level admitted", level)
		}
	}
	if !(*LogPolicy)(nil).valid() || ValidateLogPolicyConfiguration(nil) == nil || ValidateLogPolicyChangeConfiguration(nil) == nil {
		t.Fatal("absence acquired a managed value or mutation")
	}
	for _, policy := range []*LogPolicy{
		{Version: 0, Generation: 1, Level: LogLevelInfo},
		{Version: LogPolicyVersion + 1, Generation: 1, Level: LogLevelInfo},
		{Version: LogPolicyVersion, Level: LogLevelInfo},
	} {
		if ValidateLogPolicyConfiguration(policy) == nil {
			t.Fatal("invalid version/generation admitted")
		}
	}
	if ValidateLogPolicyChangeConfiguration(&LogPolicyChange{Version: LogPolicyVersion + 1, Level: LogLevelInfo}) == nil {
		t.Fatal("unknown mutation schema admitted")
	}
}

func TestLogPolicyClone(t *testing.T) {
	original := &LogPolicy{Version: LogPolicyVersion, Generation: 7, Level: LogLevelWarn}
	copy := original.clone()
	if copy == original || *copy != *original || (*LogPolicy)(nil).clone() != nil {
		t.Fatal("policy copy lost value or ownership")
	}
	copy.Generation = 8
	copy.Level = LogLevelDebug
	if original.Generation != 7 || original.Level != LogLevelWarn {
		t.Fatal("copy mutated original")
	}
}
