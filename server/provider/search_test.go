package provider

import (
	"errors"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/search"
	v1 "github.com/anaregdesign/lantern/pb/graph/v1"
)

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
			name:   "int64 value formatted decimally",
			vertex: &v1.Vertex{Key: "count", Value: &v1.Vertex_Int64{Int64: 42}},
			want:   "count 42",
		},
		{
			name:   "bool value rendered",
			vertex: &v1.Vertex{Key: "flag", Value: &v1.Vertex_Bool{Bool: true}},
			want:   "flag true",
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
			if got := vertexSearchDocument(tc.vertex).String(); got != tc.want {
				t.Errorf("vertexSearchDocument = %q, want %q", got, tc.want)
			}
		})
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
