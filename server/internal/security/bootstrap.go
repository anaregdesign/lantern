package security

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"sort"
)

// Bootstrap is operator-owned input. Its monotonic revision and canonical
// digest prevent an old pod or conflicting same-version configuration from
// silently restoring removed administrators. Tokens/secrets are not input.
type Bootstrap struct {
	Revision      uint64             `json:"revision"`
	Issuer        Issuer             `json:"issuer"`
	AdminSubjects []string           `json:"admin_subjects"`
	Roles         []Role             `json:"roles,omitempty"`
	Machines      []BootstrapMachine `json:"machines,omitempty"`
}
type BootstrapMachine struct {
	Name        string              `json:"name"`
	RoleIDs     []string            `json:"role_ids"`
	Credentials []MachineCredential `json:"credentials,omitempty"`
}

func (b Bootstrap) canonical() (Bootstrap, string, error) {
	if b.Revision == 0 || !b.Issuer.Enabled || b.Issuer.Deleted || !validIssuerURL(b.Issuer.URL) || len(b.AdminSubjects) == 0 || len(b.AdminSubjects) > 64 || len(b.Machines) > 64 {
		return Bootstrap{}, "", ErrInvalidImage
	}
	b.Issuer.EnvOwned = true
	b.Issuer.ConfigRevision = 0
	b.Issuer.Algorithms = append([]string(nil), b.Issuer.Algorithms...)
	sort.Strings(b.Issuer.Algorithms)
	b.AdminSubjects = append([]string(nil), b.AdminSubjects...)
	sort.Strings(b.AdminSubjects)
	for i, subject := range b.AdminSubjects {
		if !validSubject(subject) || i > 0 && b.AdminSubjects[i-1] == subject {
			return Bootstrap{}, "", ErrInvalidImage
		}
	}
	b.Roles = append([]Role(nil), b.Roles...)
	sort.Slice(b.Roles, func(i, j int) bool { return b.Roles[i].ID < b.Roles[j].ID })
	for i := range b.Roles {
		if b.Roles[i].ID == "security_admin" {
			return Bootstrap{}, "", ErrBootstrapLocked
		}
		b.Roles[i].Rules = append([]PermissionRule(nil), b.Roles[i].Rules...)
		for j := range b.Roles[i].Rules {
			if b.Roles[i].Rules[j].Prefix != nil {
				value := *b.Roles[i].Rules[j].Prefix
				b.Roles[i].Rules[j].Prefix = &value
			}
		}
		sort.Slice(b.Roles[i].Rules, func(a, c int) bool { return b.Roles[i].Rules[a].ID < b.Roles[i].Rules[c].ID })
		if err := (Change{Kind: PutRole, Role: &b.Roles[i]}).validate(); err != nil {
			return Bootstrap{}, "", err
		}
	}
	if _, err := CompileRoles(b.Roles, DefaultPolicyLimits()); err != nil {
		return Bootstrap{}, "", err
	}
	b.Machines = append([]BootstrapMachine(nil), b.Machines...)
	sort.Slice(b.Machines, func(i, j int) bool { return b.Machines[i].Name < b.Machines[j].Name })
	roleIDs := make(map[string]bool)
	for _, role := range b.Roles {
		roleIDs[role.ID] = true
	}
	for i := range b.Machines {
		machine := &b.Machines[i]
		machine.RoleIDs = append([]string(nil), machine.RoleIDs...)
		sort.Strings(machine.RoleIDs)
		if !validRoleID(machine.Name) || i > 0 && b.Machines[i-1].Name == machine.Name || len(machine.RoleIDs) == 0 || len(machine.RoleIDs) > DefaultPolicyLimits().MaxAssignments {
			return Bootstrap{}, "", ErrInvalidImage
		}
		for j, role := range machine.RoleIDs {
			if !roleIDs[role] || j > 0 && machine.RoleIDs[j-1] == role {
				return Bootstrap{}, "", ErrInvalidImage
			}
		}
		machine.Credentials = append([]MachineCredential(nil), machine.Credentials...)
		sort.Slice(machine.Credentials, func(a, c int) bool { return machine.Credentials[a].Digest < machine.Credentials[c].Digest })
		if len(machine.Credentials) > 4 {
			return Bootstrap{}, "", ErrInvalidImage
		}
		for j, credential := range machine.Credentials {
			if !credential.valid() || credential.Identity.MachineName != machine.Name || j > 0 && machine.Credentials[j-1].Digest == credential.Digest {
				return Bootstrap{}, "", ErrInvalidImage
			}
		}
	}
	encoded, err := json.Marshal(b)
	if err != nil || len(encoded) > MaxImageBytes {
		return Bootstrap{}, "", ErrInvalidImage
	}
	digest := sha256.Sum256(encoded)
	return b, hex.EncodeToString(digest[:]), nil
}
func (s *Store) ApplyBootstrap(ctx context.Context, configuration Bootstrap) (ChangeResult, error) {
	b, digest, err := configuration.canonical()
	if err != nil {
		return ChangeResult{}, err
	}
	current, known := s.Current()
	image := Image{Version: ImageVersion}
	expected := uint64(0)
	if known {
		image = current.Snapshot().Image()
		expected = current.Sequence()
		if b.Revision < image.BootstrapRevision || b.Revision == image.BootstrapRevision && digest != image.BootstrapDigest {
			return ChangeResult{}, ErrBootstrapLocked
		}
		if b.Revision == image.BootstrapRevision {
			return ChangeResult{Revision: current.Sequence(), Digest: current.Digest(), Replayed: true}, nil
		}
	}
	// Remove previous operator membership only. Preserve independently assigned
	// Roles and lifecycle state, including suspension/deletion of known users.
	for i := range image.Principals {
		assignments := image.Principals[i].Assignments[:0]
		for _, assignment := range image.Principals[i].Assignments {
			if !assignment.EnvOwned {
				assignments = append(assignments, assignment)
			}
		}
		image.Principals[i].Assignments = assignments
	}
	issuerRevision := uint64(1)
	issuerIndex := -1
	for i := range image.Issuers {
		if image.Issuers[i].URL == b.Issuer.URL {
			issuerIndex = i
			issuerRevision = image.Issuers[i].ConfigRevision + 1
			if issuerRevision == 0 {
				return ChangeResult{}, ErrInvalidImage
			}
		}
		if image.Issuers[i].EnvOwned {
			image.Issuers[i].Enabled = false
			image.Issuers[i].Deleted = true
		}
	}
	b.Issuer.ConfigRevision = issuerRevision
	if issuerIndex < 0 {
		image.Issuers = append(image.Issuers, b.Issuer)
	} else {
		image.Issuers[issuerIndex] = b.Issuer
	}
	admin := Role{ID: "security_admin", Name: "Security administrator", Rules: []PermissionRule{{ID: "manage", Effect: Allow, Action: SecurityManage, Resource: GlobalResource}}}
	for _, role := range append([]Role{admin}, b.Roles...) {
		index := slices.IndexFunc(image.Roles, func(existing Role) bool { return existing.ID == role.ID })
		if index < 0 {
			image.Roles = append(image.Roles, role)
		} else {
			image.Roles[index] = role
		}
	}
	assign := func(identity Identity, roleIDs []string) error {
		index := slices.IndexFunc(image.Principals, func(principal Principal) bool { return principal.Identity == identity })
		if index < 0 {
			image.Principals = append(image.Principals, Principal{Identity: identity, State: Active})
			index = len(image.Principals) - 1
		}
		principal := &image.Principals[index]
		if principal.State == Deleted {
			return ErrBootstrapLocked
		}
		for _, roleID := range roleIDs {
			found := slices.IndexFunc(principal.Assignments, func(assignment RoleAssignment) bool { return assignment.RoleID == roleID })
			if found < 0 {
				principal.Assignments = append(principal.Assignments, RoleAssignment{RoleID: roleID, EnvOwned: true})
			} else {
				principal.Assignments[found].EnvOwned = true
			}
		}
		return nil
	}
	for _, subject := range b.AdminSubjects {
		if err := assign(Identity{Kind: OIDCPrincipal, Issuer: b.Issuer.URL, Subject: subject}, []string{admin.ID}); err != nil {
			return ChangeResult{}, err
		}
	}
	image.MachineCredentials = nil
	for _, machine := range b.Machines {
		if err := assign(Identity{Kind: MachinePrincipal, MachineName: machine.Name}, machine.RoleIDs); err != nil {
			return ChangeResult{}, err
		}
		image.MachineCredentials = append(image.MachineCredentials, machine.Credentials...)
	}
	// Operator bootstrap changes invalidate every existing browser session.
	for i := range image.Sessions {
		image.Sessions[i].Revoked = true
	}
	image.BootstrapRevision = b.Revision
	image.BootstrapDigest = digest
	changeHash := sha256.Sum256([]byte("bootstrap\x00" + digest))
	var changeID [16]byte
	copy(changeID[:], changeHash[:16])
	return s.ReconcileBootstrap(ctx, expected, changeID, image)
}

