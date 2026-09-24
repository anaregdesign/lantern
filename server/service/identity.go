package service

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

const (
	maxIdentityChunkItems = 1024
	maxIdentityFrameBytes = 1 << 20
)

func identityCursor(raw map[string]uint64) (map[string]uint64, error) {
	cursor := make(map[string]uint64, len(raw))
	for origin, next := range raw {
		decoded, err := hex.DecodeString(origin)
		if err != nil || len(decoded) != 16 || hex.EncodeToString(decoded) != origin || zeroNodeID(decoded) || next == 0 {
			return nil, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid identity cursor origin/seq %q:%d", origin, next))
		}
		cursor[origin] = next
	}
	return cursor, nil
}

// validateIdentityResume proves that every requested mutation through the
// responder's published frontier is still resident. A min-only ring check is
// insufficient: verified Snapshot repair may jump an origin cutoff without
// inserting the skipped entries in this replica's log.
func validateIdentityResume(cursor, frontier map[string]uint64, retained []mutationlog.Entry) error {
	need := make(map[string]uint64)
	for origin, last := range frontier {
		if last == math.MaxUint64 {
			return connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("identity cursor exhausted for origin %s", origin))
		}
		want := cursor[origin]
		if want == 0 {
			want = 1
		}
		if want <= last {
			need[origin] = want
		}
	}
	for _, entry := range retained {
		m, ok := graphMutationFromLog(entry.Op)
		if !ok || len(m.GetOrigin()) != 16 || m.GetSeq() == 0 {
			return connect.NewError(connect.CodeInternal, fmt.Errorf("identity subscription found malformed log entry %d", entry.Seq))
		}
		origin := hex.EncodeToString(m.GetOrigin())
		next, needed := need[origin]
		if !needed || m.GetSeq() < cursorNext(cursor, origin) {
			continue
		}
		if m.GetSeq() != next {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("gapped: origin %s needs seq %d, retained seq %d", origin, next, m.GetSeq()))
		}
		need[origin] = next + 1
	}
	for origin, next := range need {
		if next != frontier[origin]+1 {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("gapped: origin %s needs seq %d through %d", origin, next, frontier[origin]))
		}
	}
	return nil
}

func cursorNext(cursor map[string]uint64, origin string) uint64 {
	if next := cursor[origin]; next != 0 {
		return next
	}
	return 1
}

