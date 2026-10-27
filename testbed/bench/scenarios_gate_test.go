// Package bench_test hosts cross-cutting gates over the bench-harness
// scenario corpus (testbed/bench/scenarios/*.yaml).
//
// TestScenarioTemplates_MatchWireSchema is the schema-drift gate (#934):
// ghz resolves request messages via server reflection and rejects any JSON
// key that is not a field of the resolved message, so a proto change that
// retires a field silently orphans every scenario template that still sends
// it — the failure then only surfaces as a red nightly leak-gate run, days
// after the schema PR merged (exactly how #866 broke broad_illuminate).
// This test reproduces ghz's construction path at `go test ./...` time: it
// renders every data_template with ghz-style template data and unmarshals
// the result via protojson (which, like ghz, accepts both original and
// lowerCamel field names and rejects unknown ones) against the request
// message resolved from the scenario's `call` name.
package bench_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"text/template"
	"time"

	"github.com/anaregdesign/lantern/testbed/bench/topology"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/dynamicpb"
	"gopkg.in/yaml.v3"

	// Register the graph.v1 file descriptors (messages + both services) in
	// protoregistry.GlobalFiles so `call` names resolve.
	_ "github.com/anaregdesign/lantern/pb/graph/v1"
)

func naturalGCObservationShellFunctions(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, body, ok := strings.Cut(string(raw), "validate_natural_gc_observation() {\n")
	if !ok {
		t.Fatal("missing idle observation preflight")
	}
	body, _, ok = strings.Cut(body, "\ncleanup() {\n")
	if !ok {
		t.Fatal("missing diagnostic cleanup boundary")
	}
	return "validate_natural_gc_observation() {\n" + body
}

func TestNaturalGCObservationUnitTests(t *testing.T) {
	command := exec.CommandContext(t.Context(), "python3", "-B", "-m", "unittest", "discover", "-s", "capture", "-p", "natural_gc_observation_test.py")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("natural GC observer: %v\n%s", err, output)
	}
}

func TestNaturalGCObservationPreservesFormalExitAndCleanup(t *testing.T) {
	for _, test := range []struct {
		name             string
		formal, observer int
		seal             int
		want             int
	}{
		{"formal pass observer complete", 0, 0, 0, 0},
		{"formal fail observer complete", 1, 0, 0, 1},
		{"formal pass observer incomplete", 0, 1, 0, 1},
		{"formal fail observer refused", 1, 2, 0, 1},
		{"seal failure never starts observer", 0, 0, 1, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			fixture := `set -euo pipefail
OUTDIR="$1"; CAPTURE_DIR="$1"
LANTERN_BENCH_NATURAL_GC_OBSERVE=1
LANTERN_BENCH_DIAGNOSTIC_SOURCE_ID=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
COMPOSE_PROJECT_NAME=lantern-observer-test
NATURAL_GC_ARGS=()
NATURAL_GC_PID=""
verdict=pass; metric_verdict=skipped; semantic_verdict=skipped
perf_verdict=pass; workload_verdict=pass
formal_status="$2"; observer_status="$3"; seal_status="$4"
log() { :; }
python3() {
  if [[ "$*" == *--create-seal* ]]; then
    printf 'seal:%s\n' "$formal_status" >> "$OUTDIR/events"
    printf '%s\n' "$formal_status" > "$OUTDIR/original_formal_exit"
    return "$seal_status"
  fi
  [[ -f "$OUTDIR/original_formal_exit" ]] || return 92
  printf 'observe:%s\n' "$formal_status" >> "$OUTDIR/events"
  return "$observer_status"
}
cleanup() {
  local status=$?
  [[ -z "$NATURAL_GC_PID" ]] || exit 93
  printf 'cleanup:%s:formal:%s\n' "$status" "$formal_status" >> "$OUTDIR/events"
}
trap cleanup EXIT
` + naturalGCObservationShellFunctions(t) + `
if finish_with_natural_gc_observation "$formal_status"; then exit 0; else exit 1; fi
`
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "bash", "-c", fixture, "observation-test", dir,
				fmt.Sprint(test.formal), fmt.Sprint(test.observer), fmt.Sprint(test.seal))
			output, runErr := command.CombinedOutput()
			if command.ProcessState == nil || command.ProcessState.ExitCode() != test.want {
				t.Fatalf("exit=%v err=%v output=%s", command.ProcessState, runErr, output)
			}
			raw, err := os.ReadFile(filepath.Join(dir, "events"))
			if err != nil {
				t.Fatal(err)
			}
			want := fmt.Sprintf("seal:%d\n", test.formal)
			if test.seal == 0 {
				want += fmt.Sprintf("observe:%d\n", test.formal)
			}
			want += fmt.Sprintf("cleanup:%d:formal:%d\n", test.want, test.formal)
			if string(raw) != want {
				t.Fatalf("event order/result changed: got %q want %q", raw, want)
			}
		})
	}
}

func TestNaturalGCObservationWiringAndPreflight(t *testing.T) {
	raw, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(raw)
	check := strings.Index(script, "\nvalidate_natural_gc_observation\n")
	setup := strings.Index(script, "\n# ----- optional per-scenario cluster env overrides")
	formal := strings.LastIndex(script, "\nformal_status=1\n")
	workload := strings.LastIndex(script, " > \"$OUTDIR/workload_gate.json\"")
	if check < 0 || setup < check || formal < workload || workload < 0 ||
		!strings.Contains(script, `stop_owned_processes "${NATURAL_GC_PID:-}"`) {
		t.Fatal("preflight/formal-result/owned cleanup boundaries changed")
	}
	for _, test := range []struct {
		name, enable, scenario, driver, keep, skip, lightweight string
		wantCheck, wantOK                                       bool
	}{
		{"disabled", "0", "anything", "ghz", "1", "1", "0", false, true},
		{"eligible", "1", "receipt_edge_add_admission_lookup", "receipt_edge_add", "0", "0", "1", true, true},
		{"invalid opt in", "yes", "receipt_edge_add_admission_lookup", "receipt_edge_add", "0", "0", "1", false, false},
		{"unrelated scenario", "1", "receipt_vertex_delete_admission_lookup", "receipt_vertex_delete", "0", "0", "1", false, false},
		{"retained project", "1", "receipt_edge_add_admission_lookup", "receipt_edge_add", "1", "0", "1", false, false},
		{"existing project", "1", "receipt_edge_add_admission_lookup", "receipt_edge_add", "0", "1", "1", false, false},
		{"profiling enabled", "1", "receipt_edge_add_admission_lookup", "receipt_edge_add", "0", "0", "0", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "natural_gc_observation.py"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			fixture := `set -euo pipefail
CAPTURE_DIR="$1"
LANTERN_BENCH_NATURAL_GC_OBSERVE="$2"; SCENARIO_NAME="$3"; target_driver="$4"
KEEP_UP="$5"; SKIP_UP="$6"; LEAK_GATE_ONLY="$7"
COMPOSE_PROJECT_NAME=lantern-observer-test
REPLICA_METRICS_PORTS=(9390 9391 9392)
die() { exit 1; }
python3() { printf '%s\n' "$*" > "$CAPTURE_DIR/preflight"; }
` + naturalGCObservationShellFunctions(t) + "\nvalidate_natural_gc_observation\n"
			command := exec.CommandContext(t.Context(), "bash", "-c", fixture, "observation-test", dir,
				test.enable, test.scenario, test.driver, test.keep, test.skip, test.lightweight)
			output, runErr := command.CombinedOutput()
			if (runErr == nil) != test.wantOK {
				t.Fatalf("preflight: %v output=%s", runErr, output)
			}
			called, readErr := os.ReadFile(filepath.Join(dir, "preflight"))
			if (readErr == nil) != test.wantCheck {
				t.Fatalf("helper invocation=%q err=%v", called, readErr)
			}
			if test.wantCheck && (!strings.Contains(string(called), "--check-config") ||
				!strings.Contains(string(called), "http://127.0.0.1:9390/metrics,http://127.0.0.1:9391/metrics,http://127.0.0.1:9392/metrics")) {
				t.Fatalf("explicit fixed preflight missing: %q", called)
			}
		})
	}
}

func TestNaturalGCObservationOwnedCancellation(t *testing.T) {
	raw, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, cleanup, ok := strings.Cut(string(raw), "cleanup() {\n")
	if !ok {
		t.Fatal("missing cleanup")
	}
	cleanup, _, ok = strings.Cut("cleanup() {\n"+cleanup, "\ntrap cleanup EXIT")
	if !ok {
		t.Fatal("missing cleanup end")
	}
	owned, err := filepath.Abs("owned_processes.sh")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "natural_gc_observation.py"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	fixture := `set -euo pipefail
source "$2"
OUTDIR="$1"; CAPTURE_DIR="$1"
LANTERN_BENCH_NATURAL_GC_OBSERVE=1
LANTERN_BENCH_DIAGNOSTIC_SOURCE_ID=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
COMPOSE_PROJECT_NAME=lantern-observer-test
SCENARIO_NAME=receipt_edge_add_admission_lookup; target_driver=receipt_edge_add
KEEP_UP=0; SKIP_UP=0; LEAK_GATE_ONLY=1
REPLICA_METRICS_PORTS=(9390 9391 9392)
NATURAL_GC_ARGS=(); NATURAL_GC_PID=""
COMPOSE_STARTED=0; PEER_TLS_DIR=""; NATIVE_WORK_DIR=""; GHZ_DIAGNOSTICS_DIR=""
prod_pids=(); sub_pids=(); ghz_capture_pids=()
verdict=pass; metric_verdict=skipped; semantic_verdict=skipped
perf_verdict=pass; workload_verdict=pass
log() { :; }
python3() {
  [[ "$*" != *--check-config* ]] || return 0
  [[ "$*" != *--create-seal* ]] || return 0
  exec bash -c 'trap "exit 1" TERM; printf "%s\n" "$$" > "$1/observer_pid"; while true; do sleep 0.02; done' observer "$OUTDIR"
}
` + naturalGCObservationShellFunctions(t) + cleanup + `
trap cleanup EXIT
validate_natural_gc_observation
finish_with_natural_gc_observation 0
`
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "bash", "-c", fixture, "observation-test", dir, owned)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	// This signal is sent only after the observer registers its own identity.
	var pid []byte
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		pid, err = os.ReadFile(filepath.Join(dir, "observer_pid"))
		if err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatal("observer did not start", err)
	}
	if err := exec.Command("kill", "-TERM", fmt.Sprint(command.Process.Pid)).Run(); err != nil {
		_ = command.Process.Kill()
		_ = command.Wait()
		t.Fatal(err)
	}
	if err := command.Wait(); err == nil || command.ProcessState.ExitCode() != 143 {
		t.Fatalf("expected cancellation exit143, got %v (%v)", command.ProcessState, err)
	}
	if err := exec.Command("bash", "-c", `! kill -0 "$1" 2>/dev/null`, "check-observer", strings.TrimSpace(string(pid))).Run(); err != nil {
		t.Fatal("owned observer was left alive", err)
	}
}

// scenarioCall is one (call, data_template) pair wherever it appears in a
// scenario document.
type scenarioCall struct {
	Name         string `yaml:"name"`
	Call         string `yaml:"call"`
	DataTemplate string `yaml:"data_template"`
	RPS          int    `yaml:"rps"`
}

// scenarioDoc is the subset of the scenario schema that names RPCs. Keep in
// sync with the extraction sites in testbed/bench/run.sh (target.call,
// target.calls[], subscribe, subscribe.consumers[]).
type scenarioDoc struct {
	Name   string `yaml:"name"`
	Target struct {
		Driver       string         `yaml:"driver"`
		Call         string         `yaml:"call"`
		DataTemplate string         `yaml:"data_template"`
		Calls        []scenarioCall `yaml:"calls"`
	} `yaml:"target"`
	Subscribe struct {
		Call         string         `yaml:"call"`
		DataTemplate string         `yaml:"data_template"`
		Consumers    []scenarioCall `yaml:"consumers"`
	} `yaml:"subscribe"`
}

func TestNativeChaosServiceAndRecoverySchedule(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("scenarios", "chaos_kill_replica.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Chaos struct {
			Target  string `yaml:"kill_target"`
			Kill    string `yaml:"kill_at"`
			Restart string `yaml:"restart_at"`
		} `yaml:"chaos"`
		Phases struct {
			Steady struct {
				Duration string `yaml:"duration"`
			} `yaml:"steady"`
		} `yaml:"phases"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains([]string{"lantern-0", "lantern-1", "lantern-2"}, doc.Chaos.Target) {
		t.Fatalf("chaos must address an owned service, not a container name: %q", doc.Chaos.Target)
	}
	kill, err := time.ParseDuration(doc.Chaos.Kill)
	if err != nil || kill <= 0 {
		t.Fatal("invalid kill delay", err)
	}
	restart, err := time.ParseDuration(doc.Chaos.Restart)
	if err != nil || restart <= 0 {
		t.Fatal("invalid recovery delay", err)
	}
	steady, err := time.ParseDuration(doc.Phases.Steady.Duration)
	if err != nil || kill+restart >= steady {
		t.Fatal("recovery must finish during the declared steady phase", err)
	}
}

func TestContributionDeleteReleaseScenarioContract(t *testing.T) {
	const name = "edge_contrib_idempotent"
	raw, err := os.ReadFile(filepath.Join("scenarios", name+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Name   string `yaml:"name"`
		Phases struct {
			Warmup struct {
				RPS int `yaml:"rps"`
			} `yaml:"warmup"`
			Steady struct {
				Duration string `yaml:"duration"`
				RPS      int    `yaml:"rps"`
			} `yaml:"steady"`
			Cooldown string `yaml:"cooldown"`
		} `yaml:"phases"`
		Target struct {
			Endpoints []string       `yaml:"endpoints"`
			Calls     []scenarioCall `yaml:"calls"`
		} `yaml:"target"`
		LeakGate struct {
			GoroutineMaxDelta   int `yaml:"goroutine_max_delta"`
			HeapAllocMaxDeltaMB int `yaml:"heap_alloc_max_delta_mb"`
		} `yaml:"leak_gate"`
		PerfGate struct {
			MinSteadyRPSTotal *float64 `yaml:"min_steady_rps_total"`
			MaxP99MS          *float64 `yaml:"max_p99_ms"`
			MaxNonOKRatio     *float64 `yaml:"max_non_ok_ratio"`
			Producers         map[string]struct {
				MinSteadyRPS  *float64 `yaml:"min_steady_rps"`
				MaxP99MS      *float64 `yaml:"max_p99_ms"`
				MaxNonOKRatio *float64 `yaml:"max_non_ok_ratio"`
			} `yaml:"producers"`
		} `yaml:"perf_gate"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Name != name || len(doc.Target.Endpoints) != 3 ||
		doc.Phases.Warmup.RPS != 100 || doc.Phases.Steady.RPS != 400 ||
		doc.Phases.Steady.Duration != "2m" || doc.Phases.Cooldown != "60s" {
		t.Fatalf("contribution release workload changed: %+v", doc)
	}
	if doc.LeakGate.GoroutineMaxDelta != 20 || doc.LeakGate.HeapAllocMaxDeltaMB != 24 {
		t.Errorf("contribution leak limits changed: %+v", doc.LeakGate)
	}
	checkFloor := func(label string, value *float64, want float64) {
		t.Helper()
		if value == nil || *value != want {
			t.Errorf("%s = %v, want %g", label, value, want)
		}
	}
	checkFloor("aggregate rps", doc.PerfGate.MinSteadyRPSTotal, 150)
	checkFloor("aggregate p99", doc.PerfGate.MaxP99MS, 500)
	checkFloor("aggregate non-OK", doc.PerfGate.MaxNonOKRatio, 0.02)
	want := []struct{ name, method, idSpace string }{
		{"add_singular", "AddEdge", "200"},
		{"add_plural", "AddEdges", "40"},
		{"delete_singular", "DeleteEdgeContribution", "200"},
		{"delete_plural", "DeleteEdgeContributions", "40"},
	}
	if len(doc.Target.Calls) != len(want) || len(doc.PerfGate.Producers) != len(want) {
		t.Fatalf("contribution producers = %d, gates = %d, want %d",
			len(doc.Target.Calls), len(doc.PerfGate.Producers), len(want))
	}
	offered := 0
	for i, expected := range want {
		call := doc.Target.Calls[i]
		if call.Name != expected.name ||
			call.Call != "graph.v1.LanternService/"+expected.method || call.RPS != 100 ||
			!strings.Contains(call.DataTemplate, "printf `%024d`") ||
			!strings.Contains(call.DataTemplate, "mod .RequestNumber "+expected.idSpace) {
			t.Errorf("producer[%d] lost its fixed RPC/RPS/bounded 24-byte ID: %+v", i, call)
		}
		offered += call.RPS
		gate, ok := doc.PerfGate.Producers[call.Name]
		if !ok {
			t.Errorf("producer %s has no independent performance gate", call.Name)
			continue
		}
		checkFloor(call.Name+" rps", gate.MinSteadyRPS, 35)
		checkFloor(call.Name+" p99", gate.MaxP99MS, 500)
		checkFloor(call.Name+" non-OK", gate.MaxNonOKRatio, 0.02)
	}
	if offered != doc.Phases.Steady.RPS {
		t.Errorf("offered producer RPS = %d, steady RPS = %d", offered, doc.Phases.Steady.RPS)
	}
	for _, requestNumber := range []int64{0, 121, 987654321} {
		rows := make(map[string][]contributionScenarioRow, len(doc.Target.Calls))
		for _, call := range doc.Target.Calls {
			rows[call.Name] = renderContributionScenarioRows(t, call, requestNumber)
		}
		if !slices.Equal(rows["add_singular"], rows["delete_singular"]) ||
			!slices.Equal(rows["add_plural"], rows["delete_plural"]) {
			t.Errorf("Add and Delete target different pairs or IDs at request %d: %+v", requestNumber, rows)
		}
	}
	release, err := os.ReadFile("release-scenarios.txt")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, line := range strings.Split(string(release), "\n") {
		if strings.TrimSpace(strings.SplitN(line, "#", 2)[0]) == name {
			count++
		}
	}
	if count != 1 {
		t.Errorf("contribution scenario appears %d times in release sweep, want exactly one", count)
	}
}

type contributionScenarioRow struct {
	tail, head, id string
}

func renderContributionScenarioRows(t *testing.T, call scenarioCall, requestNumber int64) []contributionScenarioRow {
	t.Helper()
	tmpl, err := template.New(call.Name).Funcs(ghzFuncs()).Parse(call.DataTemplate)
	if err != nil {
		t.Fatal(err)
	}
	var rendered strings.Builder
	if err := tmpl.Execute(&rendered, ghzTemplateData{RequestNumber: requestNumber}); err != nil {
		t.Fatal(err)
	}
	var request struct {
		Edge *struct {
			Tail, Head string
		}
		Edges []struct {
			Tail, Head string
		}
		Tail, Head    string
		ContribID     string   `json:"contribId"`
		ContribIDs    []string `json:"contribIds"`
		Contributions []struct {
			Tail, Head string
			ContribID  string `json:"contribId"`
		}
	}
	if err := json.Unmarshal([]byte(rendered.String()), &request); err != nil {
		t.Fatal(err)
	}
	var rows []contributionScenarioRow
	switch call.Name {
	case "add_singular":
		if request.Edge != nil {
			rows = append(rows, contributionScenarioRow{request.Edge.Tail, request.Edge.Head, request.ContribID})
		}
	case "add_plural":
		if len(request.Edges) != 3 || len(request.ContribIDs) != len(request.Edges) {
			t.Fatalf("%s generated %d edges and %d IDs", call.Name, len(request.Edges), len(request.ContribIDs))
		}
		for i, edge := range request.Edges {
			rows = append(rows, contributionScenarioRow{edge.Tail, edge.Head, request.ContribIDs[i]})
		}
	case "delete_singular":
		rows = append(rows, contributionScenarioRow{request.Tail, request.Head, request.ContribID})
	case "delete_plural":
		for _, item := range request.Contributions {
			rows = append(rows, contributionScenarioRow{item.Tail, item.Head, item.ContribID})
		}
		if len(rows) != 3 {
			t.Fatalf("%s generated %d keys, want 3", call.Name, len(rows))
		}
	default:
		t.Fatalf("unexpected contribution producer %q", call.Name)
	}
	if len(rows) == 0 {
		t.Fatalf("%s generated no contribution keys", call.Name)
	}
	for i, row := range rows {
		id, err := base64.StdEncoding.DecodeString(row.id)
		if err != nil {
			t.Fatalf("%s[%d]: invalid base64 ID: %v", call.Name, i, err)
		}
		nonzero := false
		for _, b := range id {
			nonzero = nonzero || b != 0
		}
		if row.tail == "" || row.head == "" || len(id) != 24 || !nonzero {
			t.Errorf("%s[%d]: invalid pair or 24-byte nonzero ID: %+v", call.Name, i, row)
		}
	}
	return rows
}

