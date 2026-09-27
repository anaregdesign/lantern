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

- One Issue per coherent problem (`gh issue create` if none exists). Related Issues
  may share a cohesive PR when each outcome remains independently reviewable.
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
  lib test tool && dart pub get --enforce-lockfile \
  && dart analyze && dart test \
  && dart doc --output "$(mktemp -d)" --validate-links)
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

During edits, run the narrowest targeted checks for changed behavior rather than
repeating the whole gate after each intermediate change. Plan one complete
mandatory gate for the reviewed, cohesive PR head before pushing; every later
push must again have passing results for all required components. A passing
component can carry across a documentation-only follow-up only when its inputs
and any required documentation-dependent checks are unchanged. CI then
validates the exact synthetic merge, not a preliminary local branch image.

Per-module test runs are mandatory: the root `go test ./...` does **not** span
submodules. `make lint` runs the same linter as the `Lint` job. The `Proto (buf)` check
fails on any uncommitted codegen diff — regenerate locally first (below).

The Dart SDK is outside `go.work`; its format/analyze/test gate is therefore
separate too. When `proto/` changes, run `sdks/dart/scripts/codegen.sh` and commit
the regenerated `sdks/dart/lib/src/gen/**` files. The supported toolchain source of
truth is `docs/decisions/0001-dart-mobile-transport.md`; the workflow pins mirror that
decision and also runs the package at its declared minimum Dart floor. The workflow
routes backend/search-only changes through the current-Dart unit and real-wire gates.
The separately tested `full` scope adds minimum Dart and package-quality checks for
Dart SDK, proto, codegen/toolchain, workflow, release-tag, root Go dependency, and
release-contract documentation changes. The `mobile` scope requires Android and iOS
for Dart/Flutter, proto/codegen, mobile transport/release-contract, workflow,
release-tag, and Go language/toolchain directive changes; ordinary Go dependency
or general documentation changes skip native jobs. The stable `Gate` job checks
both decisions independently. The experimental
`sdks/dart/offline/` child runs from its own working directory at minimum/current
Dart, including fresh-process canonical snapshot tests and real-server
committed-response-loss replay. Android and iOS jobs upload content-free JSON
manifests bound to the exact commit, workflow run, Flutter/Dart revisions,
application package, platform kind, scenario set, and pass result. Simulator
manifests do not substitute for the sanitized exact-revision physical-device
record required before an offline release. The merged offline 0.4.0 source
candidate declares a hosted `lantern_client: ^0.3.2` dependency and has an
independent candidate archive gate; the parent `lantern_client` publish
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
After `simctl` returns the Runner PID, it reads that process's
retrospective Simulator log for the VM Service URL, test-body marker, and
terminal result, then runs `flutter drive --use-existing-app` to verify the
actual assertions. The outer step remains 14 minutes, leaving time for bounded
diagnostics. Only `launch_stall` or `attach_stall` may retry, once, on a newly
created simulator with the same runtime/device type and a different UDID. An
exited Runner, build failure, assertion or RPC failure, and post-body stall fail
the blocking `Gate` directly. Bounded, redacted process/simulator/app
diagnostics are always uploaded; the temporary retry simulator is always
deleted. Each attempt builds the integration-test app and installs it on its
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

## Standalone Rust SDK gate

`sdks/rust/` is outside `go.work`. Its checked-in `src/generated/**` sources
come only from `cargo xtask codegen` run in that directory (pinned Tonic/Prost
codegen with bundled `protoc`). Commit `Cargo.lock` for repository CI; the
library archive builds without the Go workspace, root `proto/`, Buf, a system
`protoc`, or a generator build script. `.github/workflows/rust-sdk.yml` checks
codegen drift, formatting, warnings-denied Clippy, tests, example compilation,
and warning-free docs with Rust 1.88/stable on Linux, macOS, and Windows.
Each stable lane builds the production Go server and runs the full opt-in
real-wire h2c/TLS/auth/CRUD/query suite, including failure paths. Locally,
build the server from the root and run that suite from the crate:

```sh
mkdir -p sdks/rust/target
go build -o sdks/rust/target/lantern-smoke ./server/cmd
(cd sdks/rust && LANTERN_RUST_TEST_SERVER="$PWD/target/lantern-smoke" \
  cargo test --locked --all-features -- --ignored --test-threads=1)
```

