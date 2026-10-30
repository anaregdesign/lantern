package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Issue1960 is an opt-in, normal EdgeDelete prototype. Its result is never a
// replacement for the operative legacy leak gate or release acceptance.
const goroutineCandidateProtocol = "receipt_goroutine_candidate_v1"

type goroutineReference struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type goroutinePassive struct {
	SubscribeActive   int64            `json:"subscribe_active"`
	ChangesActive     int64            `json:"changes_active"`
	AntiEntropyCycles int64            `json:"anti_entropy_cycles"`
	Peers             map[string]int64 `json:"peers"`
}

type goroutineNodeProfile struct {
	Endpoint      string   `json:"endpoint"`
	ContainerID   string   `json:"container_id"`
	PID           int64    `json:"pid"`
	StartedAt     string   `json:"started_at"`
	ExpectedPeers []string `json:"expected_peers"`
	// Baseline only: the four allowlisted metric names from the SAME old PRE
	// scrape. Post values come from the collector flight, without another scrape.
	PassiveMetrics *goroutineReference `json:"passive_metrics,omitempty"`
}

type goroutineProfile struct {
	Schema              int                    `json:"schema"`
	Protocol            string                 `json:"protocol"`
	Scenario            string                 `json:"scenario"`
	Family              string                 `json:"family"`
	ImageScope          string                 `json:"image_scope"`
	MeasurementProtocol string                 `json:"measurement_protocol"`
	SourceCommit        string                 `json:"source_commit"`
	SourceTree          string                 `json:"source_tree"`
	ImageID             string                 `json:"image_id"`
	ConfigSHA256        string                 `json:"config_sha256"`
	ScenarioSHA256      string                 `json:"scenario_sha256"`
	ObserverSHA256      string                 `json:"observer_sha256"`
	ObserverGoVersion   string                 `json:"observer_go_version"`
	MetricsTransport    string                 `json:"metrics_transport"`
	BridgePID           int64                  `json:"bridge_pid"`
	BridgePorts         []int                  `json:"bridge_ports"`
	CPUSet              string                 `json:"cpuset"`
	RuntimeUser         string                 `json:"runtime_user"`
	Nodes               []goroutineNodeProfile `json:"nodes"`
}

type goroutineOwnedWork struct {
	Kind         string `json:"kind"` // producer, subscriber, chaos
	Declared     *int64 `json:"declared"`
	Reaped       *int64 `json:"reaped"`
	PendingRPC   *int64 `json:"pending_rpc"`
	ExitFailures *int64 `json:"exit_failures"`
}

type goroutineWorkReports struct {
	Phase             string             `json:"phase"` // warmup, steady
	Admission         goroutineReference `json:"admission"`
	Lookup            goroutineReference `json:"lookup"`
	CoordinatorReturn goroutineReference `json:"coordinator_return"`
}

// A successful foreground coordinator return proves its client phase exited;
// this is deliberately narrower than normal-server/global owner quiescence.
type goroutinePhaseReturn struct {
	Schema              int    `json:"schema"`
	Protocol            string `json:"protocol"`
	Phase               string `json:"phase"`
	Scope               string `json:"scope"`
	StartedNS           int64  `json:"started_ns"`
	FinishedNS          int64  `json:"finished_ns"`
	ExitCode            *int   `json:"exit_code"`
	CoordinatorReturned *bool  `json:"coordinator_returned"`
}

type goroutineBarrier struct {
	Schema           int                    `json:"schema"`
	Protocol         string                 `json:"protocol"`
	Scope            string                 `json:"scope"` // declared_client_work_only_not_global_idle
	T0NS             int64                  `json:"t0_ns"` // host Python CLOCK_MONOTONIC, shared by wrappers
	SemanticComplete *bool                  `json:"semantic_complete"`
	PendingRPC       *int64                 `json:"pending_rpc"`
	OwnedWork        []goroutineOwnedWork   `json:"owned_work"`
	WorkReports      []goroutineWorkReports `json:"work_reports"`
}

