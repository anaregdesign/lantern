# Receipt-only physical matrix (release-enforced, not yet qualified)

The dedicated `integration_test/physical_receipt_matrix_test.dart` target,
the fixed 12-ID per-platform matrix in `support/receipt_scenarios.dart`,
and the paired-marker release check are source implementations for #1399/#1449.
They do **not** qualify Android or iPhone until the exact frozen commit is
run on both physical devices and its signed originals are placed under
approved private immutable custody. The existing smoke/CDC results and the
binary probe below cannot substitute for a receipt run. Do not tag or
publish while either receipt record is absent.

## Two-launch on-device contract

`ReceiptAttestation.fromBuild` reads the 40-character source SHA and fresh
32-hex run ID compiled with `--dart-define`. The target and required
scenarios are constants in source, not operator-supplied. The first launch
calls `prepareForRestart`, executes each pre-kill assertion under
`verifyScenario`, and writes an atomic **running / awaiting_sigkill** marker
plus a private, bounded SQLite-directory restart journal. The four queued
receipt families must start with durable `mayHaveDispatched=false`; immediately
before each real mutation RPC, the SQLite outbox must already hold
`mayHaveDispatched=true`. After each committed response is lost, the target
checks the durable true flag and status-required state, then stores a
canonical SHA-256 of all four post-drop logical/record, receipt operation,
mutation, and group associations in the **private journal only**. Provisional
pre-send receipt IDs may change safely before dispatch; the comparison is
against the post-drop identities, not the initial queue snapshot. The first
launch leaves the repository, database, and clients open. The operator must
kill that process with an actual SIGKILL and relaunch the **same installed
binary** without uninstalling, reinstalling, clearing app data, or using hot
restart. A graceful close, crash-probe-only run, or initial-launch marker
is not a pass.

The second launch calls `resumeAfterRestart`. It requires the original
canonical marker and journal, exact commit/target/run/platform/package,
the same installed SHA-256 and completed pre-kill scenario set, a different
process ID, and a valid UTC handoff. It reopens the file-backed SQLite store
and checks the four pending ambiguous writes before sending anything. It
requires all four durable dispatch flags to remain true and compares the
reopened SQLite identities to the private journal digest before any status
lookup or drain. It then performs status-first reconciliation, checks **no
mutation resend** across the proxy's sealed kill boundary, asserts persisted
receipt results
(including finite-source float32 overflow), and runs each registered
cleanup. Only after all required scenario IDs and cleanup obligations have
passed and the installed bytes have been rehashed does it atomically write a
schema-2 **passed / complete** marker with the `restart` proof. An
incomplete, `running`, `failed`, or schema-1 marker cannot satisfy the
dedicated release check.

The marker stays content-free at `tmp/lantern-receipt-attestation.json`
(Android app cache); it never records an endpoint, token, graph data,
receipt IDs or their identity digest, device identifier, binary path, or raw
exception. The private journal is app-local and is not a public artifact.
Android hashes the installed single APK containing the Dart AOT target;
iOS hashes the installed signed
`Runner.app/Frameworks/App.framework/App`, **not** `Runner.app/Runner`.
Missing channels, unreadable bytes, split/debug-only APKs, and simulators
fail closed.

## Nonqualifying native lookup probe

`integration_test/receipt_binary_probe_test.dart` exercises the real
method channel and emits only a platform and SHA-256. It has **no** receipt
assertions, receipt attestation marker, or release record. If approved for a
physical probe, build this probe separately in profile mode for Android and
iOS, install the exact built app, and compare its hash with the host-built
APK or `App.framework/App` from that **probe build**. The hash is printed
when VM attachment works and also written to the distinct, nonqualifying
app-temp file `lantern-receipt-binary-probe.json`. On Android, copy it with:

```bash
adb -d exec-out run-as com.anaregdesign.lantern_example \
  cat cache/lantern-receipt-binary-probe.json > "$PRIVATE_DIR/android-probe.json"
```

On iOS use the CoreDevice `appDataContainer` copy shown below with
`tmp/lantern-receipt-binary-probe.json`. This only checks that the
installed-path lookup works on each device. The strict validator rejects
this probe's kind and missing receipt scenarios; its hash can never be
reused for a different receipt target. No device probe has been run as part
of #1449; get a serial device slot before attempting one.

