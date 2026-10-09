# S3-A private authenticated control owner

S3-A (#1688, tracking #1608) adds a separately constructed, package-private
network owner beside the unchanged S2-C participant. It has no provider,
product route, environment variable, startup path, public protobuf or SDK
surface. The tests use real localhost TLS and native M/P/B files. This is an
inactive recovery/ordering component, not qualified serving admission.

## Identity and membership

`peerauth.ControlProfile` has its own versioned canonical JSON encoding and
signature domain. It binds deployment, an independently pinned control lineage,
namespace format, actual S2-C bootstrap scope, provisioned TLS-root bytes'
SHA-256, and the complete immutable mapping between workload ID/URI SAN/SPKI/
fixed HTTPS origin and voter ID/Ed25519 key/proposer role. All configured voters
occur exactly once. No voting key may also be a configured TLS key. The owner
compares the complete mapping with the actual kernel bootstrap and loads its
own independently provisioned private key files before any dispatch.

The legacy `peerauth.Domain` format and OFF/OIDC rules remain separate. Neither
profile accepts the other's signed manifest or scope header. Control requests
and responses carry the exact hex `Lantern-Control-Scope` digest. No fake legacy
writer identity or caller-asserted verified constructor is introduced. Original
H identity, key and incarnation remain independent S2-C bootstrap inputs; the base S3-A stage uses fixture H; the subsequent current owner issues genuine
private-profile H as described below.

M uses the existing private checkpoint and FileWAL lease mechanisms. A current
same-binding operator manifest can refresh only revision and time. A strictly
higher operator-authenticated revision for this pinned lineage that changes the
binding is durably checkpointed and then closes admission. `MembershipFloor`
can return that terminal fence for external retention. Resume rejects the old
binding even with no supplied floor; the new binding is never auto-adopted.
Unauthenticated and foreign-lineage input cannot fence the owner. Uncertain
checkpoint I/O, including a panic before the complete persistence/publication
operation finishes, returns no retained-fence receipt and closes admission. The
possibly retained checkpoint is preserved for explicit recovery, never replaced
by a retry over uncertainty. Refresh also stops the outer network owner before
propagating a membership-operation panic.

## Composite ownership and recovery

Fresh enrollment and intact-state resume are separate constructors. The owner
validates canonical, disjoint M/P/B families, all provisioned identities and
limits, then owns every acquired lease, signing key, client, listener and
worker. No listener starts until M/P/B validation, retained CHOSEN→B→DRAINED
recovery and current workload eligibility have succeeded. Partial construction
closes all acquired resources; M lease cleanup is registered immediately after
acquisition, before any configured clock callback. It never creates a missing
family on resume.

The caller supplies three independent minima without conversion:

| Minimum | Evidence retained by the caller |
| --- | --- |
| M `ControlFloor` | Known operator manifest revision and authenticated bytes |
| P `s2cJournalFloor` | Known ballot, promise, accept and origin/control history |
| B `s2LocalReceipt` | Known complete local materialized cut |

Zero means no supplied rollback witness for that family. Same-volume files,
local epochs and signatures cannot detect whole-volume rollback after all
independent witnesses are lost. The tests retain minima outside the restarted
node; they do not deploy or qualify a witness service. Missing/corrupt families,
wrong identity/incarnation/epoch or known rollback fail before dispatch. There
is no P reconstruction from B, graph snapshot fallback, clone repair or rejoin.

Uncertainty stops admission and scheduling and cancels network work. Close
also waits for entered kernel work, admitted HTTP handlers and active recovery
calls before releasing durable ownership and clearing owned signing keys. TLS signer
access is serialized with key clearing, including handshakes unwinding during
shutdown. If an owned closer panics, all remaining cleanup steps are attempted
before the first panic is propagated; subsequent Close calls retain a cleanup
failure instead of concealing it. Pending transition producers are joined before
queued closures and delivery buffers are discarded. A cancelled/lost response
cannot undo a durable promise or accept.
Workload eligibility is checked before mutation and response/dispatch release;
response processing retains the original TLS admission expiry across refresh.
Terminal response suppression aborts the HTTP handler/connection through
`http.ErrAbortHandler`, so wrapper deadline cleanup cannot produce an implicit
successful response. These checks are not a policy-freshness certificate or a
qualified suspend bound.

## Private wire contract

Both operations are POST over pinned TLS 1.3 with mutual certificate validation,
`application/octet-stream`, and the control-scope header. Fixed approved origins,
URI SANs and SPKIs apply in both directions. Public Authorization/Cookie/proxy
credentials, proxies, redirects, plaintext and legacy scope headers are rejected.

| Endpoint | Request | Successful response |
| --- | --- | --- |
| `/lantern-private/control/v1/message` | Exact existing S2-C message bytes | 204, empty body |
| `/lantern-private/control/v1/chosen-range` | Two big-endian uint64 values: first slot and maximum count | 200, `LNS3R001`, uint32 count, repeated uint32 length plus exact CHOSEN bytes |

Prepare, Promise, Accept and Accepted must be signed by the voter mapped from
the admitted TLS peer. The kernel still verifies the full message. An admitted
peer may relay CHOSEN signed by another proposer: the full historical QC and
contiguous prefix, not the relay's identity, establish its authority. Self
broadcasts use a bounded internal worker and the same kernel Receive path;
there is no external local/authentication-bypass flag.

CatchUp validates the entire bounded range's framing, signatures and contiguous
slots before passing the real CHOSEN messages to Receive. It never downloads
another owner's P/B files. HTTP success only acknowledges transport processing;
it is not a vote, majority certificate, Apply outcome or current authority.

## Capacity and scheduling

Configuration explicitly bounds body/frame size, headers, connections, range
count/bytes, transition count, per-peer queue count, pending bytes, retry attempts,
backoff, ballot pacing and elapsed scheduling windows. Constructors reject
arithmetic/representability failures, including an outbox unable to carry a
maximum valid broadcast. There are no production sizing defaults.

Let N be the voter count, O the configured maximum kernel outbox bytes, F the
maximum representable message frame, Q the pending delivery byte budget and
E=64 the conservative queue-envelope charge. The owner accounts for distinct
pools; **Q is not the total memory budget**:

- One serialized transition reserves O + N×E before calling any kernel method
  that can produce an outbox. Pending network work cannot occupy this pool.
- Pending delivery caches partition Q into N separate budgets of floor(Q/N)
  bytes, each also bounded by the configured per-peer count. The division
  remainder is unused. Validation guarantees floor(Q/N) >= F+E, so another
  destination cannot consume the space needed for an empty peer/self queue to
  admit one maximum frame. Each worker transfers one frame into its independent
  active-delivery slot before waiting on network or self Receive.
- Active delivery retains at most N×(F+E). One outgoing chosen-range operation
  has its own slot. One local Begin/Retry and one Drive may be outstanding.
- Each external peer has one admitted inbound body/range operation. The bounded
  FIFO can admit all peers, self, local initiation and catch-up without an
  unavailable peer consuming every transition slot. Active connections and
  headers have separate finite limits.

These are logical payload/envelope budgets. Bounded HTTP buffers, decoding and
range copies, TLS/Go runtime overhead and the inherited kernel's separate
retained-state budgets are additional memory, not part of Q or an RSS guarantee.
Range response framing reserves 12 + 4×RangeSlots bytes beyond RangeBytes.

The transition pool releases unused credit once, including cancellation and
terminal errors. Produced frames transfer into their destination's pending
cache if its count and byte limits permit. Otherwise the transport copy is dropped, like a failed
send, while its durable protocol obligation remains in P/B. This deliberate
loss-safe policy keeps maximum-outbox reservations available despite one
unavailable peer. Separate per-destination pending-byte partitions also prevent
a low-ID unavailable peer from repeatedly taking healthy/self capacity during
sorted broadcasts; fair worker execution alone would not establish this. The
staging, active and pending pools preserve distinct ownership.

Workers retry exact frame bytes for a bounded number of timed attempts and
then discard that transport copy. Kernel Retry, repeated incoming Prepare/
Accept and ExportChosen regenerate exact retained messages without a delivery
WAL append. No volatile cache supplies authority after process loss. FIFO
transition processing and independent peer/self workers separate network waits
from the durable gate. Same-slot ballot escalation is paced separately from
retransmission and cannot spend S2-C's durable completion reserve.

Drive uses a finite scheduling window, real phase one, exact retries and
rotating chosen-range requests. Progress requires an eventual stable interval
with an eligible sufficiently stable proposer, a reachable functioning quorum,
intact nonrolled-back state and enough retained capacity. A finite invocation
may end without installing a slot. There is no permanent leader, arbitrary
partition liveness, unbounded retention or exactly-once delivery claim.

## Native qualification boundary

Source-paired tests and the cross-source `control_network_gate_test.go` integration cover:

| Boundary | Test evidence |
| --- | --- |
| Canonical profile, lineage, mapping, distinct keys | `TestControlManifestCanonicalTypedLineage`, `TestS3AIdentityRejectsBeforeAnyStateCreation` |
| Refresh/fence/checkpoint uncertainty and time edges | `TestControlStoreRefreshFenceAndIndependentFloor`, `TestControlStoreUncertainFenceAndTimeBoundaries`, `TestS3AOwnerMembershipRefreshAndDurableFence` |
| Direct sender, malformed/oversized traffic, TLS, foreign replay, public credentials | `TestS3ATransportDirectSenderAndBounds`, `TestS3ATransportPlaintextPublicHeadersAndProxy`, `TestS3ATransportRealCertificateRejectionsAndForeignReplay` |
| Lost ACK, original admission expiry, withdrawal/cancellation | `TestS3ATransportLostACKRegeneratesExactDurablePromise`, `TestS3ATransportResponseKeepsHandshakeDeadlineAcrossRefresh`, `TestS3ATransportBlockedRPCExpiryWithdrawalAndCancel` |
| Actual agreement, duplicate/reordered QC, intact laggard, relay by another peer | `TestS3ADriverRealTLSAndChosenCatchup`, `TestS3ADriverContiguousCatchupDuplicateAndReorderedQC`, `TestS3ADeliverySaturatedPeerSelfCreditAndEventualQuorum` |
| A-only/A+B hidden accepts, issuer loss, expired original H, real higher ballot, process recovery | `TestS3AGateHiddenChoiceTLSProcessRecovery`: each history crosses afterChosen, afterB and beforeDrained process exits |
| Explicit floors, missing families, actual rollback, lifecycle | `TestS3AOwnerExplicitResumeFamiliesAndFloors`, `TestS3AOwnerKnownNativeRollbackWithIndependentFloors`, `TestS3AOwnerCloseRacesAndReleasesLeases`, `TestS3AOwnerCloseDrainsAdmittedHTTPBeforeReleasingState` |
| Saturation, self-credit, pre-mutation limits, cancellation and native uncertain I/O | `TestS3ADeliverySaturatedPeerSelfCreditAndEventualQuorum`, `TestS3AOwnerRejectsUnrepresentableLimitsBeforeState`, `TestS3ADeliveryCancellationDoesNotReleaseEnteredDurability`, `TestS3ADeliveryUncertainNativeIOClosesAllOutwardAPIs` |

Review regressions additionally cover the original low-ID/multiple-copy cache
counterexample at minimum Q (`TestS3ADeliveryLowIDPartitionPreservesHealthyAndSelfOpportunity`),
M checkpoint panic cuts and construction callback panics
(`TestS3AIndependentControlCheckpointPanicMustClose`,
`TestS3AIndependentControlConstructionPanicMustReleaseLease`), outer Close and
Refresh panic handling (`TestS3AIndependentOwnerClosePanicMustReleaseMAndSigner`,
`TestS3AOwnerRefreshPanicStopsScheduling`), and terminal response suppression
observed by a raw mTLS client independent of membership cancellation
(`TestS3ATransportRawTLSExpiryWithdrawalAndCancelSuppressResponse`). The cache
regression controls finite worker turns over native protocol bytes; it is not
an exhaustive or unbounded HTTP liveness proof.

The child process receives only independent test configuration and provisioned
identity files; restart discards volatile messages and proofs. Consensus and
recovery evidence crosses actual TLS. Test IPC observes crash readiness and
retained state; it neither supplies votes nor tells the driver a chosen value.
The unchanged S2-C source/evidence remains its own qualification, not newly
executed evidence. Focused native and race results, exact commit/tree hashes,
independent review and later mandatory full76/CI are recorded separately.

## Subsequent current-authority extension

S3-A alone does not establish policy freshness: A+B may retain a restrictive
ACCEPT while every materialized head remains old. The subsequent private
[#1722 current-authority/origin owner](security-current-authority.md) implements
the serialized renewal, conditional native-time, genuine credential/origin and
bounded output boundaries without changing S3-A's public activation status.
That document records its explicit profile, limits and remaining qualification.

Public session/control API activation, final target OS evidence, deployment,
dynamic key/membership operations and #1668 remain separate. Deferred
backup/snapshot/clone return is not silently enabled. Old 28/35-second constants
and fixed-writer authority do not become guarantees of this component.
