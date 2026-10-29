# Contributing to Lantern

This file is the process and maintenance contract for the repo: triage, the local
quality gate, the issue-first policy, PR/merge rules, code-generation steps, and the
release/tag procedure. [AGENTS.md](AGENTS.md) covers the codebase architecture and
per-task conventions; [.github/copilot-instructions.md](.github/copilot-instructions.md)
is the always-on short subset.

Each maintenance item below is **trigger → action**. Run the matching step before
pushing or merging.

## Versioning & compatibility (pre-v1.0.0)

**Lantern is pre-v1.0.0 and makes _no_ backward-compatibility guarantees.** Until the
first `v1.0.0`, prefer the cleanest design over compatibility — break freely across the
whole surface and don't hedge for old clients:

- **Proto / buf schema** — renumber, retype, or drop fields and RPCs freely. You do
  **not** need to `reserved` retired field numbers/names purely for compatibility; add a
  `reserved` only when it prevents a real decode hazard you actually care about. If a
  `buf breaking` gate is ever added, treat it as waived until `v1.0.0`.
- **SDK APIs (Go / Dart / Node / Rust), CLI / REPL grammar, the `LANTERN_*` env-var contract, and
  metric names** — may change between releases. Update every call site in the same change.
- **Still forbidden (unrelated to compatibility):** `buf generate --clean`. It deletes
  `pb/go.mod` + `pb/doc.go` — a tooling footgun, not a compat concern — so the `--clean`
  prohibition stays in force.

