package envconfig

import (
	"log/slog"
	"os"
	"testing"
	"time"
)

func TestInt(t *testing.T) {
	const key = "LANTERN_TEST_INT"
	cases := []struct {
		name   string
		set    bool
		value  string
		def    int
		expect int
	}{
		{"unset returns default", false, "", 7, 7},
		{"valid value", true, "42", 7, 42},
		{"empty value falls back", true, "", 7, 7},
		{"non-numeric falls back", true, "abc", 7, 7},
		{"negative value", true, "-3", 0, -3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(key, tc.value)
			} else {
				unset(t, key)
			}
			if got := Int(key, tc.def); got != tc.expect {
				t.Fatalf("Int(%q)=%d, want %d", tc.value, got, tc.expect)
			}
		})
	}
}

func TestUint32(t *testing.T) {
	const key = "LANTERN_TEST_UINT32"
	cases := []struct {
		name   string
		set    bool
		value  string
		def    uint32
		expect uint32
	}{
		{"unset returns default", false, "", 7, 7},
		{"valid value", true, "42", 7, 42},
		{"empty value falls back", true, "", 7, 7},
		{"non-numeric falls back", true, "abc", 7, 7},
		{"negative falls back", true, "-3", 7, 7},
		{"max uint32 is honoured", true, "4294967295", 7, 4294967295},
		{"above uint32 range falls back", true, "4294967296", 7, 7},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(key, tc.value)
			} else {
				unset(t, key)
			}
			if got := Uint32(key, tc.def); got != tc.expect {
				t.Fatalf("Uint32(%q)=%d, want %d", tc.value, got, tc.expect)
			}
		})
	}
}

func TestFloat(t *testing.T) {
	const key = "LANTERN_TEST_FLOAT"
	cases := []struct {
		name   string
		set    bool
		value  string
		def    float64
		expect float64
	}{
		{"unset returns default", false, "", 1.5, 1.5},
		{"valid value", true, "3.14", 0, 3.14},
		{"int parses as float", true, "10", 0, 10},
		{"garbage falls back", true, "not-a-float", 2.5, 2.5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(key, tc.value)
			} else {
				unset(t, key)
			}
			if got := Float(key, tc.def); got != tc.expect {
				t.Fatalf("Float(%q)=%v, want %v", tc.value, got, tc.expect)
			}
		})
	}
}

func TestString(t *testing.T) {
	const key = "LANTERN_TEST_STRING"
	t.Run("unset returns default", func(t *testing.T) {
		unset(t, key)
		if got := String(key, "fallback"); got != "fallback" {
			t.Fatalf("got %q, want fallback", got)
		}
	})
	t.Run("set value wins", func(t *testing.T) {
		t.Setenv(key, "explicit")
		if got := String(key, "fallback"); got != "explicit" {
			t.Fatalf("got %q, want explicit", got)
		}
	})
	t.Run("empty string is honoured", func(t *testing.T) {
		t.Setenv(key, "")
		if got := String(key, "fallback"); got != "" {
			t.Fatalf("got %q, want empty string", got)
		}
	})
}

func TestBool(t *testing.T) {
	const key = "LANTERN_TEST_BOOL"
	truthy := []string{"1", "true", "TRUE", " True ", "yes", "on"}
	falsy := []string{"0", "false", " False ", "no", "off"}
	for _, v := range truthy {
		t.Run("truthy_"+v, func(t *testing.T) {
			t.Setenv(key, v)
			if !Bool(key, false) {
				t.Fatalf("Bool(%q) should be true", v)
			}
		})
	}
	for _, v := range falsy {
		t.Run("falsy_"+v, func(t *testing.T) {
			t.Setenv(key, v)
			if Bool(key, true) {
				t.Fatalf("Bool(%q) should be false", v)
			}
		})
	}
	t.Run("unset returns default", func(t *testing.T) {
		unset(t, key)
		if !Bool(key, true) {
			t.Fatal("unset should return default true")
		}
		if Bool(key, false) {
			t.Fatal("unset should return default false")
		}
	})
	t.Run("garbage returns default", func(t *testing.T) {
		t.Setenv(key, "maybe")
		if !Bool(key, true) {
			t.Fatal("garbage should return default")
		}
	})
}

func TestLogLevel(t *testing.T) {
	cases := []struct {
		in     string
		expect slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{" info ", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"error", slog.LevelError},
		{"", slog.LevelInfo},
		{"verbose", slog.LevelInfo},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := LogLevel(tc.in); got != tc.expect {
				t.Fatalf("LogLevel(%q)=%v, want %v", tc.in, got, tc.expect)
			}
		})
	}
}

func TestDuration(t *testing.T) {
	const key = "LANTERN_TEST_DURATION"
	cases := []struct {
		name   string
		set    bool
		value  string
		def    time.Duration
		expect time.Duration
	}{
		{"unset returns default", false, "", time.Minute, time.Minute},
		{"valid value", true, "90s", time.Minute, 90 * time.Second},
		{"empty value falls back", true, "", time.Minute, time.Minute},
		{"garbage falls back", true, "soon", time.Minute, time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.set {
				t.Setenv(key, tc.value)
			} else {
				unset(t, key)
			}
			if got := Duration(key, tc.def); got != tc.expect {
				t.Fatalf("Duration(%q)=%v, want %v", tc.value, got, tc.expect)
			}
		})
	}
}

