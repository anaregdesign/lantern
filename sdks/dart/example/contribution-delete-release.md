# Contribution Delete Dart release qualification

Issue [#1530](https://github.com/anaregdesign/lantern/issues/1530) adds the online
`lantern_client 0.4.1` contribution Delete family. The offline outbox remains
unsupported for this operation; no new durable offline intent is included.
The public SDK exposes `EdgeContributionRef`, plural-canonical direct and
receipt-bearing Delete, distinct `EdgeContributionDeleteReceipt` originals,
and identity CDC `deleteEdgeContribution`. A CDC frame invalidates its edge
pair: re-fetch the effective edge instead of interpreting it as whole-edge
Delete. Natural TTL expiration does not synthesize a CDC event.

Before Add, persist an explicit nonzero 24-byte `EdgeInput.contribId`; use that
same ID to construct the Delete target. Automatically generated IDs and folded
graph-only backups cannot reconstruct old identities. Removal obeys the D4
retention boundary in `docs/replication.md`: it preserves other Add rows and
any Put base and does not promise an eternal tombstone. A plain lost response
is ambiguous; receipt status `notYetObserved` or `noLongerProvable` does not
permit a blind cross-endpoint resend.

## Source and device contract

Merge source, tests and release preparation first. Freeze one clean exact
`main` commit and use it for physical Android and iPhone. The dedicated signed
profile target is `integration_test/physical_contribution_delete_test.dart`.
Its fixed eleven-scenario matrix tests authenticated platform-trusted HTTPS,
invalid IDs, index-aligned duplicates/missing/expired IDs, Put-base retention,
CDC edge re-fetch, offline unsupported-family rejection, persisted dispatch
marker before send, actual committed-response socket drop, genuine SIGKILL and
same-installed-binary relaunch, status-first no-resend and original true/false
receipt replay. A host/simulator run or a rebuilt/reinstalled app is insufficient.

Use the private runtime BFF and five HTTPS defines described in
[receipt-attestation.md](receipt-attestation.md). Start the existing proxy with
`LANTERN_CONTRIBUTION_PROXY=true`; its allowed mutation/drop family becomes
only `DeleteEdgeContributions`. Give each platform/build a fresh 32-hex
`LANTERN_RECEIPT_RUN_ID` and compile the same `LANTERN_TESTED_COMMIT`. Keep
addresses/tokens/config outside Git and record the immutable server image or
binary hash. Preflight reachability, 401 without auth and successful authorized
RPC, synthetic state and physical device identity before each run.
The born-expired seed uses a fixed past UTC timestamp and verifies its effective
weight before Delete. A device-relative one-second offset is insufficient when
device and server clocks differ; application TTL timestamps remain caller-owned.
Identity CDC preserves repeated edge pairs for duplicate request positions;
the fixture verifies both invalidations and re-fetches the effective edge.

Build once and install the exact signed profile APK or Runner.app. Launch the
app and wait for its content-free `lantern-contribution-phase.json` to say
`sigkill_now` and `lantern-receipt-attestation.json` to say `awaiting_sigkill`.
Use a real signal 9, prove the process exited, then relaunch the same install
without clearing app data. The second process must write schema 2,
`passed/complete` with all eleven scenarios and a changed-process restart proof.
The journal is app-private and must never be published.

Capture and validate the paired marker/record within 30 minutes using
`offline/tool/physical_receipt_attestation.py`, the target above and each fixed
scenario independently. The content-free record uses kind
`physical_edge_contribution_delete_evidence`; its schema/network/application
fields otherwise follow the receipt runbook. Require installed APK SHA-256 or
iOS `App.framework/App` SHA-256 to equal the exact host build. Re-read and
hash-verify signed originals, record, marker and private operator transcript
in an owner-controlled mode-700 directory on the FileVault volume, retaining
them through tag Gate and hosted archive equality. Restore device settings
and stop the disposable fixture afterwards. Remove private originals after
verification; discard and rerun evidence older than 30 days.

## Tag and publication

After both captures pass, commit only `android.json`, `android-marker.json`,
`ios.json` and `ios-marker.json` under `example/evidence/contribution-delete/`
as the immediate evidence-only child of the tested source. Before pushing
its unused `sdks/dart/v0.4.1` tag, run the complete local quality gate and
`scripts/physical_contribution_release_gate.py --tag sdks/dart/v0.4.1`.
The tag's full Dart/native Gate and read-only parent preflight enforce the
same source, exact scenario sets and physical records. Publication is the
existing protected pub.dev OIDC workflow; package-admin approval remains
human-owned. Verify actual hosted archive equality before the exact-title
GitHub Release and before closing #1530. Artifact publication is not deployment.

The pushed `sdks/dart/v0.4.0` tag remains immutable and unpublished: its full
Gate passed, but a skipped optional ancestor suppressed release preflight.
The 0.4.1 workflow explicitly requires a successful direct upstream result and
an uncanceled run. Manual dispatch and PRs remain test-only. A green aggregate
Gate alone does not prove publication; require actual preflight, protected OIDC,
published-archive equality and exact-title Release job success.
