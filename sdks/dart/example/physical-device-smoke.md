# Physical-device smoke evidence

Physical evidence is a release prerequisite whenever the mobile or offline
contract changes. Simulator and emulator CI cannot prove physical iOS
local-network privacy, radio changes, or Android Doze behavior. Record actual
device output here; do not infer a pass from a build artifact.

## Run command

Use a LAN-reachable HTTPS endpoint and runtime token BFF:

```bash
flutter test integration_test/mobile_smoke_test.dart -d <device-id> \
  --dart-define=LANTERN_ENDPOINT=https://<trusted-lan-name>:6380
```

Run the example UI separately with `flutter run` to exercise lifecycle and
failure-state scenarios that the compact smoke does not automate.

Every current release run must record the full 40-character commit SHA (never
"working tree based on"), Flutter and Dart versions plus framework revision,
application package ID, device model/OS without a device identifier, test
target and scenarios, UTC time, network topology, and a pass/fail result. Keep
the artifact content-free: no endpoint, token, partition/key/value, certificate,
device identifier, or raw server trace. The Android/iOS CI jobs emit the same
shape for emulator/simulator runs and bind it to `GITHUB_SHA`; those artifacts
prove reproducibility but do not count as physical-device evidence.

Run from a clean checkout and capture `git rev-parse HEAD` before the test. The
sanitized artifact uses this minimum JSON shape (one file per platform):

```json
{
  "schema": 1,
  "kind": "physical_mobile_revision_evidence",
  "contentFree": true,
  "physicalDevice": true,
  "repository": "anaregdesign/lantern",
  "commit": "<40-character SHA>",
  "recordedAt": "<UTC RFC 3339>",
  "toolchain": {
    "flutter": "<version>",
    "flutterRevision": "<40-character revision>",
    "dart": "<version>"
  },
  "application": {
    "packageId": "<checked-in Android or iOS package ID>",
    "target": "integration_test/mobile_smoke_test.dart"
  },
  "platform": {
    "kind": "<physical-android or physical-ios>",
    "model": "<model>",
    "os": "<version without a device identifier>"
  },
  "network": {
    "transport": "Connect/HTTPS",
    "topology": "<sanitized LAN or trusted-tunnel description>",
    "authenticated": true
  },
  "scenarios": [
    "online_exact_values",
    "offline_put_replay",
    "authoritative_server_expiry",
    "watch_cleanup",
    "wipe_before_send",
    "sqlite_pending_close_reopen",
    "sqlite_original_ttl_preserved",
    "sqlite_expired_before_replay",
    "sqlite_logout_wipe_reopen",
    "sqlite_partition_isolation"
  ],
  "result": "passed"
}
```

Reject the record if the checkout is dirty, the SHA/toolchain/package does not
match the tested binary, or any required scenario did not pass. Never replace
the placeholders with an endpoint or identifier.

When Flutter classifies a cabled iOS device as wirelessly tethered, use the
checked-in host driver with `flutter drive --publish-port`,
`--driver=test_driver/integration_test.dart`, and
`--target=integration_test/mobile_smoke_test.dart`. If mDNS discovery is
unavailable, build the same integration-test target in profile mode, install it
with `xcrun devicectl device install app`, and launch it with CoreDevice. Keep
only a sanitized content-free RPC category/status summary as real-wire evidence;
do not attach the raw server trace.

The `mobile_smoke_test.dart` target also writes a content-free result to its
app data container at `tmp/lantern-mobile-smoke-result.json`. Use the
CoreDevice `device info files` and `device copy from` commands shown in the
identity CDC section below, substituting that file name. A fresh `passed`
marker with `phase: complete` means the test body and its registered cleanup
finished; a missing, stale, `running`, or `failed` marker does not qualify the
run. Match the installed binary and trusted HTTPS route to the evidence record.

## Required matrix