At `v1.0.0` this is revisited and a real compatibility / deprecation policy is adopted.
Until then, pre-existing `reserved` markers (e.g. `IlluminateRequest`'s `reserved 4, 5;`)
may stay because they are harmless, but they are **not** required going forward.

## Issue triage — the `Lantern roadmap` project

Write every GitHub Issue title, body, comment and update in English.

Cross-track triage lives in a single GitHub Project named `Lantern roadmap`. Issues
remain the source of truth — the Project is only a view layer + lightweight kanban on
top of them.

Every Issue must have all four custom fields filled **in the same session it is
created**:

| Field | Allowed values |
| --- | --- |
| `Track` | `Admin` / `HA` / `Connect` / `SDK` / `Maintenance` / `Docs` |
| `Module` | `pb` / `core` / `server` / `sdks-go` / `sdks-dart` / `sdks-rust` / `sdks-node` / `sdks-python` / `admin` / `mcp` / `tests` / `docs` / `ci` |
| `Release target` | `next pb` / `next sdks-go` / `next sdks-dart` / `next sdks-rust` / `next root` / `next mcp` / `next admin-internal` / `unscheduled` |
| `Priority` | `P0` / `P1` / `P2` |

Rules:

- The Project is **not** the source of truth — do not infer scope, dependencies, or
  release order from the board. Those live in the Issue body and in `AGENTS.md`.
- Do not create additional Projects (per-module, per-release, …); one Project keeps the
  "what is everyone working on" cost near zero.
- `gh` needs the `project` scope (`gh auth refresh -s project`) to mutate the board.

## Record findings on the Issue they help, in the same session

The moment your current work produces a fact that future-you (or another agent) will
need when picking up an **unrelated** open Issue, post it as a comment on that Issue
before you forget. Wire-shape gotchas, flake reproducers, perf numbers, build-order
constraints, model-shift implications — anything that would make a future reader ask
"why didn't anyone mention this?".

Format so the reader gets the context without chasing the link:

> Finding from \<work that surfaced this — PR/Issue/exploration\>: \<one-paragraph
> fact\>. Source: \<code/file/log link\>.

Always post on the Issue itself — never only in a PR description or a chat reply, which
are invisible to whoever opens the Issue next. If three or more Issues benefit from the
same finding, also add a one-line entry to the relevant section of `AGENTS.md`.

## Before starting any non-trivial fix or feature

**Search for a GitHub Issue first; link one or file one before implementing.** This is
a hard rule for non-trivial changes, including bugs, improvements, and validation
repairs discovered while another PR is in flight. Record the expected outcome,
boundaries, dependencies, and verification before editing, not after a diff exists.

- Default to one bounded implementation Issue → one cohesive PR containing source,
  paired tests, documentation and generated consumers. Epics/specification parents
  and final acceptance owners remain separate. Related existing Issues may share a
  cohesive PR when each outcome remains independently reviewable; do not split CI
  or review repairs into another PR or accumulate unrelated changes to save a gate.
- Before editing, read the acceptance and relevant required CI, identify generation
  and dependency inputs, check toolchain/environment prerequisites and existing
  authorization. Record meaningful blockers early and continue independent work.
- Within that authorization, continue implementation, focused tests, review fixes,
  final qualification, CI repair and ordinary integration without repeated approval
  requests. Permission failures must identify the exact action/target; do not work
  around them by changing connections, credentials, scopes or protections.
- Use normal implementation effort for routine changes. Concentrate Ultra on new
  safety/proof boundaries, not repeated status narration or unchanged gate output.
- Reference every closing Issue in the PR (`Closes #N` per Issue) so the merge
  wires each discussion to the diff.
- Exceptions (no new Issue required): already-scoped direct documentation edits
  or proofreading, and direct fixes to the already-filed Issue requested in an
  in-flight PR review. A newly discovered bug or improvement, including a
  documentation improvement, needs an Issue even if it is a one-line fix or
  arises during review.
- When in doubt, file the Issue — the overhead is tiny next to a reworked PR.

## Track independent exits and budget expensive validation

For an epic or other multi-stage effort, keep a concise, issue-linked progress ledger
in the driving Issue. Separately count completed/total outcomes for **merged source
and CI**, **final exact-source acceptance**, **external publication**, and
**human/physical-device evidence**; link the next unblocker for each unfinished
bucket. Update at meaningful milestones, not every diagnostic sample. Do not
collapse these buckets into one percentage or treat preliminary branch-image
performance, partial CI, or simulator runs as final-source or physical acceptance.

Before costly whole-host or device validation, record the owner, dependencies,
acceptance matrix, pinned source/image revision, fresh-state requirements, and
test budget (including any predeclared stability repetitions) in the driving
Issue. Preflight at each required run or family boundary, including host
contention, image provenance, and fresh state. Reuse the one immutable image
and unchanged shared setup across the complete declared matrix on final exact
merged source, without skipping per-family checks or repeating preflight of
unchanged inputs. Preserve raw evidence securely and record its exact SHA-256
alongside a content-free public verdict. Repeat a costly run only after a
relevant build change, a documented invalid run, or a predeclared stability
check; never cherry-pick a passing sample, force GC in a measured steady
window, or weaken load or thresholds.

## Before every `git push` — local quality gate

Lantern is a pre-production beta. During edits run focused tests, resolve meaningful
review findings, then freeze one coherent candidate. Before pushing, use fresh
**affected local checks** against the fetched target main:

```sh
python3 -B .github/scripts/local_gate_session.py "$PWD" ../affected-evidence-new --affected-base origin/main
```

The small [path/dependency map](.github/scripts/validation_scope.py) selects module
owners and consumers. Mixed changes union obligations; unknown paths, unusable
bases, policy/configuration/dependency changes and unsupported input types select
the complete gate. Go source selects its module DAG and root integration; Node
feeds Admin, Dart parent feeds Offline/SQLite/example, and native file/process/TLS
inputs select their owning checks. Selected steps execute freshly; omitted steps
are reported as **NOT_SELECTED**, never carried or passed. The affected mode
accepts no prior receipts and does not require the carry session's toolchain
fingerprinting. Its results describe the frozen local checkout, not hosted or
release qualification. Use the existing explicit [pure-prose path](#local-prose-eligibility)
for eligible explanation-only edits.

The complete 77-step plan below remains available by omitting `--affected-base`.
Policy-changing PRs must run the **previous full contract on their final source**,
including functional-validation/configured-source-integration and applicable native coverage; a relaxation cannot qualify itself.
The explicit #1820 owner-approved time-contract migration below supersedes only
the retired repeated-external-source full configured-source-integration campaign; it retains complete functional-validation,
bounded configured-source-integration and every other applicable mandatory check.
This policy's adoption PR follows that rule once before push. Later changes use
the merged policy, with normal exact synthetic-merge/main checks still blocking.
The owner-approved #1946 migration separately replaces only the full local plan's
automatic external-source acquisition with complete controlled-source
functional-validation and isolates inherited qualification opt-ins. Its adoption
uses that revised local plan; the other 76 steps, selected PR/main
configured-source-integration, exact pre-tag and chosen native-environment
qualification remain mandatory. It does not require rerunning the retired local
external-source coupling as a bootstrap, and preserves its recorded failures.

Complete frozen Bun dependency installation in Node and Admin before Go walks
the root workspace. Installation can mutate dependency directories containing
Go files; those operations must not run concurrently (#1646). Independent tests
may run in parallel after installation completes.

When copying changed sources into a reused validation worktree, update their
modification times or use a fresh build target. Preserving older times can let
incremental tools reuse binaries from the previous source (#1648). Bind results
to the candidate tree, check generation/content drift, and verify that explicitly
selected wire tests actually ran; an empty selection is not acceptance.

Run from the repo root; this matches the required CI checks (Build & Test, Lint,
Proto (buf), govulncheck):

```bash
gofmt -l .                       # must print nothing (covers every Go module)
go test ./...                    # root module
(cd core    && go test ./...)
(cd mcp     && go test ./...)
(cd pb      && go test ./...)
(cd sdks/go && go test ./...)
(cd server  && go vet ./... && go test ./...)
(cd sdks/dart && dart format --output=none --set-exit-if-changed \
  lib/lantern_client.dart lib/src/*.dart test \
  && dart pub get --enforce-lockfile && dart analyze lib test && dart test \
  && dart doc --output "$(mktemp -d)" --validate-links \
  && dart pub publish --dry-run)
(cd sdks/dart/offline && dart format --output=none --set-exit-if-changed \
  lib test tool && python3 tool/paired_source_gate.py -- bash -c \
  'dart analyze && dart test && dart doc --output "$(mktemp -d)" --validate-links')
(cd sdks/dart/example && dart format --output=none --set-exit-if-changed \
  lib test integration_test \
  && flutter pub get --enforce-lockfile && flutter analyze && flutter test)
(cd sdks/dart/offline_sqlite && dart format --output=none --set-exit-if-changed \
  lib test tool && flutter pub get --enforce-lockfile \
  && flutter analyze && flutter test && dart run tool/crash_probe.dart \
  && dart run tool/performance_probe.dart)
(cd sdks/rust && cargo fmt --all -- --check \
  && cargo clippy --locked --all-targets --all-features -- -D warnings \
  && cargo test --locked --all-features \
  && RUSTDOCFLAGS="-D warnings" cargo doc --locked --no-deps --all-features)
```

For a coordinated unpublished online/offline candidate, run the offline source
commands through `python3 -B tool/paired_source_gate.py -- <command>` (or prepare
once and clean up in a `trap`). It checks the parent version against the future
hosted dependency floor and resolves the independently generated
`tool/paired_source.pubspec.lock` with enforcement. Temporary overrides and the
hosted lockfile are restored after the command. This is an explicit paired-source
check, not an isolated hosted-archive pass. Keep hosted archive resolution strict
in tag/publication preflight and record that separate exit as pending until the
parent has been published and the hosted lockfile regenerated. Do not publish a
parent early merely to unblock source development.

During edits, run the narrowest targeted checks for changed behavior. Resolve
review findings, freeze the coherent candidate, then qualify the selected local
closure before push, or the explicit prose path when eligible.
Every later push also needs passing results for its applicable gate, with fresh
executions and mechanically verified component carries
explicitly distinguished under the local carry contract below. Repair a CI
failure in the same PR, preserve the failed run, and repeat checks whose inputs
changed or whose evidence became invalid. CI qualifies the exact synthetic merge.

Review the immutable candidate diff and its dependency/acceptance boundaries.
After corrections, re-review the changed behavior and affected boundaries;
expand the review only when a new finding warrants it. Retain raw logs privately
and report a short machine-generated head/tree verdict, executed/carried counts,
manifest SHA-256 and material blockers instead of hand-maintained checklists of
successful command output. A policy-changing PR uses the previous full gate on
its final source; it must not apply its proposed relaxation to itself.

All selected per-module test runs are mandatory: the root `go test ./...` does **not** span
submodules. `make lint` runs the same linter as the `Lint` job. The `Proto (buf)` check
uses the Buf version pinned in `generate.go`, `Makefile` and `.github/workflows/go.yml`.
Run `go run github.com/bufbuild/buf/cmd/buf@v1.70.0 format -d --exit-code` and
`go run github.com/bufbuild/buf/cmd/buf@v1.70.0 lint`, then require zero uncommitted
codegen drift. Use that same pinned command with `format -w` for source formatting
and regenerate locally first (below). A different Buf version on `PATH` does not
reproduce the required formatter gate.

The Dart SDK is outside `go.work`; its format/analyze/test gate is therefore
separate too. When `proto/` changes, run `sdks/dart/scripts/codegen.sh` and commit
the regenerated `sdks/dart/lib/src/gen/**` files. The supported toolchain source of
truth is `docs/decisions/0001-dart-mobile-transport.md`; the workflow pins mirror that
decision and also runs the package at its declared minimum Dart floor. The workflow
routes backend/search-only changes through the current-Dart unit and real-wire gates.
The separately tested `full` scope adds minimum Dart and package-quality checks for
Dart SDK, proto, codegen/toolchain, workflow, release-tag and root Go dependency
changes. The `mobile` scope requires Android and iOS for Dart/Flutter source
and fixtures, proto/codegen and workflow inputs, release tags, and Go
language/toolchain directive changes; ordinary Go dependency
changes skip native jobs. Ordinary prose-only changes skip package and native jobs. The stable `Gate` job checks
both decisions independently. The experimental
`sdks/dart/offline/` child runs from its own working directory at minimum/current
Dart, including locked paired-source resolution, fresh-process canonical snapshot
tests and real-server committed-response-loss replay. These checks and the isolated parent archive precede
parent publication. The separate read-only `offline-hosted` job follows the source
`Gate`; on a parent tag push it also waits for `verify-published`. It compares the
hosted parent archive bytes before checking the committed offline hosted lock,
then resolves the offline archive with an isolated cache and no overrides. Missing
parent versions and stale locks report **pending** without claiming hosted archive
acceptance on PR/main/manual or parent-tag runs. They block an offline tag push.
Transport errors, archive mismatches, and invalid hosted-source/checksum evidence
fail the hosted job. The source Gate does not depend on hosted readiness.
Android and iOS jobs upload content-free JSON
manifests bound to the exact commit, workflow run, Flutter/Dart revisions,
application package, platform kind, scenario set, and pass result. Simulator
manifests do not substitute for the sanitized exact-revision physical-device
record required before an offline release. Published offline 0.4.0 uses hosted `lantern_client: ^0.3.3` and passed
its independent receipt release gates in #1399. The 0.5.0 contribution
Delete candidate uses hosted `lantern_client: ^0.4.1` and has a fresh
independent archive and physical gate in #1586; the parent `lantern_client` publish
archive continues to exclude `offline/` and `offline_sqlite/`. The maintained
Flutter app under `sdks/dart/example/` is a repository integration fixture
with local development overrides, so only its standalone online example is
included in the parent archive. CI uses Dart Pub's own archive builder,
unpacks the resulting tarball outside the checkout, resolves every included
`pubspec.yaml` with an isolated cache, then runs analysis, tests, and `pana`
against the unpacked artifact.

For exact-main source qualification after a change misses the Dart or Node SDK
path filters, dispatch `dart-sdk.yml` and `node-sdk.yml` on `main` and record
each run ID and `headSha`; rerunning an older run does not test the final SHA.
Manual Dart dispatch selects the full package and Android/iOS matrix, and Node
still tests both supported runtimes. SDK publication and GitHub Releases require
release-tag **push** events and their existing test/preflight gates; manual
dispatch, including on a tag, is test-only. Simulator results do not replace
physical-device evidence.

The opt-in `sdks/dart/offline_sqlite/` adapter has a mandatory Flutter host gate:
format/analyze/test, real SQLite close/reopen conformance and disk-full checks,
`dart run tool/crash_probe.dart` for process termination at transaction
boundaries, and `tests/integration/dart_offline_sqlite_test.dart` over the real
Connect endpoint. The maintained Android/iOS smoke uses platform SQLite and
checks pending restart, original TTL, replay, and logout wipe. Host FFI is
development-only; its native library is not an application runtime dependency.
This package stays `publish_to: none` until its independent release is qualified.

The iOS job classifies the native smoke instead of treating every outer timeout
as retryable infrastructure. On CI, it gives the full attempt 720 seconds. It
gives the cold test-app build 360 seconds, the native launch 180 seconds, and
the install 90 seconds. The longer build budget is necessary when the
separate production build is skipped and the integration target compiles first.
The attached helper builds before booting its selected Simulator, so first-boot
services do not compete with compilation. Boot and readiness share 180 seconds
within the same total attempt; boot failures/stalls fail directly. A fresh retry
device follows the same order. Build timeout captures a bounded, redacted process
snapshot before termination; later completion cannot turn the deadline failure
into success. Use the explicit classification and final Gate, not the smoke
step's `continue-on-error` conclusion, to determine the test result.
After `simctl` returns the Runner PID, it reads that process's
retrospective Simulator log for the VM Service URL, test-body marker, and
terminal result, then runs `flutter drive --use-existing-app` to verify the
actual assertions. The outer step remains 14 minutes, leaving time for bounded
diagnostics. Only `launch_stall` or `attach_stall` may retry, once, on a newly
created simulator with the same runtime/device type and a different UDID. An
exited Runner, build failure, assertion or RPC failure, and post-body stall fail
the blocking `Gate` directly. Bounded, redacted process/simulator/app
diagnostics are always uploaded; the temporary retry simulator is always
deleted. Unproven or uncompleted primary-command cleanup records `cleanup_failure`
and fails without retry. After the existing bounded child reaping, a prior group
signal denial is resolved only when an independent check of that exact owned
group returns `ESRCH`; child exit alone is insufficient. A still-existing group,
denied or unknown group existence, or an unreaped child remains a failure.
The primary operation and bounded, redacted cleanup reason are retained in
`flutter.log` before secondary diagnostics, even if that capture fails.
Native launch uses `--terminate-running-process` after
installation so the returned PID identifies the new run, without a separate
stop request for an application that may not be installed yet. Operation
records in `phases/install_operation` and
`phases/native_launch_operation` retain `started` and the returned/timeout outcome
independently of log rotation; a returned exit code is not native smoke success.
Install output uses the existing redaction, disk and shared console caps.
These records distinguish operation evidence, not the cause of historical stalls.
Diagnostic capture errors cannot erase the original failure classification. Each attempt
builds the integration-test app and installs it on its
own simulator before native launch. The simulator build compiles the native host
and platform SQLite plugin. Ordinary PR/main runs analyze the production Dart app
and compile `main.dart` through widget tests; the separate production/device iOS
build remains mandatory on SDK release tags.

The iOS helper uses Python 3's standard library to retain at most 256 KiB per
redacted log on disk throughout the attempt. The authenticated VM Service URL
stays in memory and is redacted from live output and artifacts. Phase flags are
stored separately so log rotation cannot change classification. Live Actions
output is redacted and capped too; lines over 8 KiB are omitted without
buffering their remaining contents. The artifact finalizer reserves space for
classification and phase metadata, then shares its 2 MiB / 32-file budget
across useful diagnostic tails from both attempts instead of dropping the
artifact when logs are noisy.

## Documentation-only CI routing

The Go workflow stays enabled on every PR/main push so required contexts never
remain pending because of a workflow path filter. Its shared
[prose classifier](.github/scripts/ci_docs.py) accepts root instructions,
Markdown under `docs/`, and Markdown directly in known module/documentation
directories. `Build & Test` validates text/whitespace and runs the inexpensive
CI/release-contract tests; Lint, Proto, vulnerability, Windows and Fuzz jobs
skip before a runner starts. A failed classifier still fails `Build & Test`.

Dart, Node, Rust, Admin and CodeQL use conservative ordered
path exclusions to avoid starting for ordinary README/CHANGELOG changes.
Dart instruction/process-doc changes use a cheap contract check and `Gate`.
Mixed source changes, fixtures, unknown paths, workflow/config changes,
and generated `docs/env.md` retain their owning checks. The four canonical
Dart/Rust design and SDK contract Markdown files use lightweight text, newly
introduced local-link, SDK toolchain-document and release-contract checks.
They are design prose, not compiled or embedded inputs. Rust and CodeQL do
not start for those documents alone; Dart uses its documentation job and Gate.
Markdown under source/fixture directories and unknown paths remains executable
input for routing purposes, including native Dart fixture checks. Adding design
prose to backend changes does not add a mobile matrix. Tags ignore path filters;
release qualification, manual full sweeps and weekly CodeQL analysis are unchanged. The exact eight-file
physical-evidence PR validation remains separate from prose classification.

GitHub-managed Code Quality has no path-filter setting. Its automatic analysis
is disabled for this repository; checked-in CodeQL security analysis and the
language-specific Lint/static-analysis gates remain enabled. The repository's
required contexts stay `Build & Test`, `Lint`, `Proto (buf)` and `govulncheck`;
job-level skips satisfy GitHub's required-check contract. This hosted routing
does not itself select local eligibility; the explicit local path below is narrower.

The historical Connect/gRPC transport comparison is manual-only. Production
Dart SDK, offline/SQLite and native Android/iOS conformance remain in
`dart-sdk.yml`; dispatch the comparison when evaluating a fallback or changing
the probe itself. Rust, Node and Admin cancel only superseded PR runs. Main
qualification and release runs remain independent. See the
[CI inventory and reduction rationale](docs/ci-inventory.md). These reductions
add no local test obligation; the existing full local gate is unchanged.

## Local prose eligibility

After fetching the target main and reviewing the complete diff, a clean committed
local feature-branch candidate may request fresh prose checks:

```sh
git fetch origin main
python3 -B .github/scripts/local_gate_session.py "$PWD" ../prose-evidence-new --prose-base origin/main
```

The selector reuses `ci_docs.py` and initially accepts **only modifications to
existing README/CHANGELOG files in its ordinary module directories, plus root
README**. It requires origin/main to match the requested base and be an ancestor
of HEAD, and checks the entire base/head diff without rename inference. Empty or
mixed changes, adds/deletes, unknown paths, missing/altered working inputs,
nonregular/non-UTF-8 text and classifier errors retain full qualification.
Changes to code examples or inline literals also retain full checks. A changed
file containing `<` at either revision retains full checks, including HTML block
bodies, ambiguous or unclosed markup and autolinks.
All `docs/` contracts, instructions, workflows, fixtures, generated outputs,
deployment/benchmark guidance, generators, locks, configuration and toolchain
inputs remain outside this eligibility. Even in README, review must confirm that
the change is explanation rather than a new runtime, platform, security, SDK or
release requirement; use the full gate when that meaning is uncertain.

Eligible candidates execute text/whitespace and new local link-target checks plus
the existing Python CI, Dart, offline and Rust document-contract suites. External
URL availability and fragment rendering are checked in content review. Required
suites must execute nonzero tests with no skips/expected failures. A failed check
fails qualification; it cannot be replaced by a later full-gate success. The
runner uses the existing owner-frozen checkout contract, checks committed bytes
and eligibility before/after, retains raw log
hashes, and writes a separate `local-prose-pre-push` base/head/tree verdict with
fresh executed counts and **zero carried** results. It never establishes a carry
baseline. It needs no language dependency installation or native host slot.

If eligibility is refused, the same invocation runs the ordinary full gate;
acquire the shared host slot and pinned tools before invoking an uncertain
candidate. It is one-shot and cannot combine with `--session`. CI, main/detached
or tagged checkouts cannot select it. Hosted synthetic/main checks and independent
release/archive, provider/device and performance qualifications are unchanged.

## Standalone Rust SDK gate

`sdks/rust/` is outside `go.work`. Its checked-in `src/generated/**` sources
come only from `cargo xtask codegen` run in that directory (pinned Tonic/Prost
codegen with bundled `protoc`). Commit `Cargo.lock` for repository CI; the
library archive builds without the Go workspace, root `proto/`, Buf, a system
`protoc`, or a generator build script. `.github/workflows/rust-sdk.yml` checks
codegen drift, formatting, warnings-denied Clippy, tests, example compilation,
and warning-free docs on three routine lanes: Rust 1.88 on Linux,
plus stable on Linux and macOS for affected ordinary source changes. Full Windows
Rust compatibility runs on explicit `workflow_dispatch` and exact release source;
MSRV remains represented on Linux. This schedules beta development coverage and
does not claim every ordinary build is qualified on every platform.

Affected native Windows Go and Rust tests run in Go's existing Windows job and
block `Build & Test`: owner/protected DACL, backup/WAL/receipt locking and sync,
listener/process ownership, native TLS roots/rejection and native value behavior.
Shared callers and generic native files count, not just `_windows` filenames.
Selected tests must actually execute; empty/skipped selections fail. Unknown
native impact selects coverage. Full native release conformance remains intact.
Each selected full stable lane builds the production Go server/authfixture and runs the opt-in
real-wire h2c/TLS/auth/CRUD/query suite, including failure paths. Locally,
build the server from the root and run that suite from the crate:

```sh
mkdir -p sdks/rust/target
go test -c -tags=lantern_test_time -o sdks/rust/target/lantern-smoke ./server/cmd
(cd sdks/rust && LANTERN_RUST_TEST_SERVER="$PWD/target/lantern-smoke" \
  cargo test --locked --all-features -- --ignored --test-threads=1 \
    --skip scoped_changes::tests::real_public_scoped_wire \
    --skip security::tests::real_public_current_wire)
```

The two public fixture tests require their root Go orchestrators. Scoped CDC
runs through `TestAuth_OIDCRustScopedChangesFacadeRealConnect`; current authority
runs through the native public SDK4 gate in
`tests/integration/current_security_gate_test.go`. The latter provisions native
time and a current quorum, passes the private fixture inputs, and requires the
exact Rust test to execute successfully. Standalone-suite success does not
qualify that native current-authority case.

Historical reports used **F** for `functional-validation` and **Q** for
`configured-source-integration`. Existing logs, receipts and comments retain those
historical labels; current CLI and receipts accept only the descriptive names, without
shorthand aliases.

The [current-authority runner](.github/scripts/current_sdk4_gate.py) records separate
receipt schema 3 with `functional-full-v4` and `source-cohort-v4` case contracts.
Old v3 case contracts or three-row source receipts cannot qualify this lifecycle.
**`functional-validation`** owns complete
mechanism/application behavior with controlled synthetic UTC: every retained functional
root, all four public SDKs, the scoped Rust case and active export revocation.
**`configured-source-integration`** owns integration with the configured time source
using the normal build and one independently provisioned three-member cohort reused for
SDK4, basic success/refusal, ordinary Apply/foreign retained-original status, native
15-second old-cut observation and active export revocation. Export remains last and
retains the positive multi-record control, APPLIED withdrawal, immutable accepted
prefix, next-body final-Write refusal and handler/stream shutdown. Every required body
must run once and pass, with no skipped, missing, duplicate or failed body. Neither kind
qualifies real identity providers or physical devices.

Unit time math, retry/fault/expiry cases use injected observations/counters and
deterministic time advancement, without external NTP/DNS or real holdover waits.
`functional-validation` selects `lantern_test_time` only at compile time. The private
serving harness is a `go test -c` executable running the real App/listener/certification
paths; normal and tagged non-test server binaries still cannot mint fixture time. The
controlled source uses local UDP; loss/recovery advances injected native time instead of
sleeping through an arbitrary age cutoff. This validation never qualifies real UTC, the
configured factory or external source/path reachability. Explicit standalone
external-source diagnostic tests additionally require the `lantern_source_qualification`
build tag; ordinary unit invocations cannot enable them just by inheriting an
environment flag.

`configured-source-integration` prebuilds SDK dependencies, then uses the fixed time
source and normal constructor. Each owner must acquire its own fresh anchor. Complete
process-owned counters record attempts, actual UDP writes and installations separately
after shutdown. The same three fixed identities use six sequential native owners:
explicit initialization of members 1, 2 and 3, then ordinary resume of members 1,
2 and 3. Source rows bind phase (`init`/`resume`) and member; all six rows together
permit at most 12 attempts and 12 writes. The controlled functional browser case
additionally resumes member 2 without changing the normal-source budget.
Missing, duplicate, zero, inconsistent or in-flight count evidence fails
configured-source-integration. Lossy trace records cannot substitute for these counters.
No soak or retry-until-green is permitted. Startup remains 45 seconds with bounded
3-second flights. Runtime refresh aims for 5-second interval width with 5 minutes of
lead; this is an operational target, never a login/consumption cutoff. Native 15-second
quorum, whole-interval credential/certificate/membership expiry, revocation and final
output refusal remain independent. Source failure or incomplete bodies fail
configured-source-integration; the runner changes no host settings and installs no
tools.

Receipt schema 2 does not qualify schema 3. The owner explicitly requested this scoped
migration to remove repeated external queries from functional/unit loops; only the old
repeated full configured-source obligation is superseded. All functional bodies, other
mandatory gates and same-run exact-source verification remain required. Old failures,
interruptions and NOT_RUN results retain their original meaning.

PR/main functional-validation and module/coverage jobs run independently of
configured-source-integration. The stable required `Build & Test` aggregates their
results and verifies distinct functional-validation/configured-source-integration
artifacts against the exact merge/main source and successful same-run producer digests.
configured-source-integration is mandatory for time, native, production composition,
dependency/toolchain, deployment, fixture bridge and proof/CI boundary changes. The
conservative [classifier](.github/scripts/current_qualification_scope.py) exempts only
regular modified application/computational sources and exact profiling handlers in its
small allowlist; unknown paths, base failures, additions/deletions/renames and mixed
changes require configured-source-integration. Existing docs/evidence-only
classifications are unchanged. A non-boundary candidate may show **functional-validation
PASS / configured-source-integration NOT_RUN**, which is code acceptance, not release or
environment qualification. Any executed configured-source-integration failure remains
FAIL and cannot be hidden as NOT_RUN; required configured-source-integration failure
blocks integration.

Ordinary affected local checks leave authoritative functional validation and required
configured-source integration to hosted exact-merge CI; they do not repeat
external-source acquisition for an unrelated diagnostic. Runtime guards and executed
configured-source-integration failures are unchanged. Under the owner-approved
#1946 local-only migration, the 77-step full local plan runs complete
functional-validation in `current-functional-wire` with explicit
`--proof-kind functional-validation --fresh-child`. This includes the scoped Rust
case and both native loopback mechanism tests, preserving the other 76 steps.
Each invocation writes fresh controlled-source evidence without receipt import
or carry. The local runner removes inherited current/time/native fixture,
provisioning-export and serving/output-child activation controls; dedicated
functional/native-provider runners supply their own explicit flags and build
tags. Tool/dependency settings and ordinary runtime configuration remain intact.
This local result qualifies code and controlled mechanisms. Required normal-build
configured-source-integration stays an independent, blocking selected PR/main,
release-candidate and selected-environment exit, with its six owners, counters,
source/path binding and all required bodies unchanged. An external-source timeout
remains FAIL; this separation adds no retry or failure-to-PASS rule.
Read timeouts identify a failed acquisition stage; the client, host, path or
provider cause remains unknown until supported by separate evidence.
The #1820 scoped migration remains in force for the retired repeated external-source
functional campaign; historical receipts retain their original scope. For separate focused
qualification of a clean frozen candidate with pinned tools and dependencies:

```sh
python3 -B .github/scripts/current_sdk4_gate.py run --proof-kind functional-validation \
  --evidence ../current-functional-validation-new
python3 -B .github/scripts/current_sdk4_gate.py run --proof-kind configured-source-integration \
  --evidence ../current-configured-source-integration-new
```

Exact release candidates require configured-source-integration before publication. Rust
retains its native lane; root/Go/MCP use `_verify.yml`, and Node, Dart parent/offline
and Admin publication depend on `current-source-release.yml`. These preflights qualify
the exact source in the selected Linux container; existing archive/image fingerprint and
publication equality gates remain necessary. A source receipt alone never qualifies a
substituted artifact. Tags exposed by a module proxy cannot be withheld by CI; complete
release-candidate configured-source-integration before creating tags.

Selected deployment/environment acceptance also requires configured-source-integration
on the exact source and actual chosen supported native environment. Requalify after
material host, kernel/VM, namespace/capability, image, source/configuration or DNS/UDP
path changes. Preserve source/artifact equality and environment evidence in the
acceptance ledger. A CI Linux result does not qualify another platform/host. Every
production startup still obtains a fresh anchor; a configured-source-integration receipt
is neither a startup permit nor a time capability. No release/deployment is performed by
these checks. Release-candidate configured-source-integration should start when
selecting the candidate.

Receipts bind proof/source/build kind, synthetic UTC, HEAD/tree/tracked inputs, runner,
actual toolchains, native environment, configuration, commands, required executions and
raw hashes. The verifier refuses functional-validation/configured-source-integration
interchange. Qualification is conditional on the source/path and native counter
premises, never physical clock certification. Run a declared finite attempt; preserve
any failure and record a relevant repair or invalid-evidence reason before another
attempt. This split reduces real-source coupling on ordinary hosted CI: an unclassified
integration defect can be detected later, at release qualification. The old post-main
`228df0aa` failure remains a failure; this contract does not turn its unexecuted
export/module/coverage bodies into passes.

Audit locked runtime and codegen dependencies with
`(cd sdks/rust && cargo audit --deny warnings --file Cargo.lock)`; an
unmaintained crate warning blocks release as surely as a vulnerability.
The independent [Rust release procedure](sdks/rust/RELEASING.md) freezes a
clean candidate, inspects its license and archive, tests the package in
isolation, runs a publish dry-run, and compares a fresh repackage. It does
not authorize publishing during SDK development.

## Local validation carry

The optional full-plan [live runner](.github/scripts/local_gate_session.py) owns a reviewed
[77-step command plan](.github/scripts/local_gate_plan.py). It installs dependencies
serially before Go traverses the workspace and executes the complete plan on its
first candidate. It accepts no manifest, receipt, digest or success JSON as input.
The external historical full-gate runner is not modified by this mechanism.

```sh
# Configure the repository-pinned tools on PATH; acquire the shared host slot first.
# Use a new evidence directory outside the checkout. One invocation runs the full gate.
python3 -B .github/scripts/local_gate_session.py "$PWD" ../gate-evidence-new --session
# Keep this process alive while committing reviewed fixes in another terminal.
# Enter qualify to validate the new clean head; enter quit to end the session.
```

The initial eligible set is deliberately small: `go-test-core`, `go-test-mcp`,
`go-test-pb`, `go-test-sdks-go`, `go-test-server` and `server-vet`. Each fingerprints
all Go workspace source/manifests, proto/generated inputs, shared fixtures under
`tests/`, `testdata/` and `testbed/`, relevant root inputs, gate commands and
configuration. Cross-module Go changes therefore invalidate these receipts
conservatively. At most **6 of 77 steps** can carry; the other **71 steps execute
again**. A useful case is a reviewed follow-up changing only `admin/app/` or
`sdks/node/src/`, with locks, configuration, Go/shared inputs and the execution
context unchanged while the original runner process remains alive. This saves
repeated Go submodule tests and server vet; dependency preparation, root integration
and Rust/Dart/Flutter gates still run. Time savings depend on those six actual
runtimes and have not been measured by the synthetic regression tests. Session
receipts are discarded on exit; there is no persistent validation cache.
The ordinary runner also isolates inherited qualification activation controls
before constructing its execution environment; explicit source qualification
uses the independent entrypoint above. This filter is limited to fixture/time/
native/child controls declared in `local_gate_session.py` and preserves tool,
dependency and ordinary application settings.
Eligible commands execute in a runner-owned read-only Git export containing
exactly their fingerprinted tracked input closure and its directories. The export
is populated from the pinned Git objects, verified, and made read-only; developer
checkout bytes, hardlinks, symlinks, ignored fixtures and untracked files never
enter it. A developer source edit followed by restoration cannot change these
execution inputs. A required fixture outside this namespace fails qualification;
nondefault GOFLAGS/overlays and external local module/workspace replacements have
no support. The remaining 71 fresh steps keep the existing owner-frozen checkout
contract. Toolchain binary/source/tool files, actual Go version/settings,
downloaded dependency integrity and effective environment are checked as well.
The Go-generated decimal work-directory suffix in `GOGCCFLAGS` prefix maps is
normalized; map kind/base/destination, other compiler flags and user CGO settings
remain fingerprinted. Malformed or ambiguous flags fail qualification.

All other components execute on every full component qualification; affected mode instead makes a fresh selection without carry. In particular, root Go
integration, SDK real-wire, generation/drift, package/archive, Rust, Dart/Flutter,
SQLite, Admin and documentation gates have no carry support. Their full-tree
input closure includes backend, proto, shared fixtures and generated consumers;
backend-only edits cannot retain SDK real-wire success. Do not reinterpret a
missing/unsupported result as a skip or a pass.

**Evidence trust boundary.** The live reviewed Python process creates a receipt
only after its fixed subprocess command returns zero and its output checks pass.
Empty-output checks remain mandatory where declared; Dart and offline dartdoc
steps also reject warning lines even when the command returns zero. Each native
provider wire execution allocates a fresh child evidence directory, preserving
earlier results without importing them. The runner retains the input fingerprint,
source head/tree and raw-log SHA-256 in
memory; it verifies every raw log establishing the complete current baseline,
including unsupported/fresh steps, and its output manifest against retained values
before reuse. Altering an output JSON or log cannot create a receipt.
Missing/altered evidence, unknown paths, classification errors, policy/dependency/
configuration changes or toolchain/environment changes force full execution.
Ignored input detection covers Go, proto, shared fixture, root and policy inputs.
Only the plan's named dependency/build output directories are exempt from that
presence guard: they are absent from the read-only export and their owning fresh
gates still execute. A required ignored fixture cannot produce a successful
eligible receipt. Unknown ignored inputs in the closure conservatively disable
carry, even if a particular unit test does not read them.
Changing the loaded runner/plan/classifier requires a restart and a new full run.
Failures invalidate the baseline. A fresh component-carry session imports nothing
and starts full; the separate explicit prose path does not import or create receipts.

This protects against stale inputs, damaged files and self-reported evidence within
a trusted local development session. The developer who owns the process and host
can change executables, permissions, private exports or process memory; this is
not an attestation against a hostile host owner. Do not accept an exported
manifest alone as externally verified proof. The existing required hosted checks remain independent and blocking;
no signing keys, tokens or new authentication permissions are introduced.

Carry applies only to descendant commits on the same local feature branch.
Synthetic PR merge, exact-main, release/tag/archive equality, physical-device,
provider, quiet-host performance and final acceptance qualifications need their own
exact-source execution. A local source result never completes those independent
exits. Start a new full session when switching branch, restarting or changing the
validation policy. Confirm every selected real-wire case actually ran and preserve
original failures; an empty selection is not acceptance.

## Coverage floor (ratchet)

The `Build & Test` job collects vet, build and race/coverage test outcomes for all
six modules before returning a blocking aggregate exit. A root/module failure
does not hide later module results. Two independent module workers run vet/build/test sequentially within each module, with separate logs/profiles. The existing single job avoids matrix setup
and new aggregation/check names. Its JSON outcomes and partial profiles survive
failure in the coverage artifact. Coverage merging and floors still run after a
collector failure; missing profiles or any lowered coverage fail the job.
It measures `-covermode=atomic`, merges six profiles with `gocovmerge`, and enforces
the unchanged per-module floors in `Enforce coverage floors`. No failed module is
converted to success.

The floors are a **ratchet, not an aspiration**: adopted floors are never lowered.
Raise them against a durable baseline under the same measurement scope. They are per-module
(not one workspace number) because module totals vary widely — generated `pb` and the
CLI sit far below `core`/`mcp`, and a single merged floor would let a regression in a
well-tested module hide behind the large low-coverage denominator.

Each module uses `-coverpkg=./...` so calls between its packages count, including
the generated Connect/protobuf boundary. The pattern stays within the selected
module; it neither attributes another workspace module's code nor excludes
generated code. Transport mocks qualify wire fidelity only; real Server policy
and provider/device evidence remain separate.

Current floors (**raise these in the same PR** whenever a
module's coverage rises durably):

| Module (profile slug) | Floor |
| --- | --- |
| `root` | 31% |
| `core` | 84% |
| `mcp` | 84% |
| `pb` | 15% |
| `sdks-go` | 37% |
| `server` | 52% |

The #1660 integration candidate measured 63.8 / 87.9 / 87.6 / 23.8 / 70.4 /
80.5% respectively with race, shuffle and module-wide instrumentation. Existing
floors remain unchanged during this measurement-scope correction. The earlier
package-only figures and these figures are not a same-condition improvement
comparison. Ratchet updates need a durable baseline under the corrected scope,
including main-directed CI; generated statements remain in the denominator.

The authoritative values live in the `floors=` line of the `Enforce coverage floors`
step in [`.github/workflows/go.yml`](.github/workflows/go.yml); this table must be kept
in sync with it. To reproduce a module's number locally:

```bash
(cd <module> && go test -coverpkg=./... -covermode=atomic -coverprofile=/tmp/cov.out ./...)
go tool cover -func=/tmp/cov.out | tail -1   # the `total:` line
```

If a PR legitimately lowers a floor (e.g. deleting a well-covered package), say so in
the PR body and update both the workflow and this table together.

## External-surface testing policy (Definition of Done)

Lantern is a database; its externally observable behaviour — the RPC surface, the
Go/Node SDKs, the CLI grammar, the MCP tools, the `LANTERN_*` env contract, and the
TTL/decay semantics — must not regress silently. Every PR that adds or changes any
of that surface ships, **in the same PR**:

1. **An integration test over the real wire path.** New or changed RPCs, SDK
   methods, CLI verbs, and MCP tools get coverage in `tests/integration/` — a
   Connect/h2c round-trip through the SDK, or the raw generated client for surface
   the SDK does not wrap — asserting the happy path AND at least one failure/edge
   contract (NotFound sentinel, batch partial-miss, chunking, TTL expiry,
   idempotent retry, ...). Unit tests in the owning module complement but do not
   replace this: the wire path is where the singular→plural facades, validation
   interceptors, and codec behaviour actually live. For the standalone Node
   SDK, real-wire cases instead live in `sdks/node/test/`: `node-sdk.yml` runs
   `bun run test:real-wire` on Node 20/22 against Connect/h2c and `bun test`
   for browser Connect-Web real-wire cases. Both paths must assert a happy
   path and a failure/edge contract; in-process stubs do not replace them.
2. **Bench coverage for perf-relevant paths.** A change on a hot path (reads,
   writes, scans, traversals, streams) joins an existing scenario fan-out in
   `testbed/bench/scenarios/` or gets a new scenario. The release-sweep scenarios
   carry `perf_gate:` floors (min steady rps / max p99 / max non-OK ratio)
   enforced in local, exact-source pre-tag qualification. The optional
   `bench-nightly.yml` is a hosted-platform diagnostic, not release evidence.
   Sizing, host qualification, and re-baselining rules live in
   [testbed/bench/README.md](testbed/bench/README.md). Perf floors are a
   ratchet like the coverage floors: when a PR legitimately moves one (an accepted
   performance trade-off), adjust the floor in the same PR and say so in the PR
   body.
3. **Wire-schema changes keep the bench templates green.** The root
   `go test ./...` includes `testbed/bench/scenarios_gate_test.go`, which renders
   every scenario `data_template` and validates it against the current proto
   schema. If you retire or rename a wire field, migrate every scenario that sends
   it in the same PR (#934 — six Illuminate scenarios silently broke and the
   nightly went red — is the cautionary tale).
4. **Coverage floors hold** (previous section) and the pre-push quality gate
   passes.

Exemptions: doc-only changes, and pure refactors with no wire-visible behaviour
change (still subject to the coverage ratchet). When in doubt, add the integration
test.

## Before merging a PR

- Wait for **all required checks** green. Never use `--admin` or `--no-verify`.
  A cohesive PR from clean `main` may close multiple related Issues when their
  outcomes and verification remain independently reviewable.
- Before merging, inspect the exact final squash subject and body, including
  any text copied from historical commit messages. Reject GitHub CI-skip markers
  (`[skip ci]`, `[ci skip]`, `[no ci]`, `[skip actions]`, `[actions skip]`) and
  a true `skip-checks` trailer. Passing PR checks do not validate this message.
- Supply the reviewed subject and body explicitly with
  `gh pr merge <n> --squash --delete-branch --match-head-commit <sha>`
  plus `--subject <subject> --body-file <file>`, then
  `git checkout main && git pull --rebase`. Verify the resulting main SHA and
  ordinary CI runs before recording final-source acceptance.
- **Multi-issue `Closes` syntax:** GitHub only auto-links the *first* issue on a
  comma-separated line. Use one keyword per issue (`Closes #1, closes #2, closes #3`)
  or one keyword per line; otherwise the later issues are left open after merge.
- **Non-closing references:** A negated or hyphenated closing keyword next to an
  issue reference can still register in GitHub's `closingIssuesReferences` and
  auto-close that Issue. For non-closing references use `Tracks #N`, then check
  the PR's live `closingIssuesReferences` before merging.

## After editing `.proto`

```bash
go generate ./...                                # buf generate (NO --clean) + wire
sdks/dart/scripts/codegen.sh
(cd sdks/node && bun run codegen)
(cd sdks/rust && cargo xtask codegen)
testbed/dart-transport-probe/scripts/codegen.sh
```

Commit regenerated stubs under `pb/`, `sdks/dart/lib/src/gen/`,
`sdks/node/src/gen/`, `sdks/rust/src/generated/`, and both
`testbed/dart-transport-probe/{connect,grpc}/lib/src/gen/`
when they change. Never hand-edit generated files. CI reruns these generators and
checks for drift in `go.yml`, `dart-sdk.yml`, `node-sdk.yml`,
`rust-sdk.yml`; even a comment-only proto edit can change generated docs.
The historical transport comparison checks its generated sources when manually
dispatching `dart-transport-probe.yml`; regenerate its stubs with schema changes
as above. It is not a routine PR or release prerequisite.
Never pass `--clean` to buf — its output root is `pb/`, so `--clean` would delete
`pb/go.mod` and `pb/doc.go` alongside the stubs.
If the wire shape consumed or shipped by `lantern_client` changes, cut an independent
`sdks/dart/vX.Y.Z` release after the compatible server/proto change lands.

## After editing wire providers (`server/provider/*`, `server/cmd/wire.go`)

```bash
cd server && go tool wire ./cmd       # or: make wire
```

Commit the regenerated `server/cmd/wire_gen.go`; never hand-edit it. If you introduced a
new sub-config, update the **Providers** note in `AGENTS.md`.

## After renaming any public SDK / server symbol

- Grep the **entire workspace** (every Go module, the TypeScript `admin/`, and all
  `*.md`) for the old name. Single-module compilation is not sufficient — modules can
  build in isolation while a cross-module call site is broken.
- Update the affected notes in `AGENTS.md` and `README.md` in the same PR.

## After adding a dependency

- Minimize external dependencies. Lantern is the database; do not add
  PostgreSQL or another external runtime store for Server authentication.
  Prefer native storage, replication and persistence primitives. The planned
  OIDC boundary uses internal `sys:` metadata and physical `data:` keys behind
  unchanged public logical keys (ADR 0012, #1599, #1615); namespace isolation
  alone does not prove asynchronous authorization freshness.
- The Dart SDK SQLite route, including `sdks/dart/offline_sqlite`, is explicitly
  approved. This Server-auth policy does not revoke that exception.

- Add the require to the module that **actually imports** it (server-only middleware →
  `server/go.mod`; client transport → `sdks/go/go.mod`; cli or integration tests only →
  root `go.mod`).
- Add Dart dependencies only to `sdks/dart/pubspec.yaml`; keep it pure Dart and run
  `dart pub get --enforce-lockfile`, `dart analyze`, and `dart test` in that package.
- Run `go mod tidy` in **every** affected module — workspace `replace`s do not propagate
  `go.sum` entries.
- If the `Dockerfile` was touched, confirm every workspace member's `go.mod`/`go.sum` is
  `COPY`ed **before** `go mod download` (a `go.work` prerequisite). The module set must
  stay in lockstep with `go.work`. PR CI does not run `docker build`, so a missed `COPY`
  only surfaces at release-tag time.

## Bumping the Go toolchain

Update **all** of these in one PR, then re-run the local quality gate:

1. Every `go.mod` (all workspace modules) and `go.work`.
2. Every `Dockerfile` (`FROM golang:<version>-alpine`).
3. Every `go-version:` in `.github/workflows/*.yml`.
4. The Go-version mentions in `README.md` (no instruction file pins a version — they
   point here / to `go.mod`).

## Bumping the `buf` pin

Update the `@vX.Y.Z` suffix in [generate.go](generate.go), `BUF_VERSION` in the
[Makefile](Makefile), the Proto setup version in [.github/workflows/go.yml](.github/workflows/go.yml)
and the pinned local commands above together. Keep those versions identical.

## Cutting a release (`vX.Y.Z`)

Tag order matters because each downstream module pins its upstream tag:

1. `pb/vX.Y.Z`
2. `core/vX.Y.Z`
3. `sdks/go/vX.Y.Z` — first pin a **published** `pb/vA.B.C` tag in
   `sdks/go/go.mod` whose Go sources and `go.mod` match `pb/` at the intended SDK
   tag. After that pb tag is available through the public Go module proxy and
   all applicable quality and physical-device gates pass, create the SDK tag
   locally at a clean frozen HEAD and run
   `python3 .github/scripts/verify_go_sdk_release.py --tag sdks/go/vX.Y.Z`
   **before pushing** with the `sdks/go/go.mod` Go toolchain installed as `go`
   (`GOTOOLCHAIN=local` disallows automatic toolchain selection). Push only
   after it passes. The pushed SDK tag triggers
   `sdks-go-publish.yml`: normal configured-source-integration followed by one
   standalone check of the exact tagged `sdks/go` archive; it does not repeat
   the root/Server workspace gate. Root and MCP releases retain their shared
   full-module verification lanes.
   That check strips the sole permitted `replace pb => ../../pb` directive
   **only in a temporary copy** and rejects any other replacement,
   resolves the pinned pb tag through the public Go module proxy and checksum
   database from a checksum-free directory and fresh module cache with
   `GOWORK=off`, compares the published pb Go sources and
   `go.mod` with the SDK-tag pb tree, and requires clean `gofmt -l .`,
   `go vet -mod=readonly ./...`, the existing readonly build, and
   `go test -mod=readonly -race -shuffle=on -json ./...` in that isolated SDK.
   Use the pinned Go toolchain's supported native race host, with cgo and a C
   compiler where required. The Ubuntu standalone job sets `CGO_ENABLED=1`;
   the pinned Go Darwin race path does not require cgo. No race-disable
   fallback is allowed.
   Existing command/test timeouts remain unchanged. A
   missing, stale, unresolved, or replaced pb blocks the exact-title GitHub
   Release; green workspace tests alone are not enough. The Go module proxy
   pulls tags directly from VCS, so this workflow cannot prevent an invalid
   pushed tag from being indexed; check the pb dependency **before** tagging,
   and never move a tag to repair it. There is no SDK artifact to push.
4. Bump the matching `require`/`replace` lines in the root `go.mod` to the freshly-tagged
   versions.
5. Before tagging the reviewed root commit, run the untruncated eight-scenario
   sweep and four independent fresh-WAL receipt families locally on one
   exact-source image. One complete pass on a chosen suitable environment
   (ARM or x86) is mandatory; a second-architecture run is not. Retain the
   source SHA, image ID, platform, host/CPU conditions, per-scenario verdicts,
   and raw artifact hashes in the release tracking Issue. Disclose known
   failed hosted runs without treating them as proof of an architecture-specific
   defect; one local pass does not establish a production SLO on either
   architecture. Track production readiness separately. Do not substitute an
   earlier branch run, erase a genuine failed gate by switching environments,
   skip a scenario, cherry-pick a passing run, or relax gates to publish.
   The procedure is in [testbed/bench/README.md](testbed/bench/README.md).
6. Root `vX.Y.Z` — triggers `docker-publish.yml`. Before any multi-arch image or GitHub
   Release can publish, the tagged SHA must pass the short blocking Search qualification:
   request-boundary tests, production real-h2c semantics, HA convergence, and the fresh
   three-replica `search_qualification` scenario. Its artifact records the tag, full SHA,
   and an explicit pass/fail/skipped status for every stage and scenario; any non-success
   blocks the image and GoReleaser jobs. The tag workflow does **not** run the
   long benchmark: release notes point to the platform-scoped local evidence
   recorded before tagging. Deterministic PR CI and the short Search gate
   remain blocking.

The checksum closure is committed before tagging. The pinned GoReleaser recipe
checks live source cleanliness after dependency download and verifies each
binary's source/target metadata before any cask publisher runs. Hidden Git index
flags and Go environment overrides are refused. A complete
ten-target archive/checksum check then blocks Actions artifact upload and the
GitHub Release; this archive check follows optional cask publication. Its
separate integrity artifact preserves the receipt without
adding a public Release asset. CLI version evidence distinguishes the exact
linker recipe and host-native execution from values not exposed by trimmed
BuildInfo. OCI images have keyless signatures and provenance; the binary
archives remain unsigned and are verified through checksums, CI artifacts and
source binding.

The root release also builds the `lantern` (server) and `lantern-cli` binaries via
GoReleaser and pushes Homebrew casks to
[`anaregdesign/homebrew-tap`](https://github.com/anaregdesign/homebrew-tap)
(`brew install --cask lantern` / `lantern-cli`). Cask publishing needs the
`HOMEBREW_TAP_GITHUB_TOKEN` secret — a fine-grained PAT or App token with
`contents:write` on the tap repo. It is gated on that secret, so a release without it
still succeeds but skips the cask push.

Release workflows publish artifacts only; they do not deploy a running cluster.
The project-managed GKE deployment target and its manual recovery workflow are
retired. The [Helm chart](deploy/helm/lantern/) remains available for Kubernetes
installations, with deployment performed separately by the operator.

The `server/` module is never tagged independently — it ships under the root tag. arm64
buildx under QEMU is slow; if a root tag already pushed the amd64 image, bump the patch
number rather than force-moving the tag.

`mcp/`, `admin/`, `sdks/dart/`, `sdks/rust/`, and the offline core are cut
**independently** of the root cadence:

- `mcp/vX.Y.Z` triggers `mcp-publish.yml` → `ghcr.io/anaregdesign/lantern-mcp` (multi-arch
  + cosign). The MCP server only imports `pb/` and `sdks/go/`, so a `sdks/go` bump is the
  only upstream pin that forces a re-tag.
- `admin/vX.Y.Z` triggers `admin-publish.yml` (admin gates → multi-arch + cosign) →
  `ghcr.io/anaregdesign/lantern-admin`. The admin SPA links the local
  `sdks/node/` package through `file:../sdks/node`; the release workflow builds
  that SDK from the tagged tree before building the SPA. A `pb/` change that
  requires a new admin image must first flow through a published
  `sdks/node/v*` release. Caddy serves the SPA and optionally proxies
  `/auth/*`, `/browser/*` and `/graph.v1.*/*` to the operator-fixed
  `LANTERN_ADMIN_SERVER_UPSTREAM` (`scheme://hostname:port`). The browser's
  gateway picker never selects that upstream.

  **OFF direct-Server example:** open `http://localhost:8080`, select
  `http://localhost:6380` as the gateway, and set the Server's
  `LANTERN_CORS_ALLOWED_ORIGINS=http://localhost:8080`. This local example
  has no IdP or browser session.

  **OIDC same-origin example:** expose Admin at `https://admin.example.com:8443`
  and select that exact HTTPS public origin as the gateway. Set
  `public_origin` to that origin in the human `LANTERN_SECURITY_CONFIG_JSON`.
  Supply the same configuration to `provision-security`, `init-security` and
  every normal start; the retained identity material is not a runtime-config
  source. Register each configured browser Issuer's derived redirect URI using
  that origin plus the exact Issuer-specific `/auth/callback/<SHA-256>` path.
  See the [configuration guide](deploy/examples/auth/README.md#environment-json-startup).
  Configure, for example,
  `LANTERN_ADMIN_SERVER_UPSTREAM=https://server.internal.example:6380`;
  verify its certificate against that upstream hostname and mount
  `LANTERN_ADMIN_SERVER_CA_FILE` for a private CA. Preserve the public
  Host/scheme through the proxy chain and configure exact
  `trusted_proxy_ips` in that same JSON where required. A plaintext Server hop is
  permitted only behind the explicitly trusted exact gateway IP with validated
  HTTPS/public Host headers. Keep credentials and private keys out of the SPA,
  browser local storage and Vite env.

  Pin auth/browser and security/control routes to the configured eligible
  authority origin, preserving per-attempt login affinity. OIDC requires its
  original quorum provisioning and current native time; a pinned proxy is not
  authority proof. Operational cutover and final qualification remain
  #1608/#1609 S5 work. Missing upstream routes fail closed
  instead of returning the SPA shell. The optional Prometheus proxy checks
  `/auth/operations` first: OFF permits diagnostics; OIDC requires current
  `operations.read` authority. Only GET diagnostics pass, and Prometheus never
  receives cookies or Authorization. See the [OIDC operations guide](docs/oidc-operations.md#browser-and-diagnostics-boundary)
  and [HA runbook](docs/ha-runbook.md) for the current deployment contract.
- `sdks/node/vX.Y.Z` triggers the two-runtime real-wire `node-sdk.yml` gate and npm
  trusted publishing with provenance. Before creating an exact-title GitHub Release,
  dispatch the read-only `node-registry-audit.yml` on the default branch with the
  immutable tag and the tagged publish job's SHA-1. It downloads the *actual* npm
  archive using a fresh cache, checks both registry digests against that publish,
  and verifies npm's signed provenance binds the archive to the tag, workflow, and
  source commit. An npm publish log alone, or a local registry cache, is not proof
  the public artifact is available.
- `sdks/rust/vX.Y.Z` must match the `sdks/rust/Cargo.toml` package version and
  an exact `## X.Y.Z` heading in `sdks/rust/CHANGELOG.md`. The prepared
  `0.1.0` heading is not evidence of publication. `rust-release.yml` verifies
  the exact immutable tag, four representative MSRV/native conformance lanes,
  warning-free dependency audit, and a license-checked, independently tested
  crate archive; the publish dry-run must pass and a fresh repackage must
  reproduce the tested bytes. The **first** crates.io publication (the only
  version in the complete registry history, not necessarily `0.1.0`) is
  owner-held from the verified tag using pinned Cargo `1.97.1`, after
  matching the owner's local package SHA-256 with CI's candidate: no CI token
  or secret. After the crate exists, only a protected `crates.io` Environment
  and repository-bound trusted publisher may publish subsequent versions
  with short-lived OIDC. A separate read-only job verifies the registry's
  actual archive bytes before the exact-title GitHub Release. Follow
  [RELEASING.md](sdks/rust/RELEASING.md);
  never publish an unverified package or bypass a failed gate.
- `sdks/dart/vX.Y.Z` must match `sdks/dart/pubspec.yaml` version `X.Y.Z` and a
  `CHANGELOG.md` heading `## X.Y.Z`. It triggers `dart-sdk.yml`, which must pass the
  minimum/current Dart gates, real-wire tests, warning-free docs, isolated publish-
  archive resolution, `pana`, and Android/iOS example conformance before publishing
  `lantern_client` with pub.dev OIDC (`id-token: write`, no token secret). Configure
  pub.dev automated publishing for repository `anaregdesign/lantern` and tag pattern
  `sdks/dart/v{{version}}`. A read-only preflight verifies the exact tag and
  dependency-closed candidate archive. The publish job has `contents: read` and
  `id-token: write` only and uploads that verified archive. An independent read-only
  job waits for pub.dev visibility and compares every archive file with the candidate,
  including on reruns of an existing version. Only then may a separate job with
  `contents: write` and no OIDC create/update the GitHub Release; its title is exactly
  the tag. A missing package, failed publication, or differing/missing published
  archive blocks the Release. Paired-source offline validation remains required;
  its future hosted parent dependency cannot block this parent publication.
- `sdks/dart/offline/vX.Y.Z` independently publishes the storage-neutral
  `lantern_client_offline` core owned by #1162. It does not include SQLite,
  encryption, secure storage, or another production adapter; #1163 owns the
  separately versioned SQLite package. The offline tag must match its
  `pubspec.yaml` version and `CHANGELOG.md` heading. It triggers the full
  minimum/current Dart, real-wire, archive, Android emulator, and iOS simulator
  Gate in `dart-sdk.yml`. The separate `offline-hosted` job must finish with
  `qualified=true` after verifying the already-published parent bytes and the
  refreshed hosted lock. A pending, failed or unexpectedly skipped prerequisite
  blocks offline preflight. That preflight retains the physical Android/iOS matrix
  and builds an isolated archive from the exact tag, checks its hosted parent
  dependency and contents,
  resolves it outside the checkout, and checks pub.dev state. The package
  already exists on pub.dev; later versions use the separate offline OIDC
  publish job only after a package admin verifies its private automated
  publishing binding. Read-only archive equality gates the exact-title
  GitHub Release. The parent tag's release jobs never run for an offline tag.

**Offline first-publication history (#1162).** `lantern_client_offline 0.2.0`
was first published from its exact tagged revision with one-time interactive
OAuth; identity CDC 0.3.0 and receipt-bearing 0.4.0 are also hosted. That
bootstrap is complete, not a procedure for later receipt-bearing candidates. Never
repeat manual publication for a later version, reuse a published tag, or put
a pub token in GitHub Secrets, CI, or the repository.

**Dart publishing status.** The parent `lantern_client` 0.4.1 is published and
its exact-tag archive has been verified. The one-time manual first publish completed with `0.1.0`,
and pub.dev automated publishing is bound to repository `anaregdesign/lantern` and tag
pattern `sdks/dart/v{{version}}`. Later releases are tag-driven only; do not run a
manual `dart pub publish`. Immediately before tagging, check
`https://pub.dev/api/packages/lantern_client` and confirm the target version does not
already exist. Never force-move a published Dart tag/version—bump patch.

**Offline receipt release preparation (#1586).** Published offline 0.4.0
uses hosted `lantern_client ^0.3.3` and completed #1399. Published 0.5.0 adds
targeted contribution Delete with hosted `lantern_client ^0.4.1`; #1586 completed
its frozen-source, physical and publication gates. The new 0.6.0 candidate binds
typed undisclosed acceptance to parent 0.5.0 source and future hosted `^0.5.0`.
The committed offline hosted lock still records parent 0.4.1, which does not satisfy
`^0.5.0`; it is pending release preparation, not hosted 0.6.0 acceptance.
Publish and verify parent 0.5.0 first when separately authorized, then generate
and review the offline hosted lock in a follow-up preparation change before
freezing the offline device candidate. The bounded commands are in the
[lock-refresh procedure](sdks/dart/example/offline-release-resume.md#parent-publication-and-hosted-lock-refresh).
`paired_source_gate.py --refresh-lock` updates only the paired-source lock; it
cannot refresh or qualify the hosted lock. Its source, hosted archive, final-source,
physical and publication exits are independent; previous release evidence does not qualify this candidate. The maintained Flutter example and
unpublished SQLite adapter use local path overrides; resolve the offline
candidate archive against the hosted parent outside the checkout without
a path override. Before tagging, confirm the target version and tag are
unused. Have an authorized pub.dev package admin inspect, without changing
settings, that `lantern_client_offline` has GitHub Actions publishing enabled
for repository `anaregdesign/lantern`, tag pattern
`sdks/dart/offline/v{{version}}`, the push event enabled, and **Require GitHub
Actions environment** checked for exact `pub.dev`. Record dated, redacted
confirmation without credentials or emails. For this single-maintainer beta,
keep signed physical-test originals privately only through release verification
as described in the physical release runbook; a second human custody review or
year-long cloud archive is not a release prerequisite. The
protected GitHub `pub.dev` environment already selects both parent
`sdks/dart/v*.*.*` and offline `sdks/dart/offline/v*.*.*` tag patterns and
requires human reviewers; its rules do not prove that separate private
pub.dev binding or successful OIDC publication. Never bypass the approval.

Merge all required source and release-contract docs before freezing one clean
tested source commit. The
[physical release runbook](sdks/dart/example/offline-release-resume.md)
owns the receipt-specific Android/iOS evidence and exact-commit procedure;
prior release, CDC, or simulator records do not qualify the 0.6.0 candidate. After both
physical platforms and all pre-tag gates pass, tag only the immediate
evidence-only child of that frozen commit. Do not change code or release docs
between the tested source commit and its tagged evidence child. The original
#1399 [performance gate](docs/decisions/0010-bounded-mutation-receipts.md#dependencies-and-rollout)
requires four separate, sequential, fresh-WAL family scenarios on the same
immutable final image, not a simultaneous mixed-load run; a preparatory
driver does not supply final measured acceptance. That four-family gate was
completed for 0.4.0; its evidence does not qualify new contribution Delete
code. For #1586, retain the unchanged resource thresholds and add the fifth
family to offline replay/resource qualification. The offline tag's full
Gate, OIDC publication, and published-archive equality must pass before
the exact-title GitHub Release. If publication fails, leave the tag and
Issue open and create no Release; never move a published tag or version.

**Release title convention (locked).** Every GitHub Release title MUST equal its tag name
verbatim (`v0.7.2`, `core/v0.2.0`, `sdks/go/v0.8.0`, `sdks/dart/v0.1.0`,
`sdks/dart/offline/v0.1.0`, `sdks/rust/v0.1.0`, `mcp/v0.1.0`, `admin/v0.1.0`, …) —
no friendly aliases. The container-publishing workflows enforce this via
`gh release create --title "$TAG"`; when creating SDK releases manually, pass the same
`--title "$TAG"`.

## Periodic doc-staleness sweep

Before each release, or whenever memory and code disagree:

- Re-read `AGENTS.md`, `.github/copilot-instructions.md`, `README.md` "Conventions and
  gotchas", and `/memories/repo/lantern.md`.
- For every cited symbol, file path, env var, and shell command, verify it still exists
  and works (CLI snippets via `go run ./cli <subcommand> --help`).
- Reconcile any drift in the same PR.
