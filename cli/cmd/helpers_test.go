package cmd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

// mutationCommandWire tests stream consumers through actual SDK Connect calls.
type mutationCommandWire struct {
	graphv1connect.UnimplementedLanternServiceHandler
	calls  int
	failAt int
}

func (h *mutationCommandWire) next() error {
	h.calls++
	if h.calls == h.failAt {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("genuine input failure"))
	}
	return nil
}
func (h *mutationCommandWire) AddEdges(_ context.Context, req *connect.Request[pb.AddEdgesRequest]) (*connect.Response[pb.AddEdgesResponse], error) {
	if err := h.next(); err != nil {
		return nil, err
	}
	if h.calls == 1 {
		return connect.NewResponse(&pb.AddEdgesResponse{Acceptance: &pb.MutationAcceptance{Kind: pb.MutationAcceptanceKind_MUTATION_ACCEPTANCE_KIND_HANDLED_EFFECT_UNDISCLOSED}}), nil
	}
	return connect.NewResponse(&pb.AddEdgesResponse{Written: int32(len(req.Msg.Edges)), EffectiveWeights: make([]float32, len(req.Msg.Edges))}), nil
}
func (h *mutationCommandWire) PutEdges(_ context.Context, req *connect.Request[pb.PutEdgesRequest]) (*connect.Response[pb.PutEdgesResponse], error) {
	if err := h.next(); err != nil {
		return nil, err
	}
	if h.calls == 1 {
		return connect.NewResponse(&pb.PutEdgesResponse{Acceptance: &pb.MutationAcceptance{Kind: pb.MutationAcceptanceKind_MUTATION_ACCEPTANCE_KIND_HANDLED_EFFECT_UNDISCLOSED}}), nil
	}
	outcomes := make([]pb.PutOutcome, len(req.Msg.Edges))
	for i := range outcomes {
		outcomes[i] = pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE
	}
	return connect.NewResponse(&pb.PutEdgesResponse{Outcomes: outcomes}), nil
}
func commandMutationFixture(t *testing.T, failAt int) *mutationCommandWire {
	t.Helper()
	oldAddress, oldChunk, oldTimeout, oldTLS, oldCA, oldCert, oldKey, oldToken, oldCompression := flagAddress, flagChunkSize, flagTimeout, flagTLS, flagTLSCA, flagTLSCert, flagTLSKey, flagToken, flagCompression
	t.Cleanup(func() {
		flagAddress, flagChunkSize, flagTimeout, flagTLS, flagTLSCA, flagTLSCert, flagTLSKey, flagToken, flagCompression = oldAddress, oldChunk, oldTimeout, oldTLS, oldCA, oldCert, oldKey, oldToken, oldCompression
	})
	h := &mutationCommandWire{failAt: failAt}
	_, handler := graphv1connect.NewLanternServiceHandler(h)
	server := httptest.NewUnstartedServer(handler)
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	server.Config.Protocols = protocols
	server.Start()
	t.Cleanup(server.Close)
	flagAddress, flagChunkSize, flagTimeout = strings.TrimPrefix(server.URL, "http://"), 1, 5*time.Second
	flagTLS, flagTLSCA, flagTLSCert, flagTLSKey, flagToken, flagCompression = false, "", "", "", "", "none"
	t.Setenv("LANTERN_TOKEN", "")
	return h
}
