package service

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
)

// WithDataNamespace installs the public logical/physical boundary. Internal
// apply/restore and service methods operate on physical identities; only the
// public Connect adapter invokes this mapper. Lower-level graph fixtures may
// omit it. Production construction must install it before exposing handlers.
func (s *LanternService) WithDataNamespace() *LanternService {
	if s.namespaceFormat != "" {
		return s
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic("data cursor entropy unavailable")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	s.namespaceCursor, err = cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	s.namespaceFormat = keyspace.Version
	return s
}

// Explicit descriptor rules keep ordinary value strings, JWT/receipt bytes,
// client IDs and metadata out of namespace conversion. A new public RPC needs
// an explicit entry, even when it has no identities. Schema coverage is tested.
var publicDataRequests = map[protoreflect.Name]bool{
	"IlluminateRequest": true, "GetVertexRequest": true, "GetVerticesRequest": true,
	"PutVertexRequest": true, "PutVerticesRequest": true, "DeleteVertexRequest": true,
	"DeleteVerticesRequest": true, "ScanVerticesRequest": true, "ScanVertexKeysRequest": true,
	"SearchVerticesRequest": true, "CountVerticesByPrefixRequest": true, "DeleteVerticesByPrefixRequest": true,
	"TopVerticesByDegreeRequest": true, "GetEdgeRequest": true, "GetEdgesRequest": true,
	"CreateEdgeRequest": true, "CreateEdgesRequest": true, "AddEdgeRequest": true, "AddEdgesRequest": true, "PutEdgeRequest": true, "PutEdgesRequest": true,
	"DeleteEdgeRequest": true, "DeleteEdgesRequest": true, "DeleteEdgeContributionRequest": true,
	"DeleteEdgeContributionsRequest": true, "DeleteEdgesByPrefixRequest": true, "ScanEdgesRequest": true,
	"GetServerStatusRequest": true, "GetReplicationStatusRequest": true, "GetReceiptCapabilityRequest": true,
	"GetReceiptStatusRequest": true, "GetReceiptStatusesRequest": true, "BackupSnapshotRequest": true,
}

// false is a nonempty identity; true is a literal prefix (including empty).
var dataIdentityFields = map[protoreflect.FullName]map[protoreflect.Name]bool{
	"graph.v1.Vertex": {"key": false}, "graph.v1.Edge": {"tail": false, "head": false},
	"graph.v1.EdgeKey": {"tail": false, "head": false}, "graph.v1.EdgeContributionKey": {"tail": false, "head": false},
	"graph.v1.IlluminateRequest": {"seed": false, "vertex_prefix": true},
	"graph.v1.GetVertexRequest":  {"key": false}, "graph.v1.GetVerticesRequest": {"keys": false},
	"graph.v1.DeleteVertexRequest": {"key": false}, "graph.v1.DeleteVerticesRequest": {"keys": false},
	"graph.v1.GetVerticesResponse": {"missing": false},
	"graph.v1.ScanVerticesRequest": {"prefix": true}, "graph.v1.ScanVertexKeysRequest": {"prefix": true},
	"graph.v1.ScanVertexKeysResponse": {"keys": false},
	"graph.v1.SearchVerticesRequest":  {"prefix": true}, "graph.v1.SearchHit": {"key": false},
	"graph.v1.CountVerticesByPrefixRequest": {"prefix": true}, "graph.v1.DeleteVerticesByPrefixRequest": {"prefix": true},
	"graph.v1.TopVerticesByDegreeRequest": {"prefix": true}, "graph.v1.TopVerticesByDegreeResponse.Entry": {"key": false},
	"graph.v1.GetEdgeRequest": {"tail": false, "head": false}, "graph.v1.DeleteEdgeRequest": {"tail": false, "head": false},
	"graph.v1.DeleteEdgeContributionRequest": {"tail": false, "head": false},
	"graph.v1.ScanEdgesRequest":              {"tail_prefix": true, "head_prefix": true},
	"graph.v1.DeleteEdgesByPrefixRequest":    {"tail_prefix": true, "head_prefix": true},
	"graph.v1.BackupSnapshotRequest":         {"vertex_prefix": true},
}

func mapDataIdentities(message protoreflect.Message, encode bool) error {
	for name, prefix := range dataIdentityFields[message.Descriptor().FullName()] {
		field := message.Descriptor().Fields().ByName(name)
		if field == nil || field.Kind() != protoreflect.StringKind {
			return errors.New("namespace schema mismatch")
		}
		convert := keyspace.LogicalKey
		if encode {
			convert = keyspace.DataKey
			if prefix {
				convert = keyspace.DataKeyPrefix
			}
		}
		if field.IsList() {
			list := message.Mutable(field).List()
			for i := range list.Len() {
				mapped, err := convert(list.Get(i).String())
				if err != nil {
					return err
				}
				list.Set(i, protoreflect.ValueOfString(mapped))
			}
		} else {
			mapped, err := convert(message.Get(field).String())
			if err != nil {
				return err
			}
			message.Set(field, protoreflect.ValueOfString(mapped))
		}
	}
	var failure error
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		if field.Kind() != protoreflect.MessageKind || field.IsMap() {
			return true
		}
		if field.IsList() {
			for i := range value.List().Len() {
				if failure = mapDataIdentities(value.List().Get(i).Message(), encode); failure != nil {
					return false
				}
			}
		} else {
			failure = mapDataIdentities(value.Message(), encode)
		}
		return failure == nil
	})
	return failure
}

