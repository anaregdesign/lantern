# Versioned search request policy

The managed group contains exactly these request settings. Bootstrap defaults
come from the existing ordinary configuration loader; no index or session
setting moves into this group.

| Setting | Default |
| --- | --- |
| `LANTERN_SEARCH_DEFAULT_LIMIT` | `100` |
| `LANTERN_SEARCH_MAX_LIMIT` | `1000` |
| `LANTERN_SEARCH_DEFAULT_MODE` | `any` |
| `LANTERN_SEARCH_DEFAULT_MIN_SHOULD` | `1` |
| `LANTERN_SEARCH_TIMEOUT_MS` | `5000` |
| `LANTERN_SEARCH_MAX_QUERY_BYTES` | `16384` |
| `LANTERN_SEARCH_MAX_QUERY_TERMS` | `1024` |
| `LANTERN_SEARCH_MAX_DICTIONARY_VISITS` | `1000000` |
| `LANTERN_SEARCH_MAX_POSTING_VISITS` | `10000000` |
| `LANTERN_SEARCH_MAX_POSITION_VISITS` | `10000000` |
| `LANTERN_SEARCH_MAX_EXPIRATION_VISITS` | `100000` |

Candidate resolution is read-only. It overlays the eleven typed values onto
the actual normalized engine baseline and returns the existing full capability
fingerprint. It never reads environment input, clamps managed values or changes
the core index. Default and maximum limits are positive with default at most
maximum. Modes form a closed enum. Min-should, whole-millisecond timeout,
query bytes and all five work budgets are positive; query terms cannot exceed
query bytes. The local excluded session hit cap must exceed the selected
maximum. Local admission/session resources must already be positive. The
ordinary startup validation remains responsible for the excluded index inputs;
their exact values also participate in the full fingerprint.

The native preflight must resolve candidates independently on every member of
its fixed cohort and bind the resulting compatibility proof before committing
desired state. Different excluded baselines may produce different fingerprints
or reject a candidate. Saving does not repair those baselines or establish
rolling-restart safety.

The runtime owner stages a committed desired snapshot separately from its
effective engine. While those differ, direct Search RPCs return `Unavailable`,
even when callers ignore health/readiness. A trusted request authority also
checks the currently certified desired generation and fingerprint before
admission and output. This closes the interval between native commit and an
asynchronous owner callback. Missing or stale authority does not fall back to
the owner's cached desired observation. The production adapter derives this
proof from the private data admission, independently of management grants and
ordinary query authorization; only explicitly environment-owned composition
uses no authority source.

An explicit engine restart uses the exact staged snapshot as its CAS token.
It constructs a new immutable query engine, semaphore and bounded cursor store,
then swaps the complete engine. It preserves the existing graph, core search
index and replication data. A repeated activation of the same effective
snapshot preserves the active cursor store. In-flight requests retain their
original engine, configuration and budgets; a changed desired/effective cut
refuses their output. The public boundary retains its independent current ACL,
namespace, Snapshot/publication and final output checks.

New engines have new cursor signing keys. Old engine cursors consequently
return the existing typed `SEARCH_CURSOR_INVALID`; naturally expired or
evicted current-engine cursors keep the existing `SEARCH_CURSOR_STALE`
contract. No unauthenticated generation field is used to classify a supplied
cursor. The outer namespace and authorization binding remains in force.

`SearchPolicyOwner.Status` describes its last staged desired observation and
actual effective engine. Management status must combine the current native
desired cut with this effective state and any observer/application error;
an observer cache alone cannot establish authoritative desired state.
Capabilities and `SearchConfigFingerprint` report the installed engine, never
the pending candidate.

The supported activation preserves graph data by restarting the owned query
engine rather than stopping every graph-only process. Complete integration
must refresh the replication workers' advertised fingerprint coherently with
activation. Their current constructors capture a fingerprint string; this
runtime package exposes a stable owner fingerprint provider for that later
worker integration and does not claim those workers refresh automatically.
Existing peer fingerprint mismatch warns and marks readiness unavailable while
graph replication continues. Management/private control and non-search data
remain reachable during a search transition. Zero-downtime rolling process
restart is not a blanket guarantee.

This package implements the pure policy/runtime seam and real Connect
transition tests for #2014. Native adoption, fixed-cohort proofs, persistence,
Admin review/Apply/recovery and replication-worker integration remain owned by
the common Epic #1990 integration packages.
