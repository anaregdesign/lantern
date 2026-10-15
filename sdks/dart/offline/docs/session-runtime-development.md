# Automatic foreground session runtime (#1769–#1772)

`OfflineLanternSession.open` supplies automatic memory sessions;
`SqliteOfflineStore.openSession` reserves native ownership before opening SQLite.
Memory returns the common `LanternDataClient` CRUD/watch contract. The qualified
SQLite factory requires installation policy and returns a ready session or typed
restore-held startup with no client. Post-commit
admission, open, verified credentials, foreground resume and persisted deadlines
wake delivery. A reachability hint only coalesces eligible work; no Get, drain or
Sync button is required after an endpoint recovers. The earlier explicit
`OfflineLanternDataClient.open` remains an infrastructure/conformance seam.

## Ownership and lifecycle

Matching canonical Store, immutable binding, transport and configuration share
one runtime in an isolate (16 runtimes, 64 handles per runtime). Different opens
fail before automatic work. Last close closes the synchronous lifetime gate,
joins network/local callbacks and accepted transactions, conditionally releases
the owner row/guard, then closes owned store and transport. Each handle cancels
its own subscriptions and completion waiters; admitted work belongs to the shared
runtime. A failed join keeps ownership. Qualified SQLite retains the native guard through
database close; a failed database close never reports successful release. Cleanup
attempts the remaining owned resources while preserving the first failure, and
the core reservation stays blocked on any teardown failure. Legacy low-level
owner primitives retain their explicit release protocol. App/account close
memoizes the same Future and blocks replacement after failed retirement.

SQLite uses a small C Code Asset on Android/iOS and the same primitive on the
host: nonblocking exclusive `flock` on a dedicated sidecar, with at most 16 opaque
native guards. No FD is exposed, duplicated, finalized or unlocked at isolate
exit. An abandoned isolate remains fail-closed until process exit. The mobile
SQLite engine stays sqflite; the native helper is only an ownership lock.

Existing DB names use realpath; new DBs use the real parent plus basename. SQLite
and sidecar use the same canonical name. Device/inode/regularity are checked by
stat, without opening the DB as a raw FD. DB/sidecar hardlinks are rejected.
The sidecar is never unlinked during production close. This contract assumes an
application-owned local directory; it does not coordinate external rename/delete,
network filesystems or copied/rolled-back databases.

The acquisition order is core reservation, native provisional guard, application
installation policy and bounded read-only preflight, atomic SQLite migration/adoption/validation,
transactional monotone owner epoch plus random token, capability publication,
then work. OS ownership is the exclusion proof; the row is fence/history, never a
PID/time-based liveness test. Process death permits a new guard to adopt a stale
active row at a greater epoch and reconcile original receipt evidence.

## Work and credentials

Owner/account/store generation is the lifetime fence. Work epoch and accepted
credential generation are renewable. Every finite delivery/read attempt uses a
fresh checked token through preparation, original-receipt status, post-token
pre-wire and pre-publication boundaries. A stop synchronously gates/cancels work;
control-plane transactions return only owned claims and persist pauses under the
still-current lifetime. Cancellation never reduces possible-dispatch evidence.
Auth callbacks do not await their own join.

Lifecycle suspend stops all wake timers and joins attempts. Foreground resume
applies local expiry before reopening dispatch. Auth/policy/transfer pauses stop
network work while local admission, completion status and TTL continue.
`OfflineVerifiedCredentials` represents a new event already verified by the
application/native auth provider. Install occurs after old work joins; only then
does the runtime advance generation and clear auth. Reused events, counters,
token-string changes, lifecycle hints and Health cannot clear auth. Policy,
storage and restore holds survive auth refresh. A different full identity ends
the old immutable lifetime. The maintained app supplies this verified event only for a new completed native
login/registration; restored credentials do not supply it.

