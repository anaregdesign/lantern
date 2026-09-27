# Lantern Rust client (unpublished)

`lantern-client` is the standalone native Rust crate (`lantern_client` in Rust)
for [Lantern](https://github.com/anaregdesign/lantern). It includes a public
single-endpoint connection builder, gRPC Health `ping`, typed errors, and
exact-value Vertex/Edge CRUD with bounded plural-first batches, bounded
queries, typed graph traversal, and explicit status snapshots. Generated
Tonic/Prost clients and RPC envelopes remain private. Receipt and
long-lived streaming APIs are tracked in
[ADR 0011](https://github.com/anaregdesign/lantern/blob/main/docs/decisions/0011-native-rust-sdk.md)
and later issues. Do not treat this source as a published release.

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
    AddInput, EdgeInput, EdgeRef, Expiration, LanternClient, PutOutcome, VertexInput,
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
ID deduplicates only while its contribution remains **live**; Delete or
expiry permits that same ID to apply again. Therefore neither automatic
retry nor prepared-input replay is proof of an uncertain receipt-less
Add's original result. Only the caller can decide whether a manual
attempt is appropriate; this SDK does not implement receipt recovery.

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
  cargo test --locked --all-features -- --ignored --test-threads=1
```

The scaffold smoke intentionally sends no bearer token. Other tests exercise
the explicit single-instance h2c exception, verified TLS, mTLS, auth,
structured search failures, a response above Tonic's 4-MiB default, all
exact values/expiration boundaries, bounded CRUD chunks, failure reporting,
and retained-ID Add semantics over the production h2c wire.
The `target/` build and server binary are ignored. Ordinary `cargo test`
needs neither Go nor a running server.

See [RELEASING.md](RELEASING.md) for the independent
`sdks/rust/vX.Y.Z` tag gates, crate archive/advisory checks, and the
owner-held first-publication versus later OIDC procedures. No release
has been authorized or published yet.

This crate is licensed under Apache-2.0; see [LICENSE](LICENSE).
