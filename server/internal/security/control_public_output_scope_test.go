package security

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/server/decisiontrace"
)

func TestCurrentOutputRefusalTracePreservesSanitizationAndNativeSample(t *testing.T) {
	var absent *currentOutputRequest
	if _, err := absent.authorize(t.Context(), currentOutputGrant{}); !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatal("diagnostic changed absent request rejection", err)
	}
	o, p, _, ticks := currentPublicOutputFixture(t)
	baseline, _, _ := currentPublicTestWriter(t, o, p)
	w, recorder, _ := currentPublicTestWriter(t, o, p)
	ticks.Add(uint64(20 * time.Second))
	calls := 0
	sample := o.origin.network.timeOwner.producer.sample
	o.origin.network.timeOwner.producer.sample = func() (authorityTimeStamp, error) { calls++; return sample() }
	const privateMessage = "SYNTHETIC_PRIVATE_OUTPUT_CREDENTIAL"
	baselineErr := p.responseError(baseline.ctx, connect.NewError(connect.CodeUnavailable, errors.New(privateMessage)))
	baselineCalls := calls
	calls = 0
	capture := decisiontrace.Start(16)
	if capture == nil {
		t.Fatal("capture already owned")
	}
	defer capture.Stop()
	err := p.responseError(w.ctx, connect.NewError(connect.CodeUnavailable, errors.New(privateMessage)))
	capture.Stop()
	events, dropped := capture.Drain()
	var refusal, renewal bool
	for _, event := range events {
		if event.Kind == "public_refusal" && event.Stage == "output" && event.Reason == "authority" {
			refusal = true
		}
		if event.Kind == "browser_decision" && event.Stage == "renewal_current" && event.Reason == "expired" {
			renewal = true
		}
		if event.Reason == privateMessage {
			t.Fatal("trace disclosed private output error")
		}
	}
	var failure *connect.Error
	var baselineFailure *connect.Error
	if !errors.As(baselineErr, &baselineFailure) || !errors.As(err, &failure) || baselineFailure.Code() != failure.Code() || baselineFailure.Message() != failure.Message() || failure.Code() != connect.CodeUnavailable || failure.Message() != "public RPC request rejected" || baselineCalls != 2 || calls != baselineCalls || !refusal || !renewal || dropped != 0 || recorder.Body.Len() != 0 {
		t.Fatal("trace changed final refusal/sanitization/native sample", err, calls, events, dropped)
	}
}

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
