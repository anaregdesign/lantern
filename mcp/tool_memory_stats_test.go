package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// vexp builds a string-valued vertex carrying an absolute expiration, for
// exercising the remaining-life histogram.
func vexp(key, val string, exp time.Time) *client.Vertex {
	return &pb.Vertex{
		Key:        key,
		Value:      &pb.Vertex_String_{String_: val},
		Expiration: timestamppb.New(exp),
	}
}

// scopeCountsByName indexes a result's per-scope breakdown for terse asserts.
func scopeCountsByName(out memoryStatsOutput) map[string]uint64 {
	m := make(map[string]uint64, len(out.Scopes))
	for _, s := range out.Scopes {
		m[s.Scope] = s.Count
	}
	return m
}

// bucketCountsByName indexes a result's TTL histogram for terse asserts.
func bucketCountsByName(out memoryStatsOutput) map[string]int {
	m := make(map[string]int, len(out.TTLBuckets))
	for _, b := range out.TTLBuckets {
		m[b.Bucket] = b.Count
	}
	return m
}

func TestMemoryStats_CountsAndScopeBreakdown(t *testing.T) {
	h := newTestHarness(t)
	// Radix totals: whole keyspace plus the four recognized scope heads.
	h.fake.countByPrefixFn = func(_ context.Context, prefix string) (uint64, error) {
		switch prefix {
		case "":
			return 10, nil
		case "session.":
			return 2, nil
		case "task.":
			return 1, nil
		case "project.":
			return 4, nil
		case "user.":
			return 3, nil
		}
		return 0, nil
	}
	h.fake.scanVerticesFn = singlePage(
		vstr("user.identity.role", "engineer"),
		vstr("project.lantern.stack", "go"),
	)
	h.fake.scanEdgesFn = func(_ context.Context, _ ...client.EdgeScanOption) ([]*client.Edge, []byte, error) {
		return []*client.Edge{
			{Tail: "user.identity.role", Head: "project.lantern.stack", Weight: 0.9},
			{Tail: "project.lantern.stack", Head: "user.identity.role", Weight: 0.4},
		}, nil, nil
	}

	res := h.call(t, "memory_stats", map[string]any{})
	if res.IsError {
		t.Fatalf("IsError = true: %s", contentText(res))
	}
	var out memoryStatsOutput
	structuredAs(t, res, &out)

	if out.Prefix != "" {
		t.Fatalf("Prefix = %q, want empty", out.Prefix)
	}
	if out.Vertices != 10 {
		t.Fatalf("Vertices = %d, want 10", out.Vertices)
	}
	if out.Edges != 2 {
		t.Fatalf("Edges = %d, want 2", out.Edges)
	}
	by := scopeCountsByName(out)
	for scope, want := range map[string]uint64{"session": 2, "task": 1, "project": 4, "user": 3} {
		if by[scope] != want {
			t.Fatalf("scope %q count = %d, want %d (scopes=%+v)", scope, by[scope], want, out.Scopes)
		}
	}
	// Scopes are sorted by count descending: project(4) first, task(1) last.
	if out.Scopes[0].Scope != "project" || out.Scopes[len(out.Scopes)-1].Scope != "task" {
		t.Fatalf("scopes not sorted by count desc: %+v", out.Scopes)
	}
}

func TestMemoryStats_TTLHistogramByRemainingLife(t *testing.T) {
	h := newTestHarness(t)
	now := time.Now()
	h.fake.scanVerticesFn = singlePage(
		vexp("user.a", "x", now.Add(10*time.Second)),   // <= seconds (30s)
		vexp("user.b", "x", now.Add(5*time.Minute)),    // <= turn (10m)
		vexp("user.c", "x", now.Add(50*24*time.Hour)),  // <= quarter (90d), > month (30d)
		vexp("user.d", "x", now.Add(200*24*time.Hour)), // > durable (180d) -> unbounded
		vstr("user.e", "x"),                            // no expiration -> unbounded
		vexp("user.f", "x", now.Add(-time.Minute)),     // past expiry -> expired
	)

	res := h.call(t, "memory_stats", map[string]any{})
	if res.IsError {
		t.Fatalf("IsError = true: %s", contentText(res))
	}
	var out memoryStatsOutput
	structuredAs(t, res, &out)

	if out.Sampled != 6 {
		t.Fatalf("Sampled = %d, want 6", out.Sampled)
	}
	by := bucketCountsByName(out)
	for bucket, want := range map[string]int{"seconds": 1, "turn": 1, "quarter": 1, "unbounded": 2, "expired": 1} {
		if by[bucket] != want {
			t.Fatalf("bucket %q count = %d, want %d (histogram=%+v)", bucket, by[bucket], want, out.TTLBuckets)
		}
	}
	// Histogram must be in canonical ascending-horizon order, then unbounded,
	// then expired.
	wantOrder := []string{"seconds", "turn", "quarter", "unbounded", "expired"}
	gotOrder := make([]string, len(out.TTLBuckets))
	for i, b := range out.TTLBuckets {
		gotOrder[i] = b.Bucket
	}
	if strings.Join(gotOrder, ",") != strings.Join(wantOrder, ",") {
		t.Fatalf("histogram order = %v, want %v", gotOrder, wantOrder)
	}
	if out.Truncated {
		t.Fatalf("Truncated = true for a small keyspace")
	}
}

