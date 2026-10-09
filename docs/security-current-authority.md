# Private current authority and durable origin owner

#1722 extends the [S3-A owner](security-s3a.md) for the fixed-membership,
intact-storage scope of #1608. It is separately constructed and unwired from
public Wire, environment variables, RPCs, SDKs and Admin. The existing public
fixed-writer runtime keeps its existing meanings. S4 owns current session/control
API composition and deliberate activation; #1609/#1610 retain deployment and
final target acceptance.

The private path is actual credential verification → installed certified S1 →
current quorum renewal and native time → exact operation/purpose consume →
original H durable in P → existing quorum/contiguous Apply. A native focused
fixture exercises the actual provider, HTTPS/JWKS/Code exchange, cookie/CSRF,
TLS Connect, three M/P/B owners, signing keys and intact reopen. Fake-time unit
fixtures are explicitly separate. Neither a historical H nor an output permit
can be decoded into current authority.

## Renewal and time

A renewal uses the existing participant gate that owns ACCEPT, CHOSEN and
DRAINED. The voter requires its exact durable drained slot/prefix, capsule and
full SemanticCut, with no retained ACCEPT beyond that prefix and no undrained
choice. A higher promise, pending H or serial reservation alone does not block.
A common signed request binds the complete protocol, membership, workload and
time profiles, receiver/process nonce, unpredictable challenge and lifetime.
A compact certificate contains that request once and sorted distinct voter
signatures (at most 31 voters). Quorum intersection therefore prevents an old
head renewal after an intersecting voter retains a restrictive acceptance.

The receiver window is **15 seconds measured from its native challenge sample**.
Neither arrival, catch-up nor certificate assembly resets it. Every prefix
change, including NOOP, invalidates the active window. A separate bounded mTLS
renewal route shares the existing network owner; eight consensus turns precede
a waiting renewal turn. Background renewal does not append a holder journal.
If renewal fails, its worker tries one bounded authenticated CHOSEN range from
a rotating peer. An installed range is followed by a new challenge; a retained
ACCEPT without a QC still requires ordinary phase-one recovery. Catch-up never
relaxes the renewal quorum or reuses the old challenge lifetime.

Use requires elapsed **upper < 15 seconds**, UTC **upper < every expiry**, and
UTC **lower >= every strict start**. Equality at expiry refuses. The private
APPLIED observer starts after an authentic installed outcome and reports
cessation of new old-cut authorization only after elapsed **lower >= 15 seconds**.
A rejected operation has no successful-revocation claim. This observer does not
change the old public `globally_enforced` field or promise a last physical byte.

The implemented native adapter is the observed Darwin/arm64 profile:

| Input | Fixed conditional bound |
| --- | --- |
| Counter | `CLOCK_MONOTONIC_RAW` / `mach_continuous_time`; `kern.bootsessionuuid` plus a random process nonce; sampled regression or epoch change closes use |
| Admitted rate/read error | 1000 ppm and 1000 ns; operational assumptions, not an independently certified oscillator guarantee |
| Source | Exactly the existing `/etc/ntp.conf` host `time.asia.apple.com`; no OS time/configuration writes |
| Source/path premise | Honest conservative upstream reports and intact DNS/UDP path; **unauthenticated NTP**, not resistance to forged time packets |
| Measurement | Fresh random echoed nonce; exact 48-byte NTP v4 server packet; valid stratum/leap fields; one request per attempt |
| Source error | Root dispersion + half nonnegative root delay + precision + 10 ms allowance, at most 1 second |
| RTT | Conservative whole-flight upper strictly below 1 second; no path-symmetry or server-residence subtraction |
| Anchor | Whole-RTT conservative UTC interval; fixed UTC domain 2020–2080; conflicting live anchors fail-stop |
| Refresh/loss | 16–20 second refresh; 32–1024 second loss backoff; RATE 1024 seconds; DENY/RSTR fail-stop |
| Holdover | Age upper strictly below 60 seconds and width strictly below 4 seconds; observed source fault clears the anchor |
| Startup | At most 45 seconds, including one normal loss backoff; no anchor means no current owner |

