package service

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// ErrDurableReceiptWALBackupFallbackEligible marks the narrow restart failure
// class that a fully validated backup set may repair: the newest committed
// baseline marker exists, but its content-addressed sidecar is missing or
// damaged. All WAL, lease, identity, policy, epoch, and generation failures
// remain ineligible.
var ErrDurableReceiptWALBackupFallbackEligible = errors.New(
	"service: durable receipt WAL backup fallback eligible",
)

type receiptBaselineWALScan struct {
	marker           receiptBaselineMarker
	markerSequence   uint64
	firstGeneration  [16]byte
	activeGeneration [16]byte
	hasMarker        bool
}

func scanReceiptBaselineWAL(
	path string,
	config mutationreceipt.Config,
	nodeID hlc.NodeID,
) (receiptBaselineWALScan, error) {
	store, err := mutationreceipt.New(config)
	if err != nil {
		return receiptBaselineWALScan{}, err
	}
	policy := store.PolicyFingerprint()
	var scan receiptBaselineWALScan
	seenGenerations := make(map[[16]byte]struct{})
	err = mutationlog.ReplayFileWAL(path, decodeReceiptWALUnion, func(entry mutationlog.Entry) error {
		if err := validateReceiptWALUnionEntry(entry); err != nil {
			return fmt.Errorf("receipt WAL local seq %d: %w", entry.Seq, err)
		}
		marker, ok := entry.Op.(receiptBaselineMarker)
		if !ok {
			return nil
		}
		if marker.Epoch != config.Epoch || marker.PolicyFingerprint != policy ||
			marker.RestoreFloor.NodeID != nodeID {
			return fmt.Errorf("receipt WAL local seq %d: %w: marker policy, epoch, or local node mismatch", entry.Seq, errReceiptBaselineMarker)
		}
		if !scan.hasMarker {
			scan.firstGeneration = marker.PreviousGeneration
		} else {
			if marker.PreviousGeneration != scan.activeGeneration {
				return fmt.Errorf("receipt WAL local seq %d: %w: marker generation chain mismatch", entry.Seq, errReceiptBaselineMarker)
			}
			if marker.ReceiptHighWaterMillis < scan.marker.ReceiptHighWaterMillis {
				return fmt.Errorf("receipt WAL local seq %d: %w: marker receipt high-water regressed", entry.Seq, errReceiptBaselineMarker)
			}
			if marker.RestoreFloor.Less(scan.marker.RestoreFloor) {
				return fmt.Errorf("receipt WAL local seq %d: %w: marker restore floor regressed", entry.Seq, errReceiptBaselineMarker)
			}
		}
		if _, reused := seenGenerations[marker.RotatedGeneration]; reused ||
			marker.RotatedGeneration == scan.firstGeneration {
			return fmt.Errorf("receipt WAL local seq %d: %w: marker generation was reused", entry.Seq, errReceiptBaselineMarker)
		}
		seenGenerations[marker.RotatedGeneration] = struct{}{}
		scan.marker = marker
		scan.markerSequence = entry.Seq
		scan.activeGeneration = marker.RotatedGeneration
		scan.hasMarker = true
		return nil
	})
	if err != nil {
		return receiptBaselineWALScan{}, err
	}
	return scan, nil
}

