//go:build lantern_test_time && (linux || darwin)

package main

// Cross-cutting causal stop protocol for the controller, native lifecycle and
// separately owned membership publisher. It is not serving authority.
import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const controlledNativeOwnerControl = "/owner-control"

// This record proves only causal receipt of the original controller's stop
// request. It grants no authority and does not compare Mac and VM wall clocks.
type controlledNativeOwnerStop struct {
	Schema      int       `json:"schema"`
	Kind        string    `json:"kind"`
	Revision    string    `json:"revision"`
	Nonce       string    `json:"nonce"`
	PlannedStop time.Time `json:"planned_stop_at"`
	CleanupBy   time.Time `json:"cleanup_by"`
}

func controlledNativeOwnerControlDirectory(path string, stopped bool) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return errors.New("owner operational directory refused")
	}
	identity, ok := info.Sys().(*syscall.Stat_t)
	if !ok || identity.Uid != uint32(os.Getuid()) {
		return errors.New("owner operational directory identity refused")
	}
	entries, err := os.ReadDir(path)
	if err != nil || !stopped && len(entries) != 0 || stopped && (len(entries) != 1 || entries[0].Name() != "stop.json") {
		return errors.New("owner premature or unexpected stop metadata refused")
	}
	return nil
}

func controlledNativeOwnerStopAcknowledgement(directory, revision, nonce string, planned, cleanup time.Time) (string, error) {
	if err := controlledNativeOwnerControlDirectory(directory, true); err != nil {
		return "", err
	}
	raw, err := controlledNativeOwnerRead(filepath.Join(directory, "stop.json"), 4096)
	var value controlledNativeOwnerStop
	if err != nil || controlledNativeOwnerDecode(raw, &value) != nil || value.Schema != 1 || value.Kind != "controlled-native-provider-owner-stop" ||
		value.Revision != revision || value.Nonce != nonce || !value.PlannedStop.Equal(planned) || !value.CleanupBy.Equal(cleanup) || value.PlannedStop.Location() != time.UTC || value.CleanupBy.Location() != time.UTC {
		return "", errors.New("owner exact causal stop record refused")
	}
	return controlledGoogleDigest(raw), nil
}

func TestControlledNativeProviderOwnerStopAcknowledgement(t *testing.T) {
	privateDirectory := func(t *testing.T) string {
		t.Helper()
		dir := t.TempDir()
		if err := os.Chmod(dir, 0700); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	input, _ := unitControlledNativeProvider()
	value := controlledNativeOwnerStop{Schema: 1, Kind: "controlled-native-provider-owner-stop", Revision: input.Revision, Nonce: input.Nonce, PlannedStop: input.PlannedStop, CleanupBy: input.CleanupBy}
	ack := func(directory string) (string, error) {
		return controlledNativeOwnerStopAcknowledgement(directory, input.Revision, input.Nonce, input.PlannedStop, input.CleanupBy)
	}
	write := func(directory string, value controlledNativeOwnerStop) []byte {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(filepath.Join(directory, "stop.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	dir := privateDirectory(t)
	if err := controlledNativeOwnerControlDirectory(dir, false); err != nil {
		t.Fatal("empty operational directory", err)
	}
	if _, err := ack(dir); err == nil {
		t.Fatal("missing causal request accepted")
	}
	raw := write(dir, value)
	// The request's fixed endpoints are matched to input, not compared against
	// this process's wall clock (the fixture dates are deliberately independent).
	if got, err := ack(dir); err != nil || got != controlledGoogleDigest(raw) {
		t.Fatal("exact causal acknowledgement", got, err)
	}
	if controlledNativeOwnerControlDirectory(dir, false) == nil {
		t.Fatal("premature request admitted at startup")
	}
	for name, mutate := range map[string]func(*controlledNativeOwnerStop){
		"schema":  func(v *controlledNativeOwnerStop) { v.Schema++ },
		"kind":    func(v *controlledNativeOwnerStop) { v.Kind += "-other" },
		"source":  func(v *controlledNativeOwnerStop) { v.Revision = strings.Repeat("0", 40) },
		"cohort":  func(v *controlledNativeOwnerStop) { v.Nonce = strings.Repeat("0", 64) },
		"stop":    func(v *controlledNativeOwnerStop) { v.PlannedStop = v.PlannedStop.Add(time.Second) },
		"cleanup": func(v *controlledNativeOwnerStop) { v.CleanupBy = v.CleanupBy.Add(time.Second) },
	} {
		t.Run(name, func(t *testing.T) {
			dir := privateDirectory(t)
			bad := value
			mutate(&bad)
			write(dir, bad)
			if _, err := ack(dir); err == nil {
				t.Fatal("unbound stop request accepted")
			}
		})
	}
	for _, name := range []string{"unknown field", "insecure mode", "symlink", "hardlink", "extra file"} {
		t.Run(name, func(t *testing.T) {
			dir := privateDirectory(t)
			raw := write(dir, value)
			path := filepath.Join(dir, "stop.json")
			switch name {
			case "unknown field":
				var object map[string]any
				if json.Unmarshal(raw, &object) != nil {
					t.Fatal("fixture")
				}
				object["authority"] = true
				raw, _ = json.Marshal(object)
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "insecure mode":
				if err := os.Chmod(path, 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				real := filepath.Join(t.TempDir(), "request")
				if err := os.Rename(path, real); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(real, path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, filepath.Join(t.TempDir(), "copy")); err != nil {
					t.Fatal(err)
				}
			case "extra file":
				if err := os.WriteFile(filepath.Join(dir, "authority"), []byte("not authority"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := ack(dir); err == nil {
				t.Fatal("unsafe operational request accepted")
			}
		})
	}
}