Audit locked runtime and codegen dependencies with
`(cd sdks/rust && cargo audit --deny warnings --file Cargo.lock)`; an
unmaintained crate warning blocks release as surely as a vulnerability.
The independent [Rust release procedure](sdks/rust/RELEASING.md) freezes a
clean candidate, inspects its license and archive, tests the package in
isolation, runs a publish dry-run, and compares a fresh repackage. It does
not authorize publishing during SDK development.

## Coverage floor (ratchet)

The `Build & Test` job measures per-module coverage (`-covermode=atomic`), merges the
six profiles with `gocovmerge`, and then enforces a **per-module floor** in the
`Enforce coverage floors` step. A PR that drops any module below its floor fails CI.

The floors are a **ratchet, not an aspiration**: each sits just below that module's
current measured baseline, so coverage can only hold or climb. They are per-module
(not one workspace number) because module totals vary widely — generated `pb` and the
CLI sit far below `core`/`mcp`, and a single merged floor would let a regression in a
well-tested module hide behind the large low-coverage denominator.

Current floors (baseline measured on `main`; **raise these in the same PR** whenever a
module's coverage rises durably):

| Module (profile slug) | Floor |
| --- | --- |
| `root` | 31% |
| `core` | 84% |
| `mcp` | 84% |
| `pb` | 15% |
| `sdks-go` | 37% |
| `server` | 52% |

The authoritative values live in the `floors=` line of the `Enforce coverage floors`
step in [`.github/workflows/go.yml`](.github/workflows/go.yml); this table must be kept
in sync with it. To reproduce a module's number locally:

```bash
(cd <module> && go test -covermode=atomic -coverprofile=/tmp/cov.out ./...)
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
   carry `perf_gate:` floors (min steady rps / max p99 / max non-OK ratio) enforced
   by the blocking nightly (`bench-nightly.yml`); sizing and re-baselining rules
   live in [testbed/bench/README.md](testbed/bench/README.md). Perf floors are a
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
- Merge with `gh pr merge <n> --squash --delete-branch`, then
  `git checkout main && git pull --rebase`.
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
`rust-sdk.yml`, and `dart-transport-probe.yml`; even a comment-only proto
edit can change generated docs.
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

Update **both** the `@vX.Y.Z` suffix in [generate.go](generate.go) and `BUF_VERSION` in
the [Makefile](Makefile) — keep them identical.

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
   `sdks-go-publish.yml`: a workspace vet/build/test
   gate followed by a standalone check of the exact tagged `sdks/go` archive.
   That check strips the sole permitted `replace pb => ../../pb` directive
   **only in a temporary copy** and rejects any other replacement,
   resolves the pinned pb tag through the public Go module proxy and checksum
   database from a checksum-free directory and fresh module cache with
   `GOWORK=off`, compares the published pb Go sources and
   `go.mod` with the SDK-tag pb tree, and builds/tests the isolated SDK. A
   missing, stale, unresolved, or replaced pb blocks the exact-title GitHub
   Release; green workspace tests alone are not enough. The Go module proxy
   pulls tags directly from VCS, so this workflow cannot prevent an invalid
   pushed tag from being indexed; check the pb dependency **before** tagging,
   and never move a tag to repair it. There is no SDK artifact to push.
4. Bump the matching `require`/`replace` lines in the root `go.mod` to the freshly-tagged
   versions.
5. Root `vX.Y.Z` — triggers `docker-publish.yml`. Before any multi-arch image or GitHub
   Release can publish, the tagged SHA must pass the short blocking Search qualification:
   request-boundary tests, production real-h2c semantics, HA convergence, and the fresh
   three-replica `search_qualification` scenario. Its artifact records the tag, full SHA,
   and an explicit pass/fail/skipped status for every stage and scenario; any non-success
   blocks the image and GoReleaser jobs. The workflow separately runs the full
   release-time bench sweep and splices its report into the notes. That profiling bench
   remains **non-blocking**: if it fails
   or is cancelled the release still uses a placeholder bench section (the `release`
   job's `needs:` deliberately excludes `bench`, because Actions treats `cancelled` as
   neither success nor failure).

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
  `ghcr.io/anaregdesign/lantern-admin`. The admin SPA's only cross-module build-time
  input is the `proto/` sources (consumed by `bun run codegen`), so a `pb/` bump is the
  only upstream pin that forces a re-tag. The container hosts the SPA on Caddy and does
  not reverse-proxy the Lantern listener — the browser calls the gateway directly, so the
  server's `LANTERN_CORS_ALLOWED_ORIGINS` must include the admin origin.
- `sdks/rust/vX.Y.Z` must match the `sdks/rust/Cargo.toml` package version and
  an exact `## X.Y.Z` heading in `sdks/rust/CHANGELOG.md`. The prepared
  `0.1.0` heading is not evidence of publication. `rust-release.yml` verifies
  the exact immutable tag, six native OS/MSRV/stable conformance lanes,
  warning-free dependency audit, and a license-checked, independently tested
  crate archive; the publish dry-run must pass and a fresh repackage must
  reproduce the tested bytes. The **first** crates.io publication is
  owner-held from the verified tag after matching the owner's local package
  SHA-256 with CI's candidate: no CI token or secret. After the crate exists,
  only a protected `crates.io` Environment and repository-bound trusted
  publisher may publish subsequent versions with short-lived OIDC. A separate
  read-only job verifies the registry's actual archive bytes before the
  exact-title GitHub Release. Follow [RELEASING.md](sdks/rust/RELEASING.md);
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
  archive blocks the Release.
- `sdks/dart/offline/vX.Y.Z` independently publishes the storage-neutral
  `lantern_client_offline` core owned by #1162. It does not include SQLite,
  encryption, secure storage, or another production adapter; #1163 owns the
  separately versioned SQLite package. The offline tag must match its
  `pubspec.yaml` version and `CHANGELOG.md` heading. It triggers the full
  minimum/current Dart, real-wire, archive, Android emulator, and iOS simulator
  Gate in `dart-sdk.yml`. A separate offline preflight builds an isolated
  archive from the exact tag, checks its hosted parent dependency and contents,
  resolves it outside the checkout, and checks pub.dev state. The package
  already exists on pub.dev; later versions use the separate offline OIDC
  publish job only after a package admin verifies its private automated
  publishing binding. Read-only archive equality gates the exact-title
  GitHub Release. The parent tag's release jobs never run for an offline tag.

**Offline first-publication history (#1162).** `lantern_client_offline 0.2.0`
was first published from its exact tagged revision with one-time interactive
OAuth; the identity CDC 0.3.0 release is also hosted. That bootstrap is
complete, not a procedure for the receipt-bearing 0.4.0 candidate. Never
repeat manual publication for a later version, reuse a published tag, or put
a pub token in GitHub Secrets, CI, or the repository.

**Dart publishing status.** The parent `lantern_client` 0.3.2 is published and
its exact-tag archive has been verified. The one-time manual first publish completed with `0.1.0`,
and pub.dev automated publishing is bound to repository `anaregdesign/lantern` and tag
pattern `sdks/dart/v{{version}}`. Later releases are tag-driven only; do not run a
manual `dart pub publish`. Immediately before tagging, check
`https://pub.dev/api/packages/lantern_client` and confirm the target version does not
already exist. Never force-move a published Dart tag/version—bump patch.

**Offline receipt release preparation (#1398/#1115/#1399).** Merged offline
0.4.0 source requires hosted `lantern_client ^0.3.2`; it is not yet a
published or qualified receipt release. The maintained Flutter example and
unpublished SQLite adapter use local path overrides; resolve the offline
candidate archive against the hosted parent outside the checkout without
a path override. Before tagging, confirm the target version and tag are
unused. Have an authorized pub.dev package admin inspect, without changing
settings, that `lantern_client_offline` has GitHub Actions publishing enabled
for repository `anaregdesign/lantern`, tag pattern
`sdks/dart/offline/v{{version}}`, the push event enabled, and **Require GitHub
Actions environment** checked for exact `pub.dev`. Record dated, redacted
confirmation and custody sign-off without credentials or emails. The
protected GitHub `pub.dev` environment already selects both parent
`sdks/dart/v*.*.*` and offline `sdks/dart/offline/v*.*.*` tag patterns and
requires human reviewers; its rules do not prove that separate private
pub.dev binding or successful OIDC publication. Never bypass the approval.

Merge all required source and release-contract docs before freezing one clean
tested source commit. The
[physical release runbook](sdks/dart/example/offline-release-resume.md)
owns the receipt-specific Android/iOS evidence and exact-commit procedure;
prior Put-only, CDC, or simulator records do not qualify 0.4.0. After both
physical platforms and all pre-tag gates pass, tag only the immediate
evidence-only child of that frozen commit. Do not change code or release docs
between the tested source commit and its tagged evidence child. The #1399
[performance gate](docs/decisions/0010-bounded-mutation-receipts.md#dependencies-and-rollout)
requires four separate, sequential, fresh-WAL family scenarios on the same
immutable final image, not a simultaneous mixed-load run; a preparatory
driver does not supply final measured acceptance. The offline tag's full
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