func testReceiptScenarioContract(t *testing.T, name, driver, admissionMethod string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("scenarios", name+".yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Name   string `yaml:"name"`
		Target struct {
			Driver    string         `yaml:"driver"`
			Endpoints []string       `yaml:"endpoints"`
			Calls     []scenarioCall `yaml:"calls"`
		} `yaml:"target"`
		Cluster struct {
			ReceiptWAL struct {
				Enabled    bool   `yaml:"enabled"`
				Retention  string `yaml:"retention"`
				MaxEntries int    `yaml:"max_entries"`
				MaxBytes   int    `yaml:"max_bytes"`
			} `yaml:"receipt_wal"`
		} `yaml:"cluster"`
		Phases struct {
			Warmup struct {
				Duration    string `yaml:"duration"`
				RPS         int    `yaml:"rps"`
				Concurrency int    `yaml:"concurrency"`
			} `yaml:"warmup"`
			Steady struct {
				Duration    string `yaml:"duration"`
				RPS         int    `yaml:"rps"`
				Concurrency int    `yaml:"concurrency"`
			} `yaml:"steady"`
			Cooldown string `yaml:"cooldown"`
		} `yaml:"phases"`
		PerfGate struct {
			MinSteadyRPSTotal *float64 `yaml:"min_steady_rps_total"`
			MaxP99MS          *float64 `yaml:"max_p99_ms"`
			MaxNonOKRatio     *float64 `yaml:"max_non_ok_ratio"`
			Producers         map[string]struct {
				MinSteadyRPS  *float64 `yaml:"min_steady_rps"`
				MaxP99MS      *float64 `yaml:"max_p99_ms"`
				MaxNonOKRatio *float64 `yaml:"max_non_ok_ratio"`
			} `yaml:"producers"`
		} `yaml:"perf_gate"`
		LeakGate struct {
			GoroutineMaxDelta         int    `yaml:"goroutine_max_delta"`
			HeapAllocMaxDeltaMB       int    `yaml:"heap_alloc_max_delta_mb"`
			SteadyHeapAllocMaxDeltaMB int    `yaml:"steady_heap_alloc_max_delta_mb"`
			SteadySampleInterval      string `yaml:"steady_sample_interval"`
		} `yaml:"leak_gate"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse receipt scenario: %v", err)
	}
	if doc.Name != name || doc.Target.Driver != driver {
		t.Fatalf("scenario name/driver = %q/%q, want %q/%q", doc.Name, doc.Target.Driver, name, driver)
	}
	if len(doc.Target.Endpoints) != 3 {
		t.Fatalf("target endpoints = %d, want 3", len(doc.Target.Endpoints))
	}
	for i, endpoint := range doc.Target.Endpoints {
		want := fmt.Sprintf("localhost:%d", 6380+i)
		if endpoint != want {
			t.Errorf("target.endpoints[%d] = %q, want %q", i, endpoint, want)
		}
	}
	wantCalls := []struct {
		name         string
		call         string
		offeredRPS   int
		minSteadyRPS float64
		maxP99MS     float64
	}{
		{name: "receipt_admission", call: "graph.v1.LanternService/" + admissionMethod, offeredRPS: 100, minSteadyRPS: 75, maxP99MS: 500},
		{name: "receipt_lookup", call: "graph.v1.LanternService/GetReceiptStatus", offeredRPS: 100, minSteadyRPS: 75, maxP99MS: 200},
	}
	if len(doc.Target.Calls) != len(wantCalls) {
		t.Fatalf("target calls = %d, want %d", len(doc.Target.Calls), len(wantCalls))
	}
	totalProducerRPS := 0
	for i, want := range wantCalls {
		call := doc.Target.Calls[i]
		if call.Name != want.name || call.Call != want.call {
			t.Errorf("target.calls[%d] = (%q, %q), want (%q, %q)", i, call.Name, call.Call, want.name, want.call)
		}
		if call.RPS != want.offeredRPS {
			t.Errorf("target.calls[%d].rps = %d, want %d", i, call.RPS, want.offeredRPS)
		}
		if strings.TrimSpace(call.DataTemplate) != "" {
			t.Errorf("target.calls[%d] must leave data_template to the receipt driver", i)
		}
		totalProducerRPS += call.RPS
	}
	if totalProducerRPS != doc.Phases.Steady.RPS {
		t.Errorf("producer RPS total = %d, steady RPS = %d", totalProducerRPS, doc.Phases.Steady.RPS)
	}
	assertThreshold := func(name string, got *float64, want float64) {
		t.Helper()
		if got == nil {
			t.Errorf("%s is missing, want %g", name, want)
		} else if *got != want {
			t.Errorf("%s = %g, want %g", name, *got, want)
		}
	}
	for _, want := range wantCalls {
		gate, ok := doc.PerfGate.Producers[want.name]
		if !ok {
			t.Errorf("producer %q has no independent perf gate", want.name)
			continue
		}
		prefix := "perf_gate.producers." + want.name
		assertThreshold(prefix+".min_steady_rps", gate.MinSteadyRPS, want.minSteadyRPS)
		assertThreshold(prefix+".max_p99_ms", gate.MaxP99MS, want.maxP99MS)
		assertThreshold(prefix+".max_non_ok_ratio", gate.MaxNonOKRatio, 0)
	}
	if len(doc.PerfGate.Producers) != len(wantCalls) {
		t.Errorf("perf_gate.producers = %d, want %d", len(doc.PerfGate.Producers), len(wantCalls))
	}
	assertThreshold("perf_gate.min_steady_rps_total", doc.PerfGate.MinSteadyRPSTotal, 150)
	assertThreshold("perf_gate.max_p99_ms", doc.PerfGate.MaxP99MS, 500)
	assertThreshold("perf_gate.max_non_ok_ratio", doc.PerfGate.MaxNonOKRatio, 0)
	if doc.LeakGate.GoroutineMaxDelta != 15 {
		t.Errorf("leak_gate.goroutine_max_delta = %d, want 15", doc.LeakGate.GoroutineMaxDelta)
	}
	if doc.LeakGate.HeapAllocMaxDeltaMB != 32 {
		t.Errorf("leak_gate.heap_alloc_max_delta_mb = %d, want 32", doc.LeakGate.HeapAllocMaxDeltaMB)
	}
	if doc.LeakGate.SteadyHeapAllocMaxDeltaMB != 40 {
		t.Errorf("leak_gate.steady_heap_alloc_max_delta_mb = %d, want 40", doc.LeakGate.SteadyHeapAllocMaxDeltaMB)
	}
	if doc.LeakGate.SteadySampleInterval != "5s" {
		t.Errorf("leak_gate.steady_sample_interval = %q, want 5s", doc.LeakGate.SteadySampleInterval)
	}
	if !doc.Cluster.ReceiptWAL.Enabled {
		t.Fatal("receipt WAL is not enabled")
	}
	retention, err := time.ParseDuration(doc.Cluster.ReceiptWAL.Retention)
	if err != nil || retention < time.Hour {
		t.Fatalf("receipt retention = %q, %v; want at least 1h", doc.Cluster.ReceiptWAL.Retention, err)
	}
	warmupDuration, err := time.ParseDuration(doc.Phases.Warmup.Duration)
	if err != nil {
		t.Fatal(err)
	}
	steadyDuration, err := time.ParseDuration(doc.Phases.Steady.Duration)
	if err != nil {
		t.Fatal(err)
	}
	cooldown, err := time.ParseDuration(doc.Phases.Cooldown)
	if err != nil {
		t.Fatal(err)
	}
	if warmupDuration != 10*time.Second || steadyDuration != 45*time.Second ||
		cooldown != 10*time.Second || doc.Phases.Warmup.RPS != 200 ||
		doc.Phases.Steady.RPS != 200 || doc.Phases.Warmup.Concurrency != 12 ||
		doc.Phases.Steady.Concurrency != 12 {
		t.Errorf("receipt scenario phases = %s/%s/%s, rps %d/%d, concurrency %d/%d; want 10s/45s/10s, 200/200, 12/12",
			warmupDuration, steadyDuration, cooldown,
			doc.Phases.Warmup.RPS, doc.Phases.Steady.RPS,
			doc.Phases.Warmup.Concurrency, doc.Phases.Steady.Concurrency)
	}
	if interval, err := time.ParseDuration(doc.LeakGate.SteadySampleInterval); err != nil ||
		interval != 5*time.Second {
		t.Errorf("receipt steady sampling interval = %q, want 5s",
			doc.LeakGate.SteadySampleInterval)
	}
	admissionRPS := totalProducerRPS / len(wantCalls)
	requiredEntries := int(warmupDuration.Seconds())*(doc.Phases.Warmup.RPS/len(wantCalls)) +
		int(steadyDuration.Seconds())*admissionRPS
	if doc.Cluster.ReceiptWAL.MaxEntries < requiredEntries*6/5 {
		t.Errorf("receipt max_entries = %d, want >= 120%% of %d admitted operations", doc.Cluster.ReceiptWAL.MaxEntries, requiredEntries)
	}
	if doc.Cluster.ReceiptWAL.MaxBytes < doc.Cluster.ReceiptWAL.MaxEntries*512 {
		t.Errorf("receipt max_bytes = %d, want >= 512 bytes per retained entry", doc.Cluster.ReceiptWAL.MaxBytes)
	}
}

func TestReceiptAdmissionLookupScenarioContract(t *testing.T) {
	scenarios := []struct {
		name   string
		driver string
		method string
	}{
		{"receipt_vertex_put_admission_lookup", "receipt_vertex_put", "PutVertex"},
		{"receipt_vertex_delete_admission_lookup", "receipt_vertex_delete", "DeleteVertex"},
		{"receipt_admission_lookup", "receipt_edge_delete", "DeleteEdge"},
		{"receipt_edge_add_admission_lookup", "receipt_edge_add", "AddEdge"},
	}
	for _, scenario := range scenarios {
		t.Run(scenario.name, func(t *testing.T) {
			testReceiptScenarioContract(t, scenario.name, scenario.driver, scenario.method)
		})
	}

	runScript, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, contract := range []string{
		`target_driver="$(yq -r '.target.driver // "ghz"'`,
		`go run ./testbed/bench/receiptprobe`,
		`go -C "$REPO_ROOT/server" build -o "$AUTHFIXTURE" ./cmd/authfixture`,
		`-compose -directory "$PEER_TLS_DIR" -public-ports "$native_public_ports" -mode oidc -receipt`,
		`-renew-every 2m`,
		`COMPOSE_FILES+=( -f "$HERE/compose.receipt-tls.yml" )`,
		`receipt driver requires a fresh Compose lifecycle`,
		`owned_compose_project_absent "$COMPOSE_PROJECT_NAME"`,
		`down_owned_compose "$COMPOSE_PROJECT_NAME" "${COMPOSE_FILES[@]}"`,
		`-family "$target_driver"`,
		`endpoint_urls+="https://${ep}"`,
		`-ca-file "$PEER_TLS_DIR/ca.pem"`,
		`pin_receipt_image`,
		`verify_receipt_image_provenance "$OUTDIR/image_provenance_pre.json"`,
		`verify_receipt_image_provenance "$OUTDIR/image_provenance_post.json"`,
		`verify_receipt_peer_tls "$OUTDIR/peer_tls_pre.json"`,
		`verify_receipt_peer_tls "$OUTDIR/peer_tls_post.json" "$OUTDIR/peer_tls_pre.json"`,
		`EXPECTED_LANTERN_IMAGE_ID`,
		`EXPECTED_LANTERN_COMMIT`,
		`{{.Image}}|{{.Config.Image}}|{{.State.Running}}`,
		`ghz_steady_0_receipt_admission.json`,
		`ghz_steady_1_receipt_lookup.json`,
		`-metrics-endpoints "$metrics_urls"`,
		`-metrics-interval "$(yq -r '.leak_gate.steady_sample_interval' "$SCENARIO_FILE")"`,
		`-metrics-report "$OUTDIR/runtime_steady.json"`,
		`go run ./testbed/bench/receiptprobe evaluate-leak`,
		`-steady "$OUTDIR/runtime_steady.json"`,
		`-duration "$steady_duration"`,
		`-max-goroutines "$g_thresh"`,
		`-max-heap-mb "$h_thresh_mb"`,
		`-max-steady-heap-mb "$steady_h_thresh_mb"`,
	} {
		if !strings.Contains(string(runScript), contract) {
			t.Errorf("run.sh missing receipt driver contract %q", contract)
		}
	}
	projectScope := strings.Index(string(runScript), `export COMPOSE_PROJECT_NAME="${COMPOSE_PROJECT_NAME:-lantern-bench}"`)
	projectAbsence := strings.Index(string(runScript), `owned_compose_project_absent "$COMPOSE_PROJECT_NAME"`)
	if projectScope < 0 || projectAbsence < 0 || projectScope >= projectAbsence {
		t.Error("fresh receipt startup must prove absence of its named Compose project")
	}
	preflight := strings.Index(string(runScript), `verify_receipt_peer_tls "$OUTDIR/peer_tls_pre.json"`)
	warmup := strings.Index(string(runScript), `log "warmup:`)
	postflight := strings.Index(string(runScript), `verify_receipt_peer_tls "$OUTDIR/peer_tls_post.json"`)
	perfVerdict := strings.Index(string(runScript), `log "perf gate verdict:`)
	if preflight < 0 || warmup <= preflight || postflight <= perfVerdict || perfVerdict < warmup {
		t.Error("receipt TLS provenance must be checked before load and after the verdict")
	}
	if strings.Contains(string(runScript), `-token "$LANTERN_BENCH_AUTH_TOKEN"`) ||
		strings.Contains(string(runScript), `lantern-bench-receipt-token`) {
		t.Error("receipt token must not be a command-line argument or a committed default")
	}
	steadySampling := strings.Index(string(runScript), `-metrics-report "$OUTDIR/runtime_steady.json"`)
	optionalCapture := strings.LastIndex(string(runScript), `if [[ "${LEAK_GATE_ONLY:-0}" == "1" ]]`)
	receiptEvaluation := strings.Index(string(runScript), `go run ./testbed/bench/receiptprobe evaluate-leak`)
	if steadySampling < 0 || optionalCapture < 0 || receiptEvaluation < 0 ||
		steadySampling >= optionalCapture || receiptEvaluation <= optionalCapture {
		t.Error("receipt steady sampling and its leak verdict must stay active with LEAK_GATE_ONLY=1")
	}

	protected, err := os.ReadFile("compose.receipt-tls.yml")
	if err != nil {
		t.Fatal(err)
	}
	var overlay struct {
		Services map[string]struct {
			Environment map[string]string `yaml:"environment"`
			Ports       struct{}          `yaml:"-"`
			Volumes     []struct {
				Type     string `yaml:"type"`
				Source   string `yaml:"source"`
				Target   string `yaml:"target"`
				ReadOnly bool   `yaml:"read_only"`
				Volume   struct {
					NoCopy bool `yaml:"nocopy"`
				} `yaml:"volume"`
			} `yaml:"volumes"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(protected, &overlay); err != nil {
		t.Fatal(err)
	}
	if len(overlay.Services) != 3 {
		t.Fatal("protected overlay must own only three explicit replicas")
	}
	for index, service := range []string{"lantern-0", "lantern-1", "lantern-2"} {
		got := overlay.Services[service].Environment
		if got["LANTERN_METRICS_ADDR"] != "127.0.0.1:9090" || got["LANTERN_PPROF_ENABLED"] != "false" {
			t.Fatal("protected diagnostics guard weakened", service)
		}
		for _, key := range []string{"LANTERN_AUTH_TOKENS", "LANTERN_PEER_CA_FILE", "LANTERN_PEER_DISCOVERY"} {
			if _, ok := got[key]; ok {
				t.Fatal("retired peer admission", key)
			}
		}
		mounts := overlay.Services[service].Volumes
		if len(mounts) != 2 {
			t.Fatal("protected overlay must preserve exactly its state and backup volumes", service)
		}
		for target, source := range map[string]string{"/state": fmt.Sprintf("state-%d", index), "/data": fmt.Sprintf("backup-%d", index)} {
			matches := 0
			for _, mount := range mounts {
				if mount.Type == "volume" && mount.Source == source && mount.Target == target && !mount.ReadOnly && mount.Volume.NoCopy {
					matches++
				}
			}
			if matches != 1 {
				t.Fatalf("%s must keep its owned %s volume writable with image copy-up disabled", service, target)
			}
		}
	}

	releaseList, err := os.ReadFile("release-scenarios.txt")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(releaseList), "\n") {
		for _, scenario := range scenarios {
			if strings.TrimSpace(strings.SplitN(line, "#", 2)[0]) == scenario.name {
				t.Fatalf("%s must stay out of the release sweep until thresholds have stable evidence", scenario.name)
			}
		}
	}
	nightlyWorkflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "bench-nightly.yml"))
	if err != nil {
		t.Fatal(err)
	}
	nightly := string(nightlyWorkflow)
	for _, contract := range []string{
		`EXPECTED_LANTERN_IMAGE_ID: ${{ env.RECEIPT_IMAGE_ID }}`,
		`EXPECTED_LANTERN_COMMIT: ${{ github.sha }}`,
		`docker image inspect --format '{{.Id}}' lantern:local`,
		`./testbed/bench/run.sh "$scenario"`,
	} {
		if !strings.Contains(nightly, contract) {
			t.Errorf("nightly workflow missing receipt contract %q", contract)
		}
	}
	loop := strings.Index(nightly, "for scenario in")
	if loop < 0 {
		t.Fatal("nightly workflow has no sequential receipt scenario loop")
	}
	ordered := nightly[loop:]
	for _, scenario := range scenarios {
		index := strings.Index(ordered, scenario.name)
		if index < 0 {
			t.Errorf("nightly receipt scenario %s is missing or out of order", scenario.name)
			continue
		}
		ordered = ordered[index+len(scenario.name):]
		if !strings.Contains(nightly, "testbed/bench/out/"+scenario.name+"/") {
			t.Errorf("nightly workflow does not upload %s artifacts", scenario.name)
		}
	}
}

func receiptImageShellFunctions(t *testing.T) string {
	t.Helper()
	script, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, found := strings.Cut(string(script), "pin_receipt_image() {\n")
	if !found {
		t.Fatal("run.sh has no receipt image pin")
	}
	body, _, found := strings.Cut(rest, "\npin_receipt_image\n")
	if !found {
		t.Fatal("run.sh has no receipt image provenance verifier")
	}
	return "pin_receipt_image() {\n" + body + "\n"
}

func TestReceiptBenchImageProvenanceFailsClosed(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is required for the receipt harness")
	}
	imageID := "sha256:" + strings.Repeat("a", 64)
	wrongID := "sha256:" + strings.Repeat("b", 64)
	commit := strings.Repeat("c", 40)
	for _, tc := range []struct {
		name         string
		wantErr      string
		graph        bool
		missingSpool bool
	}{
		{name: "three matching replicas"},
		{name: "wrong pinned ID", wantErr: "differs from pinned"},
		{name: "wrong pinned commit", wantErr: "differs from pinned"},
		{name: "missing source commit", wantErr: "lacks a full source commit"},
		{name: "image retagged", wantErr: "changed since startup"},
		{name: "missing replica", wantErr: "has no unique running container"},
		{name: "replica image drift", wantErr: "not a running, restart-free"},
		{name: "wrong Compose project", wantErr: "not a running, restart-free"},
		{name: "replica restarted", wantErr: "not a running, restart-free"},
		{name: "replica recreated", wantErr: "was recreated during the run"},
		{name: "runtime user drift", wantErr: "mismatched runtime user or CPU placement"},
		{name: "CPU placement drift", wantErr: "mismatched runtime user or CPU placement"},
		{name: "empty CPU placement"},
		{name: "graph three validated spool proofs", graph: true},
		{name: "graph missing one spool proof", wantErr: "missing graph Snapshot spool provenance", graph: true, missingSpool: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outDir := t.TempDir()
			fixture := `set -euo pipefail
COMPOSE_FILES=(-f fixture.yml)
COMPOSE_PROJECT_NAME=lantern-bench-fixture
receipt_image_ref=lantern:local
receipt_image_id=""
receipt_image_commit=""
receipt_container_ids=()
receipt_driver=1
native_spool_provenance=()
runtime_owner=501:20
export LANTERN_BENCH_CPUSET=0-3
if [[ "$CASE" == "empty CPU placement" ]]; then LANTERN_BENCH_CPUSET=""; fi
if [[ "$GRAPH_PROFILE" == 1 ]]; then
 receipt_driver=0
 # These are the previously validated native mount proofs. This verifier
 # consumes them; native mount/UID validation has its own fixture below.
 for index in 0 1 2; do
  native_spool_provenance[$index]="$(jq -nc --arg volume "snapshot-spool-$index" --arg owner "$runtime_owner" '
   {volume:$volume,target:"/var/lib/lantern-snapshot-spool",runtime_user:$owner,mode:"0700",read_write:true,configured_nocopy:true}')"
 done
fi
die() { printf 'fatal: %s\n' "$*" >&2; exit 37; }
log() { :; }
docker() {
  if [[ "$1" == image && "$2" == inspect ]]; then
    if [[ "$4" == '{{.Id}}' ]]; then
      if [[ "$CASE" == "image retagged" && -f "$OUTDIR/proof_pre.json" ]]; then
        echo "$WRONG_ID"
      else
        echo "$GOOD_ID"
      fi
      return
    fi
    if [[ "$CASE" == "missing source commit" ]]; then
      echo "LANTERN_COMMIT="
    else
      echo "LANTERN_COMMIT=$GOOD_COMMIT"
    fi
    return
  fi
  if [[ "$1" == compose ]]; then
    case "${@: -1}" in
      lantern-0) printf '%064d\n' 1 ;;
      lantern-1)
        if [[ "$CASE" == "missing replica" ]]; then
          return
        elif [[ "$CASE" == "replica recreated" && -f "$OUTDIR/proof_pre.json" ]]; then
          printf '%064d\n' 9
        else
          printf '%064d\n' 2
        fi ;;
      lantern-2) printf '%064d\n' 3 ;;
      *) return 88 ;;
    esac
    return
  fi
  if [[ "$1" == inspect ]]; then
    local container="${@: -1}" service image="$GOOD_ID" project="$COMPOSE_PROJECT_NAME"
    case "${container: -1}" in
      1) service=lantern-0 ;;
      2|9) service=lantern-1 ;;
      3) service=lantern-2 ;;
      *) return 89 ;;
    esac
    if [[ "$CASE" == "replica image drift" && "$service" == "lantern-0" ]]; then image="$WRONG_ID"; fi
    if [[ "$CASE" == "wrong Compose project" && "$service" == "lantern-0" ]]; then project=unrelated; fi
    local restarts=0
    if [[ "$CASE" == "replica restarted" && "$service" == "lantern-0" ]]; then restarts=1; fi
    local actual_user=501:20 actual_cpuset="$LANTERN_BENCH_CPUSET"
    if [[ "$service" == lantern-1 && -f "$OUTDIR/proof_pre.json" ]]; then
      if [[ "$CASE" == "runtime user drift" ]]; then actual_user=0:0; fi
      if [[ "$CASE" == "CPU placement drift" ]]; then actual_cpuset=4-7; fi
    fi
    printf '%s|%s|lantern:local|true|%s|%s|%s|%s|%s\n' "$container" "$image" "$project" "$service" "$restarts" "$actual_user" "$actual_cpuset"
    return
  fi
  return 90
}
` + receiptImageShellFunctions(t) + `
pin_receipt_image
verify_receipt_image_provenance "$OUTDIR/proof_pre.json"
if [[ "$CASE" == "graph missing one spool proof" ]]; then
 unset 'native_spool_provenance[1]'
