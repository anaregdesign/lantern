# Supervised control and data membership renewal

Tracks #1742 and #1610. A long operator window consists of many short signed memberships.
Every signature remains at most ten minutes long. Renewal does not extend a
session, token, Code transaction, purpose, private handshake, RPC or output unit.
It does not change the cohort, voters, workload keys, Roles or time profile.

`CurrentAuthority` retains the canonical control manifest input from the final
custody-checked provisioning. `SecurityWorkers` rereads that file and the data
manifest independently on its existing one-second cadence, including while
readiness is false or the old membership has expired. The control read joins
owner shutdown and passes the complete bytes to the existing gated `Refresh`.
There is no new listener, watcher, clock, admission cache or readiness shortcut.

## Declare and provision a finite window

Build the external operator with `go build -o membershippublisher
./server/cmd/membershippublisher`. Keep its keys and persistent journal outside
the server inputs, repository and public evidence. The serving processes need
only their existing operator public keys and manifest inputs.

Prepare one private JSON configuration with the following fields (duration
values are integer nanoseconds, as with Go `time.Duration` JSON):

| Field | Required value |
| --- | --- |
| `Version` | `1` |
| `End` | Explicit UTC time bounding publisher activity and every new signature |
| `CleanupBy` | Later explicit UTC deadline for joining and cleaning the owned runtime |
| `Interval` | Early renewal cadence, e.g. `60000000000` for one minute |
| `Lifetime` | e.g. `300000000000` for five minutes; at most ten minutes, with more than twice the cadence plus four seconds of overlap capacity |
| `Streams` | Exactly one `control` and one `data` stream |

