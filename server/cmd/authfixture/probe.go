package main

import (
	"strconv"

	"github.com/anaregdesign/lantern/server/internal/security"
)

// Probe credentials use the existing named machine Principal and grant only
// the scoped data transport contracts, never security or private peer access.
func transportProbeRoles() []security.Role {
	role := security.Role{ID: "fixture_data", Name: "Local transport probe"}
	for i, prefix := range []string{"probe/connect/", "probe/grpc/"} {
		for _, permission := range []security.Permission{security.Read, security.Write} {
			role.Rules = append(role.Rules, security.PermissionRule{
				ID:         strconv.Itoa(i) + "_" + string(permission),
				Permission: permission, Resource: security.DataResource, Prefix: &prefix,
			})
		}
	}
	return []security.Role{role}
}
