# Releasing the Rust SDK

`lantern-client` is a standalone crates.io package outside `go.work`. It has
its own `sdks/rust/vX.Y.Z` tag cadence, independent of the Go, Dart, and Node
releases. This is a maintainer procedure for an **owner-authorized future
release**, not authorization to tag, publish, or create a GitHub Release now.
`0.1.0` was published and verified through the owner-held bootstrap; later
versions must use the protected, tag-driven OIDC path below.
See [Issue #1381](https://github.com/anaregdesign/lantern/issues/1381),
the [release workflow](../../.github/workflows/rust-release.yml), and the
[contributor quality gate](../../CONTRIBUTING.md#standalone-rust-sdk-gate).

## Freeze and qualify the candidate

1. Obtain explicit crate-owner authorization before cutting a candidate. Freeze
   a clean, reviewed commit. The tag must be exactly `sdks/rust/vX.Y.Z` (three
   numeric components, no prerelease/build suffix), its version `X.Y.Z` must
   exactly match [`Cargo.toml`](Cargo.toml), and
   [`CHANGELOG.md`](CHANGELOG.md) must contain exactly one `## X.Y.Z` heading.
   A matching changelog heading alone does not authorize a tag: qualify the
   reviewed candidate and get the crate owner's approval first. Never move
   or delete a published tag or reuse a crates.io version to repair a failure.
2. Before any tag push, check the crate name and intended crates.io owner;
   protect `sdks/rust/v*` tags against movement/deletion. Configure the
   repository **variable** `LANTERN_RUST_CRATES_IO_OWNER` with the exact
   crates.io owner login (a user or GitHub team such as `github:org:team`).
   The workflow requires this even for the first run, when the crate does not
   yet exist. Keep credentials out of the repository, GitHub Secrets, and CI.
3. Run the applicable repository quality gates and review the
   [Rust PR workflow](../../.github/workflows/rust-sdk.yml). **Once, immediately
   before authorizing the frozen release candidate**, run the applicable
   pre-release performance/benchmark qualification **locally** and retain
   the source SHA, image ID, host/platform, verdicts, and artifact hashes
   against that exact commit; use the
   [bench policy](../../testbed/bench/README.md) for relevant server/hot-path
   scenarios. One suitable local environment (ARM or x86) suffices; a second
   architecture is not required. Do not claim a local arm64 result certifies
   amd64 production performance, erase a genuine failed gate by changing
   platforms, reuse an optional hosted diagnostic as release evidence, run
   performance repeatedly during preparation, or assume the tag workflow
   runs it. A changed candidate needs fresh qualification and still requires
   new crate-owner approval.

The [release workflow](../../.github/workflows/rust-release.yml) runs **only**
on pushed `sdks/rust/v*.*.*` tags and rejects any tag that is not exactly
`sdks/rust/vX.Y.Z`. There is no PR, `workflow_run`, or manual-dispatch release
path. Its tag job compares the peeled commit and remote tag object with the
checked-out commit, package metadata, license, and exact changelog heading.
Its conformance matrix has **six native lanes**: Linux, macOS, and Windows,
each on the declared Rust MSRV (`1.88.0`) and stable. Every lane checks
generated-binding drift, formatting, all-target/all-feature Clippy, default
TLS roots, all-feature tests, and warning-free rustdoc; the stable lane on
**each OS** also builds the production Go server and runs the ignored real-wire
h2c/TLS/auth/CRUD/query failure tests. A path-filtered green PR workflow
does not substitute for this full tag matrix.

An additional single [native SDK4 lane](../../.github/workflows/current-sdk4.yml)
is mandatory for the exact tagged source. It exercises current Go, Node/Admin,
Dart and Rust over actual TLS through the existing native constructor and
qualified current quorum. Missing tools/configuration, native readiness failure,
zero execution or skipped cases fail the lane. This is the limited SDK seam,
not a repetition of the full clock/recovery campaign.
Before archive inspection, preflight downloads that same-run job's artifact by
its ID and verifies the producer's receipt SHA-256, exact tag HEAD/tree, tracked
source and runner hashes, toolchain/configuration binding and required execution
logs. An older/manual receipt or a status field alone cannot authorize release.
Publication depends on both that lane and successful archive preflight, whose
existing byte comparison binds the `.crate` to the same tag checkout.

Only after all six conformance lanes and native SDK4 pass, preflight audits
locked dependencies with
warnings denied (including unmaintained crates), builds the tagged `.crate`
with `cargo package --locked`, checks its license, version, source and
generated-code identity, verifies its **packaged** README links to the
published crates.io crate without prepublication claims, and tests the
unpacked archive independently of the Go workspace, root proto, and
system `protoc`.
Before artifact upload or the registry decision, tag-time preflight also runs
`cargo publish --dry-run --locked --package lantern-client` in a fresh target
directory, then independently reruns `cargo package` in another fresh target
and requires that archive's **SHA-256 and bytes** to match the inspected/tested
package. Both packaging and the conditional publishing job use the fixed
Rust/Cargo `1.97.1` toolchain, rather than a moving stable alias; each checks
the exact `cargo --version` before running. The dry-run does not publish, and
the location of its temporary archive is not a Cargo API; neither check
proves the bytes of a later real publish. Registry archive verification after
publication remains mandatory.
Preflight then uploads the candidate `lantern-client-X.Y.Z.crate` as a 14-day
Actions artifact and records its SHA-256, tagged commit, runner OS, and Cargo
version in the run summary. Do not publish if **any** of these
pre-publication checks fails, or if no candidate artifact was uploaded.

The registry gate uses the complete crates.io `/versions` history (including
its total and pagination marker), crate owner, checksum, and provenance to
distinguish an absent crate, its first and only published version, and later
versions. It does **not** assume `0.1.0` is necessarily the first version.
Incomplete or inconsistent registry responses fail closed.

## First publication: historical owner-only bootstrap

The first version (`0.1.0`) was published by the owner from a verified
immutable tag using owner-held credentials: crates.io cannot use trusted
publishing before a crate exists. The steps below document that completed
bootstrap for audit; **do not repeat them for later versions**. Never move
the old tag, republish a version, or manually publish a subsequent release.

1. With the authorized tag pushed, inspect its **Rust SDK Release** run. All
   six conformance lanes, the advisory audit, packaging, archive inspection,
   isolated archive tests, publish dry-run, and independent archive
   comparison must have succeeded. For an absent crate, the first run
   **intentionally fails only at preflight's registry lookup after uploading
   the candidate**. Any earlier failure is a blocker, not the expected
   bootstrap state.
2. Obtain the candidate artifact from **that exact run** (its name includes
   the commit SHA and run attempt). Check its SHA-256 against the run summary.
   On the owner host, check out the exact tag in a clean tree, confirm its
   remote tag object and peeled commit agree with the run, and use the
   pinned Rust/Cargo `1.97.1` toolchain and preferably the same Linux
   packaging environment. A `.crate` produced on another OS or with another
   Cargo version is not assumed byte-reproducible. The following checks are
   for the future owner-authorized release, not commands to run during
   preparation:

   ```bash
   set -euo pipefail
   tag=sdks/rust/v0.1.0 # historical first-publication tag
   version=${tag#sdks/rust/v}
   ci_archive="/path/to/downloaded/lantern-client-$version.crate"
   expected_sha='<SHA-256 from this Actions run summary>'
   rustup toolchain install 1.97.1 --profile minimal
   test "$(rustup run 1.97.1 cargo --version)" = 'cargo 1.97.1 (c980f4866 2026-06-30)'
   test -z "$(git status --porcelain)"
   test "$(git rev-parse HEAD)" = "$(git rev-parse "$tag^{commit}")"
   test "$(gh api "repos/anaregdesign/lantern/git/ref/tags/$tag" --jq '.object.sha')" = \
     "$(git rev-parse "refs/tags/$tag")"
   (cd sdks/rust && rustup run 1.97.1 cargo package --locked --package lantern-client)
   local_archive="sdks/rust/target/package/lantern-client-$version.crate"
   printf '%s  %s\n' "$expected_sha" "$ci_archive" | sha256sum --check --strict
   printf '%s  %s\n' "$expected_sha" "$local_archive" | sha256sum --check --strict
   cmp "$ci_archive" "$local_archive"
   ```

   **Stop before publishing if either digest check or the byte-for-byte
   comparison fails.** Resolve the difference and requalify the candidate;
   if the source must change, choose a new version/tag rather than moving this
   tag. Do not use a different artifact, weaken verification, or publish an
   unverified archive. Keep the checkout and toolchain unchanged between this
   check and the owner's manual publication. Cargo packages again during
   publish, so this pre-publish check is necessary but the registry check
   below remains mandatory.
3. Only after the owner confirms all checks may the owner publish with their
   own credentials from that verified tag using the same pinned toolchain:

   ```bash
   (cd sdks/rust && rustup run 1.97.1 cargo publish --locked --package lantern-client)
   ```

   Wait for the exact version and archive to become visible on crates.io.
   Configure the **protected**
   GitHub Environment named `crates.io` with release-tag restrictions and
   required reviewers; register its crates.io trusted publisher for repository
   `anaregdesign/lantern`, workflow file `rust-release.yml`, and environment
   `crates.io`. Then choose **Re-run all jobs** on the original tag workflow
   run (not a new tag or a manual-dispatch workflow). Its registry preflight
   must see the existing first-and-only owner-held version (whether `0.1.0` or
   a later first version) without OIDC provenance and with an
   exact candidate checksum; its protected publish job obtains a short-lived
   OIDC credential but does not republish the existing version. If registry
   propagation is incomplete, wait and rerun; if the new candidate differs
   from the published archive, stop rather than bypassing the comparison.

## Subsequent versions: tag-driven OIDC

For later approved versions, retain the protected `crates.io` Environment,
trusted-publisher binding, immutable tag rules, and owner variable. After
candidate qualification, push only the exact authorized tag. The tag,
six-lane conformance, audit, and isolated candidate gates run again. If that
version does not exist, only the protected `publish` job can request
`id-token: write`; it has `contents: read`, uses a short-lived crates.io OIDC
credential, and publishes without a long-lived registry secret. A missing or
misconfigured trusted publisher blocks publication. The ordinary validation
and registry-verification jobs are read-only.

In **both** paths, a separate read-only job waits for crates.io visibility
and compares its declared checksum and the **downloaded registry archive's
SHA-256** with the tested candidate; it also checks ownership. Later versions
must carry GitHub trusted-publisher provenance for this repository, tag's
commit, and Actions run. A mismatch, missing provenance, missing archive, or
yanked version blocks the GitHub Release. Only then may the separate
`contents: write` job (with **no** OIDC permission) create or accept a
non-draft, non-prerelease GitHub Release whose title and tag are **exactly**
`sdks/rust/vX.Y.Z`. Do not override a failed gate, force-move a tag, or
manually create a Release to bypass archive verification.

Native conformance inputs are created through the maintained Go authfixture's
bounded stdin-only private-input route. That fixture installs owner-only Unix
modes or a protected Windows owner DACL before writing any bytes. Production
readers do not bypass file security for tests. Startup diagnostics forward only
fixed categories from private logs; native Windows permission/restart and all
authenticated wire cases remain required, independently of cross-compilation.