fi
verify_receipt_image_provenance "$OUTDIR/proof_post.json"
`
			pinnedID := imageID
			if tc.name == "wrong pinned ID" {
				pinnedID = wrongID
			}
			pinnedCommit := commit
			if tc.name == "wrong pinned commit" {
				pinnedCommit = strings.Repeat("d", 40)
			}
			graphProfile := "0"
			if tc.graph {
				graphProfile = "1"
			}
			cmd := exec.Command("bash", "-c", fixture)
			cmd.Env = append(os.Environ(),
				"OUTDIR="+outDir, "CASE="+tc.name,
				"GOOD_ID="+imageID, "WRONG_ID="+wrongID,
				"GOOD_COMMIT="+commit, "EXPECTED_LANTERN_IMAGE_ID="+pinnedID,
				"EXPECTED_LANTERN_COMMIT="+pinnedCommit,
				"GRAPH_PROFILE="+graphProfile,
			)
			output, err := cmd.CombinedOutput()
			if tc.wantErr != "" {
				if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 37 ||
					!strings.Contains(string(output), tc.wantErr) {
					t.Fatalf("provenance exit = %v, err = %v, output = %s; want %s",
						cmd.ProcessState, err, output, tc.wantErr)
				}
				if _, err := os.Stat(filepath.Join(outDir, "proof_post.json")); !os.IsNotExist(err) {
					t.Fatalf("invalid post-run provenance was recorded: %v", err)
				}
				if tc.missingSpool {
					if _, err := os.Stat(filepath.Join(outDir, "proof_pre.json")); err != nil {
						t.Fatal("missing-spool control never recorded its complete pre-run proof", err)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("valid receipt image provenance = %v, output = %s", err, output)
			}
			data, err := os.ReadFile(filepath.Join(outDir, "proof_post.json"))
			if err != nil {
				t.Fatal(err)
			}
			var proof struct {
				ImageID      string `json:"image_id"`
				SourceCommit string `json:"source_commit"`
				Replicas     []struct {
					ImageID            string `json:"image_id"`
					RuntimeUser        string `json:"runtime_user"`
					CPUSet             string `json:"cpuset"`
					GraphSnapshotSpool *struct {
						Volume           string `json:"volume"`
						Target           string `json:"target"`
						RuntimeUser      string `json:"runtime_user"`
						Mode             string `json:"mode"`
						ReadWrite        bool   `json:"read_write"`
						ConfiguredNocopy bool   `json:"configured_nocopy"`
					} `json:"graph_snapshot_spool"`
				} `json:"replicas"`
			}
			if err := json.Unmarshal(data, &proof); err != nil {
				t.Fatal(err)
			}
			if proof.ImageID != imageID || proof.SourceCommit != commit ||
				len(proof.Replicas) != 3 {
				t.Fatalf("incomplete image proof: %+v", proof)
			}
			for index, replica := range proof.Replicas {
				wantCPUSet := "0-3"
				if tc.name == "empty CPU placement" {
					wantCPUSet = ""
				}
				if replica.ImageID != imageID || replica.RuntimeUser != "501:20" || replica.CPUSet != wantCPUSet {
					t.Fatalf("replica image drift in proof: %+v", proof)
				}
				if !tc.graph {
					if replica.GraphSnapshotSpool != nil {
						t.Fatal("receipt provenance recorded an unrelated graph spool")
					}
					continue
				}
				spool := replica.GraphSnapshotSpool
				if spool == nil || spool.Volume != fmt.Sprintf("snapshot-spool-%d", index) ||
					spool.Target != "/var/lib/lantern-snapshot-spool" || spool.RuntimeUser != "501:20" ||
					spool.Mode != "0700" || !spool.ReadWrite || !spool.ConfiguredNocopy {
					t.Fatalf("missing or altered validated graph spool proof for replica %d: %+v", index, spool)
				}
			}
		})
	}
}

func receiptTLSShellFunction(t *testing.T) string {
	t.Helper()
	script, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, found := strings.Cut(string(script), "verify_receipt_peer_tls() {\n")
	if !found {
		t.Fatal("missing native TLS verifier")
	}
	body, _, found := strings.Cut(rest, "\n# ----- compose up")
	if !found {
		t.Fatal("incomplete native TLS verifier")
	}
	return "verify_receipt_peer_tls() {\n" + body + "\n"
}

func TestReceiptBenchTLSPeerPreflightFailsClosed(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is required for the receipt harness")
	}
	for _, tc := range []struct {
		name, wantErr string
		postFailure   bool
		graph         bool
	}{
		{name: "verified before and after load"},
		{name: "missing own mount", wantErr: "no pinned, read-only"},
		{name: "writable mount", wantErr: "no pinned, read-only"},
		{name: "wrong runtime user", wantErr: "mismatched native runtime user"},
		{name: "missing declared time source", wantErr: "no pinned, read-only declared time source"},
		{name: "writable declared time source", wantErr: "no pinned, read-only declared time source"},
		{name: "foreign declared time source", wantErr: "no pinned, read-only declared time source"},
		{name: "changed declared time source", wantErr: "no pinned, read-only declared time source", postFailure: true},
		{name: "wrong workload identity", wantErr: "missing or mismatched"},
		{name: "retired bearer config", wantErr: "missing or mismatched"},
		{name: "invalid live TLS identity", wantErr: "TLS identity/provenance verification failed"},
		{name: "changed live TLS identity", wantErr: "TLS identity/provenance verification failed", postFailure: true},
		{name: "receipt with unexpected graph spool", wantErr: "unexpectedly mounts a graph Snapshot spool"},
		{name: "graph spool verified before and after load", graph: true},
		{name: "graph missing overlay", graph: true, wantErr: "wrong graph/receipt volume set"},
		{name: "graph missing spool mount", graph: true, wantErr: "mismatched graph Snapshot spool mount"},
		{name: "graph wrong spool name", graph: true, wantErr: "mismatched graph Snapshot spool mount"},
		{name: "graph bind spool", graph: true, wantErr: "mismatched graph Snapshot spool mount"},
		{name: "graph read-only spool", graph: true, wantErr: "mismatched graph Snapshot spool mount"},
		{name: "graph foreign spool label", graph: true, wantErr: "foreign graph Snapshot spool"},
		{name: "graph wrong spool owner", graph: true, wantErr: "wrong UID/GID/mode"},
		{name: "graph wrong spool mode", graph: true, wantErr: "wrong UID/GID/mode"},
		{name: "graph changed spool mode", graph: true, wantErr: "wrong UID/GID/mode", postFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outDir := t.TempDir()
			private := filepath.Join(outDir, "private")
			if err := os.Mkdir(private, 0700); err != nil {
				t.Fatal(err)
			}
			var nodes []any
			services, volumes := map[string]any{}, map[string]any{}
			for index := range 3 {
				environment := map[string]string{"LANTERN_AUTH_MODE": "oidc", "LANTERN_PEER_WORKLOAD_ID": fmt.Sprintf("spiffe://fixture/node-%d", index)}
				mounts := []any{}
				for _, directory := range []struct{ name, target string }{{"state", "/state"}, {"backup", "/data"}} {
					name := fmt.Sprintf("%s-%d", directory.name, index)
					mounts = append(mounts, map[string]any{"type": "volume", "source": name, "target": directory.target})
					volumes[name] = map[string]any{"name": name}
				}
				if tc.graph {
					environment["LANTERN_AUTH_MODE"] = "off"
					environment["LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR"] = "/var/lib/lantern-snapshot-spool"
					if tc.name != "graph missing overlay" {
						name := fmt.Sprintf("snapshot-spool-%d", index)
						mounts = append(mounts, map[string]any{"type": "volume", "source": name, "target": "/var/lib/lantern-snapshot-spool", "volume": map[string]any{"nocopy": true}})
						volumes[name] = map[string]any{"name": name}
					}
				}
				nodes = append(nodes, map[string]any{"name": fmt.Sprintf("node-%d", index), "environment": environment})
				services[fmt.Sprintf("lantern-%d", index)] = map[string]any{"user": "501:20", "volumes": mounts}
			}
			expected := map[string]any{"nodes": nodes}
			configuration, err := json.Marshal(map[string]any{"name": "lantern-bench-fixture", "services": services, "volumes": volumes})
			if err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(expected)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(private, "fixture.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			fixture := `set -euo pipefail
COMPOSE_FILES=(-f fixture.yml)
export COMPOSE_PROJECT_NAME=lantern-bench-fixture
source owned_compose.sh
PEER_TLS_DIR="$OUTDIR/private"
REPO_ROOT="$OUTDIR"
REPLICA_GRPC_PORTS=(6380 6381 6382)
MEMBERSHIP_PID=$$
DIAGNOSTICS_PID=$$
receipt_driver="$TEST_DRIVER"
native_spool_provenance=()
runtime_owner=501:20
postflight=0
die() { printf 'fatal: %s\n' "$*" >&2; exit 37; }
docker() {
 if [[ "$1" == compose ]]; then
  if [[ "$4" == config ]]; then printf '%s\n' "$TEST_CONFIG"; else printf 'container-%s\n' "$6"; fi
  return
 fi
 if [[ "$1" == volume && "$2" == inspect ]]; then
  if [[ "$CASE" == "graph foreign spool label" && "${@: -1}" == snapshot-spool-1 ]]; then echo unrelated; else echo "$COMPOSE_PROJECT_NAME"; fi
  return
 fi
 if [[ "$1" == exec ]]; then
  [[ "$3" == /bin/sh && "${@: -1}" == /var/lib/lantern-snapshot-spool ]] || return 87
  local metadata="$runtime_owner:700"
  if [[ "$2" == container-lantern-1 ]]; then
   if [[ "$CASE" == "graph wrong spool owner" ]]; then metadata=0:0:700; fi
   if [[ "$CASE" == "graph wrong spool mode" || ( "$CASE" == "graph changed spool mode" && "$postflight" == 1 ) ]]; then metadata="$runtime_owner:750"; fi
  fi
  printf '%s\n' "$metadata"
  return
 fi
 [[ "$1" == inspect && "$2" == --format ]] || return 88
 local service="${4#container-}" index="${4##*-}"
 case "$3" in
 '{{.Config.User}}')
  if [[ "$CASE" == "wrong runtime user" && "$index" == 1 ]]; then echo 0:0; else echo "$runtime_owner"; fi
  ;;
 '{{json .Mounts}}')
  local source="$PEER_TLS_DIR/$service" rw=false time_source="$PEER_TLS_DIR/$service/ntp.conf" time_rw=false
  if [[ "$CASE" == "missing own mount" && "$index" == 1 ]]; then source="$PEER_TLS_DIR/lantern-0"; fi
  if [[ "$CASE" == "writable mount" && "$index" == 1 ]]; then rw=true; fi
  if [[ "$CASE" == "missing declared time source" && "$index" == 1 ]]; then time_source=""; fi
  if [[ "$CASE" == "writable declared time source" && "$index" == 1 ]]; then time_rw=true; fi
  if [[ "$CASE" == "foreign declared time source" && "$index" == 1 ]]; then time_source="$PEER_TLS_DIR/lantern-0/ntp.conf"; fi
  if [[ "$CASE" == "changed declared time source" && "$index" == 1 && "$postflight" == 1 ]]; then time_rw=true; fi
  local spool_name="snapshot-spool-$index" spool_type=volume spool_rw=true with_spool="$TEST_GRAPH"
  if [[ "$CASE" == "receipt with unexpected graph spool" ]]; then with_spool=true; fi
  if [[ "$index" == 1 ]]; then
   if [[ "$CASE" == "graph missing spool mount" ]]; then with_spool=false; fi
   if [[ "$CASE" == "graph wrong spool name" ]]; then spool_name=snapshot-spool-0; fi
   if [[ "$CASE" == "graph bind spool" ]]; then spool_type=bind; fi
   if [[ "$CASE" == "graph read-only spool" ]]; then spool_rw=false; fi
  fi
  jq -nc --arg source "$source" --argjson rw "$rw" --arg time_source "$time_source" --argjson time_rw "$time_rw" \
    --arg spool_name "$spool_name" --arg spool_type "$spool_type" --argjson spool_rw "$spool_rw" --argjson with_spool "$with_spool" '
    [{Type:"bind",Destination:"/run/lantern-config",RW:$rw,Source:$source}] +
    (if $time_source=="" then [] else [{Type:"bind",Destination:"/etc/ntp.conf",RW:$time_rw,Source:$time_source}] end) +
    (if $with_spool then [{Type:$spool_type,Destination:"/var/lib/lantern-snapshot-spool",Name:$spool_name,RW:$spool_rw}] else [] end)'
  ;;
 '{{json .Config.Env}}')
  local identity="spiffe://fixture/node-$index" legacy=""
  if [[ "$CASE" == "wrong workload identity" && "$index" == 1 ]]; then identity="spiffe://unrelated/node-1"; fi
  if [[ "$CASE" == "retired bearer config" && "$index" == 1 ]]; then legacy="LANTERN_AUTH_TOKENS=fixture-only"; fi
  jq -nc --arg identity "$identity" --arg legacy "$legacy" --argjson graph "$TEST_GRAPH" '
    ["LANTERN_AUTH_MODE="+(if $graph then "off" else "oidc" end),"LANTERN_PEER_WORKLOAD_ID="+$identity] +
    (if $graph then ["LANTERN_METRICS_ADDR=:9090","LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR=/var/lib/lantern-snapshot-spool"] else [] end) +
    (if $legacy=="" then [] else [$legacy] end)'
  ;;
 *) return 89;;
 esac
}
go() {
 [[ "$1" == run && "$2" == ./testbed/bench/receipttls && "$3" == verify ]] || return 88
 if [[ "$CASE" == "invalid live TLS identity" || ( "$CASE" == "changed live TLS identity" && "$*" == *"-baseline"* ) ]]; then return 86; fi
 local out="" prev="" arg
 for arg in "$@"; do if [[ "$prev" == -out ]]; then out="$arg"; fi; prev="$arg"; done
 printf '{"ca_sha256":"fixture"}\n' > "$out"
}
` + receiptTLSShellFunction(t) + `
if [[ "$receipt_driver" == 0 ]]; then
 verify_native_workload_settings
 printf '%s\n' "${native_spool_provenance[@]}" | jq -s . > "$OUTDIR/tls_pre.json"
 postflight=1
 verify_native_workload_settings
 printf '%s\n' "${native_spool_provenance[@]}" | jq -s . > "$OUTDIR/tls_post.json"
else
 verify_receipt_peer_tls "$OUTDIR/tls_pre.json"
 postflight=1
 verify_receipt_peer_tls "$OUTDIR/tls_post.json" "$OUTDIR/tls_pre.json"
