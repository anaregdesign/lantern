package security

import (
	"context"
	"testing"
)

func TestCurrentPublicOutputScopeRequiresOwnedRecipientAndExactPolicy(t *testing.T) {
	o, p, _, _ := currentPublicOutputFixture(t)
	w, _, a := currentPublicTestWriter(t, o, p)
	if err := a.BindCurrentSelfOutput(context.Background()); err == nil {
		t.Fatal("current admission detached from actual request recipient")
	}
	if err := a.BindCurrentGlobalOutput(w.ctx, SecurityManage); err != nil {
		t.Fatal(err)
	}
	for _, resources := range [][]PublicOutputResource{
		{{Kind: OutputKey, Key: "private:x", Actions: []Action{VertexRead}}},
		{{Kind: OutputVertexCollection, Actions: []Action{VertexRead}}},
		{{Kind: OutputEdge, Tail: "a", Head: "b", Actions: []Action{EdgeWrite}}},
		{{Kind: OutputReceipt, Key: "a", Actions: []Action{ReceiptRead}}},
		{{Kind: OutputEmpty, Key: "a"}},
	} {
		if err := a.BindCurrentResourceOutput(w.ctx, [32]byte{1}, resources); err == nil {
			t.Fatal("bootstrap security admin gained data/receipt authority", resources)
		}
	}
	if _, err := w.Write([]byte("approved")); err != nil {
		t.Fatal(err)
	}
	if err := a.BindCurrentSelfOutput(w.ctx); err == nil {
		t.Fatal("changed stream resource after first final event")
	}
}
