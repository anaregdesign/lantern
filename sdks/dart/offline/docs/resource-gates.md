# Parent and child resource qualification (#1902)

The probes contain contracts, not just performance measurements. They still run
in both offline jobs and remain direct prerequisites of Gate. No job, workload,
iteration, timeout, numerical limit, archive check or device gate is removed.

`--profile full` is the default. Only a parent `push` tag exactly matching
`sdks/dart/vX.Y.Z` selects `--profile parent-contracts`. Child tags, normal source
and changed-artifact checks, manual dispatch (including tags), unknown events
and malformed refs retain full envelopes. Missing/unknown explicit profiles fail.
The parent profile is qualifying for the parent dependency contract only; its
reported profile must never be cited as full child performance qualification.
Diagnostic SQLite runs remain nonqualifying and reject parent-contracts.

## Assertion inventory and separation

All assertions below exist in the original probes. The only profile-dependent
checks are marked **envelope**; the rest execute unconditionally.

- Reference store: `status_watch`, `entity_watch`, `confirmed_count`,
  `durable_status`, `one_send_per_item`, `bounded_concurrent_sends`, `send_cleanup`,
  `bounded_terminal_state`, `bounded_decoded_status_objects`, `canonical_reopen`
  are logical, ownership, permit, retention or cleanup contracts.
- Reference store: `snapshot_bytes` and `contribution_delete_codec_bytes` retain
  the 4 MiB retention limit. `replay_millis` (30 seconds) and `dispose_millis`
  (5 seconds) remain settlement/cleanup bounds in both profiles.
- Reference store **envelopes**: `enqueue_millis`, `recovery_millis`,
  `cache_read_p99_micros`, `entity_watch_p99_micros`, `rss_delta_bytes`, and
  `contribution_delete_codec_millis`. Typed contribution Delete codec round trips,
  dispatch marker and original immutable result checks remain unconditional.
- Bound admission: exact pending projection, 1001 nonterminal status items and
  zero token acquisition remain correctness/network-free contracts. The bound
  close latency remains a cleanup bound. Bound enqueue/read latency and RSS
  are **envelopes**. All measured values continue to be reported.
- Shared automatic session: `entity_read`, `graph_dimensions`, durable outbox
  settlement polling, exact send count/outbox/claims (`settlement`), global and
  per-scope send/read/traversal permits, alarm peak/final counts and joined
  traffic (`joined`) remain correctness, permit, ownership and cleanup contracts.
  Subscription cancellation, session close and client close remain awaited.
  The 30-second ticket timeout, 5-second watch readiness timeout, 500 settlement
  turns, `settle_budget` and `close_budget` are unchanged watchdogs.
- Shared automatic **envelopes**: enqueue latency, traversal phase, individual
  samples and RSS. The former compound `memory_budget` separates RSS from its
  4 MiB retained snapshot bound: only RSS is profile-dependent. Retained operation
  counts and subscription counts are still reported, not invented new limits.
- SQLite legacy: actual cache presence, replay count/status and close/reopen stay
  mandatory. Database 8 MiB, cache WAL 64 MiB and lifecycle WAL 128 MiB remain
  retention/write-amplification checks. Replay 30 seconds stays a settlement
  bound. Write/read/enqueue/reopen latency are **envelopes**.
- SQLite automatic: database 8 MiB, cache WAL 64 MiB and lifecycle WAL 128 MiB
  remain retention/write-amplification checks. Reopen latency is an **envelope**.
  SQLite retainedBytes remains telemetry (its shared memoryBytes input is zero),
  not a newly invented qualifying limit. Actual close/reopen still executes.
- Diagnostic stopped-snapshot capacity (100 outbox / one operation), 30-second
  wait and bounded failure inspection/cleanup remain diagnostic-only. Failure
  cannot become success; uncertain cleanup retains the database for the existing
  outer watchdog. No diagnostic output is relabeled qualification.

## Unchanged release boundary

Gate still requires direct successful offline jobs. Parent preflight retains
`!cancelled()` and direct Gate/configured-source success, floor/current Dart,
codegen, real-wire, mobile, isolated archive and published archive equality.
Offline hosted qualification still follows Gate and parent published equality;
only hosted success with `qualified=true` admits offline preflight. Own-tag
pending is refused; source/parent pending remains explicit. Normal current-sdk4
authority proof is unchanged. Physical evidence and publication remain separate,
held work; this change neither executes nor authorizes them.

Profile parser/envelope tests, source assertion boundary tests and the existing
workflow-condition/hosted contract truth tables cover this separation. Fresh full
qualification is still required on ordinary child changes; parent-contract results
cannot be reused as full-envelope results or release/device evidence.
