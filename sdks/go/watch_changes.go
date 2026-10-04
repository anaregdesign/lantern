package client

import (
	"context"
	"errors"
	"fmt"
	"iter"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// ErrChangeGap means the public stream cannot prove cursor-safe progress.
// Resume with the last durably applied opaque cursor; a server-side gap requires
// a new bootstrap and authorized resident-key revalidation.
var ErrChangeGap = errors.New("public change gap")

// ScopedChangeCursor is an opaque, scope-bound public CDC checkpoint. It does
// not contain a client-interpretable per-origin sequence vector.
type ScopedChangeCursor struct{ bytes []byte }

func NewScopedChangeCursor(value []byte) (ScopedChangeCursor, error) {
	if len(value) == 0 || len(value) > 8192 {
		return ScopedChangeCursor{}, fmt.Errorf("%w: invalid public change cursor", ErrInvalidArgument)
	}
	return ScopedChangeCursor{bytes: append([]byte(nil), value...)}, nil
}

// Bytes returns a copy suitable for application-owned durable storage.
func (c ScopedChangeCursor) Bytes() []byte { return append([]byte(nil), c.bytes...) }

type ChangeProjection uint8

const (
	ChangeIdentity ChangeProjection = iota
	ChangeValue
)

// WatchChangesOptions describes a single authorized subscription. Exactly one
// of Bootstrap or a nonempty Cursor is required. Vertex prefix is literal and
// applies to both Edge endpoints. Unary default timeouts do not apply; use ctx.
type WatchChangesOptions struct {
	Prefix     string
	Projection ChangeProjection
	Bootstrap  bool
	Cursor     ScopedChangeCursor
}

// ChangeInvalidation identifies one committed resource. Exactly one of Key or
// Edge is present. Value projection may include the current local live image;
// it is not the original mutation value. A missing image means invalidate.
type ChangeInvalidation struct {
	Key         string
	Edge        *EdgeRef
	Vertex      *Vertex
	CurrentEdge *Edge
}

// ChangeFrame may carry only periodic progress. Persist Cursor only after
// applying every Invalidation; intermediate mutation frames have no cursor.
type ChangeFrame struct {
	Invalidations []ChangeInvalidation
	Cursor        ScopedChangeCursor
	Bootstrap     bool
}

// WatchChanges opens one public CDC stream without retry or automatic failover.
// Keep a bootstrap tail open while rebuilding the cache with ordinary reads.
// Stop iteration or cancel ctx to release the server subscriber.
func (l *Lantern) WatchChanges(ctx context.Context, options WatchChangesOptions) iter.Seq2[*ChangeFrame, error] {
	// Capture caller-owned options before deferred iteration.
	cursor := options.Cursor.Bytes()
	return func(yield func(*ChangeFrame, error) bool) {
		if options.Projection > ChangeValue || options.Bootstrap == (len(cursor) != 0) || len(cursor) > 8192 {
			yield(nil, fmt.Errorf("%w: choose bootstrap or an opaque resume cursor", ErrInvalidArgument))
			return
		}
		projection := pb.ChangeProjection_CHANGE_PROJECTION_IDENTITY
		if options.Projection == ChangeValue {
			projection = pb.ChangeProjection_CHANGE_PROJECTION_VALUE
		}
		stream, err := l.changeClient.WatchChanges(ctx, connect.NewRequest(&pb.WatchChangesRequest{Prefix: options.Prefix, Projection: projection, Bootstrap: options.Bootstrap, Cursor: cursor}))
		if err != nil {
			yield(nil, wrapConnectErr(err))
			return
		}
		defer func() { _ = stream.Close() }()
		bootstrapped := false
		for stream.Receive() {
			frame, err := decodeChangeFrame(stream.Msg(), options.Projection)
			if err == nil && (frame.Bootstrap && (!options.Bootstrap || bootstrapped) || !frame.Bootstrap && options.Bootstrap && !bootstrapped) {
				err = fmt.Errorf("%w: unexpected bootstrap order", ErrChangeGap)
			}
			if err != nil {
				yield(nil, err)
				return
			}
			bootstrapped = bootstrapped || frame.Bootstrap
			if !yield(frame, nil) {
				return
			}
		}
		if err := ctx.Err(); err != nil {
			yield(nil, err)
			return
		}
		if err := stream.Err(); err != nil {
			yield(nil, wrapConnectErr(err))
			return
		}
		yield(nil, fmt.Errorf("%w: stream ended unexpectedly", ErrChangeGap))
	}
}

func changeUnknownFields(message protoreflect.Message) bool {
	if len(message.GetUnknown()) > 0 {
		return true
	}
	unknown := false
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.IsList() && field.Message() != nil {
			values := value.List()
			for i := 0; i < values.Len(); i++ {
				if changeUnknownFields(values.Get(i).Message()) {
					unknown = true
					return false
				}
			}
		} else if !field.IsMap() && field.Message() != nil {
			unknown = changeUnknownFields(value.Message())
		}
		return !unknown
	})
	return unknown
}

func decodeChangeFrame(raw *pb.WatchChangesResponse, projection ChangeProjection) (*ChangeFrame, error) {
	invalid := func() (*ChangeFrame, error) {
		return nil, fmt.Errorf("%w: malformed public change frame", ErrChangeGap)
	}
	if raw == nil || proto.Size(raw) > 1<<20 || changeUnknownFields(raw.ProtoReflect()) || len(raw.Invalidations) > 1024 || len(raw.Cursor) > 8192 || len(raw.Invalidations) == 0 && len(raw.Cursor) == 0 || raw.Bootstrap && (len(raw.Invalidations) != 0 || len(raw.Cursor) == 0) {
		return invalid()
	}
	frame := &ChangeFrame{Bootstrap: raw.Bootstrap, Invalidations: make([]ChangeInvalidation, 0, len(raw.Invalidations))}
	if len(raw.Cursor) > 0 {
		frame.Cursor, _ = NewScopedChangeCursor(raw.Cursor)
	}
	for _, item := range raw.Invalidations {
		if item == nil || projection == ChangeIdentity && item.CurrentImage != nil {
			return invalid()
		}
		result := ChangeInvalidation{}
		switch identity := item.Identity.(type) {
		case *pb.ChangeInvalidation_VertexKey:
			if identity.VertexKey == "" || item.GetEdge() != nil {
				return invalid()
			}
			result.Key, result.Vertex = identity.VertexKey, item.GetVertex()
			if result.Vertex != nil && result.Vertex.Key != result.Key {
				return invalid()
			}
		case *pb.ChangeInvalidation_EdgeKey:
			key := identity.EdgeKey
			if key == nil || key.Tail == "" || key.Head == "" || item.GetVertex() != nil {
				return invalid()
			}
			result.Edge, result.CurrentEdge = &EdgeRef{Tail: key.Tail, Head: key.Head}, item.GetEdge()
			if result.CurrentEdge != nil && (result.CurrentEdge.Tail != key.Tail || result.CurrentEdge.Head != key.Head) {
				return invalid()
			}
		default:
			return invalid()
		}
		frame.Invalidations = append(frame.Invalidations, result)
	}
	return frame, nil
}
