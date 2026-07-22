# Changelog

## 0.1.0

- Initial experimental storage-neutral offline Repository core.
- Add strict canonical v1 cache/outbox codecs and fresh-process conformance
  snapshots.
- Add partition/generation-safe confirmed caching, negative markers,
  cache-first revalidation, and coalesced local snapshot watches.
- Add atomic plural Put/PutEdge/stable-ID Add enqueue, pending overlays,
  expiration-safe foreground replay, probe-gated drain, and process-local
  status streams.
- Add global/per-partition capacity limits, lease recovery, content-free
  diagnostics, partition wipe, and authorized dead-letter controls.
- Add real-Lantern committed-response-loss coverage and maintained Flutter
  cached/pending/replay UX.
