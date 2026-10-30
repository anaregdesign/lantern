package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type goroutinePolicyFixture struct {
	dir         string
	input       goroutinePolicyInput
	profile     goroutineProfile
	before      goroutineProfile
	barrier     goroutineBarrier
	returns     []goroutinePhaseReturn
	pre, legacy []runtimeSnapshot
	steady      steadyMetricsReport
	captures    []goroutinePostCapture
	profiles    []goroutineProfile
	scenario    map[string]any
}

func goroutineReviewedScenario(t *testing.T, name string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "scenarios", name+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func goroutineTestInt(v int64) *int64 { return &v }
func goroutineTestExit(v int) *int    { return &v }

func goroutineFixtureRef(t *testing.T, path string, value any, mode os.FileMode) goroutineReference {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	return goroutineReference{Path: path, SHA256: hex.EncodeToString(digest[:])}
}

func newGoroutinePolicyFixture(t *testing.T) *goroutinePolicyFixture {
	t.Helper()
	if !retainedEvidenceSupported {
		t.Skip("operative owned evidence supports Linux/Darwin")
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	f := &goroutinePolicyFixture{dir: dir}
	f.scenario = goroutineReviewedScenario(t, "receipt_admission_lookup")
	f.profile = goroutineProfile{Schema: 1, Protocol: goroutineOperationalProtocol, Scenario: "receipt_admission_lookup", Family: "receipt_edge_delete", ImageScope: "normal", MeasurementProtocol: "none", SourceCommit: strings.Repeat("a", 40), SourceTree: strings.Repeat("b", 40), ImageID: "sha256:" + strings.Repeat("c", 64), ConfigSHA256: strings.Repeat("d", 64), ScenarioSHA256: strings.Repeat("e", 64), ObserverSHA256: strings.Repeat("f", 64), ObserverGoVersion: "go1.27.3", MetricsTransport: "operator_docker_exec", BridgePID: 100, BridgePorts: []int{9390, 9391, 9392}, CPUSet: "0-3", RuntimeUser: "501:20"}
	for i := 0; i < 3; i++ {
		path := filepath.Join(dir, fmt.Sprintf("passive-%d.txt", i))
		data := []byte(goroutineTestPassive())
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		ref := goroutineReference{Path: path, SHA256: hex.EncodeToString(digest[:])}
		f.profile.Nodes = append(f.profile.Nodes, goroutineNodeProfile{Endpoint: fmt.Sprintf("localhost:%d", 9390+i), ContainerID: fmt.Sprintf("%064x", i+1), PID: int64(200 + i), StartedAt: "2026-10-30T00:00:00Z", ExpectedPeers: []string{"https://peer-a:6381", "https://peer-b:6381"}, PassiveMetrics: &ref})
		row := runtimeSnapshot{Endpoint: f.profile.Nodes[i].Endpoint, Goroutines: goroutineTestInt(50), HeapAllocBytes: goroutineTestInt(100), HeapInuseBytes: 100, HeapObjects: 1}
		f.pre = append(f.pre, row)
		f.legacy = append(f.legacy, row)
	}
	f.before = f.profile
	f.before.Nodes = append([]goroutineNodeProfile(nil), f.profile.Nodes...)
	for i := range f.before.Nodes {
		f.before.Nodes[i].PassiveMetrics = nil
	}
	f.steady = steadyMetricsReport{IntervalMS: 5000, DurationMS: 45000, Endpoints: []string{"localhost:9390", "localhost:9391", "localhost:9392"}}
	for i := 0; i <= 9; i++ {
		round := metricsRound{ElapsedMS: int64(i * 5000)}
		for _, n := range f.profile.Nodes {
			round.Replicas = append(round.Replicas, runtimeMetrics{Endpoint: n.Endpoint, Goroutines: 66, HeapAllocBytes: 100})
		}
		f.steady.Samples = append(f.steady.Samples, round)
	}
	complete := true
	f.barrier = goroutineBarrier{Schema: 1, Protocol: goroutineOperationalProtocol, Scope: "declared_client_work_only_not_global_idle", T0NS: int64(time.Second), SemanticComplete: &complete, PendingRPC: goroutineTestInt(0)}
	for _, kind := range []string{"producer", "subscriber", "chaos"} {
		count := int64(0)
		if kind == "producer" {
			count = 2
		}
		f.barrier.OwnedWork = append(f.barrier.OwnedWork, goroutineOwnedWork{Kind: kind, Declared: goroutineTestInt(count), Reaped: goroutineTestInt(count), PendingRPC: goroutineTestInt(0), ExitFailures: goroutineTestInt(0)})
	}
	for _, phase := range []string{"warmup", "steady"} {
		report := summary{Count: 10, StatusCodeDistribution: map[string]int64{"OK": 10}}
		f.barrier.WorkReports = append(f.barrier.WorkReports, goroutineWorkReports{Phase: phase, Admission: goroutineFixtureRef(t, filepath.Join(dir, phase+"-admission.json"), report, 0o644), Lookup: goroutineFixtureRef(t, filepath.Join(dir, phase+"-lookup.json"), report, 0o644)})
	}
	for i, phase := range []string{"warmup", "steady"} {
		returned := true
		f.returns = append(f.returns, goroutinePhaseReturn{Schema: 1, Protocol: goroutineOperationalProtocol, Phase: phase, Scope: "synchronous_receipt_phase_only", StartedNS: int64(100 + i*200), FinishedNS: int64(200 + i*200), ExitCode: goroutineTestExit(0), CoordinatorReturned: &returned})
	}
	f.input = goroutinePolicyInput{Schema: 1, Protocol: goroutineOperationalProtocol, WorkloadReport: goroutineFixtureRef(t, filepath.Join(dir, "workload.json"), map[string]string{"verdict": "pass"}, 0o644), OwnerEvidence: []goroutineReference{goroutineFixtureRef(t, filepath.Join(dir, "source-test-ref.json"), map[string]string{"scope": "source reference only, not normal join certification"}, 0o644)}}
	for _, offset := range []int{5, 10} {
		capture := goroutinePostCapture{Schema: 1, Protocol: goroutineOperationalProtocol, PolicyRole: "operative_normal_component", OffsetSeconds: offset, StartedUTC: "2026-10-30T00:00:01Z", DurationNS: int64(time.Millisecond), Verdict: "captured"}
		for _, n := range f.profile.Nodes {
			p, err := parseGoroutinePassive([]byte(goroutineTestPassive()))
			if err != nil {
				t.Fatal(err)
			}
			capture.Replicas = append(capture.Replicas, goroutinePostReplica{Runtime: runtimeMetrics{Endpoint: n.Endpoint, Goroutines: 65, HeapAllocBytes: 100}, Passive: p})
		}
		f.captures = append(f.captures, capture)
		f.profiles = append(f.profiles, f.before)
	}
	f.persist(t)
	return f
}

func (f *goroutinePolicyFixture) persist(t *testing.T) {
	t.Helper()
	write := func(name string, value any, mode os.FileMode) goroutineReference {
		return goroutineFixtureRef(t, filepath.Join(f.dir, name), value, mode)
	}
	scenario := write("scenario.resolved.json", f.scenario, 0o644)
	f.profile.ScenarioSHA256 = scenario.SHA256
	f.before.ScenarioSHA256 = scenario.SHA256
	for i := range f.profiles {
		f.profiles[i].ScenarioSHA256 = scenario.SHA256
	}
	f.input.Pre = write("runtime_pre.json", f.pre, 0o644)
	f.input.LegacyPost = write("runtime_post.json", f.legacy, 0o644)
	f.input.Steady = write("runtime_steady.json", f.steady, 0o644)
	for i, returned := range f.returns {
		f.barrier.WorkReports[i].CoordinatorReturn = write(returned.Phase+"-return.json", returned, 0o600)
	}
	f.input.BaselineProfile = write("profile-pre.json", f.profile, 0o600)
	f.input.BeforePostProfile = write("profile-before.json", f.before, 0o600)
	f.input.Barrier = write("barrier.json", f.barrier, 0o600)
	f.input.Posts = nil
	for i, offset := range []int{5, 10} {
		f.captures[i].Profile = f.input.BeforePostProfile
		capture := write(fmt.Sprintf("post-%d.json", offset), f.captures[i], 0o600)
		profile := write(fmt.Sprintf("profile-%d.json", offset), f.profiles[i], 0o600)
		start := f.barrier.T0NS + int64(offset)*int64(time.Second)
		f.input.Posts = append(f.input.Posts, goroutinePostInput{OffsetSeconds: offset, StartNS: start, EndNS: start + int64(2*time.Millisecond), Capture: capture, Profile: profile})
	}
}

func TestGoroutinePolicyOperationalBoundaries(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*goroutinePolicyFixture)
		want   string
	}{
		{"nominal", func(*goroutinePolicyFixture) {}, "pass"},
		{"128", func(f *goroutinePolicyFixture) { f.steady.Samples[2].Replicas[0].Goroutines = 128 }, "pass"},
		{"129_recovers", func(f *goroutinePolicyFixture) { f.steady.Samples[2].Replicas[0].Goroutines = 129 }, "fail"},
		{"PRE129", func(f *goroutinePolicyFixture) { f.pre[0].Goroutines = goroutineTestInt(129) }, "fail"},
		{"legacy_POST129", func(f *goroutinePolicyFixture) { f.legacy[0].Goroutines = goroutineTestInt(129) }, "fail"},
		{"legacy_POST15", func(f *goroutinePolicyFixture) { f.legacy[0].Goroutines = goroutineTestInt(65) }, "pass"},
		{"legacy_POST128_delta15", func(f *goroutinePolicyFixture) {
			f.pre[0].Goroutines = goroutineTestInt(113)
			f.legacy[0].Goroutines = goroutineTestInt(128)
			f.captures[0].Replicas[0].Runtime.Goroutines = 113
			f.captures[1].Replicas[0].Runtime.Goroutines = 113
		}, "pass"},
		{"legacy_POST16_later_zero", func(f *goroutinePolicyFixture) {
			f.legacy[0].Goroutines = goroutineTestInt(66)
			f.captures[0].Replicas[0].Runtime.Goroutines = 50
			f.captures[1].Replicas[0].Runtime.Goroutines = 50
		}, "fail"},
		{"post5_16", func(f *goroutinePolicyFixture) { f.captures[0].Replicas[0].Runtime.Goroutines = 66 }, "fail"},
		{"post10_16", func(f *goroutinePolicyFixture) { f.captures[1].Replicas[0].Runtime.Goroutines = 66 }, "fail"},
		{"unjoined_small_scalar", func(f *goroutinePolicyFixture) { f.barrier.OwnedWork[0].Reaped = goroutineTestInt(1) }, "fail"},
		{"pending_small_scalar", func(f *goroutinePolicyFixture) { f.barrier.PendingRPC = goroutineTestInt(1) }, "fail"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newGoroutinePolicyFixture(t)
			test.change(f)
			f.persist(t)
			r := evaluateGoroutinePolicy(f.input)
			if r.Verdict != test.want {
				t.Fatal(r.Verdict, r.Failures, r.MissingPremises)
			}
			if r.PolicyRole != "operative_normal_component" || r.Qualification || r.ReleaseAdmission {
				t.Fatal("component claimed whole qualification")
			}
		})
	}
}

