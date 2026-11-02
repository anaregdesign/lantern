package envconfig

import (
	"log/slog"
	"os"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"
)

func TestInt(t *testing.T) {
	for _, tc := range []struct {
		name      string
		environ   []string
		want      int
		malformed bool
	}{
		{"unset", nil, 7, false}, {"valid", []string{"VALUE=42"}, 42, false},
		{"empty", []string{"VALUE="}, 7, false}, {"whitespace", []string{"VALUE=  "}, 7, false},
		{"malformed", []string{"VALUE=abc"}, 7, true}, {"negative", []string{"VALUE=-3"}, -3, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reader := NewReader(tc.environ)
			if got := reader.Int("VALUE", 7); got != tc.want {
				t.Fatalf("Int = %d, want %d", got, tc.want)
			}
			if got := len(reader.Findings()); (got != 0) != tc.malformed {
				t.Fatalf("findings = %+v", reader.Findings())
			}
		})
	}
}

func TestUint32(t *testing.T) {
	for _, tc := range []struct {
		input     string
		want      uint32
		malformed bool
	}{
		{"42", 42, false}, {"", 7, false}, {"abc", 7, true}, {"-3", 7, true},
		{"4294967295", 4294967295, false}, {"4294967296", 7, true},
	} {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			reader := NewReader([]string{"VALUE=" + tc.input})
			if got := reader.Uint32("VALUE", 7); got != tc.want {
				t.Fatalf("Uint32 = %d, want %d", got, tc.want)
			}
			if (len(reader.Findings()) != 0) != tc.malformed {
				t.Fatalf("findings = %+v", reader.Findings())
			}
		})
	}
	reader := NewReader(nil)
	if got := reader.Uint32("UNSET", 7); got != 7 {
		t.Fatalf("unset Uint32 = %d", got)
	}
}

func TestFloat(t *testing.T) {
	for _, tc := range []struct {
		input     string
		want      float64
		malformed bool
	}{
		{"3.14", 3.14, false}, {"10", 10, false}, {"", 2.5, false}, {"bad", 2.5, true},
	} {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			reader := NewReader([]string{"VALUE=" + tc.input})
			if got := reader.Float("VALUE", 2.5); got != tc.want {
				t.Fatalf("Float = %v, want %v", got, tc.want)
			}
			if (len(reader.Findings()) != 0) != tc.malformed {
				t.Fatalf("findings = %+v", reader.Findings())
			}
		})
	}
	if got := NewReader(nil).Float("UNSET", 1.5); got != 1.5 {
		t.Fatalf("unset Float = %v", got)
	}
}

func TestString(t *testing.T) {
	for _, tc := range []struct {
		name    string
		environ []string
		want    string
	}{
		{"unset", nil, "fallback"}, {"set", []string{"VALUE=explicit"}, "explicit"},
		{"empty", []string{"VALUE="}, ""}, {"whitespace", []string{"VALUE=  "}, "  "},
		{"equals", []string{"VALUE=a=b"}, "a=b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := NewReader(tc.environ).String("VALUE", "fallback"); got != tc.want {
				t.Fatalf("String = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBool(t *testing.T) {
	for _, input := range []string{"1", "true", "TRUE", " True ", "yes", "on"} {
		t.Run("truthy_"+input, func(t *testing.T) {
			t.Parallel()
			if !NewReader([]string{"VALUE=" + input}).Bool("VALUE", false) {
				t.Fatalf("Bool(%q) should be true", input)
			}
		})
	}
	for _, input := range []string{"0", "false", " False ", "no", "off"} {
		t.Run("falsy_"+input, func(t *testing.T) {
			t.Parallel()
			if NewReader([]string{"VALUE=" + input}).Bool("VALUE", true) {
				t.Fatalf("Bool(%q) should be false", input)
			}
		})
	}
	reader := NewReader(nil)
	if !reader.Bool("UNSET", true) || reader.Bool("UNSET", false) {
		t.Fatal("unset Boolean did not preserve default")
	}
	reader = NewReader([]string{"VALUE=maybe"})
	if !reader.Bool("VALUE", true) || len(reader.Findings()) != 1 {
		t.Fatalf("malformed Boolean = %+v", reader.Findings())
	}
	reader = NewReader([]string{"VALUE= "})
	if !reader.Bool("VALUE", true) || len(reader.Findings()) != 0 {
		t.Fatal("empty Boolean should use default without finding")
	}
}

func TestDuration(t *testing.T) {
	for _, tc := range []struct {
		input     string
		want      time.Duration
		malformed bool
	}{
		{"90s", 90 * time.Second, false}, {"", time.Minute, false}, {"soon", time.Minute, true},
	} {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			reader := NewReader([]string{"VALUE=" + tc.input})
			if got := reader.Duration("VALUE", time.Minute); got != tc.want {
				t.Fatalf("Duration = %v, want %v", got, tc.want)
			}
			if (len(reader.Findings()) != 0) != tc.malformed {
				t.Fatalf("findings = %+v", reader.Findings())
			}
		})
	}
	if got := NewReader(nil).Duration("UNSET", time.Minute); got != time.Minute {
		t.Fatalf("unset Duration = %v", got)
	}
}

func TestLevel(t *testing.T) {
	for _, tc := range []struct {
		input     string
		want      slog.Level
		malformed bool
	}{
		{"debug", slog.LevelDebug, false}, {" INFO ", slog.LevelInfo, false},
		{"warning", slog.LevelWarn, false}, {"error", slog.LevelError, false},
		{"", slog.LevelWarn, false}, {"verbose", slog.LevelWarn, true},
	} {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			reader := NewReader([]string{"VALUE=" + tc.input})
			if got := reader.Level("VALUE", slog.LevelWarn); got != tc.want {
				t.Fatalf("Level = %v, want %v", got, tc.want)
			}
			if (len(reader.Findings()) != 0) != tc.malformed {
				t.Fatalf("findings = %+v", reader.Findings())
			}
		})
	}
	if got := NewReader(nil).Level("UNSET", slog.LevelWarn); got != slog.LevelWarn {
		t.Fatalf("unset Level = %v", got)
	}
}

