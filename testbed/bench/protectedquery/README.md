# Protected query preparation (#1612)

This is an explicitly selected standalone preparation lane using production
Server admission. It is separate from `run.sh`, Compose, `shared_ranking.yaml`,
the release scenarios and their unchanged workloads/floors. It supplies no final
performance verdict, HA evidence or provider login evidence. Matching S4
admission/disclosure integration is delivered through #1725/#1726; this fixture
adaptation does not repeat that implementation or close #1612/#1610 acceptance.

`authfixture -protected-query -query-security-profile legacy-v1` retains the
original one-Server preparation. Explicit `-query-security-profile current-v2`
uses three production Server processes and the existing fixed three-member sys
authority; the measured data and independent writer use only node 0. OFF uses
the same three-process layout with authentication disabled. No data replication,
receipt or HA workload is enabled. Both profiles use a short-lived
loopback HTTPS issuer. Both OFF and OIDC retain verified public TLS and HTTP/2;
neither OS trust nor Keychain is changed. The issuer signs 15-minute Ed25519
access JWTs, exposes discovery/JWKS, and serves no browser/login/token issuance
endpoint. Its signing key stays in memory; credentials live in the owned private
fixture directory only. Stop the supervisor with `shutdown` on stdin; retain
its normal exit, raw logs and current shutdown receipt before removing private
state. Only paths, public profile bindings, binary hashes and source metadata enter readiness
output. Preserve private diagnostic logs after failure, excluding credentials.

The measured OIDC reader is a synthetic local end-user Bearer JWT, bound through
the existing assignment API for legacy, or the independently authored original
current genesis for current-v2, to the same `fixture_query_reader`. That Role allows Vertex
read under `bench:`, explicitly denies `bench:private:`, and independently allows
Query. The issuer reserves these synthetic end-user subjects and issues no OAuth
client tokens. This exercises actual production JWT validation and Role admission;
it does not qualify a real provider or an authorization-code login. An independent
writer producer uses the existing named machine credential with all-data
`fixture_data` authority. OFF sends no credentials. Writer results therefore do
not represent OIDC JWT validation cost.

The deterministic logical corpus reuses the complete `broad_illuminate` 64-way,
three-hop walk and two dense 32-member communities, plus shared ranking documents
and high-weight denied bridges. OFF/ON receive the same logical keys, values,
edges, expiration policy (no TTL), topology, request shapes and offered schedule.
There is no query-prefix substitute for authorization. Seeding verifies every
plural Put outcome. Preflight checks actual visible counts, exact mode-specific
Search keys and nonempty
BFS/PPR/Community results, missing/invalid JWT rejection, exact Role and denied-seed
rejection, and absence of denied vertices/edges or paths through them. The
all-data writer control must actually reach the planted bridge-only destination.
Shared statistics remain corpus-wide; hidden updates may change visible ranking.

The expected Search set is `bench:ranking:a`, `bench:ranking:b` and
`bench:private:best` in OFF; OIDC expects only `bench:ranking:a/b`. Empty, missing,
duplicate or unexpected results refuse. OFF may traverse the planted private
bridge; OIDC must exclude private vertices/edges and the bridge-only destination.
The comparison axis is `end_to_end_mode_specific_authorized_results`. These
correct differences are preserved, rather than requiring OFF/ON result-hash
equality. Same-allowed-set authorization-overhead comparisons require a
separately selected lane and are not added here.

Build matching binaries from a frozen clean source, outside the checkout.
Current also requires the original-input exporter test binary with VCS metadata:

```sh
go -C server build -buildvcs=true -o ../../tools-1612/lantern ./cmd
go -C server build -buildvcs=true -o ../../tools-1612/authfixture ./cmd/authfixture
go build -buildvcs=true -o ../tools-1612/protectedquery ./testbed/bench/protectedquery
go -C server test -c -buildvcs=true -o ../../tools-1612/security.test ./internal/security
```

For each mode, choose an unused public loopback port and a new absolute private
directory. Start the fixture with its stdin retained by the owner, and save its
single readiness JSON privately. The issuer selects its own loopback port.
The retained legacy profile still uses its writer fence and a bounded 60-second
wait. Current requires actual native time, installed current quorum and data
readiness, then protocol 2, the expected member and complete original profile
binding. An OFF or legacy response cannot satisfy current OIDC readiness. No
clock/authority override or fallback is added.