func dataCursorBinding(request proto.Message) ([]byte, error) {
	clone := proto.Clone(request)
	// Preserve the canonical ascending default in the encrypted outer scope.
	// Prefix/RPC/direction/policy bindings remain independent of this spelling.
	switch r := clone.(type) {
	case *pb.ScanVerticesRequest:
		if r.Order == pb.ScanOrder_SCAN_ORDER_UNSPECIFIED {
			r.Order = pb.ScanOrder_SCAN_ORDER_ASC
		}
	case *pb.ScanVertexKeysRequest:
		if r.Order == pb.ScanOrder_SCAN_ORDER_UNSPECIFIED {
			r.Order = pb.ScanOrder_SCAN_ORDER_ASC
		}
	}
	m := clone.ProtoReflect()
	for _, name := range []protoreflect.Name{"cursor", "limit"} {
		if field := m.Descriptor().Fields().ByName(name); field != nil {
			m.Clear(field)
		}
	}
	encoded, err := (proto.MarshalOptions{Deterministic: true}).Marshal(clone)
	return append([]byte(keyspace.Version+"\x00"+string(m.Descriptor().FullName())+"\x00"), encoded...), err
}

func dataCursorError(request proto.Message, stale bool, cause error) error {
	if _, search := request.(*pb.SearchVerticesRequest); search {
		if stale {
			return newSearchAbortedError(pb.SearchErrorReason_SEARCH_CURSOR_STALE, cause)
		}
		return newSearchInvalidCursorError(pb.SearchErrorReason_SEARCH_CURSOR_INVALID, cause)
	}
	code := connect.CodeInvalidArgument
	if stale {
		code = connect.CodeAborted
	}
	return connect.NewError(code, cause)
}

func (s *LanternService) mapDataRequest(request proto.Message, scope ...[32]byte) (proto.Message, []byte, error) {
	if !publicDataRequests[request.ProtoReflect().Descriptor().Name()] {
		return nil, nil, connect.NewError(connect.CodeUnimplemented, errors.New("RPC namespace boundary missing"))
	}
	// Check destructive guards in the logical domain. Adding data: must not
	// accidentally turn a prohibited whole-graph predicate into a prefix.
	switch r := request.(type) {
	case *pb.DeleteVerticesByPrefixRequest:
		if r.GetPrefix() == "" {
			return nil, nil, connect.NewError(connect.CodeInvalidArgument, errors.New("prefix is required"))
		}
	case *pb.DeleteEdgesByPrefixRequest:
		if r.GetTailPrefix() == "" && r.GetHeadPrefix() == "" {
			return nil, nil, connect.NewError(connect.CodeInvalidArgument, errors.New("an edge prefix is required"))
		}
	case *pb.TopVerticesByDegreeRequest:
		if r.GetPrefix() == "" {
			return nil, nil, connect.NewError(connect.CodeInvalidArgument, errors.New("prefix is required"))
		}
	}
	var binding []byte
	if request.ProtoReflect().Descriptor().Fields().ByName("cursor") != nil {
		var err error
		binding, err = dataCursorBinding(request)
		if err != nil {
			return nil, nil, connect.NewError(connect.CodeInvalidArgument, errors.New("invalid cursor binding"))
		}
		for _, cut := range scope {
			binding = append(binding, cut[:]...)
		}
	}
	cloned := cloneDataEnvelope(request.ProtoReflect()).Interface()
	m := cloned.ProtoReflect()
	if field := m.Descriptor().Fields().ByName("cursor"); field != nil {
		if cursor := m.Get(field).Bytes(); len(cursor) > 0 {
			if len(cursor) > 16<<10 || len(cursor) < s.namespaceCursor.NonceSize()+s.namespaceCursor.Overhead() {
				return nil, nil, dataCursorError(request, false, errors.New("invalid data cursor"))
			}
			plain, openErr := s.namespaceCursor.Open(nil, cursor[:s.namespaceCursor.NonceSize()], cursor[s.namespaceCursor.NonceSize():], binding)
			if openErr != nil {
				return nil, nil, dataCursorError(request, len(scope) > 0, errors.New("invalid data cursor scope"))
			}
			m.Set(field, protoreflect.ValueOfBytes(plain))
		}
	}
	if err := mapDataIdentities(m, true); err != nil {
		return nil, nil, connect.NewError(connect.CodeInvalidArgument, keyspace.ErrInvalidKey)
	}
	return cloned, binding, nil
}

