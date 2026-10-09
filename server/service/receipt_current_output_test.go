package service

import (
	"bytes"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	"github.com/anaregdesign/lantern/server/internal/security"
	"slices"
	"testing"
)

func TestCurrentReceiptOutputBindsOriginalAndBlindProjection(t *testing.T) {
	original := mutationreceipt.Observation{Status: mutationreceipt.Confirmed, Receipt: mutationreceipt.Receipt{Intent: mutationreceipt.Intent{Kind: mutationreceipt.AddEdge, Resource: mutationreceipt.ResourceIdentity{Key: "orders:a", Head: "orders:b"}}, Result: []byte{0, 0, 128, 127}}}
	for _, blind := range []bool{true, false} {
		resources, err := currentReceiptOutputResources([]mutationreceipt.Observation{original}, blind)
		if err != nil || len(resources) != 1 {
			t.Fatal(err)
		}
		r := resources[0]
		if r.Kind != security.OutputReceipt || r.Key != "orders:a" || r.Head != "orders:b" || r.Provenance == [32]byte{} || !slices.Contains(r.Actions, security.EdgeAdd) || slices.Contains(r.Actions, security.EdgeRead) == blind {
			t.Fatal("receipt authority projection", r)
		}
		copy := original
		copy.Receipt.Result = bytes.Clone(original.Receipt.Result)
		copy.Receipt.Result[0]++
		changed, err := currentReceiptOutputResources([]mutationreceipt.Observation{copy}, blind)
		if err != nil || changed[0].Provenance == r.Provenance {
			t.Fatal("original encoded nonfinite result lost", err)
		}
	}
	for _, status := range []mutationreceipt.Status{mutationreceipt.NotYetObserved, mutationreceipt.NoLongerProvable} {
		r, err := currentReceiptOutputResources([]mutationreceipt.Observation{{Status: status}}, true)
		if err != nil || !slices.Contains(r[0].Actions, security.VertexRead) || !slices.Contains(r[0].Actions, security.EdgeRead) {
			t.Fatal("unknown receipt disclosed without whole-domain read", err)
		}
	}
	original.Receipt.Kind = mutationreceipt.Kind(255)
	if _, err := currentReceiptOutputResources([]mutationreceipt.Observation{original}, false); err == nil {
		t.Fatal("unknown original kind accepted")
	}
}
