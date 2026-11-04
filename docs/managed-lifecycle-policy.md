# Lifecycle policy runtime contract

The lifecycle group atomically owns `LANTERN_DRAIN_DELAY_SECONDS`,
`LANTERN_SHUTDOWN_TIMEOUT_SECONDS` and `LANTERN_MAX_REPLICATION_LAG`.
Managed delays are nonnegative whole seconds. A zero drain delay skips the
load-balancer propagation window; a zero shutdown timeout leaves public HTTP
draining without a forced deadline. The timeout does not bound private worker,
tracing, backup or native security cleanup. Zero replication lag requires every
observed peer/origin row to be caught up.

The bootstrap defaults remain a zero-second drain delay, a 30-second HTTP
shutdown timeout and a maximum observed replication lag of 10000 mutations.

The effective owner publishes one immutable value and generation. Invalid,
stale or conflicting same-generation updates leave the entire value unchanged.
The control-plane adapter must convert persisted seconds without overflow and
report desired versus effective state independently.

App and the public HTTP server share one shutdown plan. The first shutdown
action captures its effective generation before withdrawing readiness. Both
the drain delay and the later HTTP shutdown deadline use that frozen plan.
Updates during the drain window cannot shorten, extend or remove its deadline.
Construction and status/log observations do not capture the plan early. A server
failure can begin HTTP shutdown first; App then observes the same captured plan.
Without a managed source, existing static configuration behavior is preserved,
including nonpositive legacy HTTP timeouts having no deadline.

The reported effective lifecycle generation describes future actions. A newly
installed generation does not claim that an already-running shutdown changed
its captured drain delay or HTTP deadline. An unadopted legacy-selected positive
fractional duration remains the actual behavior and reports invalid managed
policy with no representable effective tuple. A valid reviewed commit repairs
it without substituting defaults. Verified adopted policy takes precedence over
conflicting or unrepresentable host inputs during recovery.

Updating readiness's lag threshold reevaluates retained lag observations. It
does not reset bootstrap, search compatibility, native serving permission or
the one-way drain latch. Lag updates must never write the native authority's
independent serving-permission flag.

Normal composition registers the actual readiness gate, lifecycle owner and
shutdown plan before public certification. The concrete plan verifies its
source owner without capturing a generation. The same plan pointer reaches
App and the public HTTP server; the graph-policy GC watcher and backup producer
retain their separate ownership checks. Startup uses one captured local input
unless verified native desired state has already adopted the group.

Native adoption, durable recovery, Admin review/apply and per-node observations
use the existing typed operational control plane. A new committed generation
is the rollback path. Source integration alone does not establish the final
real Connect/Admin acceptance or Epic #1990 completion. Search and replication
groups remain unavailable until their actual runtime owners are integrated.
