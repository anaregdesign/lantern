package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/cache"
	coregraph "github.com/anaregdesign/lantern/core/graph"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/prototime"
)

// ServiceName is the fully-qualified service name used for per-service
// health reporting (grpc.health.v1, the published wire surface) and as a
// logical metric label.
const ServiceName = "graph.v1.LanternService"

// LanternService is the in-process implementation of the
// graph.v1.LanternService surface. The Connect handler adapter in
// connect.go wraps it; tests exercise it directly.
//
// Per-call logging, metrics, recovery, tracing and validation are
// wired up via Connect interceptors in server/provider so handlers
// stay focused on business logic. Handlers honor the incoming
// context — short paths rely on the transport to propagate
// cancellation; the early ctx.Err() checks let us return a clean
// Canceled / DeadlineExceeded *connect.Error when a client already
// gave up.
//
// The service depends on the narrow Backend interface (see backend.go); a
// wire binding maps it to *graphcache.GraphCache in production. Tests can supply
// a fake without standing up the real cache.
type LanternService struct {
	cache                  Backend
	scan                   ScanLimits
	search                 SearchLimits
	log                    *mutationlog.Log
	clock                  *hlc.Clock
	origin                 []byte
	onAppend               func()
	logger                 *slog.Logger
	tombstoneTTL           time.Duration
	origins                *originStateTracker
	onApplied              func(origin string)
	onReplicationApply     func(op string)
	onValidationReject     func(reason string)
	onTombstoneClampReject func()
	metrics                HotPathMetrics
	traversalTimeout       time.Duration
	traversalWorkBudget    graphcache.PPRWorkBudget
	traversalMaxResults    int
	capacity               CapacityLimits

	// statusInfo + startedAt + startedAtOnce back GetServerStatus
	// (#314). Populated by WithStatusInfo / MarkStarted from the
	// composition root; default zero values keep tests that construct
	// LanternService directly compatible (the RPC still returns a
	// well-formed response, just with empty fields).
	statusInfo    StatusInfo
	startedAt     time.Time
	startedAtOnce sync.Once

	// replicationSnapshotter + replicationStatusInfo back
	// GetReplicationStatus (#315). Both nil/zero by default so tests
	// constructing LanternService directly get a well-formed
	// "single-instance, no peers" response.
	replicationSnapshotter ReplicationSnapshotter
	replicationStatusInfo  ReplicationStatusInfo
}

// HotPathMetrics is the narrow observability surface consumed by the
// hot-path handlers (#220). Implemented by *server/metrics.DomainMetrics in
// production; tests can install a fake to assert per-RPC instrumentation
// fires.
//
// Implementations must be safe for concurrent use.
type HotPathMetrics interface {
	OnIlluminate(algorithm, reduction, objective, weighting string, visitedVertices, visitedEdges int, traversal, optimize time.Duration)
	OnIlluminateResult(algorithm, reduction, objective, weighting, phase, code string)
	OnScan(op string, results int, duration time.Duration)
	OnSearch(results int, duration time.Duration)
	OnBatch(op string, size int)
	OnGetVertices(hits, misses int)
	OnGetEdges(hits, misses int)
	OnEdgeContribDeduped(n int)
}

type noopHotPathMetrics struct{}

func (noopHotPathMetrics) OnIlluminate(string, string, string, string, int, int, time.Duration, time.Duration) {
}
func (noopHotPathMetrics) OnIlluminateResult(string, string, string, string, string, string) {
}
func (noopHotPathMetrics) OnScan(string, int, time.Duration) {}
func (noopHotPathMetrics) OnSearch(int, time.Duration)       {}
func (noopHotPathMetrics) OnBatch(string, int)               {}
func (noopHotPathMetrics) OnGetVertices(int, int)            {}
func (noopHotPathMetrics) OnGetEdges(int, int)               {}
func (noopHotPathMetrics) OnEdgeContribDeduped(int)          {}

// ScanLimits caps the per-call pagination knobs for the prefix RPCs. It is
// a value struct rather than a pointer-to-provider type so the service
// stays test-instantiable without the wire graph; production callers get
// it from provider.ScanConfig via the adapter constructor below.
type ScanLimits struct {
	ScanDefaultLimit           uint32
	ScanMaxLimit               uint32
	DeleteByPrefixDefaultLimit uint32
	DeleteByPrefixMaxLimit     uint32
}

func defaultScanLimits() ScanLimits {
	return ScanLimits{
		ScanDefaultLimit:           1000,
		ScanMaxLimit:               10000,
		DeleteByPrefixDefaultLimit: 10000,
		DeleteByPrefixMaxLimit:     100000,
	}
}

// SearchLimits configures the SearchVertices RPC. Enabled gates the RPC: a
// false value makes SearchVertices return FAILED_PRECONDITION, mirroring
// the composition root's decision NOT to call GraphCache.EnableSearchIndex
// (the core search backend returns nil for a disabled index, a no-match,
// and an empty query alike, so the gate cannot live in the cache — #624).
// DefaultLimit / MaxLimit cap the per-call ranked-hit count exactly like
// ScanLimits caps the prefix RPCs.
type SearchLimits struct {
	Enabled      bool
	DefaultLimit uint32
	MaxLimit     uint32
	// DefaultMode is the match mode applied when a request leaves match_mode
	// unspecified (#892); the zero value is MatchAny (the OR-union default).
	DefaultMode search.MatchMode
	// DefaultMinShould is the minimum-should-match count applied when the mode
	// is MatchMinShould but the request leaves min_should_match at 0.
	DefaultMinShould uint32
}

func defaultSearchLimits() SearchLimits {
	return SearchLimits{
		Enabled:      false,
		DefaultLimit: 100,
		MaxLimit:     1000,
	}
}

func NewLanternService(cache Backend) *LanternService {
	return &LanternService{
		cache:               cache,
		scan:                defaultScanLimits(),
		search:              defaultSearchLimits(),
		origins:             newOriginStateTracker(),
		metrics:             noopHotPathMetrics{},
		traversalTimeout:    5 * time.Second,
		traversalWorkBudget: graphcache.PPRWorkBudget{MaxPushes: 1_000_000, MaxTouchedEdges: 10_000_000},
		traversalMaxResults: 1024,
	}
}

