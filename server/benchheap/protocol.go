// Package benchheap defines the private, measurement-only retained-heap protocol.
// It is not imported by the normal Server executable and exposes no RPC or env
// configuration. Source/image claims are bound by the external build owner;
// the tagged process independently verifies its executable hash and identity.
package benchheap

import "time"

const (
	Schema           = 1
	Protocol         = "symmetric_post_gc_2_v1"
	Directory        = "/run/lantern-bench-heap"
	SessionFile      = "session.json"
	ReadyFile        = "ready.json"
	RefusalFile      = "refusal.json"
	MaxInputBytes    = 4096
	MaxOutputBytes   = 32768
	MaxOrigins       = 16
	TargetBudget     = 25 * time.Second
	ControllerBudget = 30 * time.Second
	DrainBudget      = 10 * time.Second
	StableWindow     = 250 * time.Millisecond
	PollInterval     = 50 * time.Millisecond
	StatusComplete   = "complete"
	StatusIncomplete = "incomplete"
)

// Session is immutable, owner-written before the tagged process starts. Hashes
// are lowercase hex; run_nonce is 32 random bytes encoded as 64 hex characters.
type Session struct {
	Schema       int    `json:"schema"`
	Protocol     string `json:"protocol"`
	RunNonce     string `json:"run_nonce"`
	SourceCommit string `json:"source_commit"`
	SourceTree   string `json:"source_tree"`
	ImageID      string `json:"image_id"`
	ContainerID  string `json:"container_id"`
	BinarySHA256 string `json:"binary_sha256"`
	UID          int    `json:"uid"`
}

type Identity struct {
	Session    Session `json:"session"`
	PID        int     `json:"pid"`
	StartNonce string  `json:"start_nonce"`
}

type Ready struct {
	Schema   int      `json:"schema"`
	Protocol string   `json:"protocol"`
	Identity Identity `json:"identity"`
}

// Only baseline/ordinal1 and post/ordinal2 are accepted. Files are named
// baseline.request.json/post.request.json; completion and started use the same
// phase prefix. A request is committed before SIGUSR1, never inferred from it.
type Request struct {
	Schema           int      `json:"schema"`
	Protocol         string   `json:"protocol"`
	Identity         Identity `json:"identity"`
	Phase            string   `json:"phase"`
	Ordinal          int      `json:"ordinal"`
	CutNonce         string   `json:"cut_nonce"`
	ExpectedReceipts int64    `json:"expected_receipts"`
}

type Counter struct {
	Begun     uint64 `json:"begun"`
	Completed uint64 `json:"completed"`
	Active    int64  `json:"active"`
}

type Origin struct {
	ID       string `json:"id"`
	Sequence uint64 `json:"sequence"`
	WallNS   int64  `json:"wall_ns"`
	Logical  uint32 `json:"logical"`
}

// Native is a bounded committed-state observation, not a global fanout/stream
// idle claim. Subscribe active counts may remain nonzero; its lifecycle must
// stay stable. Public/peer unary and internal Apply must be inactive.
type Native struct {
	Vertices                int      `json:"vertices"`
	Edges                   int      `json:"edges"`
	Generation              uint64   `json:"generation"`
	Faulted                 bool     `json:"faulted"`
	PendingCount            int      `json:"pending_count"`
	PendingBytes            int      `json:"pending_bytes"`
	PendingLocal            bool     `json:"pending_local"`
	SnapshotActive          bool     `json:"snapshot_active"`
	Origins                 []Origin `json:"origins"`
	LogSequence             uint64   `json:"log_sequence"`
	LogLength               int      `json:"log_length"`
	LogCapacity             int      `json:"log_capacity"`
	LogEvicted              uint64   `json:"log_evicted"`
	ReceiptEntries          int      `json:"receipt_entries"`
	ReceiptBytes            uint64   `json:"receipt_bytes"`
	ReceiptHighWaterMS      int64    `json:"receipt_high_water_ms"`
	ReceiptOldestDeadlineMS int64    `json:"receipt_oldest_deadline_ms"`
	Public                  Counter  `json:"public"`
	PeerUnary               Counter  `json:"peer_unary"`
	PeerSubscribe           Counter  `json:"peer_subscribe"`
	Apply                   Counter  `json:"apply"`
}

type Memory struct {
	HeapAlloc    uint64 `json:"heap_alloc"`
	HeapObjects  uint64 `json:"heap_objects"`
	HeapInuse    uint64 `json:"heap_inuse"`
	NumGC        uint32 `json:"num_gc"`
	NumForcedGC  uint32 `json:"num_forced_gc"`
	LastGC       uint64 `json:"last_gc"`
	NextGC       uint64 `json:"next_gc"`
	MarkedLive   uint64 `json:"marked_live"`
	MetricCycles uint64 `json:"metric_cycles"`
	MetricForced uint64 `json:"metric_forced"`
	CycleAfter   uint32 `json:"cycle_after"`
}

type GC struct {
	StartedNS  int64  `json:"started_ns"`
	ReturnedNS int64  `json:"returned_ns"`
	Before     Memory `json:"before"`
	After      Memory `json:"after"`
}

type Started struct {
	Request Request `json:"request"`
}

type Completion struct {
	Schema      int     `json:"schema"`
	Protocol    string  `json:"protocol"`
	Request     Request `json:"request"`
	Status      string  `json:"status"`
	Reason      string  `json:"reason"`
	ElapsedNS   int64   `json:"elapsed_ns"`
	Before      Native  `json:"before"`
	After       Native  `json:"after"`
	Collections []GC    `json:"collections"`
	Memory      Memory  `json:"memory"`
}

func RequestFile(phase string) string    { return phase + ".request.json" }
func StartedFile(phase string) string    { return phase + ".started.json" }
func CompletionFile(phase string) string { return phase + ".complete.json" }