// subscribeIdentity registers the tail and captures the checkpoint or proves
// retained vector history under one service publication cut. This remains a
// single deployment-wide stream, protected by the replication service auth.
func (s *LanternReplicationService) subscribeIdentity(ctx context.Context, req *pb.SubscribeRequest, stream Sender[pb.SubscribeResponse]) error {
	if req.GetFromLocalSeq() != 0 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("identity projection uses only the per-origin cursor"))
	}
	if req.GetBootstrap() && len(req.GetFromSeqPerOrigin()) != 0 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("identity bootstrap requires an empty cursor"))
	}
	cursor, err := identityCursor(req.GetFromSeqPerOrigin())
	if err != nil {
		return err
	}
	cut, ok := s.origins.(subscribeCutProvider)
	if !ok {
		return connect.NewError(connect.CodeUnavailable, errors.New("identity subscription requires a publication-cut provider"))
	}
	var (
		faultCh    <-chan struct{}
		ch         <-chan mutationlog.Entry
		cancel     func() error
		openErr    error
		checkpoint map[string]uint64
	)
	cutErr := cut.withReplicationSubscribeCut(func(generation <-chan struct{}) {
		faultCh = generation
		checkpoint = make(map[string]uint64)
		for _, state := range s.origins.OriginStates() {
			checkpoint[hex.EncodeToString(state.Origin[:])] = state.LastSeq
		}
		lastLocal, _ := s.log.LastSeq()
		if lastLocal == math.MaxUint64 {
			openErr = connect.NewError(connect.CodeResourceExhausted, errors.New("replica-local log sequence exhausted"))
			return
		}
		fromLocal := lastLocal + 1
		if !req.GetBootstrap() {
			retained := s.log.RetainedEntries()
			if err := validateIdentityResume(cursor, checkpoint, retained); err != nil {
				openErr = err
				return
			}
			if len(retained) > 0 {
				fromLocal = retained[0].Seq
			}
		}
		ch, cancel, openErr = s.log.Subscribe(fromLocal)
	})
	if cutErr != nil {
		s.metrics.OnSubscribeDropped("gapped")
		return cutErr
	}
	if openErr != nil {
		if connect.CodeOf(openErr) == connect.CodeFailedPrecondition || errors.Is(openErr, mutationlog.ErrGapped) {
			s.metrics.OnSubscribeDropped("gapped")
		}
		if errors.Is(openErr, mutationlog.ErrGapped) {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("gapped: retained log changed while opening identity subscription: %w", openErr))
		}
		return openErr
	}
	defer func() { _ = cancel() }()
	s.metrics.OnSubscribeStarted()
	defer s.metrics.OnSubscribeEnded()
	if req.GetBootstrap() {
		frame := &pb.SubscribeResponse{Event: &pb.SubscribeResponse_Checkpoint{Checkpoint: &pb.IdentityCheckpoint{LastSeqPerOrigin: checkpoint}}}
		if proto.Size(frame) > maxIdentityFrameBytes {
			return connect.NewError(connect.CodeResourceExhausted, errors.New("identity checkpoint exceeds 1 MiB frame cap"))
		}
		if err := stream.Send(frame); err != nil {
			s.metrics.OnSubscribeDropped("send_failed")
			return err
		}
	}
	for {
		select {
		case <-ctx.Done():
			return ctxToConnect(ctx.Err())
		case <-faultCh:
			s.metrics.OnSubscribeDropped("gapped")
			return publicationGapError()
		case entry, open := <-ch:
			select {
			case <-faultCh:
				s.metrics.OnSubscribeDropped("gapped")
				return publicationGapError()
			default:
			}
			if !open {
				s.metrics.OnSubscribeDropped("gapped")
				return connect.NewError(connect.CodeFailedPrecondition, errors.New("gapped: identity subscriber fell behind"))
			}
			m, ok := graphMutationFromLog(entry.Op)
			if !ok {
				return connect.NewError(connect.CodeInternal, fmt.Errorf("identity subscription found non-Mutation log entry %d", entry.Seq))
			}
			origin := hex.EncodeToString(m.GetOrigin())
			if !req.GetBootstrap() && m.GetSeq() < cursorNext(cursor, origin) {
				continue
			}
			err := projectMutationIdentities(m, func(frame *pb.SubscribeResponse) error {
				select {
				case <-faultCh:
					return publicationGapError()
				default:
				}
				return stream.Send(frame)
			})
			if err != nil {
				if connect.CodeOf(err) == connect.CodeFailedPrecondition {
					s.metrics.OnSubscribeDropped("gapped")
				} else {
					s.metrics.OnSubscribeDropped("send_failed")
				}
				return err
			}
		}
	}
}

// identityProjector streams one mutation as bounded, value-free identity
// chunks. The final marker is mandatory even when the mutation has no items.
// Sizes include the SubscribeResponse envelope, so the wire frame never
// exceeds maxIdentityFrameBytes. The source mutation already resides in the
// bounded mutation log; projection retains only one chunk at a time.
type identityProjector struct {
	mutation  *pb.Mutation
	category  pb.IdentityOperation
	send      func(*pb.SubscribeResponse) error
	chunk     *pb.IdentityChunk
	count     uint64
	index     uint64
	chunkSize int
}

func newIdentityProjector(m *pb.Mutation, category pb.IdentityOperation, send func(*pb.SubscribeResponse) error) *identityProjector {
	p := &identityProjector{mutation: m, category: category, send: send}
	p.startChunk()
	return p
}

func (p *identityProjector) startChunk() {
	p.chunk = &pb.IdentityChunk{
		Origin:         append([]byte(nil), p.mutation.GetOrigin()...),
		Seq:            p.mutation.GetSeq(),
		Hlc:            p.mutation.GetHlc(),
		Operation:      p.category,
		ChunkIndex:     uint32(p.index),
		IsLast:         true, // reserve the larger final-frame encoding
		FirstItemIndex: uint32(p.count),
	}
	p.chunkSize = proto.Size(p.chunk)
}

func identityFrame(chunk *pb.IdentityChunk) *pb.SubscribeResponse {
	return &pb.SubscribeResponse{Event: &pb.SubscribeResponse_IdentityChunk{IdentityChunk: chunk}}
}

