package integration_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/service"
)

// searchVertexDocument mirrors the production provider projection
// (server/provider/search.go) closely enough for the wire test: it folds
// the key together with the string value so a query matches either. The
// integration suite cannot import the unexported provider helper, and the
// exact value-type rendering is covered by the provider unit test — here we
// only need a live index behind the RPC.
func searchVertexDocument(v *pb.Vertex) search.Document {
	var b strings.Builder
	b.WriteString(v.GetKey())
	if s := v.GetString_(); s != "" {
		b.WriteByte(' ')
		b.WriteString(s)
	}
	return search.Text(b.String())
}

func wireSearchErrorReason(t *testing.T, err error) pb.SearchErrorReason {
	t.Helper()
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("error %T is not *connect.Error", err)
	}
	for _, detail := range connectErr.Details() {
		value, valueErr := detail.Value()
		if valueErr != nil {
			t.Fatalf("decode error detail: %v", valueErr)
		}
		if searchDetail, ok := value.(*pb.SearchErrorDetail); ok {
			return searchDetail.GetReason()
		}
	}
	t.Fatalf("SearchErrorDetail missing from wire error: %v", err)
	return pb.SearchErrorReason_SEARCH_ERROR_REASON_UNSPECIFIED
}

// newSearchRawClient stands up a Connect server whose GraphCache has the
// search index enabled (or not) and whose service gates SearchVertices on
// the matching SearchLimits.Enabled flag — the same two-sided agreement the
// production composition root makes.
func newSearchRawClient(t *testing.T, enabled bool) graphv1connect.LanternServiceClient {
	t.Helper()
	return newSearchRawClientWithLimits(t, service.SearchLimits{
		Enabled:          enabled,
		PositionsEnabled: enabled,
		DefaultLimit:     100,
		MaxLimit:         1000,
	})
}

func newSearchRawClientWithLimits(t *testing.T, limits service.SearchLimits) graphv1connect.LanternServiceClient {
	t.Helper()
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	cache.EnablePrefixIndex(func(k string) string { return k })
	if limits.Enabled {
		cache.EnableSearchIndex(searchVertexDocument, strings.Compare)
	}
	svc := service.NewLanternService(cache).WithSearchLimits(limits)
	srv := newConnectTestServer(t, svc, nil)
	return graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)
}

// TestSearchVertices_OptionsContract proves the #1055 decision table over the
// real Connect/h2c path. In particular, adding fuzziness must not turn an
// omitted match mode into ANY when this server is configured for ALL.
func TestSearchVertices_OptionsContract(t *testing.T) {
	c := newSearchRawClientWithLimits(t, service.SearchLimits{
		Enabled:          true,
		PositionsEnabled: true,
		DefaultLimit:     100,
		MaxLimit:         1000,
		DefaultMode:      search.MatchAll,
		DefaultMinShould: 2,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	exp := timestamppb.New(time.Now().Add(time.Hour))
	if _, err := c.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: []*pb.Vertex{
		{Key: "doc.both", Value: &pb.Vertex_String_{String_: "alpha beta"}, Expiration: exp},
		{Key: "doc.alpha", Value: &pb.Vertex_String_{String_: "alpha"}, Expiration: exp},
		{Key: "doc.beta", Value: &pb.Vertex_String_{String_: "beta"}, Expiration: exp},
	}})); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}

	keys := func(query string, options *pb.SearchOptions) []string {
		t.Helper()
		resp, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: query, Options: options}))
		if err != nil {
			t.Fatalf("SearchVertices(%q, %+v): %v", query, options, err)
		}
		got := make([]string, len(resp.Msg.GetHits()))
		for i, hit := range resp.Msg.GetHits() {
			got[i] = hit.GetKey()
		}
		sort.Strings(got)
		return got
	}
	assertKeys := func(name string, got, want []string) {
		t.Helper()
		if !slices.Equal(got, want) {
			t.Errorf("%s keys = %v, want %v", name, got, want)
		}
	}

	assertKeys("omitted uses ALL", keys("alpha beta", nil), []string{"doc.both"})
	assertKeys("fuzziness preserves omitted ALL", keys("alpga beta", &pb.SearchOptions{Fuzziness: 1}), []string{"doc.both"})
	assertKeys("explicit ANY", keys("alpha beta", &pb.SearchOptions{MatchMode: pb.MatchMode_MATCH_MODE_ANY}), []string{"doc.alpha", "doc.beta", "doc.both"})
	assertKeys("MIN zero uses server threshold", keys("alpha beta", &pb.SearchOptions{MatchMode: pb.MatchMode_MATCH_MODE_MIN_SHOULD}), []string{"doc.both"})
	assertKeys("MIN explicit one", keys("alpha beta", &pb.SearchOptions{MatchMode: pb.MatchMode_MATCH_MODE_MIN_SHOULD, MinShouldMatch: 1}), []string{"doc.alpha", "doc.beta", "doc.both"})

	invalid := []struct {
		name string
		opts *pb.SearchOptions
	}{
		{"unknown mode", &pb.SearchOptions{MatchMode: pb.MatchMode(99)}},
		{"minimum without explicit MIN", &pb.SearchOptions{MinShouldMatch: 1}},
		{"minimum with ANY", &pb.SearchOptions{MatchMode: pb.MatchMode_MATCH_MODE_ANY, MinShouldMatch: 1}},
		{"fuzziness out of range", &pb.SearchOptions{Fuzziness: 3}},
		{"phrase with explicit mode", &pb.SearchOptions{Phrase: true, MatchMode: pb.MatchMode_MATCH_MODE_ALL}},
		{"phrase with fuzziness", &pb.SearchOptions{Phrase: true, Fuzziness: 1}},
		{"phrase with prefix terms", &pb.SearchOptions{Phrase: true, PrefixTerms: true}},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: "alpha beta", Options: tc.opts}))
			if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
				t.Fatalf("code = %v, want InvalidArgument (err=%v)", got, err)
			}
		})
	}
}

