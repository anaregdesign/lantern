package service

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const maxChangeFrameBytes = 1 << 20
const changeCursorWireReserve = changeCursorPlainBytes + 64

// changeProjection owns immutable compiled scopes for one stream. All expensive
// projection and live-image sampling occur outside the log dispatcher lock.
type changeProjection struct {
	svc             *LanternService
	prefix          string
	value           bool
	vertices, edges *security.Scope
	access          *security.Access
}

func newChangeProjection(svc *LanternService, req *pb.WatchChangesRequest, admission *security.Admission) *changeProjection {
	p := &changeProjection{svc: svc, prefix: req.GetPrefix(), value: req.GetProjection() == pb.ChangeProjection_CHANGE_PROJECTION_VALUE}
	if admission != nil {
		p.access = admission.Access()
		p.vertices = changeScope(admission.Access(), req.GetProjection(), false)
		p.edges = changeScope(admission.Access(), req.GetProjection(), true)
	}
	return p
}

func (p *changeProjection) vertex(ctx context.Context, physical string) (*pb.ChangeInvalidation, error) {
	logical, allowed := changeLogicalKey(physical, p.prefix, p.vertices)
	if !allowed {
		return nil, nil
	}
	item := &pb.ChangeInvalidation{Identity: &pb.ChangeInvalidation_VertexKey{VertexKey: logical}}
	if p.value {
		// This is a current local image, never the original hidden batch or
		// receipt result. Natural TTL absence needs no synthetic Delete event.
		if err := p.svc.withCommittedView(func() error {
			vertex, live := p.svc.cache.GetVertex(physical)
			if live {
				if proto.Size(vertex) > maxChangeFrameBytes-changeCursorWireReserve {
					return changeFrameError()
				}
				clone := proto.Clone(vertex).(*pb.Vertex)
				clone.Key = logical
				item.CurrentImage = &pb.ChangeInvalidation_Vertex{Vertex: clone}
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return item, ctx.Err()
}

func (p *changeProjection) edge(ctx context.Context, physical *pb.EdgeKey) (*pb.ChangeInvalidation, error) {
	tail, tailAllowed := changeLogicalKey(physical.GetTail(), p.prefix, p.edges)
	head, headAllowed := changeLogicalKey(physical.GetHead(), p.prefix, p.edges)
	if !tailAllowed || !headAllowed {
		return nil, nil
	}
	if p.access != nil && (!p.access.AllowsEdge(security.EdgeRead, tail, head) || !p.access.AllowsEdgeAction(security.CDCIdentity, tail, head) ||
		p.value && !p.access.AllowsEdgeAction(security.CDCValue, tail, head)) {
		return nil, nil
	}
	item := &pb.ChangeInvalidation{Identity: &pb.ChangeInvalidation_EdgeKey{EdgeKey: &pb.EdgeKey{Tail: tail, Head: head}}}
	if p.value {
		if err := p.svc.withCommittedView(func() error {
			details := p.svc.cache.GetEdgeDetails([]graphcache.EdgeKey[string]{{Tail: physical.GetTail(), Head: physical.GetHead()}})
			if len(details) != 1 {
				return changeGapError()
			}
			if details[0].Found {
				edge := &pb.Edge{Tail: tail, Head: head, Weight: details[0].Weight}
				if !details[0].Expiration.IsZero() {
					edge.Expiration = timestamppb.New(details[0].Expiration)
				}
				item.CurrentImage = &pb.ChangeInvalidation_Edge{Edge: edge}
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	return item, ctx.Err()
}

func changeFrameError() error {
	return connect.NewError(connect.CodeResourceExhausted, errors.New("change frame capacity exceeded; rebootstrap required"))
}

// At most one visible frame plus the existing bounded identity chunk is held.
// Hidden items and original chunk indexes cannot change visible metadata. An
// error after earlier frames has no cursor: replay may duplicate invalidations.
func (p *changeProjection) project(ctx context.Context, mutation *pb.Mutation, advance func(bool) ([]byte, error), send func(*pb.WatchChangesResponse) error) error {
	var pending *pb.WatchChangesResponse
	frameSize := 0
	add := func(item *pb.ChangeInvalidation) error {
		if item == nil {
			return nil
		}
		itemSize := proto.Size(&pb.WatchChangesResponse{Invalidations: []*pb.ChangeInvalidation{item}})
		if itemSize+changeCursorWireReserve > maxChangeFrameBytes {
			return changeFrameError()
		}
		if pending != nil && (len(pending.Invalidations) == maxIdentityChunkItems || frameSize+itemSize+changeCursorWireReserve > maxChangeFrameBytes) {
			if err := send(pending); err != nil {
				return err
			}
			pending, frameSize = nil, 0
		}
		if pending == nil {
			pending = &pb.WatchChangesResponse{}
		}
		pending.Invalidations = append(pending.Invalidations, item)
		frameSize += itemSize
		return nil
	}
	err := projectMutationIdentities(mutation, func(frame *pb.SubscribeResponse) error {
		if err := ctx.Err(); err != nil {
			return ctxToConnect(err)
		}
		chunk := frame.GetIdentityChunk()
		for _, key := range chunk.GetVertexKeys() {
			item, err := p.vertex(ctx, key)
			if err != nil {
				return err
			}
			if err := add(item); err != nil {
				return err
			}
		}
		for _, key := range chunk.GetEdgeKeys() {
			item, err := p.edge(ctx, key)
			if err != nil {
				return err
			}
			if err := add(item); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return changeGapError()
	}
	cursor, err := advance(pending != nil)
	if err != nil {
		return err
	}
	if pending == nil {
		return nil // Hidden-only progress appears only on the fixed heartbeat.
	}
	pending.Cursor = cursor
	if proto.Size(pending) > maxChangeFrameBytes {
		return changeFrameError()
	}
	return send(pending)
}
