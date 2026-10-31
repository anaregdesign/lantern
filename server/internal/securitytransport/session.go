package securitytransport

import (
	"errors"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/authn"
	"github.com/anaregdesign/lantern/server/internal/security"
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
	response.UserId = sessionUserID(a.Snapshot(), a.Identity())

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

func sessionUserID(snapshot *security.Snapshot, identity security.Identity) *string {
	if snapshot == nil {
		return nil
	}
	id, _, present := snapshot.PersonalArea(identity)
	if !present {
		return nil
	}
	return &id
}
