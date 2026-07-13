// searchprobe seeds and verifies the benchmark's deterministic search fixture.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	client "github.com/anaregdesign/lantern/sdks/go"
)

const (
	liveKey    = "bench:search:probe:live"
	deletedKey = "bench:search:probe:deleted"
	expiredKey = "bench:search:probe:expired"
	alphaKey   = "bench:search:probe:01"
	betaKey    = "bench:search:probe:02"
	gammaKey   = "bench:search:probe:03"
)

type writer interface {
	PutVertices(context.Context, []client.VertexInput) error
	DeleteVertex(context.Context, string) (bool, error)
}

type searcher interface {
	SearchVertices(context.Context, string, ...client.SearchOption) ([]client.SearchHit, error)
	SearchVerticesPage(context.Context, string, ...client.SearchOption) (client.SearchPage, error)
}

type check struct {
	Name     string
	Run      func(context.Context, searcher) ([]client.SearchHit, error)
	Want     []string
	Top      []string
	Contains []string
	Excludes []string
}

type replicaReport struct {
	Endpoint string `json:"endpoint"`
	Checks   int    `json:"checks"`
	Verdict  string `json:"verdict"`
}

type probeReport struct {
	Phase    string          `json:"phase"`
	Verdict  string          `json:"verdict"`
	Replicas []replicaReport `json:"replicas"`
}

func main() {
	mode := flag.String("mode", "", "seed or verify")
	phase := flag.String("phase", "", "report phase for verify mode (pre or post)")
	endpointsFlag := flag.String("endpoints", "http://localhost:6380", "comma-separated Lantern h2c endpoints")
	reportPath := flag.String("report", "", "write a bounded semantic report to this path")
	timeout := flag.Duration("timeout", 90*time.Second, "maximum time for replication and TTL convergence")
	flag.Parse()

	endpoints := strings.Split(*endpointsFlag, ",")
	if len(endpoints) == 0 || endpoints[0] == "" || *reportPath == "" {
		fatalf("at least one endpoint and -report are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	switch *mode {
	case "seed":
		lantern, err := client.NewLantern(endpoints[0])
		if err != nil {
			fatalf("dial writer: %v", err)
		}
		if err := seed(ctx, lantern, time.Now()); err != nil {
			_ = lantern.Close()
			fatalf("seed: %v", err)
		}
		_ = lantern.Close()
		writeReport(*reportPath, probeReport{Phase: "seed", Verdict: "pass"})
	case "verify":
		if *phase != "pre" && *phase != "post" {
			fatalf("verify mode requires -phase pre or -phase post")
		}
		rep := probeReport{Phase: *phase, Verdict: "pass"}
		for _, endpoint := range endpoints {
			lantern, err := client.NewLantern(endpoint)
			if err != nil {
				rep.Verdict = "fail"
				rep.Replicas = append(rep.Replicas, replicaReport{Endpoint: endpoint, Verdict: "fail"})
				continue
			}
			err = waitForChecks(ctx, lantern)
			_ = lantern.Close()
			if err != nil {
				rep.Verdict = "fail"
				rep.Replicas = append(rep.Replicas, replicaReport{Endpoint: endpoint, Checks: searchCheckCount(), Verdict: "fail"})
				continue
			}
			rep.Replicas = append(rep.Replicas, replicaReport{Endpoint: endpoint, Checks: searchCheckCount(), Verdict: "pass"})
		}
		writeReport(*reportPath, rep)
		if rep.Verdict != "pass" {
			fatalf("semantic verification failed; see bounded report")
		}
	default:
		fatalf("-mode must be seed or verify")
	}
}

func seed(ctx context.Context, w writer, now time.Time) error {
	inputs := []client.VertexInput{
		{Key: liveKey, Value: "persistentbeacon lantern sentinel"},
		{Key: deletedKey, Value: "discardedtoken lantern sentinel"},
		{Key: expiredKey, Value: "fleetingtoken lantern sentinel", Expiration: now.Add(10 * time.Second)},
		{Key: alphaKey, Value: "quick brown fox ember"},
		{Key: betaKey, Value: "quick blue fox embers"},
		{Key: gammaKey, Value: "quick brown dog cinder"},
	}
	for i := range 9 {
		inputs = append(inputs, client.VertexInput{
			Key:   fmt.Sprintf("bench:search:page:%02d", i),
			Value: "deeppaginationbeacon",
		})
	}
	if err := w.PutVertices(ctx, inputs); err != nil {
		return err
	}
	existed, err := w.DeleteVertex(ctx, deletedKey)
	if err != nil {
		return err
	}
	if !existed {
		return errors.New("delete sentinel was not written")
	}
	return nil
}

func searchCheckCount() int { return len(searchChecks()) + 1 }

func searchChecks() []check {
	return []check{
		{Name: "live_sentinel", Run: query("persistentbeacon"), Contains: []string{liveKey}},
		{Name: "deleted_sentinel", Run: query("discardedtoken"), Excludes: []string{deletedKey}},
		{Name: "expired_sentinel", Run: query("fleetingtoken"), Excludes: []string{expiredKey}},
		{Name: "exact", Run: query("quick"), Want: []string{alphaKey, betaKey, gammaKey}},
		{Name: "key_prefix", Run: query("quick", client.WithSearchPrefix(betaKey)), Want: []string{betaKey}},
		{Name: "phrase", Run: query("quick brown", client.WithPhrase()), Want: []string{alphaKey, gammaKey}},
		{Name: "fuzzy_1", Run: query("ember", client.WithFuzziness(1)), Top: []string{alphaKey, betaKey}},
		{Name: "fuzzy_2", Run: query("embee", client.WithFuzziness(2)), Top: []string{alphaKey, betaKey}},
		{Name: "prefix_terms", Run: query("embe", client.WithPrefixTerms()), Top: []string{alphaKey, betaKey}},
		{Name: "match_all", Run: query("quick fox", client.WithMatchMode(client.MatchAll)), Want: []string{alphaKey, betaKey}},
		{Name: "min_should", Run: query("quick brown fox", client.WithMatchMode(client.MatchMinShould), client.WithMinShouldMatch(2)), Want: []string{alphaKey, gammaKey, betaKey}},
	}
}

func query(q string, opts ...client.SearchOption) func(context.Context, searcher) ([]client.SearchHit, error) {
	return func(ctx context.Context, s searcher) ([]client.SearchHit, error) {
		return s.SearchVertices(ctx, q, append([]client.SearchOption{client.WithSearchLimit(20)}, opts...)...)
	}
}

func waitForChecks(ctx context.Context, s searcher) error {
	var last error
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("semantic checks did not converge: %v: %w", last, err)
		}
		last = verifyOnce(ctx, s)
		if last == nil {
			return nil
		}
		select {
		case <-time.After(250 * time.Millisecond):
		case <-ctx.Done():
			return fmt.Errorf("semantic checks did not converge: %v: %w", last, ctx.Err())
		}
	}
}

