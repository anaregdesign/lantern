package integration_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	cliservice "github.com/anaregdesign/lantern/cli/service"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	client "github.com/anaregdesign/lantern/sdks/go"
)

func TestContributionClients_GoSDK_RealConnectH2C(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()

	t.Run("caller IDs, indexed outcomes, and the Put base", func(t *testing.T) {
		node := newAuthedPumpNode(t, hlc.NodeID{0xf1}, 64)
		sdk := newConnectClientFor(t, node.url,
			client.WithAuthToken(testToken), client.WithBatchChunkSize(2))
		const tail, head = "sdk/contrib/tail", "sdk/contrib/head"
		first, second, fresh := client.ContribID{0x11}, client.ContribID{0x22}, client.ContribID{0x33}
		if outcome, err := sdk.PutEdge(ctx, tail, head, 2, 0); err != nil ||
			outcome != client.PutOutcomeAppliedAndLive {
			t.Fatalf("Put base = (%s, %v)", outcome, err)
		}
		weights, err := sdk.AddEdgesWithIDs(ctx, []client.EdgeAddInput{
			{Edge: client.EdgeInput{Tail: tail, Head: head, Weight: 1}, ContribID: first},
			{Edge: client.EdgeInput{Tail: tail, Head: head, Weight: 3}, ContribID: second},
		})
		if err != nil || !slices.Equal(weights, []float32{3, 6}) {
			t.Fatalf("AddEdgesWithIDs = (%v, %v)", weights, err)
		}
		ref := client.EdgeContributionRef{Tail: tail, Head: head, ContribID: first}
		existed, deleted, err := sdk.DeleteEdgeContributions(ctx, []client.EdgeContributionRef{
			ref, ref, {Tail: tail, Head: "wrong", ContribID: first},
		})
		if err != nil || deleted != 1 || !slices.Equal(existed, []bool{true, false, false}) {
			t.Fatalf("chunked contribution Delete = (%v, %d, %v)", existed, deleted, err)
		}
		if edge, err := sdk.GetEdge(ctx, tail, head); err != nil || edge.GetWeight() != 5 {
			t.Fatalf("other Add and Put base = (%+v, %v)", edge, err)
		}
		if removed, err := sdk.DeleteEdgeContribution(ctx, tail, head, second); err != nil || !removed {
			t.Fatalf("singular Delete = (%t, %v)", removed, err)
		}
		if weight, err := sdk.AddEdgeWithID(ctx, tail, head, 7, 0, first); err != nil || weight != 2 {
			t.Fatalf("deleted ID evaded D4 fence = (%g, %v)", weight, err)
		}
		if weight, err := sdk.AddEdgeWithID(ctx, tail, head, 1, 0, fresh); err != nil || weight != 3 {
			t.Fatalf("fresh ID Add = (%g, %v)", weight, err)
		}
		if removed, err := sdk.DeleteEdgeContribution(ctx, tail, head, fresh); err != nil || !removed {
			t.Fatalf("fresh ID Delete = (%t, %v)", removed, err)
		}
		if edge, err := sdk.GetEdge(ctx, tail, head); err != nil || edge.GetWeight() != 2 {
			t.Fatalf("Put base after selective Deletes = (%+v, %v)", edge, err)
		}
		before := node.svc.LocalSeq(node.nodeID)
		if _, err := sdk.DeleteEdgeContribution(ctx, tail, head, client.ContribID{}); !errors.Is(err, client.ErrInvalidArgument) {
			t.Fatalf("zero contribution ID = %v, want local InvalidArgument", err)
		}
		if got := node.svc.LocalSeq(node.nodeID); got != before {
			t.Fatalf("invalid SDK input advanced origin %d -> %d", before, got)
		}
	})

	t.Run("committed response loss is not retried", func(t *testing.T) {
		node := newAuthedPumpNode(t, hlc.NodeID{0xf2}, 64)
		const tail, head = "sdk/loss/tail", "sdk/loss/head"
		id := client.ContribID{0x44}
		if _, err := node.sdk.PutEdge(ctx, tail, head, 2, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := node.sdk.AddEdgeWithID(ctx, tail, head, 3, 0, id); err != nil {
			t.Fatal(err)
		}
		transport := &commitLossTransport{
			inner: h2cClient().Transport, pathSuffix: "/DeleteEdgeContributions",
		}
		lossy := newConnectClientFor(t, node.url,
			client.WithAuthToken(testToken),
			client.WithHTTPClient(&http.Client{Transport: transport}),
			client.WithRetry(client.RetryPolicy{MaxAttempts: 3}),
		)
		removed, err := lossy.DeleteEdgeContribution(ctx, tail, head, id)
		var batch *client.BatchError
		if removed || !errors.Is(err, client.ErrUnavailable) ||
			!errors.As(err, &batch) || batch.Written != 0 ||
			!transport.dropped.Load() || transport.attempts.Load() != 1 {
			t.Fatalf("ambiguous Delete = (%t, %v), dropped=%t attempts=%d",
				removed, err, transport.dropped.Load(), transport.attempts.Load())
		}
		if removed, err := node.sdk.DeleteEdgeContribution(ctx, tail, head, id); err != nil || removed {
			t.Fatalf("second Delete after committed response loss = (%t, %v)", removed, err)
		}
		if edge, err := node.sdk.GetEdge(ctx, tail, head); err != nil || edge.GetWeight() != 2 {
			t.Fatalf("committed removal retained Put base = (%+v, %v)", edge, err)
		}
	})

	t.Run("capacity rejection has no partial result", func(t *testing.T) {
		node := newAuthedPumpNode(t, hlc.NodeID{0xf3}, 64)
		const tail, head = "sdk/cap/tail", "sdk/cap/head"
		id, missing := client.ContribID{0x55}, client.ContribID{0x66}
		if _, err := node.sdk.PutEdge(ctx, tail, head, 2, 0); err != nil {
			t.Fatal(err)
		}
		if _, err := node.sdk.AddEdgeWithID(ctx, tail, head, 3, 0, id); err != nil {
			t.Fatal(err)
		}
		node.cache.SetCausalMetadataLimits(graphcache.CausalMetadataLimits{MaxEdgeEntries: 1})
		before := node.svc.LocalSeq(node.nodeID)
		existed, deleted, err := node.sdk.DeleteEdgeContributions(ctx, []client.EdgeContributionRef{
			{Tail: tail, Head: head, ContribID: id},
			{Tail: tail, Head: head, ContribID: missing},
		})
		var batch *client.BatchError
		if !errors.Is(err, client.ErrResourceExhausted) || !errors.As(err, &batch) ||
			batch.Written != 0 || deleted != 0 || len(existed) != 0 {
			t.Fatalf("rejected Delete = (%v, %d, %v), want no observed outcomes", existed, deleted, err)
		}
		if got := node.svc.LocalSeq(node.nodeID); got != before {
			t.Fatalf("rejected SDK Delete advanced origin %d -> %d", before, got)
		}
		if edge, err := node.sdk.GetEdge(ctx, tail, head); err != nil || edge.GetWeight() != 5 {
			t.Fatalf("rejected Delete changed edge = (%+v, %v)", edge, err)
		}
	})

	t.Run("expired Add reports absent and preserves the Put base", func(t *testing.T) {
		node := newAuthedPumpNode(t, hlc.NodeID{0xf7}, 64)
		const tail, head = "sdk/expired/tail", "sdk/expired/head"
		id := client.ContribID{0x88}
		if _, err := node.sdk.PutEdge(ctx, tail, head, 2, 0); err != nil {
			t.Fatal(err)
		}
		if weight, err := node.sdk.AddEdgeWithID(ctx, tail, head, 3, 80*time.Millisecond, id); err != nil || weight != 5 {
			t.Fatalf("expiring Add = (%g, %v)", weight, err)
		}
		time.Sleep(180 * time.Millisecond)
		if edge, err := node.sdk.GetEdge(ctx, tail, head); err != nil || edge.GetWeight() != 2 {
			t.Fatalf("expired contribution still contributes = (%+v, %v)", edge, err)
		}
		if removed, err := node.sdk.DeleteEdgeContribution(ctx, tail, head, id); err != nil || removed {
			t.Fatalf("Delete of expired contribution = (%t, %v)", removed, err)
		}
	})
}

func TestContributionClients_GoSDKReceipt_RealConnectH2C(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	wire := newPublicReceiptWireServer(t, hlc.NodeID{0xf4}, 32, testToken)
	sdk := newConnectClientFor(t, wire.server.url, client.WithAuthToken(testToken))
	const tail, head = "sdk/receipt/tail", "sdk/receipt/head"
	id := client.ContribID{0x77}
	if _, err := sdk.PutEdge(ctx, tail, head, 2, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := sdk.AddEdgeWithID(ctx, tail, head, 3, 0, id); err != nil {
		t.Fatal(err)
	}
	capability, err := sdk.GetReceiptCapability(ctx)
	if err != nil || !capability.Supports(client.ReceiptMutationDeleteEdgeContribution) {
		t.Fatalf("selective Delete receipt capability = (%+v, %v)", capability, err)
	}
	receipt, err := sdk.NewReceiptContext(capability, client.ReceiptMutationDeleteEdgeContribution, 2)
	if err != nil {
		t.Fatal(err)
	}
	transport := &commitLossTransport{
		inner: h2cClient().Transport, pathSuffix: "/DeleteEdgeContributions",
	}
	lossy := newConnectClientFor(t, wire.server.url,
		client.WithAuthToken(testToken),
		client.WithHTTPClient(&http.Client{Transport: transport}),
		client.WithRetry(client.RetryPolicy{MaxAttempts: 2}),
	)
	refs := []client.EdgeContributionRef{
		{Tail: tail, Head: head, ContribID: id},
		{Tail: tail, Head: head, ContribID: id},
	}
	results, err := lossy.DeleteEdgeContributionsWithReceipt(ctx, refs, receipt)
	if err != nil || !transport.dropped.Load() ||
		len(results) != 2 || !results[0].Existed || results[1].Existed ||
		results[0].OperationID != receipt.OperationIDs[0] ||
		results[1].OperationID != receipt.OperationIDs[1] {
		t.Fatalf("receipt replay = (%+v, %v), dropped=%t", results, err, transport.dropped.Load())
	}
	statuses, err := sdk.GetReceiptStatuses(ctx, receipt.OperationIDs)
	if err != nil || len(statuses) != 2 {
		t.Fatalf("selective Delete receipt statuses = (%+v, %v)", statuses, err)
	}
	for i, status := range statuses {
		if status.State != client.ReceiptConfirmed || status.Receipt == nil {
			t.Fatalf("receipt status[%d] = %+v", i, status)
		}
		original, ok := status.Receipt.OriginalResult.(client.ReceiptDeleteEdgeContributionResult)
		if !ok || original.Existed != results[i].Existed {
			t.Fatalf("original contribution result[%d] = %+v", i, status.Receipt)
		}
	}
	if edge, err := sdk.GetEdge(ctx, tail, head); err != nil || edge.GetWeight() != 2 {
		t.Fatalf("receipt Delete retained Put base = (%+v, %v)", edge, err)
	}
}

func TestContributionClients_CLI_RealConnectH2C(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()

	t.Run("shared one-shot and REPL grammar", func(t *testing.T) {
		node := newAuthedPumpNode(t, hlc.NodeID{0xf5}, 64)
		const tail, head = "cli/contrib/tail", "cli/contrib/head"
		idHex := strings.Repeat("ab", client.ContribIDSize)
		if _, err := node.sdk.PutEdge(ctx, tail, head, 2, 0); err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		cli := cliservice.NewCLIService(node.sdk, cliservice.WithOutput(&output))
		if err := cli.RunArgs(ctx, []string{"add", "edge", tail, head, "3", "id=" + idHex}); err != nil {
			t.Fatalf("CLI explicit-ID Add: %v", err)
		}
		if edge, err := node.sdk.GetEdge(ctx, tail, head); err != nil || edge.GetWeight() != 5 {
			t.Fatalf("CLI Add over wire = (%+v, %v)", edge, err)
		}
		if err := cli.RunArgs(ctx, []string{
			"delete", "contribution", tail, head, idHex, tail, head, idHex,
			"cli/missing", head, idHex,
		}); err != nil {
			t.Fatalf("CLI plural Delete: %v", err)
		}
		if got := output.String(); got != "{\"deleted\":1,\"existed\":[true,false,false]}\n" {
			t.Fatalf("CLI indexed JSON = %q", got)
		}
		output.Reset()
		if err := cli.Run(ctx, "delete contribution "+tail+" "+head+" "+idHex); err != nil {
			t.Fatalf("REPL singular Delete: %v", err)
		}
		if got := output.String(); got != "{\"deleted\":0,\"existed\":[false]}\n" {
			t.Fatalf("REPL missing result = %q", got)
		}
		if edge, err := node.sdk.GetEdge(ctx, tail, head); err != nil || edge.GetWeight() != 2 {
			t.Fatalf("CLI selective Delete retained Put base = (%+v, %v)", edge, err)
		}
		expiringID := strings.Repeat("ac", client.ContribIDSize)
		if err := cli.RunArgs(ctx, []string{"add", "edge", tail, head, "3", "1", "id=" + expiringID}); err != nil {
			t.Fatalf("CLI expiring Add: %v", err)
		}
		if edge, err := node.sdk.GetEdge(ctx, tail, head); err != nil || edge.GetWeight() != 5 {
			t.Fatalf("CLI expiring Add over wire = (%+v, %v)", edge, err)
		}
		time.Sleep(1200 * time.Millisecond)
		if edge, err := node.sdk.GetEdge(ctx, tail, head); err != nil || edge.GetWeight() != 2 {
			t.Fatalf("CLI expired contribution still contributes = (%+v, %v)", edge, err)
		}
		output.Reset()
		if err := cli.RunArgs(ctx, []string{"delete", "contribution", tail, head, expiringID}); err != nil {
			t.Fatalf("CLI Delete of expired contribution: %v", err)
		}
		if got := output.String(); got != "{\"deleted\":0,\"existed\":[false]}\n" {
			t.Fatalf("CLI expired result = %q", got)
		}
		before := node.svc.LocalSeq(node.nodeID)
		output.Reset()
		if err := cli.RunArgs(ctx, []string{
			"delete", "contribution", tail, head, strings.Repeat("ab", 49),
		}); !errors.Is(err, cliservice.ErrDeleteContribution) {
			t.Fatalf("49-byte receipt ID accepted by CLI: %v", err)
		}
		if got := node.svc.LocalSeq(node.nodeID); got != before || output.Len() != 0 {
			t.Fatalf("invalid CLI input changed wire state or emitted result: seq %d -> %d output=%q",
				before, got, output.String())
		}
	})

	t.Run("server rejection remains typed and atomic", func(t *testing.T) {
		node := newAuthedPumpNode(t, hlc.NodeID{0xf6}, 64)
		const tail, head = "cli/rejected/tail", "cli/rejected/head"
		idHex, missingHex := strings.Repeat("cd", client.ContribIDSize),
			strings.Repeat("ef", client.ContribIDSize)
		if _, err := node.sdk.PutEdge(ctx, tail, head, 2, 0); err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		cli := cliservice.NewCLIService(node.sdk, cliservice.WithOutput(&output))
		if err := cli.RunArgs(ctx, []string{"add", "edge", tail, head, "3", "id=" + idHex}); err != nil {
			t.Fatal(err)
		}
		node.cache.SetCausalMetadataLimits(graphcache.CausalMetadataLimits{MaxEdgeEntries: 1})
		before := node.svc.LocalSeq(node.nodeID)
		err := cli.RunArgs(ctx, []string{
			"delete", "contribution", tail, head, idHex, tail, head, missingHex,
		})
		if !errors.Is(err, client.ErrResourceExhausted) || output.Len() != 0 {
			t.Fatalf("rejected CLI Delete = %v, output=%q; want typed failure/no result", err, output.String())
		}
		if got := node.svc.LocalSeq(node.nodeID); got != before {
			t.Fatalf("rejected CLI Delete advanced origin %d -> %d", before, got)
		}
		if edge, err := node.sdk.GetEdge(ctx, tail, head); err != nil || edge.GetWeight() != 5 {
			t.Fatalf("rejected CLI Delete changed edge = (%+v, %v)", edge, err)
		}
	})
}