// newSearchSDKClient mirrors newSearchRawClient but returns the high-level
// *client.Lantern so the SDK SearchVertices forwarder (#625) is exercised
// against the real service handler — the SDK package itself keeps only
// white-box request/response tests.
func newSearchSDKClient(t *testing.T, enabled bool) *client.Lantern {
	t.Helper()
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	cache.EnablePrefixIndex(func(k string) string { return k })
	if enabled {
		cache.EnableSearchIndex(searchVertexDocument, strings.Compare)
	}
	svc := service.NewLanternService(cache).WithSearchLimits(service.SearchLimits{
		Enabled:          enabled,
		PositionsEnabled: enabled,
		DefaultLimit:     100,
		MaxLimit:         1000,
	})
	srv := newConnectTestServer(t, svc, nil)
	return newConnectClientFor(t, srv.url)
}

func TestSearchVertices_EndToEnd(t *testing.T) {
	c := newSearchRawClient(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	exp := timestamppb.New(time.Now().Add(time.Hour))
	seed := []*pb.Vertex{
		{Key: "user.preferences.tone", Value: &pb.Vertex_String_{String_: "calm and concise"}, Expiration: exp},
		{Key: "user.preferences.format", Value: &pb.Vertex_String_{String_: "calm bullet points"}, Expiration: exp},
		{Key: "project.lantern.stack", Value: &pb.Vertex_String_{String_: "go and react"}, Expiration: exp},
	}
	if _, err := c.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: seed})); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}

	// Content query matches the two "calm" preference vertices, ranked.
	resp, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: "calm"}))
	if err != nil {
		t.Fatalf("SearchVertices: %v", err)
	}
	hits := resp.Msg.GetHits()
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2 (%+v)", len(hits), hits)
	}
	for _, h := range hits {
		if !strings.HasPrefix(h.GetKey(), "user.preferences.") {
			t.Errorf("unexpected hit key %q", h.GetKey())
		}
		if h.GetScore() <= 0 {
			t.Errorf("hit %q score = %v, want > 0", h.GetKey(), h.GetScore())
		}
	}

	// Prefix scopes the same query to a single namespace branch.
	scoped, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{
		Query:  "calm",
		Prefix: "user.preferences.tone",
	}))
	if err != nil {
		t.Fatalf("SearchVertices scoped: %v", err)
	}
	if got := scoped.Msg.GetHits(); len(got) != 1 || got[0].GetKey() != "user.preferences.tone" {
		t.Fatalf("scoped hits = %+v, want only user.preferences.tone", got)
	}

	// Empty query is a zero-hit success, not an error.
	empty, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: ""}))
	if err != nil {
		t.Fatalf("SearchVertices empty query: %v", err)
	}
	if got := empty.Msg.GetHits(); len(got) != 0 {
		t.Fatalf("empty-query hits = %d, want 0", len(got))
	}
}

