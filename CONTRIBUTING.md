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
- **SDK APIs (Go / Dart / Node), CLI / REPL grammar, the `LANTERN_*` env-var contract, and
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
| `Module` | `pb` / `core` / `server` / `sdks-go` / `sdks-dart` / `sdks-node` / `sdks-python` / `admin` / `mcp` / `tests` / `docs` / `ci` |
| `Release target` | `next pb` / `next sdks-go` / `next sdks-dart` / `next root` / `next mcp` / `next admin-internal` / `unscheduled` |
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

**File a GitHub Issue first, then implement.** This is a hard rule. The Issue pins down
the problem, the chosen option among alternatives, and the scope *before* a diff exists.

- One Issue per coherent problem (`gh issue create`).
- The closing PR references it (`Closes #N`) so the merge wires discussion to diff.
- Exceptions (no Issue required): pure doc-only edits; direct follow-ups requested in an
  in-flight PR review; one-line obvious bug fixes with nothing to discuss.
- When in doubt, file the Issue — the overhead is tiny next to a reworked PR.

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
  && dart pub get --enforce-lockfile && dart analyze && dart test \
  && dart doc --output "$(mktemp -d)" --validate-links \
  && dart pub publish --dry-run)
(cd sdks/dart/offline && dart format --output=none --set-exit-if-changed \
  lib test tool && dart pub get --enforce-lockfile \
  && dart analyze && dart test \
  && dart doc --output "$(mktemp -d)" --validate-links)
(cd sdks/dart/example && dart format --output=none --set-exit-if-changed \
  lib test integration_test \
  && flutter pub get --enforce-lockfile && flutter analyze && flutter test)
```

Per-module test runs are mandatory: the root `go test ./...` does **not** span
submodules. `make lint` runs the same linter as the `Lint` job. The `Proto (buf)` check
fails on any uncommitted codegen diff — regenerate locally first (below).

The Dart SDK is outside `go.work`; its format/analyze/test gate is therefore
separate too. When `proto/` changes, run `sdks/dart/scripts/codegen.sh` and commit
the regenerated `sdks/dart/lib/src/gen/**` files. The supported toolchain source of
truth is `docs/decisions/0001-dart-mobile-transport.md`; the workflow pins mirror that
decision and also runs the package at its declared minimum Dart floor. The workflow
routes backend/search-only changes through the current-Dart unit and real-wire gates;
Dart SDK, proto, codegen/toolchain, workflow, and release-tag changes run the complete
minimum/current Dart, package-quality, Android, and iOS matrix. The stable `Gate` job
checks the required result set for either scope. The experimental
`sdks/dart/offline/` child runs from its own working directory at minimum/current
Dart, including fresh-process canonical snapshot tests and real-server
committed-response-loss replay. Its path dependency and `publish_to: none` remain
intentional until the separate release Issue accepts hosted dependency conversion
after ADR 0002's physical-device graduation gate; the parent `lantern_client`
publish archive must continue to exclude `offline/`. The maintained Flutter app
under `sdks/dart/example/` is a repository integration fixture because it consumes
that child by path, so only its standalone online example is included in the parent
archive. CI uses Dart Pub's own archive builder, unpacks the resulting tarball
outside the checkout, resolves every included `pubspec.yaml` with an isolated
cache, then runs analysis, tests, and `pana` against the unpacked artifact.

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
| `pb` | 5% |
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
   interceptors, and codec behaviour actually live.
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

- Wait for **all required checks** green. Never use `--admin` or `--no-verify`. One PR
  per Issue from clean `main`.
- Merge with `gh pr merge <n> --squash --delete-branch`, then
  `git checkout main && git pull --rebase`.
- **Multi-issue `Closes` syntax:** GitHub only auto-links the *first* issue on a
  comma-separated line. Use one keyword per issue (`Closes #1, closes #2, closes #3`)
  or one keyword per line; otherwise the later issues are left open after merge.

## After editing `.proto`

```bash
go generate ./...   # runs buf generate (NO --clean) + wire
```

Commit the regenerated stubs under `pb/`. Never pass `--clean` to buf — its output root
is `pb/`, so `--clean` would delete `pb/go.mod` and `pb/doc.go` alongside the stubs.
The same schema change triggers `dart-sdk.yml`; regenerate the private Dart stubs too.
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

1. Every `go.mod` (all workspace modules).
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
3. `sdks/go/vX.Y.Z` — triggers `sdks-go-publish.yml` (vet/build/test on the tagged
   commit + a GitHub Release titled exactly the tag). The module proxy pulls source from
   VCS, so there is no artifact to push.
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

The `server/` module is never tagged independently — it ships under the root tag. arm64
buildx under QEMU is slow; if a root tag already pushed the amd64 image, bump the patch
number rather than force-moving the tag.

`mcp/`, `admin/`, and `sdks/dart/` are cut **independently** of the root cadence:

- `mcp/vX.Y.Z` triggers `mcp-publish.yml` → `ghcr.io/anaregdesign/lantern-mcp` (multi-arch
  + cosign). The MCP server only imports `pb/` and `sdks/go/`, so a `sdks/go` bump is the
  only upstream pin that forces a re-tag.
- `admin/vX.Y.Z` triggers `admin-publish.yml` (admin gates → multi-arch + cosign) →
  `ghcr.io/anaregdesign/lantern-admin`. The admin SPA's only cross-module build-time
  input is the `proto/` sources (consumed by `bun run codegen`), so a `pb/` bump is the
  only upstream pin that forces a re-tag. The container hosts the SPA on Caddy and does
  not reverse-proxy the Lantern listener — the browser calls the gateway directly, so the
  server's `LANTERN_CORS_ALLOWED_ORIGINS` must include the admin origin.
- `sdks/dart/vX.Y.Z` must match `sdks/dart/pubspec.yaml` version `X.Y.Z` and a
  `CHANGELOG.md` heading `## X.Y.Z`. It triggers `dart-sdk.yml`, which must pass the
  minimum/current Dart gates, real-wire tests, warning-free docs, isolated publish-
  archive resolution, `pana`, and Android/iOS example conformance before publishing
  `lantern_client` with pub.dev OIDC (`id-token: write`, no token secret). Configure
  pub.dev automated publishing for repository `anaregdesign/lantern` and tag pattern
  `sdks/dart/v{{version}}`. The workflow creates/updates the GitHub Release only after
  pub.dev confirms the version exists; its title is exactly the tag.

**Dart publishing status.** The one-time manual first publish completed with `0.1.0`,
and pub.dev automated publishing is bound to repository `anaregdesign/lantern` and tag
pattern `sdks/dart/v{{version}}`. Later releases are tag-driven only; do not run a
manual `dart pub publish`. Immediately before tagging, check
`https://pub.dev/api/packages/lantern_client` and confirm the target version does not
already exist. Never force-move a published Dart tag/version—bump patch.

**Release title convention (locked).** Every GitHub Release title MUST equal its tag name
verbatim (`v0.7.2`, `core/v0.2.0`, `sdks/go/v0.8.0`, `sdks/dart/v0.1.0`, `mcp/v0.1.0`, `admin/v0.1.0`, …) —
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