type goroutinePostControl struct {
	Schema           int                `json:"schema"`
	Protocol         string             `json:"protocol"`
	OffsetSeconds    int                `json:"offset_seconds"` // exactly5 or10
	MetricsURLs      []string           `json:"metrics_urls"`
	OperatorLoopback bool               `json:"operator_loopback"`
	Profile          goroutineReference `json:"profile"`
}

type goroutinePostReplica struct {
	Runtime runtimeMetrics   `json:"runtime"`
	Passive goroutinePassive `json:"passive"`
}

type goroutinePostCapture struct {
	Schema           int                    `json:"schema"`
	Protocol         string                 `json:"protocol"`
	CandidateOnly    bool                   `json:"candidate_only"`
	Qualification    bool                   `json:"qualification"`
	ReleaseAdmission bool                   `json:"release_admission"`
	OffsetSeconds    int                    `json:"offset_seconds"`
	Profile          goroutineReference     `json:"profile"`
	StartedUTC       string                 `json:"started_utc"`
	DurationNS       int64                  `json:"duration_ns"`
	Replicas         []goroutinePostReplica `json:"replicas"`
	Verdict          string                 `json:"verdict"` // captured or incomplete; never qualification
	Failure          string                 `json:"failure,omitempty"`
}

type goroutinePostInput struct {
	OffsetSeconds int                `json:"offset_seconds"`
	StartNS       int64              `json:"start_ns"` // host clock, BEFORE launching pinned collector
	EndNS         int64              `json:"end_ns"`   // same host clock, AFTER direct child reaped
	Capture       goroutineReference `json:"capture"`
	Profile       goroutineReference `json:"profile"` // identity metadata after this flight
}

type goroutinePolicyInput struct {
	Schema            int                  `json:"schema"`
	Protocol          string               `json:"protocol"`
	Pre               goroutineReference   `json:"pre"` // exact old runtime_pre.json, B unchanged
	Steady            goroutineReference   `json:"steady"`
	LegacyPost        goroutineReference   `json:"legacy_post"` // memory timing unchanged
	BaselineProfile   goroutineReference   `json:"baseline_profile"`
	BeforePostProfile goroutineReference   `json:"before_post_profile"`
	Barrier           goroutineReference   `json:"barrier"`
	Posts             []goroutinePostInput `json:"posts"` // ordered5,10, no replacements
	WorkloadReport    goroutineReference   `json:"workload_report"`
	OwnerEvidence     []goroutineReference `json:"owner_evidence"` // source-test refs only
}

type goroutinePolicyReplica struct {
	Endpoint    string `json:"endpoint"`
	Baseline    int64  `json:"baseline"`
	SampledPeak int64  `json:"sampled_peak"`
	LegacyPost  int64  `json:"legacy_post"`
	LegacyDelta int64  `json:"legacy_post_delta"`
	Post5       int64  `json:"post_5"`
	Post10      int64  `json:"post_10"`
	Delta5      int64  `json:"delta_5"`
	Delta10     int64  `json:"delta_10"`
}

// Candidate reads the existing snapshot's optional diagnostic object too,
// without changing runtimeSnapshot or any historical evaluator behavior.
type goroutineSnapshot struct {
	Endpoint           string `json:"endpoint"`
	Goroutines         *int64 `json:"goroutines"`
	HeapInuseBytes     int64  `json:"heap_inuse_bytes"`
	HeapAllocBytes     *int64 `json:"heap_alloc_bytes"`
	HeapObjects        int64  `json:"heap_objects"`
	VertexHLCEntries   int64  `json:"vertex_hlc_entries"`
	VertexHLCHighWater int64  `json:"vertex_hlc_high_water"`
	// The old snapshot also retains optional nullable operational diagnostics.
	// Candidate goroutine policy does not interpret/rewrite that memory context.
	Metrics json.RawMessage `json:"metrics,omitempty"`
}

