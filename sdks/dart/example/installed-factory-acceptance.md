# Installed factory T18 acceptance

`integration_test/physical_installed_factory_test.dart` is the bounded installed
`AppDataSessionFactory` target for [#1809](https://github.com/anaregdesign/lantern/issues/1809)
and T18 of [#1766](https://github.com/anaregdesign/lantern/issues/1766).
Preparing or compiling it does not accept the physical Android/iPhone,
publication, archive-equality or real-provider exits. The coordinator freezes
the merged source and owns those independent exits. Under
[#2119](https://github.com/anaregdesign/lantern/issues/2119), the complete signed
phone assertions may qualify device/application behavior with explicitly verified
controlled-backend provenance; they do not qualify a real provider or real UTC.

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

### Opt-in controlled native export

From the clean merged repository root, use its pinned Go toolchain and a new
private evidence directory outside the checkout:

```bash
GOTOOLCHAIN="go$(awk '$1 == "go" {print $2; exit}' go.mod)" \
  python3 -B .github/scripts/native_login_gate.py \
  --evidence "$NATIVE_FIXTURE_EVIDENCE" --serve-controlled --serve-profile native \
  --service-origin "$NATIVE_SERVICE_ORIGIN" \
  --listen-port 16443 --publish-port 18443 --prefix orders: \
  --lifetime-seconds 540
```

This foreground command is separate from the default functional-validation native
gate. Both exporter and serving executables are test binaries built with
`lantern_test_time`. A private loopback UDP source and continuously sampled native
elapsed time advance the fixture without real NTP, frozen time or jumps. It uses
a new task-labeled scratch container and publishes only `127.0.0.1:18443` to TLS
port `16443`. It refuses root-owned artifacts and `GOFLAGS` overrides.
No host DNS, clock, credential, provider or production policy changes occur.

The unchanged normal-build route uses `--serve` instead; the flags are mutually
exclusive. Normal serving uses the existing read-only `/etc/ntp.conf` and actual
configured NTP. It is not repeated as a prerequisite for controlled phone tests;
mandatory normal configured-source qualifications remain separate and blocking.

The coordinator owns the container/tunnel/BFF lifecycle. Wait for
`$NATIVE_FIXTURE_EVIDENCE/private/ready` before reading these private files:

| File | Contract |
| --- | --- |
| `private/ca.pem` | Fixture listener CA, mode 0600; no private key is exported. Use it to verify the parent-owned forwarding hop, never disable verification. |
| `private/fixture.json` | Mode 0600; controlled `schema: 2`, `kind: controlled-native-serving`, exact `endpoint`/`prefix`, `ca_file: ca.pem`, finite serving interval, and `accounts.a`/`accounts.b`, each with original `access_token` and verified fixed `expires_at`. |
| `private/ready` | Created last, only after registered identities, original session verification and real orders read/write/delete, receipt capability, Query and scoped WatchChanges checks. It is removed at shutdown. |

Controlled schema 2 also requires `source_kind: controlled-source`,
`build_kind: test-only`, `build_tags: [lantern_test_time]` and
`synthetic_utc: true`. The normal route keeps schema 1 and
`kind: synthetic-native-serving` unchanged. Reject missing, mixed or relabeled
provenance rather than inferring the source kind from an endpoint or token.

### Separate physical receipt/Head profile

For the existing smoke/CDC/contribution/receipt/Head targets, select
`--serve-profile physical` on the controlled `--serve-controlled` command.
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

Controlled `private/fixture.json` instead has `schema: 2`,
`kind: controlled-physical-serving`, the same controlled provenance fields,
`endpoint`, `ca_file`, `serving_until`, `credentials` containing
`observer_tokens` (exactly two), `writer_token` and `expires_at`, plus
`receipt_retention_ms`, `receipt_max_entries` and `receipt_max_bytes` from the
actual capability. It contains no native account/session claim. Normal `--serve`
retains schema 1 and `kind: current-physical-serving`. Both profiles preserve
the guarded runtime and cleanup protocol. The selected
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
login credentials are exported. Serving lifecycle completion alone is a synthetic
prerequisite, not configured-source-integration, real-provider or physical
acceptance. The current full local79 runs controlled functional-validation;
mandatory normal selected PR/main, exact pre-tag and selected-environment
configured-source-integration remain independent.

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

### Fixture provenance at phone capture

Verify the matching source, image, build kind and all six init/resume constructors
in the final successful driver receipt. Capture and retain a separate private
fixture-provenance record binding that receipt and descriptor to the finite
serving interval. A schema-2 host device record carries its content-free `fixture`
summary, including the provenance hash and actual phone-marker binding. Keep the
credential-bearing descriptor and raw logs private; never put them in Git.

After READY and before installing/launching the phone target, prepare the binding
and independently retain the printed SHA-256:

```bash
python3 -B .github/scripts/physical_fixture_provenance.py prepare \
  --evidence "$NATIVE_FIXTURE_EVIDENCE" --expected-head "$TESTED_SHA" \
  --profile native --source-kind controlled-source \
  --output "$NATIVE_FIXTURE_EVIDENCE/physical-fixture-binding.json"
```

Use `--profile physical` for the machine profile. After the observed phone pass
and successful release/join, finalize with that retained binding hash:

```bash
python3 -B .github/scripts/physical_fixture_provenance.py finalize \
  --evidence "$NATIVE_FIXTURE_EVIDENCE" \
  --binding "$NATIVE_FIXTURE_EVIDENCE/physical-fixture-binding.json" \
  --binding-sha256 "$PREPARED_BINDING_SHA256" \
  --output "$NATIVE_FIXTURE_EVIDENCE/physical-fixture-provenance.json"
```

Observe the full phone pass before the serving interval ends, capture the final
proxy trace, release/join the fixture successfully, then validate capture with
`--fixture-provenance`. The existing capture window and on-device byte comparison
still apply. Missing provenance, a failed lifecycle, source/build mismatch or a
phone body outside the interval fails closed. Normal legacy schema-1 host records
cannot silently describe a controlled backend.

Assemble the schema-2 record explicitly with the maintained
`.github/scripts/physical_fixture_provenance.py` API `public_summary(...)`.
Supply the actual marker's `startedAt` and `finishedAt` (`updatedAt` for smoke/CDC),
SHA-256 of its **raw captured bytes**, and `digest(encoded(payload))`, where
`payload` is the schema-2 host record excluding `fixture`. `encoded` uses sorted
JSON keys, compact separators and one trailing newline. The API re-verifies the
final private sidecar; the capture CLI remains read-only and compares the exact
assembled summary. Public `servingStoppedAt` and `lifecycleFinishedAt` also fence
early release: device completion must not follow actual serving stop, even if the
nominal `servingUntil` is later. Retain the private driver evidence alongside the
sidecar so capture can reverify its binaries, logs, descriptor and receipt.

Rebuild final-source signed phone artifacts after the implementation merges;
retaining an old signed binary does not qualify the new source. No extra public
sidecar is added to the four/eight-file evidence child. Full phone assertions,
same-install SIGKILL/status-first recovery and private custody remain required;
normal exact-source configured-source-integration still precedes publication.

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