## Fixture, physical actions, and capture

Obtain the exclusive device/host slot. Freeze **one clean code commit** after
the offline receipt fix and source checks. Prepare a receipt-certified durable
WAL responder (not graph-only) with all four mutations enabled, a retention
window **greater than four hours**, and adequate receipt caps. Its endpoint
must remain stable across both launches. Make its API and runtime-token BFF
device-reachable over platform-trusted HTTPS. Provide a separately reachable
hostname-mismatched or untrusted HTTPS listener (certificate verification
failure, **not** connection refusal), and a private LAN HTTPS route for
iOS permission denial.
Start `tool/physical_receipt_proxy.dart` with
`LANTERN_RECEIPT_PROXY_CONFIG` pointing to a mode-600 private JSON file
outside the repository. Its six keys are `listenHost`, `listenPort`,
`certificateChain`, `privateKey`, `upstream`, and `controlToken`. The proxy
control token must be the token that the runtime BFF issues to this app. The proxy
must be reachable over trusted HTTPS from the phone; it forwards only
receipt capability/status and the four receipt mutations, consumes the
upstream success before dropping each mutation's downstream socket once,
and exposes an authenticated, content-free count/order trace. The app fetches
its token at runtime from the BFF. Put all addresses, credentials, device IDs,
certificates, and private configuration outside this checkout; never publish
raw commands, logs, or transcripts containing them.

Before requesting physical devices, run the example's
`test/receipt_attestation_test.dart` and `test/physical_receipt_proxy_test.dart`
with `flutter test --no-pub`. The proxy unit test uses OpenSSL to generate
throwaway test-only CA/leaf files in a temporary directory and listens only
on loopback. It tests TLS trust and committed-response drops but does not
qualify a physical device or the real signed proxy certificate.

Supply these **private** compile-time defines in `--dart-define-from-file`:
`LANTERN_RECEIPT_ENDPOINT`, `LANTERN_RECEIPT_PROXY_ENDPOINT`,
`LANTERN_RECEIPT_UNTRUSTED_ENDPOINT`, `LANTERN_RECEIPT_LAN_ENDPOINT`, and
`LANTERN_RECEIPT_TOKEN_ENDPOINT`. Use HTTPS for all five; a loopback or
plaintext substitute cannot qualify. Generate a fresh random 32-hex
`LANTERN_RECEIPT_RUN_ID` for **each** platform and build, and compile the
same `LANTERN_TESTED_COMMIT` into both. Keep host/device UTC clocks synchronized.
The target is fixed to `integration_test/physical_receipt_matrix_test.dart`.

From `sdks/dart/example`, build a **signed profile** app once per platform
and install it once. For Android use
`flutter build apk --profile --no-pub --target="$TARGET"` with both defines
and the private define file, then `adb -d install` that exact
`build/app/outputs/flutter-apk/app-profile.apk`; require a single `pm path`
APK and `getprop ro.kernel.qemu` not equal to `1`. For iOS use
`flutter build ios --profile --no-pub --target="$TARGET"` with the same
defines and private file, then `xcrun devicectl device install app` of the
signed `build/ios/iphoneos/Runner.app`. Preserve the original signed APK and
signed iOS app/executable **privately and immutably** with their digests and
the sanitized capture transcript under approved custody; do not store
signed originals or device output in Git. A fresh rebuild or re-sign cannot
be substituted for the installed bytes.

Record `RUN_STARTED_AT` in UTC immediately before launch. Monitor the
content-free app-temp `lantern-receipt-operator-phase.json`, obey each
`radio_disable`, `radio_restore_foreground`, `android_enter_idle`,
`ios_deny_local_network`, and `ios_allow_local_network` instruction on
the **actual** device, and restore the original radio/privacy/idle settings
afterward. Require the proxy's committed-drop counters for conditional Put,
Vertex Delete, Edge Delete, and contribution-keyed Add. Wait for
`sigkill_now` and the **running / awaiting_sigkill** marker. Verify the
process remains live, send a genuine SIGKILL that does not
clear app data, record the private kill method/PIDs, and relaunch that same
installed app. Do not use `--terminate-existing` as evidence of SIGKILL.
The second process must produce a fresh schema-2 `passed / complete` marker
after post-relaunch assertions and cleanup. On Android retrieve it from
`cache/lantern-receipt-attestation.json` via `adb -d exec-out run-as`;
on iOS use CoreDevice `device copy from` with appDataContainer source
`tmp/lantern-receipt-attestation.json`. If marker extraction, an operator
phase, or any cleanup cannot be proven, stop and rerun from a fresh build
and run ID.

