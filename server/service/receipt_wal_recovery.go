package service

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/prototime"
)

// receiptWALDecisionAudit is a read-only inventory of known, unexpired
// committed results in one complete, genesis-based FileWAL. It is not a Store
// snapshot: an absent ID has no status, and the graph, origin tracker, Log,
// clock, and receipt high-water have not been installed as one serving cut.
type receiptWALDecisionAudit struct {
	knownReceipts   []mutationreceipt.Receipt
	origins         []OriginState
	lastLocalSeq    uint64
	highWaterMillis int64
	// Put effect evidence is diagnostic only. Old graph Put rows before the
	// first receipt remain readable but cannot certify a future mixed replay.
	evidencedGraphPutRows uint64
	unprovenGraphPutRows  uint64
	evidencedGraphAddRows uint64
	unprovenGraphAddRows  uint64
}

type receiptWALEnvelopeMetadata struct {
	origin    hlc.NodeID
	originSeq uint64
	epoch     mutationreceipt.Epoch
	policy    [32]byte
	receipts  []mutationreceipt.Receipt
}

func receiptWALEnvelopeInfo(op mutationlog.MutationOp) (receiptWALEnvelopeMetadata, bool) {
	switch value := op.(type) {
	case *graphAddEffectEnvelope:
		if !value.receiptBearing() {
			return receiptWALEnvelopeMetadata{}, false
		}
		return receiptWALEnvelopeMetadata{
			origin: value.Origin, originSeq: value.OriginSeq, epoch: value.Epoch,
			policy: value.PolicyFingerprint, receipts: value.Receipts,
		}, true
	case *edgeDeleteReceiptEnvelope:
		return receiptWALEnvelopeMetadata{
			origin: value.Origin, originSeq: value.OriginSeq, epoch: value.Epoch,
			policy: value.PolicyFingerprint, receipts: value.Receipts,
		}, true
	case *vertexPutReceiptEnvelope:
		return receiptWALEnvelopeMetadata{
			origin: value.Origin, originSeq: value.OriginSeq, epoch: value.Epoch,
			policy: value.PolicyFingerprint, receipts: value.Receipts,
		}, true
	case *vertexDeleteReceiptEnvelope:
		return receiptWALEnvelopeMetadata{
			origin: value.Origin, originSeq: value.OriginSeq, epoch: value.Epoch,
			policy: value.PolicyFingerprint, receipts: value.Receipts,
		}, true
	default:
		return receiptWALEnvelopeMetadata{}, false
	}
}

