# Receipt-only physical attestation (prepared, inactive)

Issue #1449 prepares an **opt-in** receipt evidence path. It does not add a
receipt test target, qualify a device, change the four existing smoke/CDC
records, or alter `physical_release_gate.py`. The final #1398 receipt target
must implement the real assertions first; #1399 will then define its immutable
required scenario IDs and activate the receipt check in the release gate. Do
not use the historical smoke/CDC evidence or the probe below as receipt proof.

## What the final target must do

In its own `integration_test/*_test.dart` target, construct
`ReceiptAttestation.fromBuild` from
`integration_test/support/receipt_attestation.dart` with the **fixed target
path and scenario IDs in source**. The factory reads the exact 40-character
source commit and fresh 32-hex run ID supplied with `--dart-define` from
the compiled app, rejecting missing or malformed values. Wrap the entire test body in
`attestation.run((run) async { ... })`. Put each scenario's actual work and
assertions inside `await run.verifyScenario('<fixed-id>', () async { ... })`;
the runner marks it complete **only after the callback's last assertion
succeeds**. Register every owned cleanup through
`run.registerCleanup(...)`, not an untracked `addTearDown`: the marker is
`passed` only after all registered cleanups succeed and all required IDs have
actually been marked. An assertion, missing ID, duplicate mark, cleanup
failure, missing native channel, unreadable binary, or changed installed
platform/package/hash makes the run fail. The runner re-reads and re-hashes
the installed binary after tracked cleanup, before writing `passed`; a
change or read failure writes `failed` with phase `attestation`.
Failed/running markers do not qualify. Never pass an operator-provided
scenario list or target path into the test.

The runner first discards any old marker, asks the native app for its
**installed** binary path, hashes that file via streaming SHA-256, and
atomically writes a content-free marker in the app temp directory:
`lantern-receipt-attestation.json`. It records the commit, target, run ID,
native platform and package ID, installed hash, UTC start/finish, actual
completed IDs, and status/phase. It never records an endpoint, token, graph
key/value, app-bundle path, device ID, or raw exception. Android exposes
`ApplicationInfo.sourceDir` only for a single readable installed APK
containing `libapp.so` (the profile Dart AOT target); split installs and
debug/kernel-only APKs fail closed. iOS exposes the physical app's
`Runner.app/Frameworks/App.framework/App`, **not** `Runner.app/Runner`;
simulator lookup fails closed. No build-directory path is accepted by the
on-device reader.

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

## Capture a future receipt run on physical hardware

Use a clean, committed checkout with the exact intended code SHA, compatible
server, private authenticated TLS fixture, and a serial device slot. Keep
device IDs and any endpoint or token defines in private local files outside
this repository; do not upload commands/logs containing them. Sync host and
device UTC clocks. Pick a **new** random run ID for each platform and build
(`python3 -c 'import secrets; print(secrets.token_hex(16))'`). The final
target must use `ReceiptAttestation.fromBuild` so
`LANTERN_TESTED_COMMIT` and `LANTERN_RECEIPT_RUN_ID` are compiled into
the target and validated. Set `TARGET` to that target's fixed path; do not
substitute the probe.

For Android, from `sdks/dart/example`:

```bash
flutter build apk --profile --no-pub --target="$TARGET" \
  --dart-define=LANTERN_TESTED_COMMIT="$TESTED_SHA" \
  --dart-define=LANTERN_RECEIPT_RUN_ID="$RUN_ID" \
  --dart-define-from-file="$PRIVATE_DEFINES"
shasum -a 256 build/app/outputs/flutter-apk/app-profile.apk
adb -d install -r build/app/outputs/flutter-apk/app-profile.apk
adb -d shell pm path com.anaregdesign.lantern_example
adb -d shell getprop ro.kernel.qemu
RUN_STARTED_AT=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
adb -d shell am start -n com.anaregdesign.lantern_example/.MainActivity
adb -d exec-out run-as com.anaregdesign.lantern_example \
  cat cache/lantern-receipt-attestation.json > "$PRIVATE_DIR/android-marker.json"
```

Select an actual USB-connected physical device; check the private `pm path`
output is a single installed APK and `ro.kernel.qemu` is not `1`. The native
code hashes the installed `sourceDir` APK, not the host build path. If
`run-as` cannot retrieve the app-temp marker for this profile installation,
**stop**: a built APK or a handwritten JSON replacement is not evidence.
Optionally pull that `pm path` APK privately and compare its hash as a second
installed-path check; never upload the path or device ID.

For physical iOS, build a **signed profile** app with the same target/defines:

```bash
flutter build ios --profile --no-pub --target="$TARGET" \
  --dart-define=LANTERN_TESTED_COMMIT="$TESTED_SHA" \
  --dart-define=LANTERN_RECEIPT_RUN_ID="$RUN_ID" \
  --dart-define-from-file="$PRIVATE_DEFINES"
shasum -a 256 build/ios/iphoneos/Runner.app/Frameworks/App.framework/App
xcrun devicectl device install app --device "$PRIVATE_IOS_DEVICE_ID" \
  build/ios/iphoneos/Runner.app
RUN_STARTED_AT=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
xcrun devicectl device process launch --device "$PRIVATE_IOS_DEVICE_ID" \
  --terminate-existing com.anaregdesign.lanternExample
xcrun devicectl device copy from --device "$PRIVATE_IOS_DEVICE_ID" \
  --domain-type appDataContainer \
  --domain-identifier com.anaregdesign.lanternExample \
  --source tmp/lantern-receipt-attestation.json \
  --destination "$PRIVATE_DIR/ios-marker.json"
```

The native channel reads `Bundle.main.privateFrameworksURL/App.framework/App`
inside the **installed** app and Dart hashes its bytes. Hashing just the
Runner launcher is invalid. If signing/install changes the executable bytes,
the host/installed comparison must fail; investigate rather than substituting
a different artifact. Capture each marker as soon as the run and tracked
cleanup finish. The host UTC `recordedAt` must be within 30 minutes of
`finishedAt` (2-minute clock tolerance), and `RUN_STARTED_AT` must be taken
just before launch: the marker start must fall within 2 minutes before to
10 minutes after it. A run may last at most 4 hours. Run the capture validator
within 30 minutes of the record's `recordedAt` (and create the record within
30 minutes of the marker finish); old matching marker/record pairs cannot
pass a later capture-time check. The marker and record are each limited to
64 KiB, including direct device output. Keep the marker and host artifact
in private storage until independently validated.

Create a **new** sanitized receipt record for each platform only from that
captured marker. Keep markers and records outside the checkout while
validating: the capture CLI checks `git rev-parse HEAD`, a clean `git status`,
that the target exists in that commit, and the exact example build-artifact
path in that checkout. It also re-reads the marker directly from the
installed app (`adb -d run-as` after checking `ro.kernel.qemu` on Android,
CoreDevice app-data copy on iOS) and requires a byte-for-byte match with the
private marker file. A handwritten JSON file alone cannot pass the capture
CLI. The receipt record has the exact keys `schema`, `kind`
(`physical_offline_receipt_evidence`), `repository` (`anaregdesign/lantern`),
`contentFree` and `physicalDevice` (both true), `testedCommit`, `runId`,
`runStartedAt`, `recordedAt`, `platform` (`{"kind":"physical-android"}` or
`physical-ios`), `application` (`packageId`, fixed `target`,
`binarySha256`), sorted `scenarios`, and `result` (`passed`). Copy only
completed IDs and the installed digest from the marker, never planned IDs or
a build-only hash. **Do not check in receipt evidence yet.** The validator
also requires an independently supplied fixed scenario set and hash of the
exact target-specific host profile artifact:

```bash
python3 ../offline/tool/physical_receipt_attestation.py \
  --marker "$PRIVATE_DIR/android-marker.json" \
  --record "$PRIVATE_DIR/android-record.json" \
  --built-binary build/app/outputs/flutter-apk/app-profile.apk \
  --tested-commit "$TESTED_SHA" --target "$TARGET" --platform android \
  --run-id "$RUN_ID" --run-started-at "$RUN_STARTED_AT" \
  --scenario "$FIRST_FIXED_SCENARIO" --scenario "$NEXT_FIXED_SCENARIO"
```

For iOS use its marker/record, `--platform ios`, and
`--built-binary build/ios/iphoneos/Runner.app/Frameworks/App.framework/App`,
plus `--device-id "$PRIVATE_IOS_DEVICE_ID"` for the private CoreDevice copy.
Pass **every** scenario ID from the final target's fixed contract, not a list
copied from the marker. The validator rejects missing/stale/failed markers,
unexpected or duplicate keys (including nested fields), incorrect
commit/target/platform/run/scenarios, and
installed-vs-built hash drift. Its standalone CLI is a **capture-time**
check, not a release switch: after #1398 the blocking gate must require both
new physical records and a code-defined target/scenario set, and extend the
evidence-only tag change set. The exact signed iOS executable and installed
Android APK may not be reproducible from source in tag CI; #1399 must decide
how to retain or attest the capture-time host-byte comparison without
substituting a new build or accepting just a declared hash. A marker/record
cannot cryptographically attest a dishonest operator; retain device capture
and clean-checkout provenance privately for human review.