func TestSearchVertices_DisabledReturnsFailedPrecondition(t *testing.T) {
	c := newSearchRawClient(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: "calm"}))
	if err == nil {
		t.Fatalf("expected FAILED_PRECONDITION when search disabled")
	}
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", got)
	}
	if got := wireSearchErrorReason(t, err); got != pb.SearchErrorReason_SEARCH_DISABLED {
		t.Fatalf("reason = %v, want SEARCH_DISABLED", got)
	}
}

// TestSearchVertices_SDKForwarder drives the SDK's high-level SearchVertices
// against the live handler: ranked SearchHit mapping, WithSearchPrefix
// scoping, the empty-query zero-hit success, and the disabled-server path
// surfacing as the client.ErrFailedPrecondition sentinel (#625).
func TestSearchVertices_SDKForwarder(t *testing.T) {
	t.Run("ranked hits, prefix scope and empty query", func(t *testing.T) {
		l := newSearchSDKClient(t, true)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		seed := []client.VertexInput{
			{Key: "user.preferences.tone", Value: "calm and concise", Expiration: time.Now().Add(time.Hour)},
			{Key: "user.preferences.format", Value: "calm bullet points", Expiration: time.Now().Add(time.Hour)},
			{Key: "project.lantern.stack", Value: "go and react", Expiration: time.Now().Add(time.Hour)},
		}
		if err := l.PutVertices(ctx, seed); err != nil {
			t.Fatalf("PutVertices: %v", err)
		}

		hits, err := l.SearchVertices(ctx, "calm")
		if err != nil {
			t.Fatalf("SearchVertices: %v", err)
		}
		if len(hits) != 2 {
			t.Fatalf("hits = %d, want 2 (%+v)", len(hits), hits)
		}
		for _, h := range hits {
			if !strings.HasPrefix(h.Key, "user.preferences.") {
				t.Errorf("unexpected hit key %q", h.Key)
			}
			if h.Score <= 0 {
				t.Errorf("hit %q score = %v, want > 0", h.Key, h.Score)
			}
		}

		scoped, err := l.SearchVertices(ctx, "calm", client.WithSearchPrefix("user.preferences.tone"))
		if err != nil {
			t.Fatalf("SearchVertices scoped: %v", err)
		}
		if len(scoped) != 1 || scoped[0].Key != "user.preferences.tone" {
			t.Fatalf("scoped hits = %+v, want only user.preferences.tone", scoped)
		}

		empty, err := l.SearchVertices(ctx, "")
		if err != nil {
			t.Fatalf("empty query must be a zero-hit success: %v", err)
		}
		if len(empty) != 0 {
			t.Fatalf("empty-query hits = %d, want 0", len(empty))
		}
	})

	t.Run("disabled server surfaces ErrFailedPrecondition", func(t *testing.T) {
		l := newSearchSDKClient(t, false)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		hits, err := l.SearchVertices(ctx, "calm")
		if hits != nil {
			t.Errorf("want nil hits when disabled, got %+v", hits)
		}
		if !errors.Is(err, client.ErrFailedPrecondition) {
			t.Fatalf("want errors.Is(err, client.ErrFailedPrecondition), got %v", err)
		}
		if !errors.Is(err, client.ErrSearchDisabled) {
			t.Fatalf("want errors.Is(err, client.ErrSearchDisabled), got %v", err)
		}
		if got := client.SearchFailureReason(err); got != client.SearchErrorReasonDisabled {
			t.Fatalf("SearchFailureReason = %v, want SEARCH_DISABLED", got)
		}
	})
}