// auditReceiptDecisionsFromFileWAL validates a closed mixed FileWAL without
// opening an append writer. Each origin must start at seq 1 and remain
// contiguous: this helper has no verified Snapshot baseline to justify a
// later starting seq. The caller owns the file exclusively during all replay
// passes and must discard the returned audit on any error.
//
// The report deliberately cannot become an active receipt Store. A committed
// WAL does not record every Store.Begin/Lookup clock advance, and replaying
// only receipt rows would leave graph effects and origin state uninstalled.
// Until a complete atomic restore exists, neither fresh admission nor an
// absent-ID answer can be certified from this report.
func auditReceiptDecisionsFromFileWAL(path string, config mutationreceipt.Config, now time.Time) (receiptWALDecisionAudit, error) {
	base, err := mutationreceipt.New(config)
	if err != nil {
		return receiptWALDecisionAudit{}, err
	}
	highWater := now.UnixMilli()
	if highWater < 0 {
		return receiptWALDecisionAudit{}, mutationreceipt.ErrInvalidClock
	}
	if configured := base.Stats().HighWaterMillis; configured > highWater {
		highWater = configured
	}
	rows := make(map[hlc.NodeID]originRow)
	seenReceipts := make(map[mutationreceipt.ID]mutationreceipt.Receipt)
	report := receiptWALDecisionAudit{}
	var knownBytes uint64
	seenReceipt := false
	err = mutationlog.ReplayFileWAL(path, decodeReceiptWALUnion, func(entry mutationlog.Entry) error {
		if err := validateReceiptWALUnionEntry(entry); err != nil {
			return fmt.Errorf("receipt WAL local seq %d: %w", entry.Seq, err)
		}
		var origin hlc.NodeID
		var seq uint64
		var receiptRows []mutationreceipt.Receipt
		if envelope, ok := receiptWALEnvelopeInfo(entry.Op); ok {
			seenReceipt = true
			origin, seq = envelope.origin, envelope.originSeq
			if envelope.epoch != config.Epoch || envelope.policy != base.PolicyFingerprint() {
				return fmt.Errorf("receipt WAL local seq %d: %w: epoch or policy mismatch", entry.Seq, errReceiptWALUnion)
			}
			receiptRows = envelope.receipts
		} else {
			switch value := entry.Op.(type) {
			case *pb.Mutation:
				if isAnyGraphPut(value) {
					if seenReceipt {
						return fmt.Errorf("receipt WAL local seq %d: %w: graph Put lacks receiver-local accepted-effect evidence after receipt", entry.Seq, errReceiptWALUnion)
					}
					report.unprovenGraphPutRows++
				}
				if isAnyGraphAdd(value) {
					if seenReceipt {
						return fmt.Errorf("receipt WAL local seq %d: %w: graph Add lacks receiver-local accepted-effect evidence after receipt", entry.Seq, errReceiptWALUnion)
					}
					report.unprovenGraphAddRows++
				}
				copy(origin[:], value.GetOrigin())
				seq = value.GetSeq()
			case *graphPutEffectEnvelope:
				copy(origin[:], value.Mutation.GetOrigin())
				seq = value.Mutation.GetSeq()
				report.evidencedGraphPutRows++
			case *graphAddEffectEnvelope:
				copy(origin[:], value.Mutation.GetOrigin())
				seq = value.Mutation.GetSeq()
				report.evidencedGraphAddRows++
			case *graphDeleteEffectEnvelope:
				copy(origin[:], value.Mutation.GetOrigin())
				seq = value.Mutation.GetSeq()
			default:
				return fmt.Errorf("receipt WAL local seq %d: %w: unknown operation", entry.Seq, errReceiptWALUnion)
			}
		}
		previous, exists := rows[origin]
		if !nextContiguousOriginSeq(seq, previous, exists) {
			return fmt.Errorf("receipt WAL local seq %d: %w: origin %x seq %d is not contiguous", entry.Seq, errReceiptWALUnion, origin, seq)
		}
		if wallMS := entry.HLC.WallNs / int64(time.Millisecond); wallMS > highWater {
			highWater = wallMS
			// A later graph-only record can advance the clock just as a
			// receipt record can. Release expired rows and their exact
			// logical byte charge before the next receipt capacity check.
			previousReceipts := report.knownReceipts
			live := previousReceipts[:0]
			for _, receipt := range previousReceipts {
				if receipt.DeadlineMillis <= highWater {
					knownBytes -= receiptWALDecisionCost(receipt)
					continue
				}
				live = append(live, receipt)
			}
			clear(previousReceipts[len(live):])
			report.knownReceipts = live
		}
		if receiptRows != nil {
			for _, receipt := range receiptRows {
				// Check every committed row, including rows now expired.
				// Snapshot restore only sees the retained subset below.
				issued := int64(binary.BigEndian.Uint64(receipt.ID[17:25]))
				if receipt.DeadlineMillis-issued != config.Retention.Milliseconds() {
					return fmt.Errorf("receipt WAL local seq %d: %w: retention differs from policy", entry.Seq, errReceiptWALUnion)
				}
				if previous, duplicate := seenReceipts[receipt.ID]; duplicate {
					if !sameReceiptWALDecision(previous, receipt) {
						return fmt.Errorf("receipt WAL local seq %d: %w: conflicting duplicate operation ID %x", entry.Seq, mutationreceipt.ErrInvalidSnapshot, receipt.ID)
					}
					continue
				}
				copyOf := receipt
				copyOf.Result = append([]byte(nil), receipt.Result...)
				seenReceipts[receipt.ID] = copyOf
				// Expired results are never returned as confirmed. The ID and
				// retention policy still make them non-executable, but this
				// audit cannot answer any absent-ID status.
				if receipt.DeadlineMillis <= highWater {
					continue
				}
				// Match the Store's current logical byte ledger before
				// retaining another row; NewFromSnapshot verifies the ledger
				// again at the end.
				cost := receiptWALDecisionCost(receipt)
				if len(report.knownReceipts) >= config.MaxEntries || cost > config.MaxBytes-knownBytes {
					return fmt.Errorf("receipt WAL local seq %d: %w", entry.Seq, mutationreceipt.ErrCapacity)
				}
				knownBytes += cost
				report.knownReceipts = append(report.knownReceipts, copyOf)
			}
		}
		rows[origin] = originRow{seq: seq, hlc: entry.HLC}
		report.lastLocalSeq = entry.Seq
		return nil
	})
	if err != nil {
		return receiptWALDecisionAudit{}, err
	}
	// Validate the final bounded set using the Store's own receipt/index
	// rules rather than duplicating those rules here.
	sort.Slice(report.knownReceipts, func(i, j int) bool {
		return bytes.Compare(report.knownReceipts[i].ID[:], report.knownReceipts[j].ID[:]) < 0
	})
	state, err := base.Snapshot()
	if err != nil {
		return receiptWALDecisionAudit{}, err
	}
	state.ClockHighWaterMillis = highWater
	state.Receipts = report.knownReceipts
	if _, err := mutationreceipt.NewFromSnapshot(config, state); err != nil {
		return receiptWALDecisionAudit{}, fmt.Errorf("receipt WAL decisions: %w", err)
	}
	report.highWaterMillis = highWater
	report.origins = make([]OriginState, 0, len(rows))
	for origin, row := range rows {
		report.origins = append(report.origins, OriginState{Origin: origin, LastSeq: row.seq, LastHLC: row.hlc})
	}
	sort.Slice(report.origins, func(i, j int) bool {
		return bytes.Compare(report.origins[i].Origin[:], report.origins[j].Origin[:]) < 0
	})
	return report, nil
}

