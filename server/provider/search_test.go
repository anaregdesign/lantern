package provider

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/search"
	"github.com/anaregdesign/lantern/core/search/relevance"
	v1 "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const projectedFieldsFixturePath = "../../core/search/relevance/testdata/projected_fields.json"
const luceneBaselinePath = "../../core/search/relevance/testdata/lucene_runs.json"

type projectedFieldsFixture struct {
	FixtureFormat     string                     `json:"fixture_format"`
	ProjectionVersion string                     `json:"projection_version"`
	Corpora           map[string]projectedCorpus `json:"corpora"`
}

type projectedCorpus struct {
	Docs []projectedDoc `json:"docs"`
}

type projectedDoc struct {
	ID     string           `json:"id"`
	Fields []projectedField `json:"fields"`
}

type projectedField struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

func canonicalProjectedFieldsFixture(t *testing.T) projectedFieldsFixture {
	t.Helper()
	corpora, err := relevance.Corpora()
	if err != nil {
		t.Fatal(err)
	}
	fixture := projectedFieldsFixture{
		FixtureFormat:     relevance.BaselineFixtureFormat,
		ProjectionVersion: relevance.BaselineProjectionVersion,
		Corpora:           make(map[string]projectedCorpus, len(corpora)),
	}
	for _, corpus := range corpora {
		projected := projectedCorpus{Docs: make([]projectedDoc, 0, len(corpus.Docs))}
		for _, doc := range corpus.Docs {
			vertex := &v1.Vertex{Key: doc.ID, Value: &v1.Vertex_String_{String_: doc.Text}}
			fields := vertexSearchDocument(doc.ID, vertex).(vertexSearchProjection).SearchFields()
			out := projectedDoc{ID: doc.ID, Fields: make([]projectedField, len(fields))}
			for i, field := range fields {
				name := "value"
				if field.ID == search.FieldKey {
					name = "key"
				}
				out.Fields[i] = projectedField{Name: name, Text: field.Text}
			}
			projected.Docs = append(projected.Docs, out)
		}
		fixture.Corpora[corpus.Name] = projected
	}
	return fixture
}

func projectedFixtureBytes(t *testing.T, fixture projectedFieldsFixture) []byte {
	t.Helper()
	raw, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(raw, '\n')
}

