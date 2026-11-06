# Managed replication scheduling

The selected scheduling group contains exactly five operational settings:

| Environment input | Default | Managed meaning |
| --- | --- | --- |
| `LANTERN_ANTI_ENTROPY_GAP_WARN_THRESHOLD` | 1024 | Nonnegative gap warning threshold; zero disables the warning. |
| `LANTERN_ANTI_ENTROPY_INTERVAL_MS` | 30000 | Nonnegative cadence; zero disables periodic anti-entropy. |
| `LANTERN_ANTI_ENTROPY_SUBSCRIBE_TIMEOUT_MS` | 30000 | Positive catch-up stream deadline in milliseconds. |
| `LANTERN_PUMP_BACKOFF_MIN_MS` | 250 | Positive initial reconnect delay in milliseconds. |
| `LANTERN_PUMP_BACKOFF_MAX_MS` | 30000 | Positive maximum reconnect delay, at least the minimum. |

Managed durations must fit `time.Duration`. Node identity, peer addresses,
discovery, transport trust and storage remain externally supplied.

The runtime uses process restart application. A committed successor changes
desired state and reports pending restart. Existing Pump and AntiEntropy objects,
trackers, resume state and sessions retain their actual effective policy. Replaying
a callback or rolling back to previous values at a new generation cannot claim
that this process has applied a new generation.

During startup, composition must read verified native desired state, validate the
atomic five-field group before constructor normalization, resolve only those
fields into focused configuration copies, and build both workers after the final
restored Search engine. Both workers must capture that engine's fingerprint.
Only comparison with the actual constructed worker pair establishes an applied
generation. Authentication and management remain usable while restart is pending.

Read-only worker scheduling observations return actual constructor-resolved
values. Legacy invalid bootstrap values that cannot be represented as a valid
managed policy remain an explicit unknown/error; no default is invented. A valid
successor can be staged for restart without mutating those live workers.

Normal composition registers one concrete worker bundle before public
certification. It reuses the Search-certified constructors internally and
exposes checked selectors for that exact Pump and AntiEntropy. App and public
certification check the registered pair and the original Search installation;
an equal fingerprint on a foreign pair does not establish ownership. Workers
are constructed without starting goroutines; App retains their Run and join
ownership. There is no parallel legacy worker construction in normal Wire.

All eight operational groups then have actual runtime owners; the log-level
policy retains its separate existing owner. Native adoption, review/apply and
observations use the existing typed control plane. This source integration does
not by itself provide a data-preserving restart procedure or establish final
real Connect/Admin, required CI or Issue #2015 acceptance. Search still requires
coordinated process restart; this group does not make a rolling restart safe.
