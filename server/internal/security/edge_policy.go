package security

import "github.com/anaregdesign/lantern/server/internal/authz"

const EdgePolicyVersion = 1

// EdgePolicy is managed via the existing reviewed native security transaction.
// A version-3 fresh image's absent policy has the explicit RR default. Old
// image versions must undergo reviewed migration and never inherit this default.
type EdgePolicy struct {
	Version int                    `json:"version"`
	Mode    authz.EdgeEndpointMode `json:"mode"`
}

func (p *EdgePolicy) valid() bool {
	return p == nil || p.Version == EdgePolicyVersion && p.Mode.Valid()
}
func (p *EdgePolicy) mode() authz.EdgeEndpointMode {
	if p == nil {
		return authz.EdgeReadRead
	}
	return p.Mode
}
func (p *EdgePolicy) clone() *EdgePolicy {
	if p == nil {
		return nil
	}
	c := *p
	return &c
}
func (s *Snapshot) EdgePolicy() EdgePolicy {
	if s == nil || s.edgePolicy == nil {
		return EdgePolicy{Version: EdgePolicyVersion, Mode: authz.EdgeReadRead}
	}
	return *s.edgePolicy
}
