# Automatic foreground session runtime (#1769)

`OfflineLanternSession.open` supplies automatic memory sessions;
`SqliteOfflineStore.openSession` reserves native ownership before opening SQLite.
Both return the common `LanternDataClient` CRUD/watch contract. Post-commit
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
runtime. A failed join keeps ownership. A row-cleanup failure after a successful
join invalidates the old capability and releases the guard, then reports the
error. Close is idempotent.

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

The acquisition order is native provisional guard, SQLite migration/validation,
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
the old immutable lifetime. Native account/installation integration remains WP6.

`transferAllowed` defaults true; platform policy may change it through
`setTransferAllowed`. `restoreHeld` is a factory input from the installation/backup
continuity decision, hides old confirmed data and blocks preparation/send. It is
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

This candidate is based on #1768 merged main
`3e4ff3696cd6f8362c5f377348ed6f145111a84f`. Its additional memory reservation
memoization changes no public API or schema: the memo is transaction-local
copyable derived state, invalidated on topology replacement/removal and recomputed
on snapshot restore. Automatic progress, auth pause and local expiry retain the
same immutable operation topology and capacity checks. SQLite schema 7 follows
that base's schema 6; reference snapshots advance from 9 to 10.

The existing designer's limited R1/R2/R3 review passed before this base connection.
Final source still requires the ordinary full gate and exact submitted-source CI.
Publication, archive equality, physical Android/iPhone and OS-sleep evidence remain
separate. Physical-device work remains stopped.