// TestSearchVertices_IncrementalLatestInputWins drives the Go SDK's
// search-as-you-type orchestration over the real Connect/h2c path. The final
// query in a burst is the only result exposed, and an empty input invalidates
// pending debounce work immediately without a later stale delivery (#1052).
func TestSearchVertices_IncrementalLatestInputWins(t *testing.T) {
	l := newSearchSDKClient(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := l.PutVertices(ctx, []client.VertexInput{
		{Key: "doc.alpha", Value: "alpha", Expiration: time.Now().Add(time.Hour)},
		{Key: "doc.beta", Value: "beta", Expiration: time.Now().Add(time.Hour)},
	}); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}

	const debounce = 30 * time.Millisecond
	incremental := l.NewIncrementalSearch(ctx, client.WithDebounce(debounce))
	defer func() {
		if err := incremental.Close(); err != nil {
			t.Errorf("Close incremental search: %v", err)
		}
	}()

	incremental.Search("alpha")
	incremental.Search("beta")
	select {
	case update := <-incremental.Updates():
		if update.Err != nil {
			t.Fatalf("incremental beta: %v", update.Err)
		}
		if update.Query != "beta" || len(update.Hits) != 1 || update.Hits[0].Key != "doc.beta" {
			t.Fatalf("update = %+v, want beta/doc.beta", update)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for incremental beta result")
	}

	incremental.Search("alpha")
	incremental.Search("")
	select {
	case update := <-incremental.Updates():
		if update.Query != "" || update.Err != nil || len(update.Hits) != 0 {
			t.Fatalf("empty reset = %+v, want synchronous empty update", update)
		}
	default:
		t.Fatal("empty input did not reset synchronously")
	}
	select {
	case update := <-incremental.Updates():
		t.Fatalf("pending alpha search delivered after empty input: %+v", update)
	case <-time.After(2 * debounce):
	}
}

// TestSearchVertices_Options drives the #892 SearchOptions end to end through
// the raw Connect handler: match mode, phrase adjacency, fuzzy/prefix term
// expansion, and minimum-should-match, each over a live index.
func TestSearchVertices_Options(t *testing.T) {
	c := newSearchRawClient(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	exp := timestamppb.New(time.Now().Add(time.Hour))
	seed := []*pb.Vertex{
		{Key: "doc.both", Value: &pb.Vertex_String_{String_: "alpha beta gamma"}, Expiration: exp},
		{Key: "doc.alpha", Value: &pb.Vertex_String_{String_: "alpha delta"}, Expiration: exp},
		{Key: "doc.beta", Value: &pb.Vertex_String_{String_: "beta epsilon"}, Expiration: exp},
		{Key: "doc.phrase", Value: &pb.Vertex_String_{String_: "the alpha beta end"}, Expiration: exp},
		{Key: "doc.split", Value: &pb.Vertex_String_{String_: "beta stands well before the word alpha"}, Expiration: exp},
	}
	if _, err := c.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: seed})); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}
	keys := func(q string, o *pb.SearchOptions) map[string]bool {
		resp, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: q, Options: o}))
		if err != nil {
			t.Fatalf("SearchVertices(%q): %v", q, err)
		}
		got := map[string]bool{}
		for _, h := range resp.Msg.GetHits() {
			got[h.GetKey()] = true
		}
		return got
	}

	t.Run("match_mode ALL requires every term", func(t *testing.T) {
		all := keys("alpha beta", &pb.SearchOptions{MatchMode: pb.MatchMode_MATCH_MODE_ALL})
		if all["doc.alpha"] || all["doc.beta"] {
			t.Errorf("MatchAll kept a single-term doc: %v", all)
		}
		for _, k := range []string{"doc.both", "doc.phrase", "doc.split"} {
			if !all[k] {
				t.Errorf("MatchAll dropped %q which has both terms: %v", k, all)
			}
		}
	})

	t.Run("phrase requires adjacency", func(t *testing.T) {
		got := keys("alpha beta", &pb.SearchOptions{Phrase: true})
		if !got["doc.both"] || !got["doc.phrase"] {
			t.Errorf("phrase dropped an adjacent doc: %v", got)
		}
		if got["doc.split"] {
			t.Errorf("phrase kept the scattered doc.split: %v", got)
		}
	})

	t.Run("fuzziness tolerates a typo", func(t *testing.T) {
		got := keys("alpga beta", &pb.SearchOptions{Fuzziness: 1}) // alpga is one edit from alpha
		if !got["doc.both"] {
			t.Errorf("fuzzy query did not reach doc.both: %v", got)
		}
	})

	t.Run("prefix_terms match an extending word", func(t *testing.T) {
		got := keys("gam", &pb.SearchOptions{PrefixTerms: true}) // gam -> gamma
		if !got["doc.both"] {
			t.Errorf("prefix_terms did not reach doc.both (gamma): %v", got)
		}
	})

	t.Run("min_should_match tunes coverage", func(t *testing.T) {
		msm := keys("alpha beta gamma", &pb.SearchOptions{
			MatchMode:      pb.MatchMode_MATCH_MODE_MIN_SHOULD,
			MinShouldMatch: 2,
		})
		if !msm["doc.both"] {
			t.Errorf("msm(2) dropped doc.both (has all three terms): %v", msm)
		}
		if msm["doc.alpha"] || msm["doc.beta"] {
			t.Errorf("msm(2) kept a single-term doc: %v", msm)
		}
	})
}

