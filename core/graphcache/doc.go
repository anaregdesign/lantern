// Package graphcache provides Lantern's in-memory graph cache: a TTL-backed
// vertex store plus additive, expiring directed edges.
//
// GraphCache is the aggregate consistency boundary. Public write methods take
// GraphCache.mu before mutating the vertex cache, edge cache, dictionary,
// replication watermarks, tombstones, or secondary indexes. The outer rule is:
//
//	GraphCache.mu is acquired before any aggregate member is made inconsistent.
//
// The inner structures also synchronize themselves because a few hot paths
// read or compact per-edge weights without taking a write lock on the whole
// aggregate. Low-level types must not call back into GraphCache while holding
// their own locks. User callbacks have narrower contracts: ScanByPrefix
// snapshots under GraphCache.mu and invokes the visitor after unlocking, while
// ScanEdgesByPrefix invokes the visitor while holding GraphCache.mu.RLock.
//
// Mutation lifecycle, at the aggregate level:
//
//   - vertex writes intern the key, update the vertex cache, then update
//     enabled secondary indexes;
//   - vertex eviction through Delete, Clear, or Flush releases dictionary
//     references and drops prefix/search postings through the vertex cache's
//     eviction hook;
//   - edge writes auto-create endpoint vertices, mutate edge buckets, then
//     update the per-tail head index when a bucket is created or deleted;
//   - replicated writes additionally pass through HLC/tombstone guards before
//     mutating storage;
//   - GC first flushes expired vertices, then sweeps tombstones and stale HLC
//     watermarks, then removes zero-weight and dangling edges.
//
// The package deliberately keeps all index maintenance synchronous. There is no
// background indexer or asynchronous event bus: a public write returns only
// after every observable in-memory view has been updated.
package graphcache
