package cmd

import (
	"errors"
	"strings"
	"testing"
)

func TestPrefixDeleteConfirmed(t *testing.T) {
	cases := []struct {
		name          string
		dryRun, yes   bool
		wantConfirmed bool
	}{
		{"neither/refused", false, false, false},
		{"dry-run/ok", true, false, true},
		{"yes/ok", false, true, true},
		{"both/ok", true, true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := prefixDeleteConfirmed(tc.dryRun, tc.yes); got != tc.wantConfirmed {
				t.Fatalf("prefixDeleteConfirmed(%v,%v) = %v, want %v",
					tc.dryRun, tc.yes, got, tc.wantConfirmed)
			}
		})
	}
}

func TestErrPrefixDeleteUnconfirmedMessage(t *testing.T) {
	// Operators see this verbatim — keep the failure mode self-explanatory
	// and make sure the gate names both escape hatches.
	msg := errPrefixDeleteUnconfirmed.Error()
	if !strings.Contains(msg, "--dry-run") || !strings.Contains(msg, "--yes") {
		t.Fatalf("safety-gate error must mention --dry-run and --yes, got: %s", msg)
	}
}

func TestVertexPrefixCommandsRegistered(t *testing.T) {
	// Guard against init() drift: scan/count/delete-prefix must be reachable
	// as subcommands of `vertex`.
	want := map[string]bool{"scan": false, "count": false, "delete-prefix": false}
	for _, c := range vertexCmd.Commands() {
		if _, ok := want[c.Name()]; ok {
			want[c.Name()] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("vertex subcommand %q not registered", name)
		}
	}
}

func TestVertexPrefixFlagDefaults(t *testing.T) {
	// scan
	if vertexScanLimit != 0 {
		t.Errorf("scan --limit default = %d, want 0 (server-side default)", vertexScanLimit)
	}
	if vertexScanCursor != "" {
		t.Errorf("scan --cursor default = %q, want empty", vertexScanCursor)
	}
	if vertexScanAll {
		t.Errorf("scan --all default = true, want false")
	}
	// delete-prefix — critical: --yes and --dry-run must default to false
	// so the safety gate is the default behaviour.
	if vertexDeletePrefixDryRun {
		t.Fatalf("delete-prefix --dry-run default = true, want false (safety gate must be the default)")
	}
	if vertexDeletePrefixYes {
		t.Fatalf("delete-prefix --yes default = true, want false (safety gate must be the default)")
	}
	if vertexDeletePrefixLimit != 0 {
		t.Errorf("delete-prefix --limit default = %d, want 0", vertexDeletePrefixLimit)
	}
}

// sanity: errors.Is unwraps the package-level sentinel as itself.
func TestErrPrefixDeleteUnconfirmedIs(t *testing.T) {
	if !errors.Is(errPrefixDeleteUnconfirmed, errPrefixDeleteUnconfirmed) {
		t.Fatal("errors.Is should reflexively match the sentinel")
	}
}