func resumeReceiptBaselineWALCandidate(
	ctx context.Context,
	path string,
	config mutationreceipt.Config,
	now time.Time,
	opts mutationlog.Options,
	defaultTTL time.Duration,
	configureGraph func(*graphcache.GraphCache[string, *pb.Vertex]) error,
	codec ReceiptBaselineArchiveCodec,
	journalHighWater int64,
	tip *mutationlog.FileWALTipJournal,
	scan receiptBaselineWALScan,
) (_ *receiptWALRecoveryCandidate, _ io.Closer, err error) {
	if codec == nil || !scan.hasMarker || tip == nil {
		return nil, nil, errors.New("service: baseline recovery requires a marker, codec, and tip journal")
	}
	sidecars := receiptBaselineSidecarStore{walPath: path}
	raw, err := sidecars.load(scan.marker.Format, scan.marker.Digest, scan.marker.Size)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, errReceiptBaselineSidecar) {
			return nil, nil, fmt.Errorf(
				"%w: load committed receipt baseline at WAL seq %d: %w",
				ErrDurableReceiptWALBackupFallbackEligible,
				scan.markerSequence,
				err,
			)
		}
		return nil, nil, fmt.Errorf("service: load committed receipt baseline at WAL seq %d: %w", scan.markerSequence, err)
	}
	policy := config
	policy.ClockHighWater = time.Time{}
	baseline, err := codec.StageCombinedReceiptBaseline(
		ctx,
		raw,
		policy,
		defaultTTL,
		configureGraph,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("service: stage committed receipt baseline at WAL seq %d: %w", scan.markerSequence, err)
	}
	if baseline == nil || baseline.Graph == nil || baseline.Receipts == nil ||
		baseline.Policy.Epoch != scan.marker.Epoch ||
		baseline.Receipts.PolicyFingerprint() != scan.marker.PolicyFingerprint ||
		baseline.CutoffLocalSeq != scan.marker.SourceLocalCutoff ||
		!baseline.CutoffHLC.Equal(scan.marker.SnapshotHLC) {
		return nil, nil, fmt.Errorf("%w: committed marker provenance differs from sidecar", errReceiptBaselineMarker)
	}
	receiptSnapshot, err := baseline.Receipts.Snapshot()
	if err != nil {
		return nil, nil, fmt.Errorf("service: read staged baseline receipts: %w", err)
	}
	if scan.marker.ReceiptHighWaterMillis != receiptSnapshot.ClockHighWaterMillis {
		return nil, nil, fmt.Errorf("%w: marker and sidecar receipt high-water differ", errReceiptBaselineMarker)
	}
	return resumeStagedReceiptBaselineWALCandidate(
		ctx,
		path,
		config,
		now,
		opts,
		codec,
		journalHighWater,
		tip,
		baseline,
		scan.marker.RestoreFloor,
		scan.markerSequence,
	)
}