`transferAllowed` defaults true; platform policy may change it through
`setTransferAllowed`. The core runtime input `restoreHeld` hides old confirmed
data and blocks preparation/send, but is not a pre-initialization restore check.
The qualified SQLite factory returns `SqliteRestoreHeld` before write-open when
application installation continuity is uncertain, without creating a runtime. It is
immutable for the lifetime and cannot be cleared by hints or auth. A newly opened
unheld lifetime requires the external continuity decision; the SDK does not infer
safety from a copied file or local counter.

An eligible reply settles a live waiter before confirmation persistence. If the
confirmation fails, the known reply remains and storageBlocked stops further
dispatch. A reply after stop cannot newly settle; reopen/resume retains the
original receipt/Add IDs, absolute TTL and uncertainty, and proceeds status-first.

## One bounded scheduler

One wake timer covers local expiry, retries, watch TTL and polling. Registered
slots have one in-flight callback plus one dirty bit. Hot invalidations use 50 ms
debounce with 250 ms maximum wait; they never repeatedly cancel slow work.
Weighted round robin uses reconcile/send/read/traversal weights 2/2/2/1, plus a
local lane with no network permit. Default global send/read/traversal permits are
4/8/2 and per-scope permits 2/4/1. Traversal preserves one ordinary read permit.
Global registered send/read/traversal/local slots are 128/128/64/128; scoped limits
are 32/32/16/16 and 64 total. No later CDC or Traversal component starts its own
scheduler.

The incoming lane reserves one tail per owner and at most 16 globally, without
holding an ordinary read/send permit. Cancellation and credential joins include
that tail; synchronization metadata excludes its idle connection. Its one retained
resident-recovery job uses the existing read permits and 32-identity pages.
Edge endpoint dependencies are also read in pages of at most 32. A callback may
reuse a runtime read permit only when its current lane already owns one.

Resident recovery advances an ephemeral exclusive keyset cursor after selecting
each page, even when a racing invalidation prevents its commit. Unknown markers
still require the original epoch-valid evidence. One invocation examines one
page; reaching the end with Unknown remaining wraps the cursor and imposes a
one-second resident-only floor. Every 32 selected pages impose the same floor
while keeping the cursor, including when the last page is interrupted. CDC wakes
cannot shorten it. The entry check re-arms an earlier queued turn without scanning
or RPC, then normal debounce resumes when the floor expires. Endpoint backoff
still applies, while unrelated watches and outgoing work retain their permits.
This uses one cursor, a bounded page counter and one deadline in the existing job;
there is no durable pagination state, new schema or second timer. Bootstrap resets
them; certified availability/lifecycle resume preserves them. Stale tail responses
cannot overwrite a reset or pause replacement recovery.

Two finite delivery workers claim eligible FIFO heads transactionally, allowing
an unrelated identity to progress while one receipt is slow. SQLite computes
aggregate wake deadlines without decoding payload lists; local expiry and auth
resume scans use 32-row pages. Preparation failure does not consume mutation
attempt budget. Dispatch retains the existing eight attempts and seven-day age.
Standard adapter attempts use 15 seconds with nested retries disabled.

Availability backoff is shared by reads and writes: jitter within the increasing
100 ms–30 s bound, preserving a longer supported Retry-After (delta seconds or
IMF-fixdate). Only the absolute deadline is persisted per retrying mutation;
metadata/credentials are never stored. New Get calls return eligible local
fallback during backoff; serverOnly remains Unknown. Hints cannot bypass a
throttling deadline. Status reports outstanding mutations, terminal attention,
reachability, pauses, lifecycle and deferred work. Idle/zero pending does not mean
all writes succeeded or cached graph state is current.

## Parent observation boundary

The parent coordinator retains canonical query refcounts, epochs, initial/local
capture, eight-event mailboxes and observationLost. An optional cache-neutral
scheduler port delegates starts, permits, debounce, poll deadlines and cancel/join.
Direct's default path remains available without injection. Registration reserves
both coordinator and scheduler slots atomically and retains the slot while idle.

