# Physical SQLite evidence — 2026-09-24

These content-free records describe actual physical runs from clean commit
`50c9b00d9f8013947d8a6eccb6aa86a0b6073dfc`, using Flutter 3.44.6 / Dart 3.12.2.
The records retain the exact tested commit and installed binary hashes; the later
evidence and host-test additions do not retroactively change that tested SHA.

- [Android process restart](android-restart.json): one profile install, three
  processes, two forced stops; pending Vertex/Edge and original TTL survive,
  logout wipe remains durable, and the sibling user's local partition survives.
- [iOS process restart](ios-restart.json): the same three-launch contract with
  two actual SIGKILLs. On-device UI phase assertions were observed because the
  profile console did not forward Dart print output.
- [Android authenticated smoke](android-smoke.json): all ten current native
  SQLite/online/replay scenarios, using a synthetic runtime token BFF.
- [Android API matrix](android-api.json): four existing online API contracts.
  This does not enable Add in the offline outbox, which remains Put-only.

Android networking used USB forwarding to a task-owned local fixture because the
phone had no route to the host LAN. Network tests used explicit debug-insecure
Connect/h2c; no public tunnel was used. iOS used a task-only deployment target 15
override for local Xcode 27, with the checked-in target 13 unchanged. The records
therefore do not qualify trusted TLS, radio/Doze, local-network privacy, or the
complete #1162 publication matrix. Neither offline package is published here.

No device identifier, endpoint, token, key/value, certificate, or raw trace is
included. Screenshot hashes are provenance only; device screenshots are not
checked in. Refer to #1163 / PR #1267 for any additional physical smoke results.

## Tested source identity

The following Git objects identify compiled runtime sources and the two probe
entrypoints. Host evidence tooling or documentation can change separately without
claiming a new physical run of those later commits.

| Path | Git object at tested commit |
| --- | --- |
| `sdks/dart/lib` | `1d138e3d1d1e37ce1a9fa69c2261aebbe5a9aa3f` |
| `sdks/dart/offline/lib` | `f42db82725d75cb99b203b36c874c5d9766875a5` |
| `sdks/dart/offline_sqlite/lib` | `adeceb91079409c61ad68aea4f5eb311b05090b8` |
| `sdks/dart/example/lib` | `7049f758e502468959fee09ed3888819c757deeb` |
| `sdks/dart/example/integration_test/mobile_smoke_test.dart` | `1577e23133011bc0f525abc51a3dafd9262eca37` |
| `sdks/dart/example/integration_test/sqlite_restart_probe.dart` | `09a30e1cd396e2b8ee9135563889639376574bfe` |