| Scenario | Physical Android | Physical iOS |
| --- | --- | --- |
| Platform-trusted TLS succeeds | Passed, 2026-07-18 UTC | Passed, 2026-07-18 UTC |
| Untrusted/hostname-mismatched TLS fails closed | Passed, 2026-07-18 UTC | Passed, 2026-07-18 UTC |
| Missing and rotated short-lived token | Passed, 2026-07-18 UTC | Passed, 2026-07-18 UTC |
| Airplane/offline, then resume and explicit refetch | Passed, 2026-07-18 UTC | Passed, 2026-07-18 UTC |
| Background/Doze-like pause, then resume/refetch | Passed, 2026-07-18 UTC | Passed, 2026-07-18 UTC |
| Navigation cancels page and incremental-search work | Passed, 2026-07-18 UTC | Passed, 2026-07-18 UTC |
| Large cursor pages and partial batch failure stay bounded | Passed, 2026-07-18 UTC | Passed, 2026-07-18 UTC |
| Add retry preserves exactly one contribution | Passed, 2026-07-18 UTC | Passed, 2026-07-18 UTC |
| Every Vertex oneof renders exactly | Passed, 2026-07-18 UTC | Passed, 2026-07-18 UTC |
| iOS local-network privacy prompt/denial/retry | N/A | Passed, 2026-07-18 UTC |

For each completed column, add the UTC date, device/OS, Flutter revision,
Lantern revision, network topology, commands, and sanitized logs. Never include
tokens, private keys, device identifiers, or user data.

## v0.1.0 recorded environment

- UTC date: 2026-07-18.
- Android: Pixel 9a, Android 17 (API 37).
- iOS: iPhone 16 Pro, iOS 26.5.2 (23F84).
- Host: MacBook Pro, macOS 26.5.2; Ethernet on the phones' private LAN.
- Flutter: 3.44.6, Dart 3.12.2, framework revision
  `ee80f08bbf97172ec030b8751ceab557177a34a6`.
- Lantern: `31537f49d16c1db3230fae6fc992a7ac5833b058`.
- Topology: both phones used Wi-Fi on the host's router. Platform-trusted HTTPS
  used an ephemeral public-CA Cloudflare Quick Tunnel to a local authenticated
  Lantern server. Local-network privacy and lifecycle tests used the host's LAN
  address. Runtime credentials were synthetic, short-lived test values.

## Android evidence

Representative sanitized commands:

```bash
flutter test integration_test/mobile_smoke_test.dart -d <pixel-id> --no-pub \
  --dart-define=LANTERN_ENDPOINT=https://<public-ca-tunnel>

flutter test integration_test/physical_api_matrix_test.dart -d <pixel-id> \
  --plain-name '<one physical API contract>' --no-pub \
  --dart-define=LANTERN_ENDPOINT=https://<public-ca-tunnel> \
  --dart-define=LANTERN_OLD_TOKEN=<synthetic-old-token> \
  --dart-define=LANTERN_NEW_TOKEN=<synthetic-new-token>

flutter test integration_test/physical_ui_matrix_test.dart -d <pixel-id> \
  --no-pub --dart-define=LANTERN_ENDPOINT=http://<host-lan-address>:<auth-port> \
  --dart-define=LANTERN_TOKEN_ENDPOINT=http://<host-lan-address>:<bff-port>/token \
  --dart-define=LANTERN_TOKEN=<synthetic-token> \
  --dart-define=LANTERN_ALLOW_INSECURE=true
```

Sanitized observations:

```text
All tests passed!  # mobile smoke
All tests passed!  # missing -> old -> new runtime token rotation
All tests passed!  # all 13 Vertex oneofs preserve exact values
All tests passed!  # 75 items in three bounded cursor pages; partial failure explicit
All tests passed!  # two committed-response-loss retries; final edge weight = 2
UI_MATRIX discovery ready
UI_MATRIX exact values visible
UI_MATRIX CRUD ready
UI_MATRIX incremental search ready
UI_MATRIX traversal families ready
UI_MATRIX navigation cancellation ready
All tests passed!
```

The platform-trusted tunnel completed every RPC. The untrusted-certificate run
failed with `CERTIFICATE_VERIFY_FAILED` and no accepted RPC. Airplane mode made
the LAN `Network is unreachable`; after restoring Wi-Fi, token fetch and
`ScanVertexKeys` / `ScanVertices` succeeded. A real screen-off forced Doze
reported `IDLE`; after `unforce`, unlock, and foreground resume, both scans
again completed with `grpc.code=ok`. Airplane mode, Wi-Fi, Private DNS, and the
device VPN were restored after testing.

## iOS evidence

Representative sanitized commands:

```bash
flutter test integration_test/local_network_denied_test.dart -d <iphone-id> \
  --no-pub --dart-define=LANTERN_ENDPOINT=http://<host-lan-address>:<open-port>

flutter build ios --profile --no-pub \
  --target=integration_test/mobile_smoke_test.dart \
  --dart-define=LANTERN_ENDPOINT=http://<host-lan-address>:<open-port> \
  --dart-define=LANTERN_ALLOW_INSECURE=true
xcrun devicectl device install app --device <iphone-id> \
  build/ios/iphoneos/Runner.app
xcrun devicectl device process launch --device <iphone-id> \
  --terminate-existing com.anaregdesign.lanternExample
```