// TestSearchVertices_DeterministicAcrossHistories drives cap-overflow prefix
// and fuzzy expansion through independent real Connect/h2c servers whose final
// corpora are identical but whose document/term ordinal histories differ. The
// complete protobuf bytes (ordered keys and float bits) must agree on every
// repeated call.
func TestSearchVertices_DeterministicAcrossHistories(t *testing.T) {
	type document struct {
		key   string
		value string
	}
	var documents []document
	var prefixWant []string
	for i := 0; i < 80; i++ {
		key := fmt.Sprintf("docp-%03d", i)
		documents = append(documents, document{key: key, value: fmt.Sprintf("pr%03d", i)})
		if i < search.MaxTermExpansions {
			prefixWant = append(prefixWant, key)
		}
	}
	var fuzzyTerms []string
	for ch := byte('b'); ch <= 'z'; ch++ {
		fuzzyTerms = append(fuzzyTerms, string([]byte{ch, 'a'}), string([]byte{'a', ch}))
	}
	for digit := byte('0'); digit <= '9'; digit++ {
		fuzzyTerms = append(fuzzyTerms, string([]byte{'a', digit, 'a'}))
	}
	sort.Strings(fuzzyTerms)
	var fuzzyWant []string
	for i, term := range fuzzyTerms {
		key := fmt.Sprintf("docf-%03d", i)
		documents = append(documents, document{key: key, value: term})
		if i < search.MaxTermExpansions {
			fuzzyWant = append(fuzzyWant, key)
		}
	}

	seed := func(t *testing.T, c graphv1connect.LanternServiceClient, order []int, churn bool) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		exp := timestamppb.New(time.Now().Add(time.Hour))
		if churn {
			var transient []*pb.Vertex
			for i := 0; i < len(documents); i += 3 {
				transient = append(transient, &pb.Vertex{
					Key:        documents[i].key,
					Value:      &pb.Vertex_String_{String_: fmt.Sprintf("temporary%03d", i)},
					Expiration: exp,
				})
			}
			if _, err := c.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: transient})); err != nil {
				t.Fatalf("PutVertices transient: %v", err)
			}
		}
		vertices := make([]*pb.Vertex, 0, len(order))
		for _, i := range order {
			vertices = append(vertices, &pb.Vertex{
				Key:        documents[i].key,
				Value:      &pb.Vertex_String_{String_: documents[i].value},
				Expiration: exp,
			})
		}
		if _, err := c.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: vertices})); err != nil {
			t.Fatalf("PutVertices final: %v", err)
		}
	}

	forward := make([]int, len(documents))
	for i := range forward {
		forward[i] = i
	}
	reverse := append([]int(nil), forward...)
	for i, j := 0, len(reverse)-1; i < j; i, j = i+1, j-1 {
		reverse[i], reverse[j] = reverse[j], reverse[i]
	}
	random := append([]int(nil), forward...)
	rand.New(rand.NewSource(1056)).Shuffle(len(random), func(i, j int) {
		random[i], random[j] = random[j], random[i]
	})
	histories := []struct {
		name  string
		order []int
		churn bool
	}{
		{"forward", forward, false},
		{"reverse", reverse, false},
		{"random", random, false},
		{"release-reuse", reverse, true},
	}
	tests := []struct {
		name    string
		query   string
		options *pb.SearchOptions
		want    []string
	}{
		{"prefix", "pr", &pb.SearchOptions{PrefixTerms: true}, prefixWant},
		{"fuzzy", "aa", &pb.SearchOptions{Fuzziness: 1}, fuzzyWant},
	}

	baseline := make(map[string][]byte, len(tests))
	for _, history := range histories {
		t.Run(history.name, func(t *testing.T) {
			c := newSearchRawClient(t, true)
			seed(t, c, history.order, history.churn)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			for _, tc := range tests {
				t.Run(tc.name, func(t *testing.T) {
					for repeat := 0; repeat < 100; repeat++ {
						resp, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{
							Query:   tc.query,
							Limit:   100,
							Options: tc.options,
						}))
						if err != nil {
							t.Fatalf("repeat %d SearchVertices: %v", repeat, err)
						}
						gotKeys := make([]string, len(resp.Msg.GetHits()))
						for i, hit := range resp.Msg.GetHits() {
							gotKeys[i] = hit.GetKey()
						}
						if !slices.Equal(gotKeys, tc.want) {
							t.Fatalf("repeat %d keys = %v, want %v", repeat, gotKeys, tc.want)
						}
						wire, err := proto.MarshalOptions{Deterministic: true}.Marshal(resp.Msg)
						if err != nil {
							t.Fatalf("marshal response: %v", err)
						}
						if want, ok := baseline[tc.name]; !ok {
							baseline[tc.name] = append([]byte(nil), wire...)
						} else if !bytes.Equal(wire, want) {
							t.Fatalf("repeat %d response bytes differ across calls/histories", repeat)
						}
					}
				})
			}
		})
	}
}

