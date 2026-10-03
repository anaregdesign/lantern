package security

type PrincipalState string

const (
	Active    PrincipalState = "active"
	Suspended PrincipalState = "suspended"
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
}
