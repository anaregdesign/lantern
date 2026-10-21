package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"connectrpc.com/connect"
	graphv1 "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestProtectedEdgeAddIntentBindsExistingEndpointEffect(t *testing.T) {
	t.Parallel()
	contribID := bytes.Repeat([]byte{0xa5}, 24)
	edge := &graphv1.Edge{Tail: "t", Head: "h", Weight: 1.25}
	got, err := protectedEdgeAddIntentSHA256(edge, contribID)
	if err != nil {
		t.Fatal(err)
	}
	// Independent canonical bytes for Add(t,h,1.25,no expiration,24*a5),
	// followed by the immutable protected effect domain used by the server.
	canonical, err := hex.DecodeString("03" + "0000000000000001" + "74" + "0000000000000001" + "68" +
		"3fa00000" + "00" + "0000000000000018" + strings.Repeat("a5", 24))
	if err != nil {
		t.Fatal(err)
	}
	base := sha256.Sum256(append([]byte("lantern-receipt-intent-v1\x00"), canonical...))
	want := sha256.Sum256(append([]byte("lantern-receipt-intent-v1\x00edge-only-v1\x00"), base[:]...))
	if got != want || got == base {
		t.Fatalf("protected receipt intent = %x, want %x, distinct from legacy %x", got, want, base)
	}
	operation := testReceiptOperation(t, receiptEdgeAdd)
	operation.intentSHA256 = got
	response := confirmedResponse(operation)
	if err := verifyConfirmedStatus(connect.NewResponse(response), operation); err != nil {
		t.Fatalf("protected digest did not pass exact receipt verification: %v", err)
	}
	response.Status.Receipt.IntentSha256 = base[:]
	if err := verifyConfirmedStatus(connect.NewResponse(response), operation); err == nil {
		t.Fatal("protected receipt verification accepted the legacy endpoint-creating digest")
	}
}

func TestProtectedEdgeAddIntentPreservesSourceValidation(t *testing.T) {
	t.Parallel()
	digest, err := protectedEdgeAddIntentSHA256(nil, bytes.Repeat([]byte{1}, 24))
	if err == nil || digest != ([sha256.Size]byte{}) {
		t.Fatalf("invalid source produced protected intent: %x, %v", digest, err)
	}
}