// TestSearchVertices_PositionsOff verifies that the wire service fails closed:
// without positions it cannot prove adjacency, so it must never silently
// reinterpret phrase=true as an unordered AND query.
func TestSearchVertices_PositionsOff(t *testing.T) {
	// Build a search-enabled service whose cache index tracks no positions.
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	cache.EnablePrefixIndex(func(k string) string { return k })
	cache.EnableSearchIndex(searchVertexDocument, strings.Compare, graphcache.WithoutSearchPositions())
	svc := service.NewLanternService(cache).WithSearchLimits(service.SearchLimits{
		Enabled:          true,
		PositionsEnabled: false,
		DefaultLimit:     100,
		MaxLimit:         1000,
	})
	srv := newConnectTestServer(t, svc, nil)
	c := graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	exp := timestamppb.New(time.Now().Add(time.Hour))
	seed := []*pb.Vertex{
		{Key: "doc.phrase", Value: &pb.Vertex_String_{String_: "the alpha beta end"}, Expiration: exp},
		{Key: "doc.split", Value: &pb.Vertex_String_{String_: "beta stands well before the word alpha"}, Expiration: exp},
	}
	if _, err := c.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: seed})); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}

	_, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{
		Query:   "alpha beta",
		Options: &pb.SearchOptions{Phrase: true},
	}))
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", got)
	}
	if got := wireSearchErrorReason(t, err); got != pb.SearchErrorReason_SEARCH_POSITIONS_DISABLED {
		t.Fatalf("reason = %v, want SEARCH_POSITIONS_DISABLED", got)
	}
	status, statusErr := c.GetServerStatus(ctx, connect.NewRequest(&pb.GetServerStatusRequest{}))
	if statusErr != nil {
		t.Fatalf("GetServerStatus: %v", statusErr)
	}
	capabilities := status.Msg.GetSearch()
	if !capabilities.GetEnabled() || capabilities.GetPositionsEnabled() {
		t.Fatalf("search capabilities = %+v, want enabled with positions off", capabilities)
	}
	if capabilities.GetConfigFingerprint() == "" {
		t.Fatal("search config fingerprint is empty")
	}
	withPositions := newSearchRawClient(t, true)
	peerStatus, peerErr := withPositions.GetServerStatus(ctx, connect.NewRequest(&pb.GetServerStatusRequest{}))
	if peerErr != nil {
		t.Fatalf("GetServerStatus positions-enabled peer: %v", peerErr)
	}
	if peerStatus.Msg.GetSearch().GetConfigFingerprint() == capabilities.GetConfigFingerprint() {
		t.Fatal("mixed search configuration is invisible: peer fingerprints match")
	}
}

// TestSearchVertices_SDKOptions drives the #892 options through the high-level
// SDK: WithMatchMode(MatchAll) narrows the OR-union to the document holding
// every term.
func TestSearchVertices_SDKOptions(t *testing.T) {
	l := newSearchSDKClient(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	seed := []client.VertexInput{
		{Key: "doc.both", Value: "alpha beta gamma", Expiration: time.Now().Add(time.Hour)},
		{Key: "doc.alpha", Value: "alpha delta", Expiration: time.Now().Add(time.Hour)},
		{Key: "doc.beta", Value: "beta epsilon", Expiration: time.Now().Add(time.Hour)},
	}
	if err := l.PutVertices(ctx, seed); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}

	any, err := l.SearchVertices(ctx, "alpha beta")
	if err != nil {
		t.Fatalf("SearchVertices any: %v", err)
	}
	all, err := l.SearchVertices(ctx, "alpha beta", client.WithMatchMode(client.MatchAll))
	if err != nil {
		t.Fatalf("SearchVertices all: %v", err)
	}
	if len(all) >= len(any) {
		t.Errorf("WithMatchMode(MatchAll) should narrow: any=%d all=%d", len(any), len(all))
	}
	got := map[string]bool{}
	for _, h := range all {
		got[h.Key] = true
	}
	if !got["doc.both"] || got["doc.alpha"] || got["doc.beta"] {
		t.Errorf("MatchAll hits = %v, want only doc.both", got)
	}
}