fi
`
			command := exec.Command("bash", "-c", fixture)
			driver := "1"
			if tc.graph {
				driver = "0"
			}
			command.Env = append(os.Environ(), "OUTDIR="+outDir, "CASE="+tc.name, "TEST_CONFIG="+string(configuration), "TEST_DRIVER="+driver, fmt.Sprintf("TEST_GRAPH=%t", tc.graph))
			output, err := command.CombinedOutput()
			if tc.wantErr != "" {
				if command.ProcessState == nil || command.ProcessState.ExitCode() != 37 || !strings.Contains(string(output), tc.wantErr) {
					t.Fatalf("native preflight: %v %s; want %s", err, output, tc.wantErr)
				}
				if _, err := os.Stat(filepath.Join(outDir, "tls_post.json")); !os.IsNotExist(err) {
					t.Fatal("invalid postflight was recorded")
				}
				if _, err := os.Stat(filepath.Join(outDir, "tls_pre.json")); tc.postFailure != (err == nil) {
					t.Fatal("wrong preflight result")
				}
			} else if err != nil {
				t.Fatalf("valid native TLS proof: %v %s", err, output)
			} else if tc.graph {
				raw, err := os.ReadFile(filepath.Join(outDir, "tls_post.json"))
				if err != nil {
					t.Fatal(err)
				}
				var proof []map[string]any
				if err := json.Unmarshal(raw, &proof); err != nil || len(proof) != 3 {
					t.Fatalf("incomplete graph spool provenance: %s (%v)", raw, err)
				}
				for index, spool := range proof {
					if spool["volume"] != fmt.Sprintf("snapshot-spool-%d", index) || spool["target"] != "/var/lib/lantern-snapshot-spool" ||
						spool["mode"] != "0700" || spool["runtime_user"] != "501:20" || spool["read_write"] != true || spool["configured_nocopy"] != true {
						t.Fatalf("wrong graph spool provenance: %+v", spool)
					}
				}
			}
		})
	}
}

func TestReceiptBenchCleanupPreservesFailureAndProjectScope(t *testing.T) {
	script, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, found := strings.Cut(string(script), "cleanup() {\n")
	if !found {
		t.Fatal("run.sh is missing cleanup")
	}
	body, _, found := strings.Cut(rest, "\n}\ntrap cleanup EXIT")
	if !found {
		t.Fatal("run.sh cleanup is not registered as an EXIT trap")
	}
	cleanup := "cleanup() {\n" + body + "\n}\n"

	for _, tc := range []struct {
		name         string
		dockerStatus int
		logsStatus   int
		runStatus    int
		keepUp       int
		wantStatus   int
		wantDown     bool
		wantBridge   bool
		bridgeLink   bool
		captureError string
	}{
		{name: "clean teardown", wantDown: true},
		{name: "teardown failure disqualifies pass", dockerStatus: 19, wantStatus: 1, wantDown: true, wantBridge: true},
		{name: "primary failure preserved", dockerStatus: 19, runStatus: 23, wantStatus: 23, wantDown: true, wantBridge: true},
		{name: "explicit keep up", dockerStatus: 19, keepUp: 1},
		{name: "primary failure with completed teardown", runStatus: 23, wantStatus: 23, wantDown: true, wantBridge: true},
		{name: "log capture failure preserves primary and teardown", logsStatus: 17, runStatus: 23, wantStatus: 23, wantDown: true, wantBridge: true},
		{name: "log exit sidecar write failure preserves primary and teardown", runStatus: 23, wantStatus: 23, wantDown: true, wantBridge: true, captureError: "sidecar"},
		{name: "symlink diagnostics excluded", runStatus: 23, wantStatus: 23, wantDown: true, bridgeLink: true},
		{name: "bridge evidence mktemp failure preserves teardown status", dockerStatus: 19, wantStatus: 1, wantDown: true, captureError: "mktemp"},
		{name: "bridge evidence chmod failure preserves teardown status", dockerStatus: 19, wantStatus: 1, wantDown: true, captureError: "chmod"},
		{name: "bridge evidence cat failure preserves teardown status", dockerStatus: 19, wantStatus: 1, wantDown: true, captureError: "cat"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outDir := t.TempDir()
			workspace := filepath.Join(outDir, "out", ".peer-tls.fixture")
			trust := filepath.Join(workspace, "trust")
			failureRoot := filepath.Join(outDir, "failure-evidence")
			for _, directory := range []string{trust, failureRoot} {
				if err := os.MkdirAll(directory, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			const bridgeLog = "{\"event\":\"request_finish\",\"reason\":\"timeout\"}\n"
			const serverLog = "lantern-0-1 | Snapshot failed phase=live_replay\nlantern-1-1 | Snapshot failed phase=seal\n"
			const privateToken = "never-copy-native-credential"
			if err := os.WriteFile(filepath.Join(trust, "tokens.json"), []byte(privateToken), 0o600); err != nil {
				t.Fatal(err)
			}
			bridgePath := filepath.Join(workspace, "diagnostics.log")
			if tc.bridgeLink {
				if err := os.Symlink(filepath.Join(trust, "tokens.json"), bridgePath); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(bridgePath, []byte(bridgeLog), 0o600); err != nil {
				t.Fatal(err)
			}
			reportPath := filepath.Join(outDir, "report.md")
			if err := os.WriteFile(reportPath, []byte("**Perf gate verdict:** `pass`\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			fixture := fmt.Sprintf(`set -euo pipefail
export COMPOSE_PROJECT_NAME=lantern-bench-cleanup-fixture
COMPOSE_STARTED=1
COMPOSE_FILES=(-f fixture-compose.yml)
source owned_compose.sh
stop_owned_processes() { :; }
KEEP_UP=%d
log() { :; }
case "${CAPTURE_ERROR:-}" in
  mktemp) mktemp() { return 29; };;
  chmod) chmod() { return 29; };;
  cat) cat() { return 29; };;
  sidecar) printf() {
    if [[ "$#" == 2 && "$1" == '%%s\n' && "$2" =~ ^[0-9]+$ ]]; then return 29; fi
    builtin printf "$@"
  };;
esac
docker() {
  [[ "$COMPOSE_PROJECT_NAME" == "lantern-bench-cleanup-fixture" ]] || return 88
  case "$*" in
    "compose -f fixture-compose.yml logs --no-color")
      printf 'capture\n' >> "$OUTDIR/log-capture-calls"
      printf '%%s\n' 'lantern-0-1 | Snapshot failed phase=live_replay' 'lantern-1-1 | Snapshot failed phase=seal'
      return %d;;
    "compose -f fixture-compose.yml --profile off-diagnostics down -v --remove-orphans")
      echo "docker down invoked" >&2
      return %d;;
    "ps -aq --filter label=com.docker.compose.project=lantern-bench-cleanup-fixture" | "volume ls -q --filter label=com.docker.compose.project=lantern-bench-cleanup-fixture" | "network ls -q --filter label=com.docker.compose.project=lantern-bench-cleanup-fixture") return 0;;
    *) return 89;;
  esac
}

%s
trap cleanup EXIT
exit %d
`, tc.keepUp, tc.logsStatus, tc.dockerStatus, cleanup, tc.runStatus)
			cmd := exec.Command("bash", "-c", fixture)
			cmd.Env = append(os.Environ(), "OUTDIR="+outDir, "HERE="+outDir,
				"NATIVE_WORK_DIR="+workspace, "PEER_TLS_DIR="+trust, "TMPDIR="+failureRoot, "CAPTURE_ERROR="+tc.captureError)
			output, err := cmd.CombinedOutput()
			if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != tc.wantStatus {
				t.Fatalf("cleanup exit = %v, err = %v, output = %s; want %d",
					cmd.ProcessState, err, output, tc.wantStatus)
			}
			if got := strings.Contains(string(output), "docker down invoked"); got != tc.wantDown {
				t.Errorf("compose down invoked = %v, want %v; output = %s", got, tc.wantDown, output)
			}
			captures, captureErr := os.ReadFile(filepath.Join(outDir, "log-capture-calls"))
			if tc.runStatus != 0 {
				if captureErr != nil || string(captures) != "capture\n" {
					t.Fatalf("log capture did not run exactly once: %q %v", captures, captureErr)
				}
			} else if !os.IsNotExist(captureErr) {
				t.Fatalf("unexpected log capture: %q %v", captures, captureErr)
			}
			if tc.dockerStatus != 0 && tc.wantDown && !strings.Contains(string(output), "run is unqualified") {
				t.Errorf("teardown failure is not reported: %s", output)
			}
			report, err := os.ReadFile(reportPath)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := strings.Contains(string(report), "unqualified"), (tc.dockerStatus != 0 && tc.wantDown) || tc.runStatus != 0; got != want {
				t.Errorf("report marked unqualified = %v, want %v: %s", got, want, report)
			}
			evidence, err := filepath.Glob(filepath.Join(failureRoot, "lantern-native-failure.*"))
			if err != nil {
				t.Fatal(err)
			}
			wantCount := 0
			if tc.runStatus != 0 || tc.wantBridge || tc.captureError != "" && tc.captureError != "mktemp" {
				wantCount = 1
			}
			if len(evidence) != wantCount {
				t.Fatalf("failure evidence = %v, want count=%d", evidence, wantCount)
			}
			for _, directory := range evidence {
				info, err := os.Stat(directory)
				if err != nil || info.Mode().Perm() != 0o700 {
					t.Fatalf("private evidence directory mode: %v %v", info, err)
				}
				files, err := os.ReadDir(directory)
				if err != nil {
					t.Fatal(err)
				}
				for _, file := range files {
					if file.Name() != "server.log" && file.Name() != "server.log.exit-code" && file.Name() != "diagnostics.log" {
						t.Fatalf("native trust input copied to evidence: %s", file.Name())
					}
					data, err := os.ReadFile(filepath.Join(directory, file.Name()))
					if err != nil || strings.Contains(string(data), privateToken) {
						t.Fatalf("private trust leaked to evidence: %v", err)
					}
				}
				serverPath := filepath.Join(directory, "server.log")
				serverData, serverErr := os.ReadFile(serverPath)
				if tc.runStatus != 0 {
					serverInfo, statErr := os.Stat(serverPath)
					if serverErr != nil || string(serverData) != serverLog || statErr != nil || serverInfo.Mode().Perm() != 0o600 {
						t.Fatalf("replica-prefixed private server log lost or wrong mode: %q %v %v", serverData, serverErr, statErr)
					}
				} else if !os.IsNotExist(serverErr) {
					t.Fatalf("unexpected server evidence: %q %v", serverData, serverErr)
				}
				exitPath := filepath.Join(directory, "server.log.exit-code")
				exitData, exitErr := os.ReadFile(exitPath)
				if tc.runStatus != 0 {
					wantExit := fmt.Sprintf("%d\n", tc.logsStatus)
					if tc.captureError == "sidecar" {
						wantExit = ""
						if !strings.Contains(string(output), "failed to retain Compose log capture exit code") {
							t.Fatalf("sidecar write failure was not reported: %s", output)
						}
					}
					exitInfo, statErr := os.Stat(exitPath)
					if exitErr != nil || string(exitData) != wantExit || statErr != nil || exitInfo.Mode().Perm() != 0o600 {
						t.Fatalf("private log exit sidecar = %q, read=%v stat=%v; want %q mode 0600", exitData, exitErr, statErr, wantExit)
					}
				} else if !os.IsNotExist(exitErr) {
					t.Fatalf("unexpected log exit sidecar: %q %v", exitData, exitErr)
				}
				path := filepath.Join(directory, "diagnostics.log")
				data, err := os.ReadFile(path)
				if tc.wantBridge {
					info, statErr := os.Stat(path)
					if err != nil || string(data) != bridgeLog || statErr != nil || info.Mode().Perm() != 0o600 {
						t.Fatalf("private bridge log lost or wrong mode: %q %v %v", data, err, statErr)
					}
				} else if tc.captureError == "cat" {
					if err != nil || len(data) != 0 {
						t.Fatalf("failed capture marked complete: %q %v", data, err)
					}
				} else if !os.IsNotExist(err) {
					t.Fatalf("unexpected bridge evidence: %q %v", data, err)
				}
			}
			_, statErr := os.Stat(workspace)
			wantRetained := tc.keepUp == 1 || tc.dockerStatus != 0 && tc.wantDown
			if wantRetained && statErr != nil || !wantRetained && !os.IsNotExist(statErr) {
				t.Fatalf("native workspace cleanup: retained=%v err=%v", wantRetained, statErr)
			}
			if tc.captureError != "" && !strings.Contains(string(output), "failed to") {
				t.Fatalf("capture failure not reported: %s", output)
			}
		})
	}
}

func TestBenchFreshComposeRejectsExistingProjectBeforeLifecycle(t *testing.T) {
	script, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, startup, found := strings.Cut(string(script), "# ----- compose up ------------------------------------------------------------\n")
	if !found {
		t.Fatal("missing fresh Compose startup")
	}
	startup, _, found = strings.Cut(startup, "# ----- discover actual published ports")
	if !found {
		t.Fatal("incomplete fresh Compose startup")
	}
	_, cleanup, found := strings.Cut(string(script), "cleanup() {\n")
	if !found {
		t.Fatal("missing owned Compose cleanup")
	}
	cleanup, _, found = strings.Cut(cleanup, "\n}\ntrap cleanup EXIT")
	if !found {
		t.Fatal("incomplete owned Compose cleanup")
	}
	for _, tc := range []struct {
		name     string
		wantCode int
		wantOwn  bool
	}{
		{name: "absent", wantOwn: true},
		{name: "absent OFF", wantOwn: true},
		{name: "initialization failure", wantCode: 37, wantOwn: true},
		{name: "container", wantCode: 37},
		{name: "volume", wantCode: 37},
		{name: "network", wantCode: 37},
		{name: "container lookup failure", wantCode: 37},
		{name: "volume lookup failure", wantCode: 37},
		{name: "network lookup failure", wantCode: 37},
		{name: "skip startup"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outDir := t.TempDir()
			fixture := `set -euo pipefail
