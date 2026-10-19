package provider

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"strconv"

	"github.com/anaregdesign/lantern/server/internal/authn"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/service"
)

func (r *SecurityRuntime) openAuthority() error {
	p, err := security.LoadCurrentProvisioning(r.config.ConfigFile)
	if err != nil {
		return err
	}
	// This starts the independent private listener before public readiness;
	// absence of a quorum leaves the actual current owner unavailable, not OFF.
	r.current, err = security.OpenCurrentAuthority(context.Background(), p, r.config.StoreMode, r.config.BrowserOrigin)
	if err != nil {
		return err
	}
	r.now = r.current.VerificationTime
	if _, err = rand.Read(r.attemptProcess[:]); err != nil {
		return err
	}
	roots, err := loadSecurityRoots(r.config.RootCAFile)
	if err != nil {
		return err
	}
	r.fetcher, err = oidc.NewFetcher(oidc.FetcherOptions{PrivateOrigins: r.config.PrivateOrigins, Roots: roots, TimeBounds: r.current.TimeBounds})
	if err != nil {
		return err
	}
	r.secrets, err = oidc.NewSecretRegistry(r.config.SecretBindings)
	if err != nil {
		return err
	}
	r.keys = oidc.NewKeyCacheWithClock(r.fetcher, r.now)
	r.verifier = oidc.NewVerifierWithClock(r.keys, r.now)
	r.logins, err = oidc.NewLoginTransactionsWithBounds(r.config.BrowserOrigin, []string{"/", "/vertices", "/edges", "/search", "/security/roles", "/security/users", "/security/issuers", "/server", "/replication"}, r.current.TimeBounds, r.currentAttemptPrefix())
	if err != nil {
		return err
	}
	r.credentials = authn.NewCredentials(r.keys)
	r.nativeLogins, err = oidc.NewNativeTransactions(r.current.TimeBounds, r.currentAttemptPrefix())
	if err != nil {
		return err
	}
	r.workflow, err = authn.NewWorkflow(r.current, r.loginResources(), r.currentAttemptPrefix())
	if err != nil {
		return err
	}
	r.composeAuthentication()
	r.control, err = service.NewSecurityConnectHandler(service.SecurityServiceOptions{Current: r.current, Now: r.now, ValidateIssuer: func(ctx context.Context, i security.Issuer) error {
		if i.BrowserEnabled() && i.RedirectURI != r.config.BrowserOrigin+oidc.CallbackPath(i.URL) {
			return oidc.ErrInvalidDocument
		}
		return r.fetcher.ValidateIssuer(ctx, i, r.secrets)
	}, CurrentBeginAuthorization: r.authentication.BeginCurrentManagementAuthorization, CurrentReadAuthorization: r.authentication.ReadCurrentManagementAuthorization})
	return err
}

func (r *SecurityRuntime) currentAttemptAffinity(id [32]byte) string {
	if r.current == nil || r.attemptProcess == [32]byte{} || id == [32]byte{} {
		return ""
	}
	return r.currentAttemptPrefix() + "." + base64.RawURLEncoding.EncodeToString(id[:])
}

func (r *SecurityRuntime) currentAttemptPrefix() string {
	if r.current == nil || r.attemptProcess == [32]byte{} {
		return ""
	}
	return "v2." + strconv.FormatUint(uint64(r.current.MemberID()), 10) + "." + base64.RawURLEncoding.EncodeToString(r.attemptProcess[:])
}

// ExportCurrentAuthorityFloors returns the owner's minimum-cut document for
// observation. It is not a final shutdown cut and does not publish custody.
// Shutdown owns the durable restart checkpoint; no HTTP floor API is exposed.
func (r *SecurityRuntime) ExportCurrentAuthorityFloors() ([]byte, error) {
	if r == nil || r.current == nil {
		return nil, security.ErrAuthorityUnavailable
	}
	return r.current.ExportFloors()
}
