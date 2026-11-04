# Request policy runtime contract

The request-policy runtime group contains the four validation bounds
(`MAX_KEY_LEN`, `MAX_BATCH_SIZE`, `ILLUMINATE_MAX_STEP`, `ILLUMINATE_MAX_K`),
the scan and delete-by-prefix default/maximum pairs, and the traversal timeout,
push, touched-edge and result bounds. These names refer to the corresponding
`LANTERN_` settings listed in Epic #1990; this group adds no search, capacity,
authentication or transport settings.

An effective owner publishes all twelve values and their generation together.
The transport captures one immutable snapshot before request validation. The
public executor, prefix pages, degree ranking, traversal work/result budgets,
and status response reuse that snapshot, including after a concurrent update.
New requests observe the newly installed effective generation. Installation of
an effective generation is distinct from committing desired authority; the
control-plane adapter must report any lag or application error accurately.

Managed validation bounds are nonnegative, with zero disabling a bound.
Legacy environment bootstrap still treats nonpositive validation bounds as
disabled; adoption canonicalizes those disabled values to zero. Scan and
delete pairs are positive with default no greater than maximum. Traversal work
and result bounds are positive; a nonnegative whole-millisecond timeout permits
zero to leave deadlines client-owned. Converting a persisted millisecond count
must reject overflow before multiplying by `time.Millisecond`. Failed policy
validation, stale callbacks and conflicting same-generation callbacks leave
the entire effective snapshot unchanged.

A traversal already in progress keeps its original deadline, work and result
limits. A shorter client deadline still wins. A resumed scan is a new request:
it uses the current page clamp while preserving its existing cursor's predicate,
order, RPC and authorization binding. Changing the page size or operational
generation alone does not invalidate that cursor. Authorization and final
output admission remain current independently of the operational snapshot.

The native typed control plane owns adoption, review/CAS, audit, persistence and
recovery. Before public certification, the managed adapter restores the Request
group into the actual primary Executor and its actual validator through one
registered owner. Public composition requires those same concrete owners;
substituting an equal-valued validator or another Executor is refused. OFF keeps
its environment-owned startup values. Other operational groups gain support only
when their own real runtime owners are installed.

Paired owner/interceptor tests and
`tests/integration/request_policy_gate_test.go` cover Connect request snapshots,
concurrent changes and existing cursor semantics. Those checks do not substitute
for exact-source native adoption, management UI or final integration acceptance.