// WithScanLimits returns s with its prefix-RPC limits replaced. Intended
// for the wire provider in package main to thread provider.ScanConfig into
// the service without forcing every test caller to construct the struct.
func (s *LanternService) WithScanLimits(l ScanLimits) *LanternService {
	s.scan = l
	return s
}

// WithSearchLimits returns s with its SearchVertices configuration replaced.
// Intended for the wire provider in package main to thread
// provider.SearchConfig into the service. Defaults leave search disabled so
// unit tests that construct LanternService directly get the FAILED_PRECONDITION
// path until they opt in.
func (s *LanternService) WithSearchLimits(l SearchLimits) *LanternService {
	s.search = l
	return s
}

// CapacityLimits carries the aggregate soft caps (#848). Zero (the default)
// means unlimited. Enforced only at the local write-RPC boundary — see
// checkVertexCapacity / checkEdgeCapacity for the conservative formulas and
// the deliberate slack in both directions. ApplyMutation and backup restore
// bypass the caps: rejecting writes peers already committed would break
// convergence, so the caps bound locally-originated growth and every HA node
// applies its own cap at its own RPC boundary.
type CapacityLimits struct {
	MaxVertices int
	MaxEdges    int
}

// WithCapacityLimits returns s with its aggregate capacity caps replaced.
// Intended for the wire provider to thread provider.CacheConfig into the
// service.
func (s *LanternService) WithCapacityLimits(l CapacityLimits) *LanternService {
	s.capacity = l
	return s
}

// checkVertexCapacity rejects a local write that could push the live vertex
// count past the cap. newVertices is the worst-case number of NEW vertices
// the batch can create: len(items) for PutVertices, 2*len(items) for edge
// writes (every edge auto-creates up to two endpoint vertices).
//
// The check is deliberately approximate in both directions (soft cap): a
// batch whose keys mostly already exist can be over-rejected near the cap,
// and concurrent in-flight batches that each passed the check can overshoot
// by their combined size. Neither is an invariant violation — the cap exists
// to fail fast instead of growing until the kernel OOM-kills the process.
func (s *LanternService) checkVertexCapacity(newVertices int) error {
	if s.capacity.MaxVertices <= 0 {
		return nil
	}
	if s.cache.VertexCount()+newVertices <= s.capacity.MaxVertices {
		return nil
	}
	if s.onValidationReject != nil {
		s.onValidationReject("capacity")
	}
	return connect.NewError(connect.CodeResourceExhausted, fmt.Errorf(
		"vertex capacity: %d live + up to %d new would exceed LANTERN_MAX_VERTICES=%d (soft cap, counted conservatively); retry after TTL decay or deletes free capacity",
		s.cache.VertexCount(), newVertices, s.capacity.MaxVertices))
}

// checkEdgeCapacity is the edge-side sibling of checkVertexCapacity;
// newEdges is len(items) (each item writes exactly one (tail, head) bucket).
func (s *LanternService) checkEdgeCapacity(newEdges int) error {
	if s.capacity.MaxEdges <= 0 {
		return nil
	}
	if s.cache.EdgeCount()+newEdges <= s.capacity.MaxEdges {
		return nil
	}
	if s.onValidationReject != nil {
		s.onValidationReject("capacity")
	}
	return connect.NewError(connect.CodeResourceExhausted, fmt.Errorf(
		"edge capacity: %d live + up to %d new would exceed LANTERN_MAX_EDGES=%d (soft cap, counted conservatively); retry after TTL decay or deletes free capacity",
		s.cache.EdgeCount(), newEdges, s.capacity.MaxEdges))
}

// WithReplication attaches the mutation log, hybrid logical clock, and an
// optional append-success callback (for metric counting). When log or clock
// is nil the service silently skips logging, which keeps existing tests
// (and the singular forwarders, which delegate to plurals) working without
// modification.
//
// origin is captured from the clock's NodeID so every appended Mutation
// carries the local node's identity.
func (s *LanternService) WithReplication(log *mutationlog.Log, clock *hlc.Clock, onAppend func()) *LanternService {
	s.log = log
	s.clock = clock
	if clock != nil {
		id := clock.NodeID()
		s.origin = id[:]
	}
	s.onAppend = onAppend
	return s
}

// WithLogger replaces the slog handle used for replication-side warnings
// (e.g. WAL write failures). Defaults to slog.Default() when unset.
func (s *LanternService) WithLogger(l *slog.Logger) *LanternService {
	s.logger = l
	return s
}

// WithHotPathMetrics installs the hot-path observability sink (#220).
// Defaults to a no-op so unit tests can construct the service without
// pulling in the metrics package.
func (s *LanternService) WithHotPathMetrics(m HotPathMetrics) *LanternService {
	if m == nil {
		s.metrics = noopHotPathMetrics{}
	} else {
		s.metrics = m
	}
	return s
}

// WithTombstoneTTL configures the maximum retention window for delete
// tombstones AND the upper bound on caller-supplied Expiration on the
// Add*/Put* RPCs (#183). When d <= 0 the clamp is disabled (legacy
// behaviour) and Delete handlers fall back to the non-HLC backend path.
// Wired from LANTERN_TOMBSTONE_TTL via provider.ReplicationConfig.
func (s *LanternService) WithTombstoneTTL(d time.Duration) *LanternService {
	s.tombstoneTTL = d
	return s
}

// WithAppliedHook registers a callback invoked after every successful
// remote-mutation apply (ApplyMutation path). The hook receives the
// lowercase-hex encoding of the originating HLC NodeID and is used by
// provider/metrics to bump lantern_replication_applied_total{origin}.
// A nil hook (the default) disables the callback. The hook MUST be
// non-blocking; the apply path holds no locks but synchronously waits
// for the callback to return.
func (s *LanternService) WithAppliedHook(f func(origin string)) *LanternService {
	s.onApplied = f
	return s
}

// WithReplicationApplyHook registers a callback invoked after every
// successful ApplyMutation, naming the MutationOp oneof variant
// (e.g. "PutVertex", "AddEdges"). Used by provider/metrics to bump
// lantern_replication_apply_total{op}. A nil hook disables the callback.
// The hook MUST be non-blocking.
func (s *LanternService) WithReplicationApplyHook(f func(op string)) *LanternService {
	s.onReplicationApply = f
	return s
}