Sanitized observations:

```text
LOCAL_NETWORK_DENIED SocketException: No route to host (errno = 65)
All tests passed!
PutVertices                  grpc.code=ok  # 13 Vertex oneofs
GetVertices                  grpc.code=ok
AddEdges                     grpc.code=ok
ScanVertexKeys               grpc.code=ok
Illuminate family=bfs        grpc.code=ok
TLS handshake error from <iphone-lan-address>: EOF  # self-signed endpoint
```

The public-CA HTTPS API matrix passed all four contracts in one physical run:
missing/rotated runtime tokens, exact 13-oneof round-trip, 75-item bounded
cursor paging plus explicit partial failure, and committed Add response loss
with one final contribution. The physical UI matrix passed runtime-token BFF,
lossless `uint64` / bytes / duration rendering, CRUD, incremental search,
BFS/PPR/community, edge expiration, and navigation cancellation.

With Local Network permission off, the SDK failed closed with `No route to
host`; after the system prompt was allowed, the complete mobile smoke RPC set
completed with `grpc.code=ok`. A dedicated self-signed TLS test accepted only a
cause containing `CERTIFICATE_VERIFY_FAILED`; the server observed an immediate
handshake EOF and zero RPCs. A real Home gesture followed by reopening the app
triggered successful scans. Airplane mode produced the bounded
`retryExhausted` UI state and zero server RPCs; after restoring Airplane mode,
Wi-Fi, and foreground state, `ScanVertexKeys`, `ScanVertices`, and
`TopVerticesByDegree` completed with `grpc.code=ok`.

## Offline Repository physical validation

The opt-in `lantern_client_offline` flow was rerun after its integration into
the maintained mobile smoke.

- UTC date: 2026-07-22.
- Device: iPhone 16 Pro, iOS 26.5.2 (23F84).
- Flutter: 3.44.6, Dart 3.12.2, framework revision
  `ee80f08bbf97172ec030b8751ceab557177a34a6`.
- Lantern: offline Epic working tree based on
  `da543e0a57b538a8429d1fc0e04dd122b6a6c625`.
- Topology: signed debug app on the physical iPhone, private LAN to the
  development Lantern listener, synthetic values, and explicit debug-only
  insecure transport.

Sanitized command:

```bash
flutter test --no-pub integration_test/mobile_smoke_test.dart \
  -d <physical-iphone-id> --reporter=expanded --timeout=3m \
  --dart-define=LANTERN_ENDPOINT=http://<host-lan-address>:6380 \
  --dart-define=LANTERN_ALLOW_INSECURE=true
```

Sanitized result:

```text
MOBILE_SMOKE_PASS vertices=13 edge=1 scan=true bfs=true \
offline_cache=true offline_replay=true
All tests passed!
```

This recorded run predates the Put-only amendment in #1175 and is retained only
as historical lifecycle/probe evidence; it does not validate the current
offline mutation contract. The maintained mobile smoke now locally commits
`PutVertex` and `PutEdge`, observes their exact pending cache values, then probes
and drains. It also proves server-authoritative expiration under a deliberately
behind device clock, releases a watch, and wipes an unsent Put with zero remote
mutation. That revised scenario must be rerun from the exact #1162 candidate SHA
on a physical device before it is recorded as current release evidence. The
historical run used `InMemoryOfflineStore`. The current example and native
smoke use `SqliteOfflineStore`, including pending Put close/reopen, original
TTL, durable logout wipe, and partition isolation. None of those native SQLite
scenarios are validated by the historical run above. A new exact-revision
physical run is required; host-side process-crash tests and simulator runs
remain separate evidence.


## Native SQLite process restart probe

`integration_test/sqlite_restart_probe.dart` is an opt-in application entrypoint
for physical process termination. Build once in profile mode with a fresh
16–64-character lower-case hex `LANTERN_SQLITE_RESTART_PROBE_RUN`, install once,
and launch the same binary three times. Each run owns a separate fixture
subdirectory; it never opens or clears the example's application database.

1. Wait for `SQLITE_RESTART_PROBE_READY1`, then force-stop/kill the process
   without clearing application data. Pending Vertex/Edge work and confirmed
   cache have committed; no graceful database close is performed.