On Android, capture each current phase and the pre-kill marker with
`adb -d exec-out run-as com.anaregdesign.lantern_example cat
cache/<file>.json` into a private file, and obtain the live PID with
`adb -d shell pidof com.anaregdesign.lantern_example`. When and only when
the phase is `sigkill_now`, use
`adb -d shell run-as com.anaregdesign.lantern_example kill -9 <pre-kill-PID>`.
Confirm that PID is gone, then start the **already installed** target with
`adb -d shell am start -n
com.anaregdesign.lantern_example/.MainActivity`. On iPhone, copy each
`tmp/<file>.json` from the appDataContainer using CoreDevice as for the
marker, verify the running process ID, and send **signal 9** using an
approved device process tool (confirm the exact supported command on that
host); verify process exit before relaunching via CoreDevice **without**
`--terminate-existing`. If SIGKILL cannot be established on either device,
do not promote the marker or substitute a Home gesture, IDE restart, or
normal app termination. Store process IDs and operator actions only in the
approved private transcript.

Create a **content-free** schema-1 `physical_offline_receipt_evidence` record
per platform using only the passed marker's actual IDs and installed hash.
Its top-level fields are `schema`, `kind`, `repository`, `contentFree`,
`physicalDevice`, `testedCommit`, `runId`, `runStartedAt`, `recordedAt`,
`platform`, `application`, `network`, `scenarios`, and `result`.
`application` has `packageId`, fixed `target`, `binarySha256`; `network`
has `transport: Connect/HTTPS`, `authenticated: true`,
`platformTrustedTls: true`, `fault: committed-response-socket-drop`.
Do not include a URL, IP, token, graph key/value, device ID, or raw trace
in this record or the marker. Copy the private pair outside the checkout
first; within 30 minutes of `recordedAt`, run:

```bash
python3 ../offline/tool/physical_receipt_attestation.py \
  --marker "$PRIVATE_DIR/android-marker.json" \
  --record "$PRIVATE_DIR/android-record.json" \
  --built-binary build/app/outputs/flutter-apk/app-profile.apk \
  --tested-commit "$TESTED_SHA" \
  --target integration_test/physical_receipt_matrix_test.dart \
  --platform android --run-id "$RUN_ID" \
  --run-started-at "$RUN_STARTED_AT" \
  --scenario <repeat-for-every-fixed-Android-scenario>
```

For iOS use its marker/record, `--platform ios`, the exact signed
`build/ios/iphoneos/Runner.app/Frameworks/App.framework/App` as
`--built-binary`, and a private `--device-id`. Supply **each** ID from
`receipt_scenarios.dart` independently, never a list copied from the marker.
The validator checks the clean checkout, committed target, exact host build
path, byte-for-byte on-device marker, full scenario set, timestamp windows,
run/platform/target/commit, and installed-vs-host artifact digest. It cannot
recreate or independently attest signed originals after capture; retain the
privately approved immutable artifacts and capture transcript for review.

Only after both physical runs pass and private custody is approved may the
eight content-free public files be committed in an **evidence-only immediate
child** of the tested commit under `sdks/dart/example/evidence/offline-release/`:
four smoke/CDC records independently re-run on that same code commit,
`android-receipt.json`,
`ios-receipt.json`, `android-receipt-marker.json`, and
`ios-receipt-marker.json`. The release gate independently checks complete
fixed scenario sets, two-launch markers, cross-file identity, distinct run
IDs, and the evidence-only diff; a previously captured marker cannot be
re-used for a new binary or branch. No physical receipt run or approved
custody has been performed by this source change.