Local initial/pending capture does not await a network permit. Epoch/publication
checks reject a stale local capture or network result; pauseAfter is applied only
after current query/work checks. Weak snapshot deadline metadata feeds one bounded
watch-expiry slot. Last unsubscribe removes only its query work. One-shot reads
leave no poll registration and scheduler callbacks reuse their existing permit.

## Automatic public incoming observation

The optional `incomingChanges` source is a parent `LanternChangeSource` bound to
the same exact client and complete immutable authority. It owns no scheduler.
The common runtime opens one identity-only public `WatchChanges` tail under an
application-certified actual-responder lifetime. Private Subscribe, status and
origin vectors are never substitutes. `LanternScopedChangeSource` is the public
Offline adapter for the older explicit consumer; the example reexports it.

A new owner bootstraps rather than treating a persisted URL/cursor as route proof.
Installing bootstrap hides resident values and retains bounded key-only Unknown
markers. Partial and complete frames transactionally invalidate entities, advance
the read epoch and optionally install the completion cursor. A failed transaction
publishes no new evidence or cursor and pauses storage work. Reads check account,
authority, work, route and read epoch before commit and publication, including
between plural pages. Incoming recovery does not wait for all outgoing delivery.

Ordinary availability loss advances the read epoch without deleting eligible
historical cache or the last completed cursor. It reconnects through shared
backoff; only the same certified lifetime can resume. EOF, malformed/gap frames
and authority/route discontinuity hide old evidence and require bootstrap. Auth
and policy rejection persist a pause and require the existing verified lifecycle
transition. An unimplemented source may fall back to active freshness polling;
denial cannot. Strict-live remains unsupported because a connected identity tail
does not certify complete dependency or natural-expiry coverage.

Vertex invalidation/settlement conservatively evicts Edge cache in the partition.
Positive Edge reads also verify both endpoint Vertices and cap evidence lifetime
by their expirations, preserving the Edge payload's own expiration. The shared
expiry alarm invalidates active views even while network transfer is paused.
No-read-cache sessions retain neither entity nor endpoint payloads after delivery.
The incoming algorithm itself changes no persistence version. The subsequent
SQLite integration uses schema 9 for installation/adoption metadata; reference
snapshot 11, outbox 5 and operation 4 retain their existing contracts.

## Focused evidence and remaining exits

The finite O1–O4 fixtures use actual native locks, same-PID isolates, child
processes, SIGKILL, aliases/hardlinks and acquisition/release faults. C1–C4 cover
preparation/status/token/marker/publication stop points, auth siblings, retained
pauses, account wipe and confirmation-store failure. S1–S4 cover sharing, 100 hot
invalidations, old denial, capacity/fairness/mailbox bounds, auth/lifecycle resume,
cacheOnly and watch TTL. T16 adds transfer/restore pause and shared Retry-After.
The Connect integration uses the production automatic memory factory for a
same-foreground outage and a committed response lost at the proxy boundary.
Native crash receipt reconciliation is a finite host fixture, distinct from those
real-wire cases; neither proves mobile device behavior.

The maintained app integration adds the I1–I8 fixture families: common consumers
across four profiles, schema8/oldest supported adoption, pre-/post-commit faults,
A→B→A original receipts, installation holds with unchanged durable intent,
provisional-owner/close failures, same-screen transport recovery and old-reply/
absolute-expiry fences. The integrated SQLite actual-wire case restores only the
transport and observes mutation settlement plus Vertex/Edge/Traversal updates;
independent subscriptions retain their own lifetimes. Platform policy channel
checks and Android/iOS builds are source/host/build evidence, not device restore
attestation. Markerless legacy adoption remains conditional on independent
trusted installation provenance.

The same existing design decision governs the changed integration boundaries.
Each immutable candidate still requires the ordinary review, full source gate and
exact submitted/merged-source CI. Publication, archive equality, physical
Android/iPhone and OS-sleep evidence remain separate. Physical-device work remains
stopped.
