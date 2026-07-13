package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	client "github.com/anaregdesign/lantern/sdks/go"
)

type fakeSearcher struct {
	responses [][]client.SearchHit
	call      int
}

func (f *fakeSearcher) SearchVertices(context.Context, string, ...client.SearchOption) ([]client.SearchHit, error) {
	if f.call >= len(f.responses) {
		return []client.SearchHit{}, nil
	}
	response := f.responses[f.call]
	f.call++
	return response, nil
}

type fakeWriter struct {
	inputs     []client.VertexInput
	deletedKey string
}

func (f *fakeWriter) PutVertices(_ context.Context, inputs []client.VertexInput) error {
	f.inputs = append([]client.VertexInput(nil), inputs...)
	return nil
}

func (f *fakeWriter) DeleteVertex(_ context.Context, key string) (bool, error) {
	f.deletedKey = key
	return true, nil
}

func TestVerifyOnce(t *testing.T) {
	t.Run("expected semantic matrix passes", func(t *testing.T) {
		checks := searchChecks()
		responses := make([][]client.SearchHit, 0, len(checks))
		for _, c := range checks {
			keys := append(append(append([]string(nil), c.Want...), c.Top...), c.Contains...)
			hits := make([]client.SearchHit, len(keys))
			for i, key := range keys {
				hits[i].Key = key
			}
			responses = append(responses, hits)
		}
		if err := verifyOnce(context.Background(), &fakeSearcher{responses: responses}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("HTTP OK with empty hits fails closed", func(t *testing.T) {
		err := verifyOnce(context.Background(), &fakeSearcher{})
		if err == nil || !strings.Contains(err.Error(), "live_sentinel") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestWaitForChecksPreservesSemanticFailureAtDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	err := waitForChecks(ctx, &fakeSearcher{})
	if err == nil || !strings.Contains(err.Error(), "live_sentinel") {
		t.Fatalf("error = %v", err)
	}
}

func TestFailureReportIsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "semantic_pre.json")
	writeReport(path, probeReport{
		Phase:   "pre",
		Verdict: "fail",
		Replicas: []replicaReport{{
			Endpoint: "http://localhost:6380",
			Checks:   len(searchChecks()),
			Verdict:  "fail",
		}},
	})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{liveKey, "persistentbeacon", "quick brown"} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("bounded report contains fixture data %q: %s", forbidden, raw)
		}
	}
}

func TestSeedWritesLifecycleSentinels(t *testing.T) {
	now := time.Unix(1000, 0)
	w := &fakeWriter{}
	if err := seed(context.Background(), w, now); err != nil {
		t.Fatal(err)
	}
	if len(w.inputs) != 6 || w.deletedKey != deletedKey {
		t.Fatalf("inputs = %d, deleted = %q", len(w.inputs), w.deletedKey)
	}
	var expiration time.Time
	for _, input := range w.inputs {
		if input.Key == expiredKey {
			expiration = input.Expiration
		}
	}
	if want := now.Add(10 * time.Second); !expiration.Equal(want) {
		t.Fatalf("expiration = %v, want %v", expiration, want)
	}
}