// WithValidationRejectHook registers a callback invoked once per
// rejected request, naming the canonical reason (one of
// metrics.validationRejectReasons). Used by provider/metrics to bump
// lantern_validation_rejected_total{reason}. A nil hook disables the
// callback. The hook MUST be non-blocking — it runs synchronously on
// the RPC critical path. Called from validateExpiration (#183) and the
// prefix-scan cursor decode (server/service/prefix.go).
func (s *LanternService) WithValidationRejectHook(f func(reason string)) *LanternService {
	s.onValidationReject = f
	return s
}

// WithTombstoneClampRejectHook registers a callback invoked once per
// ApplyMutation Put/Add operation that the tombstone-aware HLC path
// drops because the incoming HLC lost the LWW comparison (against a
// live tombstone OR against a strictly-newer existing entry). Used by
// provider/metrics to bump lantern_tombstone_clamp_rejected_total.
// A nil hook disables the callback. Only fires inside the s.tombstoneTTL > 0
// branch of apply.go — the non-HLC fast path never rejects.
func (s *LanternService) WithTombstoneClampRejectHook(f func()) *LanternService {
	s.onTombstoneClampReject = f
	return s
}

// WithTraversalTimeout installs the server-side wall-clock budget for the
// traversal-heavy Illuminate RPC (#842). By default every Illuminate runs
// under context.WithTimeout(ctx, d), so a deadline-less client cannot hold
// the graph read lock indefinitely with an expensive PPR or deep BFS; expiry
// surfaces as CodeDeadlineExceeded via the existing ctx polling inside the
// walks. Passing d <= 0 explicitly keeps deadlines client-owned. The budget only
// tightens: a shorter client deadline still wins. Streaming RPCs and scans
// are deliberately NOT covered — scans are collection-bounded by ScanLimits.
func (s *LanternService) WithTraversalTimeout(d time.Duration) *LanternService {
	s.traversalTimeout = d
	return s
}

// TraversalLimits are the server-owned ceilings for PPR and local-community
// work. The zero-valued wire sentinels top_n=0 and max_size=0 resolve to
// MaxResults, never an unbounded response. MaxPushes and MaxTouchedEdges are
// passed to the core push loop, which returns a typed exhaustion error rather
// than a partial result.
type TraversalLimits struct {
	WorkBudget graphcache.PPRWorkBudget
	MaxResults int
}

// WithTraversalLimits replaces the PPR/community work and result ceilings.
// Non-positive values retain the safe constructor defaults.
func (s *LanternService) WithTraversalLimits(l TraversalLimits) *LanternService {
	if l.WorkBudget.MaxPushes > 0 {
		s.traversalWorkBudget.MaxPushes = l.WorkBudget.MaxPushes
	}
	if l.WorkBudget.MaxTouchedEdges > 0 {
		s.traversalWorkBudget.MaxTouchedEdges = l.WorkBudget.MaxTouchedEdges
	}
	if l.MaxResults > 0 {
		s.traversalMaxResults = l.MaxResults
	}
	return s
}

func (s *LanternService) effectiveTraversalResultLimit(requested int, field string) (int, error) {
	if requested == 0 {
		return s.traversalMaxResults, nil
	}
	if requested > s.traversalMaxResults {
		return 0, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s %d exceeds traversal result cap %d", field, requested, s.traversalMaxResults))
	}
	return requested, nil
}

// validateExpiration enforces the LANTERN_TOMBSTONE_TTL clamp on
// caller-supplied per-entry expirations. A zero expiration (the proto
// default — "no expiration") is always accepted; otherwise the
// expiration must not exceed now + tombstoneTTL, which is the longest
// window any tombstone could shadow a late replay. The error message
// names the env var so operators see the knob they need to adjust.
func (s *LanternService) validateExpiration(exp time.Time) error {
	if s.tombstoneTTL <= 0 || exp.IsZero() {
		return nil
	}
	if exp.After(time.Now().Add(s.tombstoneTTL)) {
		if s.onValidationReject != nil {
			s.onValidationReject("bad_ttl")
		}
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf(
			"expiration %s exceeds LANTERN_TOMBSTONE_TTL=%s",
			exp.UTC().Format(time.RFC3339Nano), s.tombstoneTTL))
	}
	return nil
}

// tombstoneExpiration returns the wall-clock instant a tombstone stamped
// now would expire, or the zero time when the clamp is disabled.
func (s *LanternService) tombstoneExpiration() time.Time {
	if s.tombstoneTTL <= 0 {
		return time.Time{}
	}
	return time.Now().Add(s.tombstoneTTL)
}

// OriginStates returns a snapshot of the per-origin (last_seq, hlc)
// watermark map maintained by logMutation and ApplyMutation. Used by
// the PeerStatus RPC (#186). Returns nil when replication is unwired
// (the tracker is nil in test paths that construct LanternService
// directly without going through NewLanternService).
func (s *LanternService) OriginStates() []OriginState {
	if s.origins == nil {
		return nil
	}
	return s.origins.States()
}

// LocalSeq returns the highest per-origin seq the local node has
// recorded for the given origin (0 when the origin has never been
// seen, or when the tracker is unwired). Used by the anti-entropy
// driver (#186) to compute its catch-up start seq.
func (s *LanternService) LocalSeq(origin hlc.NodeID) uint64 {
	if s.origins == nil {
		return 0
	}
	return s.origins.LocalSeq(origin)
}

// OriginStatesCount returns the number of distinct origins currently
// recorded in the per-origin watermark table, or 0 when the tracker is
// unwired. Used by provider/metrics to sample lantern_origin_states_count.
func (s *LanternService) OriginStatesCount() int {
	if s.origins == nil {
		return 0
	}
	return s.origins.OriginCount()
}

// logMutation appends op to the mutation log after a local commit. The HLC
// is stamped here so the seq->hlc ordering on the log is monotone with
// commit order. Failures are logged but not surfaced to clients — the
// local write has already succeeded and replication is best-effort within
// the bounded ring buffer.
//
// Callers MUST construct op as a fully populated MutationOp (one oneof
// case set). Returns immediately when the log is not wired (test path).
func (s *LanternService) logMutation(op *pb.MutationOp) {
	if s.log == nil || s.clock == nil {
		return
	}
	s.logMutationAt(op, s.clock.Now())
}

