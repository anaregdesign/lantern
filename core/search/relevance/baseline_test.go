package relevance

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBaselineRuns(t *testing.T) {
	t.Run("RoundTrip", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "runs.json")
		blob := `{
			"engine": "lucene-9.11.1",
			"generated": "2026-07-03T00:00:00Z",
			"runs": {
				"en-standard": {
					"corpus": "en",
					"analyzer": "StandardAnalyzer",
					"results": {"q1": ["a", "b"]}
				}
			}
		}`
		if err := os.WriteFile(path, []byte(blob), 0o644); err != nil {
			t.Fatal(err)
		}
		runs, err := LoadBaselineRuns(path)
		if err != nil {
			t.Fatalf("LoadBaselineRuns = %v", err)
		}
		run, ok := runs.Runs["en-standard"]
		if !ok || run.Corpus != "en" || run.Analyzer != "StandardAnalyzer" {
			t.Fatalf("parsed run = %+v", run)
		}
		if got := run.Results["q1"]; len(got) != 2 || got[0] != "a" {
			t.Fatalf("parsed results = %v", got)
		}
	})

	t.Run("MissingFileIsNotExist", func(t *testing.T) {
		_, err := LoadBaselineRuns(filepath.Join(t.TempDir(), "absent.json"))
		if !os.IsNotExist(err) {
			t.Fatalf("err = %v, want os.IsNotExist", err)
		}
	})

	t.Run("MalformedJSON", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "bad.json")
		if err := os.WriteFile(path, []byte("{"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadBaselineRuns(path); err == nil {
			t.Fatal("malformed runs file accepted")
		}
	})
}

func TestBaselineRunEvaluate(t *testing.T) {
	c := Corpus{
		Name: "unit",
		Queries: []Query{
			{ID: "q1", Text: "one", Qrels: map[string]int{"a": 3}},
			{ID: "q2", Text: "two", Qrels: map[string]int{"b": 3}},
		},
	}
	// q1 replays a perfect ranking; q2 is absent from the run and must count
	// as a full miss, exactly like an engine that returned nothing.
	run := BaselineRun{Corpus: "unit", Results: map[string][]string{"q1": {"a"}}}
	m := run.Evaluate(c)
	if !almostEqual(m.NDCG10, 0.5) || !almostEqual(m.MRR, 0.5) || !almostEqual(m.Recall50, 0.5) {
		t.Fatalf("replayed metrics = %+v, want all 0.5", m)
	}
}
