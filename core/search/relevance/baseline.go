package relevance

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"maps"
	"os"
)

// BaselineRunsFile is the repo-relative fixture (within this package) holding
// the pinned Lucene rankings. It is produced offline by testbed/lucene-baseline
// (CI never runs a JVM) and committed; the parity gate replays it through the
// same metric functions Lantern is scored with, so both sides share one metric
// implementation and the numbers cannot drift apart formula-wise.
const BaselineRunsFile = "testdata/lucene_runs.json"

// BaselineRun is one engine configuration's recorded rankings over one corpus:
// for every query ID, the top-EvalDepth document IDs, best first.
type BaselineRun struct {
	// Corpus names the golden corpus the run was executed against.
	Corpus string `json:"corpus"`
	// Analyzer describes the engine configuration (e.g. "StandardAnalyzer").
	Analyzer string `json:"analyzer"`
	// Blocking marks the stock comparator that defines the required floor.
	// Non-blocking runs are explicit stretch references.
	Blocking bool `json:"blocking"`
	// Results maps query ID → ranked document IDs (top EvalDepth, best first).
	Results map[string][]string `json:"results"`
}

// BaselineRuns is the pinned output of the offline Lucene runner.
type BaselineRuns struct {
	// Engine records the engine and version the runs were produced with, e.g.
	// "lucene-9.11.1", so a refreshed pin documents what it was measured on.
	Engine string `json:"engine"`
	// Generated is the RFC3339 timestamp of the run, informational only.
	Generated string `json:"generated"`
	// Provenance binds the rankings to the exact corpora and Lantern contract.
	Provenance BaselineProvenance `json:"provenance"`
	// Runs holds one entry per (corpus, analyzer) pairing, keyed by a
	// human-readable name such as "en-standard" or "ja-kuromoji".
	Runs map[string]BaselineRun `json:"runs"`
}

// BaselineProvenance is the reproducibility envelope emitted by the offline
// runner. A stale/missing field is a CI failure, never a skipped comparison.
type BaselineProvenance struct {
	CorpusSHA256          map[string]string `json:"corpus_sha256"`
	ProjectedFieldsSHA256 string            `json:"projected_fields_sha256"`
	FixtureFormat         string            `json:"fixture_format"`
	ProjectionVersion     string            `json:"projection_version"`
	AnalyzerVersion       string            `json:"analyzer_version"`
	ScorerConfig          string            `json:"scorer_config"`
	GenerationCommand     string            `json:"generation_command"`
}

const (
	BaselineEngine            = "lucene-9.11.1"
	BaselineFixtureFormat     = "typed-string-vertex-fields-v1"
	BaselineProjectionVersion = "vertex-fields-v2"
	BaselineAnalyzerVersion   = "script-aware-v2"
	BaselineScorerConfig      = "field-weighted-bm25:k1=1.2,b=0.75,key=1.75,value=1,gram=0.2,proximity=0.3"
	BaselineGenerationCommand = "testbed/lucene-baseline/run.sh"
)

// LoadBaselineRuns reads a pinned runs file. Required gates treat any error as
// fatal; the committed baseline is part of the source contract.
func LoadBaselineRuns(path string) (BaselineRuns, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return BaselineRuns{}, err
	}
	var runs BaselineRuns
	if err := json.Unmarshal(raw, &runs); err != nil {
		return BaselineRuns{}, fmt.Errorf("relevance: parsing %s: %w", path, err)
	}
	return runs, nil
}

// CorpusSHA256 returns the lowercase SHA-256 of each canonical embedded corpus
// file. Hashing raw bytes makes whitespace or document edits invalidate the
// pinned artifact until it is deliberately regenerated.
func CorpusSHA256() (map[string]string, error) {
	hashes := make(map[string]string, len(corporaFiles))
	for _, name := range corporaFiles {
		raw, err := fs.ReadFile(corporaFS, name)
		if err != nil {
			return nil, fmt.Errorf("relevance: hashing %s: %w", name, err)
		}
		sum := sha256.Sum256(raw)
		hashes[name] = fmt.Sprintf("%x", sum)
	}
	return hashes, nil
}

