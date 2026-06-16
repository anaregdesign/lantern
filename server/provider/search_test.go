package provider

import (
	"testing"
	"time"

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