```sh
../tools-1612/authfixture -protected-query -mode oidc \
  -directory /absolute/new/private-directory -public-ports 16380 \
  -serve /absolute/tools-1612/lantern -ready-timeout 60s
../tools-1612/protectedquery -fixture /absolute/private/ready.json -action seed
../tools-1612/protectedquery -fixture /absolute/private/ready.json -action preflight
```

For current, choose three distinct public ports and the existing qualified native
Darwin/arm64 or Linux/amd64/arm64 environment. Its read-only `/etc/ntp.conf`
selected source and DNS/UDP path must already satisfy the existing native profile;
this lane neither edits OS time/trust/network configuration nor manufactures a
qualification Boolean. The private authority endpoints/keys/genesis are authored
by the existing test-only exporter, not borrowed from a live installation.

```sh
../tools-1612/authfixture -protected-query -query-security-profile current-v2 \
  -mode oidc -directory /absolute/new/private-current \
  -public-ports 16380,16381,16382 -serve /absolute/tools-1612/lantern \
  -current-fixture-exporter /absolute/tools-1612/security.test -ready-timeout 180s
```

Current fixture membership uses the existing ten-minute maximum, without renewal;
`expires_at` is bounded by both membership and the fifteen-minute synthetic JWT.
Current JWT iat/auth_time use the existing native fixture's one-minute preceding
signed time so that its strict start can precede the conservative UTC lower
endpoint; verification bounds and the fifteen-minute exp-minus-iat stay unchanged.
The driver refuses insufficient remaining lifetime before load. Setup uses the
same synthetic administrator's normal ListRoles read to verify the original
reader Role/full binding and prime discovery/JWKS; it does not send a legacy
scalar Apply or mint a new security operation. First remains the first graph
query after setup/ingestion, not cold provider discovery. Setup capability probes
on all three endpoints are outside the timed interval and cannot become cached
authority grants.

On stdin shutdown, current signals all three owned children and joins them with
one 90-second fixture budget. Successful exit requires every child exit code 0
and its `server stopped cleanly` log. OIDC additionally requires fresh-cycle
CLEAN, each original custody binding and the SHA-256 of its exact final floors.
`query-shutdown.json` records every child and the verdict. Missing/mixed/RUNNING
state, abnormal exit or deadline/kill fails; raw files/logs remain. OFF has no
current custody or current-authority claim. This is orderly process observation,
not intact-resume, backup/VM rollback or physical-host qualification.

Repeat with `-mode off` on the same pinned binaries and a fresh owned directory.
The fixture never reads real provider configuration or retained credentials.
Unknown profiles, plaintext, wrong selected process count, data peers and expiring credentials
fail closed. The seed and preflight actions perform no offered performance load.

The explicit short real-wire regression uses absolute paths to these binaries:

```sh
LANTERN_PROTECTED_QUERY_SERVER=/absolute/tools-1612/lantern \
LANTERN_PROTECTED_QUERY_FIXTURE=/absolute/tools-1612/authfixture \
LANTERN_PROTECTED_QUERY_DRIVER=/absolute/tools-1612/protectedquery \
go test ./tests/integration -run '^TestProtectedQueryPreparation_ProductionWire$' -count=1 -v
```

For the current native seed/preflight-only wire gate, use the same inputs plus
`LANTERN_PROTECTED_QUERY_CURRENT=1` and the absolute exporter:

```sh
LANTERN_PROTECTED_QUERY_CURRENT=1 \
LANTERN_PROTECTED_QUERY_EVIDENCE_DIR=/absolute/new/private-current-wire-evidence \
LANTERN_PROTECTED_QUERY_SERVER=/absolute/tools-1612/lantern \
LANTERN_PROTECTED_QUERY_FIXTURE=/absolute/tools-1612/authfixture \
LANTERN_PROTECTED_QUERY_DRIVER=/absolute/tools-1612/protectedquery \
LANTERN_PROTECTED_QUERY_EXPORTER=/absolute/tools-1612/security.test \
go test ./tests/integration -run '^TestProtectedQueryPreparation_CurrentProductionWire$' -count=1 -v
```