// logMutationAt is logMutation with the commit HLC supplied by the caller
// instead of sampled here. Local write paths that ALSO stamp the cache with
// an HLC watermark (so the origin's own writes participate in LWW on equal
// footing with the values its peers later apply from this same log entry)
// sample s.clock.Now() ONCE and hand that identical ts to both the cache
// write and this call. Sharing the instant closes the window in which the
// cache-side watermark and the logged mutation HLC could differ — a gap that
// let a concurrent write slip between the two and resolve LWW one way on the
// origin and the other way on its peers. Returns immediately when the log is
// not wired (test path); callers may pass a zero ts in that case.
func (s *LanternService) logMutationAt(op *pb.MutationOp, ts hlc.Timestamp) uint64 {
	if s.log == nil || s.clock == nil {
		return 0
	}
	mu := &pb.Mutation{
		Hlc: &pb.HLCTimestamp{
			WallNs:  ts.WallNs,
			Logical: ts.Logical,
			NodeId:  append([]byte(nil), ts.NodeID[:]...),
		},
		Origin: append([]byte(nil), s.origin...),
		Op:     op,
		// Seq is stamped atomically inside Append via the
		// SeqStamper callback below — by the time Append returns,
		// the dispatcher has already broadcast the entry to
		// subscribers with mu.Seq already set to the assigned
		// origin's seq. This is what keeps the leaderless
		// Subscribe contract (#415) race-free: Subscribe relay
		// forwards mu unchanged, so downstream consumers always
		// see (origin, origin_seq) anchored to the originating
		// writer regardless of how many replicas the entry
		// transits.
	}
	entry, err := s.log.Append(mu, ts, func(seq uint64) { mu.Seq = seq })
	if err != nil {
		l := s.logger
		if l == nil {
			l = slog.Default()
		}
		l.Warn("mutation log append failed", slog.Any("err", err))
		return 0
	}
	if s.origins != nil {
		s.origins.Record(ts.NodeID, entry.Seq, ts)
	}
	if s.onAppend != nil {
		s.onAppend()
	}
	return entry.Seq
}

