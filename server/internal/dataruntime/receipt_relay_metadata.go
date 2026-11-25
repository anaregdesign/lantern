package dataruntime

import (
	"bytes"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

type receiptWALEnvelopeMetadata struct {
	origin    hlc.NodeID
	originSeq uint64
	epoch     mutationreceipt.Epoch
	policy    [32]byte
	receipts  []mutationreceipt.Receipt
}

func receiptWALEnvelopeInfo(op mutationlog.MutationOp) (receiptWALEnvelopeMetadata, bool) {
	switch value := op.(type) {
	case *graphMutationEnvelope:
		m := value.Mutation
		call := m.GetOp().GetGraphMutation()
		metadata := receiptWALEnvelopeMetadata{originSeq: m.GetSeq(), receipts: value.Receipts}
		copy(metadata.origin[:], m.GetOrigin())
		copy(metadata.epoch[:], call.GetDeploymentEpoch())
		copy(metadata.policy[:], call.GetPolicyFingerprint())
		return metadata, true
	case *edgeCreateEnvelope:
		if len(value.Receipts) == 0 {
			return receiptWALEnvelopeMetadata{}, false
		}
		m := value.Mutation
		call := m.GetOp().GetEdgeCreateEffect()
		metadata := receiptWALEnvelopeMetadata{originSeq: m.GetSeq(), receipts: value.Receipts}
		copy(metadata.origin[:], m.GetOrigin())
		copy(metadata.epoch[:], call.GetDeploymentEpoch())
		copy(metadata.policy[:], call.GetPolicyFingerprint())
		return metadata, true
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
	case *edgeContributionDeleteReceiptEnvelope:
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

func sameReceiptWALDecision(a, b mutationreceipt.Receipt) bool {
	return a.Intent == b.Intent &&
		a.LifecycleReduction == b.LifecycleReduction &&
		a.DeadlineMillis == b.DeadlineMillis &&
		bytes.Equal(a.Result, b.Result)
}

func emptyReceiptGraph(graph *graphcache.GraphCache[string, *pb.Vertex]) bool {
	terms, documents := graph.SearchIndexStats()
	causal := graph.CausalMetadataStats()
	if graph.VertexCount() != 0 || graph.EdgeCount() != 0 || graph.VertexHLCCount() != 0 ||
		terms != 0 || documents != 0 || causal.VertexEntries != 0 || causal.EdgeEntries != 0 {
		return false
	}
	snapshot := graph.SnapshotReplication()
	return len(snapshot.Barriers.Vertices) == 0 && len(snapshot.Barriers.Edges) == 0 &&
		len(snapshot.Tombstones.Vertices) == 0 && len(snapshot.Tombstones.Edges) == 0 &&
		len(snapshot.Tombstones.EdgeContributions) == 0 &&
		len(snapshot.Graph.Vertices) == 0 && len(snapshot.Graph.Edges) == 0
}