func resumeStagedReceiptBaselineWALCandidate(
	ctx context.Context,
	path string,
	config mutationreceipt.Config,
	now time.Time,
	opts mutationlog.Options,
	codec ReceiptBaselineArchiveCodec,
	journalHighWater int64,
	tip *mutationlog.FileWALTipJournal,
	baseline *ReceiptBaselineCandidate,
	restoreFloor hlc.Timestamp,
	boundarySequence uint64,
) (_ *receiptWALRecoveryCandidate, _ io.Closer, err error) {
	if codec == nil || tip == nil || baseline == nil ||
		baseline.Graph == nil || baseline.Receipts == nil {
		return nil, nil, errors.New("service: staged baseline recovery requires a complete baseline, codec, and tip journal")
	}
	policy := clearReceiptClockHighWater(config)
	originTracker := newOriginStateTracker()
	originStage, err := originTracker.stageWholeState(baseline.Origins)
	if err != nil {
		return nil, nil, fmt.Errorf("service: stage baseline origins: %w", err)
	}
	originStage.Commit()

	receiptSnapshot, err := baseline.Receipts.Snapshot()
	if err != nil {
		return nil, nil, fmt.Errorf("service: read staged baseline receipts: %w", err)
	}
	if baseline.Retired.ClockHighWaterMillis != receiptSnapshot.ClockHighWaterMillis {
		return nil, nil, fmt.Errorf("%w: active and retired sidecar high-water differs", errReceiptBaselineMarker)
	}
	replay := &receiptBaselineSuffixReplay{
		graph:             baseline.Graph,
		origins:           originTracker,
		config:            policy,
		receipts:          make(map[mutationreceipt.ID]mutationreceipt.Receipt, len(receiptSnapshot.Receipts)),
		seenReceipts:      make(map[mutationreceipt.ID]mutationreceipt.Receipt, len(receiptSnapshot.Receipts)),
		policyFingerprint: baseline.Receipts.PolicyFingerprint(),
		highWater:         receiptSnapshot.ClockHighWaterMillis,
		frontier:          restoreFloor,
	}
	if now.IsZero() {
		now = time.Now()
	}
	if now.UnixMilli() < 0 || journalHighWater < 0 {
		return nil, nil, mutationreceipt.ErrInvalidClock
	}
	if replay.highWater < now.UnixMilli() {
		replay.highWater = now.UnixMilli()
	}
	if replay.highWater < journalHighWater {
		replay.highWater = journalHighWater
	}
	for _, receipt := range receiptSnapshot.Receipts {
		replay.seenReceipts[receipt.ID] = receipt
		if receipt.DeadlineMillis <= replay.highWater {
			continue
		}
		replay.receipts[receipt.ID] = receipt
		replay.receiptBytes += receiptWALDecisionCost(receipt)
	}

	var liveLog *mutationlog.Log
	var owner io.Closer
	if boundarySequence == 0 {
		liveLog, owner, err = mutationlog.ResumeLogFromFileWALWithTip(
			path,
			opts,
			encodeReceiptWALUnion,
			decodeReceiptWALUnion,
			validateReceiptWALUnionEntry,
			replay.apply,
			tip,
		)
	} else {
		liveLog, owner, err = mutationlog.ResumeLogFromFileWALWithTipSuffix(
			path,
			opts,
			encodeReceiptWALUnion,
			decodeReceiptWALUnion,
			validateReceiptWALUnionEntry,
			replay.apply,
			tip,
			boundarySequence,
		)
	}
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		if err != nil {
			err = errors.Join(err, owner.Close())
		}
	}()
	finalSnapshot := mutationreceipt.Snapshot{
		Version:              receiptSnapshot.Version,
		Epoch:                receiptSnapshot.Epoch,
		PolicyFingerprint:    receiptSnapshot.PolicyFingerprint,
		ClockHighWaterMillis: replay.highWater,
		Receipts:             make([]mutationreceipt.Receipt, 0, len(replay.receipts)),
	}
	for _, receipt := range replay.receipts {
		if receipt.DeadlineMillis > replay.highWater {
			finalSnapshot.Receipts = append(finalSnapshot.Receipts, receipt)
		}
	}
	sort.Slice(finalSnapshot.Receipts, func(i, j int) bool {
		return bytes.Compare(finalSnapshot.Receipts[i].ID[:], finalSnapshot.Receipts[j].ID[:]) < 0
	})
	finalConfig := policy
	finalConfig.ClockHighWater = time.UnixMilli(replay.highWater)
	receipts, err := mutationreceipt.NewFromSnapshot(finalConfig, finalSnapshot)
	if err != nil {
		return nil, nil, fmt.Errorf("service: restore baseline receipt suffix: %w", err)
	}
	retiredConfig, _, err := retiredCatalogConfig(policy, replay.highWater)
	if err != nil {
		return nil, nil, fmt.Errorf("service: configure restored retired receipt catalog: %w", err)
	}
	retired, err := mutationreceipt.NewRetiredCatalogFromSnapshot(
		retiredConfig,
		baseline.Retired,
	)
	if err != nil {
		return nil, nil, fmt.Errorf("service: restore retired receipt catalog: %w", err)
	}
	retiredSnapshot, err := retired.Snapshot(time.UnixMilli(replay.highWater))
	if err != nil {
		return nil, nil, fmt.Errorf("service: snapshot restored retired receipt catalog: %w", err)
	}
	if err := baseline.Graph.CompleteSearchIndexRecovery(); err != nil {
		return nil, nil, fmt.Errorf("service: rebuild baseline suffix search index: %w", err)
	}
	return &receiptWALRecoveryCandidate{
		graph:       baseline.Graph,
		receipts:    receipts,
		retired:     retiredSnapshot,
		origins:     originTracker,
		log:         liveLog,
		hlcFrontier: replay.frontier,
	}, owner, nil
}

type receiptBaselineSuffixReplay struct {
	graph             *graphcache.GraphCache[string, *pb.Vertex]
	origins           *originStateTracker
	config            mutationreceipt.Config
	receipts          map[mutationreceipt.ID]mutationreceipt.Receipt
	seenReceipts      map[mutationreceipt.ID]mutationreceipt.Receipt
	policyFingerprint [32]byte
	receiptBytes      uint64
	highWater         int64
	frontier          hlc.Timestamp
}