// Illuminate returns a subgraph rooted at the seed, optionally reduced via
// an (algorithm family, reduction, objective) triple after the traversal
// walk. Edge weighting (RAW vs TF-IDF) is applied BEFORE the walk and
// therefore feeds into both the frontier ranking and any subsequent
// reduction. See #410, #963.
func (s *LanternService) Illuminate(ctx context.Context, request *pb.IlluminateRequest) (_ *pb.IlluminateResponse, retErr error) {
	algoLabel, redLabel, objLabel, weightLabel := illuminateMetricLabels(request)
	phase := "validation"
	defer func() {
		code := "ok"
		if retErr != nil {
			code = connect.CodeOf(retErr).String()
		}
		s.metrics.OnIlluminateResult(algoLabel, redLabel, objLabel, weightLabel, phase, code)
	}()

	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	// Server-side traversal budget (#842). Applied to BOTH arms (BFS and
	// PPR) at the top so the whole handler — walk plus any post-traversal
	// reduction — shares one deadline. WithTimeout only ever tightens, so a
	// client deadline shorter than the budget is unaffected.
	if s.traversalTimeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.traversalTimeout)
		defer cancel()
	}

	weighting := request.GetWeighting()
	// Map the wire weighting axis to the core edge-weight transform applied
	// BEFORE the per-hop top-k prune. RAW/UNSPECIFIED → verbatim weight, TFIDF
	// → the crude hub-suppressor, BM25 → Okapi BM25 over the out-edge
	// distribution (#800).
	coreWeighting := weightingToCore(weighting)
	// vertex_prefix (#602) scopes the traversal frontier to vertices whose key
	// carries the prefix. The server owns the concrete predicate (S = string
	// here); core only ever sees keep func(string) bool (#601). An empty prefix
	// leaves keep nil, i.e. an unfiltered walk. The closure is applied inside
	// the BFS BEFORE per-hop top-k and BEFORE any MST/SPT reduction below, so
	// the result is an induced subgraph (non-matching vertices are not
	// traversable bridges) and the seed stays as the anchor.
	var keep func(string) bool
	if p := request.GetVertexPrefix(); p != "" {
		keep = func(s string) bool { return strings.HasPrefix(s, p) }
	}

	var (
		g            *coregraph.Graph[string, *pb.Vertex]
		expirations  map[string]map[string]time.Time
		traversalDur time.Duration
		optimizeDur  time.Duration
		err          error
	)

	// Dispatch on the params oneof (#846): the selected case IS the traversal
	// family, and each arm reads only the knobs its family understands —
	// cross-algorithm misconfiguration is unrepresentable on the wire. Family
	// selection is required: an unset oneof (including a stale pre-#846 request
	// that only carries retired fields) is rejected rather than silently becoming
	// a zero-hop BFS walk.
	params := request.GetParams()
	if params == nil {
		return nil, invalidIlluminateParams("traversal family is required")
	}
	switch params := params.(type) {
	case *pb.IlluminateRequest_Ppr:
		if params.Ppr == nil {
			return nil, invalidIlluminateParams("ppr params are required")
		}
		// Personalized PageRank is a distinct traversal path, not a
		// post-traversal reduction (#801): it row-normalises the weighted
		// out-edges into a transition matrix and runs ACL forward-push from the
		// seed, returning a one-hop relevance star (seed→v carries pi[v]). It
		// is intrinsically a relevance maximiser with no per-hop step
		// semantics — which is why PprParams carries neither knob; top_n caps
		// the star by mass. There is no expirations map because the star
		// edges are synthetic.
		ppr := params.Ppr
		topN, limitErr := s.effectiveTraversalResultLimit(int(ppr.GetTopN()), "ppr.top_n")
		if limitErr != nil {
			return nil, limitErr
		}
		alpha, epsilon := resolvePPRParams(ppr.GetRestartProb(), ppr.GetEpsilon())
		phase = "traversal"
		traversalStart := time.Now()
		g, err = s.cache.PersonalizedPageRankWithWorkBudgetContext(ctx, request.GetSeed(), topN, alpha, epsilon, coreWeighting, keep, s.traversalWorkBudget)
		traversalDur = time.Since(traversalStart)
		if err != nil {
			return nil, traversalToConnect(err)
		}
		phase = "response"

	case *pb.IlluminateRequest_Community:
		if params.Community == nil {
			return nil, invalidIlluminateParams("community params are required")
		}
		// Local community extraction (#845): PageRank-Nibble sweep cut over
		// the shared push. Membership is decided by conductance (max_size is
		// an upper bound, not a count) and the result is the induced
		// subgraph — real edges (weighting-transformed weights; RAW = the
		// verbatim stored weight) with their expirations, unlike the PPR
		// star. An optional Reduction renders a tree VIEW rooted at the
		// seed, reducing over those weighting-transformed weights; sweep
		// prefixes need not be connected, so members unreachable from the
		// seed within the community stay as ISOLATED vertices rather than
		// being dropped or wired up artificially.
		comm := params.Community
		maxSize, limitErr := s.effectiveTraversalResultLimit(int(comm.GetMaxSize()), "community.max_size")
		if limitErr != nil {
			return nil, limitErr
		}
		alpha, epsilon := resolvePPRParams(comm.GetRestartProb(), comm.GetEpsilon())
		phase = "traversal"
		traversalStart := time.Now()
		g, expirations, err = s.cache.LocalCommunityWithWorkBudgetContext(ctx, request.GetSeed(), maxSize, alpha, epsilon, coreWeighting, keep, s.traversalWorkBudget)
		traversalDur = time.Since(traversalStart)
		if err != nil {
			return nil, traversalToConnect(err)
		}
		phase = "response"
		if opt := resolveOptimizer(comm.GetReduction(), comm.GetObjective()); opt != nil {
			phase = "reduction"
			optStart := time.Now()
			reduced, rerr := opt(ctx, g, request.GetSeed())
			if rerr != nil {
				return nil, optimizerToConnect(rerr)
			}
			// Membership is preserved: re-add community members the tree
			// could not reach as isolated vertices, then trim expirations to
			// the surviving tree edges.
			for k, v := range g.Vertices {
				if _, ok := reduced.Vertices[k]; !ok {
					reduced.Vertices[k] = v
				}
			}
			g = reduced
			trimmed := make(map[string]map[string]time.Time, len(g.Edges))
			for tail, heads := range g.Edges {
				if expRow, ok := expirations[tail]; ok {
					row := make(map[string]time.Time, len(heads))
					for head := range heads {
						if exp, has := expRow[head]; has {
							row[head] = exp
						}
					}
					if len(row) > 0 {
						trimmed[tail] = row
					}
				}
			}
			expirations = trimmed
			optimizeDur = time.Since(optStart)
			phase = "response"
		}

	case *pb.IlluminateRequest_Bfs:
		if params.Bfs == nil {
			return nil, invalidIlluminateParams("bfs params are required")
		}
		bfs := params.Bfs
		if bfs.GetStep() == 0 || bfs.GetFanOut() == 0 {
			return nil, invalidIlluminateParams("bfs step and fan_out must both be positive")
		}
		// Objective steers the per-hop top-k pruning as well as the
		// post-traversal reduction (#560): a MINIMIZE caller keeps the
		// fan_out smallest-weight edges at each hop so the cost-minimiser is
		// not handed a candidate set pruned to the costliest edges.
		// UNSPECIFIED resolves to MAXIMIZE (strongest edges).
		objective := bfs.GetObjective()
		selectSmallest := objective == pb.Objective_OBJECTIVE_MINIMIZE
		phase = "traversal"
		traversalStart := time.Now()
		g, expirations, err = s.cache.NeighborWithExpirationsContext(ctx, request.GetSeed(), int(bfs.GetStep()), int(bfs.GetFanOut()), coreWeighting, selectSmallest, keep)
		traversalDur = time.Since(traversalStart)
		if err != nil {
			return nil, ctxToConnect(err)
		}
		phase = "response"

		if opt := resolveOptimizer(bfs.GetReduction(), objective); opt != nil {
			phase = "reduction"
			optStart := time.Now()
			g, err = opt(ctx, g, request.GetSeed())
			optimizeDur = time.Since(optStart)
			if err != nil {
				return nil, optimizerToConnect(err)
			}
			phase = "response"
		}
	default:
		return nil, invalidIlluminateParams("unknown traversal family")
	}

	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}

	vertices := make([]*pb.Vertex, 0, len(g.Vertices))
	for k, v := range g.Vertices {
		if v == nil {
			vertices = append(vertices, &pb.Vertex{Key: k, Value: &pb.Vertex_Nil{Nil: true}})
		} else {
			vertices = append(vertices, v)
		}
	}

	var edges []*pb.Edge
	edgeCount := 0
	for tail, heads := range g.Edges {
		expRow := expirations[tail]
		for head, weight := range heads {
			edge := &pb.Edge{Tail: tail, Head: head, Weight: weight}
			if exp, ok := expRow[head]; ok && !exp.IsZero() {
				edge.Expiration = timestamppb.New(exp)
			}
			edges = append(edges, edge)
			edgeCount++
		}
	}

	s.metrics.OnIlluminate(algoLabel, redLabel, objLabel, weightLabel, len(vertices), edgeCount, traversalDur, optimizeDur)
	phase = "complete"

	return &pb.IlluminateResponse{
		Graph: &pb.Graph{Vertices: vertices, Edges: edges},
	}, nil
}

func invalidIlluminateParams(message string) error {
	return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("illuminate: %s", message))
}

func (s *LanternService) GetVertex(ctx context.Context, request *pb.GetVertexRequest) (*pb.GetVertexResponse, error) {
	resp, err := s.GetVertices(ctx, &pb.GetVerticesRequest{Keys: []string{request.GetKey()}})
	if err != nil {
		return nil, err
	}
	if len(resp.GetMissing()) == 1 {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("vertex %q not found", request.GetKey()))
	}
	return &pb.GetVertexResponse{Vertex: resp.GetVertices()[0]}, nil
}