func (s *LanternService) mapDataResponse(response proto.Message, binding []byte) (proto.Message, error) {
	cloned := cloneDataEnvelope(response.ProtoReflect()).Interface()
	m := cloned.ProtoReflect()
	if err := mapDataIdentities(m, false); err != nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("invalid internal data identity"))
	}
	if field := m.Descriptor().Fields().ByName("next_cursor"); field != nil {
		if cursor := m.Get(field).Bytes(); len(cursor) > 0 {
			nonce := make([]byte, s.namespaceCursor.NonceSize())
			if _, err := rand.Read(nonce); err != nil {
				return nil, connect.NewError(connect.CodeInternal, errors.New("data cursor entropy unavailable"))
			}
			sealed := s.namespaceCursor.Seal(nonce, nonce, cursor, binding)
			m.Set(field, protoreflect.ValueOfBytes(sealed))
		}
	}
	return cloned, nil
}

func dataUnary[Req, Resp any](ctx context.Context, req *connect.Request[Req], svc *LanternService, fn func(context.Context, *Req) (*Resp, error)) (*connect.Response[Resp], error) {
	admission, err := svc.authorizeData(ctx, any(req.Msg).(proto.Message))
	if err != nil {
		return nil, err
	}
	ctx, err = svc.dataQueryContext(ctx, any(req.Msg).(proto.Message), admission)
	if err != nil {
		return nil, err
	}
	blind := blindMutationRequired(admission, any(req.Msg).(proto.Message))
	if svc.namespaceFormat == "" && !blind {
		return unary(ctx, req, fn)
	}
	var scope [][32]byte
	if admission != nil {
		scope = append(scope, admission.ScopeBinding())
	}
	mapped := any(req.Msg).(proto.Message)
	var binding []byte
	if svc.namespaceFormat != "" {
		mapped, binding, err = svc.mapDataRequest(mapped, scope...)
		if err != nil {
			return nil, err
		}
	}
	// The exact mapped batch and immutable access view are complete before
	// the final local admission. No control lock or I/O enters the graph lock.
	if admission != nil {
		if err := admission.Check(ctx, svc.securityNow()); err != nil {
			return nil, connect.NewError(connect.CodeUnavailable, err)
		}
	}
	response, err := fn(ctx, any(mapped).(*Req))
	if err != nil {
		if !blind || !blindReceiptDisposition(err) {
			return nil, err
		}
		response = new(Resp)
	}
	if admission != nil {
		if err := admission.Check(ctx, svc.securityNow()); err != nil {
			return nil, connect.NewError(connect.CodeUnavailable, err)
		}
	}
	logical := any(response).(proto.Message)
	if svc.namespaceFormat != "" {
		logical, err = svc.mapDataResponse(logical, binding)
		if err != nil {
			return nil, err
		}
	}
	if blind {
		logical, err = mutationAcceptanceResponse(logical)
		if err != nil {
			return nil, err
		}
	}
	return connect.NewResponse(any(logical).(*Resp)), nil
}

// DataNamespaceFormat identifies the private graph representation. It does not
// grant a peer or public caller permission to read that representation.
func (s *LanternService) DataNamespaceFormat() string { return s.namespaceFormat }
