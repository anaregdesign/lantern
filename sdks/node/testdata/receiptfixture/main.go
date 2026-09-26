// A test-only Connect endpoint for exercising Node receipt codecs. It returns a
// synthetic NaN original result for a finite Edge Add without changing a graph.
package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"log"
	"math"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"google.golang.org/protobuf/proto"
)

const retentionMs = uint64(time.Hour / time.Millisecond)

type recordedAdd struct {
	receipt   *pb.MutationReceipt
	tail      string
	head      string
	weight    uint32
	contribID []byte
}

type fixture struct {
	token      string
	epoch      []byte
	nodeID     []byte
	generation []byte
	mu         sync.Mutex
	receipts   map[string]recordedAdd
}

func (f *fixture) authorize(header http.Header) error {
	if header.Get("Authorization") != "Bearer "+f.token {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("fixture authorization required"))
	}
	return nil
}

func (f *fixture) capability(
	_ context.Context,
	req *connect.Request[pb.GetReceiptCapabilityRequest],
) (*connect.Response[pb.GetReceiptCapabilityResponse], error) {
	if err := f.authorize(req.Header()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.GetReceiptCapabilityResponse{
		Enabled: true,
		Policy: &pb.ReceiptPolicy{
			DeploymentEpoch: bytes.Clone(f.epoch),
			Fingerprint:     bytes.Repeat([]byte{0x22}, 32),
			RetentionMs:     retentionMs,
			MaxEntries:      1000,
			MaxBytes:        1_000_000,
		},
		Endpoint: &pb.ReceiptEndpoint{
			NodeId:     bytes.Clone(f.nodeID),
			Generation: bytes.Clone(f.generation),
		},
		ServerNowUnixMs: uint64(time.Now().UnixMilli()),
		SupportedMutations: []pb.ReceiptMutationKind{
			pb.ReceiptMutationKind_RECEIPT_MUTATION_KIND_ADD_EDGE,
		},
	}), nil
}

// Match the server's validated Edge Add intent encoding, not protobuf bytes.
func addIntentDigest(edge *pb.Edge, contribID []byte) [32]byte {
	canonical := []byte{byte(mutationreceipt.AddEdge)}
	canonical = binary.BigEndian.AppendUint64(canonical, uint64(len(edge.GetTail())))
	canonical = append(canonical, edge.GetTail()...)
	canonical = binary.BigEndian.AppendUint64(canonical, uint64(len(edge.GetHead())))
	canonical = append(canonical, edge.GetHead()...)
	canonical = binary.BigEndian.AppendUint32(canonical, math.Float32bits(edge.GetWeight()))
	canonical = append(canonical, 0)
	canonical = binary.BigEndian.AppendUint64(canonical, uint64(len(contribID)))
	canonical = append(canonical, contribID...)
	return mutationreceipt.IntentDigest(canonical)
}

func (f *fixture) addEdges(
	_ context.Context,
	req *connect.Request[pb.AddEdgesRequest],
) (*connect.Response[pb.AddEdgesResponse], error) {
	if err := f.authorize(req.Header()); err != nil {
		return nil, err
	}
	in := req.Msg
	ctx := in.GetReceiptContext()
	if len(in.GetEdges()) != 1 || len(in.GetContribIds()) != 1 || ctx == nil ||
		len(ctx.GetOperationIds()) != 1 || len(ctx.GetLogicalCallId()) != 16 ||
		!bytes.Equal(ctx.GetEndpoint().GetNodeId(), f.nodeID) ||
		!bytes.Equal(ctx.GetEndpoint().GetGeneration(), f.generation) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("fixture requires one aligned receipt Edge Add"))
	}
	edge := in.GetEdges()[0]
	opID := ctx.GetOperationIds()[0]
	contribID := in.GetContribIds()[0]
	if edge == nil || edge.GetTail() == "" || edge.GetHead() == "" ||
		edge.GetExpiration() != nil || len(opID) != 49 || opID[0] != 1 ||
		!bytes.Equal(opID[1:17], f.epoch) ||
		len(contribID) != 24 || bytes.Equal(contribID, make([]byte, 24)) ||
		math.IsNaN(float64(edge.GetWeight())) || math.IsInf(float64(edge.GetWeight()), 0) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("fixture requires a finite keyed delta"))
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if existing, ok := f.receipts[string(opID)]; ok {
		if !bytes.Equal(existing.receipt.GetLogicalCallId(), ctx.GetLogicalCallId()) ||
			existing.tail != edge.GetTail() || existing.head != edge.GetHead() ||
			existing.weight != math.Float32bits(edge.GetWeight()) ||
			!bytes.Equal(existing.contribID, contribID) {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("fixture receipt intent conflict"))
		}
		return connect.NewResponse(&pb.AddEdgesResponse{
			Written:          1,
			EffectiveWeights: []float32{existing.receipt.GetOriginalResult().GetAddEdgeEffectiveWeight()},
		}), nil
	}

	originalWeight := float32(math.NaN())
	digest := addIntentDigest(edge, contribID)
	f.receipts[string(opID)] = recordedAdd{
		receipt: &pb.MutationReceipt{
			OperationId:    bytes.Clone(opID),
			LogicalCallId:  bytes.Clone(ctx.GetLogicalCallId()),
			ItemIndex:      0,
			ItemCount:      1,
			IntentSha256:   digest[:],
			DeadlineUnixMs: binary.BigEndian.Uint64(opID[17:25]) + retentionMs,
			OriginalResult: &pb.ReceiptResult{
				Result: &pb.ReceiptResult_AddEdgeEffectiveWeight{
					AddEdgeEffectiveWeight: originalWeight,
				},
			},
		},
		tail:      edge.GetTail(),
		head:      edge.GetHead(),
		weight:    math.Float32bits(edge.GetWeight()),
		contribID: bytes.Clone(contribID),
	}
	return connect.NewResponse(&pb.AddEdgesResponse{
		Written:          1,
		EffectiveWeights: []float32{originalWeight},
	}), nil
}