DNS supplies at most 16 answers for the exact configured host. Each scheduled
attempt selects one sorted, deduplicated answer in rotation. A silent UDP
endpoint does not trigger a query burst or reset the backoff. The bounded latest
observation/error is diagnostic evidence, not a cached TIME_OK capability.
Every new use samples the native counter and propagates the interval locally;
no per-output NTP request occurs. Source loss, uncertainty, overflow and unknown
or changed epoch refuse new use. An unsupported native target has no Go wall-clock
fallback. This does not change the existing product's platform support.

Membership, workload TLS and current-profile OIDC HTTPS checks use both UTC
endpoints. HTTPS response checks cover reused TLS connections too. Existing
OIDC JWT verification uses the qualified upper endpoint and current admission
also checks the strict lower endpoint. Existing `core/hlc` Timestamp, Now/Update,
comparisons, data ordering and WAL/recovery paths are reused unchanged. HLC event
ordering does not substitute for source-error or elapsed-validity evidence.

## Original H and operation approval

The origin has a separately provisioned Ed25519 key, incarnation and independently
enrolled retry namespace. It cannot reuse a voting or enrolled TLS key. New
origin profile/P/H grammars are explicitly versioned and scoped; old families
are not automatically converted or mixed into a current quorum.

Before consume, the owner freezes the operation, full reviewed cut, actor,
lineage and original credential facts. It runs the existing AssessS1 and human
management checks, including effective expansion/Deny removal and last qualified
OIDC administrator preservation. It preflights pending H, terminal outcome and
P/B completion capacity. Four bounded verification slots and four operation
assembly slots prevent unbounded producer/encoder work; at most 128 prepared
requests and 16 MiB of charged retained request state are permitted, further
limited by the existing configuration.

Under the common gate, a durable reservation in the existing P family binds a
strictly increasing serial, origin/incarnation, full ID and operation digest.
Only then does the final native sample consume current authority and any exact
one-use purpose. Signing represents those immutable inputs. Exact ORIGIN_H must
cross the existing real WAL/tip durability barrier before H is returned or
acknowledged. A pause after the consume may delay that immutable completion.
Uncertain reserve/append/sync or panic closes the owner; it never restamps the ID,
review, serial or consume. Bare reservations burn serials but grant no authority.

Access evidence preserves the original #1719 signed-date presence and values;
a derived start is explicitly max(iat, numeric nbf). Ordinary human work has no
blanket auth_time age gate. Code retains genuine login versus generic step-up
versus operation approval. Native Session evidence preserves the actual session
and does not invent a JWT. Human enrollment, issuer configuration, full cut,
session and lineage are recomputed against installed S1. Machine/unresolved
credentials retain their granted reference/data-read eligibility, but cannot
mint management H. Source credential expiry and the receiver's elapsed window
are independent; no artificial short credential timestamp is manufactured.

High-impact approval shares the existing bounded ManagementAuthorizations owner
(1024 records, at most ten minutes, shortened by original expiry). A tagged
full-S1 binding carries exact full ID, operation, actor/config/lineage and review.
The genuine Code event must follow the conservative review bound; it never
refreshes the ordinary session. Final exclusive consume binds origin and serial.
The old reusable Verify cannot accept these tagged proofs. Restart discards all
volatile approvals.

H v2 binds the original typed credential/purpose facts, conservative consume
interval and upper endpoint, native profile/epoch/counter, and the compact
renewal certificate once. Historical verification samples no current time.
The existing ceilings remain H ≤ 8 MiB, header/proof ≤ 64 KiB and participant
payload below 32 MiB. Capacity sizing precedes reservation/signing and preserves
existing restrictive completion credit.

An eligible different proposer can durably retain the exact foreign H in P and
complete it after credential expiry or origin loss. It allocates no local origin
serial and does not change the namespace, evidence or purpose. First-decision
Apply may then succeed or retain an exact CAS/invariant refusal. Known exact work
and outcomes are resolved before asking for fresh purpose; local absence remains
unresolved. Historical recovery/status is distinct from current disclosure.

## Bounded output under approved A

