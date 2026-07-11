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

## Required matrix

| Scenario | Physical Android | Physical iOS |
| --- | --- | --- |
| Platform-trusted TLS succeeds | Not yet recorded | Not yet recorded |
| Untrusted/hostname-mismatched TLS fails closed | Not yet recorded | Not yet recorded |
| Missing and rotated short-lived token | Not yet recorded | Not yet recorded |
| Airplane/offline, then resume and explicit refetch | Not yet recorded | Not yet recorded |
| Background/Doze-like pause, then resume/refetch | Not yet recorded | Not yet recorded |
| Navigation cancels page and incremental-search work | Not yet recorded | Not yet recorded |
| Large cursor pages and partial batch failure stay bounded | Not yet recorded | Not yet recorded |
| Add retry preserves exactly one contribution | Not yet recorded | Not yet recorded |
| Every Vertex oneof renders exactly | Not yet recorded | Not yet recorded |
| iOS local-network privacy prompt/denial/retry | N/A | Not yet recorded |

For each completed column, add the UTC date, device/OS, Flutter revision,
Lantern revision, network topology, commands, and sanitized logs. Never include
tokens, private keys, device identifiers, or user data.