The current wire gate requires a new private absolute evidence directory and
retains both mode directories, source/readiness, seed/preflight reports, original
inputs, logs and custody on success or failure. These private files include fixture
credentials/keys; do not publish the directory. A selected gate with missing native
inputs fails qualification; an unselected gate reports a skip, never evidence.

Ordinary root tests cover the driver, report-pair contract and topology. The
legacy wire test reports a skip without its three binaries; selected current
requires all four and its native/evidence inputs. A skipped test is not real-wire evidence. Fixture tests run separately with
`go -C server test ./cmd/authfixture`.

After normal self-check and the owner's exclusive native/heavy-slot allocation,
prepare each of Search/BFS/PPR/Community in all three phases. `measure` requires
clean matching Server/driver VCS revisions, a pinned Server binary SHA-256 and
sufficient JWT lifetime. It never seeds or preflights implicitly:

| Phase | Required state | Independent writer |
| --- | --- | --- |
| first | Fresh fixture, seed only, no earlier Search/traversal; exactly one query | none |
| warm | Fresh matched fixture; same predeclared warmup/preflight in each mode | none |
| update-mixed | Fresh matched warmed fixture; same fixed schedule and existing visible/hidden mutation sequence | explicit positive `-writer-rps` |

Validate a sacrificial sibling fixture with the same source/corpus before a first
sample, then seed a new fixture for each family. Calling preflight on the measured
first fixture warms it and invalidates a first-query claim.

Here `first` means the first query on the loaded corpus and reader scope. OIDC
setup during seed has already authenticated the setup administrator and primed
issuer metadata/JWKS; this is not cold provider discovery or the first
JWT validation. It does not mean an empty search index before ingestion.

Capture readiness,
seeding, phase order and raw report hashes in the owner manifest; the driver alone
cannot attest that the caller has never queried a fixture. Predeclare the load,
duration, warmup, host conditions and run budget in #1610 before measuring.
The following is command syntax, not an adopted final load or an acceptance floor:

```sh
../tools-1612/protectedquery -fixture /absolute/private/ready.json -action measure \
  -family search -phase update-mixed -count 80 -rps 40 -writer-rps 20 \
  -concurrency 8 -timeout 5s -report /absolute/new/on-search-update.json
python3 -B testbed/bench/protectedquery/compare.py \
  /absolute/off-search-update.json /absolute/on-search-update.json
```

Use identical declared flags for the OFF counterpart. Absolute offered slots are
scheduled independently for reader and writer; saturation, scheduler lateness,
RPC failure or wrong results retain failed slots and fail the diagnostic. There
is no retry or silent conversion to closed-loop load. Every writer RPC/result is
retained separately from the reader. For current, compare only after successful
owned shutdown, adding
`--off-shutdown /absolute/off/query-shutdown.json` and
`--on-shutdown /absolute/on/query-shutdown.json`. Both receipts must match their
report fixture ID, source/binaries and profile binding; all three exits and ON
fresh CLEAN hashes must pass. Missing, failed or unrelated teardown refuses.

Pair comparison rejects source/binary, corpus, topology, transport, actor, phase/load and offered-slot mismatches, and
always leaves final acceptance `not_qualified`.

Latency is client dispatch through RPC and result validation: it includes
encoding, verified transport, admission, execution, decoding and validation,
but excludes setup and scheduled waiting. Independent mixed writers can also
expose different intermediate snapshots; validate each mode against its own
correct result contract, without serializing writers behind readers. OFF/ON authorized outputs differ
in size and graph work; the difference includes JWT validation, Role admission,
authorized-view execution and client result work. It cannot isolate pure JWT CPU
cost. Writer RPC latency is not internal lock waiting time.

Server allocations, retained/peak memory, internal lock wait, export revocation,
TTL/Delete/restore and host qualification are not collected by this driver and
must remain `not_measured`. The existing operator-local
`capture/diagnostics_bridge.py`/runtime metrics can support a separately approved
final sampler; do not expose diagnostics to the data reader or force GC during
steady measurement. Matching S4 admission integration, publication/disclosure
revalidation and policy-token binding are delivered source/CI work. The final pinned-source
measurement matrix remains unfinished. A successful preparation lane does not
close #1612.