export COMPOSE_PROJECT_NAME=lantern-bench-fresh-fixture
COMPOSE_FILES=(-f fixture.yml)
COMPOSE_STARTED=0
KEEP_UP=0
PEER_TLS_DIR=""
receipt_driver=1
if [[ "$CASE" == "absent OFF" ]]; then receipt_driver=0; fi
receipt_image_id=fixture
runtime_owner=501:20
prod_pids=()
sub_pids=()
SKIP_UP=0
if [[ "$CASE" == "skip startup" ]]; then SKIP_UP=1; fi
source owned_compose.sh
die() { printf 'fatal: %s\n' "$*" >&2; exit 37; }
log() { :; }
stop_owned_processes() { :; }
owned_compose_volume_layout() { printf '{"count":%s}\n' "$((receipt_driver == 1 ? 6 : 9))"; }
initialize_owned_compose_volumes() {
 [[ "$COMPOSE_STARTED" == 1 && "$KEEP_UP" == 1 ]] || return 88
 printf 'initialize\n' >> "$OUTDIR/actions"
}
initialize_owned_compose_security() {
 [[ "$receipt_driver" == 1 && "$COMPOSE_STARTED" == 1 && "$KEEP_UP" == 1 ]] || return 88
 printf 'security initialize\n' >> "$OUTDIR/actions"
 [[ "$CASE" != "initialization failure" ]]
}
docker() {
 local family
 case "$1" in
  ps) family=container;;
  volume) family=volume;;
  network) family=network;;
  compose)
   case "$4" in
    config) printf '{}\n';;
    up) printf 'up\n' >> "$OUTDIR/actions";;
    --profile) printf '%s\n' "$6" >> "$OUTDIR/actions";;
    *) return 89;;
   esac
   return;;
  *) return 89;;
 esac
 [[ "$*" == *"label=com.docker.compose.project=$COMPOSE_PROJECT_NAME"* ]] || return 89
 if [[ "$CASE" == "$family lookup failure" ]]; then return 19; fi
 if [[ "$CASE" == "$family" ]]; then printf 'existing-owned-resource\n'; fi
}
cleanup() {
` + cleanup + "\n}\ntrap cleanup EXIT\n" + startup
			command := exec.Command("bash", "-c", fixture)
			command.Env = append(os.Environ(), "OUTDIR="+outDir, "CASE="+tc.name)
			output, err := command.CombinedOutput()
			if command.ProcessState == nil || command.ProcessState.ExitCode() != tc.wantCode {
				t.Fatalf("fresh startup exit=%v err=%v output=%s, want %d", command.ProcessState, err, output, tc.wantCode)
			}
			if tc.wantCode != 0 && tc.name != "initialization failure" && !strings.Contains(string(output), "already present or its absence cannot be verified") {
				t.Fatalf("existing project was not refused at absence boundary: %s", output)
			}
			actions, readErr := os.ReadFile(filepath.Join(outDir, "actions"))
			if tc.wantOwn {
				want := "initialize\nsecurity initialize\nup\ndown\n"
				switch tc.name {
				case "absent OFF":
					want = "initialize\nup\ndown\n"
				case "initialization failure":
					want = "initialize\nsecurity initialize\n"
					if !strings.Contains(string(output), "no serving started") {
						t.Fatalf("initialization failure did not stop before serving: %s", output)
					}
				}
				if readErr != nil || string(actions) != want {
					t.Fatalf("fresh owned lifecycle=%q err=%v", actions, readErr)
				}
			} else if !os.IsNotExist(readErr) {
				t.Fatalf("refused or skipped startup changed existing resources: %q err=%v", actions, readErr)
			}
		})
	}
}

func receiptSnapshotShellFunctions(t *testing.T) string {
	t.Helper()
	script, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, found := strings.Cut(string(script), "receipt_runtime_scalar() {\n")
	if !found {
		t.Fatal("run.sh is missing receipt_runtime_scalar")
	}
	body, _, found := strings.Cut(rest, "\n}\n\nrun_ghz() {")
	if !found {
		t.Fatal("run.sh receipt_runtime_scalar and snapshot_runtime do not precede run_ghz")
	}
	return "receipt_runtime_scalar() {\n" + body + "\n}\n"
}

func TestReceiptBenchEdgeAddPreparationBoundary(t *testing.T) {
	script, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, found := strings.Cut(string(script), "prepare_receipt_edge_add() {\n")
	if !found {
		t.Fatal("missing protected EdgeAdd preparation")
	}
	body, _, found := strings.Cut(rest, "\n}\n\nrun_receipt_probe() {")
	if !found {
		t.Fatal("incomplete protected EdgeAdd preparation")
	}
	prepareAt := strings.Index(string(script), "\nprepare_receipt_edge_add\n")
	warmupAt := strings.Index(string(script), "# ----- WARMUP ")
	preAt := strings.Index(string(script), `snapshot_runtime "$OUTDIR/runtime_pre.json"`)
	if prepareAt < 0 || warmupAt <= prepareAt || preAt <= warmupAt {
		t.Fatal("endpoint preparation must precede warmup and the PRE memory snapshot")
	}
	for _, tc := range []struct {
		name   string
		driver string
		exit   int
	}{
		{"protected Add", "receipt_edge_add", 0},
		{"preparation failure", "receipt_edge_add", 23},
		{"other family", "receipt_vertex_delete", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := t.TempDir()
			fixture := `set -euo pipefail
receipt_driver=1
target_driver="$TEST_DRIVER"
REPO_ROOT="$PWD"
PEER_TLS_DIR="$OUTDIR/trust"
warmup_duration=10s
warmup_rps=200
steady_duration=45s
steady_rps=200
endpoints=(localhost:6380 localhost:6381 localhost:6382)
die() { exit 91; }
go() {
 [[ "$1 $2 $3" == 'run ./testbed/bench/receiptprobe prepare-edge-add' ]] || return 92
 shift 3
 local eps="" ca="" output="" warmdur="" warmrps="" steadydur="" steadyrps=""
 while (( $# )); do
  case "$1" in
   -endpoints) eps="$2";;
   -ca-cert) ca="$2";;
   -output) output="$2";;
   -warmup-duration) warmdur="$2";;
   -warmup-pair-rps) warmrps="$2";;
   -steady-duration) steadydur="$2";;
   -steady-pair-rps) steadyrps="$2";;
   *) return 93;;
  esac
  shift 2
 done
 [[ "$eps" == 'https://localhost:6380,https://localhost:6381,https://localhost:6382' &&
    "$ca" == "$PEER_TLS_DIR/ca.pem" && "$output" == "$OUTDIR/edge_add_fixture.json" &&
    "$warmdur/$warmrps/$steadydur/$steadyrps" == '10s/100/45s/100' ]] || return 94
 touch "$OUTDIR/preparation-called"
 return "$FIXTURE_EXIT"
}
prepare_receipt_edge_add() {
` + body + "\n}\nprepare_receipt_edge_add\ntouch \"$OUTDIR/after-preparation\"\n"
			command := exec.Command("bash", "-c", fixture)
			command.Env = append(os.Environ(), "OUTDIR="+out, "TEST_DRIVER="+tc.driver, fmt.Sprintf("FIXTURE_EXIT=%d", tc.exit))
			output, err := command.CombinedOutput()
			if command.ProcessState == nil || command.ProcessState.ExitCode() != tc.exit {
				t.Fatalf("preparation exit=%v err=%v output=%s", command.ProcessState, err, output)
			}
			_, callErr := os.Stat(filepath.Join(out, "preparation-called"))
			if tc.driver == "receipt_edge_add" && callErr != nil || tc.driver != "receipt_edge_add" && !os.IsNotExist(callErr) {
				t.Fatalf("unexpected preparation side effect: %v", callErr)
			}
			_, afterErr := os.Stat(filepath.Join(out, "after-preparation"))
			if tc.exit == 0 && afterErr != nil || tc.exit != 0 && !os.IsNotExist(afterErr) {
				t.Fatalf("failed preparation crossed the measurement boundary: %v", afterErr)
			}
		})
	}
}

func TestReceiptBenchWarmupRetainsPairedSummariesOnFailure(t *testing.T) {
	script, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, found := strings.Cut(string(script), "run_receipt_probe() {\n")
	if !found {
		t.Fatal("missing receipt probe wrapper")
	}
	body, _, found := strings.Cut(rest, "\n}\n\nsnapshot_search_lifecycle() {")
	if !found {
		t.Fatal("incomplete receipt probe wrapper")
	}
	_, warmup, found := strings.Cut(string(script), "# ----- WARMUP ----------------------------------------------------------------\n")
	if !found {
		t.Fatal("missing warmup block")
	}
	warmup, _, found = strings.Cut(warmup, "\n# ----- PRE snapshot")
	if !found {
		t.Fatal("incomplete warmup block")
	}
	for _, exitCode := range []int{0, 23} {
		t.Run(fmt.Sprintf("producer_exit_%d", exitCode), func(t *testing.T) {
			out := t.TempDir()
			fixture := `set -euo pipefail
receipt_driver=1
target_driver=receipt_vertex_delete
SCENARIO_FILE=fixture.yaml
REPO_ROOT="$PWD"
PEER_TLS_DIR="$OUTDIR/trust"
warmup_conc=12
warmup_rps=200
warmup_duration=10s
endpoints=(localhost:6380 localhost:6381 localhost:6382)
log() { :; }
die() { echo "unexpected receipt wrapper failure" >&2; exit 91; }
yq() {
 case "$2" in
  '.target.calls | length') echo 2;;
  '.target.calls[0].name') echo receipt_admission;;
  '.target.calls[0].call') echo graph.v1.LanternService/DeleteVertex;;
  '.target.calls[1].name') echo receipt_lookup;;
  '.target.calls[1].call') echo graph.v1.LanternService/GetReceiptStatus;;
  *) return 92;;
 esac
}
go() {
 [[ "$1 $2" == 'run ./testbed/bench/receiptprobe' ]] || return 93
 shift 2
 local admission="" lookup="" phase="" duration="" pair="" concurrency=""
 while (( $# )); do
  case "$1" in
   -admission-report) admission="$2";;
   -lookup-report) lookup="$2";;
   -phase) phase="$2";;
   -duration) duration="$2";;
   -pair-rps) pair="$2";;
   -concurrency) concurrency="$2";;
   -endpoints|-family|-ca-file) :;;
   *) return 94;;
  esac
  shift 2
 done
 [[ "$phase/$duration/$pair/$concurrency" == 'warmup/10s/100/12' ]] || return 95
 [[ "$admission" == "$OUTDIR/ghz_warmup_0_receipt_admission.json" &&
    "$lookup" == "$OUTDIR/ghz_warmup_1_receipt_lookup.json" ]] || return 96
 if [[ "$FIXTURE_EXIT" == 0 ]]; then
  printf '{"count":1000,"statusCodeDistribution":{"OK":1000}}\n' > "$admission"
  printf '{"count":1000,"statusCodeDistribution":{"OK":1000}}\n' > "$lookup"
 else
  printf '{"count":997,"statusCodeDistribution":{"OK":996,"Unavailable":1}}\n' > "$admission"
  printf '{"count":996,"statusCodeDistribution":{"OK":996}}\n' > "$lookup"
 fi
 return "$FIXTURE_EXIT"
}
run_receipt_probe() {
` + body + "\n}\n" + warmup + "\ntouch \"$OUTDIR/after-warmup\"\n"
			command := exec.Command("bash", "-c", fixture)
			command.Env = append(os.Environ(), "OUTDIR="+out, fmt.Sprintf("FIXTURE_EXIT=%d", exitCode))
			output, err := command.CombinedOutput()
			if command.ProcessState == nil || command.ProcessState.ExitCode() != exitCode {
				t.Fatalf("warmup exit=%v err=%v output=%s, want %d", command.ProcessState, err, output, exitCode)
			}
			wantAdmission, wantLookup := 1000, 1000
			if exitCode != 0 {
				wantAdmission, wantLookup = 997, 996
			}
			for name, wantCount := range map[string]int{"ghz_warmup_0_receipt_admission.json": wantAdmission, "ghz_warmup_1_receipt_lookup.json": wantLookup} {
				raw, err := os.ReadFile(filepath.Join(out, name))
				var report struct {
					Count  int            `json:"count"`
					Status map[string]int `json:"statusCodeDistribution"`
				}
				if err != nil || json.Unmarshal(raw, &report) != nil || report.Count != wantCount {
					t.Fatalf("paired summary missing after exit: %s %q %v", name, raw, err)
				}
				if wantCount == 997 && report.Status["Unavailable"] != 1 {
					t.Fatalf("failed admission status lost: %+v", report)
				}
			}
			_, err = os.Stat(filepath.Join(out, "after-warmup"))
			if exitCode == 0 && err != nil || exitCode != 0 && !os.IsNotExist(err) {
				t.Fatalf("failure did not stop warmup boundary: %v", err)
			}
		})
	}
}

func TestReceiptBenchOperatorMetricsFlagIsSteadySamplerOnly(t *testing.T) {
	raw, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, found := strings.Cut(string(raw), "run_receipt_probe() {\n")
	if !found {
		t.Fatal("missing receipt probe wrapper")
	}
	body, _, found := strings.Cut(rest, "\n}\n\nsnapshot_search_lifecycle() {")
	if !found {
		t.Fatal("incomplete receipt probe wrapper")
	}
	for _, family := range []struct{ driver, call string }{
		{"receipt_vertex_put", "PutVertex"}, {"receipt_vertex_delete", "DeleteVertex"},
		{"receipt_edge_delete", "DeleteEdge"}, {"receipt_edge_add", "AddEdge"}, {"ghz", "unsupported"},
	} {
		for _, phase := range []string{"warmup", "steady"} {
			t.Run(family.driver+"/"+phase, func(t *testing.T) {
				out := t.TempDir()
				fixture := `set -euo pipefail
target_driver="$TARGET_DRIVER"
SCENARIO_FILE=fixture.yaml
REPO_ROOT="$PWD"
PEER_TLS_DIR="$OUTDIR/trust"
endpoints=(localhost:6380 localhost:6381 localhost:6382)
REPLICA_METRICS_PORTS=(19390 19391 19392)
die() { exit 91; }
yq() {
 case "$2" in
  '.target.calls | length') echo 2;;
  '.target.calls[0].name') echo receipt_admission;;
  '.target.calls[0].call') echo "graph.v1.LanternService/$EXPECTED_CALL";;
  '.target.calls[1].name') echo receipt_lookup;;
  '.target.calls[1].call') echo graph.v1.LanternService/GetReceiptStatus;;
  '.leak_gate.steady_sample_interval') echo 5s;;
  *) return 92;;
 esac
}
go() { printf '%s\n' "$@" > "$OUTDIR/args"; }
run_receipt_probe() {
` + body + "\n}\nrun_receipt_probe \"$PHASE\" 12 200 45s \"$OUTDIR/admission.json\" \"$OUTDIR/lookup.json\"\n"
				command := exec.Command("bash", "-c", fixture)
				command.Env = append(os.Environ(), "OUTDIR="+out, "TARGET_DRIVER="+family.driver, "EXPECTED_CALL="+family.call, "PHASE="+phase)
				output, err := command.CombinedOutput()
				if family.driver == "ghz" {
					if command.ProcessState == nil || command.ProcessState.ExitCode() != 91 {
						t.Fatalf("non-receipt entered receipt sampler wrapper: %v, %s", err, output)
					}
					if _, err := os.Stat(filepath.Join(out, "args")); !os.IsNotExist(err) {
						t.Fatalf("unsupported workload started a receipt client: %v", err)
					}
					return
				}
				if err != nil {
					t.Fatalf("receipt wrapper: %v, %s", err, output)
				}
				captured, err := os.ReadFile(filepath.Join(out, "args"))
				if err != nil {
					t.Fatal(err)
				}
				args := strings.Split(strings.TrimSuffix(string(captured), "\n"), "\n")
				value := func(flag string) string {
					index := slices.Index(args, flag)
					if index < 0 || index+1 >= len(args) {
						return ""
					}
					return args[index+1]
				}
				if len(args) < 2 || args[0] != "run" || args[1] != "./testbed/bench/receiptprobe" ||
					value("-endpoints") != "https://localhost:6380,https://localhost:6381,https://localhost:6382" ||
					value("-ca-file") != out+"/trust/ca.pem" || value("-phase") != phase || value("-pair-rps") != "100" {
					t.Fatalf("workload transport/configuration changed: %q", args)
				}
				flagCount := 0
				for _, arg := range args {
					if arg == "-metrics-operator-loopback" {
						flagCount++
					}
				}
				if phase == "steady" {
					if flagCount != 1 || value("-metrics-endpoints") != "http://localhost:19390/metrics,http://localhost:19391/metrics,http://localhost:19392/metrics" ||
						value("-metrics-interval") != "5s" || value("-metrics-report") != out+"/runtime_steady.json" {
						t.Fatalf("owned bridge sampler arguments changed: %q", args)
					}
				} else if flagCount != 0 || value("-metrics-endpoints") != "" || value("-metrics-report") != "" {
					t.Fatalf("warmup unexpectedly enables the metrics route: %q", args)
				}
			})
		}
	}
}

func TestProtectedReceiptBenchSnapshotNeverRequestsPprof(t *testing.T) {
	outDir := t.TempDir()
	fixture := `set -euo pipefail
receipt_driver=1
REPLICA_METRICS_PORTS=(9390 9391 9392)
die() { printf 'fatal: %s\n' "$*" >&2; exit 37; }
prom_scalar() { awk -v name="$1" '$1==name {print $2;exit}' <<<"$2"; }
curl() {
 [[ "$*" == *"/debug/pprof"* ]] && { echo "protected pprof attempted" >&2; return 22; }
 printf '%s\n' "$*" >> "$OUTDIR/scrapes"
 printf '%s\n' 'go_goroutines 12' 'go_memstats_heap_inuse_bytes 100000' 'go_memstats_heap_alloc_bytes 50000' 'go_memstats_heap_objects 10' 'lantern_vertex_hlc_entries 0' 'lantern_vertex_hlc_entries_high_water 0'
 printf '%s\n' "$CONTEXT_METRICS"
}
` + receiptSnapshotShellFunctions(t) + `
snapshot_runtime "$OUTDIR/runtime.json"
`
	command := exec.Command("bash", "-c", fixture)
	command.Env = append(os.Environ(), "OUTDIR="+outDir, "CONTEXT_METRICS="+receiptRuntimeContextMetrics)
	output, err := command.CombinedOutput()
	if err != nil || strings.Contains(string(output), "protected pprof attempted") {
		t.Fatalf("protected snapshot: %v %s", err, output)
	}
	raw, err := os.ReadFile(filepath.Join(outDir, "runtime.json"))
	if err != nil {
		t.Fatal("missing natural-GC snapshot", err)
	}
	var snapshots []struct {
		Metrics map[string]float64 `json:"metrics"`
	}
	if err := json.Unmarshal(raw, &snapshots); err != nil || len(snapshots) != 3 {
		t.Fatalf("snapshots = %s, err = %v", raw, err)
	}
	for _, snapshot := range snapshots {
		if len(snapshot.Metrics) != 9 || snapshot.Metrics["go_memstats_last_gc_time_seconds"] != 1700000000.25 ||
			snapshot.Metrics["lantern_mutation_log_fill_ratio"] != 0.5 ||
			snapshot.Metrics["lantern_receipt_entries"] != 0 {
			t.Fatalf("GC/receipt/log observations lost: %+v", snapshot.Metrics)
		}
	}
	scrapes, err := os.ReadFile(filepath.Join(outDir, "scrapes"))
	if err != nil || strings.Count(string(scrapes), "/metrics\n") != 3 {
		t.Fatalf("want one existing scrape per replica: %q, err=%v", scrapes, err)
	}
}

const receiptRuntimeContextMetrics = `go_gc_duration_seconds_count 0
go_memstats_last_gc_time_seconds 1700000000.25
go_memstats_next_gc_bytes 1048576
lantern_receipt_entries 0
lantern_receipt_bytes 0
lantern_receipt_oldest_deadline_seconds 1700003600.125
lantern_mutation_log_capacity 10000
lantern_mutation_log_fill_ratio 0.5
lantern_mutation_log_evicted_total 0`

func TestReceiptBenchRuntimeContextRejectsInvalidMetrics(t *testing.T) {
	functions := receiptSnapshotShellFunctions(t)
	for _, line := range strings.Split(receiptRuntimeContextMetrics, "\n") {
		name := strings.Fields(line)[0]
		for _, kind := range []string{"missing", "duplicate", "malformed"} {
			t.Run(name+"/"+kind, func(t *testing.T) {
				metrics := receiptRuntimeContextMetrics
				switch kind {
				case "missing":
					metrics = strings.Replace(metrics, line, "", 1)
				case "duplicate":
					metrics += "\n" + line
				case "malformed":
					metrics = strings.Replace(metrics, line, line+" extra", 1)
				}
				cmd := exec.Command("bash", "-c", "set -euo pipefail\n"+functions+"\nreceipt_runtime_context \"$METRICS\"\n")
				cmd.Env = append(os.Environ(), "METRICS="+metrics)
				if output, err := cmd.CombinedOutput(); err == nil {
					t.Fatalf("invalid observation accepted: %s", output)
				}
			})
		}
	}
	for _, value := range []string{"NaN", "+Inf", "-1", "1.01", "1.0000000000000001", "1e-9999"} {
		t.Run("fill_ratio/"+value, func(t *testing.T) {
			metrics := strings.Replace(receiptRuntimeContextMetrics, "lantern_mutation_log_fill_ratio 0.5", "lantern_mutation_log_fill_ratio "+value, 1)
			cmd := exec.Command("bash", "-c", "set -euo pipefail\n"+functions+"\nreceipt_runtime_context \"$METRICS\"\n")
			cmd.Env = append(os.Environ(), "METRICS="+metrics)
			if output, err := cmd.CombinedOutput(); err == nil {
				t.Fatalf("invalid observation accepted: %s", output)
			}
		})
	}
}

func TestReceiptBenchSnapshotRejectsInvalidMetrics(t *testing.T) {
	for _, tc := range []struct {
		name    string
		wantErr string
	}{
		{name: "failed_scrape", wantErr: "metrics scrape failed for localhost:9390 (round 1)"},
		{name: "missing_heap_alloc", wantErr: "invalid go_memstats_heap_alloc_bytes for localhost:9390 (round 1)"},
		{name: "nonfinite_goroutines", wantErr: "invalid go_goroutines for localhost:9390 (round 1)"},
		{name: "negative_heap_objects", wantErr: "invalid go_memstats_heap_objects for localhost:9390 (round 1)"},
		{name: "duplicate_heap_alloc", wantErr: "invalid go_memstats_heap_alloc_bytes for localhost:9390 (round 1)"},
		{name: "fractional_heap_alloc", wantErr: "invalid go_memstats_heap_alloc_bytes for localhost:9390 (round 1)"},
		{name: "tiny_heap_alloc", wantErr: "invalid go_memstats_heap_alloc_bytes for localhost:9390 (round 1)"},
		{name: "rounded_fractional_heap_alloc", wantErr: "invalid go_memstats_heap_alloc_bytes for localhost:9390 (round 1)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outDir := t.TempDir()
			fixture := `set -euo pipefail
receipt_driver=1
REPLICA_METRICS_PORTS=(9390 9391 9392)
SNAPSHOT_ROUNDS=1
die() { printf 'fatal: %s\n' "$*" >&2; exit 37; }
curl() {
  if [[ "$*" == *"/debug/pprof/heap?gc=1"* ]]; then return 0; fi
  if [[ "$*" != *"/metrics"* ]]; then return 98; fi
  if [[ "$METRICS_CASE" == "failed_scrape" ]]; then return 22; fi
  case "$METRICS_CASE" in
    missing_heap_alloc)
      printf '%s\n' 'go_goroutines 12' 'go_memstats_heap_inuse_bytes 100000' 'go_memstats_heap_objects 10'
      ;;
    nonfinite_goroutines)
      printf '%s\n' 'go_goroutines NaN' 'go_memstats_heap_inuse_bytes 100000' 'go_memstats_heap_alloc_bytes 50000' 'go_memstats_heap_objects 10'
      ;;
    negative_heap_objects)
      printf '%s\n' 'go_goroutines 12' 'go_memstats_heap_inuse_bytes 100000' 'go_memstats_heap_alloc_bytes 50000' 'go_memstats_heap_objects -1'
      ;;
    duplicate_heap_alloc)
      printf '%s\n' 'go_goroutines 12' 'go_memstats_heap_inuse_bytes 100000' 'go_memstats_heap_alloc_bytes 50000' 'go_memstats_heap_alloc_bytes 1' 'go_memstats_heap_objects 10'
      ;;
    fractional_heap_alloc)
      printf '%s\n' 'go_goroutines 12' 'go_memstats_heap_inuse_bytes 100000' 'go_memstats_heap_alloc_bytes 1.5' 'go_memstats_heap_objects 10'
      ;;
    tiny_heap_alloc)
      printf '%s\n' 'go_goroutines 12' 'go_memstats_heap_inuse_bytes 100000' 'go_memstats_heap_alloc_bytes 1e-40' 'go_memstats_heap_objects 10'
      ;;
    rounded_fractional_heap_alloc)
      printf '%s\n' 'go_goroutines 12' 'go_memstats_heap_inuse_bytes 100000' 'go_memstats_heap_alloc_bytes 1.0000000000000001' 'go_memstats_heap_objects 10'
      ;;
  esac
}
` + receiptSnapshotShellFunctions(t) + `
snapshot_runtime "$OUTDIR/runtime.json"
`
			cmd := exec.Command("bash", "-c", fixture)
			cmd.Env = append(os.Environ(), "OUTDIR="+outDir, "METRICS_CASE="+tc.name)
			output, err := cmd.CombinedOutput()
			if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 37 ||
				!strings.Contains(string(output), tc.wantErr) {
				t.Fatalf("snapshot exit = %v, err = %v, output = %s; want %s",
					cmd.ProcessState, err, output, tc.wantErr)
			}
			if _, err := os.Stat(filepath.Join(outDir, "runtime.json")); !os.IsNotExist(err) {
				t.Fatalf("invalid-metrics snapshot artifact exists or stat failed: %v", err)
			}
		})
	}
}

func TestReceiptBenchSnapshotAcceptsIntegralScientificNotation(t *testing.T) {
	fixture := `set -euo pipefail
` + receiptSnapshotShellFunctions(t) + `
receipt_runtime_scalar go_memstats_heap_alloc_bytes 'go_memstats_heap_alloc_bytes 1.949696e+07'
`
	output, err := exec.Command("bash", "-c", fixture).CombinedOutput()
	if err != nil || string(output) != "19496960" {
		t.Fatalf("integral scientific metric = %q, err = %v; want 19496960", output, err)
	}
}

func offRuntimeSnapshotFixture(t *testing.T) string {
	t.Helper()
	return `set -euo pipefail