2. Launch the same installed binary. Wait for `SQLITE_RESTART_PROBE_READY2`,
   then kill again. This phase verifies pending values/status and the original
   absolute TTL, wipes one user partition, and checks the sibling partition.
3. Launch again and require `SQLITE_RESTART_PROBE_PASS`. The final phase verifies
   durable wipe, generation isolation, absent old-user overlays, and preserved
   sibling pending work. Different process IDs are required between phases;
   hot restart and reinstall are not valid evidence.

Record the exact clean checkout SHA, toolchain, device model/OS, installed binary
hash, kill method and the three content-free markers. This offline probe proves
storage behavior only; the normal `mobile_smoke_test.dart` separately verifies
real-server replay. The smoke optionally obtains its synthetic test token at
runtime from `LANTERN_TOKEN_ENDPOINT`; plaintext fixture endpoints require
explicit `LANTERN_ALLOW_INSECURE=true`. Private-LAN h2c evidence must say so and
must not be described as platform-trusted HTTPS or full release qualification.


## 2026-09-24 SQLite qualification

Current SQLite-specific physical evidence is recorded under
[`evidence/2026-09-24-sqlite/`](evidence/2026-09-24-sqlite/README.md), bound to the
exact tested commit and binary hashes. Both Android and iOS passed actual
process-kill/relaunch, pending TTL, durable logout wipe, and local user-partition
isolation. The evidence records its transport and local build limitations and
does not replace the complete first-publication matrix in #1162.

## Identity CDC physical qualification

`integration_test/physical_identity_cdc_test.dart` is an opt-in native Android/iOS
test of the production `LanternClientIdentitySource` and platform SQLite. Run it
from a clean checkout with an authenticated endpoint that routes Subscribe,
GetReplicationStatus, GetVertices, and GetEdges to one real responder. The
operator must establish that route; the stream frame cannot prove it. Use a
platform-trusted HTTPS endpoint and a runtime token BFF that issues a distinct
valid token on the test's post-checkpoint refresh. The responder must accept
both issued tokens during the run. For release evidence:

```bash
flutter test --no-pub integration_test/physical_identity_cdc_test.dart \
  -d <physical-android-id> --reporter=expanded --timeout=3m \
  --dart-define=LANTERN_ENDPOINT=https://<pinned-responder> \
  --dart-define=LANTERN_TOKEN_ENDPOINT=https://<token-bff>/token \
  --dart-define=LANTERN_OFFLINE_CDC_PINNED_RESPONDER=true

flutter test --no-pub integration_test/physical_identity_cdc_test.dart \
  -d <physical-ios-id> --reporter=expanded --timeout=3m \
  --dart-define=LANTERN_ENDPOINT=https://<pinned-responder> \
  --dart-define=LANTERN_TOKEN_ENDPOINT=https://<token-bff>/token \
  --dart-define=LANTERN_OFFLINE_CDC_PINNED_RESPONDER=true
```

For a trusted-LAN development fixture only, both URLs may use HTTP with
`--dart-define=LANTERN_ALLOW_INSECURE=true`; record that limitation and do not
claim platform-trusted TLS. On iOS, use the checked-in `flutter drive`
integration driver with `--publish-port` if the device is classified as
wirelessly tethered. The test requires `IDENTITY_CDC_BODY_STARTED`,
`IDENTITY_CDC_PASS`, and `All tests passed!`. It verifies checkpoint recovery
of stale Vertex and Edge residents, exact live invalidation, durable cursor and
Unknown state after SQLite reopen, foreground resume, responder stability,
runtime token acquisition and post-checkpoint refresh, and partition wipe
cancellation.

If wireless Flutter VM-service discovery stalls after the iOS app installs,
build this same integration target in profile mode and launch it with
CoreDevice. The target writes a content-free JSON result to its app data
container at `tmp/lantern-identity-cdc-result.json`; the result contains only
`running`/`passed`/`failed`, the current test phase, UTC timestamps, and a
bounded failure category. It contains no endpoint, token, key, responder, or
device identifier. Locate and copy it after launch:

```bash
xcrun devicectl device info files --device <physical-ios-id> \
  --domain-type appDataContainer \
  --domain-identifier com.anaregdesign.lanternExample \
  --search lantern-identity-cdc-result.json
xcrun devicectl device copy from --device <physical-ios-id> \
  --domain-type appDataContainer \
  --domain-identifier com.anaregdesign.lanternExample \
  --source tmp/lantern-identity-cdc-result.json \
  --destination <private-host-result-path>
```

