package graph

import (
	"context"
	"fmt"
	"testing"
	"time"
)

// BenchmarkPrefixScan measures the cost of ScanByPrefix and CountByPrefix
// at increasing cache sizes. It is the headline number for the prefix
// index Phase 1 work: every later optimization PR must keep these
// numbers from regressing.
//
// Scales: 10k / 100k / 1M live keys. The 1M case takes ~5 s to populate
// on a modern laptop and is skipped under -short.
func BenchmarkPrefixScan(b *testing.B) {
	scales := []int{10_000, 100_000}
	if !testing.Short() {
		scales = append(scales, 1_000_000)
	}
	for _, n := range scales {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			c := NewGraphCache[string, string](time.Hour)
			c.EnablePrefixIndex(identityExtract)
			// Keys are namespaced "tenant:%04d:key:%010d" so a typical
			// prefix yields ~1/1000 of the population \u2014 representative
			// of multi-tenant production patterns.
			for i := 0; i < n; i++ {
				key := fmt.Sprintf("tenant:%04d:key:%010d", i%1000, i)
				c.PutVertex(key, "v")
			}
			ctx := context.Background()
			prefix := "tenant:0042:"

			b.Run("Count", func(b *testing.B) {
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_ = c.CountByPrefix(prefix)
				}
			})
			b.Run("Scan", func(b *testing.B) {
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					c.ScanByPrefix(ctx, prefix, func(string, string, string) bool {
						return true
					})
				}
			})
			b.Run("ScanAll", func(b *testing.B) {
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					c.ScanByPrefix(ctx, "", func(string, string, string) bool {
						return true
					})
				}
			})
		})
	}
}