func (f *fixture) statuses(
	_ context.Context,
	req *connect.Request[pb.GetReceiptStatusesRequest],
) (*connect.Response[pb.GetReceiptStatusesResponse], error) {
	if err := f.authorize(req.Header()); err != nil {
		return nil, err
	}
	ids := req.Msg.GetOperationIds()
	if len(ids) == 0 || len(ids) > 10_000 {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("fixture status IDs out of bounds"))
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	statuses := make([]*pb.ReceiptStatus, len(ids))
	for i, id := range ids {
		if len(id) != 49 {
			return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("fixture status ID has wrong length"))
		}
		status := &pb.ReceiptStatus{
			OperationId: bytes.Clone(id),
			State:       pb.MutationReceiptState_MUTATION_RECEIPT_STATE_NOT_YET_OBSERVED,
		}
		if existing, ok := f.receipts[string(id)]; ok {
			status.State = pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED
			status.Receipt = proto.Clone(existing.receipt).(*pb.MutationReceipt)
			if req.Header().Get("X-Fixture-Omit-Result") == "true" {
				status.Receipt.OriginalResult = nil
			}
		}
		statuses[i] = status
	}
	return connect.NewResponse(&pb.GetReceiptStatusesResponse{Statuses: statuses}), nil
}

func main() {
	port, err := strconv.Atoi(os.Getenv("LANTERN_NODE_NAN_FIXTURE_PORT"))
	if err != nil || port < 1 || port > 65535 {
		log.Fatal("LANTERN_NODE_NAN_FIXTURE_PORT must be a valid port")
	}
	token := os.Getenv("LANTERN_NODE_RECEIPT_TOKEN")
	if token == "" {
		log.Fatal("LANTERN_NODE_RECEIPT_TOKEN is required")
	}
	f := &fixture{
		token:      token,
		epoch:      bytes.Repeat([]byte{0x21}, 16),
		nodeID:     bytes.Repeat([]byte{0x23}, 16),
		generation: bytes.Repeat([]byte{0x24}, 16),
		receipts:   make(map[string]recordedAdd),
	}
	mux := http.NewServeMux()
	mux.Handle(graphv1connect.LanternServiceGetReceiptCapabilityProcedure,
		connect.NewUnaryHandler(graphv1connect.LanternServiceGetReceiptCapabilityProcedure, f.capability))
	mux.Handle(graphv1connect.LanternServiceAddEdgesProcedure,
		connect.NewUnaryHandler(graphv1connect.LanternServiceAddEdgesProcedure, f.addEdges))
	mux.Handle(graphv1connect.LanternServiceGetReceiptStatusesProcedure,
		connect.NewUnaryHandler(graphv1connect.LanternServiceGetReceiptStatusesProcedure, f.statuses))

	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	server := &http.Server{
		Addr:              "127.0.0.1:" + strconv.Itoa(port),
		Handler:           mux,
		Protocols:         protocols,
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Fatal(server.ListenAndServe())
}
