package security

type PrincipalState string

const (
	Active    PrincipalState = "active"
	Suspended PrincipalState = "suspended"
	Deleted   PrincipalState = "deleted"
)

// RoleAssignment is the only path from a Principal to permissions. EnvOwned
// assignments are reconciled by the writer's bootstrap configuration revision.
type RoleAssignment struct {
	RoleID   string `json:"role_id"`
	EnvOwned bool   `json:"env_owned,omitempty"`
}

type Principal struct {
	Identity    Identity         `json:"identity"`
	State       PrincipalState   `json:"state"`
	Assignments []RoleAssignment `json:"assignments"`
	// Set only by exact operator human enrollment or verified Code login, not
	// by public Principal/assignment changes. It survives session expiry.
	HumanIssuerConfigRevision uint64 `json:"human_issuer_config_revision,omitempty"`
}
