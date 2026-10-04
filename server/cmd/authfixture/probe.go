package main

import (
	"strconv"
	"strings"

	"github.com/anaregdesign/lantern/server/internal/security"
)

// Probe credentials use the existing named machine Principal and grant only
// the unary/backup transport contracts, never security or private peer access.
func transportProbeRoles() []security.Role {
	role := security.Role{ID: "fixture_data", Name: "Local transport probe"}
	for i, prefix := range []string{"probe/connect/", "probe/grpc/"} {
		for _, action := range []security.Action{security.VertexRead, security.VertexWrite, security.Export} {
			role.Rules = append(role.Rules, security.PermissionRule{
				ID:     strconv.Itoa(i) + "_" + strings.ReplaceAll(string(action), ".", "_"),
				Action: action, Effect: security.Allow, Resource: security.DataResource, Prefix: &prefix,
			})
		}
	}
	return []security.Role{role}
}