func (b Bootstrap) Validate() error {
	canonical, _, err := b.canonical()
	if err != nil {
		return err
	}
	issuer := canonical.Issuer
	issuer.ConfigRevision = 1
	image := Image{Version: ImageVersion, Issuers: []Issuer{issuer}, Roles: append([]Role{{ID: "security_admin", Rules: []PermissionRule{{ID: "manage", Effect: Allow, Action: SecurityManage, Resource: GlobalResource}}}}, canonical.Roles...)}
	for _, subject := range canonical.AdminSubjects {
		image.Principals = append(image.Principals, Principal{Identity: Identity{Kind: OIDCPrincipal, Issuer: issuer.URL, Subject: subject}, State: Active, Assignments: []RoleAssignment{{RoleID: "security_admin", EnvOwned: true}}})
	}
	for _, machine := range canonical.Machines {
		principal := Principal{Identity: Identity{Kind: MachinePrincipal, MachineName: machine.Name}, State: Active}
		for _, roleID := range machine.RoleIDs {
			principal.Assignments = append(principal.Assignments, RoleAssignment{RoleID: roleID, EnvOwned: true})
		}
		image.Principals = append(image.Principals, principal)
		image.MachineCredentials = append(image.MachineCredentials, machine.Credentials...)
	}
	_, err = CompileImage(image, DefaultPolicyLimits())
	return err
}
