# Rust SDK v0.1: API sketch and RPC coverage

Status: Proposed design for #1375 (implementation belongs to #1379, #1376,
#1377, #1378, and #1381). The binding behavior and deferred boundaries are
recorded in [ADR 0011](decisions/0011-native-rust-sdk.md); names below are an
API sketch, **not** checked-in compilable Rust.

## Public boundary and builder

The library is `lantern-client` (`lantern_client` in Rust). `gen` is a
**private** module. The crate selectively re-exports generated domain
`Vertex`, `Edge`, `vertex::Value` as `VertexValue`, `PutOutcome`, traversal
enums, scan/search/projection/status enums and `SearchCapabilities`; it does
not publicly expose generated RPC clients or request/response messages.
`Timestamp` and signed `ProtoDuration` are direct re-exports of
`prost_types` well-known messages, not chrono or `std::time::Duration`
substitutes. Immutable `ServerStatus` and `ReplicationStatus` wrappers
expose snapshot accessors without leaking their RPC envelopes.
The exact patch/edition/MSRV/toolchain choices live in
[ADR 0011](decisions/0011-native-rust-sdk.md#wire-package-and-generated-boundary):
Tonic/its codec/Health/rich-status/build family `=0.14.6`, Prost
`=0.14.4`, vendored protoc `=3.2.0`, edition 2024 and Rust 1.88.
The vendored generator declares no upstream MSRV and needs a #1379
Rust-1.88 CI proof.

Illustrative shape (all public operations are async except preparation and
configuration):

```rust
pub trait TokenProvider: Send + Sync {
    fn token(&self) -> Pin<Box<
        dyn Future<Output = Result<String, TokenError>> + Send + '_
    >>;
}

pub struct LanternClientBuilder { /* one endpoint, private configuration */ }
impl LanternClient {
    pub fn builder(endpoint: impl AsRef<str>) -> LanternClientBuilder;
}
impl LanternClientBuilder {
    pub fn token_provider(self, provider: Arc<dyn TokenProvider>) -> Self;
    pub fn allow_insecure_credentials_for_trusted_network(self, yes: bool) -> Self;
    pub fn connect_timeout(self, timeout: Duration) -> Result<Self, LanternError>;
    pub fn unary_timeout(self, timeout: Duration) -> Result<Self, LanternError>;
    pub fn message_limits(self, encode: usize, decode: usize)
        -> Result<Self, LanternError>;
    pub fn batch_chunk_size(self, items: usize) -> Result<Self, LanternError>;
    pub fn retry(self, policy: RetryPolicy) -> Result<Self, LanternError>;
    pub async fn connect(self) -> Result<LanternClient, LanternError>;
}
```

`tls(...)` defaults to `tonic/tls-ring` plus `tonic/tls-native-roots`;
an optional `tonic/tls-webpki-roots` feature supports an explicitly selected
bundled root policy. Call the versioned Tonic
[`ClientTlsConfig::with_native_roots`](https://docs.rs/tonic/0.14.6/tonic/transport/channel/struct.ClientTlsConfig.html)
or `with_webpki_roots` for only that store; private-CA-only trust starts
from `ClientTlsConfig::new().ca_certificate(...)` without either root flag.
`ca_certificate` **adds**, never replaces existing roots; do not call
`with_enabled_roots` for policy selection. Optional mTLS identity is
independent of root selection
([ADR](decisions/0011-native-rust-sdk.md#transport-security-and-lifecycle)).
`http://` plus credentials fails
`connect()` without the separate opt-in. `https://` always verifies and
never downgrades. `TokenProvider::token()` is called for each data-plane
attempt, **not** for `ping()`: use
[`tonic_health::pb::health_client::HealthClient`](https://docs.rs/tonic-health/0.14.6/tonic_health/pb/health_client/struct.HealthClient.html)
on the bare channel, without copying bearer metadata. The crate supplies
the generated Health v1 client; no separate Health proto is needed. Empty
tokens/provider errors fail; no credential logging, storage, or global
interceptor. A cloned client shares
one channel/provider/ID sequence but has no `close()` method; concurrent
calls take `&self` and do not serialize on a client-wide lock.

The default connection budget is 5 seconds; the overall unary budget is 15
seconds. `CallOptions` expresses `Default`, explicit `After(Duration)`,
and explicit `NoDeadline`; dropping the returned future cancels client
work but leaves a write's server outcome unknown. One lazy scan/search
stream can live longer than 15 seconds because each page has its own
unary budget. A true network stream (#1380) never inherits that default.
Builder encode/decode caps default to 16,777,216 bytes each and allow
positive values only through 64 MiB. Invalid bounds return errors instead
of clamping silently. Both generated client codec limits must be set:
[Tonic 0.14.6 defaults](https://docs.rs/tonic/0.14.6/tonic/index.html)
to a 4-MiB decode cap but effectively unlimited encoding. An
[`Endpoint::connect_timeout`](https://docs.rs/tonic/0.14.6/tonic/transport/struct.Endpoint.html#method.connect_timeout)
controls dialing; the unary budget is client-local and also sets
the per-attempt server-visible
[`grpc-timeout`](https://docs.rs/tonic/0.14.6/tonic/struct.Request.html#method.set_timeout)
header to the remaining budget.

## Exact values, TTL, and CRUD

```rust
pub enum Expiration { Never, At(Timestamp), After(Duration) }
pub struct VertexInput {
    pub key: String,
    pub value: Option<VertexValue>, // None = unset; Some(Nil(true)) = explicit nil
    pub expiration: Expiration,
}
pub struct EdgeInput {
    pub tail: String,
    pub head: String,
    pub weight: f32,
    pub expiration: Expiration,
}
pub struct EdgeRef { pub tail: String, pub head: String }
pub struct GetBatch<T, Id> { pub found: Vec<T>, pub missing: Vec<Id> }
pub struct DeleteBatch { pub deleted: usize, pub existed: Vec<bool> }
pub struct AddBatch { pub written: usize, pub effective_weights: Vec<f32> }
pub struct BatchError { pub completed_items: usize, pub source: Box<LanternError> }
```

The oneof preserves all wire widths (`i32/i64/u32/u64/f32/f64`), bool,
string, bytes, signed protobuf duration and nanosecond timestamp. Validate
the full protobuf well-known ranges and duration sign rules, including
negative durations and pre-epoch **value** timestamps. `Nil(false)` is
invalid, not a synonym for unset. There is no lossy int/float coercion.

`Never` omits the expiration field, mapping to permanent Go zero-time;
the status-reported default TTL
does **not** apply to writes ([env.md](env.md)). `After` requires a positive
duration and is converted **once** to a checked absolute timestamp before
chunking or any retry. `At` accepts valid protobuf timestamps, including
pre-epoch, epoch, and positive fractional first-epoch-second deadlines,
except the exact year-one Go zero-time sentinel
(`seconds = -62135596800, nanos = 0`). Explicit past deadlines yield a
born-expired Put rather than permanence. The former server behavior treating
`Unix() <= 0` as permanent was fixed in #1469 (merged as PR #1475).
Real-wire tests qualify omitted/Go-zero permanence, explicit
pre-epoch/epoch/fractional first-second born-expired Puts, and explicit
year-one rejection; do not reintroduce the old local `At` guard
([ADR rationale](decisions/0011-native-rust-sdk.md#exact-values-and-expiration)).
After an accepted Put, only `APPLIED_AND_LIVE` can be conservatively
downgraded to `EXPIRED` when the exact sent deadline has passed. Do not
replace a server `CONDITION_NOT_MET` or `SUPERSEDED` result with a guess.

Method families and result shapes:

| Public Rust family | Return | Invariant |
| --- | --- | --- |
| `get_vertex`, `get_edge` | `Vertex` / `Edge` or typed NotFound | One-item plural facade; present explicit nil is **not** missing. |
| `get_vertices`, `get_edges` | `GetBatch<..., ...>` | Found and missing are unordered identity **multisets**, not index-aligned; validate duplicates and exact membership. |
| `put_vertex`, `put_vertex_if_absent`, `put_edge` | Validated `PutOutcome` | Singular calls reuse plural write/result logic and demand exactly one known outcome. |
| `put_vertices`, `put_vertices_if_absent`, `put_edges` | Request-index-aligned `Vec<...PutResult>` | Fail on outcome length/enum drift; zero/empty batches return empty results. `if_absent` checks existing **live** vertices. |
| `delete_vertex`, `delete_edge` | `bool` | Reuse plural Delete; false is absent, not a transport error. |
| `delete_vertices`, `delete_edges` | `DeleteBatch` | Validate `existed.len == input.len` and `deleted == count(true)` including duplicates. |
| `add_edge`, `add_edges` | Effective `f32` / `AddBatch` | Add is additive, not Put. Validate exact `effective_weights` alignment and bounded `written`; effective weights are serving-node-local. |

Chunk size defaults to 1,000 items and is configurable in `1..=65_536`
(and no more than a known lower server cap). Logical calls accept at most
65,536 items. The SDK also checks encoded-byte limits, so item counts do
not permit an oversized request; one oversized item fails explicitly.
Only **fully observed and validated earlier chunks** increase
`BatchError.completed_items`. The failed chunk might already have
committed, and that count is neither the number of live outcomes nor proof
of rollback. Read batches fail without success-shaped partial results.

Add supports caller-supplied `ContribId([u8; 24])` and an explicit
`prepare_add` helper that exposes the IDs and freezes relative expiration
**before** sending a `PreparedAdd`. Generated IDs use a CSPRNG 16-byte
per-client nonce shared by clones plus a checked, monotonic 48-bit sequence:
the last eight bytes are big-endian `(seq << 16) | index`, with a
16-bit item index. Malformed, all-zero, duplicate-in-call and
counter-overflow IDs fail locally. Prepared input can be retained by the
caller for an **explicit** decision after response loss, but the SDK
never automatically replays Add, even with the same ID: Delete/expiry
can end the live dedup horizon. Receipt-bearing recovery is not v0.1.

## Bounded queries and typed errors

`scan_vertices_page`, `scan_vertex_keys_page`, `scan_edges_page`, and
`search_vertices_page` return one bounded page (`items`/`hits`,
`next_cursor`). Corresponding lazy `*_stream` methods yield
`Stream<Item = Result<..., LanternError>> + Send`; they fetch no
more than one page ahead, keep only one page buffered, and drop pending
work when the consumer drops the stream. The public cursor types are
separate opaque wrappers for vertex, keys, edge, and search cursors.
Cursor bytes can be persisted verbatim but never cross-fed to a
different RPC, order, filter, options, projection, or endpoint.

`SearchPage` includes `hits`, `next_cursor`, `effective_limit`,
`truncated`, and `continuation_limited`. `SearchHit` exposes its
generated projection status: `KEY_SCORE` has no vertex;
`SNAPSHOT` carries an exact vertex/TTL; `MISSING`/`REPLACED` may not
fabricate one. A stream emits retained hits then a terminal typed
continuation-limited error when the server could not retain the tail.
It never silently restarts a stale/invalid cursor. Search options
validate incompatible phrase/match/fuzziness combinations locally.

`illuminate(seed, TraversalOptions)` selects exactly one family:
`Bfs { step, fan_out, objective, reduction }`,
`Ppr { top_n, restart_prob, epsilon }`, or
`LocalCommunity { max_size, restart_prob, epsilon, reduction,
objective }`, with shared weighting and vertex prefix. BFS dimensions
must be positive; preserve wire-defined zero sentinels for the other
families. The SDK-local `Graph` keys vertices and tail/head edges;
`GraphEdge::Traversed(Edge)` retains real edge identity/expiration
(weight may reflect server-selected weighting), while
`GraphEdge::PprRelevance { mass }` identifies a synthetic star
connection with no invented stored expiration.

`count_vertices_by_prefix`, bounded `delete_vertices_by_prefix` and
`delete_edges_by_prefix` (with explicit `dry_run`), and
`top_vertices_by_degree` are single-call operations, **not** implicit
drain loops. Keys-only scans and degree ranking require nonempty
vertex prefixes; edge-prefix Delete requires a nonempty tail or head
prefix. IN/BOTH degree can scan every edge in the server even with a
narrow prefix. `server_status()` and `replication_status()` are
explicit snapshots, never background polling.

`LanternError` is non-exhaustive. A gRPC error retains its source
`tonic::Status`, code, message and original raw details; transport
and token-provider failures preserve their causes. It exposes known
search `SearchErrorDetail.reason` and `work_kind` decoded from rich
gRPC trailers, including disabled/positions, work/admission/index
budgets, incomplete index, stale/invalid cursor, and bounded tail.
Malformed/unknown details remain available (with a decode failure)
but produce **no invented typed reason**. A wrong response length,
missing field, inconsistent count, or unknown Put/projection enum
is a protocol error. `ping()` uses gRPC Health v1 without fetching
or sending a bearer token; only `SERVING` succeeds. The exact
rich-status path is
`tonic_types::pb::Status::decode(status.details())` for nonempty bytes,
followed by a full `Any.type_url` match on
`type.googleapis.com/graph.v1.SearchErrorDetail` and
`SearchErrorDetail::decode(any.value.as_slice())`. Preserve the outer
gRPC status even when rich details are absent, invalid, or unknown
([upstream implementation](https://github.com/grpc/grpc-rust/blob/6cb6056b5a748bc5a29bd48f4602dbc4e552bb7d/tonic-types/src/richer_error/mod.rs),
[server detail](../server/service/search.go)).

## Timeout, cancellation, and retry matrix

All methods are cancelable by dropping the future/stream; that
cannot prove whether a mutation committed. Retry is opt-in, at most
three total tries with full jitter and one shared overall deadline.
Only `UNAVAILABLE` is eligible; unknown methods/codes fail closed.

| Category | Deadline | Automatic retry when enabled | Response-loss rule |
| --- | --- | --- | --- |
| Connect/build, TLS and token acquisition | 5 s connect; token lookup inside unary budget | Never retry invalid TLS, auth or provider errors | No plaintext downgrade or silent empty token. |
| Health/Check | 15 s unary or caller override | Same unary Health request on `UNAVAILABLE` | Never invoke token provider; `NOT_SERVING` is a failure. |
| Get(s), scans/keys/edges **page**, search page, count, traverse, degree, status | 15 s unary or caller override | Same request on `UNAVAILABLE` only | Search continuation uses same endpoint/cursor; no auto restart. |
| Lazy scan/search stream of unary pages | 15 s **per page**; optional caller lifetime budget | Individual page only, same cursor on `UNAVAILABLE` | No whole-stream restart, implicit collection, or cursor substitution. |
| Unconditional Put Vertex/Edge (singular/plural) | One 15 s logical unary budget across chunks/attempts unless overridden | Same frozen protobuf payload on `UNAVAILABLE` | Desired-state replay may overwrite another write; does not recover original outcome. |
| Conditional Put; exact Vertex/Edge Delete and returned `existed`/`deleted` | Unary budget as above | **Never**, even though unconditional Delete's final state could look idempotent | The original condition/count is unknown after loss. |
| Receipt-less Add (with or without stable ContribID) | Unary budget as above | **Never** | Delete/TTL can remove ID before retry, recreating the contribution. |
| Prefix Delete (including `dry_run`) | 15 s one-call unary | **Never** | Capped replay can target a different page; callers reconcile explicitly. |
| True `BackupSnapshot`/replication streams (#1380, post-v0.1) | Optional explicit lifetime/idle budget, not the unary default | **Never** | Drop cancels; caller owns durable progress/integrity. |
| Receipt/status APIs (post-v0.1) | To be specified with receipt continuity policy | **Never** in v0.1 | No receipt proof or original-result lookup is claimed. |

`RESOURCE_EXHAUSTED`, `DEADLINE_EXCEEDED`, `CANCELLED`, invalid input,
auth/permission, deterministic search work budget, and all unknown
status codes are never automatically retried. Exhausted/canceled
retry budgets and writes with ambiguous responses surface errors, not
success-shaped defaults.

## RPC coverage against the Go SDK

One row per RPC in
[`LanternService`](../proto/graph/v1/graph.proto), plus the Health
and replication services. "Facade" means a public Rust SDK operation;
the generated wire client stays private. The Go column names a public
Go SDK method where one exists, **not** a promise of identical helper
APIs or retry policy.

| RPC | Go SDK counterpart | Rust v0.1 facade / status | Contract |
| --- | --- | --- | --- |
| `Illuminate` | `Illuminate` | `illuminate` | Exactly one BFS/PPR/community family; TTL-preserving Graph. |
| `GetVertex` | `GetVertex` | `get_vertex` (one-item plural facade) | Missing -> typed NotFound; nil value remains present. |
| `GetVertices` | `GetVertices` | `get_vertices` | Unordered found/missing multiset, including duplicates. |
| `PutVertex` | `PutVertex`, `PutVertexIfAbsent` | `put_vertex`, `put_vertex_if_absent` (plural facade) | Exactly one validated Put outcome. |
| `PutVertices` | `PutVertices`, `PutVerticesIfAbsent` | `put_vertices`, `put_vertices_if_absent` | `if_absent`; exact request-index-aligned outcomes. |
| `DeleteVertex` | `DeleteVertex` | `delete_vertex` (plural facade) | One validated existed flag; no automatic retry. |
| `DeleteVertices` | `DeleteVertices` | `delete_vertices` | Exact existed vector, deleted count, partial-prefix progress. |
| `ScanVertices` | `ScanVertices`, `ScanVerticesAll` | `scan_vertices_page`, `scan_vertices_stream` | Bounded pages, order-bound cursor. |
| `ScanVertexKeys` | `ScanVertexKeys`, `ScanVertexKeysAll` | `scan_vertex_keys_page`, `scan_vertex_keys_stream` | Nonempty prefix; distinct cursor kind. |
| `SearchVertices` | `SearchVertices`, `SearchVerticesPage`, `SearchVerticesIter` | `search_vertices_page`, `search_vertices_stream` | Typed reasons, projection, retained bounded-tail error. |
| `CountVerticesByPrefix` | `CountVerticesByPrefix` | `count_vertices_by_prefix` | `u64` count; read-only. |
| `DeleteVerticesByPrefix` | `DeleteVerticesByPrefix` | `delete_vertices_by_prefix` | Bounded one shot/dry run; never auto-retry. |
| `TopVerticesByDegree` | No public Go SDK wrapper (raw RPC) | `top_vertices_by_degree` | Required prefix; IN/BOTH may cost O(all edges). |
| `GetEdge` | `GetEdge` | `get_edge` (plural facade) | Full Edge/expiration or typed NotFound. |
| `GetEdges` | `GetEdges` | `get_edges` | Unordered identity multiset; complete Edge. |
| `AddEdge` | `AddEdge`, `AddEdgeAt` | `add_edge` (plural facade) | Additive local effective weight; never auto-retry. |
| `AddEdges` | `AddEdges` | `add_edges` | Index-aligned effective weights; optional prepared ContribIDs; never auto-retry. |
| `PutEdge` | `PutEdge`, `PutEdgeAt` | `put_edge` (plural facade) | Idempotent replacement; known outcome only. |
| `PutEdges` | `PutEdges` | `put_edges` | Exact outcome vector, weight **replacement**. |
| `DeleteEdge` | `DeleteEdge` | `delete_edge` (plural facade) | One existed flag; no automatic retry. |
| `DeleteEdges` | `DeleteEdges` | `delete_edges` | Existed vector/count; no automatic retry. |
| `DeleteEdgesByPrefix` | `DeleteEdgesByPrefix` | `delete_edges_by_prefix` | At least one prefix; capped/dry-run; no auto-retry. |
| `ScanEdges` | `ScanEdges`, `ScanEdgesAll` | `scan_edges_page`, `scan_edges_stream` | Two prefixes, one opaque edge cursor. |
| `GetServerStatus` | `GetServerStatus` | `server_status` | Explicit snapshot, including reported-only default TTL. |
| `GetReplicationStatus` | `GetReplicationStatus` | `replication_status` | Explicit snapshot; singleton has `enabled=false`, no peers. |
| `GetReceiptCapability` | `GetReceiptCapability` | **Deferred: receipt-specific follow-up** | Current wire exists; v0.1 makes no durable-receipt claim. |
| `GetReceiptStatus` | `GetReceiptStatus` | **Deferred: receipt-specific follow-up** | Original-result lookup is not equivalent to a live Get. |
| `GetReceiptStatuses` | `GetReceiptStatuses` | **Deferred: receipt-specific follow-up** | Indexed original results need epoch/continuity validation. |
| `BackupSnapshot` | `Backup` (`Restore` replays plural Put) | **Deferred: #1380** | Server-streaming on singleton too; no restore RPC. |

| Other service RPC | Go SDK counterpart | Rust v0.1 facade / status | Contract |
| --- | --- | --- | --- |
| `grpc.health.v1.Health/Check` | `Ping` (Connect+JSON) | `ping` via gRPC Health v1 | Auth-exempt; require `SERVING`, no bearer/provider. |
| `LanternReplicationService.Subscribe` | `Subscribe`, `BootstrapIdentity`, `SubscribeIdentity` | **Deferred: #1380** | Wire service is registered on the production singleton and provides CDC; Rust stream facade deferred. |
| `LanternReplicationService.Snapshot` | No public Go SDK peer Snapshot facade | **Deferred: #1380** | Internal replication bootstrap, not `BackupSnapshot`. |
| `LanternReplicationService.PeerStatus` | No public Go SDK peer-status facade | **Deferred: peer administration** | Not `GetReplicationStatus`; not an application v0.1 RPC. |

Go-only helpers (`Graph.Render`, `AddDecayingEdge`, JSON marshaling,
`NewIncrementalSearch`, `NewLanternFailover`, and receipt-bearing
`*WithReceipt` variants) are not independent RPCs. v0.1 intentionally
does not promise these helpers. The receipt-bearing variants of
`PutVertex`, `DeleteVertex`, `DeleteEdge`, and `AddEdge` are also
deferred even though their optional `receipt_context` shares the
same four RPC names in the table; v0.1 sends no receipt context.
The supported singleton `Subscribe` wire surface and available
single-node `BackupSnapshot` stream remain distinct.