Only a fresh marker whose `startedAt` is after this launch and whose `status`
is `passed` with `phase: complete` demonstrates that the test body and its
registered cleanup completed. A missing, stale, `running`, or `failed` marker
is not a pass. The marker supplements the exact-binary, trusted-HTTPS, and
sanitized RPC evidence required above; it does not qualify a different app
build or network route.

For a cabled Android development run when the LAN route is unstable, forward
the fixture ports over USB and use device loopback. Configure the local BFF to
rotate between two test tokens accepted by the server. This proves native
SQLite and CDC behavior, but does not qualify the release network/TLS contract:

```bash
adb -s <physical-android-id> reverse tcp:6380 tcp:6380
adb -s <physical-android-id> reverse tcp:6381 tcp:6381
flutter test --no-pub integration_test/physical_identity_cdc_test.dart \
  -d <physical-android-id> --reporter=expanded --timeout=3m \
  --dart-define=LANTERN_ENDPOINT=http://127.0.0.1:6380 \
  --dart-define=LANTERN_TOKEN_ENDPOINT=http://127.0.0.1:6381/token \
  --dart-define=LANTERN_OFFLINE_CDC_PINNED_RESPONDER=true \
  --dart-define=LANTERN_ALLOW_INSECURE=true
adb -s <physical-android-id> reverse --remove tcp:6380
adb -s <physical-android-id> reverse --remove tcp:6381
```

Record each physical run against the exact clean code SHA, Flutter revision,
device model/OS, installed binary SHA-256, authenticated network topology, and
sanitized markers. Keep endpoints, tokens, device identifiers, and graph keys
out of evidence. This dedicated target has its own binary and evidence record;
the offline release gate checks it separately from `mobile_smoke_test.dart`.

## Offline core publication matrix

For the repeatable clean-checkout, temporary HTTPS fixture, per-device test,
and teardown sequence, use the
[offline release resume runbook](offline-release-resume.md).

Before an `sdks/dart/offline/vX.Y.Z` tag, test a clean code commit on both
physical platforms using a platform-trusted HTTPS endpoint. Record the
`mobile_smoke_test.dart` runs as `android.json` and `ios.json`, and the
`physical_identity_cdc_test.dart` runs as `android-cdc.json` and
`ios-cdc.json`, under `evidence/offline-release/`. The smoke records use
`kind: physical_offline_release_evidence`; the CDC records use
`kind: physical_offline_identity_cdc_evidence`. Each file must set `schema: 1`,
`contentFree: true`,
`physicalDevice: true`, `cleanCheckout: true`, `repository:
anaregdesign/lantern`, `testedCommit` to the full tested code SHA,
`recordedAt` to a UTC timestamp, and `result: passed` with empty `limitations`.
Include the exact Flutter/Dart versions and Flutter framework revision, the
platform package ID and each target's own installed binary SHA-256, device
model/OS without an identifier, and `network` with `transport: Connect/HTTPS`,
authenticated and platform-trusted TLS both true, plus a sanitized topology
description. Set `application.target` to the corresponding integration test.

The smoke `scenarios` array must contain the ten native smoke scenarios listed in
the example above, plus `platform_trusted_tls`, `untrusted_tls_rejection`,
`token_rotation`, and `radio_offline_foreground_recovery`. Android also needs
`android_doze_like_pause`; iOS also needs
`ios_local_network_privacy_denial_retry`. The CDC array must contain
`identity_checkpoint_revalidation`, `identity_live_vertex_invalidation`,
`identity_live_edge_invalidation`, `identity_cursor_persisted`,
`identity_unknown_survives_sqlite_reopen`,
`identity_resume_live_invalidation`, `identity_partition_wipe`,
`identity_same_responder`, `identity_runtime_token_refresh`, and
`identity_cancellation`. Each record must describe actual observed passes,
not planned work. Keep endpoints, IP addresses, certificates, tokens, device
identifiers, and raw traces out of the files.

Commit **only** these evidence files (and an optional README in the same
directory) as the immediate child of the tested code commit. Tag that child.
The release gate requires its parent to equal all four `testedCommit` fields and
rejects every other changed path. It also compares toolchain/package identity
to the tag's Android/iOS simulator manifests from the current workflow attempt.
This proves the tagged code is
the exact code tested on devices while allowing the evidence to be checked in
without a self-referential Git SHA.