func receiptWALDecisionCost(receipt mutationreceipt.Receipt) uint64 {
	return uint64(len(receipt.ID) + len(receipt.Digest) + len(receipt.Group) +
		4 + 4 + 8 + 1 + len(receipt.Result))
}

func sameReceiptWALDecision(a, b mutationreceipt.Receipt) bool {
	return a.Intent == b.Intent &&
		a.DeadlineMillis == b.DeadlineMillis &&
		bytes.Equal(a.Result, b.Result)
}

func replayReceiptEnvelopeGraph(
	graph *graphcache.GraphCache[string, *pb.Vertex],
	op mutationlog.MutationOp,
) error {
	switch value := op.(type) {
	case *graphAddEffectEnvelope:
		if !value.receiptBearing() {
			return fmt.Errorf("%w: graph-only Add reached receipt replay", errReceiptWALUnion)
		}
		return replayGraphAddEffect(graph, value)
	case *edgeDeleteReceiptEnvelope:
		tx, err := graph.BeginReplicatedEdgeDelete(
			value.OriginalKeys, value.HLC, value.TombstoneExpiration,
		)
		if err != nil {
			return err
		}
		if !slices.Equal(tx.Result().Accepted, value.Accepted) {
			tx.Abort()
			return fmt.Errorf("%w: accepted Edge Delete projection drift", errReceiptWALUnion)
		}
		tx.Commit()
		return nil
	case *vertexDeleteReceiptEnvelope:
		keys := make([]string, len(value.Accepted))
		previous := -1
		for i, accepted := range value.Accepted {
			if accepted.Index <= previous || accepted.Index < 0 ||
				accepted.Index >= len(value.OriginalKeys) ||
				accepted.Key != value.OriginalKeys[accepted.Index] {
				return fmt.Errorf("%w: accepted Vertex Delete projection is invalid", errReceiptWALUnion)
			}
			keys[i] = accepted.Key
			previous = accepted.Index
		}
		tx, err := graph.BeginReplicatedVertexDelete(
			keys, value.HLC, value.TombstoneExpiration,
		)
		if err != nil {
			return err
		}
		replayed := tx.Result().Accepted
		if len(replayed) != len(keys) {
			tx.Abort()
			return fmt.Errorf("%w: accepted Vertex Delete projection drift", errReceiptWALUnion)
		}
		for i, accepted := range replayed {
			if accepted.Index != i || accepted.Key != keys[i] {
				tx.Abort()
				return fmt.Errorf("%w: accepted Vertex Delete projection drift", errReceiptWALUnion)
			}
		}
		tx.Commit()
		return nil
	case *vertexPutReceiptEnvelope:
		items := make([]graphcache.VertexItem[string, *pb.Vertex], len(value.Accepted))
		indexes := make([]int, len(value.Accepted))
		for i, accepted := range value.Accepted {
			items[i], indexes[i] = accepted.Item, accepted.Index
		}
		tx, err := graph.BeginReplicatedVertexPut(items, value.HLC)
		if err != nil {
			return err
		}
		replayed, err := normalizedVertexPutAccepted(tx.Result().Accepted, indexes)
		if err != nil {
			tx.Abort()
			return err
		}
		if !vertexPutReplayProjectionMatches(value.Accepted, replayed) {
			tx.Abort()
			return fmt.Errorf("%w: accepted Vertex Put projection drift", errReceiptWALUnion)
		}
		tx.Commit()
		return nil
	default:
		return fmt.Errorf("%w: unknown receipt envelope %T", errReceiptWALUnion, op)
	}
}

