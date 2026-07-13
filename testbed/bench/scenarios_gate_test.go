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
	"fmt"
	"os"
	"path/filepath"
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
}

// scenarioDoc is the subset of the scenario schema that names RPCs. Keep in
// sync with the extraction sites in testbed/bench/run.sh (target.call,
// target.calls[], subscribe, subscribe.consumers[]).
type scenarioDoc struct {
	Name   string `yaml:"name"`
	Target struct {
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
			Calls []struct {
				Name         string `yaml:"name"`
				Call         string `yaml:"call"`
				DataTemplate string `yaml:"data_template"`
				RPS          int    `yaml:"rps"`
			} `yaml:"calls"`
		} `yaml:"target"`
		Phases struct {
			Steady struct {
				RPS int `yaml:"rps"`
			} `yaml:"steady"`
		} `yaml:"phases"`
		MetricGate struct {
			Metrics map[string]map[string]float64 `yaml:"metrics"`
		} `yaml:"metric_gate"`
		PerfGate struct {
			Producers map[string]struct {
				MinSteadyRPS *float64 `yaml:"min_steady_rps"`
				MaxP99MS     *float64 `yaml:"max_p99_ms"`
				MaxNonOK     *float64 `yaml:"max_non_ok_ratio"`
			} `yaml:"producers"`
		} `yaml:"perf_gate"`
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
		"lantern_search_index_retained_ratio",
		"lantern_search_index_healthy",
	} {
		if len(doc.MetricGate.Metrics[metric]) == 0 {
			t.Errorf("metric %s has no threshold", metric)
		}
	}
	retainedRatio := doc.MetricGate.Metrics["lantern_search_index_retained_ratio"]
	if increase, ok := retainedRatio["max_increase"]; !ok || increase != 0 {
		t.Errorf("retained ratio max_increase = %v, present %t; want 0, true", increase, ok)
	}
	if ratio, ok := retainedRatio["max_ratio"]; !ok || ratio != 1 {
		t.Errorf("retained ratio max_ratio = %v, present %t; want 1, true", ratio, ok)
	}
	if _, ok := retainedRatio["max_post"]; ok {
		t.Error("retained ratio must not use an absolute max_post")
	}

	calls := make(map[string]string, len(doc.Target.Calls))
	totalRPS := 0
	for _, call := range doc.Target.Calls {
		if call.Name == "" || call.RPS <= 0 {
			t.Errorf("producer has invalid name/rps: %+v", call)
			continue
		}
		totalRPS += call.RPS
		calls[call.Name] = strings.TrimSpace(call.DataTemplate)
		gate, ok := doc.PerfGate.Producers[call.Name]
		if !ok || gate.MinSteadyRPS == nil || gate.MaxP99MS == nil || gate.MaxNonOK == nil {
			t.Errorf("producer %q lacks independent rps+p99/non-OK gate", call.Name)
		}
	}
	if totalRPS != doc.Phases.Steady.RPS {
		t.Errorf("producer rps sum = %d, steady rps = %d", totalRPS, doc.Phases.Steady.RPS)
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
	runScript, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(runScript), `any(.producer_results[]; .verdict == "fail")`) {
		t.Error("perf gate does not conjunctively fold named producer failures")
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

// TestSearchReleaseQualificationIsBlocking pins the release dependency rather
// than merely checking that a qualification job exists. A failed or skipped
// stage must produce a fail verdict, and image publication must need that job.
func TestSearchReleaseQualificationIsBlocking(t *testing.T) {
	releaseWorkflow, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", "docker-publish.yml"))
	if err != nil {
		t.Fatal(err)
	}
	release := string(releaseWorkflow)
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
			calls := doc.calls()
			if len(calls) == 0 {
				t.Fatal("scenario declares no target/subscribe calls — run.sh could not drive it")
			}
			for site, c := range calls {
				if c.Call == "" {
					t.Errorf("%s: empty call", site)
					continue
				}
				if strings.TrimSpace(c.DataTemplate) == "" {
					t.Errorf("%s (%s): empty data_template", site, c.Call)
					continue
				}
				desc, err := requestDescriptor(c.Call)
				if err != nil {
					t.Errorf("%s: %v", site, err)
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