// GetVertices reads several vertices in one round trip. Keys present at call
// time are returned in Vertices; missing (or expired) keys are reported in
// Missing. Order within either slice is not guaranteed to follow request
// order — clients should match by Vertex.key.
func (s *LanternService) GetVertices(ctx context.Context, request *pb.GetVerticesRequest) (*pb.GetVerticesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	keys := request.GetKeys()
	s.metrics.OnBatch("GetVertices", len(keys))
	resp := &pb.GetVerticesResponse{
		Vertices: make([]*pb.Vertex, 0, len(keys)),
	}
	for _, k := range keys {
		v, ok := s.cache.GetVertex(k)
		if !ok {
			resp.Missing = append(resp.Missing, k)
			continue
		}
		if v == nil {
			resp.Vertices = append(resp.Vertices, &pb.Vertex{Key: k, Value: &pb.Vertex_Nil{Nil: true}})
		} else {
			resp.Vertices = append(resp.Vertices, v)
		}
	}
	s.metrics.OnGetVertices(len(resp.Vertices), len(resp.Missing))
	return resp, nil
}

func (s *LanternService) PutVertex(ctx context.Context, request *pb.PutVertexRequest) (*pb.PutVertexResponse, error) {
	resp, err := s.PutVertices(ctx, &pb.PutVerticesRequest{
		Vertices: []*pb.Vertex{request.GetVertex()},
		IfAbsent: request.GetIfAbsent(),
	})
	if err != nil {
		return nil, err
	}
	// Unconditional puts always write; if_absent writes 0 or 1.
	return &pb.PutVertexResponse{Written: resp.GetWritten() >= 1}, nil
}

func (s *LanternService) PutVertices(ctx context.Context, request *pb.PutVerticesRequest) (*pb.PutVerticesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	in := request.GetVertices()
	s.metrics.OnBatch("PutVertices", len(in))
	now := time.Now()
	items := make([]graphcache.VertexItem[string, *pb.Vertex], 0, len(in))
	liveCount := 0
	for _, v := range in {
		expiration := prototime.Expiration(v.GetExpiration())
		if err := s.validateExpiration(expiration); err != nil {
			return nil, err
		}
		items = append(items, graphcache.VertexItem[string, *pb.Vertex]{
			Key:        v.GetKey(),
			Value:      v,
			Expiration: expiration,
		})
		if cache.IsLiveAt(expiration, now) {
			liveCount++
		}
	}
	// Aggregate capacity soft cap (#848): fail fast BEFORE any cache or log
	// mutation so a rejected batch is all-or-nothing.
	if err := s.checkVertexCapacity(len(items)); err != nil {
		return nil, err
	}
	// Conditional put (#896): write only keys with no live vertex. "Live"
	// follows the #750 visibility rule, so an expired-but-uncollected vertex
	// does not block the write. Under replication we stamp the accepted writes
	// with the mutation's HLC and replicate only that subset as an
	// unconditional LWW put — two concurrent if_absent writers both win
	// locally, then converge via higher-HLC-wins (documented best-effort,
	// like Redis SETNX with async replicas).
	if request.GetIfAbsent() {
		if s.clock != nil {
			ts := s.clock.Now()
			// Core returns only indices that were actually stored — a
			// born-expired item is discarded and excluded (#918), so
			// writtenIdx is all-live by construction and needs no second
			// liveness filter before replication.
			writtenIdx, skipped := s.cache.PutVerticesWithExpirationIfAbsentHLC(items, ts)
			if len(writtenIdx) > 0 {
				live := make([]*pb.Vertex, 0, len(writtenIdx))
				for _, i := range writtenIdx {
					live = append(live, in[i])
				}
				s.logMutationAt(&pb.MutationOp{Op: &pb.MutationOp_PutVertices{PutVertices: &pb.PutVerticesRequest{Vertices: live}}}, ts)
			}
			return &pb.PutVerticesResponse{Written: int32(len(writtenIdx)), SkippedKeys: skipped}, nil
		}
		written, skipped := s.cache.PutVerticesWithExpirationIfAbsent(items)
		return &pb.PutVerticesResponse{Written: int32(written), SkippedKeys: skipped}, nil
	}
	// When replication is enabled (clock wired) every local write is stamped
	// with the SAME HLC it is logged under, via the *HLC cache method, so the
	// origin's own PutVertex participates in last-writer-wins on equal footing
	// with the values its peers apply from this log entry. The non-HLC method
	// stored a value WITHOUT a vertexHLC watermark, so a concurrently-written
	// OLDER value replayed from a peer would clobber the origin's newer value
	// on the origin alone while every other replica kept the newer one —
	// permanent divergence (docs/replication.md "Higher HLC wins"). The
	// non-replicated path (clock nil) keeps the cheaper watermark-free method.
	//
	// A born-expired write (expiration already in the past) is dead on arrival:
	// the cache does not store it and it is invisible to GetVertex, so it must
	// not be logged or replicated either — otherwise peers would apply, store
	// and index (and watermark in vertexHLC) data that is already gone,
	// inflating their high-water for zero benefit (#698). Replicate only the
	// live subset; the all-live fast path forwards the original request
	// unchanged so the steady-state cost is a single liveness check per vertex.
	if s.clock != nil {
		ts := s.clock.Now()
		s.cache.PutVerticesWithExpirationHLC(items, ts)
		switch {
		case liveCount == len(in):
			s.logMutationAt(&pb.MutationOp{Op: &pb.MutationOp_PutVertices{PutVertices: request}}, ts)
		case liveCount > 0:
			live := make([]*pb.Vertex, 0, liveCount)
			for _, v := range in {
				if cache.IsLiveAt(prototime.Expiration(v.GetExpiration()), now) {
					live = append(live, v)
				}
			}
			s.logMutationAt(&pb.MutationOp{Op: &pb.MutationOp_PutVertices{PutVertices: &pb.PutVerticesRequest{Vertices: live}}}, ts)
		}
	} else {
		s.cache.PutVerticesWithExpiration(items)
	}
	// The response counts values that became live, not merely values accepted
	// for processing. This keeps the plural result aligned with PutVertex's
	// written=false contract for a born-expired value.
	return &pb.PutVerticesResponse{Written: int32(liveCount)}, nil
}