Each stream has `Kind`, `KeyFile` (private Ed25519 PKCS#8 PEM), `RootsFile`,
`Initial` (base64 of the exact initial signed manifest), `Targets` (canonical
absolute input file paths) and `Certificates` (one PEM leaf/chain for every
pinned workload). These are existing private files, not checkpoint M/P/B paths.
The roots digest must match the signed configuration. Each leaf must match a
member's SPKI and URI; both client/server chains must verify now and beyond
`CleanupBy` plus the existing clock margin. These checks do not replace the
server's own certificate and native-time admission.

For ordinary unattended operation, choose `End` at the explicitly approved next
maintenance date, for example a month away when the TLS chains cover it. There
is no two-hour campaign cap. The supervised process automatically renews short
memberships throughout that interval; do not build a cron that silently advances
End or require repeated manual extensions of short experimental windows. Choose
a separate cleanup allowance before every chain expires. The tool does not start
or stop servers or modify OS trust. The orchestration owner must enforce the
declared runtime/cleanup deadline. A short fixture transition test does not
constitute a two-hour runtime qualification or real-provider data acceptance.

Keep the configuration immutable for this run. All target directories must
support private temporary files, atomic same-directory rename and directory
sync. Retain one operator state path and its stable sidecars for the cohort.
Do not restore snapshots of only part of the state/targets or delete lease files.
The journal is local durable evidence, not an external rollback witness. In
particular, restoring the pre-transition journal binding while target bytes are
unchanged cannot be detected from those files alone. Never restore a journal or
partial snapshot to recover a failed transition.

## Start, observe and stop

Start explicitly with:

```sh
membershippublisher -config /absolute/private/publisher.json \
  -state /absolute/private/publisher-state.json -mode init
```

`init` requires an unused journal and unused publication lease names, and every
target must exactly match its stream's `Initial`. The tool pins the signed
profile/domain/member set and advances only its revision and validity interval.
It holds a journal lease plus each target's `.publisher.lease`, preventing two
cooperating publishers even if they select different journals.

Before distributing a successor to **any** target, it retains its revision,
SHA-256 digest, exact signed bytes, immediate predecessor and pending status in
the private journal. Every journal/target replacement performs temporary create,
write, file sync, close, rename and directory sync. A target rename alone is not
durable delivery. Partial delivery leaves the exact pending candidate intact;
other targets and the other stream can progress. The next retry sends only those
saved bytes. Once all targets are durable, completion is recorded before another
revision is signed. Server acknowledgement is not implied by file publication.

Observe both stream revisions and each node's existing readiness/health and
control floor diagnostics. Exact replay is not renewal and can return success
after expiry in the control store. A future candidate can be refused initially
and reevaluated on the next worker tick; never replace it with different bytes
at the same revision. Control/data revisions need not be equal.

At `End` the tool stops issuing/publishing and exits. SIGINT or SIGTERM joins the
current synchronous file operation, releases its leases and exits zero on a
successful normal stop; a flush, status-output or close failure exits nonzero. Keep
the state. Membership expiry then closes protected operations. The orchestration
owner stops/joins workers, listeners and native owners, checks the exact CLEAN
floor hash and completes owned fixture cleanup by `CleanupBy`. Preserve reusable
certificates/trust only under their existing separate authorization.

## Continue at a planned maintenance boundary

Stop the publisher and wait for lease release. Retain the old immutable config,
journal, sidecars and every target. Prepare a **different private file** whose
decoded configuration differs only in later `End` and `CleanupBy`; the cleanup
allowance cannot shrink. Then explicitly run:

```sh
membershippublisher -mode advance-window \
  -config /absolute/private/window-1.json \
  -next-config /absolute/private/window-2.json \
  -state /absolute/private/publisher-state.json
membershippublisher -mode resume -config /absolute/private/window-2.json \
  -state /absolute/private/publisher-state.json -maintenance-warning 168h
```

The operation holds all existing leases, authenticates the old journal against
the **old** config and issuance End, checks the new period's currently valid TLS
chains, and durably changes **only the journal config binding**. It signs no
membership and changes no candidate, revision, predecessor, pending flag or
target. It cannot issue from the transition owner; resume is a separate explicit
operation. Do not edit a bound config in place. Leaving a supervisor on the old
config fails closed after a successful transition.

Expired but intact old state is eligible for this explicit transition. A renewed
same-SPKI/URI leaf need not have been valid at the obsolete old CleanupBy; it must
be valid now, with a selected complete client/server chain continuously covering
the new CleanupBy plus clock margin. This preflight does not prove running nodes
have loaded the staged certificate.

After an uncertain transition, retain both configurations and inspect the
canonical journal. If it matches the new config, explicitly resume that config
after full validation and retained-byte sync. If it matches the old config and
the old End has expired, ordinary resume is insufficient: explicitly repeat
`advance-window` after revalidation, then resume the new config. If even the new
End has expired, use it as the old config for the next approved period. Unknown,
missing, noncanonical or inconsistent state stops recovery. Never promote a
temporary file, infer from mtime, reset floors or fall back to init.

Withhold a next-window authorization, or stop/disable the publisher, to withdraw
further issuance. Already signed memberships retain their original short expiry;
stop is not instant revocation. Journal-before-target preserves the exact bytes
of durable/publishable revisions, not literally one invocation of the signing
function across a crash before the candidate was first saved.

## Status and action before expiry

The running CLI emits bounded JSON to stdout every five seconds and on failure
category changes. It uses stream names and target ordinals, never private paths,
identities, key material or raw OS errors. `journal_version` is the last confirmed
durable candidate, not an in-memory successor after an uncertain save. Each
target reports its own observed revision/expiry and last completed durable
delivery; null delivery dates after restart mean unknown historical timing.
Matching readback alone is not a successful delivery: file and directory sync
must both complete. A supervisor may retain prior records as historical evidence.

`delivery_retry` means exact retained target publication can retry.
`journal_uncertain` or `operator_recovery_required` requires stopped, intact-state
operator recovery. `trust_capacity`, `budget_exhausted`, `clock_rollback` and
`reporting_failure` distinguish other stop causes. Do not automatically restart
forever or map these errors to init. Alert when status becomes stale as well as
when its contents warn; the last green record does not prove a live process.

For cadence I and the existing two-second margin M, delivery warns immediately
on an error or when the observed target has at most `2I + 2M` signed validity
remaining, and becomes urgent at `I + 2M`. With one-minute renewal and five-minute
signatures, these leads are 124 and 64 seconds. They are warning budgets, not a
recovery SLO. `-maintenance-warning` defaults to seven days and is clamped to at
least the delivery warning plus cleanup allowance. Select a practical larger
lead when certificate renewal or maintenance needs it. The maintenance warning
covers the effective End and selected TLS chain capacity, including issuers.

These are wall-clock operator diagnostics. They never replace the consumers'
native-time, quorum, floor, credential or permanent-fault checks. File delivery
does not establish node acceptance; use existing node reload/floor and
readiness/health diagnostics separately. The internal floor export is not an
HTTP endpoint. A readiness failure alone is not a specific RPC refusal, and
repeated reload warning entries need not be distinct incidents.

## One supported container distribution topology

Use a publisher on one host with private per-node **directory** bind mounts
into its serving containers. Individual-file bind mounts retain the old inode
after atomic replacement and are unsupported for renewable inputs. Operator
signing keys and journal stay outside all serving mounts. Each container sees
only its own static/renewable inputs read-only, and its own writable durable
state. Nodes still hold their own necessary TLS/voter keys, not the operator key.

The systemd example below targets a native Linux host. The bounded container
fixture also supports a macOS host publisher with Linux containers; its receipt
records that host and immutable image explicitly. Shared filesystem durability
and ownership remain deployment prerequisites; a Docker Desktop run does not
certify native Linux storage or power-loss behavior.

The selected time source must be reachable from each container. The native
producer rotates the configured hostname's bounded DNS answers. Before sending,
local `ENETUNREACH`, `EHOSTUNREACH` or `EAFNOSUPPORT` dial failures can advance to
the next answer within the original three-second attempt. This permits a usable
address family when another has no route. Each scheduled attempt still sends at
most one NTP datagram; it never switches after a write/read failure or source
response. IPv6 assignment or successful UDP dial alone does not prove reply
reachability. Packet loss can still exhaust the unchanged 60-second holdover
and withdraw readiness even while membership publication succeeds. Diagnose
transport and native admission separately; do not extend time bounds to repair
a network failure.

The existing HA Compose contract has a concrete overlay:

```sh
docker compose -f deploy/compose/docker-compose.yml \
  -f deploy/compose/docker-compose.ha.yml \
  -f deploy/compose/docker-compose.membership.yml config
```

Set existing HA env inputs and fixed image, plus `LANTERN_MEMBERSHIP_ROOT`
(the example supervisor uses `/srv/lantern`) and the pre-provisioned
`LANTERN_SERVICE_UID`/`LANTERN_SERVICE_GID`. Each `node-0`/`node-1`/`node-2` has
`static`, `renewable` and `state` directories. The static node config points to
`/run/lantern-renewal/control.signed` and `data.signed`, with all mutable
checkpoints/WAL/custody under `/state`. The inherited node-0 backup directory is
also under `/state/backup`. Private directories/files remain 0700/0600; align
actual host/container ownership rather than broadening modes. Validate the
rendered mounts: the overlay uses Compose 2.24.4+
[`!override`](https://docs.docker.com/reference/compose-file/merge/#replace-value)
so no broader inherited input mount remains. No missing host path is auto-created.

`deploy/compose/membershippublisher.service` is the host supervisor example.
Install the built binary at its configured path and supply the existing service
identity, private layout and `/etc/lantern/membershippublisher.env` through the
operator's normal provisioning. The unit creates no identity or key. Perform
the first explicit `-mode init` once, observe publication, stop it normally, then
start the unit's explicit resume command. The unit keeps state across stops,
retries no terminal failure and has no SIGKILL fallback. It reports status to
the existing supervisor journal, requiring no new backend. A stuck normal join
is a retained-state failure, not permission to force termination.

For maintenance, update the supervisor's selected immutable config only after
the reviewed advance procedure. Do not use auto-init based on file existence.
On a full service stop, drain/join serving workers/listeners/native owners while
the publisher is still renewing; confirm every exact CLEAN floor hash, then
stop/join the publisher. Retain persistent volumes and sidecars. Resume serving
stores/WAL in their existing explicit intact modes and require native readiness
again. No production deployment, independent-host delivery or seamless rotation
is implied by these examples.

## TLS and signing-key maintenance

Plan certificate maintenance before the reported End/TLS capacity. Same-key leaf
renewal must preserve pinned SPKI, URI, roots and profile. Staging a new leaf is
not serving adoption: perform the planned normal serving restart and ordinary
TLS/native admission checks. A safe maintenance stop is supported; TLS hot reload
is not promised. Root/profile/cohort changes or operator/voter key replacement
require a separately authorized stopped/fenced provisioning transition. Raw
Ed25519 signing keys have no certificate-style NotAfter, and this tool neither
rotates them nor creates new authority. Insufficient capacity stops continuation.

## Partial distribution and explicit resume

After an interrupted publisher, retain all files and run the same command with
`-mode resume`. Resume requires the immutable configuration, journal and every
lease sidecar. It verifies both signatures, revision/digest, pinned bindings and
target bytes, then flushes the retained journal before any distribution. Targets
may contain the current candidate or, only while pending, its saved immediate
predecessor. Unknown, advanced, rolled-back, missing or unsafe inputs are rejected.
Never switch to `init` to repair missing state, roll back an advanced target, or
re-sign a distributed revision. An uncertain journal write stops the current
publisher; only explicit intact-state resume can proceed. Failed target writes
retry the saved candidate. If a saved candidate has expired, it is completed
exactly first; a subsequent tick can sign a higher live revision within the
remaining declared budget.

With qualified native time and intact surviving ownership, a higher same-profile
membership can recover an expiry-only refusal. Quorum, catch-up, credential,
scope and output-event checks still apply. An existing session may authorize a
new event without changing its original expiry. Old RPC/output windows do not
gain extra time. A changed control profile reaches the existing durable terminal
fence, including when the proposed interval is future or expired. Checkpoint I/O
uncertainty/panic, closed ownership and permanent time faults remain fail-stop.

## Bounded qualification

Paired tests cover file safety, expiry/future replay/fence, independent worker
reload, publisher persistence fault points, partial exact replay, lease conflicts,
state loss, certificate capacity and finite budgets. Existing store, final-time,
output, immutable private Admission and custody tests retain their contracts.

The native wire gate reuses the production public setup with the real native time
constructor, three synthetic voters, synthetic OIDC and a separate operator CLI:

```sh
go build -o /absolute/task/publisher ./server/cmd/membershippublisher
go test -c -o /absolute/task/exporter ./server/internal/security
LANTERN_CURRENT_MEMBERSHIP_GATE=1 \
LANTERN_CURRENT_FIXTURE_EXPORTER=/absolute/task/exporter \
LANTERN_MEMBERSHIP_PUBLISHER=/absolute/task/publisher \
go test ./tests/integration -run '^TestCurrentSecurityPublicNativeGate$' \
  -count=1 -v -timeout=4m
```

After native startup, the first 30-second publisher successor defines the
measured initial interval. Bootstrap retains its ordinary validity so the existing
32-second source-loss backoff cannot consume the transition test before it starts.
The gate then requires at least two further successors in both streams,
same-session/Role/Code and allowed data operations across the original deadline,
one delayed data target through expiry, publisher stop/expiry and intact recovery,
scope/expired-credential refusal, and latest-M CLEAN/intact resume. All keys and
identities are synthetic. Public HTTP uses the existing exact loopback trusted
gateway fixture; private control and the test issuer use TLS. This does not claim
a new TLS4 browser run or final real-Google data acceptance. The historical real
public-RPC refusal remains UNKNOWN; later successful traffic is not attribution.
