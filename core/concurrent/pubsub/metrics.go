package pubsub

import "time"

// Observer is the optional sink for pubsub subscription telemetry. core/ is
// a leaf module and must not import server/metrics (AGENTS.md dependency
// boundary), so the server-side Prometheus collectors are injected via this
// interface. Implementations must be safe for concurrent use; pubsub may
// call any method from any worker goroutine. A nil observer is treated as
// the no-op.
//
// Method semantics:
//
//   - RecordEnqueueDepth is invoked once per successful enqueue with the
//     channel length after the send. Sampled rather than gauged so the
//     collector can aggregate (histogram) without per-subscription
//     cardinality.
//   - RecordDrop is invoked once per drop-path execution with one of the
//     policy strings exported below. Each drop must increment exactly once,
//     even when the drop-oldest fallback also drops the newest message
//     (that case fires RecordDrop("drop_oldest") followed by
//     RecordDrop("drop_newest_after_oldest")).
//   - ObserveDispatch is invoked once per consumer return with the wall-
//     clock duration from the originating Publish (message.createdAt) to
//     the moment the consumer callback returned.
type Observer interface {
	RecordEnqueueDepth(depth int)
	RecordDrop(policy string)
	ObserveDispatch(d time.Duration)
}

// Drop policy labels passed to Observer.RecordDrop. Exposed as a bounded
// set so server-side collectors can pre-warm the label rows and dashboards
// render the full series from process start.
const (
	DropPolicyNewest            = "drop_newest"
	DropPolicyOldest            = "drop_oldest"
	DropPolicyNewestAfterOldest = "drop_newest_after_oldest"
)

// DropPolicies is the canonical ordered list of policy labels emitted by
// RecordDrop. Useful for pre-warming Prometheus CounterVec rows.
var DropPolicies = []string{
	DropPolicyNewest,
	DropPolicyOldest,
	DropPolicyNewestAfterOldest,
}

// noopObserver is the default Observer used when none is configured. Kept
// as a typed value rather than a nil check in every call site so the hot
// path stays branch-free.
type noopObserver struct{}

func (noopObserver) RecordEnqueueDepth(int)        {}
func (noopObserver) RecordDrop(string)             {}
func (noopObserver) ObserveDispatch(time.Duration) {}
