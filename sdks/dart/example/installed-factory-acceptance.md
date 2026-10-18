# Installed factory T18 acceptance

`integration_test/physical_installed_factory_test.dart` is the bounded installed
`AppDataSessionFactory` target for [#1809](https://github.com/anaregdesign/lantern/issues/1809)
and T18 of [#1766](https://github.com/anaregdesign/lantern/issues/1766).
Preparing or compiling it does not accept the physical Android/iPhone,
publication, archive-equality or real-provider exits. The coordinator freezes
the merged source and owns those independent exits.

## Fixture and ownership prerequisites

Use one production-guard-compatible, receipt-WAL Server with current native
end-user roles and scoped opaque WatchChanges. The two accounts must have
distinct Server-verified Principals, registered UIDs and personal prefixes.
Both original `lnu1_` credentials need more than ten minutes of remaining
fixed lifetime. The target verifies both with `GetNativeSession`; labels,
canonical `lnt_m1_` machine credentials or fabricated SDK metadata cannot
substitute for this prerequisite.

The isolated native-login fixture can perform the existing synthetic
Begin/Complete and public Begin/Prepare/Apply registration flows, then privately
export the verified credentials. This is fixture setup through production
verification, not real Google/Apple plugin or device signup acceptance
([#1750](https://github.com/anaregdesign/lantern/issues/1750) /
[#1758](https://github.com/anaregdesign/lantern/issues/1758)).
Provider hostname mapping belongs only to that new labeled fixture container;
never change host DNS, verifier hooks, actual provider configuration or
production safety guards.

The selected native service origin must equal the actual public HTTPS
`LANTERN_ENDPOINT`, including any port. Parent-owned BFF routes A and B return
bounded JSON `{"access_token":"<original native credential>"}` over
platform-trusted HTTPS. They must not exchange, trim or relabel the credentials.
The existing `authfixture -receipt/-head-edge` machine-token output cannot
provide these native-controller accounts.

Both synthetic native roles need the existing Vertex read/write/delete,
receipt and Query authority for the explicit `orders:` fixture prefix and
its current WatchChanges contract. Registered personal metadata is not itself
a grant. This target tests immutable *local* account isolation; the shared
`orders:` role is not evidence of remote per-user data isolation.

Use a dedicated test installation with no pre-existing saved native
credential. The target refuses to overwrite one. Each run owns fresh
`orders:t18:<runId>:` keys and exact run-specific database paths. Do not erase
genuine application data, an installation sentinel, Keychain state or native
lock to make a restore/owner hold disappear.

### Opt-in current-v2 native export

From the clean merged repository root, use its pinned Go toolchain and a new
private evidence directory outside the checkout:

```bash
GOTOOLCHAIN="go$(awk '$1 == "go" {print $2; exit}' go.mod)" \
  python3 -B .github/scripts/native_login_gate.py \
  --evidence "$NATIVE_FIXTURE_EVIDENCE" --serve \
  --service-origin "$NATIVE_SERVICE_ORIGIN" \
  --listen-port 16443 --publish-port 18443 --prefix orders: \
  --lifetime-seconds 540
```

This foreground command is separate from the unchanged default F native gate
and its other opt-ins. Serving uses normal builds, a new task-labeled scratch
container, actual configured NTP over a bridge, the existing `/etc/ntp.conf`
mounted read-only, and only `127.0.0.1:18443` published to container TLS port
`16443`. It refuses root-owned artifacts and `GOFLAGS` overrides. No host DNS,
clock, credential, provider or production policy changes occur.

The coordinator owns the container/tunnel/BFF lifecycle. Wait for
`$NATIVE_FIXTURE_EVIDENCE/private/ready` before reading these private files:

| File | Contract |
| --- | --- |
| `private/ca.pem` | Fixture listener CA, mode 0600; no private key is exported. Use it to verify the parent-owned forwarding hop, never disable verification. |
| `private/fixture.json` | Mode 0600; `schema: 1`, `kind: synthetic-native-serving`, exact `endpoint`/`prefix`, `ca_file: ca.pem`, finite RFC3339 `serving_until`, and `accounts.a`/`accounts.b`, each with original `access_token` and verified fixed `expires_at`. |
| `private/ready` | Created last, only after registered identities, original session verification and real orders read/write/delete, receipt capability, Query and scoped WatchChanges checks. It is removed at shutdown. |

### Separate physical receipt/Head profile

For the existing smoke/CDC/contribution/receipt/Head targets, select
`--serve-profile physical` on the same normal-build `--serve` command.
This is the independent [#1811](https://github.com/anaregdesign/lantern/issues/1811)
machine fixture, **not** a substitute for registered native A/B or T18.
The default native profile remains unchanged, including its one-hour/32-entry/
1-MiB receipt policy. The local `authfixture -receipt -head-edge` profile does not
qualify a physical device or supply this profile's receipt policy.

The physical profile has two independently generated, simultaneously accepted
`lnt_m1_` credentials for the same `public-worker` identity, plus a distinct
Head writer. Return the observer tokens in alternating runtime BFF responses
for CDC renewal; use a separate fixed-token BFF/control token for the two-launch
receipt proxy. No credential belongs in compile-time defines or public evidence.
The observer has explicit data/Query/ReceiptRead/WatchChanges grants only for
`mobile-smoke:`, `physical-api:`, `physical-cdc:`, `physical-contribution:`,
`physical-receipt:`, `physical-head:` and `flutter-demo:`. It has no management
grant. The Head writer has only tail read, head write, the denied head
subprefix and receipt read specified in `physical-device-smoke.md`.

Before creating `private/ready`, this profile verifies both rotating credentials
with `GetCurrentPrincipal`, every synthetic prefix's CRUD/Query, a real scoped
WatchChanges bootstrap, denial outside scope, minimal Head write-only behavior
and independent observation. It also reads the certified receipt capability:
retention is exactly **six hours**, maximum entries **1,024**, and maximum bytes
**32 MiB**, with all five required mutation families. Search is enabled.
These policy values do not extend signed membership or the finite serving
window. A timeout or authority failure never completes a phone test.

`private/fixture.json` instead has `kind: current-physical-serving`,
`endpoint`, `ca_file`, `serving_until`, `credentials` containing
`observer_tokens` (exactly two), `writer_token` and `expires_at`, plus
`receipt_retention_ms`, `receipt_max_entries` and `receipt_max_bytes` from the
actual capability. It contains no native account/session claim. Both kinds
use the same production-guarded runtime and cleanup protocol. The selected
profile is recorded separately in the private runner receipt and its exact
lifecycle case is required; native and machine lifecycle logs cannot substitute
for one another.

Prebuild/sign all consuming targets. Budget a fresh authority lifetime per
declared family/platform, preserve one immutable same-source image and unchanged
clock/trust setup, and never restart a responder between a receipt target's
two launches. The actual phone actions, final markers and signed-byte capture
remain mandatory. Restore device settings after each family.

### Shared bounded serving lifecycle

The native profile's fixture Principals are registered through public
Begin/Prepare/Apply against the existing PersonalUserRole before new native
login credentials are exported. The actual serving mode is a synthetic
prerequisite, not full configured-source Q, real-provider or physical
acceptance. The ordinary full local gate retains independent F/Q execution.

Keep the existing signed-membership maximum unchanged. Serving is bounded to
15–540 seconds, default/max 540; at export, signed membership must cover the
whole window plus fifteen seconds. The nine-minute setting therefore requires
setup to finish roughly forty seconds after provision issuance. Insufficient
time fails explicitly with `signed native fixture membership has insufficient
remaining serving lifetime`. Do not renew/reissue the membership, change
guards or shorten required assertions to obtain a pass.

Prebuild/sign the immutable target and prepare forwarding/BFFs **before**
starting this short window. Check `serving_until` after setup. Two sequential
eight-minute upper-bound runs require fresh fixture lifetimes; do not restart
the responder mid-run or claim continuity across fixture restarts.

To end the hold early from a separate terminal:

```bash
(umask 077; set -C; : > "$NATIVE_FIXTURE_EVIDENCE/private/release")
```

Otherwise the finite timeout ends it. Require the foreground driver to exit
zero and `receipt.json` to report `PASS` after owner cleanup/join. Current guard
failure remains `FAIL`. Lifecycle completion is not device acceptance. Serving
does not run the ordinary destructive native/registration subtests.

## Runtime defines and launch

Keep the define file private and outside Git. The BFF endpoints are runtime
credential delivery; no credential is compiled into the app.

```json
{
  "LANTERN_ENDPOINT": "https://server.example.invalid",
  "LANTERN_TOKEN_ENDPOINT": "https://tokens.example.invalid/native-a",
  "LANTERN_FACTORY_B_TOKEN_ENDPOINT": "https://tokens.example.invalid/native-b",
  "LANTERN_FACTORY_PREFIX": "orders:",
  "LANTERN_OFFLINE_CDC_PINNED_RESPONDER": true,
  "LANTERN_PHYSICAL_QUALIFICATION": true,
  "LANTERN_TESTED_COMMIT": "<full lowercase 40-hex merged source SHA>",
  "LANTERN_RECEIPT_RUN_ID": "<fresh lowercase 32-hex run ID>"
}
```

`LANTERN_FACTORY_PREFIX` defaults to `orders:`; an explicit subprefix must
start with `orders:` and end with `:`. Pin all traffic to the same qualified
responder. The target enforces HTTPS and normal platform trust, independently
of any other suite's insecure diagnostic option.

From `sdks/dart/example`, run the one target in a signed profile installation
on each actual phone:

```bash
flutter drive --profile --no-pub \
  --driver=test_driver/integration_test.dart \
  --target=integration_test/physical_installed_factory_test.dart \
  --dart-define-from-file=/private/t18/runtime.json -d <physical-device-id>
```

If iOS VM-service discovery is unavailable, use the existing direct-launch
binding and retrieve the two on-device files below instead of treating a
successful launch as a pass:

```bash
flutter build ios --profile --no-pub \
  --target=integration_test/physical_installed_factory_test.dart \
  --dart-define-from-file=/private/t18/runtime.json
xcrun devicectl device install app --device <physical-ios-id> \
  build/ios/iphoneos/Runner.app
xcrun devicectl device process launch --device <physical-ios-id> \
  --terminate-existing com.anaregdesign.lanternExample
```

Keep the exact installed signed artifact immutable. The complete target has an
eight-minute timeout and bounded 45-second transition waits. A timeout,
credential expiry, policy/restore hold, missing Query capability or failed
cleanup is a failure, not permission to manually drain or relax the fixture.

## Seven required scenarios

| Scenario | Contract |
| --- | --- |
| `factory_direct` | The same Live data UI, Vertex/Edge/Traversal watches and server-handling API, without a reusable confirmed cache or automatic mutation queue; a failed direct attempt needs a new explicit Put. |
| `factory_memory` | Bounded memory admission/cache; the existing foreground runtime handles admitted work after actual connection loss recovers. |
| `factory_durable` | Native SQLite admission/cache, exclusive owner and ordinary qualified reopen; original admitted work is handled automatically. |
| `factory_durable_without_read_cache` | The same native durable outgoing behavior, but cache-only confirmed reads stay Unknown. |
| `immutable_account_a_b_a` | Real secure credential storage and `NativeSessionController.restore` on two verified registered native accounts; old clients/lifetimes retire, B cannot observe A's local cache/status, and A's original operation and absolute TTL survive re-entry. No provider login runs. |
| `shared_remote_wipe_before_swap` | The separate mutable-remote path wipes A before B credentials are exposed, including a reopen check. Its explicit legacy drain is not the automatic-recovery proof. |
| `installation_restore_ownership` | Real installation MethodChannel, iOS secure marker and platform SQLite: unproven legacy holds without traffic or byte changes; independently proven same-installed-process provenance permits exact adoption preserving the original opaque operation; normal reopen delivers it; mismatched metadata and dual-location conflicts remain held. |

Each profile opens the actual factory and the same `DataObservationsScreen`,
not a transport/SQLite fake. A loopback relay forwards opaque TCP bytes while
the SDK retains the original HTTPS URL, SNI and platform trust. It destroys
existing sockets, including WatchChanges, and blocks new connections.
Restoring socket availability is the only recovery stimulus: no manual Get,
Sync, drain, reachability notification or lifecycle kick. An independently
connected observer changes the Edge while the owner is disconnected; Vertex,
Edge and derived Traversal must converge through their existing watches.

The target also checks memoized startup/close, explicit profile cache/admission
differences, suspended admission and resume, canceled alarms, retired
credential callbacks/streams and native owner exclusion/release. Controlled
restore corruption affects only the run's already closed fixture database,
never the installation sentinel or secure marker. Cleanup joins owners and
removes only tracked test data/files.

## Fresh marker and installed-byte attestation

Retrieve both files from the app's temporary directory **before another target
can overwrite the shared attestation filename**:

| File | Required final fields |
| --- | --- |
| `lantern-installed-factory-result.json` | `schema: 1`, `kind: physical_installed_factory_on_device_result`, `contentFree: true`, `status: passed`, `phase: complete`, fresh UTC `startedAt`/`updatedAt`, platform/package and `installedBinarySha256`. |
| `lantern-receipt-attestation.json` | Existing `schema: 1`, `kind: physical_receipt_attestation`, `contentFree: true`, exact target, tested commit, run ID, platform/package, matching installed hash, fresh UTC start/finish, all seven exact `completedScenarios`, `status: passed`, `phase: complete`. |

The historical receipt helper is reused for its cleanup and immutable-installed
byte contract; its filename/kind does **not** turn T18 into a legacy receipt
matrix. It hashes the native-reported installed APK on Android or the installed
`Frameworks/App.framework/App` on iOS before and after the run. A missing,
unreadable or changed binary fails closed. Every tracked cleanup must finish
before either final pass. The dedicated marker also waits for the integration
binding's successful result and independently hashes installed bytes again for
its final pass. No endpoint, credential, account, graph value,
database path or device identifier belongs in either public file.

The coordinator must independently match both hashes to the retained actually
installed signed artifact, match the source SHA/run ID/target and all scenarios,
and reject stale, partial, failed or mismatched results. A build or console
launch alone is not acceptance. Retain private logs/provenance separately and
cite content-free T18 proof in the driving Issue or evidence README.

Do **not** add a ninth JSON to the Offline release evidence-only child:
`offline/tool/physical_release_gate.py` still requires exactly the eight
historical Android/iOS smoke, CDC and receipt records/markers, plus an optional
README. Cite separately retained T18 files from that README or the Issue.
The historical suites do not retroactively cover this target, and T18 does
not replace any of their records or the independent publication/archive exits.