type goroutinePolicyReport struct {
	Schema             int                      `json:"schema"`
	Protocol           string                   `json:"protocol"`
	CandidateOnly      bool                     `json:"candidate_only"`
	Qualification      bool                     `json:"qualification"`
	ReleaseAdmission   bool                     `json:"release_admission"`
	SampledPeakLimit   int64                    `json:"sampled_peak_limit"`
	PostResidualLimit  int64                    `json:"post_residual_limit"`
	OperationalVerdict string                   `json:"sampled_peak_verdict"`
	ResidualVerdict    string                   `json:"post_drain_verdict"`
	Verdict            string                   `json:"verdict"`
	Scope              string                   `json:"scope"`
	Replicas           []goroutinePolicyReplica `json:"replicas"`
	Failures           []string                 `json:"failures,omitempty"`
	MissingPremises    []string                 `json:"missing_premises,omitempty"`
	Inputs             goroutinePolicyInput     `json:"inputs"`
}

func goroutineRead(ref goroutineReference, limit int64) ([]byte, error) {
	return goroutineReadMode(ref, limit, false)
}

func goroutineReadMode(ref goroutineReference, limit int64, private bool) ([]byte, error) {
	if !filepath.IsAbs(ref.Path) || filepath.Clean(ref.Path) != ref.Path || !retainedHex(ref.SHA256, 64) {
		return nil, errors.New("invalid hash-bound reference")
	}
	physical, err := filepath.EvalSymlinks(ref.Path)
	if err != nil || physical != ref.Path {
		return nil, errors.New("nonphysical reference")
	}
	before, err := os.Lstat(ref.Path)
	valid := func(info os.FileInfo) bool {
		return info.Mode().IsRegular() && retainedEvidenceMetadataOK(info) && info.Size() > 0 && info.Size() <= limit && (info.Mode().Perm() == 0o600 || !private && info.Mode().Perm() == 0o644)
	}
	if err != nil || !valid(before) {
		return nil, errors.New("invalid owned evidence metadata")
	}
	file, err := openRetainedEvidence(ref.Path)
	if err != nil {
		return nil, errors.New("evidence open failed")
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || !valid(opened) || !os.SameFile(before, opened) || !retainedEvidenceMetadataEqual(before, opened) || before.Mode() != opened.Mode() || before.Size() != opened.Size() || before.ModTime() != opened.ModTime() {
		return nil, errors.New("evidence replaced")
	}
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil || int64(len(data)) != before.Size() {
		return nil, errors.New("evidence read incomplete")
	}
	final, err := file.Stat()
	after, pathErr := os.Lstat(ref.Path)
	if err != nil || pathErr != nil || !valid(after) || !os.SameFile(before, final) || !os.SameFile(before, after) || !retainedEvidenceMetadataEqual(before, final) || !retainedEvidenceMetadataEqual(before, after) || before.Mode() != final.Mode() || before.Mode() != after.Mode() || before.Size() != final.Size() || before.Size() != after.Size() || before.ModTime() != final.ModTime() || before.ModTime() != after.ModTime() {
		return nil, errors.New("evidence changed")
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != ref.SHA256 {
		return nil, errors.New("reference digest differs")
	}
	return data, nil
}

func goroutineDecode(ref goroutineReference, value any) error {
	data, err := goroutineReadMode(ref, 256<<10, true)
	if err != nil {
		return err
	}
	return decodeGoroutineJSON(data, value)
}

// Generated evidence belongs to one current case. Source-test references are
// the sole explicit outside-root exception; hashes alone do not bind a run.
func goroutinePhysicalCaseRoot(root string) bool {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return false
	}
	physical, err := filepath.EvalSymlinks(root)
	if err != nil || physical != root {
		return false
	}
	info, err := os.Stat(root)
	return err == nil && info.IsDir()
}

func goroutineCaseReference(root string, ref goroutineReference) bool {
	if !goroutinePhysicalCaseRoot(root) || !filepath.IsAbs(ref.Path) || filepath.Clean(ref.Path) != ref.Path || filepath.Dir(ref.Path) != root {
		return false
	}
	physical, err := filepath.EvalSymlinks(ref.Path)
	return err == nil && physical == ref.Path
}

func goroutineCLIPaths(input, output string) (string, error) {
	root := filepath.Dir(input)
	if !goroutineCaseReference(root, goroutineReference{Path: input}) || !filepath.IsAbs(output) || filepath.Clean(output) != output || filepath.Dir(output) != root {
		return "", errors.New("candidate CLI requires one physical case root")
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		return "", errors.New("candidate output must be new")
	}
	return root, nil
}

func goroutineGeneratedRefsInCase(input goroutinePolicyInput, root string) bool {
	refs := []goroutineReference{input.Pre, input.Steady, input.LegacyPost, input.BaselineProfile, input.BeforePostProfile, input.Barrier, input.WorkloadReport}
	for _, post := range input.Posts {
		refs = append(refs, post.Capture, post.Profile)
	}
	for _, ref := range refs {
		if !goroutineCaseReference(root, ref) {
			return false
		}
	}
	return true
}

func writeGoroutineArtifact(path string, value any) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return errors.New("candidate output must be physical absolute path")
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil || parent != filepath.Dir(path) {
		return errors.New("candidate output parent differs")
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(append(data, '\n'))
	syncErr := file.Sync()
	closeErr := file.Close()
	return errors.Join(writeErr, syncErr, closeErr)
}

func validateGoroutineProfile(p goroutineProfile, baseline bool) error {
	if p.Schema != 1 || p.Protocol != goroutineCandidateProtocol || p.Scenario != "receipt_admission_lookup" || p.Family != "receipt_edge_delete" || p.ImageScope != "normal" || p.MeasurementProtocol != "none" || p.CPUSet != "0-3" || p.RuntimeUser != "501:20" ||
		!retainedHex(p.SourceCommit, 40) || !retainedHex(p.SourceTree, 40) || !strings.HasPrefix(p.ImageID, "sha256:") || !retainedHex(strings.TrimPrefix(p.ImageID, "sha256:"), 64) || !retainedHex(p.ConfigSHA256, 64) || !retainedHex(p.ScenarioSHA256, 64) || !retainedHex(p.ObserverSHA256, 64) || !strings.HasPrefix(p.ObserverGoVersion, "go1.") || len(p.Nodes) != 3 || p.MetricsTransport != "operator_docker_exec" || p.BridgePID <= 0 || len(p.BridgePorts) != 3 {
		return errors.New("normal profile identity missing or outside candidate scope")
	}
	endpoints, ids, pids := map[string]bool{}, map[string]bool{}, map[int64]bool{}
	for i, n := range p.Nodes {
		host, port, err := net.SplitHostPort(n.Endpoint)
		if err != nil || host != "localhost" || p.BridgePorts[i] < 1 || p.BridgePorts[i] > 65535 || port != strconv.Itoa(p.BridgePorts[i]) {
			return errors.New("metrics bridge endpoint mismatch")
		}
		if n.Endpoint == "" || endpoints[n.Endpoint] || !retainedHex(n.ContainerID, 64) || ids[n.ContainerID] || n.PID <= 0 || pids[n.PID] || len(n.ExpectedPeers) != 2 || n.ExpectedPeers[0] == n.ExpectedPeers[1] || n.ExpectedPeers[0] == "" || n.ExpectedPeers[1] == "" {
			return errors.New("three distinct target identities and two expected peer labels required")
		}
		if _, err := time.Parse(time.RFC3339Nano, n.StartedAt); err != nil {
			return errors.New("target start identity absent")
		}
		if baseline != (n.PassiveMetrics != nil) {
			return errors.New("baseline passive refs only")
		}
		endpoints[n.Endpoint] = true
		ids[n.ContainerID] = true
		pids[n.PID] = true
	}
	return nil
}

func sameGoroutineProfile(a, b goroutineProfile) bool {
	a.Nodes = append([]goroutineNodeProfile(nil), a.Nodes...)
	b.Nodes = append([]goroutineNodeProfile(nil), b.Nodes...)
	for i := range a.Nodes {
		a.Nodes[i].PassiveMetrics = nil
	}
	for i := range b.Nodes {
		b.Nodes[i].PassiveMetrics = nil
	}
	return reflect.DeepEqual(a, b)
}

func goroutineObserverMatches(p goroutineProfile) error {
	path, err := os.Executable()
	if err != nil {
		return errors.New("observer executable unavailable")
	}
	path, err = filepath.EvalSymlinks(path)
	if err != nil {
		return errors.New("observer executable nonphysical")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return errors.New("observer executable unreadable")
	}
	digest := sha256.Sum256(data)
	if hex.EncodeToString(digest[:]) != p.ObserverSHA256 || runtime.Version() != p.ObserverGoVersion {
		return errors.New("observer binary or Go version differs")
	}
	return nil
}

func validateGoroutinePassive(p goroutinePassive, n goroutineNodeProfile) bool {
	if p.SubscribeActive != 2 || p.ChangesActive != 0 || p.AntiEntropyCycles < 0 || len(p.Peers) != 2 {
		return false
	}
	for _, peer := range n.ExpectedPeers {
		if value, ok := p.Peers[peer]; !ok || value != 1 {
			return false
		}
	}
	return true
}

// The barrier proves only what the host harness explicitly waited. It is not a
// census or certification that all normal server queues/watchers have joined.
func validateGoroutineBarrier(b goroutineBarrier, caseRoot string) (bool, error) {
	if b.Schema != 1 || b.Protocol != goroutineCandidateProtocol || b.Scope != "declared_client_work_only_not_global_idle" || b.T0NS <= 0 || b.SemanticComplete == nil || b.PendingRPC == nil || len(b.OwnedWork) != 3 || len(b.WorkReports) != 2 {
		return false, errors.New("declared work barrier incomplete")
	}
	failed := !*b.SemanticComplete || *b.PendingRPC != 0
	seen := map[string]bool{}
	var producer goroutineOwnedWork
	for _, w := range b.OwnedWork {
		if (w.Kind != "producer" && w.Kind != "subscriber" && w.Kind != "chaos") || seen[w.Kind] || w.Declared == nil || w.Reaped == nil || w.PendingRPC == nil || w.ExitFailures == nil || *w.Declared < 0 || *w.Reaped < 0 || *w.PendingRPC < 0 || *w.ExitFailures < 0 {
			return false, errors.New("owned work coverage missing")
		}
		seen[w.Kind] = true
		if *w.Declared != *w.Reaped || *w.PendingRPC != 0 || *w.ExitFailures != 0 {
			failed = true
		}
		if w.Kind == "producer" {
			producer = w
		}
	}
	phases := map[string]goroutinePhaseReturn{}
	for _, work := range b.WorkReports {
		for _, ref := range []goroutineReference{work.Admission, work.Lookup, work.CoordinatorReturn} {
			if !goroutineCaseReference(caseRoot, ref) {
				return false, errors.New("phase evidence outside current case root")
			}
		}
		if work.Phase != "warmup" && work.Phase != "steady" {
			return false, errors.New("work report phases incomplete")
		}
		if _, ok := phases[work.Phase]; ok {
			return false, errors.New("duplicate phase")
		}
		var returned goroutinePhaseReturn
		if goroutineDecode(work.CoordinatorReturn, &returned) != nil || returned.Schema != 1 || returned.Protocol != goroutineCandidateProtocol || returned.Scope != "synchronous_receipt_phase_only" || returned.Phase != work.Phase || returned.StartedNS <= 0 || returned.FinishedNS <= returned.StartedNS || returned.FinishedNS > b.T0NS || returned.ExitCode == nil || returned.CoordinatorReturned == nil {
			return false, errors.New("phase coordinator return evidence incomplete")
		}
		phases[work.Phase] = returned
		if *returned.ExitCode != 0 || !*returned.CoordinatorReturned {
			failed = true
		}
		var a, q summary
		for _, row := range []struct {
			ref   goroutineReference
			value *summary
		}{{work.Admission, &a}, {work.Lookup, &q}} {
			data, err := goroutineRead(row.ref, 1<<20)
			if err != nil {
				return false, errors.New("work report reference incomplete")
			}
			if decodeGoroutineJSON(data, row.value) != nil {
				return false, errors.New("work report invalid")
			}
		}
		if a.Count <= 0 || q.Count != a.Count || len(a.StatusCodeDistribution) != 1 || len(q.StatusCodeDistribution) != 1 || a.StatusCodeDistribution["OK"] != a.Count || q.StatusCodeDistribution["OK"] != q.Count {
			failed = true
		}
	}
	if len(phases) != 2 || phases["warmup"].FinishedNS > phases["steady"].StartedNS {
		return false, errors.New("phase return ordering differs")
	}
	if producer.Declared == nil || *producer.Declared != int64(len(phases)) || *producer.Reaped != int64(len(phases)) {
		failed = true
	}
	return failed, nil
}

func evaluateGoroutinePolicy(input goroutinePolicyInput) goroutinePolicyReport {
	return evaluateGoroutinePolicyInCase(input, filepath.Dir(input.Pre.Path))
}

func evaluateGoroutinePolicyInCase(input goroutinePolicyInput, caseRoot string) goroutinePolicyReport {
	r := goroutinePolicyReport{Schema: 1, Protocol: goroutineCandidateProtocol, CandidateOnly: true, SampledPeakLimit: 128, PostResidualLimit: 15, OperationalVerdict: "incomplete", ResidualVerdict: "incomplete", Verdict: "incomplete", Scope: "sampled_normal_operational_and_declared_client_residual_screen_not_leak_free", Inputs: input}
	missing := func(reason string) goroutinePolicyReport {
		r.MissingPremises = append(r.MissingPremises, reason)
		if len(r.Failures) > 0 {
			r.Verdict = "fail"
		}
		return r
	}
	if input.Schema != 1 || input.Protocol != goroutineCandidateProtocol || len(input.Posts) != 2 || len(input.OwnerEvidence) == 0 {
		return missing("candidate inputs or source-lifecycle evidence absent")
	}
	if !goroutineGeneratedRefsInCase(input, caseRoot) {
		return missing("generated references outside physical current case root or missing")
	}
	var profile, before goroutineProfile
	if goroutineDecode(input.BaselineProfile, &profile) != nil || validateGoroutineProfile(profile, true) != nil {
		return missing("baseline normal profile incomplete")
	}
	if goroutineDecode(input.BeforePostProfile, &before) != nil || validateGoroutineProfile(before, false) != nil || !sameGoroutineProfile(profile, before) {
		return missing("post identity/configuration differs from baseline")
	}
	for _, ref := range input.OwnerEvidence {
		if _, err := goroutineRead(ref, 1<<20); err != nil {
			return missing("source-lifecycle reference unavailable")
		}
	}
	var barrier goroutineBarrier
	if goroutineDecode(input.Barrier, &barrier) != nil {
		return missing("t0 barrier unavailable")
	}
	workFailed, err := validateGoroutineBarrier(barrier, caseRoot)
	if err != nil {
		return missing("t0 declared-work premises incomplete")
	}
	var workload struct {
		Verdict string `json:"verdict"`
	}
	data, err := goroutineRead(input.WorkloadReport, 1<<20)
	if err != nil || json.Unmarshal(data, &workload) != nil || workload.Verdict == "" {
		return missing("workload result absent")
	}
	if workFailed || workload.Verdict != "pass" {
		r.Failures = append(r.Failures, "declared work did not complete, join or satisfy workload checks")
	}
	if workFailed {
		r.ResidualVerdict = "fail"
	}
	var pre, legacy []goroutineSnapshot
	var steady steadyMetricsReport
	data, err = goroutineRead(input.Pre, 1<<20)
	if err != nil || decodeGoroutineJSON(data, &pre) != nil || len(pre) != 3 {
		return missing("original PRE absent")
	}
	data, err = goroutineRead(input.LegacyPost, 1<<20)
	if err != nil || decodeGoroutineJSON(data, &legacy) != nil || len(legacy) != 3 {
		return missing("unchanged legacy memory POST absent")
	}
	data, err = goroutineRead(input.Steady, 1<<20)
	if err != nil || decodeGoroutineJSON(data, &steady) != nil {
		return missing("original steady samples absent")
	}
	cfg := steadyMetricsConfig{interval: 5 * time.Second}
	for _, n := range profile.Nodes {
		cfg.endpoints = append(cfg.endpoints, metricsEndpoint{endpoint: n.Endpoint})
	}
	if validateSteadyMetrics(steady, cfg, 45*time.Second) != nil {
		return missing("five-second steady coverage incomplete")
	}
	baselinePassive := make([]goroutinePassive, 3)
	for i, n := range profile.Nodes {
		if pre[i].Endpoint != n.Endpoint || pre[i].Goroutines == nil || *pre[i].Goroutines <= 0 || legacy[i].Endpoint != n.Endpoint || legacy[i].Goroutines == nil || *legacy[i].Goroutines <= 0 {
			return missing("PRE or legacy POST target correspondence incomplete")
		}
		if !goroutineCaseReference(caseRoot, *n.PassiveMetrics) {
			return missing("PRE passive evidence outside current case root")
		}
		data, err := goroutineRead(*n.PassiveMetrics, 1<<20)
		if err != nil {
			return missing("same-scrape PRE passive reference absent")
		}
		p, err := parseGoroutinePassive(data)
		if err != nil || !validateGoroutinePassive(p, n) {
			return missing("declared PRE peer/stream profile unavailable")
		}
		baselinePassive[i] = p
		row := goroutinePolicyReplica{Endpoint: n.Endpoint, Baseline: *pre[i].Goroutines, SampledPeak: max(*pre[i].Goroutines, *legacy[i].Goroutines), LegacyPost: *legacy[i].Goroutines, LegacyDelta: *legacy[i].Goroutines - *pre[i].Goroutines}
		for _, s := range steady.Samples {
			row.SampledPeak = max(row.SampledPeak, s.Replicas[i].Goroutines)
		}
		r.Replicas = append(r.Replicas, row)
		if row.SampledPeak > 128 && r.OperationalVerdict != "fail" {
			r.OperationalVerdict = "fail"
			r.Failures = append(r.Failures, "normal sampled whole-process ceiling exceeded")
		}
		if row.LegacyDelta > 15 && r.ResidualVerdict != "fail" {
			r.ResidualVerdict = "fail"
			r.Failures = append(r.Failures, "original post-cooldown residual exceeded")
		}
	}
	for index, post := range input.Posts {
		offset := 5 + index*5
		if post.OffsetSeconds != offset || post.StartNS < barrier.T0NS || post.StartNS-barrier.T0NS < int64(offset)*int64(time.Second) || post.EndNS < post.StartNS || post.EndNS-barrier.T0NS > int64(offset+3)*int64(time.Second) {
			return missing("fixed post window missing, late, duplicate or unordered")
		}
		var metadata goroutineProfile
		var capture goroutinePostCapture
		if goroutineDecode(post.Profile, &metadata) != nil || validateGoroutineProfile(metadata, false) != nil || !sameGoroutineProfile(profile, metadata) {
			return missing("post target restarted or profile changed")
		}
		if goroutineDecode(post.Capture, &capture) != nil || capture.Schema != 1 || capture.Protocol != goroutineCandidateProtocol || !capture.CandidateOnly || capture.Qualification || capture.ReleaseAdmission || capture.OffsetSeconds != offset || capture.Verdict != "captured" || capture.Failure != "" || capture.DurationNS < 0 || capture.DurationNS > int64(3*time.Second) || capture.DurationNS > post.EndNS-post.StartNS || len(capture.Replicas) != 3 || capture.Profile != input.BeforePostProfile {
			return missing("post collector flight incomplete or foreign")
		}
		if _, err := time.Parse(time.RFC3339Nano, capture.StartedUTC); err != nil {
			return missing("post capture clock label absent")
		}
		for i, v := range capture.Replicas {
			if v.Runtime.Endpoint != profile.Nodes[i].Endpoint || v.Runtime.Goroutines <= 0 || !validateGoroutinePassive(v.Passive, profile.Nodes[i]) || v.Passive.AntiEntropyCycles < baselinePassive[i].AntiEntropyCycles {
				return missing("post passive profile or endpoint correspondence differs")
			}
			r.Replicas[i].SampledPeak = max(r.Replicas[i].SampledPeak, v.Runtime.Goroutines)
			if index == 0 {
				r.Replicas[i].Post5 = v.Runtime.Goroutines
				r.Replicas[i].Delta5 = v.Runtime.Goroutines - r.Replicas[i].Baseline
			} else {
				r.Replicas[i].Post10 = v.Runtime.Goroutines
				r.Replicas[i].Delta10 = v.Runtime.Goroutines - r.Replicas[i].Baseline
			}
			if r.Replicas[i].SampledPeak > 128 && r.OperationalVerdict != "fail" {
				r.OperationalVerdict = "fail"
				r.Failures = append(r.Failures, "normal sampled whole-process ceiling exceeded")
			}
			if v.Runtime.Goroutines-r.Replicas[i].Baseline > 15 && r.ResidualVerdict != "fail" {
				r.ResidualVerdict = "fail"
				r.Failures = append(r.Failures, "fixed post residual exceeded")
			}
		}
	}
	if r.OperationalVerdict != "fail" {
		r.OperationalVerdict = "pass"
	}
	if r.ResidualVerdict != "fail" {
		r.ResidualVerdict = "pass"
	}
	r.Verdict = "pass"
	if len(r.Failures) > 0 {
		r.Verdict = "fail"
	}
	return r
}

func runGoroutinePolicyEvaluation(args []string) int {
	fs := flag.NewFlagSet("evaluate-goroutine-policy", flag.ContinueOnError)
	input := fs.String("input", "", "candidate-only typed input")
	out := fs.String("out", "", "candidate-only JSON report")
	if fs.Parse(args) != nil || *input == "" || *out == "" {
		return 2
	}
	caseRoot, err := goroutineCLIPaths(*input, *out)
	if err != nil {
		return 2
	}
	data, err := readRetainedFile(*input, 256<<10)
	var value goroutinePolicyInput
	if err != nil || decodeGoroutineJSON(data, &value) != nil {
		return 2
	}
	report := evaluateGoroutinePolicyInCase(value, caseRoot)
	var profile goroutineProfile
	if goroutineCaseReference(caseRoot, value.BaselineProfile) && (goroutineDecode(value.BaselineProfile, &profile) != nil || goroutineObserverMatches(profile) != nil) {
		report.Verdict = "incomplete"
		report.MissingPremises = append(report.MissingPremises, "actual observer executable/Go binding differs")
	}
	if err := writeGoroutineArtifact(*out, report); err != nil {
		fmt.Fprintln(os.Stderr, "candidate goroutine report write failed")
		return 1
	}
	if report.Verdict != "pass" {
		return 1
	}
	return 0
}