func TestGoroutinePolicyReviewedFamilyProfile(t *testing.T) {
	for _, test := range []struct {
		user string
		want string
	}{
		{"1000:1000", "pass"}, {"0:0", "incomplete"}, {"0:1000", "incomplete"},
		{"1000", "incomplete"}, {"1000:-1", "incomplete"}, {"01:1000", "incomplete"}, {"1000:01000", "incomplete"},
	} {
		t.Run("runtime_user_"+test.user, func(t *testing.T) {
			f := newGoroutinePolicyFixture(t)
			f.profile.RuntimeUser, f.before.RuntimeUser = test.user, test.user
			for i := range f.profiles {
				f.profiles[i].RuntimeUser = test.user
			}
			f.persist(t)
			if got := evaluateGoroutinePolicy(f.input); got.Verdict != test.want {
				t.Fatal(got)
			}
		})
	}
	for scenario, family := range map[string]string{"receipt_vertex_put_admission_lookup": "receipt_vertex_put", "receipt_vertex_delete_admission_lookup": "receipt_vertex_delete", "receipt_admission_lookup": "receipt_edge_delete", "receipt_edge_add_admission_lookup": "receipt_edge_add"} {
		t.Run(scenario, func(t *testing.T) {
			f := newGoroutinePolicyFixture(t)
			f.scenario = goroutineReviewedScenario(t, scenario)
			f.profile.Scenario, f.profile.Family = scenario, family
			f.before.Scenario, f.before.Family = scenario, family
			for i := range f.profiles {
				f.profiles[i].Scenario, f.profiles[i].Family = scenario, family
			}
			f.persist(t)
			if got := evaluateGoroutinePolicy(f.input); got.Verdict != "pass" {
				t.Fatal(got)
			}
		})
	}
	for _, test := range []struct {
		name   string
		change func(map[string]any)
	}{
		{"rps", func(d map[string]any) { d["phases"].(map[string]any)["steady"].(map[string]any)["rps"] = 201 }},
		{"workers", func(d map[string]any) { d["phases"].(map[string]any)["steady"].(map[string]any)["concurrency"] = 13 }},
		{"duration", func(d map[string]any) { d["phases"].(map[string]any)["cooldown"] = "9s" }},
		{"WAL", func(d map[string]any) {
			d["cluster"].(map[string]any)["receipt_wal"].(map[string]any)["retention"] = "2h"
		}},
		{"perf", func(d map[string]any) { d["perf_gate"].(map[string]any)["min_steady_rps_total"] = 149 }},
		{"endpoints", func(d map[string]any) {
			d["target"].(map[string]any)["endpoints"] = []string{"localhost:6380", "localhost:6381"}
		}},
		{"policy", func(d map[string]any) { d["goroutine_policy"].(map[string]any)["sampled_absolute_max"] = 129 }},
		{"extra_native_override", func(d map[string]any) { d["environment"] = map[string]any{"LANTERN_AUTH_MODE": "off"} }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newGoroutinePolicyFixture(t)
			test.change(f.scenario)
			f.persist(t)
			if got := evaluateGoroutinePolicy(f.input); got.Verdict != "incomplete" {
				t.Fatal(got)
			}
		})
	}
}

