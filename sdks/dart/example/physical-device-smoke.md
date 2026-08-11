# Physical-device smoke evidence

This evidence is a release prerequisite for `lantern_client` v0.1.0. Simulator
and emulator CI cannot prove physical iOS local-network privacy, radio changes,
or Android Doze behavior. Record actual device output here; do not infer a pass
from a build artifact.

## Run command

Use a LAN-reachable HTTPS endpoint and runtime token BFF:

```bash
flutter test integration_test/mobile_smoke_test.dart -d <device-id> \
  --dart-define=LANTERN_ENDPOINT=https://<trusted-lan-name>:6380
```

Run the example UI separately with `flutter run` to exercise lifecycle and
failure-state scenarios that the compact smoke does not automate.

When Flutter classifies a cabled iOS device as wirelessly tethered, use the
checked-in host driver with `flutter drive --publish-port`,
`--driver=test_driver/integration_test.dart`, and
`--target=integration_test/mobile_smoke_test.dart`. If mDNS discovery is
unavailable, build the same integration-test target in profile mode, install it
with `xcrun devicectl device install app`, and launch it with CoreDevice. Keep
the server trace as the real-wire evidence.

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
and drains. That revised scenario must be rerun on a physical device before it
is recorded as current release evidence. The example uses
`InMemoryOfflineStore`; process-restart durability remains covered by the
storage-neutral fresh-process, Put response-loss, legacy-Add quarantine, and
adapter conformance tests rather than being claimed from this device run.