func TestLevel(t *testing.T) {
	const key = "LANTERN_TEST_LEVEL"
	t.Run("unset returns default", func(t *testing.T) {
		unset(t, key)
		if got := Level(key, slog.LevelWarn); got != slog.LevelWarn {
			t.Fatalf("got %v, want warn", got)
		}
	})
	t.Run("valid value", func(t *testing.T) {
		t.Setenv(key, "debug")
		if got := Level(key, slog.LevelInfo); got != slog.LevelDebug {
			t.Fatalf("got %v, want debug", got)
		}
	})
	t.Run("garbage falls back and records a finding", func(t *testing.T) {
		ResetForTesting()
		t.Setenv(key, "verbose")
		if got := Level(key, slog.LevelInfo); got != slog.LevelInfo {
			t.Fatalf("got %v, want info", got)
		}
		fs := Findings()
		if len(fs) != 1 || fs[0].Key != key || fs[0].Raw != "verbose" {
			t.Fatalf("findings = %+v, want one for %s", fs, key)
		}
	})
}

func TestRegistryAndFindings(t *testing.T) {
	t.Run("registry captures key, kind, and default", func(t *testing.T) {
		ResetForTesting()
		unset(t, "LANTERN_TEST_REG_INT")
		unset(t, "LANTERN_TEST_REG_DUR")
		_ = Int("LANTERN_TEST_REG_INT", 42)
		_ = Duration("LANTERN_TEST_REG_DUR", 5*time.Minute)
		specs := Known()
		if len(specs) != 2 {
			t.Fatalf("Known() = %+v, want 2 specs", specs)
		}
		// Known() sorts by key: DUR < INT.
		if specs[0].Key != "LANTERN_TEST_REG_DUR" || specs[0].Kind != "duration" || specs[0].Default != "5m0s" {
			t.Fatalf("spec[0] = %+v", specs[0])
		}
		if specs[1].Key != "LANTERN_TEST_REG_INT" || specs[1].Kind != "int" || specs[1].Default != "42" {
			t.Fatalf("spec[1] = %+v", specs[1])
		}
	})

	t.Run("malformed set values are recorded, unset and empty are not", func(t *testing.T) {
		ResetForTesting()
		unset(t, "LANTERN_TEST_F_UNSET")
		_ = Int("LANTERN_TEST_F_UNSET", 1)
		t.Setenv("LANTERN_TEST_F_EMPTY", "  ")
		_ = Int("LANTERN_TEST_F_EMPTY", 1)
		t.Setenv("LANTERN_TEST_F_BAD", "abc")
		_ = Int("LANTERN_TEST_F_BAD", 1)
		fs := Findings()
		if len(fs) != 1 || fs[0].Key != "LANTERN_TEST_F_BAD" || fs[0].Raw != "abc" {
			t.Fatalf("findings = %+v, want exactly the malformed set value", fs)
		}
	})

	t.Run("SetKeys reports set variables without values", func(t *testing.T) {
		ResetForTesting()
		t.Setenv("LANTERN_TEST_SET", "9")
		unset(t, "LANTERN_TEST_UNSET")
		_ = Int("LANTERN_TEST_SET", 1)
		_ = Int("LANTERN_TEST_UNSET", 1)
		keys := SetKeys()
		if len(keys) != 1 || keys[0] != "LANTERN_TEST_SET" {
			t.Fatalf("SetKeys() = %v", keys)
		}
	})

	t.Run("Malformed records custom-parser failures", func(t *testing.T) {
		ResetForTesting()
		Malformed("LANTERN_TEST_CUSTOM", "zz", "not hex")
		fs := Findings()
		if len(fs) != 1 || fs[0].Reason != "not hex" {
			t.Fatalf("findings = %+v", fs)
		}
	})
}

func TestUnknownLanternVars(t *testing.T) {
	ResetForTesting()
	unset(t, "LANTERN_TEST_PORT")
	_ = Int("LANTERN_TEST_PORT", 6380)

	environ := []string{
		"PATH=/usr/bin",               // non-LANTERN: ignored
		"LANTERN_TEST_PORT=6380",      // registered: ignored
		"LANTERN_TEST_PROT=6380",      // typo of a registered key: flagged with suggestion
		"LANTERN_TOTALLY_DIFFERENT=1", // unknown, nothing close: flagged without suggestion
		"LANTERN_MCP_AGENT_ID=foo",    // foreign namespace: ignored
		"MALFORMED-NO-EQUALS",         // not KEY=value: ignored
	}
	got := UnknownLanternVars(environ, "LANTERN_MCP_")
	if len(got) != 2 {
		t.Fatalf("UnknownLanternVars = %+v, want 2 entries", got)
	}
	if got[0].Key != "LANTERN_TEST_PROT" || got[0].Suggestion != "LANTERN_TEST_PORT" {
		t.Fatalf("typo entry = %+v, want suggestion LANTERN_TEST_PORT", got[0])
	}
	if got[1].Key != "LANTERN_TOTALLY_DIFFERENT" || got[1].Suggestion != "" {
		t.Fatalf("unrelated entry = %+v, want no suggestion", got[1])
	}
}

// unset removes the env var for the duration of the test, restoring its
// prior value on cleanup.
func unset(t *testing.T, key string) {
	t.Helper()
	prev, had := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(key, prev)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}