func TestGoroutinePolicyIncompletePremises(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*goroutinePolicyFixture)
	}{
		{"restart", func(f *goroutinePolicyFixture) {
			f.profiles[1].Nodes = append([]goroutineNodeProfile(nil), f.before.Nodes...)
			f.profiles[1].Nodes[0].PID++
		}},
		{"configuration", func(f *goroutinePolicyFixture) { f.profiles[1].ConfigSHA256 = strings.Repeat("0", 64) }},
		{"same_PID_counter_1_3_2", func(f *goroutinePolicyFixture) {
			f.captures[0].Replicas[0].Passive.AntiEntropyCycles = 3
			f.captures[1].Replicas[0].Passive.AntiEntropyCycles = 2
		}},
		{"tagged", func(f *goroutinePolicyFixture) { f.profile.ImageScope = "tagged" }},
		{"foreign_observer", func(f *goroutinePolicyFixture) { f.profiles[0].ObserverSHA256 = strings.Repeat("0", 64) }},
		{"missing_peer", func(f *goroutinePolicyFixture) {
			delete(f.captures[0].Replicas[0].Passive.Peers, "https://peer-a:6381")
		}},
		{"late_flight", func(f *goroutinePolicyFixture) { f.captures[0].DurationNS = int64(4 * time.Second) }},
		{"sample_gap", func(f *goroutinePolicyFixture) { f.steady.Samples = f.steady.Samples[2:] }},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newGoroutinePolicyFixture(t)
			test.change(f)
			f.persist(t)
			if r := evaluateGoroutinePolicy(f.input); r.Verdict != "incomplete" {
				t.Fatal(r.Verdict, r.Failures)
			}
		})
	}
	for _, kind := range []string{"missing", "missing_legacy_post", "duplicate", "late", "no_owner_evidence"} {
		t.Run(kind, func(t *testing.T) {
			f := newGoroutinePolicyFixture(t)
			switch kind {
			case "missing":
				f.input.Posts = f.input.Posts[:1]
			case "missing_legacy_post":
				f.input.LegacyPost = goroutineReference{Path: filepath.Join(f.dir, "missing.json"), SHA256: strings.Repeat("0", 64)}
			case "duplicate":
				f.input.Posts[1].OffsetSeconds = 5
			case "late":
				f.input.Posts[1].EndNS = f.barrier.T0NS + int64(14*time.Second)
			case "no_owner_evidence":
				f.input.OwnerEvidence = nil
			}
			if r := evaluateGoroutinePolicy(f.input); r.Verdict != "incomplete" {
				t.Fatal(r.Verdict, r.Failures)
			}
		})
	}
}

