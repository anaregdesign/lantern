# Lantern Flutter example

This maintained Android/iOS app demonstrates the mobile-facing
`lantern_client` API without choosing a state-management package. It compiles
against the parent package by path, so CI catches example/API drift.
The maintained example app targets iOS 15.0 or later; this app setting does not
define a minimum OS for the pure-Dart SDK.

`Seed CRUD` runs once per discovery screen session and keeps a separate list of
confirmed steps, including when a later data/search request fails or the visible
data is refreshed. A failed write can have an uncertain outcome; inspect it
before starting a new screen session and replaying additive operations.
Native mode uses the Server-returned `users:<uid>:` personal prefix for data,
search and offline keys. The alternate token-endpoint demo uses `flutter-demo:`
and the Principal's existing data and query permissions.
`Server search info` is a separate optional operation requiring
`operations.read`; it also demonstrates phrase search when positions are enabled.
Its failure never grants permissions or changes the recorded CRUD result.

## Native Google and Apple login

Current source has an opt-in native login screen using exact app dependencies
`google_sign_in 7.2.0`, `sign_in_with_apple 8.2.0` and
`flutter_secure_storage 11.2.0`. Server current-v2 is required. Sign-in uses an
existing exact `(issuer, subject)` Principal and its explicit Roles. The separate
Register action requests a UID and personal Role only when the operator has
enabled that exact provider profile in the default-disabled registration policy.
There is no email linking, BFF, provider API scope request or background refresh.

```bash
flutter run \
  --dart-define=LANTERN_ENDPOINT=https://lantern.example.com \
  --dart-define=LANTERN_NATIVE_LOGIN=true \
  --dart-define=LANTERN_GOOGLE_PROFILE=google_mobile \
  --dart-define=LANTERN_APPLE_PROFILE=apple_ios
```

