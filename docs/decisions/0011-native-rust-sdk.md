# 0011: Native Rust SDK v0.1 contract

- Status: Proposed (awaiting #1375 design review)
- Date: 2026-09-26
- Issues: #1375, #1382
- API sketch and exhaustive RPC matrix: [Rust v0.1 contract](../rust-sdk-v0.1-contract.md)

## Context

Lantern serves Connect and gRPC on the same HTTP/2-capable listener. The
official Rust client should use that existing wire surface, not add a server
protocol. The Go SDK establishes plural-first CRUD, exact protobuf values,
server-authoritative Put outcomes, bounded cursor pages, and explicit Add
versus Put behavior. A native client must preserve those contracts without
importing Go modules, shipping a hidden persistence layer, or promising that a
lost write response proves the write did not happen.

The first release is a standalone Cargo library in `sdks/rust/`: package
`lantern-client`, library import `lantern_client`. It targets Tokio applications
on Linux, macOS, and Windows. Browser/WASM and a synchronous client are out of
scope. The implementation slices are #1379 (scaffold/codegen), #1376
(transport), #1377 (data), #1378 (queries), and #1381 (release).

## Decision

### Wire, package, and generated boundary

Use the maintained stable Tonic + Prost stack over gRPC/HTTP-2. Generate the
entire root [`proto/`](../../proto/graph/v1/graph.proto) schema into checked-in
`sdks/rust/src/gen/**` via one pinned `cargo xtask codegen` command. The
published library compiles from its archive without `protoc`, Buf, root proto
files, `OUT_DIR` generation, or access to another Lantern package. CI compares
a second generation with the checked-in output; generated code is never
hand-edited.

Keep generated service clients and request/response envelopes private. Re-export
generated domain types selectively, including `Vertex`, `Edge`, the `Vertex`
value oneof, bounded Put/traversal/search/status enums, and protobuf
`Timestamp`/signed `Duration`. No second persistent Vertex or Edge model.
Handwritten `VertexInput`, `EdgeInput`, `EdgeRef`, outcome/page/cursor types,
`Graph`, and errors exist only where Rust needs an input or result boundary.
Status responses are read-only SDK snapshots/wrappers, not exposed RPC
envelopes. Public Rust code uses no `unsafe` and does not use panics as an error
contract.

Pin the following **exact versions**, not open minor-version ranges, in the
scaffold. The crate uses Rust edition 2024 and declares `rust-version = "1.88"`.
Published [Tonic 0.14.6 manifests](https://github.com/grpc/grpc-rust/blob/6cb6056b5a748bc5a29bd48f4602dbc4e552bb7d/Cargo.toml)
set the whole Tonic family to Rust 1.88; Prost 0.14.4 declares 1.85
([Prost published manifest](https://docs.rs/crate/prost/0.14.4/source/Cargo.toml),
[Prost types manifest](https://docs.rs/crate/prost-types/0.14.4/source/Cargo.toml)).
Tonic 0.14.6 is not interchangeable with 0.14.5 for an MSRV claim: its
declared minimum rose to 1.88. The same-version `tonic-prost` codec and
`tonic-prost-build` generator must be paired with it; `tonic-build` alone
does not generate the Prost service client.

| Package role | Exact version | Boundary |
| --- | --- | --- |
| `tonic`, `tonic-prost`, `tonic-health`, `tonic-types` | `=0.14.6` each | Client transport/codec, bundled Health v1 client, and rich gRPC status envelope. |
| `prost`, `prost-types` | `=0.14.4` each | Generated Lantern messages, signed time values, `Any` details. |
| `tonic-prost-build`, `tonic-build`, `prost-build` | `=0.14.6`, `=0.14.6`, `=0.14.4` | Workspace codegen/xtask only; no published-crate generator dependency. |
| `protoc-bin-vendored` | `=3.2.0` (bundles protoc 31.1) | Codegen/xtask only; supplies native binaries for Linux, macOS, and Windows. |

The Tonic dependency edges are recorded in the
[release codec manifest](https://github.com/grpc/grpc-rust/blob/6cb6056b5a748bc5a29bd48f4602dbc4e552bb7d/tonic-prost/Cargo.toml)
and [generator manifest](https://github.com/grpc/grpc-rust/blob/6cb6056b5a748bc5a29bd48f4602dbc4e552bb7d/tonic-prost-build/Cargo.toml)
manifests. The vendored protoc version and platform packages are in its
[changelog](https://github.com/stepancheg/rust-protoc-bin-vendored/blob/master/CHANGELOG.md)
and [manifest](https://github.com/stepancheg/rust-protoc-bin-vendored/blob/master/Cargo.toml).
**Unresolved upstream guarantee:** `protoc-bin-vendored` 3.2.0 declares
**no `rust-version`**. #1379 must build/run codegen on Rust 1.88 on the
native OS lanes before calling the complete toolchain MSRV proven; do
not infer it from a missing manifest field. A reproducible gRPC
interop failure is required to replace this stack.

### Transport security and lifecycle

The runtime Tonic dependency disables default features and explicitly
enables `transport`, `codegen`, `tls-ring`, and `tls-native-roots`
([published feature manifest](https://github.com/grpc/grpc-rust/blob/6cb6056b5a748bc5a29bd48f4602dbc4e552bb7d/tonic/Cargo.toml)).
Ring is the chosen **SDK default** Rustls crypto backend; native OS roots
are the default trust source. A separate non-default bundled-roots feature
may enable `tonic/tls-webpki-roots` for applications intentionally selecting
Mozilla's embedded list. `rustls-native-certs` loads system CAs via
platform-specific paths on Linux, macOS, and Windows
([published platform dependencies](https://docs.rs/crate/rustls-native-certs/0.8.4/source/Cargo.toml)):

| Native OS | Default trust source | Explicit alternative |
| --- | --- | --- |
| Linux | OS certificate paths discovered by `openssl-probe` | Bundled Mozilla roots or private CA. |
| macOS | System Keychain via `security-framework` | Bundled Mozilla roots or private CA. |
| Windows | System store via `schannel` | Bundled Mozilla roots or private CA. |

A missing trust store is an error, not a reason to skip verification.
Construct each
[`ClientTlsConfig`](https://docs.rs/tonic/0.14.6/tonic/transport/channel/struct.ClientTlsConfig.html)
from `new()` with exactly one selected trust policy: call
`with_native_roots()` for the default, `with_webpki_roots()` for the opt-in
bundled policy, or `ca_certificate(...)`/`trust_anchor(...)` **without
either root flag** for private-CA-only trust. `new()` selects **no**
roots; `ca_certificate` **adds** to whatever roots were already selected,
it does not replace them. Validate that explicit roots are nonempty and
usable; never silently broaden private-CA trust with public roots. Avoid
`with_enabled_roots()` because feature unification could select both
stores even for a caller who requested only one
([root-selection source](https://github.com/grpc/grpc-rust/blob/6cb6056b5a748bc5a29bd48f4602dbc4e552bb7d/tonic/src/transport/channel/tls.rs),
[trust-store assembly](https://github.com/grpc/grpc-rust/blob/6cb6056b5a748bc5a29bd48f4602dbc4e552bb7d/tonic/src/transport/channel/service/tls.rs)).

Tonic uses an existing process-global Rustls provider if the application
installed one; otherwise `tls-ring` supplies Ring **locally**, without
requiring the SDK to install a global default
([upstream TLS construction](https://github.com/grpc/grpc-rust/blob/6cb6056b5a748bc5a29bd48f4602dbc4e552bb7d/tonic/src/transport/channel/service/tls.rs)).
Thus `tls-ring` does not override another library's already-installed
process-wide provider. Do not enable `tls-aws-lc` simultaneously or promise
that feature flags can supersede process policy.

The builder accepts exactly one explicit `http://` or `https://` endpoint.
HTTPS always verifies the peer certificate and hostname; failed TLS never
falls back to plaintext. Default HTTPS must have a working trust-root policy
on each supported OS. Private-CA trust and client mTLS are explicit options;
custom roots must not silently authorize unrelated public roots. There is
no skip-verification switch.

Plain h2c is usable for unauthenticated local/trusted-network deployments.
Supplying a bearer-token provider with an `http://` endpoint requires a
separate, explicit trusted-network/development opt-in; a plain endpoint alone
is not consent to transmit credentials. An object-safe `Send + Sync` async
provider supplies a nonempty token **for every attempt**. Provider failures
stop the call, never become an empty token, and tokens are neither persisted
nor logged. A synchronous Tonic interceptor cannot substitute for an async
provider. The `grpc.health.v1.Health/Check` readiness probe uses the same
endpoint and TLS trust but deliberately never invokes the token provider or
sends bearer metadata: Lantern mounts Health outside its auth interceptors
([listener](../../server/provider/lantern_listener.go),
[health](../../server/provider/health.go)). Use the bundled
[`tonic_health::pb::health_client::HealthClient`](https://docs.rs/tonic-health/0.14.6/tonic_health/pb/health_client/struct.HealthClient.html)
constructed directly on a bare channel, **not** the authenticated data-plane
client; no separately checked-in Health proto/codegen is needed. Only
`SERVING` succeeds.

`LanternClient` is cheaply `Clone + Send + Sync`. Each concurrent `&self` call
uses its own logical generated-client handle on a shared channel; no
client-wide mutex serializes RPCs. The token provider and contribution-ID
sequence are shared across clones. Dropping an RPC future or lazy page stream
releases its work; a dropped/canceled write may nevertheless have committed.
Dropping the final client/channel owner (including active stream owners)
releases transport resources. There is no `close()` promise that can
invalidate other clones or in-flight calls. v0.1 never discovers/rotates
endpoints; use proxy/DNS for HA, and preserve a search cursor on its issuing
endpoint.

### Exact values and expiration

Keep protobuf oneof kinds exact: `i32`, `i64`, `u32`, `u64`, `f32`, `f64`, bool,
string, bytes, nanosecond `Timestamp`, signed nanosecond `Duration`, explicit
`Nil(true)`, and an unset oneof (`None`) are different states. No implicit
numeric conversion, `nil`/unset conflation, or `std::time::Duration` for signed
**value** durations. Retain `prost_types::Timestamp { seconds: i64, nanos:
i32 }` and `prost_types::Duration { seconds: i64, nanos: i32 }` losslessly;
validate the [protobuf timestamp](https://protobuf.dev/reference/protobuf/google.protobuf/#timestamp)
and [duration](https://protobuf.dev/reference/protobuf/google.protobuf/#duration)
ranges/sign rules. `SystemTime` and nonnegative Rust `Duration` are checked
convenience conversions only, never the sole stored representation.

`Expiration::Never` emits an absent protobuf expiration. The server's
reported `LANTERN_DEFAULT_TTL_SECONDS` is **status-only**: it does not create
an expiration for an RPC write ([env contract](../env.md)); the comment on
`GetServerStatusResponse.default_ttl` in `graph.proto` is stale. A positive
relative TTL is converted to one absolute timestamp at the start of a logical
call, before chunking or retry. Zero/negative relative TTL and time overflow
are errors, not implicit permanence. `At(Timestamp)` validates the protobuf
year-1-through-9999 range and nanos, but **does not reject all times at or
before the Unix epoch**: a past deadline, including pre-epoch, epoch, or
epoch plus fractional nanoseconds, is a valid born-expired Put. The only
explicit timestamp that must fail locally is the exact protobuf equivalent
of Go's year-one zero time (`seconds = -62135596800, nanos = 0`), while
the server still uses that value as its internal omitted-expiration sentinel.
This exclusion concerns expiration only, not vertex timestamp **values**.
The current server's broader `expiration.Unix() <= 0` permanence rule is
an **existing bug**, not part of the Rust contract
([current liveness](../../core/cache/cache.go),
[wire conversion](../../server/internal/prototime/time.go)); #1469 must
merge and prove the narrower omission/explicit-expiration behavior over the
real wire before Rust v0.1 ships. If its certified behavior differs, reconcile
this ADR and the API sketch before #1375 PR/full gate; never compensate by
silently treating an explicit deadline as `Never`.

Put returns only known, index-aligned server-authoritative outcomes:
`APPLIED_AND_LIVE`, `EXPIRED`, `CONDITION_NOT_MET`, `SUPERSEDED`. Length drift,
`UNSPECIFIED`, and unknown values are protocol errors. A client's observation
may conservatively turn `APPLIED_AND_LIVE` into `EXPIRED` after its exact sent
deadline passes, but must not infer an application, change another outcome,
or claim to recover an original outcome from a retry. `AddEdge` accumulates
contributions and reports serving-node-local effective weight;
`PutEdge` replaces the weight and expiration.

### Bounded batches, cursors, and Graph

Plural Get/Put/Add/Delete is the canonical SDK path; singular public methods
forward a one-item plural operation and require one validated result. Public
singular server RPCs remain private generated methods, not a second
implementation. Default chunk size is 1,000, configurable from 1 through
65,536, also respecting a lower known server batch ceiling. A logical batch
has at most 65,536 items. Both the item count **and encoded byte size** are
bounded; an oversized single item fails explicitly instead of being sent or
truncated. Chunks execute in order. On a write failure, `BatchError` records
only the fully observed and validated **input-prefix length** and the original
cause; the failing chunk is ambiguous, not a proved rollback. Empty valid
batches have empty results.

Plural Get results are unordered found/missing **multisets**, including
duplicate request identities, not a request-index-aligned vector. Put outcomes,
Delete `existed`, and Add `effective_weights` are index-aligned; Delete
`deleted` must equal the number of true flags and Add `written` must be
bounded without inventing application counts. Unknown enums and inconsistent
responses fail closed.

Scans and search expose a bounded page plus a lazy `Send` stream, at most one
page in flight and one page buffered, never an implicit all-results
collection. Cursor bytes are opaque and bound to their request family,
order/options and issuing endpoint. Search preserves the server's
`effective_limit`, `truncated`, `continuation_limited`, projection status,
and typed stale/invalid failures. A bounded-tail stream yields its retained
hits then a terminal continuation-limited error; it does not claim a complete
result or restart page one. The SDK-local `Graph` keeps full `Edge` values
including expiration for BFS/community edges and marks synthetic PPR
relevance-star connections separately; it cannot describe those synthetic
links as stored edges.

### Deadlines, cancellation, retries, and errors

Design defaults: a 5-second connect timeout and 15-second overall unary
deadline. An explicit per-call override can shorten, lengthen, or disable
the unary deadline. Each retry attempt (including async credential lookup
and backoff) shares that **one** overall budget; cancellation during any
phase stops further attempts. Do not put a short transport-wide request
timeout on the shared channel: a v0.1 lazy page stream uses a bounded unary
deadline **per page**, not a 15-second deadline for the whole enumeration.
Use [`Endpoint::connect_timeout`](https://docs.rs/tonic/0.14.6/tonic/transport/struct.Endpoint.html#method.connect_timeout)
for dial and a client-local overall deadline plus per-attempt
[`Request::set_timeout`](https://docs.rs/tonic/0.14.6/tonic/struct.Request.html#method.set_timeout)
for the remaining server-visible gRPC timeout. Do not confuse
`Endpoint::timeout` (client-local, no `grpc-timeout` header) with an
end-to-end request budget; it would also cut off long-lived streams.
True server streams in #1380 must use explicit optional lifetime/idle
deadlines and cancellation-on-drop, with no inherited short unary timeout.
Timeout/cancellation of a mutation is an unknown server outcome, never proof
of a failed write.

Both client encoding and decoding default to a finite 16,777,216-byte
protobuf-message limit, matching Lantern's default receive/send limits
([env contract](../env.md)). Override only with a positive finite limit up
to an explicit 64-MiB SDK ceiling; a deployment with larger server limits
needs an explicit design revision. Check request byte size before sending and
reject oversize responses explicitly. Tonic's defaults are **asymmetric**:
4 MiB decoded, effectively unbounded (`usize::MAX`) encoded
([versioned docs](https://docs.rs/tonic/0.14.6/tonic/index.html)).
Configure generated client
`max_decoding_message_size` **and** `max_encoding_message_size` (including
Health) rather than relying on either default.

Retry is **off by default**. Opting in selects at most three attempts with
full-jitter exponential backoff (100 ms base, 2 s per-delay cap), on
`UNAVAILABLE` only, within one overall unary deadline. The classified
read-only unary calls (including Health and individual cursor pages) may
retry the *same request*. An unconditional Put may retry the *same encoded
value and expiration*; that is desired-state idempotency, not an original
result or global exactly-once guarantee. A Put that races another mutation
can still overwrite it, and callers must account for that. Unknown methods
and status codes never gain retries by default. `RESOURCE_EXHAUSTED`,
`DEADLINE_EXCEEDED`, `CANCELLED`, auth/permission, validation, capability,
and deterministic search-budget failures are not automatically retried.

**Never automatically retry** a receipt-less Add, even with stable 24-byte
ContribIDs: an Add can commit, lose its reply, then an intervening Delete (or
TTL expiry) can remove the retained ID; retrying recreates the contribution
([receipt decision](0010-bounded-mutation-receipts.md)). ContribIDs help
deduplicate while the contribution is live, not recover the original result.
Also never automatically retry conditional Put, exact Delete/count-returning
mutations, capped prefix Delete, or true streams after ambiguous response
loss. Dry-run prefix Deletes share the no-auto-retry classification.
Caller-supplied/prepared Add IDs and expiration may be reused only by an
application making an **explicit** reconciliation/attempt decision; there
is no v0.1 receipt guarantee or hidden outbox. The per-operation matrix is
in the [API contract](../rust-sdk-v0.1-contract.md#timeout-cancellation-and-retry-matrix).

Expose a non-exhaustive SDK error that preserves the original `tonic::Status`
(code/message and raw/unknown detail bytes) or transport/provider cause.
Decode known `SearchErrorDetail` reasons and `work_kind` from the actual
**gRPC rich error details**, not status text; unknown/malformed details
remain inspectable and cannot turn an error into success. Test disabled
search, admission/budget, and cursor errors against Lantern's real gRPC
listener, plus controlled unknown/malformed fixtures. The exact upstream
decoder is `tonic_types::pb::Status::decode(status.details())`: its
`details: Vec<prost_types::Any>` contains
`type.googleapis.com/graph.v1.SearchErrorDetail`; compare the full type URL
and decode the matching Any's bytes with the generated
`SearchErrorDetail` (`prost::Message`). Do **not** use infallible
`StatusExt::get_error_details()` as a validation step: it can default on a
decode failure and its built-in getters target standard `google.rpc.*`
types, not Lantern's custom type. Check for empty details **before** decoding:
an empty protobuf message can parse as a default `google.rpc.Status`. Absent
details remain absent; malformed nonempty details expose a decode failure.
In both cases retain the original `tonic::Status` code/message (rather than
trusting redundant fields in the rich envelope), never an invented typed reason
([upstream rich-status decoder](https://github.com/grpc/grpc-rust/blob/6cb6056b5a748bc5a29bd48f4602dbc4e552bb7d/tonic-types/src/richer_error/mod.rs),
[server detail construction](../../server/service/search.go)).

## Scope and consequences

The [RPC matrix](../rust-sdk-v0.1-contract.md#rpc-coverage-against-the-go-sdk)
includes every public LanternService RPC, Health Check, and the separate
replication service. v0.1 ships receipt-less CRUD, bounded unary queries,
read-only status, and Health. The server's receipt capability/status RPCs
and receipt-bearing mutations remain **post-v0.1**: current direct-online
Add/conditional/Delete callers cannot claim original-result recovery.
`BackupSnapshot` and the replication Subscribe/Snapshot/PeerStatus facades
belong to post-v0.1 #1380. This is an SDK scope decision, **not** a claim
that Subscribe is unavailable on a singleton: the production singleton
registers the replication service and supports CDC
([runbook](../ha-runbook.md#21-single-instance-mode-no-ha)).
`BackupSnapshot` is a different stream and works on a singleton without
a replication gate. Static client-side failover, discovery, browser/WASM,
offline storage, credential persistence, framework adapters, and peer
administration are separate future decisions.

#1469 must first certify explicit epoch/pre-epoch expiration as born-expired
and the exact year-one sentinel as rejected. #1379 proves generated-code
drift, package independence, MSRV, native
transport features, and a real h2c gRPC smoke. #1376 proves TLS/Health/auth,
large-message boundaries and typed gRPC search details on the real server.
#1377 and #1378 add real-wire happy **and** failure/edge coverage for values,
batches, scans, search, traversal and status. #1381 owns documentation,
native-platform conformance, and independent immutable crate publication.
This doc-only decision adds no proto, server, or generated-code changes.

## Rejected alternatives

- Community Connect-protocol or preview Rust gRPC stacks as the default:
  Lantern already has a gRPC-compatible HTTP/2 listener and Tonic/Prost
  provides the stable native-client path; replace it only on reproduced
  interoperability evidence.
- Public generated service clients/envelopes or duplicate Vertex/Edge
  models: both expose wire implementation detail and invite divergent value
  and outcome semantics.
- Transparent multi-endpoint retries, a client-wide `close()`, unbounded
  collection, or implicit offline replay: none can promise the required
  endpoint-sticky cursors, clone lifecycle, or ambiguous-write safety.