func verifyOnce(ctx context.Context, s searcher) error {
	for _, c := range searchChecks() {
		hits, err := c.Run(ctx, s)
		if err != nil {
			return fmt.Errorf("%s: %w", c.Name, err)
		}
		got := make([]string, len(hits))
		for i, hit := range hits {
			got[i] = hit.Key
		}
		if c.Want != nil && !slices.Equal(got, c.Want) {
			return fmt.Errorf("%s: ordered keys = %v, want %v", c.Name, got, c.Want)
		}
		if len(got) < len(c.Top) || !slices.Equal(got[:len(c.Top)], c.Top) {
			return fmt.Errorf("%s: top ordered keys do not match", c.Name)
		}
		for _, key := range c.Contains {
			if !slices.Contains(got, key) {
				return fmt.Errorf("%s: required key is absent", c.Name)
			}
		}
		for _, key := range c.Excludes {
			if slices.Contains(got, key) {
				return fmt.Errorf("%s: forbidden key is present", c.Name)
			}
		}
	}
	return verifyDeepPagination(ctx, s)
}

func verifyDeepPagination(ctx context.Context, s searcher) error {
	want := make([]string, 9)
	for i := range want {
		want[i] = fmt.Sprintf("bench:search:page:%02d", i)
	}
	var got []string
	var cursor []byte
	pages := 0
	for {
		page, err := s.SearchVerticesPage(
			ctx,
			"deeppaginationbeacon",
			client.WithSearchLimit(2),
			client.WithFullVertex(),
			client.WithSearchCursor(cursor),
		)
		if err != nil {
			return fmt.Errorf("deep_pagination page %d: %w", pages+1, err)
		}
		pages++
		if pages == 1 && (!page.Truncated || len(page.NextCursor) == 0 || page.EffectiveLimit != 2) {
			return errors.New("deep_pagination first page omitted truthful continuation metadata")
		}
		for _, hit := range page.Hits {
			if hit.ProjectionStatus != client.SearchHitProjectionSnapshot || hit.Vertex == nil {
				return errors.New("deep_pagination FULL_VERTEX hit is not a proven snapshot")
			}
			value, valueErr := client.StringValue(hit.Vertex)
			if valueErr != nil || value != "deeppaginationbeacon" {
				return errors.New("deep_pagination FULL_VERTEX snapshot value changed")
			}
			got = append(got, hit.Key)
		}
		if len(page.NextCursor) == 0 {
			if page.Truncated || page.ContinuationLimited {
				return errors.New("deep_pagination ended with an incomplete bounded tail")
			}
			break
		}
		cursor = page.NextCursor
	}
	if pages < 5 || !slices.Equal(got, want) {
		return fmt.Errorf("deep_pagination pages=%d ordered key count=%d", pages, len(got))
	}
	return nil
}

func writeReport(path string, rep probeReport) {
	encoded, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		fatalf("encode report: %v", err)
	}
	if err := os.WriteFile(path, append(encoded, '\n'), 0o644); err != nil {
		fatalf("write report: %v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "searchprobe: "+format+"\n", args...)
	os.Exit(1)
}
