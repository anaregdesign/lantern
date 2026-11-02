# ADR 0013: Managed operational settings

Status: log-level pilot implemented. Qualification evidence is tracked in
[Issue #1995](https://github.com/anaregdesign/lantern/issues/1995).
Tracking: [Epic #1990](https://github.com/anaregdesign/lantern/issues/1990).
Baseline: `196dc9bcaf2f94ef3b94de6059367b2da8cf73de`.

## Problem and ownership

Some operational values should be editable in Admin and survive restarts. Moving
every environment variable into `sys:` would create a bootstrap cycle, expose
secret material and imply unsupported hot reload. Persistence and runtime
application are separate operations.

Ordinary values and semantic validation already belong to `server/internal/config`.
Primitive environment parsing belongs to `server/internal/envconfig`. Provider
owns composition, material resolution and runtime certification. Preserve those
boundaries. The generated [environment reference](../env.md) is the actual input
inventory, including retired settings; it is not a management allowlist.

Existing `sys:` state is a typed security control plane. Extend its explicit
management commands, certified current authority, audit, consensus and verified
recovery. Never use ordinary graph writes, LWW, a generic configuration map or a
parallel consensus/storage system for managed policy.

## Initial catalog

Only `logging.level` is selected for initial management. The remaining rows
classify inputs and candidates; they do not enable them.

| Key / current input | Type and default | Scope / owner | Validation and change mode |
| --- | --- | --- | --- |
| `logging.level` / `LANTERN_LOG_LEVEL` | enum, `info` | Cluster desired policy; node-local application | Canonical `debug`, `info`, `warn`, `error`; hot apply after explicit adoption. Env accepts `warning` as `warn`. |
| Admission RPS + Burst | finite float `0`; canonical integer burst, currently derived as twice positive RPS | Cluster policy enforced independently by each process | Deferred atomic pair. Never a cluster-sum limit. Resident admission owner and management lockout recovery required. |
| Slow RPC threshold | integer milliseconds, `500`; `0` disables | Operational candidate | Deferred; existing middleware is assembled at startup. |
| Log format | string, `json` | Host/operator | Startup-only in pilot. |
| Public/peer/metrics/pprof listeners, TLS, CORS, reflection | Existing typed startup values | Host/security owner | External or dedicated security path; excluded from pilot. Metrics default is `:9090` in OFF and `127.0.0.1:9090` in OIDC. |
| Node identity, storage/WAL/recovery/spool paths, cluster trust and signing custody | Existing identity/storage inputs | Local bootstrap/private authority | Remain externally supplied; preserve ownership, lease and restore bindings. |
| LLM API keys, client secrets, CDC key rings | Secrets or secret references | External custody/dedicated security | No plaintext values in general `sys:` settings, Admin or public diagnostics. |
| Search/index/query/cursor/session/semaphore settings | Existing typed limits | Individually designed future policies | Deferred; each needs in-flight, fingerprint, transition or rebuild semantics. |
| `LANTERN_DEFAULT_TTL_SECONDS` | integer, `60` | Currently reported default metadata | Do not claim it changes writes with omitted expiration. |

Existing authentication/authorization configuration retains its dedicated
management paths, env-owned restrictions and configured-Issuer reconciliation.
Settings management must not reset Roles, assignments or suspended administrators.

## Input foundation

[Issue #1991](https://github.com/anaregdesign/lantern/issues/1991) replaces the
process-global registration/findings/set-key records with one Reader per load.
The Reader copies its environment input once. Ordinary, security and conditional
loaders share it; unknown-key validation runs after all registrations. Separate
loads, including concurrent loads, do not share diagnostics or process mutations.
The env-reference generator uses an explicit empty input instead of clearing the
process environment.

Preserve current primitive parsing/defaults, strict malformed/unknown handling,
always-enforced semantic validation and foreign namespace exclusions. A present
empty String remains different from an absent String; trimmed primitive empties
use their defaults. Security presence checks, retired-prefix rejection and OFF's
rejection of even present-empty security JSON read the same captured input.
Tracing activation/protocol selection also reads that input; other exporter
options retain their OpenTelemetry SDK ownership.

Domain settings and validation must accept typed values independently of an
environment source. Do not create an environment-specific managed-policy model.

## Authority and adoption

The pilot uses current-authority/OIDC management only. OFF keeps environment-owned
startup behavior. It does not gain an unauthenticated settings store or writer.
Initially reuse `security.manage`; adding a new grant is not necessary and live
credential/grant changes are outside this work.

An absent policy in a verified current Image means unadopted. Admin shows the
contacted node's effective environment/default value and requires the operator to
choose a value explicitly. A reviewed typed log-policy command commits the first
managed value. Never auto-import different node environments at startup.

After adoption, committed state is authoritative on every restart. Environment
values cannot overwrite it. The pilot has no emergency environment override and
no delete/reset-to-environment operation. Rollback selects a prior value through
a new reviewed commit and advances generation; it never rewinds history.

Use a closed, versioned, bounded policy field and typed command. An optional
omitted Image field must preserve existing canonical absent-policy bytes. Reject
unknown schema, kind, fields, enum and invalid/overflow generation. Derive the
next generation inside committed application; callers supply intent, not an
authoritative generation. Other security edits must preserve adopted policy.

Review and Apply bind actor, exact semantic cut and command. Existing current
Roles, credential/session expiry, CSRF, freshness, env-owned locks and
administrator invariants remain. Machines cannot mutate management policy.
Same ID and same intent resolve to the retained original result; changed intent
conflicts. Stale reviews must be refreshed. Lost-response recovery checks original
status before retrying and never silently creates a new identity.

Operational-only changes and batches containing any operational write use
ordinary capacity. They must not be classified as proven nonexpanding merely
because permission grants are unchanged: the security revocation reserve remains
available exclusively for its existing purpose.

## Recovery, rollout and readiness

Persist through existing certified native history, WAL, materialized capsules and
private snapshots. Do not introduce an unverified settings sidecar. Unknown,
corrupt or unavailable authoritative state cannot be interpreted as unadopted or
silently replaced with environment/default state. Verify committed recovery and
install the policy before public readiness; preserve restore-before-listen and
the exact graph/Store/origin/Log/HLC runtime certification.

Bootstrap diagnostics emitted before verified recovery may use the startup
logger. That does not establish the serving policy. After authority loss, retain
the last verified applied level while preserving existing security readiness
failure; never fall back to another source.

First adoption requires support from **every fixed voter and enrolled origin**,
including nonproposing/read-only participants. Native genesis must remain
unadopted: compiling a historical committed Image does not permit provisioning
that Image as fresh genesis. No operator-provided readiness boolean is accepted.

Prepare, read and review do not change durable state. Explicit Apply consumes and
retains one original H through the existing native path. Authenticated private
peers verify that exact intent, the reviewed cut, current management authority,
required operation-purpose proof, and an actually installed logging owner. They
preflight the bounded candidate and completion resources using ordinary capacity,
persist a current-profile-only P journal minimum-reader-version marker, synchronize
the WAL and chain tip, and only then sign. A separate candidate envelope carries
the unchanged original H and all distinct voter/origin support signatures. Bare H
cannot activate first adoption. Public graphs, exports and CDC never carry these
private records or certificates.

The preceding closed P grammar refuses the new marker. The existing native lease
prevents a simultaneous old process on the same family, and complete-history
resume never truncates an unknown application record into an older startup. P has
no compaction today; any future compaction must retain the minimum-reader version.
This does not create a new external rollback witness for replacing an entire
native/custody family with an older valid copy. Existing independent floors retain
that responsibility. An older binary is not a valid rollback target after the
marker is written, even if the later policy commit cannot finish. First-adoption
review must disclose this irreversible downgrade restriction before Apply.

An offline enrolled participant blocks adoption. A partial preparation retains
its local marker and a read-only reference to the original change ID, intent and
actor digest; that reference does not claim commit. An incomplete original uses
bounded ordinary resources and cannot block independently authorized security
revocation. If a genuine security change advances the semantic cut, status-only
recovery may commit the original's existing CAS refusal without a support
certificate: the exact-cut check rejects it before any policy effect. It never
remints an identity or consumes credentials again. Admin keeps logging recovery
separate from urgent security management while retaining one application store.

The protocol remains ineligible by default until the production composition root
installs the actual resident logging owner. Merely decoding the model or reporting
a version cannot supply that eligibility. The pilot adds no generic capability
negotiation, independent consensus system or settings sidecar.

## Effective runtime and Admin

One focused runtime owner controls a shared `slog.LevelVar`. Retained/default
loggers and their `With` descendants observe its updates. Log format, metrics
and pprof listeners remain startup-owned.

Startup source metadata reflects successful parsing: valid explicit environment
input, including an explicit `info`, is environment-owned; absent, blank or
malformed input uses the default. An adopted policy reports managed ownership.
Presence alone cannot determine the effective source.

`LANTERN_LOG_LEVEL` remains a validated bootstrap input even after adoption.
Malformed input with `LANTERN_STRICT_CONFIG=true` refuses startup before verified
recovery. Without strict mode it produces the existing diagnostic and bootstrap
default. After verified recovery succeeds, managed policy supplies the serving
level; a valid conflicting environment value never overwrites it.

Public certification requires the exact logger owner and a successfully verified
local recovery projection. Install recovered policy before binding native runtime
support. The existing private authority listener starts independently so other
members can form a quorum; fresh native eligibility and local application are
still required before public readiness. The bounded existing readiness worker
reconciles later policy generations without constructing a full Image each poll.

Expose immutable snapshots of the contacted node: effective value/source,
desired/applied generation, exact committed cut, pending/error and whether restart
is required. Mark a generation applied only after successful installation.
Superseded work cannot regress the generation. A commit response proves a saved
policy, not application on every node. Peer state that has not been observed is
unknown; the pilot makes no all-node application claim or new aggregation protocol.

The applied cut is historical installation provenance. An unrelated security
commit does not reinstall an unchanged log policy or manufacture pending state.
Fresh admission and guarded output separately bind a settings read to the current
security cut. Authority loss fails fresh reads/readiness while retaining the last
verified installed level; a fresh read can confirm that installation after
visibility returns.

A dedicated Admin settings feature follows existing use-case/port/adapter
boundaries and reuses reviewed-change and unknown-result recovery. It is separate
from the Role/Issuer editor. Display adoption, source and local application state;
handle cancellation, stale review, conflict and reload recovery explicitly.

## Delivery and evidence

| Order | Issue | Cohesive outcome |
| --- | --- | --- |
| 1 | [#1991](https://github.com/anaregdesign/lantern/issues/1991) | This design/catalog and isolated load foundation |
| Preparation | [#1997](https://github.com/anaregdesign/lantern/issues/1997) | Actual serving logger owner, before native support attestation; startup-owned only |
| 2 | [#1992](https://github.com/anaregdesign/lantern/issues/1992) | Typed committed policy, capacity, rollout and verified recovery |
| 3 | [#1993](https://github.com/anaregdesign/lantern/issues/1993) | Logger owner, local desired/applied generations and readiness |
| 4 | [#1994](https://github.com/anaregdesign/lantern/issues/1994) | Authenticated API, generated consumers and Admin review/apply |
| 5 | [#1995](https://github.com/anaregdesign/lantern/issues/1995) | Final integrated exact-source pilot acceptance |
| Later | [#1996](https://github.com/anaregdesign/lantern/issues/1996) | Separately scheduled atomic admission policy design |

The logger preparation is separately reviewable. It does not enable policy
adoption, management writes or an application-status claim. The native protocol
must attest an actually installed owner, and verified projection/readiness
integration follows in #1993.

Required tests include isolated concurrent loads; schema/canonical rejection;
adoption/update/new-generation rollback; idempotency/conflicts/lost response;
machine/unauthorized/stale/revoked/CSRF refusal; ordinary versus reserved capacity;
retained logger updates; lag/error snapshots; conflicting-env restart; corrupt or
missing adopted state; mixed-version refusal; and graph/export/CDC isolation.
API changes include real Connect happy/refusal tests and all generated consumers.
Use isolated fixtures; do not change live credentials, grants or security settings.

Independently review new safety boundaries and final code, then run pinned tools
and the repository's affected local gate before every push. Configuration inputs
currently select the full local plan. Required synthetic-merge and main CI remain
independent, including configured-source integration when selected. Follow
[#1974](https://github.com/anaregdesign/lantern/issues/1974)'s claim/input/invalidation
rules and preserve failed verdicts. No threshold weakening, control bypass,
unrelated performance campaign, release or deployment is part of this initiative.
Track merged source/CI, final exact-source acceptance, publication and human/device
evidence separately on the Epic.
