package service

import (
	"context"
	"encoding/hex"
	"errors"
	"math"
	"strings"
	"time"
	"unicode/utf8"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// ChangeServiceOptions is trusted Server composition. Heartbeat bounds idle
// authority checks; blocked sends also need the RPC admission deadline.
type ChangeServiceOptions struct {
	CursorKeys        []ChangeCursorKey
	CurrentKeyVersion uint32
	Now               func() time.Time
	Heartbeat         time.Duration
	Metrics           SubscribeMetrics
}

type ChangeConnectHandler struct {
	svc       *LanternService
	cursors   *changeCursorCodec
	now       func() time.Time
	heartbeat time.Duration
	metrics   SubscribeMetrics
}

var _ graphv1connect.LanternChangeServiceHandler = (*ChangeConnectHandler)(nil)

func NewChangeConnectHandler(svc *LanternService, options ChangeServiceOptions) (*ChangeConnectHandler, error) {
	if svc == nil || svc.namespaceFormat != keyspace.Version || svc.log == nil || svc.clock == nil {
		return nil, errors.New("CDC requires one namespaced publication runtime")
	}
	codec, err := newChangeCursorCodec(options.CursorKeys, options.CurrentKeyVersion)
	if err != nil {
		return nil, err
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.Heartbeat == 0 {
		options.Heartbeat = 5 * time.Second
	}
	if options.Heartbeat < 10*time.Millisecond || options.Heartbeat > 5*time.Second {
		return nil, errors.New("CDC heartbeat must be between 10 ms and 5 seconds")
	}
	if options.Metrics == nil {
		options.Metrics = nopSubscribeMetrics{}
	}
	return &ChangeConnectHandler{svc: svc, cursors: codec, now: options.Now, heartbeat: options.Heartbeat, metrics: options.Metrics}, nil
}

func changeGapError() error {
	return connect.NewError(connect.CodeFailedPrecondition, errors.New("authorized cache rebootstrap required"))
}

func (h *ChangeConnectHandler) WatchChanges(ctx context.Context, req *connect.Request[pb.WatchChangesRequest], stream *connect.ServerStream[pb.WatchChangesResponse]) error {
	return h.watch(ctx, req.Msg, stream)
}

func (h *ChangeConnectHandler) watch(ctx context.Context, req *pb.WatchChangesRequest, stream Sender[pb.WatchChangesResponse]) (resultErr error) {
	if req == nil || rejectProtoUnknownFields(req.ProtoReflect()) != nil || len(req.GetPrefix()) > 1024 || !utf8.ValidString(req.GetPrefix()) ||
		(req.GetProjection() != pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY && req.GetProjection() != pb.ChangeProjection_CHANGE_PROJECTION_VALUE) ||
		(req.GetBootstrap() == (len(req.GetCursor()) != 0)) {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("invalid change request"))
	}
	admission, err := h.svc.dataAdmission(ctx)
	if err != nil {
		return err
	}
	var binding [32]byte
	if admission != nil {
		scope := changeScope(admission.Access(), req.GetProjection(), false)
		if scope.Within(req.GetPrefix()).Empty() && changeScope(admission.Access(), req.GetProjection(), true).Within(req.GetPrefix()).Empty() {
			return dataPermissionError()
		}
		binding = admission.ScopeBinding()
	}
	requestBinding := changeRequestBinding(req)
	projection := newChangeProjection(h.svc, req, admission)
	var next map[string]uint64
	if !req.GetBootstrap() {
		next, err = h.cursors.open(req.GetCursor(), binding, requestBinding, h.now())
		if err != nil {
			return err
		}
	}
	var entries <-chan mutationlog.Entry
	var cancel func() error
	var fault <-chan struct{}
	var openErr error
	err = h.svc.withReplicationSubscribeCut(func(generation <-chan struct{}) {
		fault = generation
		frontier := make(map[string]uint64)
		for _, state := range h.svc.OriginStates() {
			frontier[hex.EncodeToString(state.Origin[:])] = state.LastSeq
		}
		if len(frontier) > maxChangeOrigins {
			openErr = changeGapError()
			return
		}
		last, _ := h.svc.log.LastSeq()
		if last == math.MaxUint64 {
			openErr = changeGapError()
			return
		}
		fromLocal := last + 1
		if req.GetBootstrap() {
			next = make(map[string]uint64, len(frontier))
			for origin, seq := range frontier {
				if seq == math.MaxUint64 {
					openErr = changeGapError()
					return
				}
				next[origin] = seq + 1
			}
		} else {
			retained := h.svc.log.RetainedEntries()
			if validateRetainedOriginResume(next, frontier, retained) != nil {
				openErr = changeGapError()
				return
			}
			if len(retained) > 0 {
				fromLocal = retained[0].Seq
			}
		}
		// No policy, projection or value lookup inside dispatcher's subsMu.
		entries, cancel, openErr = h.svc.log.Subscribe(fromLocal)
	})
	if err != nil || openErr != nil {
		return changeGapError()
	}
	defer func() { _ = cancel() }()
	h.metrics.OnSubscribeStarted()
	defer func() {
		h.metrics.OnSubscribeEnded()
		if resultErr != nil && ctx.Err() == nil {
			reason := "send_failed"
			if connect.CodeOf(resultErr) == connect.CodeFailedPrecondition {
				reason = "gapped"
			}
			h.metrics.OnSubscribeDropped(reason)
		}
	}()
	send := func(frame *pb.WatchChangesResponse) error {
		select {
		case <-fault:
			return changeGapError()
		default:
		}
		if _, err := h.svc.dataAdmission(ctx); err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return ctxToConnect(err)
		}
		return stream.Send(frame)
	}
	progress := func(bootstrap bool) error {
		cursor, err := h.cursors.seal(binding, requestBinding, next, h.now())
		if err != nil {
			return err
		}
		return send(&pb.WatchChangesResponse{Cursor: cursor, Bootstrap: bootstrap})
	}
	if req.GetBootstrap() {
		if err := progress(true); err != nil {
			return err
		}
	}
	ticker := time.NewTicker(h.heartbeat)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctxToConnect(ctx.Err())
		case <-fault:
			return changeGapError()
		case <-ticker.C:
			if err := progress(false); err != nil {
				return err
			}
		case entry, ok := <-entries:
			if !ok {
				return changeGapError()
			}
			mutation, valid := graphMutationFromLog(entry.Op)
			if !valid || len(mutation.GetOrigin()) != 16 || zeroNodeID(mutation.GetOrigin()) || mutation.GetSeq() == 0 || mutation.GetSeq() == math.MaxUint64 {
				return changeGapError()
			}
			origin := hex.EncodeToString(mutation.GetOrigin())
			if mutation.GetSeq() < cursorNext(next, origin) {
				continue
			}
			if mutation.GetSeq() != cursorNext(next, origin) || len(next) == maxChangeOrigins && next[origin] == 0 {
				return changeGapError()
			}
			// Advance only after the complete exact projection is validated. It
			// may emit multiple frames, with a cursor on the last visible frame.
			if err := projection.project(ctx, mutation, func(visible bool) ([]byte, error) {
				next[origin] = mutation.GetSeq() + 1
				if !visible {
					return nil, nil
				}
				return h.cursors.seal(binding, requestBinding, next, h.now())
			}, send); err != nil {
				return err
			}
		}
	}
}

func changeScope(access *security.Access, projection pb.ChangeProjection, edges bool) *security.Scope {
	actions := []security.Action{security.CDCIdentity}
	if projection == pb.ChangeProjection_CHANGE_PROJECTION_VALUE {
		actions = []security.Action{security.CDCIdentity, security.CDCValue, security.VertexRead}
	}
	if edges {
		actions = append(actions, security.EdgeRead)
		return access.EdgeCandidateScope(actions...)
	}
	return access.Scope(actions...)
}

func changeLogicalKey(physical, prefix string, scope *security.Scope) (string, bool) {
	logical, err := keyspace.LogicalKey(physical)
	return logical, err == nil && strings.HasPrefix(logical, prefix) && (scope == nil || scope.Contains(logical))
}
