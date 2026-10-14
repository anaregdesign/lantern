package service

import (
	"errors"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/authn"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func sessionErrorCode(err error) connect.Code {
	if errors.Is(err, authn.ErrUnauthenticated) {
		return connect.CodeUnauthenticated
	}
	return securityErrorCode(err)
}
func encodeSessionView(view authn.SessionView) *pb.GetCurrentPrincipalResponse {
	a := view.Admission
	response := &pb.GetCurrentPrincipalResponse{Identity: encodeSecurityIdentity(a.Identity()), Version: SecurityVersionForAdmission(a), ExpiresAt: timestamppb.New(a.ExpiresAt()), RecentAuthentication: view.RecentAuthentication}
	if id, _, present := a.Snapshot().PersonalArea(a.Identity()); present {
		response.UserId = &id
	}
	for _, role := range view.Roles {
		encoded := encodeSecurityRole(role.Role)
		encoded.EnvOwned = role.EnvOwned
		response.Roles = append(response.Roles, encoded)
	}
	if a.Browser() {
		response.CsrfToken = a.CSRFToken()
	}
	return response
}
