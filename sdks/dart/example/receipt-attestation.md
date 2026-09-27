# Receipt-only physical matrix (release-enforced, not yet qualified)

The dedicated `integration_test/physical_receipt_matrix_test.dart` target,
the fixed 12-ID per-platform matrix in `support/receipt_scenarios.dart`,
and the paired-marker release check are source implementations for #1399/#1449.
They do **not** qualify Android or iPhone until the exact frozen commit is
run on both physical devices and its signed originals are placed under
approved private immutable custody. The existing smoke/CDC results and the
binary probe below cannot substitute for a receipt run. Do not tag or
publish while either receipt record is absent. The encrypted custody workflow
below is preparation only; **never upload a signed original or private journal
before separate approval of the parent custody boundary**.

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

The marker stays content-free under `Directory.systemTemp`: normally
`code_cache/lantern-receipt-attestation.json` on Android (with `cache/` as the
Flutter fallback), and `tmp/lantern-receipt-attestation.json` on iOS. It never
records an endpoint, token, graph data,
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
  cat code_cache/lantern-receipt-binary-probe.json > "$PRIVATE_DIR/android-probe.json"
```

If Flutter used the ordinary `cache/` fallback, read that path instead. Check
both paths and reject an ambiguous duplicate before using a captured marker.
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
An operator-phase announcement failure or normal 20-minute wait timeout writes
a failed handoff marker; start a fresh run rather than relaunching it.
The second process must produce a fresh schema-2 `passed / complete` marker
after post-relaunch assertions and cleanup. On Android retrieve it from
`code_cache/lantern-receipt-attestation.json` via `adb -d exec-out run-as`
(or `cache/` only when the `code_cache/` file is absent);
on iOS use CoreDevice `device copy from` with appDataContainer source
`tmp/lantern-receipt-attestation.json`. If marker extraction, an operator
phase, or any cleanup cannot be proven, stop and rerun from a fresh build
and run ID.

On Android, capture each current phase and the pre-kill marker with
`adb -d exec-out run-as com.anaregdesign.lantern_example cat
code_cache/<file>.json` into a private file (using `cache/` only if Flutter's
code cache is unavailable), and obtain the live PID with
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

## Private encrypted custody (operator hold)

**Do not upload any original or journal yet.** #1499 supplies only the
operator-side utility and this procedure; it neither approves #1493 nor
configures or accesses GCP. The 365-day evidence bucket was empty when this
procedure was written; do not assume it stays empty. Its default CMEK encrypts
stored objects at rest, but GCS still returns plaintext to an inherited
project-wide Storage Admin principal. Changing
bucket-only IAM does not remove that inherited read. A separate authorized
operator must approve and verify the client-side encryption, KMS/IAM,
auditing, retention, and recovery boundary **before** uploading any real
artifact. Neither a signed device run nor a synthetic encryption test
authorizes an upload on its own.

The operator must use the separately provisioned symmetric Cloud KMS
**client-side wrapping key**, never the bucket-default **server-side CMEK
key**. The Cloud Storage service agent has decrypt access to the bucket CMEK;
using it to wrap the client data key would defeat the intended isolation.
The client wrapping key currently has **no direct key IAM grants**: the
existing project owner inherits access, and no independent reviewer has
been designated. Do not claim independent review or approved custody.
Before real use, establish and verify named encrypting and recovering
principals with effective least-privileged permissions (for example,
`roles/cloudkms.cryptoKeyEncrypter` and
`roles/cloudkms.cryptoKeyDecrypter` on the wrapping key). The inherited
Storage Admin must have **no** KMS decrypt grant or impersonation path
through project/folder/org IAM. Limit GCS object creation and read access
independently; keep the storage service agent's
`roles/cloudkms.cryptoKeyEncrypterDecrypter` grant scoped to the **bucket
CMEK only**. Verify the retention lock, CMEK, generation preconditions,
permissions on both keys, Data Access audit configuration and dedicated
long-retention audit sink **including actual routing/retrieval**, which is
still under review.

Retain every client wrapping and bucket-CMEK **key version** needed to
read each object for the entire 365-day object retention **plus the
approved recovery window**. Rotating the primary version is fine; disabling,
destroying, or losing an older version makes retained evidence unreadable.
An administrator can still disable/schedule destruction of a key despite
the object lock; a destruction recovery grace period is not a substitute
for keeping the version enabled through retention. Bucket Lock guarantees
retention of ciphertext, **not** cryptographic key immutability or
independent human sign-off. Record version ownership and demonstrate a
synthetic restore under the approved access policy before using originals.

Work on an owner-controlled encrypted volume outside the checkout; use a
mode-700 directory and mode-600 regular files (not symlinks). Keep the
capture transcript, marker, journals, original signed APK, signed iOS
`App.framework/App`, and any lossless private `Runner.app` archive there.
Finish the device validator and independently compare each installed hash
with its exact built original **before** copying the original for custody.
For journals and a whole signed app archive, independently compute and
record their original SHA-256 in the private ledger. If archiving `Runner.app`,
preserve its signing metadata and executable mode (for example with a
private, lossless `ditto` archive on macOS), and separately hash and
custody its `App.framework/App` executable. The utility accepts individual
files, not directories, and restores exact file bytes; a bundle restore
also needs its signature and executable hash rechecked. Copy the built APK
or iOS executable into the private directory with
`install -m 600 "$BUILT_BINARY" "$PRIVATE_DIR/<opaque-original>"`, then
**rehash the copy** against the already validated installed/built digest.
This changes only the custody copy's filesystem mode, not its signed bytes;
the separate whole-bundle archive retains signing metadata and executable
modes.

From the repository root, prepare the utility locally without contacting GCP.
Set `PRIVATE_DIR`, `ORIGINAL`, `EXPECTED_SHA`, and `KMS_KEY` **only in the
private operator environment**, not in Git, public evidence, verbose shell
tracing, or captured command logs. `EXPECTED_SHA` must come from the
independently validated installed-binary marker or the separately measured
private journal/archive, not from an untrusted ciphertext:

```bash
umask 077
go test ./testbed/custody -count=1
go build -o "$PRIVATE_DIR/lantern-custody" ./testbed/custody
shasum -a 256 "$ORIGINAL"            # compare its digest privately with EXPECTED_SHA
"$PRIVATE_DIR/lantern-custody" encrypt \
  --in "$ORIGINAL" --out "$PRIVATE_DIR/<opaque-artifact>.lenc" \
  --kms-key "$KMS_KEY" --sha256 "$EXPECTED_SHA"