func (r *receiptBaselineSuffixReplay) apply(entry mutationlog.Entry) error {
	if wallMS := entry.HLC.WallNs / int64(time.Millisecond); wallMS > r.highWater {
		r.advanceHighWater(wallMS)
	}
	var origin hlc.NodeID
	var originSequence uint64
	if envelope, ok := receiptWALEnvelopeInfo(entry.Op); ok {
		origin, originSequence = envelope.origin, envelope.originSeq
		if envelope.epoch != r.config.Epoch || envelope.policy != r.policyFingerprint {
			return fmt.Errorf("receipt WAL local seq %d: %w: receipt policy or epoch mismatch", entry.Seq, errReceiptWALUnion)
		}
		if err := replayReceiptEnvelopeGraph(r.graph, entry.Op); err != nil {
			return fmt.Errorf("receipt WAL local seq %d: receipt graph replay: %w", entry.Seq, err)
		}
		for _, receipt := range envelope.receipts {
			issued := int64(binary.BigEndian.Uint64(receipt.ID[17:25]))
			if receipt.DeadlineMillis-issued != r.config.Retention.Milliseconds() {
				return fmt.Errorf("receipt WAL local seq %d: %w: retention differs from policy", entry.Seq, errReceiptWALUnion)
			}
			if previous, duplicate := r.seenReceipts[receipt.ID]; duplicate {
				if !sameReceiptWALDecision(previous, receipt) {
					return fmt.Errorf("receipt WAL local seq %d: %w: conflicting duplicate operation ID %x", entry.Seq, mutationreceipt.ErrInvalidSnapshot, receipt.ID)
				}
				continue
			}
			r.seenReceipts[receipt.ID] = receipt
			if receipt.DeadlineMillis <= r.highWater {
				continue
			}
			cost := receiptWALDecisionCost(receipt)
			if len(r.receipts) >= r.config.MaxEntries || cost > r.config.MaxBytes-r.receiptBytes {
				return fmt.Errorf("receipt WAL local seq %d: %w", entry.Seq, mutationreceipt.ErrCapacity)
			}
			r.receipts[receipt.ID] = receipt
			r.receiptBytes += cost
		}
	} else {
		switch value := entry.Op.(type) {
		case *pb.Mutation:
			return fmt.Errorf("receipt WAL local seq %d: %w: raw graph mutation after baseline", entry.Seq, errReceiptWALUnion)
		case *graphDeleteEffectEnvelope:
			copy(origin[:], value.Mutation.GetOrigin())
			originSequence = value.Mutation.GetSeq()
			if err := replayGraphDeleteEffect(r.graph, value); err != nil {
				return fmt.Errorf("receipt WAL local seq %d: graph Delete effect replay: %w", entry.Seq, err)
			}
		case *graphPutEffectEnvelope:
			copy(origin[:], value.Mutation.GetOrigin())
			originSequence = value.Mutation.GetSeq()
			if err := replayGraphPutEffect(r.graph, value); err != nil {
				return fmt.Errorf("receipt WAL local seq %d: graph Put effect replay: %w", entry.Seq, err)
			}
		case *graphAddEffectEnvelope:
			copy(origin[:], value.Mutation.GetOrigin())
			originSequence = value.Mutation.GetSeq()
			if err := replayGraphAddEffect(r.graph, value); err != nil {
				return fmt.Errorf("receipt WAL local seq %d: graph Add effect replay: %w", entry.Seq, err)
			}
		case receiptBaselineMarker:
			return fmt.Errorf("receipt WAL local seq %d: %w: later baseline escaped marker selection", entry.Seq, errReceiptBaselineMarker)
		default:
			return fmt.Errorf("receipt WAL local seq %d: %w: unknown operation", entry.Seq, errReceiptWALUnion)
		}
	}
	if !r.origins.Record(origin, originSequence, entry.HLC) {
		return fmt.Errorf("receipt WAL local seq %d: %w: origin frontier drift", entry.Seq, errReceiptWALUnion)
	}
	if r.frontier.Less(entry.HLC) {
		r.frontier = entry.HLC
	}
	return nil
}

func (r *receiptBaselineSuffixReplay) advanceHighWater(highWater int64) {
	r.highWater = highWater
	for id, receipt := range r.receipts {
		if receipt.DeadlineMillis <= highWater {
			delete(r.receipts, id)
			r.receiptBytes -= receiptWALDecisionCost(receipt)
		}
	}
}
