package envconfig

import (
	"log/slog"
	"os"
	"testing"
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