```

The utility requires a canonical
`projects/PROJECT/locations/LOCATION/keyRings/RING/cryptoKeys/KEY`
resource (not a key-version path), and a 64-hex original SHA-256 on
**both** encrypt and decrypt. It streams in bounded 1 MiB AES-256-GCM
chunks with a fresh 256-bit data key and nonce prefix per file (up to
the per-key GCM invocation ceiling, nearly 4 PiB), authenticates
the header and ordered frame lengths, and seals an end frame with the
total byte count, frame count, and original SHA-256. The header carries
only a hash of the KMS key resource, the wrapped data key, and format
parameters; do not put original names or device IDs in object names or
metadata. `gcloud kms encrypt/decrypt` receives only the 32-byte data key
or its bounded wrapped ciphertext through stdin/stdout pipes, never as a
process argument or plaintext file. Keep gcloud HTTP debug tracing off.
Output is a new mode-600 file in an existing mode-700 directory on a
filesystem supporting atomic hard links, linked into place without replacing
another file only after full authentication and hash verification; a failed
decrypt leaves no committed plaintext. A zero exit code confirms the supplied
original SHA-256; independently hash the private restored copy as a second
check. Keep all paths and digests private.

**Only after separate parent custody approval**, use an opaque object name
and upload **only the encrypted `.lenc` file** with an absent-object
generation precondition. **Disable gcloud parallel composite uploads**:
they create temporary component objects that may be impossible to delete
from a locked 365-day bucket. The effective per-command override must
print `False` before use (see the
[Cloud Storage parallel composite upload guidance](https://cloud.google.com/storage/docs/parallel-composite-uploads)).
In the private ledger record the locally
computed ciphertext SHA-256, expected original SHA-256, KMS key resource
and required versions, object URI, returned generation, size, retention
status, and exact source/marker identity. Do not put those identifiers
or raw transcripts in the content-free public record. The following
commands are for that later approved operator step, **not this PR**:

```bash
CLOUDSDK_STORAGE_PARALLEL_COMPOSITE_UPLOAD_ENABLED=False \
  gcloud config get-value storage/parallel_composite_upload_enabled
CLOUDSDK_STORAGE_PARALLEL_COMPOSITE_UPLOAD_ENABLED=False \
  gcloud storage cp "$CIPHERTEXT" "$OPAQUE_GCS_URI" --if-generation-match=0
gcloud storage objects describe "$OPAQUE_GCS_URI" --format=json \
  > "$PRIVATE_DIR/object-description.json"
gcloud storage cp "$OPAQUE_GCS_URI" "$PRIVATE_DIR/downloaded.lenc" \
  --do-not-decompress
shasum -a 256 "$CIPHERTEXT" "$PRIVATE_DIR/downloaded.lenc"
"$PRIVATE_DIR/lantern-custody" decrypt \
  --in "$PRIVATE_DIR/downloaded.lenc" --out "$PRIVATE_DIR/restored-original" \
  --kms-key "$KMS_KEY" --sha256 "$EXPECTED_SHA"
gcloud storage objects describe "$OPAQUE_GCS_URI" --format=json \
  > "$PRIVATE_DIR/object-description-after.json"
```

Independently compare the **exact** ciphertext SHA-256, remote generation,
size, lock/retention and CMEK details before and after download; CRC32C alone
is not a cryptographic comparison. Confirm the restored file's SHA-256 against
the trusted pre-upload original, and verify the signed binary/bundle again.
If any generation, digest, permission, version, signature, or audit check
differs, stop: do not promote evidence or discard the originals. Keep the
envelope key versions recoverable and the originals/private ledger on the
approved encrypted private volume until retention and recovery sign-off;
clean working plaintext copies using the volume's approved erasure procedure,
not an assumed SSD secure-delete command. Never publish KMS identifiers,
object URI, journal contents, tokens, or raw device output.

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