func (p *identityProjector) addVertex(key string) error {
	return p.add(protowire.SizeTag(7)+protowire.SizeBytes(len(key)), func() {
		p.chunk.VertexKeys = append(p.chunk.VertexKeys, key)
	})
}

func (p *identityProjector) addEdge(tail, head string) error {
	edge := &pb.EdgeKey{Tail: tail, Head: head}
	return p.add(protowire.SizeTag(8)+protowire.SizeBytes(proto.Size(edge)), func() {
		p.chunk.EdgeKeys = append(p.chunk.EdgeKeys, edge)
	})
}

func (p *identityProjector) add(itemBytes int, appendItem func()) error {
	if p.count >= math.MaxUint32 {
		return connect.NewError(connect.CodeResourceExhausted, errors.New("identity projection item index exhausted"))
	}
	frameSize := protowire.SizeTag(3) + protowire.SizeBytes(p.chunkSize+itemBytes)
	if len(p.chunk.GetVertexKeys())+len(p.chunk.GetEdgeKeys()) == maxIdentityChunkItems || frameSize > maxIdentityFrameBytes {
		if len(p.chunk.GetVertexKeys())+len(p.chunk.GetEdgeKeys()) == 0 {
			return connect.NewError(connect.CodeResourceExhausted, errors.New("identity projection item exceeds 1 MiB frame cap"))
		}
		if err := p.flush(false); err != nil {
			return err
		}
		p.index++
		if p.index > math.MaxUint32 {
			return connect.NewError(connect.CodeResourceExhausted, errors.New("identity projection chunk index exhausted"))
		}
		p.startChunk()
		frameSize = protowire.SizeTag(3) + protowire.SizeBytes(p.chunkSize+itemBytes)
	}
	if frameSize > maxIdentityFrameBytes {
		return connect.NewError(connect.CodeResourceExhausted, errors.New("identity projection item exceeds 1 MiB frame cap"))
	}
	appendItem()
	p.chunkSize += itemBytes
	p.count++
	return nil
}

func (p *identityProjector) flush(last bool) error {
	p.chunk.IsLast = last
	frame := identityFrame(p.chunk)
	if proto.Size(frame) > maxIdentityFrameBytes {
		return connect.NewError(connect.CodeInternal, errors.New("identity projection exceeded frame cap"))
	}
	return p.send(frame)
}

