package security

import "slices"

// Change is a typed security operation, never generic sys: Vertex CRUD. Exactly
// the fields required by its Kind must be present. No Principal operation can
// contain permissions; assignment operations refer only to registered Roles.
type Change struct {
	Kind                  string         `json:"kind"`
	Issuer                *Issuer        `json:"issuer,omitempty"`
	IssuerURL             string         `json:"issuer_url,omitempty"`
	Role                  *Role          `json:"role,omitempty"`
	RoleID                string         `json:"role_id,omitempty"`
	Identity              *Identity      `json:"identity,omitempty"`
	State                 PrincipalState `json:"state,omitempty"`
	PreserveSecret        bool           `json:"preserve_secret,omitempty"`
	PreserveNativeSecrets []string       `json:"preserve_native_secrets,omitempty"`
}

const (
	PutIssuer             = "issuer.put"
	DisableIssuer         = "issuer.disable"
	DeleteIssuer          = "issuer.delete"
	PutRole               = "role.put"
	DeleteRole            = "role.delete"
	PutPrincipal          = "principal.put"
	DeletePrincipal       = "principal.delete"
	PutAssignment         = "assignment.put"
	DeleteAssignment      = "assignment.delete"
	RevokeSessions        = "session.revoke_user"
	MaxTransactionChanges = 100
)

func (c Change) validate() error {
	if c.PreserveSecret && (c.Kind != PutIssuer || c.Issuer == nil || c.Issuer.SecretRef != "") {
		return ErrInvalidImage
	}
	if len(c.PreserveNativeSecrets) > MaxNativeClients || len(c.PreserveNativeSecrets) != 0 && (c.Kind != PutIssuer || c.Issuer == nil) {
		return ErrInvalidImage
	}
	for n, id := range c.PreserveNativeSecrets {
		profile, known := c.Issuer.NativeClient(id)
		if !known || profile.SecretRef != "" || profile.Kind != AppleNative && profile.Kind != AppleAndroidWeb || n > 0 && c.PreserveNativeSecrets[n-1] >= id {
			return ErrInvalidImage
		}
	}
	issuer, role, identity := c.Issuer != nil, c.Role != nil, c.Identity != nil
	switch c.Kind {
	case PutIssuer:
		if c.Issuer != nil && validateIssuerConfiguration(*c.Issuer, c.PreserveNativeSecrets) != nil {
			return ErrInvalidImage
		}
		if !issuer || role || identity || c.IssuerURL != "" || c.RoleID != "" || c.State != "" || c.Issuer.EnvOwned || c.Issuer.Deleted || c.Issuer.ConfigRevision != 0 {
			return ErrInvalidImage
		}
	case DisableIssuer, DeleteIssuer:
		if issuer || role || identity || !validIssuerURL(c.IssuerURL) || c.RoleID != "" || c.State != "" {
			return ErrInvalidImage
		}
	case PutRole:
		if c.Role != nil {
			for _, rule := range c.Role.Rules {
				if !validRoleID(rule.ID) {
					return ErrInvalidPolicy
				}
			}
		}
		if issuer || !role || identity || c.IssuerURL != "" || c.RoleID != "" || c.State != "" {
			return ErrInvalidImage
		}
	case DeleteRole:
		if issuer || role || identity || c.IssuerURL != "" || !validRoleID(c.RoleID) || c.State != "" {
			return ErrInvalidImage
		}
	case PutPrincipal:
		if issuer || role || !identity || !c.Identity.valid() || c.IssuerURL != "" || c.RoleID != "" || (c.State != Active && c.State != Suspended) {
			return ErrInvalidImage
		}
	case DeletePrincipal, RevokeSessions:
		if issuer || role || !identity || !c.Identity.valid() || c.IssuerURL != "" || c.RoleID != "" || c.State != "" {
			return ErrInvalidImage
		}
	case PutAssignment, DeleteAssignment:
		if issuer || role || !identity || !c.Identity.valid() || c.IssuerURL != "" || !validRoleID(c.RoleID) || c.State != "" {
			return ErrInvalidImage
		}
	default:
		return ErrInvalidImage
	}
	return nil
}

// ResolveIssuerSecrets applies the canonical omission intent to an exact prior
// Issuer. It returns a detached candidate; final configuration and provider
// validation still require an actual bound secret for every Apple profile.
func (c Change) ResolveIssuerSecrets(prior Issuer) Issuer {
	if c.Kind != PutIssuer || c.Issuer == nil {
		return Issuer{}
	}
	i := c.Issuer.clone()
	if prior.URL != i.URL {
		return i
	}
	if c.PreserveSecret {
		i.SecretRef = prior.SecretRef
	}
	for n := range i.NativeClients {
		client := &i.NativeClients[n]
		if slices.Contains(c.PreserveNativeSecrets, client.ID) {
			if old, known := prior.NativeClient(client.ID); known && old.Kind == client.Kind && old.ClientID == client.ClientID {
				client.SecretRef = old.SecretRef
			}
		}
	}
	return i
}

func (c Change) target() string {
	switch c.Kind {
	case PutIssuer:
		return "issuer:" + c.Issuer.URL
	case DisableIssuer, DeleteIssuer:
		return "issuer:" + c.IssuerURL
	case PutRole:
		return "role:" + c.Role.ID
	case DeleteRole:
		return "role:" + c.RoleID
	case PutAssignment, DeleteAssignment:
		return "assignment:" + redactedDigest(*c.Identity) + ":" + c.RoleID
	case RevokeSessions:
		return "sessions:" + redactedDigest(*c.Identity)
	default:
		return "principal:" + redactedDigest(*c.Identity)
	}
}
