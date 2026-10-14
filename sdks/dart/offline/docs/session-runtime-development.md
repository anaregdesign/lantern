# Automatic session runtime development (#1769)

The owner-dependent scheduler foundation is implemented in
`lib/src/session_runtime.dart`. This is an internal preparatory slice, not an
automatic application factory or completion of #1769. Ordinary
`OfflineLanternDataClient.open` still uses the #1768 orchestration seam.

One runtime consumes an already acquired exclusive `OfflineAutomaticOwner`.
Item leases, transaction serialization, network hints and a matching filename
cannot supply that capability. The factory must validate migration, canonical
Store identity, immutable account/remote/configuration compatibility and owner
acquisition before registration; release the exclusive capability only after
all runtime work has canceled and joined.

Each canonical job has one bounded queued/in-flight slot and at most one dirty
follow-up. A hot job receives 50 ms debounce with a 250 ms maximum wait. Existing
absolute retry deadlines are retained; scheduling does not mint IDs, extend TTL,
clear durable pauses, consume mutation attempts or invent a retry classification.
An invalidation does not cancel a slow traversal. Its publication callback must
check its captured work fence after awaits and before publishing.

The runtime has one wake timer, finite lane queues and weighted round robin
reconcile/send/read/traversal turns (2/2/2/1). Default shared send/read/traversal
permits are 4/8/2 and scoped permits are 2/4/1. Reconciliation reserves a send and
read permit; traversal reserves a read and separate expensive-work permit while
preserving ordinary-read capacity. Registered global send/read/traversal slots
are capped at 128/128/64, scoped slots at 32/32/16 and all scoped work at 64.
Registered runtimes are capped at 16. The composition root must inject one shared
budget; separately instantiated budgets do not establish process-wide bounds.
Cap rejection preserves admitted slots, including callbacks awaiting cancellation.

Suspend invalidates the work epoch, cancels callbacks and joins them while
retaining queued work. Resume requires a current owner/account fence and joined
callbacks. It does not clear authentication, policy, storage or restore holds.
Close is idempotent and joins all work before returning. Canceling a scheduler job
never withdraws an admitted mutation or proves a Server effect was undone.

Finite fake-clock tests cover maximum debounce, rollback, absolute deadlines,
dirty slow work, fairness, scope/global caps, ordinary-read reservation, owner loss,
cancel/publication races and deterministic teardown. The existing response-dropping
proxy supplies two real Connect cases: same-foreground endpoint outage and a lost
reply after actual Put handling. Their one initial fixture registration retries
from the persisted deadline without a second application scheduling call; they
retain input TTL and operation identity. The receipt-less Put result means the
handling observed by the successful retry. These cases use a test owner capability
and do not qualify platform ownership, receipt reconciliation or a production
automatic factory.

Before connecting the facade, the original design owner must resolve SQLite's
OS-lock/same-process isolate acquisition and close semantics, owner-row death and
alias policy; delivery cancellation/credential fencing; and delegation of the
existing bounded observation coordinator's timers to the shared runtime. Remaining
WP3 work includes the qualified owner/refcount factory, indexed persisted wakeups,
automatic enqueue/open/reachability triggers, TTL/status expiry, pause/health and
credential integration, Retry-After/backoff, and later observation lane composition.
No CDC or Traversal component should start a second scheduler.

Publication remains held until #1768 is integrated and the permission blocker is
resolved. Focused checks on the unpublished dependency do not qualify rebased main,
the full local gate, CI, publication or physical Android/iPhone behavior. Physical
device and OS-sleep trials remain stopped.
