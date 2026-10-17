# Beta validation workflow — #1822

Ordinary development uses fresh affected local checks from the small explicit
`.github/scripts/validation_scope.py` map, then hosted exact synthetic-merge F,
all-six-module coverage and classified Q. Unknown/policy/configuration inputs
select full. The explicit old full77 remains available and is required once on
the policy adoption candidate. No persistent evidence cache or new permissions
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
long performance/stability qualification. Exact release/environment Q, archive
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

## Historical CI inventory and representative coverage

Audit: 2026-10-13, main `f8db9d460e9ddac5cc9afcdf0870e485a4d2d0fa`,
Issue #1753. The table covers all 18 workflow files. Runner time is the sum
of successful job `completed_at - started_at` durations, excluding skipped
jobs; it is not elapsed PR latency or a billed-cost calculation.

## Routine and diagnostic workflows

| Workflow | Trigger and jobs | Decision |
| --- | --- | --- |
| `go.yml` | Every main push/PR: scope → module/coverage, F and classified Q → `Build & Test`; independent Lint, Proto, govulncheck, Windows receipt backup, fuzz and native deployment/private-file checks. | Keep. Distinct functional, coverage, native security and configured-source contracts. |
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
| `current-sdk4.yml` | Reusable F/Q native execution and same-run receipt. | Keep distinct proof kinds; no relaxation of Q classification or failure behavior. |
| `current-source-release.yml` | Reusable Q → exact-source receipt verification. | Keep; shared by Node, Dart, Admin and `_verify.yml`. |
| `_verify.yml` | Reusable workspace vet/build/test plus configured-source release gate. | Keep; shared by root, Go SDK and MCP publication. |
| `rust-release.yml` | Exact tag → conformance + Q → audit/isolated archive → protected OIDC publication → registry equality → Release. | Same four representative Rust lanes; preserve all remaining dependencies/permissions. |
| `sdks-go-publish.yml` | Go SDK tag → `_verify` → isolated published-pb dependency check → Release. | Keep; workspace success does not prove the published dependency. |
| `docker-publish.yml` | Root tag → `_verify` → Search qualification → multi-arch image/binaries → Release. | Keep; no ordinary PR cost. |
| `mcp-publish.yml` | MCP tag → `_verify` → multi-arch image/signing → Release. | Keep; no ordinary PR cost. |
| `admin-publish.yml` | Admin tag → SPA validation + Q → multi-arch image/signing → Release. | Keep; no ordinary PR cost. |

Dart parent/offline publication remains inside `dart-sdk.yml`: source Gate,
configured-source Q, exact-tag archive preflight, restricted OIDC publisher,
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
F/Q and static analysis. This change does not move the retired hosted work
locally or add local native matrices. Local gating can be evaluated separately
with a component-dependency policy; it must not be bypassed for this policy
change. Physical Android/iPhone, release archive/provenance and configured
environment acceptance are not replaceable by either local unit tests or CI.