func TestGoroutinePolicyPreservesLegacyFailure(t *testing.T) {
	f := newGoroutinePolicyFixture(t)
	if r := evaluateGoroutinePolicy(f.input); r.Verdict != "pass" {
		t.Fatal(r.MissingPremises)
	}
	legacy := evaluateReceiptLeak(f.input.Pre.Path, f.input.LegacyPost.Path, f.input.Steady.Path, 45*time.Second, 5*time.Second, 15, 32, 40)
	if legacy.Verdict != "fail" {
		t.Fatal("old saved +16 was reclassified")
	}
	// Original snapshots retain an open operational context, including nullable
	// metrics. Only the candidate's fixed goroutine fields are evaluated here.
	data, err := os.ReadFile(f.input.Pre.Path)
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if json.Unmarshal(data, &rows) != nil {
		t.Fatal("fixture snapshot")
	}
	for _, row := range rows {
		row["metrics"] = map[string]any{"existing_optional_context": nil}
	}
	f.input.Pre = goroutineFixtureRef(t, f.input.Pre.Path, rows, 0o644)
	if r := evaluateGoroutinePolicy(f.input); r.Verdict != "pass" {
		t.Fatal("legacy optional context changed", r.MissingPremises)
	}
}

func TestGoroutinePolicyCoordinatorReturn(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*goroutinePolicyFixture)
		want   string
	}{
		{"exit_nonzero", func(f *goroutinePolicyFixture) { f.returns[1].ExitCode = goroutineTestExit(1) }, "fail"},
		{"not_returned", func(f *goroutinePolicyFixture) { returned := false; f.returns[1].CoordinatorReturned = &returned }, "fail"},
		{"finished_after_t0", func(f *goroutinePolicyFixture) { f.returns[1].FinishedNS = f.barrier.T0NS + 1 }, "incomplete"},
		{"phases_overlap", func(f *goroutinePolicyFixture) { f.returns[0].FinishedNS = f.returns[1].StartedNS + 1 }, "incomplete"},
		{"wrong_phase", func(f *goroutinePolicyFixture) { f.returns[1].Phase = "foreign" }, "incomplete"},
		{"missing_exit", func(f *goroutinePolicyFixture) { f.returns[1].ExitCode = nil }, "incomplete"},
		{"scope", func(f *goroutinePolicyFixture) { f.returns[1].Scope = "global_server_idle" }, "incomplete"},
		{"producer_count_from_families", func(f *goroutinePolicyFixture) {
			f.barrier.OwnedWork[0].Declared = goroutineTestInt(4)
			f.barrier.OwnedWork[0].Reaped = goroutineTestInt(4)
		}, "fail"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newGoroutinePolicyFixture(t)
			test.change(f)
			f.persist(t)
			if r := evaluateGoroutinePolicy(f.input); r.Verdict != test.want {
				t.Fatal(r.Verdict, r.Failures, r.MissingPremises)
			}
		})
	}
	f := newGoroutinePolicyFixture(t)
	f.barrier.WorkReports[1].CoordinatorReturn.SHA256 = strings.Repeat("0", 64)
	f.input.Barrier = goroutineFixtureRef(t, f.input.Barrier.Path, f.barrier, 0o600)
	if r := evaluateGoroutinePolicy(f.input); r.Verdict != "incomplete" {
		t.Fatal("missing/foreign return ref accepted", r.Verdict)
	}
}

