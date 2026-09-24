# Lantern Flutter example

This maintained Android/iOS app demonstrates the mobile-facing
`lantern_client` API without choosing a state-management package. It compiles
against the parent package by path, so CI catches example/API drift.
The maintained example app targets iOS 15.0 or later; this app setting does not
define a minimum OS for the pure-Dart SDK.

## Runtime configuration

No credential is stored in source or passed through `--dart-define`. Configure
only locations, a non-secret account scope, and the explicit local-development
transport switch:

```bash
flutter run \
  --dart-define=LANTERN_ENDPOINT=https://lantern.example.com \
  --dart-define=LANTERN_TOKEN_ENDPOINT=https://bff.example.com/mobile-token \
  --dart-define=LANTERN_OFFLINE_SCOPE=demo-user
```

The token endpoint is called asynchronously for each transport attempt
(including retries) and must return JSON shaped as
`{"access_token":"short-lived-user-or-device-token"}`.
The example never embeds a shared `LANTERN_AUTH_TOKENS` value. A public app
should obtain user/device-scoped, short-lived credentials through a BFF or
gateway; the server's static token list is an operator-side deployment input.
`LANTERN_OFFLINE_SCOPE` is required with a token endpoint and must identify
the user/tenant whose state is stored locally. It is never an access token.
Anonymous local development uses an `anonymous` scope.

The Offline screen starts foreground identity CDC only when
`--dart-define=LANTERN_OFFLINE_CDC_PINNED_RESPONDER=true` is set. Set it only
when `LANTERN_ENDPOINT` routes every Subscribe, status, and plural read to one
real Lantern responder for the session. An ordinary load-balancing URL is not
sufficient. The example never infers responder pinning from a URL or starts CDC
in the background. Opening the Offline screen starts one session; hide/pause or
navigation cancels it, and resume starts a new session only while the account
remains active. `wipeOnLogout()` blocks new sessions before quiescing and wiping
the old partition.

Plaintext requires both the SDK opt-in and a debug/trusted-LAN build:

```bash
flutter run \
  --dart-define=LANTERN_ENDPOINT=http://10.0.2.2:6380 \
  --dart-define=LANTERN_ALLOW_INSECURE=true
```

The Android debug manifest permits cleartext for this workflow. The main
manifest does not opt release builds into cleartext.

## Host addressing

| Target | Development endpoint | Notes |
| --- | --- | --- |
| Android emulator | `http://10.0.2.2:6380` | `10.0.2.2` aliases the development host. |
| iOS simulator | `http://127.0.0.1:6380` | The simulator normally reaches the Mac loopback directly. |
| Physical Android/iOS | `https://<LAN-address>:6380` | Bind Lantern to a reachable interface, keep devices on the same trusted network, and use a certificate valid for the address/name. iOS may show the local-network privacy prompt. |

Plaintext is for debug on a trusted LAN only. Production uses HTTPS and
platform trust. For a private CA or mTLS, inject a dedicated `HttpClient`
created with a narrowly configured `SecurityContext`; never install a global
`badCertificateCallback` and never accept every certificate. Low-level Dart
sockets do not uniformly inherit every ATS or Android network-security rule,
so `LanternClient` independently rejects non-HTTPS endpoints unless
`allowInsecure` is explicit.

Private PKI and mTLS stay application-owned through the SDK injection point:

```dart
final context = SecurityContext(withTrustedRoots: true)
  ..setTrustedCertificatesBytes(privateCaPem)
  ..useCertificateChainBytes(deviceCertificatePem)
  ..usePrivateKeyBytes(devicePrivateKeyPem);
final client = LanternClient.connect(
  Uri.parse('https://lantern.internal'),
  httpClientFactory: () => HttpClient(context: context),
);
```

Load key material from platform-secure runtime storage; do not bundle the
placeholder byte variables shown above.

## Troubleshooting