// TestMemoryStats_NeverLeaksValues guards the counts-only contract: no stored
// value may appear anywhere in the result, even though the histogram scan
// reads full vertices.
func TestMemoryStats_NeverLeaksValues(t *testing.T) {
	h := newTestHarness(t)
	h.fake.scanVerticesFn = singlePage(
		vexp("user.secret", "TOPSECRETVALUE", time.Now().Add(time.Hour)),
	)
	res := h.call(t, "memory_stats", map[string]any{})
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if strings.Contains(string(raw), "TOPSECRETVALUE") {
		t.Fatalf("result leaked a stored value: %s", raw)
	}
}

// TestMemoryStats_PrefixScopesCountsAndOmitsScopeBreakdown verifies a prefixed
// call scopes the totals to the namespace and drops the per-scope breakdown
// (which only makes sense for a whole-keyspace call).
func TestMemoryStats_PrefixScopesCountsAndOmitsScopeBreakdown(t *testing.T) {
	h := newTestHarness(t)
	h.fake.countByPrefixFn = func(_ context.Context, prefix string) (uint64, error) {
		if prefix == "project.lantern." {
			return 4, nil
		}
		return 0, nil
	}
	h.fake.scanVerticesFn = singlePage(
		vexp("project.lantern.stack", "go", time.Now().Add(48*time.Hour)),
	)

	res := h.call(t, "memory_stats", map[string]any{"prefix": "project.lantern."})
	if res.IsError {
		t.Fatalf("IsError = true: %s", contentText(res))
	}
	var out memoryStatsOutput
	structuredAs(t, res, &out)

	if out.Prefix != "project.lantern." {
		t.Fatalf("Prefix = %q, want project.lantern.", out.Prefix)
	}
	if out.Vertices != 4 {
		t.Fatalf("Vertices = %d, want 4", out.Vertices)
	}
	if len(out.Scopes) != 0 {
		t.Fatalf("Scopes = %+v, want none for a prefixed call", out.Scopes)
	}
	if h.fake.lastCountPrefix != "project.lantern." {
		t.Fatalf("CountVerticesByPrefix last prefix = %q, want project.lantern.", h.fake.lastCountPrefix)
	}
}

// TestMemoryStats_HistogramTruncationIsHonest drives the vertex scan past its
// budget and asserts the sample is capped, truncated is set, and the
// suggestion explains the cap.
func TestMemoryStats_HistogramTruncationIsHonest(t *testing.T) {
	h := newTestHarness(t)
	exp := time.Now().Add(time.Hour)
	verts := make([]*client.Vertex, memoryStatsMaxVertexScan+1)
	for i := range verts {
		verts[i] = vexp(fmt.Sprintf("user.k%d", i), "x", exp)
	}
	h.fake.scanVerticesFn = singlePage(verts...)

	res := h.call(t, "memory_stats", map[string]any{})
	if res.IsError {
		t.Fatalf("IsError = true: %s", contentText(res))
	}
	var out memoryStatsOutput
	structuredAs(t, res, &out)

	if out.Sampled != memoryStatsMaxVertexScan {
		t.Fatalf("Sampled = %d, want %d (capped)", out.Sampled, memoryStatsMaxVertexScan)
	}
	if !out.Truncated {
		t.Fatalf("Truncated = false, want true after hitting the scan budget")
	}
	if out.Suggestion == "" {
		t.Fatalf("Suggestion empty, want a truncation note")
	}
}
