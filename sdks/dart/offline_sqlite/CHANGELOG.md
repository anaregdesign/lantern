## Unreleased

- Align the unpublished adapter and maintained example with offline core
  `0.3.0` and the hosted parent `lantern_client 0.3.0` identity stream.
- Migrate schema 1 to 2 transactionally, adding indexed key-only resident
  recovery and a partition change epoch while retaining cache, cursor, and
  pending Put state. Host tests use the OS SQLite library.
- Migrate schema 1 or 2 to schema 3 transactionally by rewriting legacy outbox
  and operation payloads/reservations for receipt evidence and exact results,
  then validating the full durable graph and configured capacities before the
  upgrade commits.
- Migrate schema 1/2/3 payloads and reservations to schema 4, treating absent
  receipt-dispatch markers as possibly sent. Persist proven-unsent receipt
  rekeys and monotone pre-send markers across SQLite reopen.

## 0.1.0

- Add the opt-in `sqflite` adapter using Android/iOS platform SQLite.
- Persist confirmed cache, Put-only outbox, operation state, partition barriers,
  leases, and per-origin CDC chunk/cursor metadata in atomic SQL transactions.
- Add close/reopen conformance, disk-full and corruption checks, process-crash
  evidence, real-wire committed-response-loss replay, and native example use.
- Keep database protection and account binding application-owned. The package
  remains unpublished while physical-device qualification is outstanding.