// projectMutationIdentities never copies a graph value or contribution ID
// into its output. A legacy predicate delete cannot prove its exact victim
// set and must fail closed instead of silently advancing the CDC cursor.
func projectMutationIdentities(m *pb.Mutation, send func(*pb.SubscribeResponse) error) error {
	if m == nil || m.GetSeq() == 0 || len(m.GetOrigin()) != 16 || m.GetHlc() == nil || m.GetOp() == nil {
		return connect.NewError(connect.CodeInternal, errors.New("identity projection received malformed mutation"))
	}
	var category pb.IdentityOperation
	switch m.GetOp().GetOp().(type) {
	case *pb.MutationOp_PutVertex, *pb.MutationOp_PutVertices, *pb.MutationOp_ReplicatedPutVertices:
		category = pb.IdentityOperation_IDENTITY_OPERATION_PUT_VERTEX
	case *pb.MutationOp_DeleteVertex, *pb.MutationOp_DeleteVertices:
		category = pb.IdentityOperation_IDENTITY_OPERATION_DELETE_VERTEX
	case *pb.MutationOp_AddEdge, *pb.MutationOp_AddEdges:
		category = pb.IdentityOperation_IDENTITY_OPERATION_ADD_EDGE
	case *pb.MutationOp_PutEdge, *pb.MutationOp_PutEdges, *pb.MutationOp_ReplicatedPutEdges:
		category = pb.IdentityOperation_IDENTITY_OPERATION_PUT_EDGE
	case *pb.MutationOp_DeleteEdge, *pb.MutationOp_DeleteEdges:
		category = pb.IdentityOperation_IDENTITY_OPERATION_DELETE_EDGE
	case *pb.MutationOp_DeleteVerticesByPrefix, *pb.MutationOp_DeleteEdgesByPrefix:
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("gapped: predicate-shaped delete has no exact committed victims"))
	default:
		return connect.NewError(connect.CodeInternal, fmt.Errorf("identity projection cannot classify mutation op %T", m.GetOp().GetOp()))
	}
	p := newIdentityProjector(m, category, send)
	var err error
	switch op := m.GetOp().GetOp().(type) {
	case *pb.MutationOp_PutVertex:
		if op.PutVertex.GetVertex() == nil {
			return connect.NewError(connect.CodeInternal, errors.New("identity projection PutVertex has no vertex"))
		}
		err = p.addVertex(op.PutVertex.GetVertex().GetKey())
	case *pb.MutationOp_PutVertices:
		for _, v := range op.PutVertices.GetVertices() {
			if err = p.addVertex(v.GetKey()); err != nil {
				return err
			}
		}
	case *pb.MutationOp_ReplicatedPutVertices:
		for _, item := range op.ReplicatedPutVertices.GetEntries() {
			switch outcome := item.GetOutcome().(type) {
			case *pb.ReplicatedPutVertex_Live:
				if outcome.Live == nil {
					return connect.NewError(connect.CodeInternal, errors.New("identity projection has nil live vertex"))
				}
				err = p.addVertex(outcome.Live.GetKey())
			case *pb.ReplicatedPutVertex_CausalBarrier:
				if outcome.CausalBarrier == nil {
					return connect.NewError(connect.CodeInternal, errors.New("identity projection has nil vertex barrier"))
				}
				err = p.addVertex(outcome.CausalBarrier.GetKey())
			default:
				return connect.NewError(connect.CodeInternal, errors.New("identity projection has invalid vertex outcome"))
			}
			if err != nil {
				return err
			}
		}
	case *pb.MutationOp_DeleteVertex:
		err = p.addVertex(op.DeleteVertex.GetKey())
	case *pb.MutationOp_DeleteVertices:
		for _, key := range op.DeleteVertices.GetKeys() {
			if err = p.addVertex(key); err != nil {
				return err
			}
		}
	case *pb.MutationOp_AddEdge:
		if op.AddEdge.GetEdge() == nil {
			return connect.NewError(connect.CodeInternal, errors.New("identity projection AddEdge has no edge"))
		}
		err = p.addEdge(op.AddEdge.GetEdge().GetTail(), op.AddEdge.GetEdge().GetHead())
	case *pb.MutationOp_AddEdges:
		for _, edge := range op.AddEdges.GetEdges() {
			if err = p.addEdge(edge.GetTail(), edge.GetHead()); err != nil {
				return err
			}
		}
	case *pb.MutationOp_PutEdge:
		if op.PutEdge.GetEdge() == nil {
			return connect.NewError(connect.CodeInternal, errors.New("identity projection PutEdge has no edge"))
		}
		err = p.addEdge(op.PutEdge.GetEdge().GetTail(), op.PutEdge.GetEdge().GetHead())
	case *pb.MutationOp_PutEdges:
		for _, edge := range op.PutEdges.GetEdges() {
			if err = p.addEdge(edge.GetTail(), edge.GetHead()); err != nil {
				return err
			}
		}
	case *pb.MutationOp_ReplicatedPutEdges:
		for _, item := range op.ReplicatedPutEdges.GetEntries() {
			switch outcome := item.GetOutcome().(type) {
			case *pb.ReplicatedPutEdge_Live:
				if outcome.Live == nil {
					return connect.NewError(connect.CodeInternal, errors.New("identity projection has nil live edge"))
				}
				err = p.addEdge(outcome.Live.GetTail(), outcome.Live.GetHead())
			case *pb.ReplicatedPutEdge_CausalBarrier:
				if outcome.CausalBarrier == nil {
					return connect.NewError(connect.CodeInternal, errors.New("identity projection has nil edge barrier"))
				}
				err = p.addEdge(outcome.CausalBarrier.GetTail(), outcome.CausalBarrier.GetHead())
			default:
				return connect.NewError(connect.CodeInternal, errors.New("identity projection has invalid edge outcome"))
			}
			if err != nil {
				return err
			}
		}
	case *pb.MutationOp_DeleteEdge:
		err = p.addEdge(op.DeleteEdge.GetTail(), op.DeleteEdge.GetHead())
	case *pb.MutationOp_DeleteEdges:
		for _, edge := range op.DeleteEdges.GetEdges() {
			if err = p.addEdge(edge.GetTail(), edge.GetHead()); err != nil {
				return err
			}
		}
	}
	if err != nil {
		return err
	}
	return p.flush(true)
}