receipt_driver=0
unset SNAPSHOT_ROUNDS
REPLICA_METRICS_PORTS=(9390 9391 9392)
RUNTIME_METRIC_NAMES=()
if [[ "$SNAPSHOT_FAILURE" == missing-replica ]]; then REPLICA_METRICS_PORTS=(9390 9391); fi
die() { printf 'fatal: %s\n' "$*" >&2; exit 37; }
prom_scalar() { awk -v name="$1" '$1==name {print $2;exit}' <<<"$2"; }
curl() {
 local url="${@: -1}" port round=0
 if [[ "$*" == *"/debug/pprof/heap?gc=1"* ]]; then
  printf 'gc\n' >> "$OUTDIR/requests"
  [[ "$SNAPSHOT_FAILURE" != failed-gc ]] || return 22
  return
 fi
 [[ "$url" == */metrics ]] || return 98
 port="${url#http://localhost:}"; port="${port%/metrics}"
 if [[ -f "$OUTDIR/$port" ]]; then read -r round < "$OUTDIR/$port"; fi
 round=$((round + 1)); printf '%s\n' "$round" > "$OUTDIR/$port"
 printf 'metrics %s %s\n' "$port" "$round" >> "$OUTDIR/requests"
 if [[ "$port" == 9391 && "$round" == 2 && "$SNAPSHOT_FAILURE" == failed-scrape ]]; then return 22; fi
 local value hlc high
 case "$round" in 1) value=3; hlc=2; high=4;; 2) value=1; hlc=0; high=6;; *) value=2; hlc=1; high=5;; esac
 local name sample
 for name in go_goroutines go_memstats_heap_inuse_bytes go_memstats_heap_alloc_bytes go_memstats_heap_objects lantern_vertex_hlc_entries lantern_vertex_hlc_entries_high_water; do
  case "$name" in
   go_goroutines) sample="${value}0";;
   go_memstats_heap_inuse_bytes) sample="${value}e+06";;
   go_memstats_heap_alloc_bytes) sample="${value}e+04";;
   go_memstats_heap_objects) sample="${value}00";;
   lantern_vertex_hlc_entries) sample="$hlc";;
   lantern_vertex_hlc_entries_high_water) sample="$high";;
  esac
  if [[ "$port" == 9391 && "$round" == 2 && "$name" == "$INVALID_METRIC" ]]; then
   case "$SNAPSHOT_FAILURE" in
    missing) continue;;
    duplicate) printf '%s %s\n' "$name" "$sample";;
    malformed) sample="$sample extra";;
    *) sample="$REPLACEMENT";;
   esac
  fi
  printf '%s %s\n' "$name" "$sample"
 done
}
` + receiptSnapshotShellFunctions(t) + `
snapshot_runtime "$OUTDIR/runtime.json"
`
}

func TestOFFBenchSnapshotKeepsThreeRoundMinimaAndZeroHLC(t *testing.T) {
	out := t.TempDir()
	cmd := exec.CommandContext(t.Context(), "bash", "-c", offRuntimeSnapshotFixture(t))
	cmd.Env = append(os.Environ(), "OUTDIR="+out, "SNAPSHOT_FAILURE=none", "INVALID_METRIC=", "REPLACEMENT=")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("valid OFF snapshot: %v %s", err, output)
	}
	requests, err := os.ReadFile(filepath.Join(out, "requests"))
	if err != nil || strings.Count(string(requests), "gc\n") != 9 || strings.Count(string(requests), "metrics ") != 9 {
		t.Fatalf("want one forced GC and scrape in every replica/round: %q %v", requests, err)
	}
	raw, err := os.ReadFile(filepath.Join(out, "runtime.json"))
	if err != nil {
		t.Fatal(err)
	}
	var snapshots []map[string]any
	if err := json.Unmarshal(raw, &snapshots); err != nil || len(snapshots) != 3 {
		t.Fatalf("snapshots=%s err=%v", raw, err)
	}
	for _, snapshot := range snapshots {
		for key, expected := range map[string]float64{"goroutines": 10, "heap_inuse_bytes": 1000000, "heap_alloc_bytes": 10000, "heap_objects": 100, "vertex_hlc_entries": 0, "vertex_hlc_high_water": 6} {
			if snapshot[key] != expected {
				t.Fatalf("%s=%v, want %v: %s", key, snapshot[key], expected, raw)
			}
		}
	}
}

func TestOFFBenchSnapshotRejectsInvalidReplicaRound(t *testing.T) {
	fixture := offRuntimeSnapshotFixture(t)
	for _, metric := range []string{"go_goroutines", "go_memstats_heap_inuse_bytes", "go_memstats_heap_alloc_bytes", "go_memstats_heap_objects", "lantern_vertex_hlc_entries", "lantern_vertex_hlc_entries_high_water"} {
		failures := []string{"missing", "duplicate", "malformed", "NaN", "+Inf", "-Inf", "-1", "1.5", "1e-40", "1.0000000000000001", "1e1000"}
		if strings.HasPrefix(metric, "go_") {
			failures = append(failures, "0")
		}
		for _, failure := range failures {
			t.Run(metric+"/"+failure, func(t *testing.T) {
				out := t.TempDir()
				cmd := exec.CommandContext(t.Context(), "bash", "-c", fixture)
				cmd.Env = append(os.Environ(), "OUTDIR="+out, "SNAPSHOT_FAILURE="+failure, "INVALID_METRIC="+metric, "REPLACEMENT="+failure)
				output, err := cmd.CombinedOutput()
				want := "invalid " + metric + " for localhost:9391 (round 2)"
				if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 37 || !strings.Contains(string(output), want) {
					t.Fatalf("invalid metric exit=%v err=%v output=%s, want %s", cmd.ProcessState, err, output, want)
				}
				if _, err := os.Stat(filepath.Join(out, "runtime.json")); !os.IsNotExist(err) {
					t.Fatalf("invalid snapshot artifact exists or stat failed: %v", err)
				}
			})
		}
	}
	for _, tc := range []struct{ failure, message string }{
		{"failed-scrape", "metrics scrape failed for localhost:9391 (round 2)"},
		{"failed-gc", "forced GC failed for localhost:9390 (round 1)"},
		{"missing-replica", "expected three replica metrics ports"},
	} {
		t.Run(tc.failure, func(t *testing.T) {
			out := t.TempDir()
			cmd := exec.CommandContext(t.Context(), "bash", "-c", fixture)
			cmd.Env = append(os.Environ(), "OUTDIR="+out, "SNAPSHOT_FAILURE="+tc.failure, "INVALID_METRIC=", "REPLACEMENT=")
			output, err := cmd.CombinedOutput()
			if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 37 || !strings.Contains(string(output), tc.message) {
				t.Fatalf("failed round exit=%v err=%v output=%s", cmd.ProcessState, err, output)
			}
			if _, err := os.Stat(filepath.Join(out, "runtime.json")); !os.IsNotExist(err) {
				t.Fatalf("failed snapshot artifact exists or stat failed: %v", err)
			}
		})
	}
}

// TestBroadIlluminateScenarioTopologyContract is the #994 semantic guard that
// complements TestScenarioTemplates_MatchWireSchema. A proto-valid Illuminate
// request can still run over an empty or one-edge graph, so pin both the
// self-contained topology preflight and each family producer's real workload.
func TestBroadIlluminateScenarioTopologyContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("scenarios", "broad_illuminate.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Preflight struct {
			Kind string `yaml:"kind"`
		} `yaml:"preflight"`
		Target struct {
			Calls []scenarioCall `yaml:"calls"`
		} `yaml:"target"`
		PerfGate struct {
			Producers map[string]map[string]float64 `yaml:"producers"`
		} `yaml:"perf_gate"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse broad_illuminate: %v", err)
	}
	if doc.Preflight.Kind != "broad_illuminate" {
		t.Fatalf("preflight.kind = %q, want broad_illuminate", doc.Preflight.Kind)
	}
	fixture := topology.NewBroadIlluminateFixture(time.Now().Add(time.Hour))
	if len(fixture.Vertices) < 1+topology.BroadIlluminateDepth*topology.BroadIlluminateFanOut+2*topology.BroadIlluminateCommunitySize {
		t.Fatalf("fixture vertices = %d, topology unexpectedly collapsed", len(fixture.Vertices))
	}

	calls := make(map[string]string, len(doc.Target.Calls))
	for _, call := range doc.Target.Calls {
		if call.Name == "" {
			t.Fatal("broad_illuminate has an unnamed producer")
		}
		calls[call.Name] = strings.TrimSpace(call.DataTemplate)
		if _, ok := doc.PerfGate.Producers[call.Name]; !ok {
			t.Errorf("producer %q has no independent perf gate", call.Name)
		}
	}
	for name, want := range map[string][]string{
		"bfs_depth3_fanout64":        {`"seed":"bench:walk:root"`, `"step":3`, `"fan_out":64`},
		"ppr_tuned":                  {`"top_n":32`, `"restart_prob":0.2`, `"epsilon":0.000001`},
		"community_arborescence_min": {`"seed":"bench:community:alpha:00"`, `"max_size":32`, `"reduction":1`, `"objective":1`, `"restart_prob":0.2`, `"epsilon":0.000001`},
	} {
		template, ok := calls[name]
		if !ok {
			t.Errorf("missing %s producer", name)
			continue
		}
		for _, fragment := range want {
			if !strings.Contains(template, fragment) {
				t.Errorf("%s template missing %q: %s", name, fragment, template)
			}
		}
	}
}

// TestBroadRWScenarioSeparatesSearchRecovery keeps its codes.OK RPC-surface
// contract independent from the derived-index rebuild lifecycle. Hosted
// mutation-log gap recovery may rebuild Search; search_churn owns that proof.
func TestBroadRWScenarioSeparatesSearchRecovery(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("scenarios", "broad_rw.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cluster struct {
			SearchEnabled *bool `yaml:"search_enabled"`
		} `yaml:"cluster"`
		Target struct {
			Calls []scenarioCall `yaml:"calls"`
		} `yaml:"target"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse broad_rw: %v", err)
	}
	if doc.Cluster.SearchEnabled == nil || *doc.Cluster.SearchEnabled {
		t.Fatal("broad_rw must disable Search so snapshot rebuilds cannot reject its codes.OK Put producers")
	}
	methods := make(map[string]bool, len(doc.Target.Calls))
	for _, call := range doc.Target.Calls {
		methods[call.Call] = true
	}
	for _, method := range []string{
		"graph.v1.LanternService/PutVertex",
		"graph.v1.LanternService/PutVertices",
	} {
		if !methods[method] {
			t.Errorf("broad_rw lost required write producer %s", method)
		}
	}
}

// TestSearchChurnScenarioGateContract keeps #1063's blocking search proof
// honest: every advanced producer is independently ratcheted, semantic probes
// run on every replica, and the derived-index gauges have real pre/post gates.
func TestSearchChurnScenarioGateContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("scenarios", "search_churn.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		SemanticGate struct {
			Kind string `yaml:"kind"`
		} `yaml:"semantic_gate"`
		Target struct {
			Endpoints []string `yaml:"endpoints"`
			Calls     []struct {
				Name         string `yaml:"name"`
				Call         string `yaml:"call"`
				DataTemplate string `yaml:"data_template"`
				RPS          int    `yaml:"rps"`
			} `yaml:"calls"`
		} `yaml:"target"`
		Phases struct {
			Steady struct {
				Concurrency int `yaml:"concurrency"`
				RPS         int `yaml:"rps"`
			} `yaml:"steady"`
		} `yaml:"phases"`
		MetricGate struct {
			Metrics map[string]map[string]float64 `yaml:"metrics"`
		} `yaml:"metric_gate"`
		PerfGate struct {
			MaxNonOK  *float64 `yaml:"max_non_ok_ratio"`
			Producers map[string]struct {
				MinSteadyRPS *float64 `yaml:"min_steady_rps"`
				MaxP99MS     *float64 `yaml:"max_p99_ms"`
				MaxNonOK     *float64 `yaml:"max_non_ok_ratio"`
			} `yaml:"producers"`
		} `yaml:"perf_gate"`
		LifecycleGate struct {
			Reason    string   `yaml:"reason"`
			MaxRatio  *float64 `yaml:"max_ratio"`
			Producers map[string]struct {
				MetricLabels map[string]string `yaml:"metric_labels"`
			} `yaml:"producers"`
		} `yaml:"lifecycle_gate"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse search_churn: %v", err)
	}
	if doc.SemanticGate.Kind != "search" {
		t.Fatalf("semantic_gate.kind = %q, want search", doc.SemanticGate.Kind)
	}
	for _, metric := range []string{
		"lantern_search_index_docs",
		"lantern_search_index_terms",
		"lantern_search_index_physical_documents",
		"lantern_search_index_expired_documents",
		"lantern_search_index_expiration_queue_entries",
		"lantern_search_index_expiration_purged",
		"lantern_search_index_retained_term_slots",
		"lantern_search_index_retained_ordinals",
		"lantern_search_index_postings",
		"lantern_search_index_position_entries",
		"lantern_search_index_estimated_retained_bytes",
		"lantern_search_index_healthy",
	} {
		if len(doc.MetricGate.Metrics[metric]) == 0 {
			t.Errorf("metric %s has no threshold", metric)
		}
	}
	if _, ok := doc.MetricGate.Metrics["lantern_search_index_retained_ratio"]; ok {
		t.Error("retained ratio must not be gated while the live denominator decays")
	}
	for metric, wantMaxPost := range map[string]float64{
		"lantern_search_index_estimated_retained_bytes": 262144,
		"lantern_search_index_retained_ordinals":        4096,
		"lantern_search_index_retained_term_slots":      8192,
	} {
		thresholds := doc.MetricGate.Metrics[metric]
		if got := thresholds["max_post"]; got != wantMaxPost {
			t.Errorf("metric %s max_post = %v, want %v", metric, got, wantMaxPost)
		}
		if _, ok := thresholds["max_ratio"]; ok {
			t.Errorf("metric %s must not use a GC-phase-sensitive pre/post ratio", metric)
		}
	}

	calls := make(map[string]string, len(doc.Target.Calls))
	searchProducersByEndpoint := make(map[string]int, len(doc.Target.Endpoints))
	totalRPS := 0
	for i, call := range doc.Target.Calls {
		if call.Name == "" || call.RPS <= 0 {
			t.Errorf("producer has invalid name/rps: %+v", call)
			continue
		}
		totalRPS += call.RPS
		calls[call.Name] = strings.TrimSpace(call.DataTemplate)
		if strings.HasSuffix(call.Call, "/SearchVertices") && len(doc.Target.Endpoints) > 0 {
			endpoint := doc.Target.Endpoints[i%len(doc.Target.Endpoints)]
			searchProducersByEndpoint[endpoint]++
		}
		gate, ok := doc.PerfGate.Producers[call.Name]
		if !ok || gate.MinSteadyRPS == nil || gate.MaxP99MS == nil || gate.MaxNonOK == nil {
			t.Errorf("producer %q lacks independent rps+p99/non-OK gate", call.Name)
		}
	}
	if totalRPS != doc.Phases.Steady.RPS {
		t.Errorf("producer rps sum = %d, steady rps = %d", totalRPS, doc.Phases.Steady.RPS)
	}
	if len(doc.Target.Endpoints) == 0 {
		t.Fatal("search churn has no target endpoints")
	}
	if doc.Phases.Steady.Concurrency <= 0 {
		t.Fatalf("steady concurrency = %d, want positive", doc.Phases.Steady.Concurrency)
	}
	const defaultSearchMaxInFlight = 32
	for _, endpoint := range doc.Target.Endpoints {
		producers := searchProducersByEndpoint[endpoint]
		inFlight := producers * doc.Phases.Steady.Concurrency
		if producers == 0 {
			t.Errorf("endpoint %s has no Search producer", endpoint)
		} else if inFlight >= defaultSearchMaxInFlight {
			t.Errorf("endpoint %s Search demand = %d producers * %d concurrency = %d, want below admission limit %d", endpoint, producers, doc.Phases.Steady.Concurrency, inFlight, defaultSearchMaxInFlight)
		}
	}
	for name, fragments := range map[string][]string{
		"writer":                    {`"expiration"`},
		"broad_posting":             {`"query":"shared"`},
		"prefix_scoped":             {`"prefix":"search-000"`},
		"fuzzy_1":                   {`"fuzziness":1`},
		"fuzzy_2":                   {`"fuzziness":2`},
		"prefix_terms":              {`"prefixTerms":true`},
		"prefix_fuzzy_cap_overflow": {`"prefixTerms":true`, `"fuzziness":2`},
		"match_all":                 {`"matchMode":2`},
		"min_should":                {`"matchMode":3`, `"minShouldMatch":2`},
		"broad_phrase":              {`"phrase":true`},
	} {
		template, ok := calls[name]
		if !ok {
			t.Errorf("missing advanced producer %q", name)
			continue
		}
		for _, fragment := range fragments {
			if !strings.Contains(template, fragment) {
				t.Errorf("%s template missing %q: %s", name, fragment, template)
			}
		}
	}
	if doc.PerfGate.MaxNonOK == nil || *doc.PerfGate.MaxNonOK != 0.02 {
		t.Errorf("generic unexpected non-OK ceiling = %v, want unchanged 0.02", doc.PerfGate.MaxNonOK)
	}
	if doc.LifecycleGate.Reason != "SEARCH_INDEX_INCOMPLETE" || doc.LifecycleGate.MaxRatio == nil || *doc.LifecycleGate.MaxRatio != 0.10 {
		t.Errorf("typed lifecycle gate = reason %q max %v, want SEARCH_INDEX_INCOMPLETE/0.10", doc.LifecycleGate.Reason, doc.LifecycleGate.MaxRatio)
	}
	expectedLifecycleLabels := map[string]map[string]string{
		"broad_posting":             {"mode": "server", "phrase": "no", "fuzziness": "0", "prefix_terms": "no", "prefix_present": "no"},
		"prefix_scoped":             {"mode": "server", "phrase": "no", "fuzziness": "0", "prefix_terms": "no", "prefix_present": "yes"},
		"fuzzy_1":                   {"mode": "server", "phrase": "no", "fuzziness": "1", "prefix_terms": "no", "prefix_present": "no"},
		"fuzzy_2":                   {"mode": "server", "phrase": "no", "fuzziness": "2", "prefix_terms": "no", "prefix_present": "no"},
		"prefix_terms":              {"mode": "server", "phrase": "no", "fuzziness": "0", "prefix_terms": "yes", "prefix_present": "no"},
		"prefix_fuzzy_cap_overflow": {"mode": "server", "phrase": "no", "fuzziness": "2", "prefix_terms": "yes", "prefix_present": "no"},
		"match_all":                 {"mode": "all", "phrase": "no", "fuzziness": "0", "prefix_terms": "no", "prefix_present": "no"},
		"min_should":                {"mode": "min_should", "phrase": "no", "fuzziness": "0", "prefix_terms": "no", "prefix_present": "no"},
		"broad_phrase":              {"mode": "server", "phrase": "yes", "fuzziness": "0", "prefix_terms": "no", "prefix_present": "no"},
	}
	if _, ok := doc.LifecycleGate.Producers["writer"]; ok {
		t.Error("writer must remain in the generic non-OK budget; only Search reason counters are classified")
	}
	seenSelectors := map[string]string{}
	for name, expected := range expectedLifecycleLabels {
		selector, ok := doc.LifecycleGate.Producers[name]
		if !ok {
			t.Errorf("Search producer %q lacks typed lifecycle selector", name)
			continue
		}
		if len(selector.MetricLabels) != len(expected) {
			t.Errorf("producer %q labels = %v, want %v", name, selector.MetricLabels, expected)
		}
		var key strings.Builder
		for _, label := range []string{"mode", "phrase", "fuzziness", "prefix_terms", "prefix_present"} {
			got := selector.MetricLabels[label]
			if got != expected[label] {
				t.Errorf("producer %q label %s = %q, want %q", name, label, got, expected[label])
			}
			fmt.Fprintf(&key, "%s=%s;", label, got)
		}
		if previous := seenSelectors[key.String()]; previous != "" {
			t.Errorf("lifecycle selectors collide: %s and %s", previous, name)
		}
		seenSelectors[key.String()] = name
	}
	if len(doc.LifecycleGate.Producers) != len(expectedLifecycleLabels) {
		t.Errorf("lifecycle producers = %d, want %d exact Search producers", len(doc.LifecycleGate.Producers), len(expectedLifecycleLabels))
	}
	runScript, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(runScript), `go run ./testbed/bench/perfgate "${perf_args[@]}"`) {
		t.Error("run.sh does not delegate typed/unexpected classification to the tested perf evaluator")
	}
	run := string(runScript)
	if strings.Contains(run, `wait "${prod_pids[@]}"`) {
		t.Error("run.sh waits for producers as one aggregate and can hide an earlier failure")
	}
	for _, fragment := range []string{
		`for i in "${!prod_pids[@]}"; do`,
		`if wait "${prod_pids[$i]}"; then`,
		`producer_failed=1`,
		`perf_args+=( -producer-failed )`,
		`"$workload_verdict" == "pass"`,
	} {
		if !strings.Contains(run, fragment) {
			t.Errorf("steady producer failure path missing %q", fragment)
		}
	}
	for _, fragment := range []string{
		`if ! run_search_probe verify pre; then`,
		`if ! run_search_probe verify post; then`,
		`semantic_verdict" != "fail"`,
	} {
		if !strings.Contains(string(runScript), fragment) {
			t.Errorf("semantic gate failure path missing %q", fragment)
		}
	}
}

