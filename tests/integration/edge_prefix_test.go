package integration_test

import (
	"context"
	"sort"
	"testing"
	"time"

	client "github.com/anaregdesign/lantern/sdks/go"
)

// seedPrefixEdges creates both endpoints as live vertices and then puts the
// edges with an explicit non-zero expiration. Edges without an explicit
// expiration are born-expired (1970-01-01) — see the AGENTS.md note in
// core/graphcache for the same trap.
func seedPrefixEdges(t *testing.T, ctx context.Context, l *client.Lantern, pairs [][2]string) {
	t.Helper()
	keySet := map[string]struct{}{}
	for _, p := range pairs {
		keySet[p[0]] = struct{}{}
		keySet[p[1]] = struct{}{}
	}
	keys := make([]string, 0, len(keySet))
	for k := range keySet {
		keys = append(keys, k)
	}
	seedPrefixVertices(t, ctx, l, keys)

	exp := time.Now().Add(time.Hour)
	edges := make([]client.EdgeInput, len(pairs))
	for i, p := range pairs {
		edges[i] = client.EdgeInput{Tail: p[0], Head: p[1], Weight: 1, Expiration: exp}
	}
	if err := l.PutEdges(ctx, edges); err != nil {
		t.Fatalf("PutEdges: %v", err)
	}
}

func collectEdges(t *testing.T, ctx context.Context, l *client.Lantern, opts ...client.EdgeScanOption) []string {
	t.Helper()
	out := []string{}
	for batch, err := range l.ScanEdgesAll(ctx, 100, opts...) {
		if err != nil {
			t.Fatalf("ScanEdgesAll: %v", err)
		}
		for _, e := range batch {
			out = append(out, e.GetTail()+"->"+e.GetHead())
		}
	}
	sort.Strings(out)
	return out
}

func TestSDK_ScanEdges_TailHeadFilters(t *testing.T) {
	l, cleanup := newPrefixSDKClient(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	seedPrefixEdges(t, ctx, l, [][2]string{
		{"user:1", "post:1"},
		{"user:1", "post:2"},
		{"user:2", "post:3"},
		{"user:2", "session:1"},
		{"admin:1", "post:9"},
		{"admin:1", "session:7"},
	})

	cases := []struct {
		name string
		opts []client.EdgeScanOption
		want []string
	}{
		{
			name: "all",
			want: []string{
				"admin:1->post:9", "admin:1->session:7",
				"user:1->post:1", "user:1->post:2",
				"user:2->post:3", "user:2->session:1",
			},
		},
		{
			name: "tail user:",
			opts: []client.EdgeScanOption{client.WithEdgeScanTailPrefix("user:")},
			want: []string{
				"user:1->post:1", "user:1->post:2",
				"user:2->post:3", "user:2->session:1",
			},
		},
		{
			name: "head post:",
			opts: []client.EdgeScanOption{client.WithEdgeScanHeadPrefix("post:")},
			want: []string{
				"admin:1->post:9",
				"user:1->post:1", "user:1->post:2",
				"user:2->post:3",
			},
		},
		{
			name: "tail user: + head post:",
			opts: []client.EdgeScanOption{
				client.WithEdgeScanTailPrefix("user:"),
				client.WithEdgeScanHeadPrefix("post:"),
			},
			want: []string{
				"user:1->post:1", "user:1->post:2", "user:2->post:3",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := collectEdges(t, ctx, l, tc.opts...)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

func TestSDK_ScanEdges_Pagination(t *testing.T) {
	l, cleanup := newPrefixSDKClient(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	pairs := [][2]string{
		{"t:1", "h:1"}, {"t:1", "h:2"}, {"t:1", "h:3"},
		{"t:2", "h:1"}, {"t:2", "h:2"},
	}
	seedPrefixEdges(t, ctx, l, pairs)

	// Page through with limit=2.
	seen := []string{}
	var cursor []byte
	for {
		page, next, err := l.ScanEdges(ctx,
			client.WithEdgeScanTailPrefix("t:"),
			client.WithEdgeScanLimit(2),
			client.WithEdgeScanCursor(cursor),
		)
		if err != nil {
			t.Fatalf("ScanEdges: %v", err)
		}
		for _, e := range page {
			seen = append(seen, e.GetTail()+"->"+e.GetHead())
		}
		if len(next) == 0 {
			break
		}
		cursor = next
	}
	want := []string{
		"t:1->h:1", "t:1->h:2", "t:1->h:3",
		"t:2->h:1", "t:2->h:2",
	}
	if len(seen) != len(want) {
		t.Fatalf("paged result %v, want %v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("paged result %v, want %v", seen, want)
		}
	}
}