func (s *LanternService) DeleteVertex(ctx context.Context, in *pb.DeleteVertexRequest) (*pb.DeleteVertexResponse, error) {
	// Per the proto contract, deleting a vertex leaves its edges orphaned;
	// the periodic GC loop reaps any tf/df rows whose endpoints disappear.
	resp, err := s.DeleteVertices(ctx, &pb.DeleteVerticesRequest{Keys: []string{in.GetKey()}})
	if err != nil {
		return nil, err
	}
	return &pb.DeleteVertexResponse{Existed: resp.GetDeleted() == 1}, nil
}

func (s *LanternService) DeleteVertices(ctx context.Context, in *pb.DeleteVerticesRequest) (*pb.DeleteVerticesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	s.metrics.OnBatch("DeleteVertices", len(in.GetKeys()))
	var n int
	if s.clock != nil && s.tombstoneTTL > 0 {
		// Replicated path: sample the commit HLC ONCE and stamp BOTH the
		// tombstone and the logged mutation with it. Sampling clock.Now()
		// separately for each (as the cache call and logMutation used to)
		// left a window where a concurrent Put with an HLC between the two
		// would lose to the delete on peers but beat the tombstone on the
		// origin — divergence. Local expiration is best-effort wall clock.
		ts := s.clock.Now()
		n = s.cache.DeleteVerticesHLC(in.GetKeys(), ts, s.tombstoneExpiration())
		s.logMutationAt(&pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{DeleteVertices: in}}, ts)
	} else {
		n = s.cache.DeleteVertices(in.GetKeys())
		s.logMutation(&pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{DeleteVertices: in}})
	}
	return &pb.DeleteVerticesResponse{Deleted: int32(n)}, nil
}

func (s *LanternService) GetEdge(ctx context.Context, request *pb.GetEdgeRequest) (*pb.GetEdgeResponse, error) {
	resp, err := s.GetEdges(ctx, &pb.GetEdgesRequest{
		Edges: []*pb.EdgeKey{{Tail: request.GetTail(), Head: request.GetHead()}},
	})
	if err != nil {
		return nil, err
	}
	if len(resp.GetMissing()) == 1 {
		return nil, connect.NewError(connect.CodeNotFound, fmt.Errorf("edge %q -> %q not found", request.GetTail(), request.GetHead()))
	}
	return &pb.GetEdgeResponse{Edge: resp.GetEdges()[0]}, nil
}

// GetEdges reads several edges in one round trip. Pairs present at call time
// are returned in Edges; missing (or expired) pairs are reported in Missing.
// Order within either slice is not guaranteed to follow request order.
func (s *LanternService) GetEdges(ctx context.Context, request *pb.GetEdgesRequest) (*pb.GetEdgesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	in := request.GetEdges()
	s.metrics.OnBatch("GetEdges", len(in))
	resp := &pb.GetEdgesResponse{
		Edges: make([]*pb.Edge, 0, len(in)),
	}
	for _, k := range in {
		w, exp, ok := s.cache.GetEdgeDetail(k.GetTail(), k.GetHead())
		if !ok {
			resp.Missing = append(resp.Missing, &pb.EdgeKey{Tail: k.GetTail(), Head: k.GetHead()})
			continue
		}
		edge := &pb.Edge{Tail: k.GetTail(), Head: k.GetHead(), Weight: w}
		if !exp.IsZero() {
			edge.Expiration = timestamppb.New(exp)
		}
		resp.Edges = append(resp.Edges, edge)
	}
	s.metrics.OnGetEdges(len(resp.Edges), len(resp.Missing))
	return resp, nil
}

func (s *LanternService) AddEdge(ctx context.Context, request *pb.AddEdgeRequest) (*pb.AddEdgeResponse, error) {
	batch := &pb.AddEdgesRequest{Edges: []*pb.Edge{request.GetEdge()}}
	// Forward the optional idempotency key into the plural request's
	// index-aligned contrib_ids so the canonical AddEdges path applies the
	// same dedup (#588). An empty key stays absent (legacy additive path).
	if cid := request.GetContribId(); len(cid) > 0 {
		batch.ContribIds = [][]byte{cid}
	}
	resp, err := s.AddEdges(ctx, batch)
	if err != nil {
		return nil, err
	}
	// Surface the single edge's post-accumulation live weight (#897) from the
	// canonical plural response's index-aligned slice.
	out := &pb.AddEdgeResponse{}
	if ew := resp.GetEffectiveWeights(); len(ew) > 0 {
		out.EffectiveWeight = ew[0]
	}
	return out, nil
}