| Symptom | Check |
| --- | --- |
| Android emulator refuses host loopback | Use `10.0.2.2`, not `127.0.0.1`, for the host service. |
| iOS physical device cannot see the LAN server | Confirm the privacy prompt/Settings permission, same network, listener bind address, firewall, and certificate SAN. |
| `LanternInvalidArgumentException` before I/O | HTTPS is required unless the explicit debug-only insecure flag is true; also check TTL and query option domains. |
| `LanternUnauthenticatedException` | Refresh the user/device credential at the BFF; never fall back to a compiled shared token. |
| `LanternUnavailableException` / retry exhausted | Check the actual endpoint, DNS/gateway, radio state, and server health. Network-type reachability alone is not proof. |
| TTL appears early or late | Relative TTL is converted using the device clock once per logical call; inspect clock synchronization or use an absolute server-agreed instant. |
| Results grow memory | Consume bounded pages, prefer keys-only scans, cancel on navigation, and cap the visible model as this example does. |

## What the screen demonstrates

- app/signed-in-session ownership of one `LanternClient`;
- async runtime token refresh, bounded retry, and idempotent additive writes;
- Vertex/Edge Put, Get, Add, Delete, relative TTL, and exact uint64 values;
- keys-only and value cursor pages with a 200-row visible-memory ceiling;
- latest-query-wins incremental search and cold-start degree ranking;
- BFS, PPR, and local-community navigation with complete Edge expiration;
- typed loading, empty, unauthenticated, unavailable, timeout,
  retry-exhausted, and unexpected-error UI states;
- `AppLifecycleListener` cancellation on hide/pause and explicit refresh on
  resume;
- an opt-in `lantern_client_offline` screen with immediate cached snapshots,
  locally committed Put pending state, explicit probe/replay, and authorized
  dead-letter inspect/retry/delete controls, plus an independently opt-in
  foreground identity CDC session. Durable Add is intentionally absent until
  #1115 provides server-authoritative operation receipts.

The app deliberately does not close its app-scoped client on `inactive`, which
can be caused by a phone call or system dialog. It cancels screen work on
hide/pause, re-fetches freshness-sensitive data on resume, and closes the
client only when its owner is disposed. Do not rely on a termination callback:
iOS may suspend shortly after backgrounding, and Android Doze can stop network
access. The standard `lantern_client` provides no implicit offline cache,
background sync, or delivery guarantee. The example's opt-in offline Repository
uses `SqliteOfflineStore` from the experimental `lantern_client_offline_sqlite`
package. It opens a database in the platform app database directory, with a
filename derived from the endpoint and account scope. Pending Put mutations and
their original absolute TTL survive close/reopen and process termination.
Shutdown awaits Repository disposal before closing SQLite, then closes the
client. No database is closed during transient `inactive` or background states.

For logout, await `OfflineDemoSession.wipeOnLogout()` before discarding that
session's credentials; a later session must use its own scope. Closing a session
alone preserves its offline work. This fixture uses normal sqflite storage,
which is **not encrypted**. Production applications own encryption, secure key
storage, OS backup/file-protection policy, account binding, and logout triggers.

## Checks

```bash
flutter pub get
dart format --output=none --set-exit-if-changed lib test integration_test
flutter analyze
flutter test
flutter build apk --debug
flutter build ios --debug --no-codesign
```

The native real-wire smoke is in
`integration_test/mobile_smoke_test.dart`; it uses the native sqflite plugin and
covers close/reopen of pending Vertex and Edge Puts, original TTL preservation,
expiry without sending, probe-gated real-wire replay, confirmation, authoritative
server expiry under a behind-device clock, watch cleanup, and logout wipe that
survives reopen while another partition remains intact. It also covers the direct
online surface. This close/reopen smoke is distinct from the adapter's host-side
process-crash tests. Hosted Android/iOS jobs attach content-free evidence manifests
to the exact tested commit and toolchain; they are simulator evidence only.
See
[physical-device-smoke.md](physical-device-smoke.md) for the required device
matrix and evidence format.

Platform references:
[Flutter lifecycle](https://api.flutter.dev/flutter/dart-ui/AppLifecycleState.html),
[AppLifecycleListener](https://api.flutter.dev/flutter/widgets/AppLifecycleListener-class.html),
[Android emulator networking](https://developer.android.com/studio/run/emulator-networking-address),
[Android Doze](https://developer.android.com/training/monitoring-device-state/doze-standby),
[Apple background execution](https://developer.apple.com/documentation/uikit/preparing-your-ui-to-run-in-the-background),
and [iOS local-network privacy](https://developer.apple.com/documentation/technotes/tn3179-understanding-local-network-privacy).