// ProjectedFieldsSHA256 binds the provider-generated field artifact consumed
// by the Lucene runner.
func ProjectedFieldsSHA256() (string, error) {
	raw, err := fs.ReadFile(corporaFS, "testdata/projected_fields.json")
	if err != nil {
		return "", fmt.Errorf("relevance: hashing projected fields: %w", err)
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum), nil
}

// ValidateProvenance rejects a baseline generated from any other corpus or
// production projection/analyzer/scorer contract.
func (r BaselineRuns) ValidateProvenance() error {
	if r.Engine != BaselineEngine {
		return fmt.Errorf("relevance: baseline engine = %q, want %q", r.Engine, BaselineEngine)
	}
	wantHashes, err := CorpusSHA256()
	if err != nil {
		return err
	}
	p := r.Provenance
	if !maps.Equal(p.CorpusSHA256, wantHashes) {
		return fmt.Errorf("relevance: baseline corpus SHA-256 mismatch: got %v, want %v", p.CorpusSHA256, wantHashes)
	}
	wantProjected, err := ProjectedFieldsSHA256()
	if err != nil {
		return err
	}
	if p.ProjectedFieldsSHA256 != wantProjected {
		return fmt.Errorf("relevance: baseline projected fields SHA-256 = %q, want %q", p.ProjectedFieldsSHA256, wantProjected)
	}
	checks := []struct{ name, got, want string }{
		{"fixture_format", p.FixtureFormat, BaselineFixtureFormat},
		{"projection_version", p.ProjectionVersion, BaselineProjectionVersion},
		{"analyzer_version", p.AnalyzerVersion, BaselineAnalyzerVersion},
		{"scorer_config", p.ScorerConfig, BaselineScorerConfig},
		{"generation_command", p.GenerationCommand, BaselineGenerationCommand},
	}
	for _, check := range checks {
		if check.got != check.want {
			return fmt.Errorf("relevance: baseline %s = %q, want %q", check.name, check.got, check.want)
		}
	}
	wantRuns := map[string]struct {
		corpus   string
		analyzer string
		blocking bool
	}{
		"en-standard":    {corpus: "en", analyzer: "StandardAnalyzer", blocking: true},
		"en-english":     {corpus: "en", analyzer: "EnglishAnalyzer", blocking: true},
		"ja-cjk":         {corpus: "ja", analyzer: "CJKAnalyzer", blocking: true},
		"ja-kuromoji":    {corpus: "ja", analyzer: "JapaneseAnalyzer"},
		"mixed-cjk":      {corpus: "mixed", analyzer: "CJKAnalyzer", blocking: true},
		"mixed-kuromoji": {corpus: "mixed", analyzer: "JapaneseAnalyzer"},
	}
	if len(r.Runs) != len(wantRuns) {
		return fmt.Errorf("relevance: baseline run count = %d, want %d", len(r.Runs), len(wantRuns))
	}
	for name, want := range wantRuns {
		got, ok := r.Runs[name]
		if !ok {
			return fmt.Errorf("relevance: baseline missing run %q", name)
		}
		if got.Corpus != want.corpus || got.Analyzer != want.analyzer || got.Blocking != want.blocking {
			return fmt.Errorf("relevance: baseline run %q metadata = (%q, %q, blocking=%t), want (%q, %q, blocking=%t)", name, got.Corpus, got.Analyzer, got.Blocking, want.corpus, want.analyzer, want.blocking)
		}
	}
	return nil
}

// Evaluate replays one recorded run against its corpus and returns the same
// Metrics Lantern pipelines are scored with. Queries missing from the run
// contribute zero to every metric, matching an engine that returned nothing.
func (r BaselineRun) Evaluate(c Corpus) Metrics {
	return Evaluate(c, func(q Query) []string { return r.Results[q.ID] })
}
