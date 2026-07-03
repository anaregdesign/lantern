package relevance

import (
	"encoding/json"
	"fmt"
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
	// Runs holds one entry per (corpus, analyzer) pairing, keyed by a
	// human-readable name such as "en-standard" or "ja-kuromoji".
	Runs map[string]BaselineRun `json:"runs"`
}

// LoadBaselineRuns reads a pinned runs file. Callers that treat the baseline
// as optional (it may not be regenerated in every checkout) should check
// os.IsNotExist on the error and skip rather than fail.
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

// Evaluate replays one recorded run against its corpus and returns the same
// Metrics Lantern pipelines are scored with. Queries missing from the run
// contribute zero to every metric, matching an engine that returned nothing.
func (r BaselineRun) Evaluate(c Corpus) Metrics {
	return Evaluate(c, func(q Query) []string { return r.Results[q.ID] })
}