func TestVertexSearchProjectionFixture(t *testing.T) {
	want := projectedFixtureBytes(t, canonicalProjectedFieldsFixture(t))
	if os.Getenv("UPDATE_SEARCH_PROJECTION_FIXTURE") == "1" {
		if err := os.WriteFile(projectedFieldsFixturePath, want, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	got, err := os.ReadFile(projectedFieldsFixturePath)
	if err != nil {
		t.Fatalf("read fixture (regenerate with cd server && UPDATE_SEARCH_PROJECTION_FIXTURE=1 go test ./provider -run TestVertexSearchProjectionFixture): %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("production search projection fixture is stale; regenerate with cd server && UPDATE_SEARCH_PROJECTION_FIXTURE=1 go test ./provider -run TestVertexSearchProjectionFixture")
	}
}

func TestVertexSearchDocument(t *testing.T) {
	tests := []struct {
		name   string
		vertex *v1.Vertex
		want   string
	}{
		{
			name:   "nil vertex",
			vertex: nil,
			want:   "",
		},
		{
			name:   "key only when value unset",
			vertex: &v1.Vertex{Key: "user.preferences.tone"},
			want:   "user.preferences.tone",
		},
		{
			name:   "explicit nil value indexes key only",
			vertex: &v1.Vertex{Key: "marker", Value: &v1.Vertex_Nil{Nil: true}},
			want:   "marker",
		},
		{
			name:   "string value folds in after key",
			vertex: &v1.Vertex{Key: "user.preferences.tone", Value: &v1.Vertex_String_{String_: "calm and concise"}},
			want:   "user.preferences.tone calm and concise",
		},
		{
			name:   "bytes value passes through as text",
			vertex: &v1.Vertex{Key: "blob", Value: &v1.Vertex_Bytes{Bytes: []byte("hello")}},
			want:   "blob hello",
		},
		{
			name:   "invalid UTF-8 bytes are treated as binary",
			vertex: &v1.Vertex{Key: "blob", Value: &v1.Vertex_Bytes{Bytes: []byte{0xff, 0xfe}}},
			want:   "blob",
		},
		{
			name:   "float64 value formatted",
			vertex: &v1.Vertex{Key: "f64", Value: &v1.Vertex_Float64{Float64: 1.25}},
			want:   "f64 1.25",
		},
		{
			name:   "float32 value formatted",
			vertex: &v1.Vertex{Key: "f32", Value: &v1.Vertex_Float32{Float32: 2.5}},
			want:   "f32 2.5",
		},
		{
			name:   "int32 value formatted decimally",
			vertex: &v1.Vertex{Key: "i32", Value: &v1.Vertex_Int32{Int32: -42}},
			want:   "i32 -42",
		},
		{
			name:   "int64 value formatted decimally",
			vertex: &v1.Vertex{Key: "count", Value: &v1.Vertex_Int64{Int64: 42}},
			want:   "count 42",
		},
		{
			name:   "uint32 value formatted decimally",
			vertex: &v1.Vertex{Key: "u32", Value: &v1.Vertex_Uint32{Uint32: 42}},
			want:   "u32 42",
		},
		{
			name:   "uint64 value formatted decimally",
			vertex: &v1.Vertex{Key: "u64", Value: &v1.Vertex_Uint64{Uint64: 18446744073709551615}},
			want:   "u64 18446744073709551615",
		},
		{
			name:   "bool value rendered",
			vertex: &v1.Vertex{Key: "flag", Value: &v1.Vertex_Bool{Bool: true}},
			want:   "flag true",
		},
		{
			name: "timestamp value uses RFC3339Nano",
			vertex: &v1.Vertex{Key: "when", Value: &v1.Vertex_Timestamp{Timestamp: timestamppb.New(
				time.Date(2026, 7, 13, 1, 2, 3, 456000000, time.UTC),
			)}},
			want: "when 2026-07-13T01:02:03.456Z",
		},
		{
			name:   "duration value uses Go form",
			vertex: &v1.Vertex{Key: "elapsed", Value: &v1.Vertex_Duration{Duration: durationpb.New(12*time.Second + 345*time.Millisecond)}},
			want:   "elapsed 12.345s",
		},
		{
			name:   "json object string value drops field names and non-strings",
			vertex: &v1.Vertex{Key: "profile", Value: &v1.Vertex_String_{String_: `{"role":"admin","name":"Alice","score":9,"active":true}`}},
			want:   "profile Alice admin",
		},
		{
			name:   "json array string value keeps only strings",
			vertex: &v1.Vertex{Key: "tags", Value: &v1.Vertex_String_{String_: `["go","db",2,true]`}},
			want:   "tags go db",
		},
		{
			name:   "json object with no string content indexes key only",
			vertex: &v1.Vertex{Key: "counters", Value: &v1.Vertex_String_{String_: `{"a":1,"b":false,"c":null}`}},
			want:   "counters",
		},
		{
			name:   "plain string value is not treated as json",
			vertex: &v1.Vertex{Key: "note", Value: &v1.Vertex_String_{String_: "calm and concise"}},
			want:   "note calm and concise",
		},
		{
			name:   "empty string value yields key only (no trailing space)",
			vertex: &v1.Vertex{Key: "k", Value: &v1.Vertex_String_{String_: ""}},
			want:   "k",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := vertexSearchDocument(tc.vertex.GetKey(), tc.vertex).String(); got != tc.want {
				t.Errorf("vertexSearchDocument = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestVertexSearchDocumentPreservesFieldInstances(t *testing.T) {
	doc := vertexSearchDocument("alpha", &v1.Vertex{
		Key: "alpha", Value: &v1.Vertex_String_{String_: `{"b":"beta gamma","a":"delta"}`},
	}).(vertexSearchProjection)
	want := []search.DocumentField{
		{ID: search.FieldKey, Text: "alpha"},
		{ID: search.FieldValue, Text: "delta"},
		{ID: search.FieldValue, Text: "beta gamma"},
	}
	if got := doc.SearchFields(); !reflect.DeepEqual(got, want) {
		t.Fatalf("fields = %+v, want %+v", got, want)
	}
	endpoint := vertexSearchDocument("implicit.endpoint", nil).(vertexSearchProjection)
	if got := endpoint.SearchFields(); !reflect.DeepEqual(got, []search.DocumentField{{ID: search.FieldKey, Text: "implicit.endpoint"}}) {
		t.Fatalf("endpoint fields = %+v", got)
	}
}

func TestNewGraphCacheSearchFieldBoundariesAndImplicitEndpoints(t *testing.T) {
	gc := NewGraphCache(CacheConfig{TTL: time.Minute}, SearchConfig{Enabled: true, Positions: true})
	expiration := time.Now().Add(time.Hour)
	if err := gc.PutVertexWithExpiration("alpha", &v1.Vertex{Key: "alpha", Value: &v1.Vertex_String_{String_: "beta"}}, expiration); err != nil {
		t.Fatal(err)
	}
	if err := gc.PutVertexWithExpiration("json", &v1.Vertex{Key: "json", Value: &v1.Vertex_String_{String_: `{"a":"alpha","b":"beta"}`}}, expiration); err != nil {
		t.Fatal(err)
	}
	if hits := gc.SearchVerticesMatch("alpha beta", 10, "", search.MatchOptions{}, true); len(hits) != 0 {
		t.Fatalf("synthetic cross-field phrase hits = %+v", hits)
	}
	gc.AddEdgeWithExpiration("implicit-tail", "implicit-head", 1, expiration)
	if hits := gc.SearchVertices("tail", 10, ""); len(hits) == 0 || hits[0].ID != "implicit-tail" {
		t.Fatalf("implicit endpoint hits = %+v", hits)
	}
}

func TestNewGraphCacheSearchDocumentBudgetsCoverJSONAndBytes(t *testing.T) {
	limits := search.SearchAnalysisLimits{
		MaxDocumentBytes: 7, MaxDocumentTokens: 100, MaxDocumentTerms: 100,
		MaxLiveTerms: 100, MaxLivePostings: 100, MaxPositionEntries: 100,
		CompactionRatio: 2, CompactionMinRetired: 1,
	}
	newCache := func() *graphcache.GraphCache[string, *v1.Vertex] {
		return NewGraphCache(CacheConfig{TTL: time.Minute}, SearchConfig{Enabled: true, AnalysisLimits: limits})
	}
	for _, tc := range []struct {
		name   string
		vertex *v1.Vertex
	}{
		{"JSON string values", &v1.Vertex{Key: "k", Value: &v1.Vertex_String_{String_: `{"field":"123456"}`}}},
		{"valid UTF-8 bytes", &v1.Vertex{Key: "k", Value: &v1.Vertex_Bytes{Bytes: []byte("123456")}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := newCache().PutVertex(tc.vertex.GetKey(), tc.vertex)
			var limit *search.AnalysisLimitError
			if !errors.As(err, &limit) || limit.Kind != search.LimitDocumentBytes {
				t.Fatalf("PutVertex error = %v", err)
			}
		})
	}
	invalid := &v1.Vertex{Key: "k", Value: &v1.Vertex_Bytes{Bytes: []byte{0xff, 0xfe, 0xfd}}}
	if err := newCache().PutVertex("k", invalid); err != nil {
		t.Fatalf("binary bytes should index key only: %v", err)
	}
}

func TestNewGraphCache_SearchEnabledIndexesKeyAndValue(t *testing.T) {
	gc := NewGraphCache(CacheConfig{TTL: time.Minute}, SearchConfig{Enabled: true})
	gc.PutVertex("user.preferences.tone", &v1.Vertex{
		Key:   "user.preferences.tone",
		Value: &v1.Vertex_String_{String_: "calm and concise"},
	})

	// Match by value content.
	if hits := gc.SearchVertices("calm", 10, ""); len(hits) == 0 {
		t.Errorf("expected a hit searching value text 'calm', got none")
	}
	// Match by key content (MCP search_facts parity: key words are searchable).
	if hits := gc.SearchVertices("preferences", 10, ""); len(hits) == 0 {
		t.Errorf("expected a hit searching key text 'preferences', got none")
	}
}

func TestNewGraphCache_SearchJSONStringValueFiltersStructure(t *testing.T) {
	gc := NewGraphCache(CacheConfig{TTL: time.Minute}, SearchConfig{Enabled: true})
	gc.PutVertex("user.profile", &v1.Vertex{
		Key:   "user.profile",
		Value: &v1.Vertex_String_{String_: `{"role":"administrator","city":"Tokyo","score":9000,"active":true}`},
	})

	// String values are searchable.
	if hits := gc.SearchVertices("administrator", 10, ""); len(hits) == 0 {
		t.Errorf("expected a hit searching JSON string value 'administrator', got none")
	}
	if hits := gc.SearchVertices("Tokyo", 10, ""); len(hits) == 0 {
		t.Errorf("expected a hit searching JSON string value 'Tokyo', got none")
	}

	// Field names and non-string scalars are excluded from the index. The
	// exact projected text is asserted deterministically by
	// TestVertexSearchDocument; an end-to-end negative is verified here with
	// bigram-disjoint tokens so the n-gram analyzer cannot produce an
	// incidental partial-overlap hit through the key or the string values.
	gc.PutVertex("zzzz", &v1.Vertex{
		Key:   "zzzz",
		Value: &v1.Vertex_String_{String_: `{"qqqq":"hello","wwww":4242}`},
	})
	if hits := gc.SearchVertices("qqqq", 10, ""); len(hits) != 0 {
		t.Errorf("expected no hit searching JSON field name 'qqqq', got %d", len(hits))
	}
	if hits := gc.SearchVertices("4242", 10, ""); len(hits) != 0 {
		t.Errorf("expected no hit searching JSON numeric value '4242', got %d", len(hits))
	}
}

func TestNewGraphCache_SearchDisabledReturnsNil(t *testing.T) {
	gc := NewGraphCache(CacheConfig{TTL: time.Minute}, SearchConfig{Enabled: false})
	gc.PutVertex("user.preferences.tone", &v1.Vertex{
		Key:   "user.preferences.tone",
		Value: &v1.Vertex_String_{String_: "calm and concise"},
	})

	if hits := gc.SearchVertices("calm", 10, ""); hits != nil {
		t.Errorf("expected nil hits when search index disabled, got %v", hits)
	}
}

func TestNewSearchConfig(t *testing.T) {
	c := &Config{Search: SearchConfig{Enabled: true, DefaultLimit: 7, MaxLimit: 9}}
	if got := NewSearchConfig(c); got != c.Search {
		t.Errorf("NewSearchConfig = %+v, want %+v", got, c.Search)
	}
}

// TestProductionProjectionRelevanceGate is deliberately separate from core's
// analyzer-only floor. It feeds canonical typed string vertices through the
// exact provider projection and production GraphCache composition, so key/value
// field drift changes these measured rankings.
func TestProductionProjectionRelevanceGate(t *testing.T) {
	corpora, err := relevance.Corpora()
	if err != nil {
		t.Fatal(err)
	}
	floors := map[string]relevance.Metrics{
		"en":    {NDCG10: 0.934, MRR: 1.000, Recall50: 0.969},
		"ja":    {NDCG10: 0.908, MRR: 1.000, Recall50: 0.722},
		"mixed": {NDCG10: 0.955, MRR: 1.000, Recall50: 0.770},
	}
	measured := make(map[string]relevance.Metrics, len(corpora))
	byName := make(map[string]relevance.Corpus, len(corpora))
	for _, corpus := range corpora {
		byName[corpus.Name] = corpus
		t.Run(corpus.Name, func(t *testing.T) {
			cache := NewGraphCache(CacheConfig{TTL: time.Hour}, SearchConfig{Enabled: true, Positions: true})
			expiration := time.Now().Add(time.Hour)
			for _, doc := range corpus.Docs {
				vertex := &v1.Vertex{Key: doc.ID, Value: &v1.Vertex_String_{String_: doc.Text}}
				if err := cache.PutVertexWithExpiration(doc.ID, vertex, expiration); err != nil {
					t.Fatalf("index %s: %v", doc.ID, err)
				}
			}
			metrics := relevance.Evaluate(corpus, func(query relevance.Query) []string {
				results := cache.SearchVertices(query.Text, relevance.EvalDepth, "")
				ids := make([]string, len(results))
				for i, result := range results {
					ids[i] = result.ID
				}
				return ids
			})
			measured[corpus.Name] = metrics
			t.Logf("production projection: nDCG@10=%.4f MRR=%.4f Recall@50=%.4f", metrics.NDCG10, metrics.MRR, metrics.Recall50)
			floor := floors[corpus.Name]
			if metrics.NDCG10 < floor.NDCG10 || metrics.MRR < floor.MRR || metrics.Recall50 < floor.Recall50 {
				t.Fatalf("metrics %+v below floor %+v", metrics, floor)
			}
		})
	}
	runs, err := relevance.LoadBaselineRuns(luceneBaselinePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := runs.ValidateProvenance(); err != nil {
		t.Fatal(err)
	}
	for name, run := range runs.Runs {
		if !run.Blocking {
			continue
		}
		t.Run("lucene/"+name, func(t *testing.T) {
			corpus := byName[run.Corpus]
			lucene := run.Evaluate(corpus)
			lantern := measured[run.Corpus]
			t.Logf("%s [%s]: Lucene=%+v Lantern=%+v", runs.Engine, run.Analyzer, lucene, lantern)
			const epsilon = 1e-9
			if lantern.NDCG10 < lucene.NDCG10-epsilon || lantern.MRR < lucene.MRR-epsilon || lantern.Recall50 < lucene.Recall50-epsilon {
				t.Fatalf("production projection metrics %+v below blocking Lucene %s %+v", lantern, run.Analyzer, lucene)
			}
		})
	}
}