// TestSearchQualificationScenarioGateContract keeps the short-TTL release
// qualification from treating the diagnostic retained/live ratio as a leak.
// Production intentionally waits for the compaction floor before rebuilding
// retained index structures, so the ratio can rise as live documents expire.
func TestSearchQualificationScenarioGateContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("scenarios", "search_qualification.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Phases struct {
			Cooldown string `yaml:"cooldown"`
		} `yaml:"phases"`
		MetricGate struct {
			Metrics map[string]map[string]float64 `yaml:"metrics"`
		} `yaml:"metric_gate"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse search_qualification: %v", err)
	}
	cooldown, err := time.ParseDuration(doc.Phases.Cooldown)
	if err != nil {
		t.Fatalf("parse search_qualification cooldown: %v", err)
	}
	if cooldown < time.Minute {
		t.Errorf("search qualification cooldown = %s, want at least 1m for TTL index quiescence", cooldown)
	}
	for _, metric := range []string{
		"lantern_search_index_retained_term_slots",
		"lantern_search_index_retained_ordinals",
		"lantern_search_index_estimated_retained_bytes",
	} {
		if len(doc.MetricGate.Metrics[metric]) == 0 {
			t.Errorf("metric %s has no threshold", metric)
		}
	}
	if _, ok := doc.MetricGate.Metrics["lantern_search_index_retained_ratio"]; ok {
		t.Error("retained ratio must not be gated while the live denominator decays")
	}
}

// TestSearchReleaseQualificationIsBlocking pins the release dependency rather
// than merely checking that a qualification job exists. A failed or skipped
// stage must produce a fail verdict, and image publication must need that job.
func TestSearchReleaseQualificationIsBlocking(t *testing.T) {
	releaseWorkflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "docker-publish.yml"))
	if err != nil {
		t.Fatal(err)
	}
	release := string(releaseWorkflow)
	var workflow struct {
		Jobs map[string]any `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(releaseWorkflow, &workflow); err != nil {
		t.Fatalf("parse root release workflow: %v", err)
	}
	if _, ok := workflow.Jobs["bench"]; ok {
		t.Error("root tags must not schedule a long hosted benchmark")
	}
	for _, job := range []string{"verify", "search-qualification", "build", "binaries", "release"} {
		if _, ok := workflow.Jobs[job]; !ok {
			t.Errorf("root release workflow lost job %q", job)
		}
	}
	for _, contract := range []string{
		"search-qualification:",
		"go test ./server/service -run",
		"go test ./tests/integration -run",
		"./testbed/bench/run.sh search_qualification",
		`all($stages[]; .status == "pass")`,
		`scenarios:$scenarios`,
		"needs: [verify, search-qualification]",
		`test "$(jq -r .verdict qualification-report.json)" = pass`,
	} {
		if !strings.Contains(release, contract) {
			t.Errorf("release workflow missing blocking contract %q", contract)
		}
	}
	for _, forbidden := range []string{
		"  bench:",
		"Run release bench sweep",
		"Download bench report",
		"bench/bench-report.md",
	} {
		if strings.Contains(release, forbidden) {
			t.Errorf("tag workflow must not use hosted full-sweep evidence %q", forbidden)
		}
	}
	for _, contract := range []string{
		"## Performance evidence",
		"testbed/bench/README.md#local-pre-tag-qualification",
		"one chosen local environment (ARM or x86)",
	} {
		if !strings.Contains(release, contract) {
			t.Errorf("release notes missing local-evidence contract %q", contract)
		}
	}

	nightlyWorkflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "bench-nightly.yml"))
	if err != nil {
		t.Fatal(err)
	}
	nightly := string(nightlyWorkflow)
	for _, contract := range []string{
		"Run deterministic advanced Search wire contracts",
		"TestSearchVertices_(OptionsContract|PositionsOff)",
		"RELEASE_BENCH_BUDGET_SECONDS: \"0\"",
	} {
		if !strings.Contains(nightly, contract) {
			t.Errorf("nightly workflow missing Search contract %q", contract)
		}
	}
}

// TestReleaseSweepIsolatesScenarioClusters keeps release measurements from
// inheriting data, high-water state, or ignored cluster overrides from a prior
// scenario. run.sh must own the complete Compose lifecycle for every entry.
func TestReleaseSweepIsolatesScenarioClusters(t *testing.T) {
	releaseScript, err := os.ReadFile("release.sh")
	if err != nil {
		t.Fatal(err)
	}
	release := string(releaseScript)
	if !strings.Contains(release, `if "$HERE/run.sh" "$s"; then`) {
		t.Error("release sweep does not invoke the scenario-owned run.sh lifecycle")
	}
	for _, sharedClusterContract := range []string{"SKIP_UP=1", "KEEP_UP=1", `docker compose "${COMPOSE_FILES[@]}" up`} {
		if strings.Contains(release, sharedClusterContract) {
			t.Errorf("release sweep still contains shared-cluster contract %q", sharedClusterContract)
		}
	}

	runScript, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatal(err)
	}
	run := string(runScript)
	clusterOverride := strings.Index(run, `cluster_ttl="$(yq -r '.cluster.default_ttl_seconds`)
	composeUp := strings.Index(run, `docker compose "${COMPOSE_FILES[@]}" up -d --wait`)
	composeDown := strings.Index(run, `down_owned_compose "$COMPOSE_PROJECT_NAME" "${COMPOSE_FILES[@]}"`)
	if clusterOverride < 0 || composeUp < 0 || clusterOverride > composeUp {
		t.Error("run.sh must apply scenario cluster overrides before compose up")
	}
	if composeDown < 0 {
		t.Error("run.sh must remove scenario volumes during cleanup")
	}
	if !strings.Contains(run, `if [[ -n "${LANTERN_BENCH_CPUSET:-}" ]]; then`) ||
		!strings.Contains(run, `COMPOSE_FILES+=(-f "$HERE/compose.cpuset.yml")`) {
		t.Error("local CPU pin must be opt-in and use the same Compose lifecycle")
	}

	overlay, err := os.ReadFile("compose.cpuset.yml")
	if err != nil {
		t.Fatal(err)
	}
	var cpuset struct {
		Services map[string]struct {
			CPUSet string `yaml:"cpuset"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(overlay, &cpuset); err != nil {
		t.Fatal(err)
	}
	if len(cpuset.Services) != 3 {
		t.Errorf("local CPU overlay has %d replicas, want 3", len(cpuset.Services))
	}
	for _, replica := range []string{"lantern-0", "lantern-1", "lantern-2"} {
		if cpuset.Services[replica].CPUSet != "${LANTERN_BENCH_CPUSET:?set LANTERN_BENCH_CPUSET for local qualification}" {
			t.Errorf("replica %s does not use the shared required local CPU set", replica)
		}
	}

	procedure, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(procedure), `docker run --rm --entrypoint awk "$LANTERN_IMAGE"`) {
		t.Error("Docker VM load preflight must override the server ENTRYPOINT")
	}
}

func TestLocalPreTagSummaryGate(t *testing.T) {
	procedure, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(procedure), "One complete passing run on a chosen suitable") {
		t.Error("local qualification must require a complete pass on one chosen environment")
	}
	_, after, found := strings.Cut(string(procedure), "awk -F '|' '\n")
	if !found {
		t.Fatal("local qualification is missing its Summary gate")
	}
	program, _, found := strings.Cut(after, "\n' \"$EVIDENCE_DIR/bench-report.md\"")
	if !found {
		t.Fatal("local qualification Summary gate is incomplete")
	}

	for _, tc := range []struct {
		name       string
		rows       int
		leak, perf string
		wantPass   bool
	}{
		{name: "complete with producer details", rows: 8, leak: "pass", perf: "pass", wantPass: true},
		{name: "partial sweep", rows: 7, leak: "pass", perf: "pass"},
		{name: "extra scenario", rows: 9, leak: "pass", perf: "pass"},
		{name: "failed leak gate", rows: 8, leak: "fail", perf: "pass"},
		{name: "failed perf gate", rows: 8, leak: "pass", perf: "fail"},
		{name: "missing summary", leak: "pass", perf: "pass"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var report strings.Builder
			if tc.rows > 0 {
				report.WriteString("## Summary\n\n| scenario | leak gate | metric gate | semantic gate | perf gate |\n")
				for i := range tc.rows {
					fmt.Fprintf(&report, "| `scenario-%d` | `%s` | `-` | `-` | `%s` |\n", i, tc.leak, tc.perf)
				}
			}
			report.WriteString("\n## broad_rw\n| `producer-0` | `pass` | 1000.0 | 10.0 |\n")
			path := filepath.Join(t.TempDir(), "bench-report.md")
			if err := os.WriteFile(path, []byte(report.String()), 0o600); err != nil {
				t.Fatal(err)
			}
			output, err := exec.Command("awk", "-F", "|", program, path).CombinedOutput()
			if (err == nil) != tc.wantPass {
				t.Fatalf("Summary gate error = %v, output = %q, want pass = %t", err, output, tc.wantPass)
			}
		})
	}
}

func TestManualQualificationRetainsProducerEvidence(t *testing.T) {
	workflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "bench-nightly.yml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, contract := range []string{
		`KEEP_OUT: "1"`,
		"testbed/bench/out/broad_rw/",
		"testbed/bench/out/broad_mutate/",
	} {
		if !strings.Contains(string(workflow), contract) {
			t.Errorf("manual qualification does not retain failed producer evidence %q", contract)
		}
	}
	releaseScript, err := os.ReadFile("release.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(releaseScript), `if [[ "${KEEP_OUT:-0}" != "1" ]]; then`) {
		t.Error("release sweep no longer honors KEEP_OUT for scenario evidence")
	}
}

func TestManualQualificationRetainsFanOutRepairDiagnostics(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "bench-nightly.yml"))
	if err != nil {
		t.Fatal(err)
	}
	type diagnosticStep struct {
		Name string            `yaml:"name"`
		If   string            `yaml:"if"`
		Env  map[string]string `yaml:"env"`
		Run  string            `yaml:"run"`
	}
	var workflow struct {
		Jobs struct {
			LeakGate struct {
				Steps []diagnosticStep `yaml:"steps"`
			} `yaml:"leak-gate"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(raw, &workflow); err != nil {
		t.Fatal(err)
	}
	steps := make(map[string]diagnosticStep)
	for _, step := range workflow.Jobs.LeakGate.Steps {
		steps[step.Name] = step
	}
	for _, name := range []string{
		"Summarize diagnostic replication health",
		"Summarize diagnostic peer repairs",
		"Stop diagnostic cluster",
	} {
		step, ok := steps[name]
		if !ok {
			t.Errorf("missing diagnostic step %q", name)
			continue
		}
		for _, clause := range []string{"always()", "broad_rw", "broad_mutate", "steps.diagnostic.outcome"} {
			if !strings.Contains(step.If, clause) {
				t.Errorf("%s condition missing %q", name, clause)
			}
		}
	}
	keepUp := steps["Run one diagnostic scenario"].Env["KEEP_UP"]
	if !strings.Contains(keepUp, "broad_rw") || !strings.Contains(keepUp, "broad_mutate") {
		t.Errorf("diagnostic KEEP_UP does not retain both fan-out clusters: %q", keepUp)
	}
	if _, ok := steps["Run manual leak-gate sweep (blocking)"].Env["LANTERN_MUTATION_LOG_SUBSCRIBER_BUFFER"]; ok {
		t.Error("full qualification must not receive the diagnostic subscriber buffer override")
	}
	diagnostic := steps["Run one diagnostic scenario"]
	if got := diagnostic.Env["LANTERN_MUTATION_LOG_SUBSCRIBER_BUFFER"]; got != "${{ inputs.diagnostic_subscriber_buffer }}" {
		t.Errorf("diagnostic subscriber buffer input = %q", got)
	}
	validate := steps["Validate diagnostic request"]
	if validate.Env["DIAGNOSTIC_SUBSCRIBER_BUFFER"] != "${{ inputs.diagnostic_subscriber_buffer }}" ||
		!strings.Contains(validate.Run, "diagnostic_subscriber_buffer must be an integer from 1 to 100000") ||
		!strings.Contains(validate.Run, "diagnostic overrides require diagnostic_scenario") ||
		!strings.Contains(string(raw), "diagnostic_subscriber_buffer:") {
		t.Error("diagnostic subscriber buffer must be explicitly requested and validated")
	}
	for _, tt := range []struct {
		name, scenario, logCapacity, buffer, wantError string
	}{
		{name: "canonical sweep"},
		{name: "diagnostic", scenario: "broad_mutate", buffer: "8192"},
		{name: "zero buffer", scenario: "broad_mutate", buffer: "0", wantError: "must be an integer"},
		{name: "non-numeric buffer", scenario: "broad_mutate", buffer: "8x", wantError: "must be an integer"},
		{name: "oversized buffer", scenario: "broad_mutate", buffer: "100001", wantError: "must be an integer"},
		{name: "full sweep buffer override", buffer: "8192", wantError: "require diagnostic_scenario"},
		{name: "full sweep capacity override", logCapacity: "100000", wantError: "require diagnostic_scenario"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cmd := exec.Command("bash", "-c", validate.Run)
			cmd.Env = append(os.Environ(),
				"DIAGNOSTIC_SCENARIO="+tt.scenario,
				"DIAGNOSTIC_LOG_CAPACITY="+tt.logCapacity,
				"DIAGNOSTIC_SUBSCRIBER_BUFFER="+tt.buffer)
			output, err := cmd.CombinedOutput()
			if tt.wantError == "" {
				if err != nil {
					t.Fatalf("valid diagnostic request failed: %v: %s", err, output)
				}
			} else if err == nil || !strings.Contains(string(output), tt.wantError) {
				t.Fatalf("invalid diagnostic request = %v: %s; want %q", err, output, tt.wantError)
			}
		})
	}
	compose, err := os.ReadFile("compose.override.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(compose), `  LANTERN_MUTATION_LOG_SUBSCRIBER_BUFFER: "${LANTERN_MUTATION_LOG_SUBSCRIBER_BUFFER:-}"`) ||
		!strings.Contains(string(compose), `LANTERN_MUTATION_LOG_CAPACITY: "${LANTERN_BENCH_MUTATION_LOG_CAPACITY:-10000}"`) {
		t.Error("the canonical sweep must keep the server subscriber-buffer default and 10k mutation log")
	}
	peerLogs := steps["Summarize diagnostic peer repairs"].Run
	for _, clause := range []string{"first_count", "matches <= 80", "matches - 159", "peer_repair_events", "replication pump: peer transition"} {
		if !strings.Contains(peerLogs, clause) {
			t.Errorf("peer repair summary missing %q", clause)
		}
	}
}

func TestTTLChurnScenarioCausalBudgetContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("scenarios", "ttl_churn.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cluster struct {
			MaxVertexCausalEntries int `yaml:"max_vertex_causal_entries"`
		} `yaml:"cluster"`
		Target struct {
			Calls []scenarioCall `yaml:"calls"`
		} `yaml:"target"`
		MetricGate struct {
			Metrics map[string]map[string]float64 `yaml:"metrics"`
		} `yaml:"metric_gate"`
	}
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Cluster.MaxVertexCausalEntries != 4096 {
		t.Fatalf("causal limit = %d, want 4096", doc.Cluster.MaxVertexCausalEntries)
	}
	calls := map[string]string{}
	for _, call := range doc.Target.Calls {
		calls[call.Name] = call.DataTemplate
	}
	for _, name := range []string{"born_expired_put", "delete"} {
		if !strings.Contains(calls[name], "mod .RequestNumber 4096") {
			t.Errorf("%s does not reuse the bounded causal identity ring", name)
		}
	}
	for metric, maxPost := range map[string]float64{
		"lantern_vertex_causal_metadata_entries":            4096,
		"lantern_vertex_causal_metadata_entries_high_water": 4096,
		"lantern_vertex_causal_metadata_over_limit":         0,
	} {
		gate, ok := doc.MetricGate.Metrics[metric]
		if !ok || gate["max_post"] != maxPost {
			t.Errorf("%s max_post = %v, want %v", metric, gate["max_post"], maxPost)
		}
	}
	entries := doc.MetricGate.Metrics["lantern_vertex_causal_metadata_entries"]
	if entries["min_post"] != 4096 {
		t.Errorf("causal entries min_post = %v, want 4096", entries["min_post"])
	}
	estimated := doc.MetricGate.Metrics["lantern_vertex_causal_metadata_estimated_bytes"]
	if estimated["max_post"] != 1<<20 {
		t.Errorf("causal estimated bytes max_post = %v, want 1 MiB", estimated["max_post"])
	}

	run, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(run), "LANTERN_BENCH_MAX_VERTEX_CAUSAL_ENTRIES") {
		t.Fatal("run.sh does not forward the scenario causal budget")
	}
}

// calls flattens every (call, data_template) pair in the document, tagged
// with where it came from so failures point at the offending YAML path.
func (d scenarioDoc) calls() map[string]scenarioCall {
	out := map[string]scenarioCall{}
	if d.Target.Call != "" {
		out["target"] = scenarioCall{Call: d.Target.Call, DataTemplate: d.Target.DataTemplate}
	}
	for i, c := range d.Target.Calls {
		out[fmt.Sprintf("target.calls[%d]", i)] = c
	}
	if d.Subscribe.Call != "" {
		out["subscribe"] = scenarioCall{Call: d.Subscribe.Call, DataTemplate: d.Subscribe.DataTemplate}
	}
	for i, c := range d.Subscribe.Consumers {
		out[fmt.Sprintf("subscribe.consumers[%d]", i)] = c
	}
	return out
}

// ghzTemplateData mirrors the call-scoped variables ghz injects into
// data_template. Only the fields the scenario corpus actually uses are
// modelled; extend when a scenario starts using more of ghz's surface.
type ghzTemplateData struct {
	RequestNumber int64
	Timestamp     string
}

// ghzFuncs approximates the sprig subset the corpus uses (ghz embeds sprig).
// Numeric args are `any` because sprig coerces (cast.ToInt64) — templates mix
// int64 fields with literal ints.
func ghzFuncs() template.FuncMap {
	toInt64 := func(v any) int64 {
		switch n := v.(type) {
		case int:
			return int64(n)
		case int64:
			return n
		default:
			panic(fmt.Sprintf("unsupported integer type %T", v))
		}
	}
	return template.FuncMap{
		"mod": func(a, b any) int64 { return toInt64(a) % toInt64(b) },
		"add": func(vs ...any) int64 {
			var sum int64
			for _, v := range vs {
				sum += toInt64(v)
			}
			return sum
		},
		"b64enc": func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) },
		"now":    time.Now,
		"dateModify": func(d string, t time.Time) (time.Time, error) {
			dur, err := time.ParseDuration(d)
			if err != nil {
				return time.Time{}, err
			}
			return t.Add(dur), nil
		},
		"date": func(layout string, t time.Time) string { return t.Format(layout) },
	}
}

// requestDescriptor resolves "pkg.Service/Method" to the method's input
// message descriptor via the global protoregistry — the same resolution ghz
// performs over server reflection.
func requestDescriptor(call string) (protoreflect.MessageDescriptor, error) {
	svcName, method, ok := strings.Cut(call, "/")
	if !ok {
		return nil, fmt.Errorf("call %q is not Service/Method", call)
	}
	d, err := protoregistry.GlobalFiles.FindDescriptorByName(protoreflect.FullName(svcName))
	if err != nil {
		return nil, fmt.Errorf("service %q not registered: %w", svcName, err)
	}
	sd, ok := d.(protoreflect.ServiceDescriptor)
	if !ok {
		return nil, fmt.Errorf("%q is not a service", svcName)
	}
	md := sd.Methods().ByName(protoreflect.Name(method))
	if md == nil {
		return nil, fmt.Errorf("service %q has no method %q", svcName, method)
	}
	return md.Input(), nil
}

