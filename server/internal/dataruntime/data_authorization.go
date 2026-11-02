package dataruntime

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/proto"
)

// WithDataAuthorization is trusted startup composition. It requires the
// logical/physical mapper and private verified admission on every public RPC.
func (s *Executor) WithDataAuthorization(now func() time.Time) *Executor {
	if s.namespaceFormat != keyspace.Version {
		panic("data authorization requires the namespace boundary")
	}
	s.dataAuthorization = true
	s.securityNow = now
	if s.securityNow == nil {
		s.securityNow = time.Now
	}
	return s
}
func (s *Executor) dataAdmission(ctx context.Context) (*security.Admission, error) {
	if !s.dataAuthorization {
		return nil, nil
	}
	admission, known := security.AdmissionFromContext(ctx)
	if !known {
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("authentication required"))
	}
	if err := admission.Check(ctx, s.securityNow()); err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, security.ErrAuthorityUnavailable)
	}
	return admission, nil
}
func dataPermissionError() error {
	return connect.NewError(connect.CodePermissionDenied, security.ErrPermissionDenied)
}

// authorizeData checks every exact member before graph/receipt lookup or
// mutation. Collection execution receives the admitted range view separately;
// ranking statistics are shared within its existing application-data corpus.
func (s *Executor) authorizeData(ctx context.Context, message proto.Message) (*security.Admission, error) {
	admission, err := s.dataAdmission(ctx)
	if err != nil || admission == nil {
		return admission, err
	}
	plan, err := planDataPermissions(admission.Access(), message)
	if err != nil {
		return admission, err
	}
	if plan.global != "" {
		err = admission.BindCurrentGlobalOutput(ctx, plan.global)
	} else {
		err = bindCurrentDataOutput(ctx, admission, message, plan.resources)
	}
	return admission, err
}

// Only the precomputed local permission is carried into the storage effect
// planner. No policy compilation, control Store lock, lease check or I/O runs
// under the aggregate graph lock.
func (s *Executor) denyVertexLifecycle(ctx context.Context, physical string) bool {
	if !s.dataAuthorization {
		return false
	}
	admission, known := security.AdmissionFromContext(ctx)
	logical, err := keyspace.LogicalKey(physical)
	return !known || err != nil || !admission.Access().Allows(security.VertexDelete, logical) || !admission.Access().Allows(security.VertexRead, logical)
}
func (s *Executor) denyEdgeLifecycle(ctx context.Context, tail, head string) bool {
	if !s.dataAuthorization {
		return false
	}
	admission, known := security.AdmissionFromContext(ctx)
	logicalTail, tailError := keyspace.LogicalKey(tail)
	logicalHead, headError := keyspace.LogicalKey(head)
	return !known || tailError != nil || headError != nil || !admission.Access().AllowsEdge(security.EdgeDelete, logicalTail, logicalHead)
}

func admissionAccess(a *security.Admission) *security.Access {
	if a == nil {
		return nil
	}
	return a.Access()
}
