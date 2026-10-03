package security

import (
	"fmt"
	"testing"
)

func TestScopeCacheBounds(t *testing.T) {
	cache := newScopeCache()
	cache.maxBytes, cache.maxEntries = 2048, 3
	var retained *Scope
	for i := 0; i < 50; i++ {
		prefix := fmt.Sprintf("visible:%d:", i)
		scope := cache.get(fmt.Sprint(i), func() *Scope { return &Scope{ranges: []Range{{prefix, prefixEnd(prefix)}}} })
		if i == 0 {
			retained = scope
		}
		if len(cache.entries) > 3 || cache.bytes > 2048 {
			t.Fatal("cache exceeded bounded memory")
		}
	}
	if !retained.Contains("visible:0:1") {
		t.Fatal("eviction mutated active query scope")
	}
	builds := 0
	one := cache.get("warm", func() *Scope { builds++; return &Scope{} })
	two := cache.get("warm", func() *Scope { builds++; return &Scope{} })
	if one != two || builds != 1 {
		t.Fatal("warm scope was recompiled")
	}
}

func BenchmarkScopeCache(b *testing.B) {
	var rules []PermissionRule
	for i := 0; i < 64; i++ {
		rules = append(rules, dataRule(Allow, VertexRead, fmt.Sprintf("tenant:%04d:", i)), dataRule(Deny, VertexRead, fmt.Sprintf("tenant:%04d:private:", i)))
	}
	policy, err := CompileRoles([]Role{{ID: "reader", Rules: rules}}, DefaultPolicyLimits())
	if err != nil {
		b.Fatal(err)
	}
	access, _ := policy.ForRoles([]string{"reader"})
	b.Run("warm", func(b *testing.B) {
		access.Scope(VertexRead)
		b.ReportAllocs()
		for b.Loop() {
			if !access.Scope(VertexRead).Contains("tenant:0010:public:1") {
				b.Fatal("scope lost")
			}
		}
	})
	b.Run("cold", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			scope := access.compileScope(VertexRead)
			if !scope.Contains("tenant:0010:public:1") {
				b.Fatal("scope lost")
			}
		}
	})
}
