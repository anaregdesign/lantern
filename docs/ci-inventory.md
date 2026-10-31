# Beta validation workflow — #1822

Ordinary development uses fresh affected local checks from the small explicit
`.github/scripts/validation_scope.py` map, then hosted exact synthetic-merge functional-validation,
all-six-module coverage and classified configured-source-integration. Unknown/policy/configuration inputs
select full. The complete local plan remains available and the previous full contract is
required once on a policy adoption candidate. No persistent evidence cache or new permissions
are introduced; NOT_SELECTED never means PASS.

Go retains all 18 vet/build/race-shuffle-coverage outcomes and every floor, using
two module workers with separate logs/profiles. Required contexts stay Build &
Test, Lint, Proto (buf), govulncheck. Selected native Windows Go and Rust commands
run inside the existing Windows job and block Build & Test; missing, skipped or
failed selected tests fail. Broad Windows Rust source compatibility moves to
manual/release; ordinary Rust retains Linux MSRV and stable Linux/macOS. Existing
Dart docs/backend-only mobile routing is reused, not replaced.

Beta coverage emphasizes the server/SDK integration and current development/app
surfaces. Every ordinary build does not promise all OS/device/distribution or
long performance/stability qualification. Exact release/environment configured-source-integration, archive
identity and explicitly promised native/physical evidence remain at those actual
milestones. Runtime authorization, revocation, quorum/custody and durability
checks remain. Normal scoped main runs remain; no extra manual full-main campaign.

The routing change also includes the exact #1821 backup tombstone fixture repair
from `89e963a53895d4bd14110ad3e03d964864d8f889`: its baseline file is identical
on main, and the prior Windows failure demonstrated the setup-speed assumption.
No failing result is rewritten. Rust's scoped-wire fixture now routes its source
workflow; full release lanes receive their authfixture and separate scoped gate.

Measured timings for the new policy belong to #1822 after qualification. The
historical inventory below records #1753's earlier change, not new savings.

## Claims, inputs and invalidation — #1974

