package main

// Shared only by the ordinary source child and the tagged functional harness.
// This file is compiled into tests, never the normal production executable.
import (
	"os"
	"strings"
	"testing"
)

func neutralizeInheritedProfileControls() (restore func(), err error) {
	var prior [][2]string
	isControl := func(key string) bool {
		upper := strings.ToUpper(key)
		return strings.HasPrefix(upper, "LANTERN_") || strings.HasPrefix(upper, "CONTROLLED_GOOGLE_") || strings.HasPrefix(upper, "CONTROLLED_NATIVE_PROVIDER_")
	}
	for _, entry := range os.Environ() {
		key, value, _ := strings.Cut(entry, "=")
		if isControl(key) {
			prior = append(prior, [2]string{key, value})
		}
	}
	restore = func() {
		// A harness can install its own controls after neutralization. Teardown
		// removes those too before restoring the complete original snapshot.
		for _, entry := range os.Environ() {
			key, _, _ := strings.Cut(entry, "=")
			if isControl(key) {
				_ = os.Unsetenv(key)
			}
		}
		for _, entry := range prior {
			_ = os.Setenv(entry[0], entry[1])
		}
	}
	for _, entry := range prior {
		if err := os.Unsetenv(entry[0]); err != nil {
			restore()
			return restore, err
		}
	}
	return restore, nil
}

func TestInheritedProfileControlsNeutralized(t *testing.T) {
	controls := []string{"LANTERN_UNIT_CONTROL", "CONTROLLED_GOOGLE_CHILD_INPUT", "CONTROLLED_GOOGLE_DESCRIBE", "CONTROLLED_NATIVE_PROVIDER_CHILD_INPUT", "CONTROLLED_NATIVE_PROVIDER_DESCRIBE", "controlled_native_provider_unit"}
	for _, key := range controls {
		t.Setenv(key, "UNIT_ONLY_INHERITED_CONTROL")
	}
	for _, key := range []string{"UNIT_ORDINARY_ENV", "LANTERNX", "CONTROLLED_GOOGLEX", "CONTROLLED_NATIVE_PROVIDERX"} {
		t.Setenv(key, "UNIT_UNRELATED_VALUE")
	}
	restore, err := neutralizeInheritedProfileControls()
	if err != nil {
		t.Fatal("UNIT inherited control neutralization failed")
	}
	t.Cleanup(restore)
	for _, key := range controls {
		if _, exists := os.LookupEnv(key); exists {
			t.Fatal("UNIT inherited profile control survived")
		}
	}
	for _, key := range []string{"UNIT_ORDINARY_ENV", "LANTERNX", "CONTROLLED_GOOGLEX", "CONTROLLED_NATIVE_PROVIDERX"} {
		if os.Getenv(key) != "UNIT_UNRELATED_VALUE" {
			t.Fatal("UNIT unrelated environment was changed")
		}
	}
	lateControls := []string{"LANTERN_UNIT_CREATED_AFTER_CLEAR", "CONTROLLED_NATIVE_PROVIDER_UNIT_CREATED_AFTER_CLEAR"}
	for _, key := range lateControls {
		t.Setenv(key, "UNIT_LATE_OWNED_CONTROL")
	}
	t.Setenv("UNIT_ORDINARY_ENV", "UNIT_UPDATED_ORDINARY_VALUE")
	// Restore is exercised, not merely registered as teardown. Repeated restore
	// remains safe for the functional harness's ordinary t.Cleanup ownership.
	restore()
	restore()
	for _, key := range controls {
		if os.Getenv(key) != "UNIT_ONLY_INHERITED_CONTROL" {
			t.Fatal("UNIT prior inherited environment not restored")
		}
	}
	for _, key := range lateControls {
		if _, exists := os.LookupEnv(key); exists {
			t.Fatal("UNIT later owned control leaked through restore")
		}
	}
	if os.Getenv("UNIT_ORDINARY_ENV") != "UNIT_UPDATED_ORDINARY_VALUE" {
		t.Fatal("UNIT restore reverted unrelated environment")
	}
}