func isReceiptScenarioDriver(driver string) bool {
	switch driver {
	case "receipt_vertex_put", "receipt_vertex_delete", "receipt_edge_delete", "receipt_edge_add":
		return true
	default:
		return false
	}
}

func TestScenarioTemplates_MatchWireSchema(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("scenarios", "*.yaml"))
	if err != nil {
		t.Fatalf("glob scenarios: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("no scenario yamls found — glob root moved?")
	}

	// Two samples: RequestNumber 0 exercises the zero/modulo edge, a large
	// value exercises the padded printf/b64enc contrib-id paths.
	samples := []ghzTemplateData{
		{RequestNumber: 0, Timestamp: time.Now().UTC().Format(time.RFC3339Nano)},
		{RequestNumber: 987654321, Timestamp: time.Now().UTC().Format(time.RFC3339Nano)},
	}

	for _, path := range paths {
		stem := strings.TrimSuffix(filepath.Base(path), ".yaml")
		t.Run(stem, func(t *testing.T) {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			var doc scenarioDoc
			if err := yaml.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("parse yaml: %v", err)
			}
			if doc.Target.Driver != "" && doc.Target.Driver != "ghz" && doc.Target.Driver != "standalone_create" &&
				!isReceiptScenarioDriver(doc.Target.Driver) {
				t.Fatalf("unknown target.driver %q", doc.Target.Driver)
			}
			calls := doc.calls()
			if len(calls) == 0 {
				t.Fatal("scenario declares no target/subscribe calls — run.sh could not drive it")
			}
			for site, c := range calls {
				if c.Call == "" {
					t.Errorf("%s: empty call", site)
					continue
				}
				desc, err := requestDescriptor(c.Call)
				if err != nil {
					t.Errorf("%s: %v", site, err)
					continue
				}
				if strings.TrimSpace(c.DataTemplate) == "" {
					if isReceiptScenarioDriver(doc.Target.Driver) && strings.HasPrefix(site, "target.calls[") {
						continue
					}
					t.Errorf("%s (%s): empty data_template", site, c.Call)
					continue
				}
				tmpl, err := template.New(site).Funcs(ghzFuncs()).Parse(c.DataTemplate)
				if err != nil {
					t.Errorf("%s (%s): parse template: %v", site, c.Call, err)
					continue
				}
				for _, data := range samples {
					var sb strings.Builder
					if err := tmpl.Execute(&sb, data); err != nil {
						t.Errorf("%s (%s): render @%d: %v", site, c.Call, data.RequestNumber, err)
						continue
					}
					msg := dynamicpb.NewMessage(desc)
					if err := protojson.Unmarshal([]byte(sb.String()), msg); err != nil {
						t.Errorf("%s (%s): rendered template does not match %s:\n  %s\n  %v",
							site, c.Call, desc.FullName(), strings.TrimSpace(sb.String()), err)
					}
				}
			}
		})
	}
}

func TestStandaloneCreateRunnerContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("scenarios", "edge_create.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var document scenarioDoc
	if err := yaml.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	if document.Name != "edge_create" || document.Target.Driver != "standalone_create" || document.Target.Call != "graph.v1.LanternService/CreateEdges" {
		t.Fatal("standalone Create scenario drift")
	}
	command := exec.CommandContext(t.Context(), "python3", "-m", "unittest", "discover", "-s", "createprobe", "-p", "run_test.py")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("standalone Create runner: %v\n%s", err, output)
	}
}

func TestProtectedQueryComparisonContract(t *testing.T) {
	command := exec.CommandContext(t.Context(), "python3", "-B", "-m", "unittest", "discover", "-s", "protectedquery", "-p", "compare_test.py")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("protected query comparison: %v\n%s", err, output)
	}
}

func TestBenchConsumerAndChaosFailureCannotMasqueradeAsProducerFailure(t *testing.T) {
	script, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, rest, ok := strings.Cut(string(script), "# Reap background helpers (subscribers run for steady_duration;")
	if !ok {
		t.Fatal("missing workload reap block")
	}
	_, rest, ok = strings.Cut(rest, "\nfor consumer_index")
	if !ok {
		t.Fatal("missing consumer reap")
	}
	body, _, ok := strings.Cut("for consumer_index"+rest, "\nsteady_end_epoch=")
	if !ok {
		t.Fatal("missing workload reap end")
	}
	for _, tc := range []struct {
		name            string
		consumer, chaos int
	}{{"healthy", 0, 0}, {"consumer fails", 1, 0}, {"chaos fails", 0, 1}, {"both fail", 1, 1}} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := fmt.Sprintf(`set -euo pipefail
sub_pids=(101 102)
chaos_pid=103
producer_failed=0
consumer_failed=0
chaos_failed=0
log(){ :; }
wait(){ if [[ "$1" == 102 ]]; then return %d; elif [[ "$1" == 103 ]]; then return %d; else return 0; fi; }
%s
[[ "$producer_failed" == 0 && "$consumer_failed" == %d && "$chaos_failed" == %d && "$chaos_pid" == "" ]]
[[ "${#sub_pids[@]}" == 0 ]]
`, tc.consumer, tc.chaos, body, tc.consumer, tc.chaos)
			if output, err := exec.Command("bash", "-c", fixture).CombinedOutput(); err != nil {
				t.Fatalf("workload failure classification: %v %s", err, output)
			}
		})
	}
}

func TestBenchGhzStderrCaptureContract(t *testing.T) {
	command := exec.CommandContext(t.Context(), "python3", "-B", "-m", "unittest", "discover", "-s", "capture", "-p", "ghz_stderr_test.py")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("fixed ghz stderr capture: %v\n%s", err, output)
	}
}

func TestBenchGhzProducerDiagnosticsPreserveExitAndDrain(t *testing.T) {
	raw, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, functions, ok := strings.Cut(string(raw), "prepare_ghz_diagnostics() {\n")
	if !ok {
		t.Fatal("missing ghz diagnostic functions")
	}
	functions, _, ok = strings.Cut("prepare_ghz_diagnostics() {\n"+functions, "\nprepare_receipt_edge_add() {")
	if !ok {
		t.Fatal("missing ghz diagnostic function boundary")
	}
	_, steady, ok := strings.Cut(string(raw), "# Steady producer(s).")
	if !ok {
		t.Fatal("missing steady producer block")
	}
	_, steady, ok = strings.Cut(steady, "\ncalls_len=")
	if !ok {
		t.Fatal("missing steady setup")
	}
	steady, _, ok = strings.Cut("calls_len="+steady, "\nif [[ -n \"$lifecycle_reason\" ]]; then")
	if !ok {
		t.Fatal("missing steady block boundary")
	}
	_, verdict, ok := strings.Cut(string(raw), "\nworkload_verdict=\"pass\"\n")
	if !ok {
		t.Fatal("missing workload verdict")
	}
	verdict, _, ok = strings.Cut(verdict, "\njq -n --arg verdict")
	if !ok {
		t.Fatal("missing workload verdict boundary")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("/opt/homebrew/bin/bash"); err == nil {
		bash = "/opt/homebrew/bin/bash"
	}
	helper, err := filepath.Abs("capture/ghz_stderr.py")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"producer_failure", "success", "capture_write_failure", "capture_timeout", "capture_startup_failure"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			bin := filepath.Join(dir, "bin")
			out := filepath.Join(dir, "out")
			capture := filepath.Join(dir, "capture")
			for _, path := range []string{bin, out, capture} {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			helperData, err := os.ReadFile(helper)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "capture_timeout" {
				helperData = []byte("import sys, time\nif sys.argv[1:]==['--check']: sys.exit(0)\nwhile sys.stdin.buffer.read(65536): pass\ntime.sleep(60)\n")
			}
			if mode == "capture_startup_failure" {
				helperData = []byte("import sys\nsys.exit(9)\n")
			}
			if err := os.WriteFile(filepath.Join(capture, "ghz_stderr.py"), helperData, 0o600); err != nil {
				t.Fatal(err)
			}
			fake := `#!/usr/bin/env python3
import json, os, sys
from pathlib import Path
args=sys.argv[1:]
index=args[args.index('--call')+1][-1]
Path(os.environ['OUTDIR'], 'direct-pid-'+index).write_text(str(os.getpid()))
sys.stdout.write('SECRET-STDOUT https://user:password@private.test/payload\n')
body=b'metadata reflection service descriptor dial connect code = Unavailable context deadline exceeded\nAuthorization: Bearer SECRET-TOKEN https://user:password@private.test/payload\n'+b'x'*100000+b'\nPOST_CAP_SECRET_MARKER\n'
sys.stderr.buffer.write(body)
sys.stderr.buffer.flush()
Path(os.environ['OUTDIR'], 'drained-'+index).write_text(str(len(body)))
Path(args[args.index('-o')+1]).write_text(json.dumps({'fixture':True}))
sys.exit(37 if os.environ['FIXTURE_MODE']=='producer_failure' and index=='0' else 0)
`
			if err := os.WriteFile(filepath.Join(bin, "ghz"), []byte(fake), 0o700); err != nil {
				t.Fatal(err)
			}
			// Force O_EXCL output rejection after EOF, without changing ghz or
			// closing its stderr early. The exact production block still runs.
			caseSteady := strings.Replace(steady, "  prepare_ghz_diagnostics\n", `  prepare_ghz_diagnostics
printf '%s' "$GHZ_DIAGNOSTICS_DIR" > "$OUTDIR/private-path"
if [[ "$FIXTURE_MODE" == capture_write_failure ]]; then
 (umask 077; printf 'preserve' > "$GHZ_DIAGNOSTICS_DIR/producer_0.stderr.json"; printf 'preserve' > "$GHZ_DIAGNOSTICS_DIR/producer_1.stderr.json")
fi
`, 1)
			fixture := `set -euo pipefail
prod_pids=()
ghz_capture_pids=()
GHZ_DIAGNOSTICS_DIR=""
sleep 30 & unrelated_pid=$!
trap 'kill "$unrelated_pid" 2>/dev/null || true; wait "$unrelated_pid" 2>/dev/null || true' EXIT
receipt_driver=0
steady_rps=20
steady_conc=1
steady_duration=1s
SCENARIO_FILE=fixture.yaml
endpoints=(localhost:6380 localhost:6381 localhost:6382)
log(){ printf '%s\n' "$*"; }
die(){ printf '%s\n' "$*" >&2; exit 91; }
` + functions + `
yq(){ case "$2" in
 '.target.calls | length // 0') echo 2;;
 '.target.calls[0].call') echo Call0;;
 '.target.calls[1].call') echo Call1;;
 '.target.calls[0].data_template'|'.target.calls[1].data_template') echo SECRET-PAYLOAD;;
 '.target.calls[0].rps // 0'|'.target.calls[1].rps // 0') echo 0;;
 *) return 92;; esac; }
` + caseSteady + `
workload_verdict="pass"
` + verdict + `
printf '%s %s %s %s %s\n' "$producer_failed" "$producer_diagnostics_failed" "$workload_verdict" "${#prod_pids[@]}" "${#ghz_capture_pids[@]}" > "$OUTDIR/result"
kill -0 "$unrelated_pid"
`
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, bash, "-c", fixture)
			command.WaitDelay = time.Second
			command.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "OUTDIR="+out, "CAPTURE_DIR="+capture, "TMPDIR="+dir, "FIXTURE_MODE="+mode)
			output, runErr := command.CombinedOutput()
			for _, secret := range []string{"SECRET-TOKEN", "SECRET-STDOUT", "SECRET-PAYLOAD", "POST_CAP_SECRET_MARKER", "user:password", "private.test"} {
				if strings.Contains(string(output), secret) {
					t.Fatalf("secret surfaced: %s", secret)
				}
			}
			if mode == "capture_startup_failure" {
				if runErr == nil {
					t.Fatal("invalid helper startup passed")
				}
				if _, err := os.Stat(filepath.Join(out, "direct-pid-0")); !os.IsNotExist(err) {
					t.Fatal("ghz started before helper check")
				}
				return
			}
			if runErr != nil {
				t.Fatalf("steady fixture: %v\n%s", runErr, output)
			}
			want := "0 0 pass 0 0\n"
			if mode == "producer_failure" {
				want = "1 0 fail 0 0\n"
			}
			if mode == "capture_write_failure" || mode == "capture_timeout" {
				want = "0 1 fail 0 0\n"
			}
			result, err := os.ReadFile(filepath.Join(out, "result"))
			if err != nil || string(result) != want {
				t.Fatalf("status %q, want %q (%v)\n%s", result, want, err, output)
			}
			privatePath, err := os.ReadFile(filepath.Join(out, "private-path"))
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(string(privatePath))
			if err != nil || info.Mode().Perm() != 0o700 {
				t.Fatalf("private directory permissions: %v %v", info, err)
			}
			for i := 0; i < 2; i++ {
				raw, err := os.ReadFile(filepath.Join(string(privatePath), fmt.Sprintf("producer_%d.exit.json", i)))
				if err != nil {
					t.Fatal(err)
				}
				var exit struct {
					Status int `json:"ghz_exit_status"`
					PID    int `json:"ghz_pid"`
				}
				if err := json.Unmarshal(raw, &exit); err != nil {
					t.Fatal(err)
				}
				wantStatus := 0
				if mode == "producer_failure" && i == 0 {
					wantStatus = 37
				}
				pid, _ := os.ReadFile(filepath.Join(out, fmt.Sprintf("direct-pid-%d", i)))
				if exit.Status != wantStatus || fmt.Sprint(exit.PID) != string(pid) {
					t.Fatalf("direct child status lost: %+v pid=%s", exit, pid)
				}
				if _, err := os.Stat(filepath.Join(out, fmt.Sprintf("drained-%d", i))); err != nil {
					t.Fatalf("ghz did not finish post-cap write: %v", err)
				}
				if mode != "capture_write_failure" && mode != "capture_timeout" {
					path := filepath.Join(string(privatePath), fmt.Sprintf("producer_%d.stderr.json", i))
					raw, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					var record struct {
						Raw       bool `json:"raw_stderr_saved"`
						Count     int  `json:"observed_stderr_bytes"`
						Prefix    int  `json:"captured_prefix_bytes"`
						Truncated bool `json:"truncated"`
					}
					if err := json.Unmarshal(raw, &record); err != nil {
						t.Fatal(err)
					}
					count, _ := os.ReadFile(filepath.Join(out, fmt.Sprintf("drained-%d", i)))
					if record.Raw || !record.Truncated || record.Prefix != 65536 || fmt.Sprint(record.Count) != string(count) {
						t.Fatalf("cap/EOF observation: %+v", record)
					}
					for _, secret := range []string{"SECRET", "private.test", "user:password"} {
						if strings.Contains(string(raw), secret) {
							t.Fatalf("secret persisted: %s", secret)
						}
					}
				}
			}
			entries, err := os.ReadDir(string(privatePath))
			if err != nil {
				t.Fatal(err)
			}
			wantEntries := 4
			if mode == "capture_timeout" {
				wantEntries = 2
			}
			if len(entries) != wantEntries {
				t.Fatalf("unexpected private artifacts: %v", entries)
			}
			for _, entry := range entries {
				info, err := entry.Info()
				if err != nil || info.Mode().Perm() != 0o600 {
					t.Fatalf("private file permissions: %v %v", info, err)
				}
			}
		})
	}
}

func TestBenchGhzDiagnosticsCancellationAndPrivateCleanup(t *testing.T) {
	raw, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatal(err)
	}
	_, functions, ok := strings.Cut(string(raw), "prepare_ghz_diagnostics() {\n")
	if !ok {
		t.Fatal("missing diagnostic functions")
	}
	functions, _, ok = strings.Cut("prepare_ghz_diagnostics() {\n"+functions, "\nprepare_receipt_edge_add() {")
	if !ok {
		t.Fatal("missing diagnostic function end")
	}
	_, cleanup, ok := strings.Cut(string(raw), "cleanup() {\n")
	if !ok {
		t.Fatal("missing cleanup")
	}
	cleanup, _, ok = strings.Cut("cleanup() {\n"+cleanup, "\ntrap cleanup EXIT")
	if !ok {
		t.Fatal("missing cleanup end")
	}
	owned, err := filepath.Abs("owned_processes.sh")
	if err != nil {
		t.Fatal(err)
	}
	helper, err := filepath.Abs("capture/ghz_stderr.py")
	if err != nil {
		t.Fatal(err)
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("/opt/homebrew/bin/bash"); err == nil {
		bash = "/opt/homebrew/bin/bash"
	}
	for _, mode := range []string{"success", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			fake := filepath.Join(dir, "ghz")
			if err := os.WriteFile(fake, []byte("#!/usr/bin/env python3\nimport os, sys, time\nfrom pathlib import Path\nsys.stderr.buffer.write(b'x'*100000)\nsys.stderr.buffer.flush()\nPath(os.environ['OUTDIR'],'ready').touch()\ntime.sleep(60)\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			fixture := `set -euo pipefail
prod_pids=()
sub_pids=()
ghz_capture_pids=()
producer_diagnostics_failed=0
COMPOSE_STARTED=0
PEER_TLS_DIR=""
NATIVE_WORK_DIR=""
GHZ_DIAGNOSTICS_DIR=""
MEMBERSHIP_PID=""
DIAGNOSTICS_PID=""
chaos_pid=""
source "$OWNED_PROCESSES"
log(){ printf '%s\n' "$*"; }
die(){ exit 91; }
` + functions + cleanup + `
trap cleanup EXIT
prepare_ghz_diagnostics
printf '%s' "$GHZ_DIAGNOSTICS_DIR" > "$OUTDIR/private-path"
if [[ "$FIXTURE_MODE" == success ]]; then exit 0; fi
pipe="$GHZ_DIAGNOSTICS_DIR/producer_0.pipe"
(umask 077; mkfifo "$pipe")
python3 -B "$CAPTURE_DIR/ghz_stderr.py" "$GHZ_DIAGNOSTICS_DIR/producer_0.stderr.json" "$GHZ_BINARY" < "$pipe" &
ghz_capture_pids+=("$!")
ghz >/dev/null 2>"$pipe" &
prod_pids+=("$!")
printf '%s %s' "${prod_pids[0]}" "${ghz_capture_pids[0]}" > "$OUTDIR/pids"
for round in {1..200}; do
 [[ ! -f "$OUTDIR/ready" ]] || break
 sleep 0.01
done
[[ -f "$OUTDIR/ready" ]]
exit 23
`
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, bash, "-c", fixture)
			command.WaitDelay = time.Second
			command.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"), "OUTDIR="+dir, "TMPDIR="+dir, "CAPTURE_DIR="+filepath.Dir(helper), "OWNED_PROCESSES="+owned, "FIXTURE_MODE="+mode)
			output, runErr := command.CombinedOutput()
			wantExit := 0
			if mode == "cancel" {
				wantExit = 23
			}
			if command.ProcessState == nil || command.ProcessState.ExitCode() != wantExit {
				t.Fatalf("cleanup exit=%v err=%v output=%s", command.ProcessState, runErr, output)
			}
			private, err := os.ReadFile(filepath.Join(dir, "private-path"))
			if err != nil {
				t.Fatal(err)
			}
			if mode == "success" {
				if _, err := os.Stat(string(private)); !os.IsNotExist(err) {
					t.Fatalf("successful private directory retained: %v", err)
				}
				if strings.Contains(string(output), "diagnostics retained") {
					t.Fatalf("success announced private failure: %s", output)
				}
				return
			}
			if !strings.Contains(string(output), "run.sh: private ghz producer diagnostics retained at "+string(private)) {
				t.Fatalf("cancel evidence marker missing: %s", output)
			}
			entries, err := os.ReadDir(string(private))
			if err != nil || len(entries) != 1 || entries[0].Name() != "producer_0.stderr.json" {
				t.Fatalf("cancelled collector not drained or FIFO retained: %v %v", entries, err)
			}
			pids, err := os.ReadFile(filepath.Join(dir, "pids"))
			if err != nil {
				t.Fatal(err)
			}
			for _, pid := range strings.Fields(string(pids)) {
				if err := exec.Command(bash, "-c", `! kill -0 "$1" 2>/dev/null`, "check", pid).Run(); err != nil {
					t.Fatalf("owned child %s still running", pid)
				}
			}
		})
	}
}