func (s *LanternService) AddEdges(ctx context.Context, request *pb.AddEdgesRequest) (*pb.AddEdgesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	in := request.GetEdges()
	s.metrics.OnBatch("AddEdges", len(in))
	contribIDs := request.GetContribIds()
	items := make([]graphcache.EdgeItem[string], 0, len(in))
	for i, e := range in {
		expiration := prototime.Expiration(e.GetExpiration())
		if err := s.validateExpiration(expiration); err != nil {
			return nil, err
		}
		item := graphcache.EdgeItem[string]{
			Tail:       e.GetTail(),
			Head:       e.GetHead(),
			Weight:     e.GetWeight(),
			Expiration: expiration,
		}
		// contrib_ids is index-aligned and optional (#588): a shorter or
		// missing slot, or an empty/zero key, leaves ContribID zero — the
		// legacy additive path. A non-zero key makes the contribution
		// idempotent so a transport retry re-sending the same bytes is a
		// no-op instead of double-counting edge weight.
		if i < len(contribIDs) {
			item.ContribID = contribIDFromBytes(contribIDs[i])
		}
		items = append(items, item)
	}
	// Aggregate capacity soft caps (#848): every edge item writes one bucket
	// and can auto-create up to two endpoint vertices, so both caps are
	// consulted with conservative worst-case deltas.
	if err := s.checkVertexCapacity(2 * len(items)); err != nil {
		return nil, err
	}
	if err := s.checkEdgeCapacity(len(items)); err != nil {
		return nil, err
	}
	// Additive merge converges via ContribID set semantics regardless of
	// delivery order, but when replication is enabled the contribution must
	// still be fenced by the edge tombstone at the SAME HLC it is logged
	// under, so a contribution older than a concurrent DeleteEdge is dropped
	// on the origin exactly as it is on every peer. The non-replicated path
	// (clock nil) keeps the cheaper tombstone-free method.
	var deduped int
	var effective []float32
	if s.clock != nil {
		ts := s.clock.Now()
		// Log FIRST so the per-origin seq this mutation commits under is
		// known, then synthesize a (origin, seq, idx) ContribID for every
		// edge the client left unkeyed BEFORE applying it locally. This
		// makes the origin's own graphcache carry the SAME ContribID a peer
		// synthesizes in ApplyMutation (contribIDFor), so the contribution
		// is a G-Set element (§4/§6 of docs/replication.md) on every
		// replica. Without it the origin stores a zero ContribID, Snapshot
		// emits it verbatim, and a re-pulled snapshot re-adds it additively
		// — doubling edge weight without bound on a gapped follower (#733).
		// Mirrors the once-sampled HLC discipline PutVertices uses for LWW.
		seq := s.logMutationAt(&pb.MutationOp{Op: &pb.MutationOp_AddEdges{AddEdges: request}}, ts)
		for i := range items {
			if items[i].ContribID.IsZero() {
				items[i].ContribID = contribIDFor(s.origin, seq, uint16(i))
			}
		}
		effective, deduped = s.cache.AddEdgesWithExpirationContribHLC(items, ts)
		s.metrics.OnEdgeContribDeduped(deduped)
	} else {
		effective, deduped = s.cache.AddEdgesWithExpirationContrib(items)
		s.metrics.OnEdgeContribDeduped(deduped)
	}
	// effective is the index-aligned post-accumulation live weight of each
	// edge (#897), a serving-node local view (a replication peer may hold
	// contributions not yet streamed in). It lets a client read back the
	// running total of a windowed counter without a second GetEdge.
	return &pb.AddEdgesResponse{Written: int32(len(items)), EffectiveWeights: effective}, nil
}

func (s *LanternService) PutEdge(ctx context.Context, request *pb.PutEdgeRequest) (*pb.PutEdgeResponse, error) {
	if _, err := s.PutEdges(ctx, &pb.PutEdgesRequest{Edges: []*pb.Edge{request.GetEdge()}}); err != nil {
		return nil, err
	}
	return &pb.PutEdgeResponse{}, nil
}

func (s *LanternService) PutEdges(ctx context.Context, request *pb.PutEdgesRequest) (*pb.PutEdgesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	in := request.GetEdges()
	s.metrics.OnBatch("PutEdges", len(in))
	items := make([]graphcache.EdgeItem[string], 0, len(in))
	for _, e := range in {
		expiration := prototime.Expiration(e.GetExpiration())
		if err := s.validateExpiration(expiration); err != nil {
			return nil, err
		}
		items = append(items, graphcache.EdgeItem[string]{
			Tail:       e.GetTail(),
			Head:       e.GetHead(),
			Weight:     e.GetWeight(),
			Expiration: expiration,
		})
	}
	// Aggregate capacity soft caps (#848) — same conservative deltas as
	// AddEdges: one bucket per item, up to two auto-created endpoints.
	if err := s.checkVertexCapacity(2 * len(items)); err != nil {
		return nil, err
	}
	if err := s.checkEdgeCapacity(len(items)); err != nil {
		return nil, err
	}
	// PutEdges*WithExpiration takes the cache write lock once for the whole
	// batch, so concurrent GetEdge readers never observe a transient NotFound
	// between the per-edge delete and add. When replication is enabled the
	// *HLC method stamps every edge with the SAME HLC it is logged under so
	// PutEdge resolves as an LWW-Register on (tail, head) across replicas —
	// without the watermark a concurrently-written OLDER edge replayed from a
	// peer would clobber the origin's newer weight on the origin alone
	// (docs/replication.md "Higher HLC wins"). The non-replicated path (clock
	// nil) keeps the cheaper watermark-free method.
	if s.clock != nil {
		ts := s.clock.Now()
		s.cache.PutEdgesWithExpirationHLC(items, ts)
		s.logMutationAt(&pb.MutationOp{Op: &pb.MutationOp_PutEdges{PutEdges: request}}, ts)
	} else {
		s.cache.PutEdgesWithExpiration(items)
	}
	return &pb.PutEdgesResponse{Written: int32(len(items))}, nil
}

func (s *LanternService) DeleteEdge(ctx context.Context, in *pb.DeleteEdgeRequest) (*pb.DeleteEdgeResponse, error) {
	resp, err := s.DeleteEdges(ctx, &pb.DeleteEdgesRequest{
		Edges: []*pb.EdgeKey{{Tail: in.GetTail(), Head: in.GetHead()}},
	})
	if err != nil {
		return nil, err
	}
	return &pb.DeleteEdgeResponse{Existed: resp.GetDeleted() == 1}, nil
}

func (s *LanternService) DeleteEdges(ctx context.Context, in *pb.DeleteEdgesRequest) (*pb.DeleteEdgesResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, ctxToConnect(err)
	}
	inEdges := in.GetEdges()
	s.metrics.OnBatch("DeleteEdges", len(inEdges))
	keys := make([]graphcache.EdgeKey[string], 0, len(inEdges))
	for _, e := range inEdges {
		keys = append(keys, graphcache.EdgeKey[string]{Tail: e.GetTail(), Head: e.GetHead()})
	}
	var n int
	if s.clock != nil && s.tombstoneTTL > 0 {
		// Share one commit HLC between the tombstone and the logged
		// mutation (see DeleteVertices for the divergence this closes).
		ts := s.clock.Now()
		n = s.cache.DeleteEdgesHLC(keys, ts, s.tombstoneExpiration())
		s.logMutationAt(&pb.MutationOp{Op: &pb.MutationOp_DeleteEdges{DeleteEdges: in}}, ts)
	} else {
		n = s.cache.DeleteEdges(keys)
		s.logMutation(&pb.MutationOp{Op: &pb.MutationOp_DeleteEdges{DeleteEdges: in}})
	}
	return &pb.DeleteEdgesResponse{Deleted: int32(n)}, nil
}

// LanternServer ties the Connect HTTP/2 server, its listener, the cache GC loop, and
