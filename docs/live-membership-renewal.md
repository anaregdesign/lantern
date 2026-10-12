# Finite control and data membership renewal

Tracks #1610. A long operator window consists of many short signed memberships.
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

For a two-hour window, choose `End` two hours after the planned start and a
separate cleanup allowance before every chain expires. The tool does not start
or stop servers or modify OS trust. The orchestration owner must enforce the
declared runtime/cleanup deadline. A short fixture transition test does not
constitute a two-hour runtime qualification or real-provider data acceptance.

Keep the configuration immutable for this run. All target directories must
support private temporary files, atomic same-directory rename and directory
sync. Retain one operator state path and its stable sidecars for the cohort.
Do not restore snapshots of only part of the state/targets or delete lease files.
The journal is local durable evidence, not an external rollback witness.

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

At `End` the tool stops issuing/publishing and exits. An interrupt joins the
current synchronous file operation, releases its leases and exits nonzero; keep
the state. Membership expiry then closes protected operations. The orchestration
owner stops/joins workers, listeners and native owners, checks the exact CLEAN
floor hash and completes owned fixture cleanup by `CleanupBy`. Preserve reusable
certificates/trust only under their existing separate authorization.

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
