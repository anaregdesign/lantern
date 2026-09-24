package service

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
	"time"

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
	err = mutationlog.ReplayFileWAL(path, decodeReceiptWALUnion, func(entry mutationlog.Entry) error {
		if err := validateReceiptWALUnionEntry(entry); err != nil {
			return fmt.Errorf("receipt WAL local seq %d: %w", entry.Seq, err)
		}
		var origin hlc.NodeID
		var seq uint64
		var envelope *edgeDeleteReceiptEnvelope
		switch value := entry.Op.(type) {
		case *pb.Mutation:
			copy(origin[:], value.GetOrigin())
			seq = value.GetSeq()
		case *edgeDeleteReceiptEnvelope:
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
