## 0.1.0

- Add the opt-in `sqflite` adapter using Android/iOS platform SQLite.
- Persist confirmed cache, Put-only outbox, operation state, partition barriers,
  leases, and per-origin CDC chunk/cursor metadata in atomic SQL transactions.
- Add close/reopen conformance, disk-full and corruption checks, process-crash
  evidence, real-wire committed-response-loss replay, and native example use.
- Keep database protection and account binding application-owned. The package
  remains unpublished while physical-device qualification is outstanding.