Use this bounded map with the existing [local gate](../CONTRIBUTING.md#before-every-git-push--local-quality-gate)
and [release procedure](../CONTRIBUTING.md#cutting-a-release-vxyz).
An unrelated harness commit neither invalidates every product observation nor
proves that an observation is reusable. Preserve product source/build inputs and
immutable image/artifact identity separately from the executing harness and
interpreting reader versions.

| Stage and consumer | Claim and relevant inputs | Fresh execution or bounded reuse |
| --- | --- | --- |
| Local iteration; author before push | Frozen-checkout affected tests. Changed source, consumer closure, test fixtures, tools/dependencies and the reviewed path map. | Execute selected checks. Known tools use their explicit closure; mixed edits union obligations. Unknown, policy, shared-fixture, dependency and build inputs retain full fallback. Omitted checks are NOT_SELECTED. The live runner's existing carry contract is separate. |
| PR and main; merge gate | Exact synthetic merge and actual integrated source respectively: module coverage, security, durability, native checks, controlled functional-validation and classified configured-source-integration. Source, toolchains, dependencies, protocol, fixtures and configuration. | Existing required checks and same-run receipt verification remain. Main proves its distinct commit; no additional manual full-main campaign. A local result or a different run's receipt cannot become this run's PASS. |
| Explicit broad qualification; release owner | Normal-image performance, natural steady memory and goroutines, plus separately identified same-source retained-memory pairs. Source/build inputs, immutable images, scenario/load, GC/timing protocol, topology, CPU/memory budgets, observers/relay, time profile, platform and host conditions. | Keep normal12/retained4 and their thresholds. Material changes or missing/invalid observations require fresh affected cases. A corrected reader may assess sufficient unchanged saved observations offline, with a separately hashed result and original verdicts retained; image equality alone is insufficient. See the [benchmark contract](../testbed/bench/README.md#local-pre-tag-qualification). |
| Release; artifact consumers | Exact tag and actual distributables: normal configured-source receipt, short Search qualification, image/signature/provenance, archive/checksum closure and ten-target binary verification. | Preserve tag/artifact-specific checks. Source qualification can establish only its declared source claim; it does not prove downloaded bytes or another architecture. No release, tag or asset change is part of #1974. |
| Selected environment; explicitly named acceptance owner | Actual public artifact, host/platform, provider, native storage/backup/restart/fresh restore and OFF/OIDC/MCP lifecycle as selected. | Qualify that environment when required. Hosted Linux x64 does not prove an ARM64 host. A separately selected host's dependency failure blocks its own claim and is a publication prerequisite only when explicitly required. Do not bypass organizational controls. |
| Optional broad diagnostics; investigator | Compatibility/statistical/stability observations under the chosen declared protocol. | `bench-nightly.yml` remains manual-only and diagnostic. No new periodic schedule or replacement for required qualification is introduced. |
| Dart parent/child gates; package consumers | Existing probes mix performance with logical, retention, permit, watchdog, ownership and cleanup assertions. | Keep the probes and qualifying envelopes intact until #1902 extracts and preserves each assertion through its separate reviewed design. A benchmark name is not permission to skip correctness. |

Record outcomes by claim: product regression, harness/reader rejection,
environmental availability, invalid or incomplete observation, and artifact
integrity. These categories describe cause/scope without turning an executed
FAIL into PASS. A harness contract PASS proves that consumer's bounded fixture;
it does not qualify product performance. Dependency unavailability does not
silently revoke completed unrelated evidence. Missing premises remain
NOT_PROVEN or incomplete, even when another component passed.

[#1967](https://github.com/anaregdesign/lantern/issues/1967) is a completed saved-evidence
example: eight canonical workloads retained their fixed product image and were
assessed for effective controlled-time behavior without replay. Seven historical
same-object bind identities remain NOT_PROVEN and the original strict aggregate
FAIL remains unchanged. Only the previously unrun normal4 and retained4 were
executed. This bounded adoption is not a general historical identity certificate
or permission to reassess under a changed workload, resource budget or protocol.

The [actual saved-observation CLI demonstration](evidence/validation-time-reassessment.json)
re-read two existing cases without load: `broad_mutate` passed this time-profile
component in 0.271679s; `broad_rw` remained incomplete for missing bind identity
in 0.151519s. Both results remain nonqualifying, the original aggregate stays
FAIL with the same SHA-256, and all 125 saved raw files were unchanged. The
complete read/hash/assessment procedure took 0.985645s; these timings are not
benchmark performance or whole-case acceptance. Private raw paths are omitted
from the public summary; exact inputs and byte/metadata inventories are retained
with the implementation evidence.

The finite migration is: (1) #1975 adds early actual-consumer contracts and the
small known-tool routing closure; (2) #1976 adds a strict, nonqualifying offline
component assessment records raw hashes, reader identity, unchanged premises,
original verdicts and missing inputs without starting load or admitting a release;
(3) compare representative ordinary runs and close the remaining #1974 exits
only after reviewed PR/main adoption. Keep #1962's classifier invocation/error
diagnosis separate, preserving conservative error fallback, and #1902's Dart
assertion separation separate. Neither stage changes the published v0.36.1
release or the existing owner's acceptance plan.

## Measured cost baseline — #1974

The following existing GitHub records were read on 2026-10-31; no benchmark was
started to measure this baseline. Wall time is workflow `updated_at - created_at`;
runner elapsed is the sum of completed job `completed_at - started_at`, including
setup and cleanup. Job wait is the sum of each job's `started_at - created_at`;
it is not added to wall time and does not include dependency waiting before job
creation. All jobs in these samples succeeded. These are neither billed minutes
nor measured savings, and wall time is not the whole development cycle or a
separately reconstructed dependency critical path.

| Existing run | Wall | Runner elapsed | Summed job wait | Dominant measured stages |
| --- | ---: | ---: | ---: | --- |
| [#1961 PR Go](https://github.com/anaregdesign/lantern/actions/runs/40994109733), `7270a965` | 18m04s | 45m11s | 32s | Module job 17m00s: combined vet/build/test 14m24s, heap check 59s, native session fixture 70s. Functional-validation 5m28s; configured-source-integration 3m18s. |
| [#1973 actual-main Go](https://github.com/anaregdesign/lantern/actions/runs/41175176208), `da346671` | 15m53s | 43m15s | 24s | Module job 14m38s: combined vet/build/test 12m14s, heap check 50s, native session fixture 65s. Functional-validation 5m54s; configured-source-integration 2m51s. |
| [v0.36.1 Release](https://github.com/anaregdesign/lantern/actions/runs/41178394371), `da346671` | 27m30s | 42m23s | 23s | Server/nonserver verification 9m09s/7m31s; configured-source job 2m49s plus 8s receipt verification; Search 4m04s; image build job 13m48s; binary job 4m37s. |

For the actual-main configured-source job, frozen dependencies took 41s and
native execution 61s; the whole job took 171s. Its functional job spent 53s on
dependencies and 220s on execution within 354s. Release Search spent 57s building
the image and 138s running the short scenario; the image publication step spent
758s building/pushing within its 828s job. The API's combined vet/build/test
step cannot independently attribute those three costs. Unlisted overhead includes
tool setup, evidence upload/interpretation and cleanup; do not infer a finer split
from these records.

In the two sampled Go scope logs, native/local selection fell back to full for
`policy, configuration or dependency input` (2/2). The PR configured-source
classifier reported `classification unavailable: IndexError` (1/2, tracked by
#1962); main reported `boundary or unknown change`. These small samples do not
estimate repository-wide fallback frequency or show that the required work was
waste. #1840 records a real Snapshot defect found by long load and then covered
by shorter semantic/race/real-wire regressions. #1969–#1972 record late harness
defects; #1946 records external availability coupling. Keep those yields distinct
instead of assigning every repeated minute to product regressions.

For the next ordinary PR/main runs, collect the same timestamps and selection
reasons, then compare matched change scope, selected claims, runner and cache
conditions. Report setup/dependencies/build/execution/interpretation/cleanup only
where logs separate them; keep queue, workflow wall span and aggregate runner
elapsed distinct. Record repeated unchanged work with its invalidation reason
and the defect category. Establish budgets from those observations; no numerical
saving or billing reduction is promised here, and no extra benchmark is needed.

During implementation, the six selected actual Go/Bash harness contracts took
60.461s of test execution on macOS ARM64 with the installed pinned Go toolchain
and two Go processors. The raw JSON log SHA-256 was
`c4bad0caded6647d262dadaf8b05f4fe43cff28e175b03a75ecd7a8efe545e6c`.
This is harness cost, not a benchmark or matched CI saving. Early source checks
run it for known harness/generation/full scope; unrelated mapped product edits
avoid duplicating unchanged early contracts. The ordinary module tests remain.
Measure hosted setup/compilation separately before setting a CI time budget.

## Historical CI inventory and representative coverage

Audit: 2026-10-13, main `f8db9d460e9ddac5cc9afcdf0870e485a4d2d0fa`,
Issue #1753. The table covers all 18 workflow files. Runner time is the sum
of successful job `completed_at - started_at` durations, excluding skipped
jobs; it is not elapsed PR latency or a billed-cost calculation.

## Routine and diagnostic workflows

| Workflow | Trigger and jobs | Decision |
| --- | --- | --- |
| `go.yml` | Every main push/PR: scope → module/coverage, functional-validation and classified configured-source-integration → `Build & Test`; independent Lint, Proto, govulncheck, Windows receipt backup, fuzz and native deployment/private-file checks. | Keep. Distinct functional, coverage, native security and configured-source contracts. |
| `rust-sdk.yml` | Path-filtered main/PR. Six OS × MSRV/stable lanes, native wire tests on stable, archive inspection on Linux stable. | Reduce to four: Linux MSRV plus all three stable platforms. Retain every step; design prose uses Go documentation checks. |
| `dart-sdk.yml` | Path-filtered main/PR, manual and SDK tags. Classification selects source, minimum Dart, offline, SQLite, Android/iOS or documentation/evidence jobs; `Gate` aggregates them. Hosted archive readiness follows separately. | Keep existing scope routing and aggregate. Native jobs already skip backend-only changes. |
| `node-sdk.yml` | Path-filtered main/PR, manual and Node tags. Node 20/22 real-wire tests; tag publication depends on tests and configured-source qualification. | Keep both supported runtimes; cancel superseded PR runs. |
| `admin.yml` | Path-filtered main/PR. Container route/security contracts, SDK build, SPA lint/type/test/build and browser tests. | Keep; cancel superseded PR runs. |
| `codeql.yml` | Path-filtered main/PR and weekly. Go autobuild and Actions security analysis. | Keep both languages and weekly scan: security analysis is distinct from language lint/vulnerability scans. |
| `commitlint.yml` | PR opened/edited/reopened/synchronize; one title check. | Keep: it validates the squash title, including title-only edits. |
| `dart-transport-probe.yml` | Previously backend/proto/probe PR paths and manual. One generator/analyze/test job plus Connect/gRPC iOS comparison. | Manual-only. Accepted Connect production coverage belongs to `dart-sdk.yml`; the unshipped gRPC fallback remains available for explicit evaluation. |
| `bench-nightly.yml` | Despite its historical name, manual-only; one optional hosted benchmark diagnostic. | Keep. It costs nothing on ordinary PR/main runs and does not replace pre-release measured evidence. |
| `node-registry-audit.yml` | Manual-only, immutable tag and publish digest inputs; checks actual registry bytes and provenance. | Keep publication integrity guard. |

## Release and reusable workflows

| Workflow | Dependency and gate | Decision |
| --- | --- | --- |
| `current-sdk4.yml` | Reusable functional-validation/configured-source-integration native execution and same-run receipt. | Keep distinct proof kinds; no relaxation of configured-source-integration classification or failure behavior. |
| `current-source-release.yml` | Reusable configured-source-integration → exact-source receipt verification. | Keep; shared by Node, Dart, Admin and `_verify.yml`. |
| `_verify.yml` | Reusable workspace vet/build/test plus configured-source release gate. | Keep; shared by root, Go SDK and MCP publication. |
| `rust-release.yml` | Exact tag → conformance + configured-source-integration → audit/isolated archive → protected OIDC publication → registry equality → Release. | Same four representative Rust lanes; preserve all remaining dependencies/permissions. |
| `sdks-go-publish.yml` | Go SDK tag → `_verify` → isolated published-pb dependency check → Release. | Keep; workspace success does not prove the published dependency. |
| `docker-publish.yml` | Root tag → `_verify` → Search qualification → multi-arch image/binaries → Release. | Keep; no ordinary PR cost. |
| `mcp-publish.yml` | MCP tag → `_verify` → multi-arch image/signing → Release. | Keep; no ordinary PR cost. |
| `admin-publish.yml` | Admin tag → SPA validation + configured-source-integration → multi-arch image/signing → Release. | Keep; no ordinary PR cost. |

Dart parent/offline publication remains inside `dart-sdk.yml`: source Gate,
configured-source-integration, exact-tag archive preflight, restricted OIDC publisher,
read-only public archive equality, then a separate Release writer. Offline
hosted readiness and physical evidence remain independent prerequisites.
No workflow is deleted merely to reduce the file count: release-only and
reusable files have live consumers, and optional diagnostics impose no
routine execution cost.

## Documentation-only routing

The first #1754 candidate changed five workflow YAML files, two Python tests
and six Markdown files; its seven runs were all automatic `pull_request`
events, with no manual dispatch. It was not a documentation-only diff.
`sdks/rust/RELEASING.md` alone was already excluded by the Rust path filter.

The audit found a separate unnecessary expansion: ADR 0001/0002/0011 and
`docs/rust-sdk-v0.1-contract.md` were exceptions that treated design prose as
code. They now use the Go documentation checks and, for Dart ADRs, the cheap
Dart documentation job/Gate. Changed local links must resolve to committed
files; documented Dart/Flutter/Rust toolchains are checked against executable
package/workflow inputs, and existing release-contract tests remain blocking.
The Rust/CodeQL positive path exceptions are removed. Product builds do not
compile/embed these four design documents; the Rust API sketch is explicitly
illustrative rather than compilable Rust.

This is an allowlist of documentation locations, not a blanket Markdown
extension skip. Generated `docs/env.md`, source/fixture Markdown, unknown paths,
workflow/classifier edits and mixed source changes keep their owning checks.
Dart no longer suppresses native coverage merely because an unknown fixture
ends in `.md`, `.mdx`, `.rst` or `.txt`. Tags/manual full qualification is
unchanged. Failure to classify or validate documentation still fails the
required aggregate rather than leaving it pending or reporting a false pass.

## Support and required checks

Rust keeps its declared 1.88 floor, represented on Linux. Linux, macOS and
Windows keep stable codegen, TLS-root tests, lint/unit/docs and real-wire
coverage; this does not claim exhaustive old-toolchain/native-platform
combinations. Release tags repeat the same matrix for their exact source.

The readable main ruleset requires `Build & Test`, `Lint`, `Proto (buf)` and
`govulncheck`, a PR, and zero approving reviews; force-push/deletion protections
also apply. Preserve all four names and the always-starting Go workflow so
required checks cannot be stranded by workflow-level path filters. No rules
or access settings change. All triggered checks are checked before merging.

Main and PR validate different commits (post-integration source versus the
synthetic merge), so main qualification remains. Rust/Node/Admin concurrency
groups share only a PR number; non-PR runs use their unique run ID, including
manual runs and release tags. Thus new PR pushes retire obsolete work without
cancelling publication or exact-main qualification.

## Baseline and expected savings

| Measured run | Rust total | Removed macOS/Windows MSRV | Transport comparison |
| --- | ---: | ---: | ---: |
| [PR #1752 Rust](https://github.com/anaregdesign/lantern/actions/runs/38392309898), [probe](https://github.com/anaregdesign/lantern/actions/runs/38392309991) | 35.27 min | 6.08 min | 22.83 min |
| [main after #1752](https://github.com/anaregdesign/lantern/actions/runs/38393982563) | 34.77 min | 6.13 min | Not triggered |
| [PR #1749 Rust](https://github.com/anaregdesign/lantern/actions/runs/38338582416), [probe](https://github.com/anaregdesign/lantern/actions/runs/38338582476) | 35.40 min | 6.60 min | 19.07 min |
| [main after #1749](https://github.com/anaregdesign/lantern/actions/runs/38339889265) | 35.78 min | 5.28 min | Not triggered |

Expected avoided work is **35.05 runner-minutes** per #1752-like PR/main
cycle (30.95 minutes for #1749). These are counterfactual estimates derived
from real completed jobs, not measured new-run savings. Cancellation savings
depend on updates while old runs are active and are not included. New-run
durations and actual lane counts are recorded on #1753 after integration;
runner/cache variance and differing path scope prevent a causal whole-PR
latency comparison.

The unchanged 76-step local gate overlaps hosted language checks, generation,
functional-validation/configured-source-integration and static analysis. This change does not move the retired hosted work
locally or add local native matrices. Local gating can be evaluated separately
with a component-dependency policy; it must not be bypassed for this policy
change. Physical Android/iPhone, release archive/provenance and configured
environment acceptance are not replaceable by either local unit tests or CI.
