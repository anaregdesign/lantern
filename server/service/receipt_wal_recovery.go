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
	report := receiptWALDecisionAudit{}
	var knownBytes uint64
	seenReceipt := false
	err = mutationlog.ReplayFileWAL(path, decodeReceiptWALUnion, func(entry mutationlog.Entry) error {
		if err := validateReceiptWALUnionEntry(entry); err != nil {
			return fmt.Errorf("receipt WAL local seq %d: %w", entry.Seq, err)
		}
		var origin hlc.NodeID
		var seq uint64
		var envelope *edgeDeleteReceiptEnvelope
		switch value := entry.Op.(type) {
		case *pb.Mutation:
			if isAnyGraphPut(value) {
				if seenReceipt {
					return fmt.Errorf("receipt WAL local seq %d: %w: graph Put lacks receiver-local accepted-effect evidence after receipt", entry.Seq, errReceiptWALUnion)
				}
				report.unprovenGraphPutRows++
			}
			copy(origin[:], value.GetOrigin())
			seq = value.GetSeq()
		case *graphPutEffectEnvelope:
			copy(origin[:], value.Mutation.GetOrigin())
			seq = value.Mutation.GetSeq()
			report.evidencedGraphPutRows++
		case *graphDeleteEffectEnvelope:
			copy(origin[:], value.Mutation.GetOrigin())
			seq = value.Mutation.GetSeq()
		case *edgeDeleteReceiptEnvelope:
			seenReceipt = true
			origin, seq = value.Origin, value.OriginSeq
			if value.Epoch != config.Epoch || value.PolicyFingerprint != base.PolicyFingerprint() {
				return fmt.Errorf("receipt WAL local seq %d: %w: epoch or policy mismatch", entry.Seq, errReceiptWALUnion)
			}
			envelope = value
		default:
			return fmt.Errorf("receipt WAL local seq %d: %w: unknown operation", entry.Seq, errReceiptWALUnion)
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
		if envelope != nil {
			for _, receipt := range envelope.Receipts {
				// Check every committed row, including rows now expired.
				// Snapshot restore only sees the retained subset below.
				issued := int64(binary.BigEndian.Uint64(receipt.ID[17:25]))
				if receipt.DeadlineMillis-issued != config.Retention.Milliseconds() {
					return fmt.Errorf("receipt WAL local seq %d: %w: retention differs from policy", entry.Seq, errReceiptWALUnion)
				}
				// Expired results are never returned as confirmed. The ID and
				// retention policy still make them non-executable, but this
				// audit cannot answer any absent-ID status.
				if receipt.DeadlineMillis <= highWater {
					continue
				}
				// Edge Delete has no contribution binding. Match the Store's
				// current logical byte ledger before retaining another row;
				// NewFromSnapshot verifies the ledger again at the end.
				cost := receiptWALDecisionCost(receipt)
				if len(report.knownReceipts) >= config.MaxEntries || cost > config.MaxBytes-knownBytes {
					return fmt.Errorf("receipt WAL local seq %d: %w", entry.Seq, mutationreceipt.ErrCapacity)
				}
				knownBytes += cost
				copyOf := receipt
				copyOf.Result = append([]byte(nil), receipt.Result...)
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

// receiptWALRecoveryCandidate is deliberately detached from LanternService.
// Its Store contains only WAL-observed results: Begin and an absent-ID Lookup
// are not certified because the previous process could advance Store's clock
// without logging it. No provider may install this candidate as a serving
// state until epoch/high-water recovery and an atomic publication cut exist.
type receiptWALRecoveryCandidate struct {
	graph       *graphcache.GraphCache[string, *pb.Vertex]
	receipts    *mutationreceipt.Store
	knownIDs    map[mutationreceipt.ID]struct{}
	origins     *originStateTracker
	log         *mutationlog.Log
	hlcFrontier hlc.Timestamp // evidence for a future restored Clock, not a live Clock
}

// knownReceiptStatus can confirm an exact retained result. An absent ID is
// always UNKNOWN after restart, including an otherwise fresh ID in the old
// epoch; WAL rows alone cannot prove the previous Store clock high-water.
func (c *receiptWALRecoveryCandidate) knownReceiptStatus(id mutationreceipt.ID, now time.Time) (mutationreceipt.Status, mutationreceipt.Receipt, error) {
	if _, known := c.knownIDs[id]; !known {
		return mutationreceipt.NoLongerProvable, mutationreceipt.Receipt{}, nil
	}
	status, receipt, err := c.receipts.Lookup(id, now)
	if status == mutationreceipt.NotYetObserved {
		return mutationreceipt.NoLongerProvable, mutationreceipt.Receipt{}, nil
	}
	return status, receipt, err
}

// resumeReceiptWALCandidate validates a complete genesis WAL before making
// any state externally visible. The caller must own path exclusively through
// both replay passes; this function closes the resumed writer before return.
// A graph-only exact Delete now has an absolute deadline and a private
// accepted-index envelope, and graph Put has an accepted-effect envelope,
// but this candidate cannot safely replay either yet:
// a later graph Put/Add may have been rejected by a floor that has since
// expired. Predicate-shaped prefix Delete is still unrepresentable; prefix
// origins publish exact victim batches instead. Graph writes after the first
// receipt Delete remain refused for the same historical-acceptance reason.
// Receipt Edge Deletes can form a suffix when their projection is reproducible.
//
// The recovered Log and FileWAL are closed before return. This read-only
// candidate does not authorize receipt admission, an absent-ID answer, or
// publication-fault clearing. A future full mixed-WAL format must record
// accepted graph effects for every dependent graph write before lifting these
// restrictions.
func resumeReceiptWALCandidate(path string, config mutationreceipt.Config, now time.Time, opts mutationlog.Options, defaultTTL time.Duration) (*receiptWALRecoveryCandidate, error) {
	audit, err := auditReceiptDecisionsFromFileWAL(path, config, now)
	if err != nil {
		return nil, err
	}
	graph := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](defaultTTL)
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
			// The indexed sidecar preserves the origin's decision, but this
			// candidate has no historical-time/effect replay for later graph
			// Put/Add. Never turn a private codec seam into serving recovery.
			return fmt.Errorf("receipt WAL local seq %d: %w: graph Delete effects are not replayable yet", entry.Seq, errReceiptWALUnion)
		case *edgeDeleteReceiptEnvelope:
			seenReceipt = true
			origin, seq = value.Origin, value.OriginSeq
			// Recheck the exact accepted projection against the detached
			// graph. A stale receipt HLC may lose to an earlier WAL Put;
			// DeleteEdgesHLCChecked would silently skip it while the Store
			// still returned the forged original result as Confirmed.
			tx, err := graph.BeginEdgeDelete(value.OriginalKeys, value.HLC, value.TombstoneExpiration)
			if err != nil {
				return fmt.Errorf("receipt WAL local seq %d: receipt graph replay: %w", entry.Seq, err)
			}
			if !slices.Equal(tx.Result().Accepted, value.Accepted) {
				tx.Abort()
				return fmt.Errorf("receipt WAL local seq %d: %w: accepted Edge Delete projection drift", entry.Seq, errReceiptWALUnion)
			}
			tx.Commit()
		default:
			return fmt.Errorf("receipt WAL local seq %d: %w: unknown operation", entry.Seq, errReceiptWALUnion)
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
	candidate.knownIDs = make(map[mutationreceipt.ID]struct{}, len(audit.knownReceipts))
	for _, receipt := range audit.knownReceipts {
		candidate.knownIDs[receipt.ID] = struct{}{}
	}
	if err := closer.Close(); err != nil {
		return nil, fmt.Errorf("receipt WAL close after detached replay: %w", err)
	}
	candidate.log = log
	return candidate, nil
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
