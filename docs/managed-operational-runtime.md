# Managed operational runtime composition

The common native authority supplies verified desired policy. Provider owners
separately observe actual effective values and generations. Saving a policy or
returning nil from an application callback does not establish installation.

`OperationalRuntimeOwner` embeds the native availability, candidate preparation
and committed application methods, and adds `ObserveOperationalPolicy`. Its
`OperationalOwnerObservation` contains actual typed values, generation, sources,
compatibility fingerprint, mode and bounded error. Callbacks never read authority;
native support can invoke preparation while holding its own lock.

`ManagedOperationalRuntime` has eight closed named registration slots. A group
package constructs actual resources, obtains its same-load bootstrap provenance,
and calls `Register`. Unsupported groups retain nil owners and report unavailable.
No schema model or placeholder capability enables support. Registration installs
recovered hot policy before native binding, then rereads/reconciles verified state
after binding to cover private catch-up during construction. Restart-applied group
constructors must already prove their initial effective generation.

The common registry records an applied cut only when a complete actual tuple
matches a committed desired generation and a fresh local native reread verifies
the same cut. Unrelated security commits preserve prior installation provenance.
Same-cut policy changes, regressing generations, changed scope or disappeared
prepared floors refuse observation. Fresh management reads supply their own native
projection and guarded output; observing status never applies policy.

A concrete group certification marker forces its owner into the generated Wire
dependency graph. `managedOperationalCertified` checks exact identities and every
adopted or prepared-only native owner requirement before public certification.
Public listener composition verifies that the exact certified Slow RPC owner is
mounted. The data/native restore boundary and private authority opening order do
not change. OFF retains environment-owned behavior and exposes no policy writer.

The initial #2009 composition registered Slow RPC first. Its resident middleware
always stays mounted, including when disabled. One atomic snapshot couples
threshold with generation, captured once at request entry. Managed application validates
nonnegative representable milliseconds and monotonic generation/CAS; a bootstrap
setter cannot overwrite an adopted generation. Requests already running retain
their captured tuple; slow logs include `policy_generation`, with zero denoting
bootstrap ownership. The adopted threshold wins conflicting environment input on
verified restart. Preparation alone never changes the threshold.

The complete composition registers eight actual group owners. Their reported
modes describe these application boundaries:

| Group | Settings | Mode | Application boundary |
| --- | --- | --- | --- |
| Slow RPC | 1 | Hot | A request retains its entry-time threshold and generation. |
| Rate | 2 | Hot | RPS and Burst update atomically in one per-process bucket. |
| Request/scan/traversal | 12 | Hot | A request captures one generation before validation. |
| Backup production | 3 | Hot | Each future attempt captures its production and retention policy. |
| Capacity/GC | 6 | Worker restart | Admission changes do not evict data; GC interval/budget changes cancel and join the owned worker before replacement. |
| Lifecycle/readiness | 3 | Hot | Future actions use the new policy; an in-progress shutdown keeps its captured plan, and lag updates preserve independent readiness conditions. |
| Search | 11 | Coordinated process restart | Live Apply stages desired state; startup reconstructs the effective engine. |
| Replication scheduling | 5 | Process restart | Live Apply stages desired state; startup constructs the actual immutable worker pair. |

These 43 settings and the separate existing log-level policy form the definitive
44-setting allowlist. Pending restart or failed operational application does not
itself invalidate authentication authority or block management recovery. Search's
trusted direct-RPC generation guard rejects pending transitions independently of
health/readiness. Search and replication workers share the sealed effective
fingerprint; mixed-fingerprint rolling restart is not a general supported guarantee.

A missing owner still reports unavailable, and recovered unsupported
prepared/adopted state refuses public startup. Source composition and component
checks do not establish integrated delivery or final real-server acceptance.
Required CI, rollout and final acceptance remain tracked by the group Issues and
[Epic #1990](https://github.com/anaregdesign/lantern/issues/1990).
