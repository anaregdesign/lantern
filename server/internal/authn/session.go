package authn

import (
	"context"
	"time"

	"github.com/anaregdesign/lantern/server/internal/security"
)

type ManagedRole struct {
	Role     security.Role
	EnvOwned bool
}

// SessionView is response data, not admission. Its Admission is the existing
// checked request authority, retained only for exact version/cut/CSRF mapping.
type SessionView struct {
	Admission            *security.Admission
	Roles                []ManagedRole
	RecentAuthentication bool
}

// InspectSession is shared by browser bootstrap and Connect GetCurrentPrincipal.
// The concrete state owners are supplied by the same certified composition.
func InspectSession(ctx context.Context, a *security.Admission, store *security.Store, current *security.CurrentAuthority, now func() time.Time) (SessionView, error) {
	if store == nil && current == nil {
		return SessionView{}, security.ErrS1Contract
	}
	if a == nil {
		return SessionView{}, ErrUnauthenticated
	}
	if err := a.Check(ctx, now()); err != nil {
		return SessionView{}, err
	}
	if current != nil {
		profile, known := a.CurrentProfile()
		if !known || profile != current.Profile() {
			return SessionView{}, ErrUnauthenticated
		}
		if err := a.BindCurrentSelfOutput(ctx); err != nil {
			return SessionView{}, err
		}
	} else {
		if a.Revision() == nil {
			return SessionView{}, ErrUnauthenticated
		}
		revision, known := store.Current()
		if !known || revision.Digest() != a.Revision().Digest() {
			return SessionView{}, security.ErrAuthorityUnavailable
		}
	}
	result := SessionView{Admission: a, RecentAuthentication: !a.AuthTime().IsZero() && !a.AuthTime().After(now()) && now().Sub(a.AuthTime()) <= security.RecentAuthenticationLifetime}
	image := a.Snapshot().Image()
	assigned, locked := make(map[string]bool), make(map[string]bool)
	for _, user := range image.Principals {
		for _, assignment := range user.Assignments {
			if user.Identity == a.Identity() {
				assigned[assignment.RoleID] = true
			}
			if assignment.EnvOwned {
				locked[assignment.RoleID] = true
			}
		}
	}
	for _, role := range image.Roles {
		if assigned[role.ID] {
			result.Roles = append(result.Roles, ManagedRole{Role: role, EnvOwned: locked[role.ID]})
		}
	}
	if err := a.Check(ctx, now()); err != nil {
		return SessionView{}, err
	}
	return result, nil
}
