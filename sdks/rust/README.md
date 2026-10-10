# Lantern Rust client

[`lantern-client` on crates.io](https://crates.io/crates/lantern-client) is
the published native Rust crate (`lantern_client` in Rust) for
[Lantern](https://github.com/anaregdesign/lantern). Check the registry for
available versions: features in this source tree may await the next
independently tagged Rust release and require a compatible server. It includes
a single-endpoint builder, gRPC Health `ping`, typed errors, exact-value
Vertex/Edge CRUD with bounded plural-first batches, bounded queries, typed
graph traversal, status snapshots, true server-streaming CDC and graph
backup, and bounded graph-only restore. Generated Tonic/Prost clients and
RPC request envelopes remain private; durable receipt *write/replay* and
peer replication administration are not public SDK APIs.

The crate builds independently of the Go workspace. Consumer builds use the
checked-in `src/generated/graph.v1.rs` and need neither the repository's `proto/`
files nor a system `protoc` or Buf. Rust 1.88+ and edition 2024 are required.
HTTPS validates certificates with Ring and native OS trust roots by default
on Linux, macOS, and Windows. `tls_private_ca_pem` selects private-CA-only
trust, and `tls_client_identity` presents an optional mTLS certificate/key.
The optional `bundled-roots` feature enables an explicitly selected Mozilla
trust alternative; it does not change the default.

```rust
use lantern_client::{LanternClient, RetryPolicy};

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    let client = LanternClient::builder("https://lantern.example.com:6380")
        .retry(RetryPolicy::Unavailable { max_attempts: 3 })?
        .connect()
        .await?;
    client.ping().await?;
    let second = client.clone();
    second.ping().await?;
    Ok(())
}
```

The client connects to **one** endpoint; use an HTTPS proxy or DNS for HA,
not SDK-side failover. To authenticate, implement the async `Send + Sync`
`TokenProvider` and pass `Arc<dyn TokenProvider>` to `token_provider`. It is
called for **each** data-plane attempt, including retries, but never for
Health; provider errors and invalid/empty tokens fail the call without
fallback. Bearer-over-h2c is rejected unless the caller explicitly asserts
`allow_credentialed_h2c_for_single_instance_development(true)`: that
exception is solely for trusted **single-instance development**, never
external clients of authenticated HA deployments. The client cannot
detect topology, so every authenticated HA client needs a verified HTTPS
path. HTTPS never falls back to plaintext or bypasses verification.

Connection timeout defaults to 5 seconds. Unary calls default to one
15-second overall budget; `CallOptions::After` overrides it,
`CallOptions::NoDeadline` disables it, and each attempt sends the
remaining budget as `grpc-timeout`. Encode/decode caps default to 16 MiB,
are configurable independently, and cannot exceed 64 MiB. Calls on
clones are concurrent; dropping a call or stream cancels local work,
but a canceled or timed-out mutation has an **unknown server outcome**.
There is no explicit `close`: the final channel owner releases transport
resources after outstanding calls and streams are dropped.

Retries are disabled by default. Opting in permits at most three
attempts with bounded full jitter **only** for gRPC `UNAVAILABLE` without
unrecognized/malformed rich details
on classified unary reads and byte-identical unconditional Put requests
with fixed absolute expiration. Receipt-less Add, conditional Put,
exact/capped-prefix Delete, and true server streams are never automatically
retried. An individual read-only cursor page can retry the identical
request on eligible `UNAVAILABLE`, but a lazy query stream never restarts
an enumeration or hides a failed page;
`RESOURCE_EXHAUSTED`, auth, validation, cancellation, deadlines, and
unknown methods/statuses do not gain retries. The public CRUD methods
share **one** `CallOptions` budget across all chunks and retries, not a
new 15-second window per chunk. `LanternError::Rpc` preserves the original Tonic status and raw
details; `RpcFailure::search_details()` decodes recognized search
reason/work-kind pairs without guessing from messages. Unknown or malformed
rich details remain inspectable but cannot masquerade as known reasons.

## Exact values and CRUD

```rust
use std::time::Duration;
use lantern_client::{
    AddInput, EdgeContributionRef, EdgeInput, EdgeRef, Expiration, LanternClient,
    PutOutcome, VertexInput,
};

# async fn example() -> Result<(), Box<dyn std::error::Error>> {
let client = LanternClient::builder("https://lantern.example.com:6380")
    .server_max_batch_size(10_000)?
    .auto_contribution_ids(true)
    .connect().await?;

let outcome = client.put_vertex(
    VertexInput::int64("rust:counter", 42)
        .with_expiration(Expiration::After(Duration::from_secs(60))),
).await?;
assert_eq!(outcome, PutOutcome::AppliedAndLive);
assert_eq!(client.get_vertex("rust:counter").await?.int64_value(), Some(42));

client.put_vertices([VertexInput::nil("rust:tail"), VertexInput::nil("rust:head")]).await?;
client.put_edge(EdgeInput::new("rust:tail", "rust:head", 2.0)).await?;
let prepared = client.prepare_add([
    AddInput::new(EdgeInput::new("rust:tail", "rust:head", 1.0)),
])?;
assert_eq!(client.add_prepared_edges(&prepared).await?.effective_weights, [3.0]);
assert_eq!(client.get_edge("rust:tail", "rust:head").await?.weight, 3.0);
let id = prepared.contrib_ids()[0].expect("auto_contribution_ids is enabled");
assert!(client.delete_edge_contribution(
    EdgeContributionRef::new("rust:tail", "rust:head", id),
).await?);
assert_eq!(client.get_edge("rust:tail", "rust:head").await?.weight, 2.0);
assert_eq!(client.delete_edges([EdgeRef::new("rust:tail", "rust:head")]).await?.existed, [true]);
# Ok(())
# }
```

`VertexInput` constructors and `Vertex` accessors preserve the wire's
individual `i32`/`i64`/`u32`/`u64`/`f32`/`f64`, bool, string, bytes,
nanosecond timestamp, and **signed** protobuf duration variants; there is
no numeric kind conversion. `VertexInput::nil` is explicit `Nil(true)`;
`VertexInput::unset` omits the oneof. `Nil(false)`, invalid timestamp or
duration ranges/signs, empty keys, and nonfinite *source* edge weights are
rejected before sending. An authoritative nonfinite *effective* Add weight
is returned unchanged when finite contributions overflow.

`Expiration::Never` omits the wire deadline and is **permanent**, even
when server status reports a default TTL. `Expiration::After` requires a
positive TTL and resolves to a single absolute timestamp before any
chunk/retry. `Expiration::At` accepts all valid protobuf timestamps,
including pre-epoch, epoch, and fractional first-epoch-second instants as
born-expired writes. Only the explicit year-one Go zero-time sentinel
(`seconds = -62135596800`, `nanos = 0`) is disallowed as a deadline; it
remains valid as a Vertex timestamp *value*. An accepted Put reports the
server's validated `PutOutcome`; only `AppliedAndLive` may be
conservatively downgraded to `Expired` after the exact sent deadline.
`ConditionNotMet` and `Superseded` are never rewritten by the local clock.

Plural Get returns unordered found/missing **multisets**; duplicate
requested identities may occur multiple times. Put outcomes, Delete
`existed`, and Add `effective_weights` are instead aligned with request
indexes. Singular calls use the same plural path; a missing singular Get
returns `LanternError::NotFound`. Empty valid batches return empty
results. Logical calls accept up to 65,536 items, defaulting to chunks of
1,000, with configurable `batch_chunk_size(1..=65_536)` and optional
`server_max_batch_size(...)` when the server ceiling is known. All inputs
and encoded request sizes are preflighted before a mutation starts. When
a later chunk fails, `LanternError::Batch(BatchError)` reports only the
number of **fully observed and validated earlier input items**; it does
not prove commit, rollback, or liveness of the failed chunk. An uncertain
Delete/conditional Put may have executed despite its missing response.

`put_edge` replaces the weight and expiration; `add_edge` **adds** a new
contribution. `AddInput::with_contrib_id` accepts a caller-supplied
`ContribId` (a nonzero 24-byte ID), while `auto_contribution_ids(true)`
opts into a clone-shared CSPRNG nonce and monotonic sequence. Prepared
Add inputs expose their frozen IDs and absolute expirations for caller
retention. Do not reuse IDs across distinct logical calls. A retained
ID deduplicates while its contribution remains **live**; selective Delete
fences the same ID for the D4 tombstone-retention window. An expired row
or retired tombstone may permit it to apply again. Therefore neither automatic
retry nor prepared-input replay is proof of an uncertain receipt-less
Add's original result. Only the caller can decide whether a manual
attempt is appropriate; this SDK does not implement receipt recovery.

`delete_edge_contribution(EdgeContributionRef::new(tail, head, id))` and
`delete_edge_contributions(refs)` delete only the named live Add rows, not a
Put base or other contributions. Both use the plural wire path; the batch
returns request-index-aligned `DeleteBatch { deleted, existed }`, including
duplicate IDs (true, then false) and misses. Retain a caller-supplied
`ContribId` or `PreparedAdd::contrib_ids()` before sending Add to name the row
later. An ID is **24 nonzero bytes**, not a 49-byte receipt operation ID.
Large batches respect the same chunk and encoded-size limits as other CRUD
writes. Neither selective Delete nor Add is automatically retried after an
uncertain outcome, even with an opt-in retry policy; a later Delete could
return false after the original removal succeeded.
The D4 tombstone suppresses delayed Add of the deleted ID only while retained;
use a fresh ID for new Add calls. Folded graph-only backup cannot carry
per-ID tombstones across restore, so it cannot prove the anti-resurrection
bound for a delayed Add.

## Bounded queries and traversal

Vertex, keys-only, edge, and search APIs return one bounded page or a
lazy `Send` stream of individual items. The stream keeps at most one
page buffered, fetches only when polled, and cancels a pending request
when dropped. Distinct opaque scan cursors carry SDK-owned bindings to
the issuing client endpoint, RPC family, exact prefix(es), page size, and
order; mismatches fail locally before a request. The Go server itself
only checks scan kind and vertex/key order, so raw server cursor bytes
alone do **not** prove prefix, limit, or endpoint origin and must not be
reconstructed as a bound SDK cursor. A proxy does not guarantee
physical-node affinity or a scan snapshot. Search uses its separate,
server-signed, request- and endpoint-bound session cursor. Keys-only
scans require a nonempty prefix. Count and capped
prefix Deletes make **one** call (including `dry_run`), not an implicit
delete-all loop; edge-prefix Delete needs a nonempty tail or head prefix.
Prefix counts inherit the server's best-effort visibility and may
temporarily include expired keys awaiting GC.

Search pages retain their effective server limit, truncation flag, and
continuation-limited flag. KEY_SCORE hits have no vertex; FULL_VERTEX
SNAPSHOT includes its exact value and TTL, whereas MISSING/REPLACED
never substitute a different value. A continuation-limited stream
yields the retained hits, then a typed terminal error even if the
server returned no next cursor. Other typed search failures preserve
the original gRPC status plus validated rich reason/work kind.

`TraversalOptions` selects exactly one `TraversalFamily`: BFS,
personalized PageRank, or local community. BFS step and fan-out must
both be positive; PPR `top_n=0`, community `max_size=0`, and zero
restart-probability/epsilon keep their server-defined defaults.
`Graph` maps vertices by key and edges by tail/head.
`GraphEdge::Traversed` retains the complete real edge, including its
expiration (its weight may be transformed by the selected weighting).
`GraphEdge::PprRelevance` is a synthetic mass in a seed-anchored star,
not a stored edge and not a fabricated TTL. `top_vertices_by_degree`
requires a nonempty prefix; IN/BOTH direction may still scan all
server-side edges, `O(E_total)`. `server_status` and
`replication_status` fetch snapshots only when explicitly called;
neither starts background polling.

## True server-streaming CDC

`bootstrap_identities(StreamOptions)` explicitly requests
`IDENTITY_ONLY`, an empty origin vector, and a checkpoint **before** the
registered live tail. Opening is deferred until `next_event()` is first
polled. The checkpoint is the selected responder's publication cut, **not**
a cluster-wide snapshot or proof that resident cache entries remain fresh.
Revalidate resident identities against that cut while the stream remains
open. Only after applying the invalidations and recording the cut can an
application use `IdentityCheckpoint::cursor_after_revalidation()`.
`resume_identities(CdcCursor, StreamOptions)` supplies a per-origin map of
**next expected** sequence numbers (canonical nonzero origins are 32
lowercase hex characters). Persist a completed invalidation and its cursor
**atomically** in application-owned storage. This SDK does not implement an
offline cache or persist a cursor.

```rust
use std::time::Duration;
use lantern_client::{IdentityEvent, LanternClient, StreamOptions};

# async fn example(client: &LanternClient) -> Result<(), Box<dyn std::error::Error>> {
let mut stream = client.bootstrap_identities(
    StreamOptions::default().with_idle(Duration::from_secs(5)),
).await?;
let IdentityEvent::Checkpoint(checkpoint) = stream.next_event().await? else {
    return Err("missing bootstrap checkpoint".into());
};
// Revalidate your resident identities now; the stream has already registered
// its tail. Commit that state and the checkpoint's next-expected cursor together.
let cursor = checkpoint.cursor_after_revalidation()?;
let mut resumed = client.resume_identities(cursor, StreamOptions::default()).await?;
if let IdentityEvent::Chunk(chunk) = resumed.next_event().await? {
    // Invalidate the named keys and atomically persist chunk.next_cursor
    // only when it is Some (the final chunk, including zero-key final frames).
    let _final_cursor = chunk.next_cursor;
}
# Ok(())
# }
```

Identity chunks contain **only** keys, HLC coordinates, operation category,
offsets and sequence: no Vertex value, Edge weight, contribution ID,
expiration, receipt, or bearer metadata. A mutation may span multiple
ordered chunks (at most 1,024 identities and 1 MiB per frame). A
`next_cursor` exists **only** on its final chunk; never advance a cursor
after a partial mutation. Category `DELETE_EDGE_CONTRIBUTION` (wire 7)
invalidates the named `(tail, head)` for a **re-read**; it is not a
whole-edge Delete. Category `RECEIPT_ONLY` (wire 6) has a zero-key final
chunk when a receipt caused no graph effect. Natural TTL expiration
produces **no synthetic CDC event**: use TTL checks and revalidation for
freshness.

`subscribe_full_mutations(cursor, options)` is a separate **explicit**
opt-in to `FULL_MUTATION` with `accept_receipt_envelopes=true`. Its owned
`FullMutationOp` covers every currently defined graph-only and
receipt-bearing mutation arm (including both contribution Delete forms,
accepted-expired causal barriers, and the five Vertex/Edge receipt
families). It retains origin, sequence, HLC, absolute deadlines,
contribution targets, request-index-aligned original receipt results
(including `false` Deletes and nonfinite *effective* Add weights), and
unknown-arm/malformed-frame rejection. Never log a full mutation
indiscriminately: it may expose values and receipt metadata. It is **not**
a receipt write/replay API or an all-history archive.

Both streams use the existing verified TLS/mTLS and per-open bearer provider
on their single configured endpoint; a stream never switches endpoints,
retries, or restarts in the background. `StreamOptions` has independent
optional `lifetime` and `idle` budgets (including authentication/open and
the first-frame wait); the unary 15-second budget does not apply. Dropping
a stream cancels its server subscriber. `FAILED_PRECONDITION` (including
log eviction or publication-generation changes), premature EOF, missing or
duplicate chunks, unknown categories/oneof arms, and malformed frames
fail closed with `LanternError::CdcGap` instead of skipping entries.
Transport/auth/idle failures are also terminal and retain their own errors.
Across replicas, a next-expected cursor is portable **if the selected
responder retains the origin history**; a lagging responder may remain
silent, and a gapped responder requires a fresh identity bootstrap plus
resident-identity revalidation. Full-mode gaps cannot be repaired by
pretending the current graph is full mutation history.

## Graph-only backup and bounded restore

`backup_snapshot(vertex_prefix, StreamOptions)` yields a cancellable
`BackupStream::next_record()`; empty prefix selects the whole graph and a
nonempty prefix selects vertices with that prefix plus edges whose **both**
endpoints match it. `write_backup(&mut writer, prefix, BackupFormat, options)`
streams one record at a time into caller-owned storage and returns a
`BackupManifest` **only** after clean stream EOF and a successful writer
flush. Discard partial output on any failure. The supported formats are
length-delimited `BackupSnapshotResponse` protobuf records
(`LengthDelimitedProtobuf`) and **Rust-specific** versioned NDJSON
(`RustNdjsonV1`); the latter has **not** been qualified as Go/Node NDJSON.
Every NDJSON line is the crate's canonical JSON encoding, ends in `\n`,
and contains `version:1` plus exactly one `vertex` or `edge`; duplicate or
noncanonical fields are rejected. An unset Vertex value is `null`, explicit nil is
`{"kind":"nil"}`, integers and signed timestamp/duration seconds are
decimal strings, float values and folded Edge weights use lowercase
8/16-digit IEEE bit strings, bytes use canonical standard base64, and
expiration is `null` or a `{seconds,nanos}` object.

Store the manifest **separately**, authenticate it as needed, and retain
its version/format, selected prefix, record counts, byte length, and
SHA-256 digest of the exact archive bytes. `BackupManifest::to_json()` and
`from_json()` carry this contract. A checksum proves only that the stored
archive matches the caller's manifest: the server has **no snapshot
footer/whole-dump checksum**, so clean EOF cannot independently prove that
the serving graph was transmitted completely.

`restore_backup(&mut immutable_seekable_source, &manifest, RestoreOptions)`
checks the archive length, record framing, checksum, counts, prefix,
values/expirations, **every folded Edge weight**, and individual Put message
sizes before sending any write. It then makes bounded application passes:
all `PutVertices` batches, followed by all `PutEdges` batches, even if
archive records were interleaved. Defaults are a 1,000-item batch,
64-MiB per-record limit and 16-GiB total archive limit; callers can
lower them. The source must not change between passes. This is **merge/
upsert**, not an atomic transaction or replace-all: other destination
keys/edges are not deleted. Use an empty destination for an exact
**folded graph** clone. `RestoreFailure` carries the original error and
fully validated earlier Put response counts; its failed batch might have
applied, and an expiration may lapse before a later pass.

Backup contains neither per-contribution identities nor decay history,
causal frontiers, removal floors, receipt Store/WAL, or mutation receipts.
It cannot reconstruct those from a folded Edge. Finite folded weights
are restorable through public Put; a server can fold finite contributions
into **NaN or infinity**, but public `PutEdges` rejects nonfinite source
weights. Restore therefore returns `UnsupportedBackup` **before any Put**
for such an archive (or already-expired records), rather than dropping an
Edge or claiming an exact all-weights restore. Peer `Snapshot` and
receipt-aware whole-state backup are different, private protocols.

## Secure end-to-end example

`examples/complete.rs` compiles under Rust 1.88 and stable. It requires a
certificate-verified HTTPS endpoint and a **user-chosen private prefix**.
It writes and deletes keys under that prefix and assigns short TTLs if the
run is interrupted; a prefix is not an authorization boundary. Only run
against an endpoint and namespace where you may safely change data:

```sh
LANTERN_ENDPOINT=https://localhost:6380 \
LANTERN_PREFIX=your-private-example-prefix: \
LANTERN_CA_PEM=/path/to/private-ca.pem \
cargo run --locked --example complete
```

Leave `LANTERN_CA_PEM` unset for a certificate chained to your native OS
trust roots. For mTLS, set **both** `LANTERN_CLIENT_CERT_PEM` and
`LANTERN_CLIENT_KEY_PEM` to PEM file paths. Set `LANTERN_BEARER_TOKEN`
only if the data-plane endpoint requires it; the example's provider reads
the token anew for each attempt and Health never sends it. Do not print or
commit credentials. The example demonstrates exact value/TTL Put and Get,
batch failure progress, prepared Add versus Put, a bounded keys-only scan,
optional search, all three traversal families, degree ranking, and explicit
server/replication status. It intentionally does **not** automatically
retry a potentially applied Add or Delete after a lost response.

`examples/cdc_backup.rs` uses the same verified HTTPS, optional private CA,
mTLS and per-open bearer settings. It demonstrates checkpoint-first
resident-key revalidation, explicit identity/full-mode selection, a
prefix-induced graph-only archive with a separate manifest, and an
**opt-in** two-pass restore. It never prints mutation values or credentials:

```sh
LANTERN_ENDPOINT=https://localhost:6380 \
LANTERN_PREFIX=your-private-example-prefix: \
LANTERN_BACKUP_PATH=./graph-backup.pb \
LANTERN_MANIFEST_PATH=./graph-backup.manifest.json \
cargo run --locked --example cdc_backup
```

Both paths must be new; neither file is overwritten. To restore, additionally
set `LANTERN_RESTORE_ENDPOINT` to a **different, empty, authorized** HTTPS
destination and `LANTERN_CONFIRM_EMPTY_RESTORE=yes`; the example uses the
same CA/mTLS/token settings for both endpoints. `LANTERN_RESIDENT_KEYS` is
an optional comma-separated list of private resident keys for this
demonstration, `LANTERN_SHOW_FULL_CDC=1` explicitly opts into one full
mutation, and `LANTERN_BACKUP_FORMAT=rust-ndjson-v1` chooses the Rust NDJSON
format instead of the default protobuf. This example does not persist a
cache/cursor transaction or prove server-side whole-dump integrity.

From this directory, after installing Rust and Cargo:

```sh
cargo fmt --all -- --check
cargo clippy --locked --all-targets --all-features -- -D warnings
cargo test --locked --all-features
cargo check --locked --examples
RUSTDOCFLAGS="-D warnings" cargo doc --locked --no-deps --all-features
```

From a repository checkout only, regenerate checked-in sources with
`cargo xtask codegen` and run it again to verify deterministic output.
`xtask` pins Tonic/Prost codegen and bundles a native `protoc`; it writes only
to `src/generated/` and is excluded from the library package. The lockfile is
committed for repository builds; the published library resolves its runtime
dependencies independently of that lockfile.

The opt-in real-wire suite starts the production Go server on fresh local
h2c and HTTPS ports and generates ephemeral TLS certificates. Build the
server at the repository root, then run:

```sh
mkdir -p sdks/rust/target
go build -o sdks/rust/target/lantern-smoke ./server/cmd
cd sdks/rust
LANTERN_RUST_TEST_SERVER="$PWD/target/lantern-smoke" \
  cargo test --locked --all-features -- --ignored --test-threads=1 \
    --skip scoped_changes::tests::real_public_scoped_wire \
    --skip security::tests::real_public_current_wire
```

The public scoped-CDC and current-authority cases use separately provisioned
root Go fixtures. `TestAuth_OIDCRustScopedChangesFacadeRealConnect` runs the
former; the native public SDK4 gate in
`tests/integration/current_security_gate_test.go` runs the latter and checks
that the exact Rust test executed. A standalone run does not qualify the native
current-authority case.

Current SDK4 execution is mandatory in the repository local gate and required
PR/main checks. A single native lane also gates Rust tag preflight/publication.
Its receipt binds the exact candidate, toolchains, native source configuration
and all four SDK executions; an absent/skipped case or another candidate's
receipt fails acceptance. See the [SDK4 qualification contract](../../CONTRIBUTING.md#standalone-rust-sdk-gate).

Tests exercise the explicit single-instance h2c exception, verified TLS,
mTLS, bearer rotation, authenticated two-node HA streaming, retained-log
gaps, receipt-WAL envelope negotiation, subscriber cancellation, graph
backup/restore, structured search failures, a response above Tonic's
4-MiB default, all exact values/expiration boundaries, bounded CRUD
chunks, failure reporting, and retained-ID Add semantics over the
production wire.
The `target/` build and server binary are ignored. Ordinary `cargo test`
needs neither Go nor a running server.

See [RELEASING.md](RELEASING.md) for the independent
`sdks/rust/vX.Y.Z` tag gates, crate archive/advisory checks, and the
owner-held historical first publication and protected later OIDC procedures.
Published versions and current release availability are listed on
[crates.io](https://crates.io/crates/lantern-client); source preparation
alone does not publish a new version.

This crate is licensed under Apache-2.0; see [LICENSE](LICENSE).


### Conditional connections

`create_edges` / `create_edge` and their `_with_options` variants require both
endpoint Vertices already live/readable and a directed `edge.create` Role pair.
`CreateEdgeOutcome` preserves created/collision/missing-endpoint/expired decisions
in input order; existing Edge values and endpoint TTLs are unchanged. Normal
Create is never automatically retried. The Rust SDK decodes typed Create accepted
effects in full CDC but does not expose receipt-bearing mutation dispatch. HA
rejects Create pending the cluster-wide conditional-creation guarantee.
