# Offline core physical release runbook

This is the repeatable device procedure for the first
`lantern_client_offline` publication in [#1162](https://github.com/anaregdesign/lantern/issues/1162).
The existing [physical-device smoke guide](physical-device-smoke.md) defines the
evidence schema and historical observations. Earlier h2c or simulator results
do not qualify the release. Keep the Issue open and do not tag or publish until
both physical platforms pass this matrix on one exact clean code commit.

## Freeze one code candidate

After all release-preparation PRs have merged, choose one full `main` SHA. Use
an isolated clean checkout of that SHA for the entire matrix:

```bash
git fetch origin main
git switch --detach <full-main-code-SHA>
test -z "$(git status --porcelain)"
git rev-parse HEAD
flutter --version --machine
flutter devices --machine
(cd sdks/dart/example && flutter pub get --enforce-lockfile)
go build -o <private-fixture-dir>/lantern-server ./server/cmd
```

Verify that Flutter lists a physical Android and a physical iPhone. Record the
Flutter/Dart versions and framework revision, device model/OS without device
IDs, and the checked-in application package IDs. Build/test artifacts must be
from this checkout. If code changes, start the matrix again with a new SHA.

## Disposable authenticated HTTPS fixture

Use fresh random synthetic tokens and a task-owned Lantern server. Configure
`LANTERN_AUTH_TOKENS` with an old and a new accepted token, disable reflection,
and bind metrics to loopback. Serve a runtime token BFF that returns the new
token as `{"access_token":"..."}` at an unguessable path; bind it to loopback.
Put tokens, the BFF path, and test URLs only in a mode-600 file outside the
repository. Never write them into shell history, checked-in evidence, or logs.

For a temporary public-CA transport, expose the Lantern and BFF listeners with
two short-lived tunnels. The BFF tunnel's hostname and unguessable path are
both needed by the app. Stop both tunnels after testing:

```bash
cloudflared tunnel --no-autoupdate --url http://127.0.0.1:6380
cloudflared tunnel --no-autoupdate --url http://127.0.0.1:6381
```

Before installing the app, call a data-plane Connect RPC through the API
tunnel. Require HTTP 401 without bearer metadata and HTTP 200 with the new
synthetic token, both with successful platform TLS verification. Test the BFF
HTTPS endpoint as well. A publicly trusted tunnel tests the client-facing TLS
boundary; the private fixture remains disposable and contains synthetic data
only. A public tunnel does **not** exercise iOS Local Network permission, so
that scenario separately uses a LAN listener.

Create a private JSON file for Flutter's `--dart-define-from-file` option:

```json
{
  "LANTERN_ENDPOINT": "https://<temporary-api-host>",
  "LANTERN_TOKEN_ENDPOINT": "https://<temporary-bff-host>/<unguessable-path>",
  "LANTERN_OLD_TOKEN": "<synthetic-old-token>",
  "LANTERN_NEW_TOKEN": "<synthetic-new-token>",
  "LANTERN_TOKEN": "<synthetic-new-token>"
}
```

Use the same file for both devices. Do not set `LANTERN_ALLOW_INSECURE` for
these trusted HTTPS runs.

## Run and observe both physical columns

From `sdks/dart/example`, run each test separately for Android and iPhone,
replacing the placeholders with the device ID shown by `flutter devices` and
the private JSON file path:

```bash
flutter test --no-pub integration_test/mobile_smoke_test.dart \
  -d <physical-device-id> --reporter=expanded --timeout=3m \
  --dart-define-from-file=<private-defines.json>
flutter test --no-pub integration_test/physical_api_matrix_test.dart \
  -d <physical-device-id> --reporter=expanded --timeout=3m \
  --dart-define-from-file=<private-defines.json>
flutter test --no-pub integration_test/physical_ui_matrix_test.dart \
  -d <physical-device-id> --reporter=expanded --timeout=3m \
  --dart-define-from-file=<private-defines.json>
```

The mobile smoke must observe all ten online/offline/SQLite scenarios in the
release gate, including pending Put close/reopen, original TTL, expired-before-
replay rejection, durable logout wipe, and partition isolation. The API matrix
covers missing/rotated tokens, exact Vertex oneofs, bounded cursor pages and
partial failure, and committed-response-loss Add retry. The UI matrix covers
runtime BFF, exact values, traversal families, search, and navigation
cancellation. Require actual on-device pass markers and real RPC responses;
build/install success alone is not evidence. If Flutter treats the cabled
iPhone as wireless, use the `flutter drive --publish-port` or profile/CoreDevice
fallback documented in the physical-device smoke guide.

Immediately after each mobile smoke, hash the app artifact built and executed
for `mobile_smoke_test.dart` **before** another integration-test target rebuilds
or replaces it. Keep the path and hash in private notes until the evidence
record is written.

Run `integration_test/untrusted_tls_test.dart` against a **separate**, reachable
self-signed or hostname-mismatched TLS listener. Require
`CERTIFICATE_VERIFY_FAILED` and zero accepted RPCs. A trusted tunnel cannot
serve as the negative TLS endpoint. For Android, USB port forwarding to that
listener is acceptable; for iOS, verify the phone can reach a LAN listener.

With the normal app running, disable and restore each device's radio, then
confirm bounded offline failure, foreground resume, and explicit successful
refetch. On Android, also force a real screen-off Doze-like pause, observe
`IDLE`, then unforce, unlock, and refetch; restore device settings afterwards.
On iOS, deny the app's Local Network permission and run
`integration_test/local_network_denied_test.dart` against a LAN endpoint;
require failure closed, then allow permission and confirm retry. Record actual
observations for every required scenario, not expected outcomes.

## Record and release

Use the mobile-smoke Android APK and iOS Runner executable hashes, and state
which files were hashed. Add content-free
`evidence/offline-release/android.json` and `ios.json` with the same
`testedCommit`, exact toolchain/package identity,
observed scenarios, and no limitations. Commit **only** those two records (and
an optional adjacent README) as the immediate child of the tested code commit.
The `sdks/dart/offline/v0.2.0` tag points to that evidence-only child. Its
preflight compares these records with same-attempt Android/iOS CI manifests and
rejects changed code. Follow the one-time interactive OAuth bootstrap and
same-tag rerun in [CONTRIBUTING.md](../../../CONTRIBUTING.md). Do not create a
tag or publish while any physical observation is missing.
