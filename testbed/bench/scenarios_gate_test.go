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
		`go run ./testbed/bench/receipttls generate -dir "$PEER_TLS_DIR"`,
		`COMPOSE_FILES+=( -f "$HERE/compose.receipt-tls.yml" )`,
		`receipt driver requires a fresh Compose lifecycle`,
		`docker compose "${COMPOSE_FILES[@]}" down -v --remove-orphans`,
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
	volumeReset := strings.Index(string(runScript), `docker compose "${COMPOSE_FILES[@]}" down -v --remove-orphans`)
	if projectScope < 0 || volumeReset < 0 || projectScope >= volumeReset {
		t.Error("receipt volume reset must use the named bench Compose project")
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
	composeOverride, err := os.ReadFile("compose.override.yml")
	if err != nil {
		t.Fatal(err)
	}
	for _, variable := range []string{
		"LANTERN_BENCH_AUTH_TOKEN",
		"LANTERN_BENCH_BACKUP_RESTORE_ON_START",
		"LANTERN_BENCH_RECEIPT_EPOCH",
		"LANTERN_BENCH_RECEIPT_WAL_MODE",
		"LANTERN_BENCH_RECEIPT_WAL_PATH",
		"LANTERN_BENCH_RECEIPT_MAX_ENTRIES",
		"LANTERN_BENCH_RECEIPT_MAX_BYTES",
		"LANTERN_BENCH_RECEIPT_RETENTION",
		"LANTERN_BENCH_NODE_ID_0",
		"LANTERN_BENCH_NODE_ID_1",
		"LANTERN_BENCH_NODE_ID_2",
	} {
		if !strings.Contains(string(composeOverride), variable) {
			t.Errorf("compose override missing %s", variable)
		}
		receiptOverlay, err := os.ReadFile("compose.receipt-tls.yml")
		if err != nil {
			t.Fatal(err)
		}
		var overlay struct {
			Services map[string]struct {
				Environment map[string]string `yaml:"environment"`
				Volumes     []string          `yaml:"volumes"`
			} `yaml:"services"`
		}
		if err := yaml.Unmarshal(receiptOverlay, &overlay); err != nil {
			t.Fatal(err)
		}
		if len(overlay.Services) != 3 {
			t.Fatalf("receipt TLS overlay has %d services, want three replicas only", len(overlay.Services))
		}
		for _, service := range []string{"lantern-0", "lantern-1", "lantern-2"} {
			got, ok := overlay.Services[service]
			if !ok {
				t.Errorf("receipt TLS overlay is missing %s", service)
				continue
			}
			if len(got.Volumes) != 1 || got.Volumes[0] !=
				"${LANTERN_BENCH_PEER_TLS_DIR:?receipt TLS certificates required}/"+service+":/run/lantern-tls:ro" {
				t.Errorf("%s must mount only its own TLS material read-only: %v", service, got.Volumes)
			}
			for name, path := range map[string]string{
				"LANTERN_TLS_CERT_FILE": "/run/lantern-tls/server.pem",
				"LANTERN_TLS_KEY_FILE":  "/run/lantern-tls/server.key",
				"LANTERN_PEER_CA_FILE":  "/run/lantern-tls/ca.pem",
			} {
				if got.Environment[name] != path {
					t.Errorf("%s %s = %q, want %q", service, name, got.Environment[name], path)
				}
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
	body, _, found := strings.Cut(rest, "\nif [[ \"$receipt_driver\" == \"1\" ]]; then\n  pin_receipt_image")
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
		name    string
		wantErr string
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
    printf '%s|%s|lantern:local|true|%s|%s|%s\n' "$container" "$image" "$project" "$service" "$restarts"
    return
  fi
  return 90
}
` + receiptImageShellFunctions(t) + `
pin_receipt_image
verify_receipt_image_provenance "$OUTDIR/proof_pre.json"
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
			cmd := exec.Command("bash", "-c", fixture)
			cmd.Env = append(os.Environ(),
				"OUTDIR="+outDir, "CASE="+tc.name,
				"GOOD_ID="+imageID, "WRONG_ID="+wrongID,
				"GOOD_COMMIT="+commit, "EXPECTED_LANTERN_IMAGE_ID="+pinnedID,
				"EXPECTED_LANTERN_COMMIT="+pinnedCommit,
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
					ImageID string `json:"image_id"`
				} `json:"replicas"`
			}
			if err := json.Unmarshal(data, &proof); err != nil {
				t.Fatal(err)
			}
			if proof.ImageID != imageID || proof.SourceCommit != commit ||
				len(proof.Replicas) != 3 {
				t.Fatalf("incomplete image proof: %+v", proof)
			}
			for _, replica := range proof.Replicas {
				if replica.ImageID != imageID {
					t.Fatalf("replica image drift in proof: %+v", proof)
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
		t.Fatal("run.sh has no receipt peer TLS preflight")
	}
	body, _, found := strings.Cut(rest, "\n}\n\n# ----- compose up")
	if !found {
		t.Fatal("run.sh has no complete receipt peer TLS preflight")
	}
	return "verify_receipt_peer_tls() {\n" + body + "\n}\n"
}

func TestReceiptBenchTLSPeerPreflightFailsClosed(t *testing.T) {
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq is required for the receipt harness")
	}
	for _, tc := range []struct {
		name, wantErr string
		postFailure   bool
	}{
		{name: "verified before and after load"},
		{name: "missing own mount", wantErr: "no pinned, read-only"},
		{name: "writable mount", wantErr: "no pinned, read-only"},
		{name: "wrong discovery identity", wantErr: "missing or mismatched"},
		{name: "wrong bearer config", wantErr: "missing or mismatched"},
		{name: "invalid live TLS identity", wantErr: "TLS identity/provenance verification failed"},
		{name: "changed live TLS identity", wantErr: "TLS identity/provenance verification failed", postFailure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outDir := t.TempDir()
			fixture := `set -euo pipefail
COMPOSE_FILES=(-f fixture.yml)
PEER_TLS_DIR="$OUTDIR/private"
REPO_ROOT="$OUTDIR"
REPLICA_GRPC_PORTS=(6380 6381 6382)
LANTERN_BENCH_AUTH_TOKEN=fixture-only
die() { printf 'fatal: %s\n' "$*" >&2; exit 37; }
docker() {
  if [[ "$1" == compose && "$2" == -f && "$3" == fixture.yml &&
        "$4" == ps && "$5" == -q ]]; then
    printf 'container-%s\n' "$6"
    return
  fi
  [[ "$1" == inspect && "$2" == --format ]] || return 88
  local service="${4#container-}"
  case "$3" in
    '{{json .Mounts}}')
      local source="$PEER_TLS_DIR/$service" rw=false
      if [[ "$CASE" == "missing own mount" && "$service" == lantern-1 ]]; then
        source="$PEER_TLS_DIR/lantern-0"
      fi
      if [[ "$CASE" == "writable mount" && "$service" == lantern-1 ]]; then rw=true; fi
      jq -nc --arg source "$source" --argjson rw "$rw" \
        '[{Type:"bind",Destination:"/run/lantern-tls",RW:$rw,Source:$source}]'
      ;;
    '{{json .Config.Env}}')
      local dns=lantern token="$LANTERN_BENCH_AUTH_TOKEN"
      if [[ "$CASE" == "wrong discovery identity" && "$service" == lantern-1 ]]; then
        dns=unrelated.invalid
      fi
      if [[ "$CASE" == "wrong bearer config" && "$service" == lantern-1 ]]; then
        token=unrelated
      fi
      jq -nc --arg dns "$dns" --arg token "$token" '[
        "LANTERN_TLS_CERT_FILE=/run/lantern-tls/server.pem",
        "LANTERN_TLS_KEY_FILE=/run/lantern-tls/server.key",
        "LANTERN_PEER_CA_FILE=/run/lantern-tls/ca.pem",
        "LANTERN_PEER_DISCOVERY=dns",
        "LANTERN_PEER_DNS_NAME=" + $dns,
        "LANTERN_PEER_DEFAULT_PORT=6380",
        "LANTERN_AUTH_TOKENS=" + $token
      ]'
      ;;
    *) return 89 ;;
  esac
}
go() {
  [[ "$1" == run && "$2" == ./testbed/bench/receipttls && "$3" == verify ]] ||
    return 88
  [[ "$*" == *"-dir $PEER_TLS_DIR"* && "$*" == *"-ports 6380,6381,6382"* ]] ||
    return 88
  if [[ "$CASE" == "invalid live TLS identity" ||
        ( "$CASE" == "changed live TLS identity" && "$*" == *"-baseline"* ) ]]; then
    return 86
  fi
  local out="" prev="" arg
  for arg in "$@"; do
    if [[ "$prev" == -out ]]; then out="$arg"; fi
    prev="$arg"
  done
  [[ -n "$out" ]] || return 88
  printf '{"ca_sha256":"fixture"}\n' > "$out"
}
` + receiptTLSShellFunction(t) + `
verify_receipt_peer_tls "$OUTDIR/tls_pre.json"
verify_receipt_peer_tls "$OUTDIR/tls_post.json" "$OUTDIR/tls_pre.json"
`
			cmd := exec.Command("bash", "-c", fixture)
			cmd.Env = append(os.Environ(), "OUTDIR="+outDir, "CASE="+tc.name)
			output, err := cmd.CombinedOutput()
			if tc.wantErr != "" {
				if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 37 ||
					!strings.Contains(string(output), tc.wantErr) {
					t.Fatalf("preflight exit = %v, err = %v, output = %s; want %s",
						cmd.ProcessState, err, output, tc.wantErr)
				}
				if _, err := os.Stat(filepath.Join(outDir, "tls_post.json")); !os.IsNotExist(err) {
					t.Fatalf("invalid TLS postflight was recorded: %v", err)
				}
				if _, err := os.Stat(filepath.Join(outDir, "tls_pre.json")); tc.postFailure != (err == nil) {
					t.Fatalf("TLS preflight present = %t, want %t: %v", err == nil, tc.postFailure, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("valid receipt TLS pre/postflight = %v, output = %s", err, output)
			}
			for _, name := range []string{"tls_pre.json", "tls_post.json"} {
				if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
					t.Fatalf("missing %s: %v", name, err)
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
		runStatus    int
		keepUp       int
		wantStatus   int
		wantDown     bool
	}{
		{name: "clean teardown", wantDown: true},
		{name: "teardown failure disqualifies pass", dockerStatus: 19, wantStatus: 1, wantDown: true},
		{name: "primary failure preserved", dockerStatus: 19, runStatus: 23, wantStatus: 23, wantDown: true},
		{name: "explicit keep up", dockerStatus: 19, keepUp: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			outDir := t.TempDir()
			reportPath := filepath.Join(outDir, "report.md")
			if err := os.WriteFile(reportPath, []byte("**Perf gate verdict:** `pass`\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			fixture := fmt.Sprintf(`set -euo pipefail
export COMPOSE_PROJECT_NAME=lantern-bench-cleanup-fixture
COMPOSE_STARTED=1
COMPOSE_FILES=(-f fixture-compose.yml)
KEEP_UP=%d
log() { :; }
docker() {
  [[ "$COMPOSE_PROJECT_NAME" == "lantern-bench-cleanup-fixture" ]] || return 88
  [[ "$*" == "compose -f fixture-compose.yml down -v --remove-orphans" ]] || return 89
  echo "docker down invoked" >&2
  return %d
}
%s
trap cleanup EXIT
exit %d
`, tc.keepUp, tc.dockerStatus, cleanup, tc.runStatus)
			cmd := exec.Command("bash", "-c", fixture)
			cmd.Env = append(os.Environ(), "OUTDIR="+outDir)
			output, err := cmd.CombinedOutput()
			if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != tc.wantStatus {
				t.Fatalf("cleanup exit = %v, err = %v, output = %s; want %d",
					cmd.ProcessState, err, output, tc.wantStatus)
			}
			if got := strings.Contains(string(output), "docker down invoked"); got != tc.wantDown {
				t.Errorf("compose down invoked = %v, want %v; output = %s", got, tc.wantDown, output)
			}
			if tc.dockerStatus != 0 && tc.wantDown && !strings.Contains(string(output), "run is unqualified") {
				t.Errorf("teardown failure is not reported: %s", output)
			}
			report, err := os.ReadFile(reportPath)
			if err != nil {
				t.Fatal(err)
			}
			if got, want := strings.Contains(string(report), "unqualified"), tc.dockerStatus != 0 && tc.wantDown; got != want {
				t.Errorf("report marked unqualified = %v, want %v: %s", got, want, report)
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

func TestReceiptBenchSnapshotRejectsFailedGC(t *testing.T) {
	outDir := t.TempDir()
	fixture := `set -euo pipefail
receipt_driver=1
REPLICA_METRICS_PORTS=(9390 9391 9392)
die() { printf 'fatal: %s\n' "$*" >&2; exit 37; }
curl() {
  [[ "$*" == *"/debug/pprof/heap?gc=1"* ]] && return 22
  echo "metrics must not be sampled without GC" >&2
  return 98
}
` + receiptSnapshotShellFunctions(t) + `
snapshot_runtime "$OUTDIR/runtime.json"
`
	cmd := exec.Command("bash", "-c", fixture)
	cmd.Env = append(os.Environ(), "OUTDIR="+outDir)
	output, err := cmd.CombinedOutput()
	if cmd.ProcessState == nil || cmd.ProcessState.ExitCode() != 37 ||
		!strings.Contains(string(output), "forced GC failed for localhost:9390 (round 1)") {
		t.Fatalf("snapshot exit = %v, err = %v, output = %s; want forced-GC failure", cmd.ProcessState, err, output)
	}
	if _, err := os.Stat(filepath.Join(outDir, "runtime.json")); !os.IsNotExist(err) {
		t.Fatalf("failed-GC snapshot artifact exists or stat failed: %v", err)
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
		`"$producer_failed" == "0"`,
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
	composeDown := strings.Index(run, `docker compose "${COMPOSE_FILES[@]}" down -v`)
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
	if !strings.Contains(string(compose), "  LANTERN_MUTATION_LOG_SUBSCRIBER_BUFFER:\n") ||
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
			if doc.Target.Driver != "" && doc.Target.Driver != "ghz" &&
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
