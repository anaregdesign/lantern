# Current authority and public activation

#1722 supplies the fixed-membership, intact-storage owner. #1725 connects that
same owner to the public control/session/data APIs under the explicit
`current-v2` profile. `legacy-v1` retains the prior fixed-writer meanings; the
two profiles never share authority or import live state. #1609 owns deployment
and cutover, and #1610 owns final target acceptance. The source and focused
fixtures below do not claim either operational exit.

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

The private native factory supports distinct Darwin/arm64 and Linux/amd64/arm64
profiles. The Linux target is a normal container with a continuously running
host/guest kernel. Neither physical host sleep/suspend with live resume nor VM
snapshot/rollback/clone/live migration is supported. Container pause/unpause,
scheduler delays and CPU throttling remain in scope. These are conditional
operating profiles, not kernel or hardware certification.

| Input | Fixed conditional bound |
| --- | --- |
| Darwin counter/epoch | `CLOCK_MONOTONIC_RAW` / `mach_continuous_time`; `kern.bootsessionuuid` plus a fresh owner/process nonce |
| Linux counter/epoch | `CLOCK_MONOTONIC_RAW`; bounded canonical `/proc/sys/kernel/random/boot_id`; fresh owner/process nonce; fixed zero-offset time namespace |
| Native failures | Missing/malformed metadata, syscall error, counter overflow/regression or changed boot/namespace fail-stop the sampler; no wall-clock fallback |
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