func TestLogLevel(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug}, {"DEBUG", slog.LevelDebug}, {" info ", slog.LevelInfo},
		{"warn", slog.LevelWarn}, {"warning", slog.LevelWarn}, {"error", slog.LevelError}, {"", slog.LevelInfo}, {"verbose", slog.LevelInfo},
	} {
		t.Run(tc.input, func(t *testing.T) {
			if got := LogLevel(tc.input); got != tc.want {
				t.Fatalf("LogLevel = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRegistryAndFindings(t *testing.T) {
	reader := NewReader([]string{"LANTERN_SET=9", "LANTERN_EMPTY=  ", "LANTERN_BAD=abc"})
	reader.Int("LANTERN_SET", 1)
	reader.Duration("LANTERN_DURATION", 5*time.Minute)
	reader.Int("LANTERN_UNSET", 1)
	reader.Int("LANTERN_EMPTY", 1)
	reader.Int("LANTERN_BAD", 1)
	reader.Int("LANTERN_SET", 2) // The first registration is the contract.
	reader.Malformed("LANTERN_CUSTOM", "zz", "not hex")
	specs := reader.Known()
	if len(specs) != 5 || specs[0] != (Spec{Key: "LANTERN_BAD", Kind: "int", Default: "1"}) || specs[1] != (Spec{Key: "LANTERN_DURATION", Kind: "duration", Default: "5m0s"}) || specs[3].Default != "1" {
		t.Fatalf("Known = %+v", specs)
	}
	wantFindings := []Finding{{Key: "LANTERN_BAD", Raw: "abc", Reason: "not a base-10 integer"}, {Key: "LANTERN_CUSTOM", Raw: "zz", Reason: "not hex"}}
	if !reflect.DeepEqual(reader.Findings(), wantFindings) {
		t.Fatalf("Findings = %+v", reader.Findings())
	}
	if want := []string{"LANTERN_BAD", "LANTERN_EMPTY", "LANTERN_SET"}; !reflect.DeepEqual(reader.SetKeys(), want) {
		t.Fatalf("SetKeys = %v", reader.SetKeys())
	}
	diagnostics := reader.Diagnostics()
	if !reflect.DeepEqual(diagnostics.Known, specs) || !reflect.DeepEqual(diagnostics.Findings, wantFindings) || !reflect.DeepEqual(diagnostics.SetKeys, reader.SetKeys()) {
		t.Fatalf("Diagnostics = %+v", diagnostics)
	}
	diagnostics.Known[0].Key = "changed"
	diagnostics.Findings[0].Raw = "changed"
	diagnostics.SetKeys[0] = "changed"
	specs[0].Key = "changed"
	findings := reader.Findings()
	findings[0].Raw = "changed"
	keys := reader.SetKeys()
	keys[0] = "changed"
	if reader.Known()[0].Key != "LANTERN_BAD" || reader.Findings()[0].Raw != "abc" || reader.SetKeys()[0] != "LANTERN_BAD" {
		t.Fatal("returned diagnostics mutated reader")
	}
}

func TestUnknownLanternVars(t *testing.T) {
	reader := NewReader([]string{
		"PATH=/usr/bin", "LANTERN_TEST_PORT=6380", "LANTERN_TEST_PROT=6380",
		"LANTERN_TOTALLY_DIFFERENT=1", "LANTERN_MCP_AGENT_ID=foo", "MALFORMED-NO-EQUALS",
	})
	reader.Int("LANTERN_TEST_PORT", 6380)
	got := reader.UnknownLanternVars("LANTERN_MCP_")
	want := []Unknown{{Key: "LANTERN_TEST_PROT", Suggestion: "LANTERN_TEST_PORT"}, {Key: "LANTERN_TOTALLY_DIFFERENT"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unknown = %+v, want %+v", got, want)
	}
	reader.String("LANTERN_TEST_PROT", "")
	if got := reader.UnknownLanternVars("LANTERN_MCP_"); len(got) != 1 || got[0].Key != "LANTERN_TOTALLY_DIFFERENT" {
		t.Fatalf("later component registration not observed: %+v", got)
	}
}

func TestRegisterStringDoesNotReadOrMarkConfiguredValue(t *testing.T) {
	reader := NewReader([]string{"LANTERN_CONDITIONAL_TEST=operator-secret"})
	reader.RegisterString("LANTERN_CONDITIONAL_TEST", "disabled-default")
	specs := reader.Known()
	if len(specs) != 1 || specs[0].Default != "disabled-default" || specs[0].Kind != "string" || len(reader.Findings()) != 0 || len(reader.SetKeys()) != 0 {
		t.Fatalf("conditional declaration drift: %+v", reader.Diagnostics())
	}
	if reader.String("LANTERN_CONDITIONAL_TEST", "disabled-default") != "operator-secret" {
		t.Fatal("declaration changed actual loading")
	}
}

func TestReaderSnapshot(t *testing.T) {
	input := []string{"LANTERN_VALUE=first", "LANTERN_VALUE=second", "EMPTY=", "WITH_EQUALS=a=b", "invalid"}
	reader := NewReader(input)
	input[0] = "LANTERN_VALUE=mutated"
	output := reader.Environ()
	output[0] = "LANTERN_VALUE=mutated-again"
	if value, present := reader.LookupEnv("LANTERN_VALUE"); !present || value != "first" {
		t.Fatalf("snapshot lookup = %q, %t", value, present)
	}
	if value, present := reader.LookupEnv("EMPTY"); !present || value != "" {
		t.Fatal("empty presence lost")
	}
	if _, present := reader.LookupEnv("ABSENT"); present {
		t.Fatal("absent key appeared")
	}
	if value, present := reader.LookupEnv("WITH_EQUALS"); !present || value != "a=b" {
		t.Fatal("value split at later equals")
	}
	if _, present := reader.LookupEnv("invalid"); present {
		t.Fatal("malformed input became a value")
	}
	if len(reader.Known()) != 0 || len(reader.SetKeys()) != 0 {
		t.Fatal("raw lookup registered settings")
	}
	t.Setenv("LANTERN_SNAPSHOT_TEST", "before")
	captured := NewReader(os.Environ())
	t.Setenv("LANTERN_SNAPSHOT_TEST", "after")
	if got := captured.String("LANTERN_SNAPSHOT_TEST", ""); got != "before" {
		t.Fatalf("process mutation changed captured value: %q", got)
	}
	processValue, processPresent := os.LookupEnv("lantern_snapshot_test")
	snapshotValue, snapshotPresent := NewReader(os.Environ()).LookupEnv("lantern_snapshot_test")
	if snapshotValue != processValue || snapshotPresent != processPresent {
		t.Fatalf("platform key lookup = %q, %t; process = %q, %t", snapshotValue, snapshotPresent, processValue, processPresent)
	}
}

func TestConcurrentReadersAreIndependent(t *testing.T) {
	const count = 32
	var workers sync.WaitGroup
	for i := range count {
		workers.Go(func() {
			key := "LANTERN_ONLY_" + strconv.Itoa(i)
			reader := NewReader([]string{key + "=bad", "LANTERN_SHARED=" + strconv.Itoa(i), "LANTERN_UNKNOWN="})
			reader.Int(key, 7)
			if got := reader.Int("LANTERN_SHARED", -1); got != i {
				t.Errorf("reader %d shared value = %d", i, got)
			}
			if findings := reader.Findings(); len(findings) != 1 || findings[0].Key != key {
				t.Errorf("reader %d findings = %+v", i, findings)
			}
			if known := reader.Known(); len(known) != 2 {
				t.Errorf("reader %d known = %+v", i, known)
			}
			if unknown := reader.UnknownLanternVars(); len(unknown) != 1 || unknown[0].Key != "LANTERN_UNKNOWN" {
				t.Errorf("reader %d unknown = %+v", i, unknown)
			}
		})
	}
	workers.Wait()
}

func TestConcurrentReaderDiagnostics(t *testing.T) {
	reader := NewReader([]string{"LANTERN_VALUE=bad"})
	const count = 32
	var workers sync.WaitGroup
	for range count {
		workers.Go(func() {
			reader.Int("LANTERN_VALUE", 7)
			reader.Diagnostics()
			reader.Known()
			reader.Findings()
			reader.SetKeys()
			reader.UnknownLanternVars()
		})
	}
	workers.Wait()
	if got := reader.Diagnostics(); len(got.Known) != 1 || len(got.Findings) != count || len(got.SetKeys) != 1 {
		t.Fatalf("concurrent diagnostics = %+v", got)
	}
}