The final event authorizes **one immutable bounded output unit**. That unit may
be physically sent or arrive arbitrarily late; there is **no send, completion or
arrival deadline guarantee**. Every subsequent unit needs its own current event.

The initial private transport profile is Connect protobuf over HTTP/1.1 TLS,
without compression. JSON, HTTP/2, gRPC and gRPC-Web are not selected by this
private constructor. This does not remove existing public protocol support.
The server uses the same enrolled TLS identity checked by the current owner,
actual accepted connection identity, at most 16 typed routes and these bounds:

| Bound | Limit |
| --- | --- |
| Live accepted connections | 64 |
| Active request/encoder owners | 8 globally; one per connection |
| Single encoded HTTP Write | 256 KiB; protobuf body capped at 256 KiB − 5 |
| Protected headers | 16 KiB, 128 names, at most 128 values per name |
| Request-owned charge | 1,310,720 bytes, reserved before handler/encoder entry |
| Connection-owned charge | 65,536 bytes |
| Global charged pool | 16 MiB, separate from P/B restrictive reserve |

The request charge covers original/detached/envelope encoding buffers, header
maps, bookkeeping and TLS/http1 buffers. Protobuf is pre-sized before encoding;
oversize unary output is rejected. Header and trailer inventories are checked
before Connect encodes them. Service errors retain their status code with one
fixed message; arbitrary error text, details and metadata are removed before
encoding. A request owns a fresh Connect handler and
its encoding pool, so no shared sync.Pool silently retains arbitrarily many
historical buffers. Stable typed service functions are reused. Request ingress
is also bounded by the private codec/read limit. The selected service remains
responsible for bounded application results before passing them to the encoder.

Each synchronous Write detaches exact encoded bytes and protected headers,
including cookies/tokens, and fixes its request credential, cut, lineage,
authorization scope, connection/recipient and sequence before the final sample.
Changes to headers after the first unit are refused; HTTP trailers are not
selected (Connect stream endings are encoded body units). Connect envelope
prefix/body writes can be separate finite units. A successful earlier part does
not promise a complete frame or authorize the next part. Serialization cannot
add new protected fields after authorization.

There is no application output queue and no unlimited stream permit. Credit is
reserved before authorization and remains held through all synchronous encoding
and transport handoff. Native TLS/socket copies already handed off are
irreversible and do not acquire a new arrival bound. Cancellation/Close closes
the owned listener/connections, joins entered producers, then clears keys and
releases M/P/B leases. Process restart cannot restore output permits.

## Evidence and remaining qualification

Paired tests cover exact interval boundaries, renewal exclusion/certificates,
serial holes and native WAL interruption, genuine credential distinctions,
full-S1 purpose ownership, capacity and closure. The real Connect tests include
blocked TCP/TLS readers, protected headers, finite encoding and late immutable
completion. An independent native-counter observer stops only an owned test
child before/after output authorization: before refuses; after sends only the
already authorized immutable unit and refuses another request. Authorization
and physical receipt are recorded separately.

The configured-source native gate records request/response packets, selected
endpoint, profile, boot/process epochs, counters, genuine origin H, Apply,
intact M/P/B reopen and exact original bytes. This demonstrates the mechanisms
under the declared source/path/rate assumptions, not universal host/VM safety.
Failed runs and negative controls remain evidence. No physical OS sleep or OS
reboot was executed on the user's working Mac. New-epoch refusal and intact
owner reopen tests do not substitute for target OS reboot/suspend qualification.
Independent review, mandatory full76, CI, exact main integration and final target
acceptance remain distinct recorded exits.

The initial scope supports intact process/OS restart and communication recovery.
Detected missing/corrupt/known-old state, identity conflicts and uncertain I/O
refuse; supplied minima are lower bounds, not a universal clone/rollback detector.
Backup/VM snapshot/clone return is deferred and unscheduled. Dynamic membership,
rotation, retirement/compaction, lost-state rejoin and automatic migration are
outside this unit. Shared typed `sys:*` consensus and `data:*` HLC/LWW remain
distinct; SystemMetadata is the bounded storage/publication boundary. There is
no arbitrary sys KV, namespace rename or cross-domain atomic transaction, and
#1668 is unchanged.