Use the registered Android web profile (for example `apple_android`) on Android.
Omit a provider profile to hide its button. Do not combine native mode with
`LANTERN_TOKEN_ENDPOINT` or `LANTERN_ALLOW_INSECURE`. The account partition comes
from the verified endpoint, issuer, subject and client profile; it does not use
the manually entered offline scope. The Server's `service_origin` must equal the
HTTPS endpoint origin. See [Server setup](../../../docs/oidc-operations.md#native-client-profiles).

Provider registration remains operator-owned and is not performed by this app:

- Google: use a backend **Web** client ID as the Server profile's `client_id`
  (`serverClientId`); register the actual Android package/signing certificate and
  iOS Bundle ID clients. On iOS supply the registered iOS client ID with
  `LANTERN_GOOGLE_IOS_CLIENT_ID`, and add its `REVERSED_CLIENT_ID` as a URL scheme
  in Runner's Info.plist (`CFBundleURLTypes` / `CFBundleURLSchemes`). These are
  public identifiers, never client secrets. The app initializes the singleton
  once with `nonce: null`, supports explicit lightweight/restored sign-in, and
  reuses valid Google ID tokens only according to the Server's exact audience/
  optional `azp` policy. Changing the backend client requires an app restart.
- Apple iOS: use the registered Bundle/App ID as `client_id`. Enable Sign in
  with Apple for that registered App ID and its signing profile; select
  `Runner/NativeLogin.entitlements` in Runner's `CODE_SIGN_ENTITLEMENTS` for the
  provider-enabled build. The file is supplied but is not activated for ordinary
  unsigned example builds. No web redirect URI belongs to this native profile.
- Apple Android: use a registered **Services ID**, associated website domain,
  and exactly `https://<service>/auth/native/apple/callback/<profile-id>` as the
  return URL. The Server profile's Android package must match this build's
  `applicationId`. The manifest includes the pinned plugin's
  `SignInWithAppleCallback` activity and `signinwithapple` scheme. Route Begin,
  callback and Complete to the same Server member/process; the signed-in Server
  state is durable, but the pending login transaction is process-local. The
  callback's `state` starts with the returned affinity hint; validate it on the
  Server rather than treating routing information as authentication.

Apple's raw Server nonce is passed unchanged to the plugin. The Server validates
the ID token and callback state, stages Android's callback once, then performs
one private authorization-code exchange. Apple client-secret JWTs/signing keys
are Server-private and never appear in Dart defines or the app bundle. The
Android callback returns only the validated provider result to a fixed package;
it never returns a Lantern credential or the app's completion secret. A custom
scheme callback alone cannot authorize completion. Cancel abandons ownership;
late plugin callbacks cannot replace the newer account.

The opaque Lantern credential and bounded pending operation reviews are stored
in device-only Keychain/Keystore.
Android cloud backup and transfer are disabled for the credential/database;
Google/Apple inputs and pending completion secrets stay in process memory.
The fixed eight-hour session requires explicit login after expiry; it does not
slide or refresh. The app verifies saved sessions on launch and current policy
on resume. An iOS Apple state other than `authorized` closes local access.
Provider disconnect/consent withdrawal is not guaranteed to revoke a Lantern
session immediately; use Lantern revocation for that purpose.

Account change closes old HTTP/CDC/replay work before opening a new partition.
Confirmed views are invalidated for the new authority binding; pending outbox
records and possibly dispatched mutations are retained under their original
account. Ordinary sign-out does **not** call `wipeOnLogout()`. Before submitting
logout the app durably records its review; a lost response is recovered by
status only after that same account signs in again. Local teardown is distinct
from confirmed Server revocation and from provider consent removal.

Registration saves the exact Server review before dispatching Apply. After a
lost response or restart, use the saved Check registration action and authenticate
the same account to inspect that original operation. Unknown status stays pending;
the app never retries Apply or allocates a replacement operation. An applied
registration without a delivered session requires ordinary sign-in. Status does
not return a lost credential. Reviews are partitioned by endpoint, verified
issuer/subject and client profile; another account cannot recover them.

`NativeSession.userId` and `personalPrefix` are available after registration,
ordinary sign-in and session restoration. An existing UID-less Principal keeps
both fields null until explicit registration; the app offers registration and
does not guess a prefix or open the native data demo. UID creation does not
rename existing offline partitions or discard uncertain mutations. Registering
again preserves the UID and never restores a withdrawn Role assignment.

The deterministic provider wire fixture and app lifecycle tests are source
evidence. Real Google/Apple on Android/iOS remain four separate qualification
exits; no provider credentials or registrations are bundled here.

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
The example never embeds a shared `LANTERN_AUTH_TOKENS` value. This alternate
transport demo accepts user/device-scoped short-lived credentials from an
app-owned token endpoint. Native mode above obtains its credential directly
from Lantern; the server's static token list is an operator-side deployment input.
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
| `LanternUnauthenticatedException` | Native mode requires a new explicit sign-in; the alternate token-endpoint mode must obtain a valid user/device credential. Never fall back to a compiled shared token. |
| `LanternUnavailableException` / retry exhausted | Check the actual endpoint, DNS/gateway, radio state, and server health. Network-type reachability alone is not proof. |
| TTL appears early or late | Relative TTL is converted using the device clock once per logical call; inspect clock synchronization or use an absolute server-agreed instant. |
| Results grow memory | Consume bounded pages, prefer keys-only scans, cancel on navigation, and cap the visible model as this example does. |

## What the screen demonstrates

- app/signed-in-session ownership of one `LanternClient`;
- async runtime token refresh, bounded read/Put retry, and contribution-ID
  live deduplication without plain Add replay;
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
  foreground identity CDC session. This screen remains Put-only: hosted
  offline 0.3.0 has no receipt outbox, and merged offline 0.4.0 source
  implements receipt-backed conditional Put, exact Delete, and explicit-ID
  Add separately. It does not add those flows to this UI or qualify their
  physical release gates (#1399); #1449's generic capture preparation is
  not receipt-specific device evidence.

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

The explicit `OfflineDemoSession.wipeOnLogout()` demonstration is destructive:
use it only for a separately authorized local-data erase after resolving any
uncertain operations. Native sign-out closes the session and preserves its
account-owned outbox for status-first recovery. This fixture uses normal sqflite storage,
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