Linux `CLOCK_MONOTONIC_RAW` follows the underlying clocksource without NTP
frequency discipline and excludes host suspend. It continues while container
processes are frozen, provided the kernel/VM clock continues. The admitted
1000 ppm rate and 1000 ns sample error are explicit requirements on that chosen
host/guest clocksource; the API name, one measurement and Docker do not establish
those limits. Detected contradictory source intervals or sampled clock faults
close use. Undetected common clock/source failure is outside these premises.
See the [kernel timekeeping contract](https://www.kernel.org/doc/html/latest/core-api/timekeeping.html).

The Linux sampler pins each observation to one OS thread and compares its time
namespace with the process leader's current and child namespaces. All must match
one fixed identity and expose zero monotonic/boottime offsets. It reads the
leader's `/proc/self/timens_offsets`, which describes its child namespace.
A kernel exposing these procfs interfaces and readable boot identity is required;
missing support refuses. No `setns`, namespace creation, clock write or capability
is used. The deployment must prohibit time-namespace changes throughout the
process lifetime; sampling is not an atomic detector for arbitrary privileged
concurrent namespace manipulation. See [Linux time namespaces](https://man7.org/linux/man-pages/man7/time_namespaces.7.html).

The exact Linux profile description is separate from Darwin's unchanged
identity. It consequently binds a different admission/trust scope in renewal
and historical H. A cohort must use one matching profile; this change provides
no Darwin-to-Linux persistent-family migration or mixed-profile quorum. The
source configuration is an explicit read-only `/etc/ntp.conf` deployment input,
not an assumed container image feature. It retains the already selected single
`time.asia.apple.com` upstream and the existing producer/interval bounds.

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
reboot was executed on the user's working Mac. Physical sleep/live resume is
outside the current scope, and a Mac reboot is not a completion gate. Host reboot
recovery is conditional on intact persistent state and native kernel/storage
semantics: all volatile anchors, renewals, purposes and output permits disappear;
a new process reopens the independently provisioned original genesis, keys,
M/P/B and minima, then acquires fresh native time and quorum authority. Boot-epoch
injection checks complement same-volume whole-process recovery. Neither a
container restart nor a SIGKILL observes power-loss flush behavior or the actual
host boot sequence.
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


## Bounded Linux container campaign

`control_container_gate_test.go` and its test-only helper connect the production
private constructor to three actual TLS owners, genuine OIDC/JWKS credentials,
original genesis, intact M/P/B, separate identity keys and retained floors.
The opt-in `.github/scripts/current_authority_container.py` controller uses a
prebuilt static Linux test binary and an already cached immutable image ID.
It creates only new labeled task containers, a network and a retained volume;
workers run as UID/GID 65534 with no capabilities, a read-only root filesystem
and no Docker socket. Test keys remain in that task volume.

The accepted evidence is split explicitly: five actual configured-source
production-constructor cases, plus one fixture/native source-loss mechanism
case. Final-candidate success of the former also supplies actual source/native
constructor/current/quorum compatibility; no duplicate public-source outage is
required. A fixture-only pass cannot qualify the production source. Previous
combined campaign failures remain historical failures.

The six bounded cases are graceful SIGTERM/Close/restart, SIGKILL/recreate on the
same volume, Docker pause/unpause before and after final output authorization,
peer outage and controlled time-source loss/recovery. Peer outage closes actual
newly accepted TCP sockets before TLS on two enrolled listeners; it is a test listener fault,
not Docker bridge isolation. Time-source loss uses one test-only loopback UDP
socket that continues reading requests while dropping responses, then replies to fresh requests on the same
socket. Three separate real Linux samplers/producers feed the unchanged time
owner run loop and private owners. The fixture supplies a fixed synthetic UTC
base propagated by its native counter; it is not UTC accuracy evidence and does
not replace the production source. Production clock injection remains rejected. The fixture records request/response nonces, each actual attempt and retry gap,
last successful anchors and native elapsed bounds. Successful sequences remain
unchanged during loss; after holdover each current sample, fresh renewal and
new Consume refuses. Recovery keeps boot/process identity but requires a new
measurement/anchor and quorum challenge before Consume/Apply. Native source
refresh/backoff, credential validity, quorum and interval bounds are unchanged. Explicit test
hooks disable automatic renewal so an expired challenge cannot be replaced
before its rejection assertion; successful recovery uses the ordinary real
quorum path. No injected clock enters the production constructor.

Restart compares exact original H and applied outcome bytes, retains serials
and floors, rejects old volatile authority, and completes retained pending H
after its original credential expiry without manufacturing new consent. Fresh
work needs new native time and renewal and a strictly greater origin serial.
The bootstrap predates the first journal and is never recovered by treating a
later replayed state as genesis. Pause cases record authorization separately
from physical completion: approved A permits the exact preauthorized unit to
finish late, while another unit requires fresh authority. A transport failure
is reported separately from a delivered immutable unit.

Volume survival is distinct from durability. These results require the selected
filesystem/device stack to honor the existing WAL, tip and directory sync
contract and exclusive custody; they do not certify Docker Desktop volumes
against physical power loss. Missing, corrupt, conflicting or known-old state
continues to refuse. Raw attempts and failures are retained; the matrix, review,
full local gate, CI and exact main receipt remain separate exits. These private campaign results do not substitute for the separate public S4
wire gate or final #1610 acceptance.


## Public current-v2 composition

The production Wire graph selects `SecurityRuntime.openCurrent`, which calls
`LoadCurrentProvisioning` then `OpenCurrentAuthority`. The private listener and
workers start before public readiness, so peers can acquire quorum without a
public-ready startup cycle. The actual public `http.Server` owns output credit
through `ConnContext`/`ConnState`; copying only its Handler into another server
is not equivalent composition. The private consensus, original-H lookup and
renewal routes are never mounted on the public mux.

No-env/OFF remains unchanged. OIDC requires `LANTERN_SECURITY_PROFILE` explicitly.
A fresh current cohort uses:

```text
LANTERN_AUTH_MODE=oidc
LANTERN_SECURITY_PROFILE=current-v2
LANTERN_SECURITY_CURRENT_CONFIG_FILE=/operator/node.json
LANTERN_SECURITY_STORE_MODE=fresh
LANTERN_OIDC_BROWSER_ORIGIN=https://admin.example
```

The existing exact TLS/trusted HTTPS gateway boundary remains required. Optional
OIDC roots, private-origin allowlists and secret bindings keep their existing
meanings. Current configuration rejects writer keys/endpoints, old bootstrap,
legacy Store paths/generations, `CLOCK_QUALIFIED`, and injected clocks. It accepts
only the already selected native time profile and existing source configuration;
it does not configure the host or install trust roots.

### Independent operator documents

Both inputs are private regular files with absolute paths. JSON uses the exact
canonical Go JSON shape in
[`control_public_config.go`](../server/internal/security/control_public_config.go):
no duplicate/unknown fields, padded/truncated fixed arrays, aliases, or unknown
versions. Hash the exact genesis file bytes (including an optional final newline).
Do not reconstruct genesis from a replayed projection, journal, peer response or
old fixed-writer image.

| Document | Required original inputs |
| --- | --- |
| `currentGenesisDocument`, Version 2 | Domain, Cohort, Generation, Fences, original Image, complete S1 Execution configuration/capacity, original capsule Roots, voter Members, enrolled Origins, finite Bounds and the exact platform TimeProfile digest |
| `currentNodeDocument`, Version 2 | GenesisFile/SHA256; Participant member/incarnation, P/B paths and their original scope/identity/epoch/policy, OwnedOrigin and pending/outbox reserves; Membership path/operator key/signed manifest/full profile/self; separate workload TLS, voting and origin key paths; actual private ListenAddress and bounded Limits |
| `FloorsFile` for resume only | Independently retained M binding/version and P/B minimum cuts from the intact owner; this is minimum-cut evidence, never serving authority |

The membership profile binds the same voter keys, workloads and protocol as the
independent genesis. An origin-free enrolled member has OwnedOrigin zero and no
origin private key: it can read, catch up and complete retained originals, but
cannot mint a new management or login-session operation. Origin signing keys
remain distinct from voting and workload TLS keys. All configured paths must
retain exclusive custody; missing/corrupt/mixed families fail before publication.

`fresh` refuses supplied resume floors and existing families. `resume` requires
all original identities, intact M/P/B and independently retained floors; it never
falls back to fresh. The internal owner `ExportFloors` returns minimum-cut bytes
for an operator lifecycle integration, not a public HTTP endpoint. It must be
retained with independent original provisioning. Neither a backup snapshot nor
an old capability establishes current time, quorum, pending approval or output
permission after reopen. Production floor custody/cutover integration remains
an S5 operator responsibility; do not infer it from process volume survival.

`TestCurrentProvisioningExportPublicFixture` is a **test-only** authoring example:
it writes original canonical documents and fixture keys before any M/P/B exists.
It is opt-in via `LANTERN_CURRENT_FIXTURE_DIR` and a loopback TLS issuer. Its fixed
test keys/identities are unsuitable for deployment. The root native public gate
uses those files through the production constructor, without injected time or H.

### Public contracts and clients

Protocol v2 carries the complete profile and SemanticCut. New preparation sends
profile/cut/changes; the server returns its enrolled namespace/nonce, exact actor
and intent digest. Retain the entire review **before** the one Apply dispatch.
After uncertainty, status uses that original full reference. Unresolved, origin
durable, chosen, and original Apply are different stages; local absence never
proves safe nonexecution. Original commit, disposition, items and cuts are
immutable. The separate stop observation concerns new old-cut authorizations,
not physical packet arrival. Its volatile observer restarts conservatively.

Normal end-user management has no generic auth_time age gate. Effective manage
expansion and trust changes use the existing exact operation approval. The Code
callback belongs to its node/process and exchanges Code once; the purpose path
never refreshes an ordinary session or cut. Approved proof remains pending for
public consumers until the native lower endpoint reaches approval's recorded
upper endpoint. Final consume independently checks it again.

Session issue/replacement becomes effective only through original Apply. Cookie
publication requires a fresh credential/view after Apply. GET session obtains a
read-only CSRF bootstrap; mutations require the separate exact origin/header
proof. Logout first returns a server-minted review, then accepts that retained
review once. Local cookie clearing and cluster revocation remain separate; a
self-revoked cookie cannot disclose protected original outcomes using its old
admission. Use another currently authorized credential for status recovery.

Node/Admin consume v2 reviews, exact refusal details and immutable original
outcomes. Go `GetCurrentPrincipal`/`CurrentSecurityVersionBinding`, Dart
`getCurrentAuthorityBinding`, and Rust `current_authority_binding` use the normal
single-endpoint authenticated transport and reject scalar legacy versions. Their
binding includes full profile/cut and credential lineage/evidence, and is a cache
partition only. Opaque CDC cursors remain responder/profile/cut scoped. CLI and
MCP have no separate security mutation facade or granting policy cache: their
existing data calls use the same per-request server admission. No management
mutation is silently retried by the current facade.

Dart offline's scoped session supplies the complete `authorityBinding`; drift
hides confirmed values/cursors and rejects late reads while preserving pending
and possibly dispatched mutation IDs. The maintained hosted example keeps its
explicit legacy client lifetime; a current composition supplies the SDK binding
callback and updates it with credentials/scope. Paired-source success does not
publish a new SDK/offline archive or qualify physical devices.

### Finite public output and performance boundary

Every admitted output unit freezes bytes, headers/trailers, actual request and
connection, sequence, complete cut and typed resource/receipt provenance before
its last native sample. A unit authorized before expiry can complete late; the
next unit needs its own event. Data results are never relabeled with a newer
cut. Post-Apply control/session disclosure deliberately captures a fresh view. It may
wait up to ten operational seconds for peers to install that cut and answer a
fresh renewal challenge; this never repeats consume/Apply or makes the timer
authority. Native time uncertainty still refuses immediately.

Limits are pre-compression as well as wire limits. The owner reserves a 2 GiB
virtual-credit pool, at most 64 connections and 8 active request encoders globally.
Each request reserves `4*read + 10*send + 1 MiB`; each connection reserves
`(streams+1)*(2*32 KiB+16 KiB)+4 MiB`, including finite HTTP/2 framing/flow/header
storage. Read/send limits must be positive and at most 64 MiB, streams 1..4096;
configurations exceeding the per-owner reserves refuse. Headers/trailers have a
32 KiB/256-key bound. This accounts for the output seam, not all application RSS.
Request-owned Connect codec/compression pools cannot survive as uncharged shared
pools. Cancellation/Close join entered producers and release actual sockets.

The source-paired output tests cover before/after-final timing, immutable payload
and metadata, trailers, TLS HTTP/1+HTTP/2, Connect proto/JSON, gRPC/gRPC-Web,
compression, oversize and separate HTTP/2 recipients. The root native public gate
covers the actual constructor and public API flow. Keep fake-time transport
fault tests distinct from native qualification. `TestCurrentWarmAdmissionUsesNoPeerJournalOrTimeRefresh`
checks 1000 warm captures/checks and final output units with unchanged HTTP peer-call count, time measurement
sequence and M/P/B floors. `BenchmarkCurrentPublicOutputCodec` measures the bounded
encoding seam only; `bash testbed/bench/scenarios/current_authority_seam.sh` runs
these two bounded cases. It is not the final Server OFF/ON, HA/load or device gate.

### Migration refusal

Existing fixtures and fixed-writer installations select `legacy-v1` explicitly.
Do not roll current and legacy authorities together, feed v1 scalar mutations to
v2, import old sessions, or call a live writer image an intact current resume.
S5 must stop/fence the old cohort, retain old status/data/receipt history, choose
a new generation/cohort with independent keys/genesis, invalidate old sessions
and cursors, install compatible clients and perform a separately approved
cutover. This source change performs none of those environment actions.

The local `SecurityRuntime.ExportCurrentAuthorityFloors` lifecycle API exports the
existing owner's minimum M/P/B cut document. Retain it independently with the
original provisioning, then set `FloorsFile` for `resume`; no public HTTP route
exports it. It is a minimum floor, not authorization to restore a backup or
replace an intact journal. Deployment custody and shutdown integration remain
part of #1609.

A current status lookup may complete an already verified durable original H
through the existing driver, including when its first invocation stopped before
phase 1. It cannot prepare, consume, refresh credentials for, or mint a new
operation. This lets status-only clients recover the original without resending
Apply; absence remains unresolved and never becomes permission to retry.
