package service

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
)

func TestReceiptResourceIdentityIsLogicalAndProven(t *testing.T) {
	resource, err := receiptResourceIdentity(keyspace.Version, "data:sys:tail", "data:data:head")
	if err != nil || resource != (mutationreceipt.ResourceIdentity{Key: "sys:tail", Head: "data:head"}) {
		t.Fatal("namespace conversion", err)
	}
	for _, key := range []string{"sys:tail", "plain", "data:"} {
		if _, err := receiptResourceIdentity(keyspace.Version, key, ""); err == nil {
			t.Fatal("invalid physical provenance", key)
		}
	}
	if resource, err := receiptResourceIdentity("", "legacy", ""); err != nil || resource != (mutationreceipt.ResourceIdentity{}) {
		t.Fatal("legacy evidence silently certified")
	}
	row := mutationreceipt.Receipt{Intent: mutationreceipt.Intent{Resource: resource}}
	if !receiptResourceMatches(row, keyspace.Version, "data:sys:tail", "data:data:head") || receiptResourceMatches(row, keyspace.Version, "data:sys:other", "data:data:head") {
		t.Fatal("resource can be replaced")
	}
}

func TestReceiptResourceSurvivesAllWALFamiliesAndRestart(t *testing.T) {
	config := durableRuntimeTestConfig(filepath.Join(t.TempDir(), "receipts.wal"))
	config.NamespaceFormat = keyspace.Version
	runtime, err := CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	svc := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
	peer, err := runtime.NewLanternReplicationService(svc)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CertifyInstallation(svc, peer); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CertifyReceiptBackup(svc, peer); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ActivatePublicReceipts(svc, peer); err != nil {
		t.Fatal(err)
	}
	var ids []mutationreceipt.ID
	var want []mutationreceipt.ResourceIdentity
	contribution := graphcache.ContribID{19}
	for i, kind := range []mutationreceipt.Kind{mutationreceipt.PutVertex, mutationreceipt.DeleteVertex, mutationreceipt.AddEdge, mutationreceipt.DeleteEdgeContribution, mutationreceipt.DeleteEdge} {
		ctx := receiptBenchmarkContext(t, runtime, time.Now(), uint64(i))
		id, err := mutationreceipt.DecodeID(ctx.OperationIds[0])
		if err != nil {
			t.Fatal(err)
		}
		resource := mutationreceipt.ResourceIdentity{Key: "sys:public"}
		switch kind {
		case mutationreceipt.PutVertex:
			_, err = svc.PutVertices(t.Context(), &pb.PutVerticesRequest{ReceiptContext: ctx, Vertices: []*pb.Vertex{{Key: "data:sys:public"}}})
		case mutationreceipt.DeleteVertex:
			_, err = svc.DeleteVertices(t.Context(), &pb.DeleteVerticesRequest{ReceiptContext: ctx, Keys: []string{"data:sys:public"}})
		case mutationreceipt.AddEdge:
			resource.Head = "data:head"
			_, err = svc.AddEdges(t.Context(), &pb.AddEdgesRequest{ReceiptContext: ctx, ContribIds: [][]byte{contribution[:]}, Edges: []*pb.Edge{{Tail: "data:sys:public", Head: "data:data:head", Weight: 3}}})
		case mutationreceipt.DeleteEdgeContribution:
			resource.Head = "data:head"
			_, err = svc.DeleteEdgeContributions(t.Context(), &pb.DeleteEdgeContributionsRequest{ReceiptContext: ctx, Contributions: []*pb.EdgeContributionKey{{Tail: "data:sys:public", Head: "data:data:head", ContribId: contribution[:]}}})
		case mutationreceipt.DeleteEdge:
			resource.Head = "data:head"
			_, err = svc.DeleteEdges(t.Context(), &pb.DeleteEdgesRequest{ReceiptContext: ctx, Edges: []*pb.EdgeKey{{Tail: "data:sys:public", Head: "data:data:head"}}})
		}
		if err != nil {
			t.Fatal(kind, err)
		}
		entry := mustMutationLogEntry(t, runtime.log, uint64(i+1))
		raw, err := encodeReceiptWALUnion(entry.Op)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeReceiptWALUnion(raw)
		if err != nil {
			t.Fatal(err)
		}
		metadata, known := receiptWALEnvelopeInfo(decoded)
		if !known || len(metadata.receipts) != 1 || metadata.receipts[0].Resource != resource {
			t.Fatal("WAL lost original resource", kind)
		}
		row, err := receiptSnapshotRow(metadata.receipts[0])
		if err != nil {
			t.Fatal(err)
		}
		restored, err := receiptFromSnapshotRow(row)
		if err != nil || restored.Resource != resource {
			t.Fatal("Snapshot lost resource", err)
		}
		ids = append(ids, id)
		want = append(want, resource)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	resumed, err := OpenDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	for i, id := range ids {
		status, row, err := resumed.receipt.store.Lookup(id, time.Now())
		if err != nil || status != mutationreceipt.Confirmed || row.Resource != want[i] {
			t.Fatal("restart lost original resource", i, err)
		}
	}
}