func TestGoroutinePolicyOwnedEvidence(t *testing.T) {
	f := newGoroutinePolicyFixture(t)
	if _, err := goroutineRead(f.input.Pre, 1<<20); err != nil {
		t.Fatal("unchanged legacy0644 refused", err)
	}
	if _, err := goroutineReadMode(f.input.Pre, 1<<20, true); err == nil {
		t.Fatal("private decoder admitted0644")
	}
	link := filepath.Join(f.dir, "symlink.json")
	if err := os.Symlink(f.input.Pre.Path, link); err != nil {
		t.Fatal(err)
	}
	ref := f.input.Pre
	ref.Path = link
	if _, err := goroutineRead(ref, 1<<20); err == nil {
		t.Fatal("symlink admitted")
	}
	if err := os.WriteFile(f.input.Pre.Path, []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := goroutineRead(f.input.Pre, 1<<20); err == nil {
		t.Fatal("tamper admitted")
	}
	path := filepath.Join(f.dir, "new-artifact.json")
	if err := writeGoroutineArtifact(path, map[string]bool{"candidate": true}); err != nil {
		t.Fatal(err)
	}
	if err := writeGoroutineArtifact(path, map[string]bool{"candidate": false}); err == nil {
		t.Fatal("artifact overwritten")
	}
}

func TestGoroutinePolicyCurrentCaseRoot(t *testing.T) {
	for _, kind := range []string{"snapshot", "profile", "phase_return", "summary", "passive", "source_exception"} {
		t.Run(kind, func(t *testing.T) {
			f := newGoroutinePolicyFixture(t)
			foreign, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			copyRef := func(ref goroutineReference) goroutineReference {
				data, err := os.ReadFile(ref.Path)
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(foreign, filepath.Base(ref.Path))
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
				return goroutineReference{Path: path, SHA256: ref.SHA256}
			}
			want := "incomplete"
			switch kind {
			case "snapshot":
				f.input.LegacyPost = copyRef(f.input.LegacyPost)
			case "profile":
				f.input.BaselineProfile = copyRef(f.input.BaselineProfile)
			case "phase_return":
				f.barrier.WorkReports[0].CoordinatorReturn = copyRef(f.barrier.WorkReports[0].CoordinatorReturn)
				f.input.Barrier = goroutineFixtureRef(t, f.input.Barrier.Path, f.barrier, 0o600)
			case "summary":
				f.barrier.WorkReports[0].Admission = copyRef(f.barrier.WorkReports[0].Admission)
				f.input.Barrier = goroutineFixtureRef(t, f.input.Barrier.Path, f.barrier, 0o600)
			case "passive":
				ref := copyRef(*f.profile.Nodes[0].PassiveMetrics)
				f.profile.Nodes[0].PassiveMetrics = &ref
				f.input.BaselineProfile = goroutineFixtureRef(t, f.input.BaselineProfile.Path, f.profile, 0o600)
			case "source_exception":
				f.input.OwnerEvidence[0] = copyRef(f.input.OwnerEvidence[0])
				want = "pass"
			}
			if result := evaluateGoroutinePolicy(f.input); result.Verdict != want {
				t.Fatal("current case boundary", kind, result.Verdict, result.MissingPremises)
			}
		})
	}
}

func TestGoroutinePolicyCLIForeignBundle(t *testing.T) {
	f := newGoroutinePolicyFixture(t) // A fully internally consistent FOREIGN bundle.
	current, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	input := goroutineFixtureRef(t, filepath.Join(current, "current-input.json"), f.input, 0o600)
	out := filepath.Join(current, "candidate-report.json")
	if code := runGoroutinePolicyEvaluation([]string{"-input", input.Path, "-out", out}); code != 1 {
		t.Fatal("foreign bundle CLI exit", code)
	}
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var report goroutinePolicyReport
	if json.Unmarshal(data, &report) != nil || report.Verdict != "incomplete" || !strings.Contains(strings.Join(report.MissingPremises, " "), "case root") {
		t.Fatal("CLI adopted a self-consistent foreign run")
	}
	foreignOut := filepath.Join(f.dir, "must-not-create.json")
	if code := runGoroutinePolicyEvaluation([]string{"-input", input.Path, "-out", foreignOut}); code != 2 {
		t.Fatal("foreign output accepted", code)
	}
	if _, err := os.Lstat(foreignOut); !os.IsNotExist(err) {
		t.Fatal("foreign output created")
	}
}
