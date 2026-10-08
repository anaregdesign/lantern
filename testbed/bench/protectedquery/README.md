# Protected query preparation (#1612)

This is an explicitly selected standalone preparation lane using production
Server admission. It is separate from `run.sh`, Compose, `shared_ranking.yaml`,
the release scenarios and their unchanged workloads/floors. It supplies no final
performance verdict, HA evidence, provider login evidence or #1608 target
admission integration. Those exits remain open in #1612/#1610.

`authfixture -protected-query` supervises one production Server and a short-lived
loopback HTTPS issuer. Both OFF and OIDC retain verified public TLS and HTTP/2;
neither OS trust nor Keychain is changed. The issuer signs 15-minute Ed25519
access JWTs, exposes discovery/JWKS, and serves no browser/login/token issuance
endpoint. Its signing key stays in memory; credentials live in the owned private
fixture directory only. Stop the supervisor with `shutdown` on stdin, then remove
that directory. Only paths, binary hashes and source metadata enter readiness
output. Preserve private diagnostic logs after failure, excluding credentials.

The measured OIDC reader is a synthetic local end-user Bearer JWT, bound through
the existing assignment API to `fixture_query_reader`. That Role allows Vertex
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
plural Put outcome. Preflight checks actual visible counts, nonempty Search and
BFS/PPR/Community results, missing/invalid JWT rejection, exact Role and denied-seed
rejection, and absence of denied vertices/edges or paths through them. The
all-data writer control must actually reach the planted bridge-only destination.
Shared statistics remain corpus-wide; hidden updates may change visible ranking.

Build the three binaries from a frozen clean source, outside the checkout:

```sh
go build -o ../tools-1612/lantern ./server/cmd
go -C server build -o ../../tools-1612/authfixture ./cmd/authfixture
go build -o ../tools-1612/protectedquery ./testbed/bench/protectedquery
```

For each mode, choose an unused public loopback port and a new absolute private
directory. Start the fixture with its stdin retained by the owner, and save its
single readiness JSON privately. The issuer selects its own loopback port.
Readiness must wait through the existing 35-second writer authority fence;
`-ready-timeout 60s` is sufficient for this short gate on a suitable host. No clock
or authority override is added.

```sh
../tools-1612/authfixture -protected-query -mode oidc \
  -directory /absolute/new/private-directory -public-ports 16380 \
  -serve /absolute/tools-1612/lantern -ready-timeout 60s
../tools-1612/protectedquery -fixture /absolute/private/ready.json -action seed
../tools-1612/protectedquery -fixture /absolute/private/ready.json -action preflight
```

Repeat with `-mode off` on the same pinned binaries and a fresh owned directory.
The fixture never reads real provider configuration or retained credentials.
Unknown profiles, plaintext, multi-node/peer profiles and expiring credentials
fail closed. The seed and preflight actions perform no offered performance load.

The explicit short real-wire regression uses absolute paths to these binaries:

```sh
LANTERN_PROTECTED_QUERY_SERVER=/absolute/tools-1612/lantern \
LANTERN_PROTECTED_QUERY_FIXTURE=/absolute/tools-1612/authfixture \
LANTERN_PROTECTED_QUERY_DRIVER=/absolute/tools-1612/protectedquery \
go test ./tests/integration -run '^TestProtectedQueryPreparation_ProductionWire$' -count=1 -v
```

Ordinary root tests cover the driver, report-pair contract and topology. The
explicit wire test requires its three binaries and otherwise reports a skip;
a skipped test is not real-wire evidence. Fixture tests run separately with
`go -C server test ./cmd/authfixture`.

After independent review and the owner's exclusive heavy-slot authorization,
prepare each of Search/BFS/PPR/Community in all three phases. `measure` requires
clean matching Server/driver VCS revisions, a pinned Server binary SHA-256 and
sufficient JWT lifetime. It never seeds or preflights implicitly:

| Phase | Required state | Independent writer |
| --- | --- | --- |
| first | Fresh fixture, seed only, no earlier Search/traversal; exactly one query | none |
| warm | Fresh matched fixture; same predeclared warmup/preflight in each mode | none |
| update-mixed | Fresh matched warmed fixture; same fixed schedule and mutation sequence | explicit positive `-writer-rps` |

Validate a sacrificial sibling fixture with the same source/corpus before a first
sample, then seed a new fixture for each family. Calling preflight on the measured
first fixture warms it and invalidates a first-query claim.

Here `first` means the first query on the loaded corpus and reader scope. OIDC
Role assignment during seed has already authenticated the setup administrator
and warmed issuer metadata/JWKS; this is not cold provider discovery or the first
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
retained separately from the reader. Pair comparison rejects source/binary,
corpus, topology, transport, actor, phase/load and offered-slot mismatches, and
always leaves final acceptance `not_qualified`.

Latency is client dispatch through RPC and result validation: it includes
encoding, verified transport, admission, execution, decoding and validation,
but excludes setup and scheduled waiting. OFF/ON authorized outputs differ in
size and graph work; the difference includes JWT validation, Role admission,
authorized-view execution and client result work. It cannot isolate pure JWT CPU
cost. Writer RPC latency is not internal lock waiting time.

Server allocations, retained/peak memory, internal lock wait, export revocation,
TTL/Delete/restore and host qualification are not collected by this driver and
must remain `not_measured`. The existing operator-local
`capture/diagnostics_bridge.py`/runtime metrics can support a separately approved
final sampler; do not expose diagnostics to the data reader or force GC during
steady measurement. Final matching admission integration, publication/disclosure
revalidation, policy-token binding and the final pinned-source matrix remain
separate unfinished work. A successful preparation lane does not close #1612.