func vertexPutReplayProjectionMatches(
	recorded, replayed []graphcache.IndexedVertexPut[string, *pb.Vertex],
) bool {
	if len(recorded) != len(replayed) {
		return false
	}
	for i := range recorded {
		left, right := recorded[i], replayed[i]
		if left.Index != right.Index || left.Item.Key != right.Item.Key {
			return false
		}
		switch left.Outcome {
		case graphcache.PutOutcomeAppliedAndLive:
			if right.Outcome == graphcache.PutOutcomeExpired {
				if left.Item.Value == nil {
					return false
				}
				expiration := left.Item.Value.GetExpiration()
				if expiration == nil || expiration.CheckValid() != nil {
					return false
				}
				continue
			}
			if right.Outcome != graphcache.PutOutcomeAppliedAndLive ||
				!sameVertexPutCanonicalValue(left.Item.Value, right.Item.Value) ||
				!left.Item.Expiration.Equal(right.Item.Expiration) {
				return false
			}
		case graphcache.PutOutcomeExpired:
			if right.Outcome != graphcache.PutOutcomeExpired {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// receiptWALRecoveryCandidate is deliberately detached from LanternService.
// Its Store contains only WAL-observed results: Begin and an absent-ID Lookup
// are not certified because the previous process could advance Store's clock
// without logging it. No provider may install this candidate as a serving
// state until epoch/high-water recovery and an atomic publication cut exist.
type receiptWALRecoveryCandidate struct {
	graph       *graphcache.GraphCache[string, *pb.Vertex]
	receipts    *mutationreceipt.Store
	retired     mutationreceipt.RetiredCatalogSnapshot
	origins     *originStateTracker
	log         *mutationlog.Log
	hlcFrontier hlc.Timestamp // evidence for a future restored Clock, not a live Clock
}

// resumeReceiptWALCandidate validates a complete genesis WAL before making
// any state externally visible. The caller must own path exclusively through
// both replay passes; this function closes the resumed writer before return.
// A graph-only exact Delete now has an absolute deadline and a private
// accepted-index envelope, and graph Put/Add have accepted-effect envelopes.
// Raw graph writes after a receipt remain unreplayable: an omitted write
// could become accepted after a causal floor expires. Prefix origins publish
// exact victim batches, not predicates. Receipt Edge Deletes and evidenced
// graph Puts/Adds/Deletes can interleave when their projections reproduce.
//
// The recovered Log and FileWAL are closed before return. This read-only
// candidate does not authorize receipt admission, an absent-ID answer, or
// publication-fault clearing. A future full mixed-WAL format must record
// accepted graph effects for every dependent graph write before lifting these
// restrictions. Put, Add, and Delete effects replay only their receiver-local
// accepted subsets.
func resumeReceiptWALCandidate(path string, config mutationreceipt.Config, now time.Time, opts mutationlog.Options, defaultTTL time.Duration) (*receiptWALRecoveryCandidate, error) {
	return resumeReceiptWALCandidateWithEffectPolicy(path, config, now, opts, defaultTTL, false, nil)
}

// stageEffectCompleteReceiptWALCandidate is the stricter prerequisite for a
// future serving restore. Legacy graph Put/Add rows have no receiver-local
// accepted-effect evidence, even if a detached replay happens to produce a
// plausible graph. The lease must cover audit and replay, and configureGraph
// must install the intended serving indexes and limits on an empty staged
// cache before replay. The returned candidate remains read-only: effect
// evidence alone cannot certify the Store clock, epoch, or publication cut.
func stageEffectCompleteReceiptWALCandidate(lease *mutationlog.FileWALLease, config mutationreceipt.Config, now time.Time, opts mutationlog.Options, defaultTTL time.Duration, configureGraph func(*graphcache.GraphCache[string, *pb.Vertex]) error) (*receiptWALRecoveryCandidate, error) {
	if lease == nil || configureGraph == nil {
		return nil, fmt.Errorf("%w: WAL lease and graph configuration are required", errReceiptWALUnion)
	}
	var candidate *receiptWALRecoveryCandidate
	err := lease.WithPath(func(path string) error {
		var stageErr error
		candidate, stageErr = resumeReceiptWALCandidateWithEffectPolicy(path, config, now, opts, defaultTTL, true, configureGraph)
		return stageErr
	})
	if err != nil {
		return nil, err
	}
	return candidate, nil
}

func resumeReceiptWALCandidateWithEffectPolicy(path string, config mutationreceipt.Config, now time.Time, opts mutationlog.Options, defaultTTL time.Duration, requireCompleteEffects bool, configureGraph func(*graphcache.GraphCache[string, *pb.Vertex]) error) (*receiptWALRecoveryCandidate, error) {
	audit, err := auditReceiptDecisionsFromFileWAL(path, config, now)
	if err != nil {
		return nil, err
	}
	if requireCompleteEffects && (audit.unprovenGraphPutRows != 0 || audit.unprovenGraphAddRows != 0) {
		return nil, fmt.Errorf("%w: graph Put/Add rows lack receiver-local accepted-effect evidence", errReceiptWALUnion)
	}
	graph := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](defaultTTL)
	if configureGraph != nil {
		if err := configureGraph(graph); err != nil {
			return nil, fmt.Errorf("receipt WAL graph configuration: %w", err)
		}
		if !emptyReceiptWALRecoveryGraph(graph) {
			return nil, fmt.Errorf("%w: graph configuration populated the recovery cache", errReceiptWALUnion)
		}
	}
	origins := newOriginStateTracker()
	replayService := NewLanternService(graph)
	candidate := &receiptWALRecoveryCandidate{graph: graph, origins: origins}
	seenReceipt := false
	log, closer, err := mutationlog.ResumeLogFromFileWAL(path, opts, encodeReceiptWALUnion, decodeReceiptWALUnion, func(entry mutationlog.Entry) error {
		if err := validateReceiptWALUnionEntry(entry); err != nil {
			return fmt.Errorf("receipt WAL local seq %d: %w", entry.Seq, err)
		}
		var origin hlc.NodeID
		var seq uint64
		if envelope, ok := receiptWALEnvelopeInfo(entry.Op); ok {
			seenReceipt = true
			origin, seq = envelope.origin, envelope.originSeq
			if err := replayReceiptEnvelopeGraph(graph, entry.Op); err != nil {
				return fmt.Errorf("receipt WAL local seq %d: receipt graph replay: %w", entry.Seq, err)
			}
		} else {
			switch value := entry.Op.(type) {
			case *pb.Mutation:
				if seenReceipt || !receiptWALGraphRecoverable(value) {
					return fmt.Errorf("receipt WAL local seq %d: %w: graph arm has no exact recovery contract", entry.Seq, errReceiptWALUnion)
				}
				copy(origin[:], value.GetOrigin())
				seq = value.GetSeq()
				if _, err := replayService.applyMutationGraph(value); err != nil {
					return fmt.Errorf("receipt WAL local seq %d: graph replay: %w", entry.Seq, err)
				}
			case *graphDeleteEffectEnvelope:
				copy(origin[:], value.Mutation.GetOrigin())
				seq = value.Mutation.GetSeq()
				if err := replayGraphDeleteEffect(graph, value); err != nil {
					return fmt.Errorf("receipt WAL local seq %d: graph Delete effect replay: %w", entry.Seq, err)
				}
			case *graphPutEffectEnvelope:
				copy(origin[:], value.Mutation.GetOrigin())
				seq = value.Mutation.GetSeq()
				if err := replayGraphPutEffect(graph, value); err != nil {
					return fmt.Errorf("receipt WAL local seq %d: graph Put effect replay: %w", entry.Seq, err)
				}
			case *graphAddEffectEnvelope:
				copy(origin[:], value.Mutation.GetOrigin())
				seq = value.Mutation.GetSeq()
				if err := replayGraphAddEffect(graph, value); err != nil {
					return fmt.Errorf("receipt WAL local seq %d: graph Add effect replay: %w", entry.Seq, err)
				}
			default:
				return fmt.Errorf("receipt WAL local seq %d: %w: unknown operation", entry.Seq, errReceiptWALUnion)
			}
		}
		if !origins.Record(origin, seq, entry.HLC) {
			return fmt.Errorf("receipt WAL local seq %d: %w: origin frontier drift", entry.Seq, errReceiptWALUnion)
		}
		if candidate.hlcFrontier.Less(entry.HLC) {
			candidate.hlcFrontier = entry.HLC
		}
		return nil
	})
	if err != nil {
		return nil, err // All callback state was detached; Resume closed its writer.
	}
	lastSeq, hasEntries := log.LastSeq()
	if lastSeq != audit.lastLocalSeq || hasEntries != (audit.lastLocalSeq != 0) || !slices.Equal(origins.States(), audit.origins) {
		_ = closer.Close()
		return nil, fmt.Errorf("%w: recovered graph/origin/Log cut differs from WAL audit", errReceiptWALUnion)
	}
	base, err := mutationreceipt.New(config)
	if err == nil {
		var state mutationreceipt.Snapshot
		state, err = base.Snapshot()
		if err == nil {
			state.ClockHighWaterMillis = audit.highWaterMillis
			state.Receipts = audit.knownReceipts
			candidate.receipts, err = mutationreceipt.NewFromSnapshot(config, state)
		}
	}
	if err != nil {
		_ = closer.Close()
		return nil, fmt.Errorf("receipt WAL Store restore: %w", err)
	}
	candidate.retired, err = newEmptyRetiredCatalogSnapshot(config, audit.highWaterMillis)
	if err != nil {
		_ = closer.Close()
		return nil, fmt.Errorf("receipt WAL retired catalog restore: %w", err)
	}
	if err := graph.CompleteSearchIndexRecovery(); err != nil {
		_ = closer.Close()
		return nil, fmt.Errorf("receipt WAL search rebuild: %w", err)
	}
	if err := closer.Close(); err != nil {
		return nil, fmt.Errorf("receipt WAL close after detached replay: %w", err)
	}
	candidate.log = log
	return candidate, nil
}

func emptyReceiptWALRecoveryGraph(graph *graphcache.GraphCache[string, *pb.Vertex]) bool {
	terms, documents := graph.SearchIndexStats()
	causal := graph.CausalMetadataStats()
	if graph.VertexCount() != 0 || graph.EdgeCount() != 0 || graph.VertexHLCCount() != 0 ||
		terms != 0 || documents != 0 || causal.VertexEntries != 0 || causal.EdgeEntries != 0 {
		return false
	}
	snapshot := graph.SnapshotReplication()
	return len(snapshot.Barriers.Vertices) == 0 && len(snapshot.Barriers.Edges) == 0 &&
		len(snapshot.Tombstones.Vertices) == 0 && len(snapshot.Tombstones.Edges) == 0 &&
		len(snapshot.Graph.Vertices) == 0 && len(snapshot.Graph.Edges) == 0
}

func receiptWALGraphRecoverable(m *pb.Mutation) bool {
	if m == nil || m.GetOp() == nil {
		return false
	}
	switch m.GetOp().GetOp().(type) {
	case *pb.MutationOp_PutVertex, *pb.MutationOp_PutVertices,
		*pb.MutationOp_AddEdge, *pb.MutationOp_AddEdges,
		*pb.MutationOp_PutEdge, *pb.MutationOp_PutEdges,
		*pb.MutationOp_ReplicatedPutVertices, *pb.MutationOp_ReplicatedPutEdges:
		return true
	default:
		return false
	}
}

// replayGraphPutEffect applies only the receiver-local accepted subset. An
// omitted slot must stay omitted even if its original causal fence expired
// before recovery. A formerly live accepted value may now be an expired
// barrier, but an accepted barrier must never become live or be rejected.
func replayGraphPutEffect(graph *graphcache.GraphCache[string, *pb.Vertex], effect *graphPutEffectEnvelope) error {
	if err := validateGraphPutEffectEnvelope(effect); err != nil {
		return err
	}
	if len(effect.Accepted) == 0 {
		return nil
	}
	ts := hlcFromProto(effect.Mutation.GetHlc())
	op := effect.Mutation.GetOp()
	switch op.GetOp().(type) {
	case *pb.MutationOp_PutVertex, *pb.MutationOp_PutVertices, *pb.MutationOp_ReplicatedPutVertices:
		items := make([]graphcache.VertexItem[string, *pb.Vertex], len(effect.Accepted))
		for i, accepted := range effect.Accepted {
			item, err := graphPutReplayVertexItem(op, accepted)
			if err != nil {
				return err
			}
			items[i] = item
		}
		outcomes := graph.PutVerticesWithExpirationHLCOutcomes(items, ts)
		return verifyGraphPutReplayOutcomes(effect.Accepted, outcomes)
	case *pb.MutationOp_PutEdge, *pb.MutationOp_PutEdges, *pb.MutationOp_ReplicatedPutEdges:
		items := make([]graphcache.EdgeItem[string], len(effect.Accepted))
		for i, accepted := range effect.Accepted {
			item, err := graphPutReplayEdgeItem(op, accepted)
			if err != nil {
				return err
			}
			items[i] = item
		}
		outcomes := graph.PutEdgesWithExpirationHLCOutcomes(items, ts)
		return verifyGraphPutReplayOutcomes(effect.Accepted, outcomes)
	default:
		return receiptWALUnionError("graph Put effect has unsupported replay arm %T", op)
	}
}

func graphPutReplayVertexItem(op *pb.MutationOp, accepted graphPutAcceptedEffect) (graphcache.VertexItem[string, *pb.Vertex], error) {
	index := int(accepted.Index)
	var value *pb.Vertex
	var barrier *pb.VertexCausalBarrier
	switch source := op.GetOp().(type) {
	case *pb.MutationOp_PutVertex:
		value = source.PutVertex.GetVertex()
	case *pb.MutationOp_PutVertices:
		value = source.PutVertices.Vertices[index]
	case *pb.MutationOp_ReplicatedPutVertices:
		entry := source.ReplicatedPutVertices.Entries[index]
		value, barrier = entry.GetLive(), entry.GetCausalBarrier()
	}
	item := graphcache.VertexItem[string, *pb.Vertex]{CausalBarrier: accepted.Kind == graphPutEffectBarrier}
	if value != nil {
		item.Key, item.Value, item.Expiration = value.GetKey(), value, prototime.Expiration(value.GetExpiration())
	} else if barrier != nil {
		item.Key = barrier.GetKey()
	} else {
		return item, receiptWALUnionError("accepted graph Vertex Put has no identity at index %d", index)
	}
	return item, nil
}

func graphPutReplayEdgeItem(op *pb.MutationOp, accepted graphPutAcceptedEffect) (graphcache.EdgeItem[string], error) {
	index := int(accepted.Index)
	var value *pb.Edge
	var barrier *pb.EdgeCausalBarrier
	switch source := op.GetOp().(type) {
	case *pb.MutationOp_PutEdge:
		value = source.PutEdge.GetEdge()
	case *pb.MutationOp_PutEdges:
		value = source.PutEdges.Edges[index]
	case *pb.MutationOp_ReplicatedPutEdges:
		entry := source.ReplicatedPutEdges.Entries[index]
		value, barrier = entry.GetLive(), entry.GetCausalBarrier()
	}
	item := graphcache.EdgeItem[string]{CausalBarrier: accepted.Kind == graphPutEffectBarrier}
	if value != nil {
		item.Tail, item.Head, item.Weight = value.GetTail(), value.GetHead(), value.GetWeight()
		item.Expiration = prototime.Expiration(value.GetExpiration())
	} else if barrier != nil {
		item.Tail, item.Head = barrier.GetTail(), barrier.GetHead()
	} else {
		return item, receiptWALUnionError("accepted graph Edge Put has no identity at index %d", index)
	}
	return item, nil
}

func verifyGraphPutReplayOutcomes(accepted []graphPutAcceptedEffect, outcomes []graphcache.PutOutcome) error {
	if len(outcomes) != len(accepted) {
		return receiptWALUnionError("graph Put replay outcome count drift")
	}
	for i, outcome := range outcomes {
		if outcome == graphcache.PutOutcomeAppliedAndLive && accepted[i].Kind == graphPutEffectLive {
			continue
		}
		if outcome == graphcache.PutOutcomeExpired {
			continue
		}
		return receiptWALUnionError("graph Put accepted effect %d at index %d replayed as %d", i, accepted[i].Index, outcome)
	}
	return nil
}

// replayGraphAddEffect applies only the contribution rows accepted by this
// receiver. The original wire index, not the compact accepted-item position,
// determines an unkeyed row's synthesized ContribID. A previously rejected
// row must stay absent even if its causal floor has expired since commit.
func replayGraphAddEffect(graph *graphcache.GraphCache[string, *pb.Vertex], effect *graphAddEffectEnvelope) error {
	if err := validateGraphAddEffectEnvelope(effect); err != nil {
		return err
	}
	if len(effect.AcceptedIndexes) == 0 {
		return nil
	}
	m := effect.Mutation
	items := make([]graphcache.EdgeItem[string], len(effect.AcceptedIndexes))
	for i, index := range effect.AcceptedIndexes {
		var edge *pb.Edge
		var rawID []byte
		switch op := m.GetOp().GetOp().(type) {
		case *pb.MutationOp_AddEdge:
			edge, rawID = op.AddEdge.GetEdge(), op.AddEdge.GetContribId()
		case *pb.MutationOp_AddEdges:
			edge = op.AddEdges.GetEdges()[index]
			if int(index) < len(op.AddEdges.GetContribIds()) {
				rawID = op.AddEdges.GetContribIds()[index]
			}
		case *pb.MutationOp_ReplicatedReceiptEdgeAdd:
			item := op.ReplicatedReceiptEdgeAdd.GetItems()[index]
			edge, rawID = item.GetOriginal(), item.GetContribId()
		default:
			return receiptWALUnionError("graph Add effect has unsupported replay arm %T", op)
		}
		if edge == nil {
			return receiptWALUnionError("accepted graph Add has no Edge at index %d", index)
		}
		id := contribIDFromBytes(rawID)
		if id.IsZero() {
			id = contribIDFor(m.GetOrigin(), m.GetSeq(), uint16(index))
		}
		items[i] = graphcache.EdgeItem[string]{
			Tail: edge.GetTail(), Head: edge.GetHead(), Weight: edge.GetWeight(),
			Expiration: prototime.Expiration(edge.GetExpiration()), ContribID: id,
		}
	}
	_, accepted, _ := graph.AddEdgesWithExpirationContribHLCResults(items, hlcFromProto(m.GetHlc()))
	for i, applied := range accepted {
		if !applied {
			return receiptWALUnionError("graph Add accepted effect %d at index %d replayed as rejected", i, effect.AcceptedIndexes[i])
		}
	}
	return nil
}

// replayGraphDeleteEffect retains only the receiver's accepted exact victims.
// Existed=false does not imply rejection: an accepted absent identity still
// installs a D4 floor. Omitted slots must remain omitted after their old
// causal floor expires, while duplicate accepted positions remain ordered.
func replayGraphDeleteEffect(graph *graphcache.GraphCache[string, *pb.Vertex], effect *graphDeleteEffectEnvelope) error {
	if err := validateGraphDeleteEffectEnvelope(effect); err != nil {
		return err
	}
	if len(effect.AcceptedIndexes) == 0 {
		return nil
	}
	m := effect.Mutation
	retainsTombstone := m.GetTombstoneExpiration() != nil
	deadline, err := mutationTombstoneExpiration(m, retainsTombstone)
	if err != nil {
		return receiptWALUnionError("graph Delete deadline: %v", err)
	}
	ts := hlcFromProto(m.GetHlc())
	var replayed []int
	switch op := m.GetOp().GetOp().(type) {
	case *pb.MutationOp_DeleteVertex:
		keys := []string{op.DeleteVertex.GetKey()}
		if retainsTombstone {
			_, replayed = graph.DeleteVerticesHLCDecisions(keys, ts, deadline)
		} else {
			graph.DeleteVertices(keys)
			replayed = []int{0}
		}
	case *pb.MutationOp_DeleteVertices:
		keys := make([]string, len(effect.AcceptedIndexes))
		for i, index := range effect.AcceptedIndexes {
			keys[i] = op.DeleteVertices.GetKeys()[index]
		}
		if retainsTombstone {
			_, replayed = graph.DeleteVerticesHLCDecisions(keys, ts, deadline)
		} else {
			graph.DeleteVertices(keys)
			replayed = allAcceptedIndexes(len(keys))
		}
	case *pb.MutationOp_DeleteEdge:
		keys := []graphcache.EdgeKey[string]{{Tail: op.DeleteEdge.GetTail(), Head: op.DeleteEdge.GetHead()}}
		if retainsTombstone {
			_, replayed = graph.DeleteEdgesHLCDecisions(keys, ts, deadline)
		} else {
			graph.DeleteEdges(keys)
			replayed = []int{0}
		}
	case *pb.MutationOp_DeleteEdges:
		keys := make([]graphcache.EdgeKey[string], len(effect.AcceptedIndexes))
		for i, index := range effect.AcceptedIndexes {
			key := op.DeleteEdges.GetEdges()[index]
			keys[i] = graphcache.EdgeKey[string]{Tail: key.GetTail(), Head: key.GetHead()}
		}
		if retainsTombstone {
			_, replayed = graph.DeleteEdgesHLCDecisions(keys, ts, deadline)
		} else {
			graph.DeleteEdges(keys)
			replayed = allAcceptedIndexes(len(keys))
		}
	default:
		return receiptWALUnionError("graph Delete effect has unsupported replay arm %T", op)
	}
	if !slices.Equal(replayed, allAcceptedIndexes(len(effect.AcceptedIndexes))) {
		return receiptWALUnionError("graph Delete accepted effect replayed as rejected")
	}
	return nil
}
