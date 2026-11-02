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

Slow RPC is the first registered group. Its resident middleware always stays
mounted, including when disabled. One atomic snapshot couples threshold with
generation, captured once at request entry. Managed application validates
nonnegative representable milliseconds and monotonic generation/CAS; a bootstrap
setter cannot overwrite an adopted generation. Requests already running retain
their captured tuple; slow logs include `policy_generation`, with zero denoting
bootstrap ownership. The adopted threshold wins conflicting environment input on
verified restart. Preparation alone never changes the threshold.

Future group adapters use their own files, actual resources and concrete marker
dependencies. They can report hot, worker-restart or process-restart behavior
through the same observation contract. Pending/restart or failed operational
application does not itself invalidate authentication authority or block management
recovery. Search additionally needs its trusted direct-RPC generation guard;
health readiness alone does not block calls. Search and replication worker
fingerprints must be installed coherently by their separate integration packages.

This source package does not complete all 44 settings. Unimplemented groups remain
unavailable, while a recovered unsupported prepared/adopted group refuses public
startup. Native/API/Admin and final real-server acceptance remain tracked by each
group Issue and Epic #1990.
