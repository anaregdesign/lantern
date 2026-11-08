package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestGoroutinePreTopologyAdmission(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	observer := sha256.Sum256(binary)
	for _, test := range []struct {
		name    string
		passive string
		badHash bool
		want    int
	}{
		{name: "connected", passive: goroutineTestPassive()},
		{name: "outbound peer down despite two inbound streams", passive: strings.Replace(goroutineTestPassive(), "} 1", "} 0", 1), want: 3},
		{name: "inbound stream not ready", passive: strings.Replace(goroutineTestPassive(), "streams 2", "streams 1", 1), want: 3},
		{name: "missing metric is invalid", passive: strings.Replace(goroutineTestPassive(), "lantern_changes_active_streams 0\n", "", 1), want: 2},
		{name: "duplicate peer is invalid", passive: goroutineTestPassive() + "lantern_peer_connected{peer=\"https://peer-a:6381\"} 1\n", want: 2},
		{name: "foreign peer label is invalid", passive: strings.Replace(goroutineTestPassive(), "https://peer-a:6381", "https://foreign:6381", 1), want: 2},
		{name: "changed captured bytes are invalid", passive: goroutineTestPassive(), badHash: true, want: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newGoroutinePolicyFixture(t)
			ref := f.profile.Nodes[1].PassiveMetrics
			if err := os.WriteFile(ref.Path, []byte(test.passive), 0o600); err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256([]byte(test.passive))
			ref.SHA256 = hex.EncodeToString(digest[:])
			if test.badHash {
				ref.SHA256 = strings.Repeat("0", 64)
			}
			f.profile.ObserverSHA256 = hex.EncodeToString(observer[:])
			f.profile.ObserverGoVersion = runtime.Version()
			f.persist(t)
			ready, checkErr := checkGoroutinePreTopology(f.profile, f.dir)
			if ready != (test.want == 0) || (checkErr != nil) != (test.want == 2) {
				t.Fatalf("topology classification ready=%v err=%v; want CLI class %d", ready, checkErr, test.want)
			}
			out := filepath.Join(f.dir, "readiness.json")
			if code := runGoroutinePreCheck([]string{"-input", f.input.BaselineProfile.Path, "-out", out}); code != test.want {
				t.Fatalf("CLI classification=%d, want %d", code, test.want)
			}
			raw, err := os.ReadFile(out)
			var report struct {
				Verdict string `json:"verdict"`
			}
			if err != nil || json.Unmarshal(raw, &report) != nil || report.Verdict != map[int]string{0: "eligible", 3: "not_ready", 2: "invalid"}[test.want] {
				t.Fatalf("readiness evidence: %s %v", raw, err)
			}
		})
	}
}

func TestGoroutinePreTopologyCannotChoosePassingRuntimeCounts(t *testing.T) {
	f := newGoroutinePolicyFixture(t)
	f.pre[0].Goroutines = goroutineTestInt(129)
	f.pre[0].HeapAllocBytes = goroutineTestInt(1 << 30)
	f.persist(t)
	ready, err := checkGoroutinePreTopology(f.profile, f.dir)
	if err != nil || !ready {
		t.Fatalf("runtime values affected topology eligibility: ready=%v err=%v", ready, err)
	}
	if report := evaluateGoroutinePolicy(f.input); report.Verdict != "fail" {
		t.Fatalf("eligible topology weakened the existing PRE ceiling: %+v", report)
	}
}

func TestGoroutinePrePreparationBindsFirstEligibleCapture(t *testing.T) {
	for _, kind := range []string{"missing preparation", "changed canonical runtime", "skipped earlier eligible attempt"} {
		t.Run(kind, func(t *testing.T) {
			f := newGoroutinePolicyFixture(t)
			if err := validateGoroutinePrePreparation(f.input.Preparation, f.dir, f.profile, f.input.Pre); err != nil {
				t.Fatal("valid fixture preparation", err)
			}
			switch kind {
			case "missing preparation":
				f.input.Preparation = goroutineReference{}
			case "changed canonical runtime":
				f.pre[0].Goroutines = goroutineTestInt(1)
				f.input.Pre = goroutineFixtureRef(t, f.input.Pre.Path, f.pre, 0o644)
			case "skipped earlier eligible attempt":
				var preparation goroutinePrePreparation
				if err := goroutineDecode(f.input.Preparation, &preparation); err != nil {
					t.Fatal(err)
				}
				directory := filepath.Join(f.dir, "goroutine_policy_pre_attempts", "002")
				if err := os.Mkdir(directory, 0o700); err != nil {
					t.Fatal(err)
				}
				copyRef := func(ref goroutineReference) goroutineReference {
					raw, err := os.ReadFile(ref.Path)
					if err != nil {
						t.Fatal(err)
					}
					path := filepath.Join(directory, filepath.Base(ref.Path))
					if err := os.WriteFile(path, raw, 0o600); err != nil {
						t.Fatal(err)
					}
					return goroutineReference{Path: path, SHA256: ref.SHA256}
				}
				first := preparation.Attempts[0]
				var profile goroutineProfile
				if err := goroutineDecode(first.Profile, &profile); err != nil {
					t.Fatal(err)
				}
				for i, node := range profile.Nodes {
					ref := copyRef(*node.PassiveMetrics)
					profile.Nodes[i].PassiveMetrics = &ref
				}
				second := goroutinePreAttempt{Index: 2, Runtime: copyRef(first.Runtime), Readiness: copyRef(first.Readiness),
					Profile: goroutineFixtureRef(t, filepath.Join(directory, "goroutine_policy_baseline_profile.json"), profile, 0o600)}
				preparation.SelectedAttempt = 2
				preparation.Attempts = append(preparation.Attempts, second)
				f.input.Preparation = goroutineFixtureRef(t, f.input.Preparation.Path, preparation, 0o600)
			}
			if err := validateGoroutinePrePreparation(f.input.Preparation, f.dir, f.profile, f.input.Pre); err == nil {
				t.Fatal("altered preparation accepted", kind)
			}
		})
	}
}
