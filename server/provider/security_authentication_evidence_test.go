package provider

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/golang-jwt/jwt/v5"
)

func TestSecurityAuthenticationEvidenceBearerClassificationAndDeadlines(t *testing.T) {
	for _, qualified := range []bool{false, true} {
		t.Run(map[bool]string{false: "unresolved", true: "enrolled human"}[qualified], func(t *testing.T) {
			config, data, clock := securityRuntimeFixture(t)
			config.Bootstrap.Issuer.HumanSubjectNamespaceQualified = qualified
			idp := newSecurityProviderFixture(t, &config, clock)
			runtime, closeRuntime, err := NewSecurityRuntime(config, data)
			if err != nil {
				t.Fatal(err)
			}
			defer closeRuntime()
			clock.advance(35 * time.Second)
			for _, auth := range []string{"absent", "old"} {
				raw := idp.token(t, "admin", func(c jwt.MapClaims) {
					if auth == "absent" {
						delete(c, "auth_time")
					} else {
						c["auth_time"] = clock.Now().Add(-time.Hour).Unix()
					}
					c["nbf"] = clock.Now().Add(10 * time.Second).Unix()
					c["email"] = "admin@example.com"
				})
				ctx, err := runtime.AuthenticateBearer(t.Context(), http.Header{"Authorization": {"Bearer " + raw}})
				if err != nil {
					t.Fatal(err)
				}
				evidence, ok := authenticationEvidenceFromContext(ctx)
				admission, _ := security.AdmissionFromContext(ctx)
				current, _ := runtime.native.Store().Current()
				if !ok || evidence.kind != "token" || evidence.token.Evidence().Mode != "access" || evidence.session != (security.Session{}) || evidence.generation != current.Generation() || evidence.revision != current.Sequence() || evidence.revisionDigest != current.Digest() || evidence.enrolledIdentity != admission.Identity() || evidence.humanNamespaceQualified != qualified || evidence.issuerConfiguration != evidence.token.Evidence().Configuration || evidence.issuerConfigRevision != 1 {
					t.Fatal("classification source or native cut lost")
				}
				expectedClass := security.UnresolvedActor
				if qualified {
					expectedClass = security.EndUser
				}
				if evidence.classification != expectedClass || (admission.CheckManagement(ctx, current, clock.Now()) == nil) != qualified {
					t.Fatal("evidence changed management policy")
				}
				if evidence.admissionExpiry != admission.ExpiresAt() || !evidence.token.ExpiresAt().After(admission.ExpiresAt()) || evidence.token.Evidence().NotBefore.Time().Unix() != clock.Now().Add(10*time.Second).Unix() || evidence.token.Evidence().AuthTime.Present != (auth == "old") {
					t.Fatal("credential/admission/signed-time facts collapsed")
				}
				if admission.Access().Allows(security.VertexRead, "private:1") {
					t.Fatal("evidence auto-granted data")
				}
				copy := evidence
				copy.enrolledIdentity.Subject = "other"
				again, _ := authenticationEvidenceFromContext(ctx)
				if again != evidence {
					t.Fatal("request evidence mutable")
				}
				short, err := runtime.AuthenticateBearer(ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				retained, _ := authenticationEvidenceFromContext(short)
				if retained != evidence {
					t.Fatal("verified short circuit lost producer")
				}
				if strings.Contains(evidence.String(), raw) {
					t.Fatal("credential logged")
				}
			}
			raw := idp.token(t, "not-enrolled", nil)
			denied, err := runtime.AuthenticateBearer(t.Context(), http.Header{"Authorization": {"Bearer " + raw}})
			if err == nil {
				t.Fatal("evidence enrolled new subject")
			}
			if _, ok := authenticationEvidenceFromContext(denied); ok {
				t.Fatal("failed admission published request evidence")
			}
		})
	}
}

func TestSecurityAuthenticationEvidenceNativeSessionOnly(t *testing.T) {
	runtime, clock, session, csrf := browserSessionFixture(t)
	// Native Sessions have no stored original JWT; unknown auth_time remains
	// unknown and their independent eight-hour lifetime remains available.
	session, _ = newBrowserSecret()
	csrf, _ = newBrowserSecret()
	current, _ := runtime.native.Store().Current()
	identity := security.Identity{Kind: security.OIDCPrincipal, Issuer: runtime.config.Bootstrap.Issuer.URL, Subject: "admin"}
	if _, err := runtime.native.Store().IssueSession(t.Context(), security.SessionRequest{ChangeID: [16]byte{19}, Identity: identity, IssuerConfigRevision: 1, Digest: browserDigest(session), CSRFDigest: browserDigest(csrf), Now: clock.Now(), Lifetime: security.MaxSessionLifetime}); err != nil {
		t.Fatal(err)
	}
	current, _ = runtime.native.Store().Current()
	native, _ := current.Snapshot().Session(browserDigest(session))
	for _, mutation := range []bool{false, true} {
		req := browserFixtureRequest(session, csrf)
		if !mutation {
			req.Header.Del("Origin")
			req.Header.Del(browserCSRFHeader)
		}
		ctx, _, err := runtime.authenticateBrowser(req, mutation)
		if err != nil {
			t.Fatal(err)
		}
		evidence, ok := authenticationEvidenceFromContext(ctx)
		admission, _ := security.AdmissionFromContext(ctx)
		if !ok || evidence.kind != "session" || evidence.token != (oidc.VerifiedIdentity{}) || evidence.session != native || evidence.revisionDigest != current.Digest() || !evidence.session.AuthTime.IsZero() || evidence.session.ExpiresAt != native.CreatedAt.Add(security.MaxSessionLifetime) || !evidence.session.ExpiresAt.After(admission.ExpiresAt()) || evidence.exactOriginChecked != mutation || evidence.mutationCSRFChecked != mutation || evidence.origin == [32]byte{} {
			t.Fatal("session facts or actual checks lost")
		}
		if evidence.session.Digest == session || evidence.session.CSRFDigest == csrf {
			t.Fatal("raw secrets retained")
		}
		if err := runtime.completeOperationAuthentication(ctx, [32]byte{1}, current, clock.Now()); err == nil {
			t.Fatal("session label minted purpose event")
		}
	}
	for _, mutate := range []func(*http.Request){func(r *http.Request) { r.Header.Set(browserCSRFHeader, "wrong") }, func(r *http.Request) { r.Header.Set("Origin", "https://foreign.example") }, func(r *http.Request) { r.Header.Del("Cookie") }} {
		req := browserFixtureRequest(session, csrf)
		mutate(req)
		ctx, _, err := runtime.authenticateBrowser(req, true)
		if err == nil {
			t.Fatal("invalid browser boundary accepted")
		}
		if _, ok := authenticationEvidenceFromContext(ctx); ok {
			t.Fatal("failed check retained session evidence")
		}
	}
}

func TestSecurityAuthenticationEvidenceNativeMachine(t *testing.T) {
	config, data, clock := securityRuntimeFixture(t)
	token, err := security.NewMachineToken()
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := security.MachineTokenDigest(token)
	identity := security.Identity{Kind: security.MachinePrincipal, MachineName: "worker"}
	prefix := "orders:"
	config.Bootstrap.Roles = []security.Role{{ID: "reader", Rules: []security.PermissionRule{{ID: "read", Effect: security.Allow, Action: security.VertexRead, Resource: security.DataResource, Prefix: &prefix}}}}
	expiry := clock.Now().Add(time.Hour)
	config.Bootstrap.Machines = []security.BootstrapMachine{{Name: "worker", RoleIDs: []string{"reader"}, Credentials: []security.MachineCredential{{Identity: identity, Digest: digest, CreatedAt: clock.Now(), ExpiresAt: expiry}}}}
	runtime, cleanup, err := NewSecurityRuntime(config, data)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	clock.advance(35 * time.Second)
	ctx, err := runtime.AuthenticateBearer(t.Context(), http.Header{"Authorization": {"Bearer " + token}})
	if err != nil {
		t.Fatal(err)
	}
	e, ok := authenticationEvidenceFromContext(ctx)
	admission, _ := security.AdmissionFromContext(ctx)
	current, _ := runtime.native.Store().Current()
	if !ok || e.kind != "machine" || e.token != (oidc.VerifiedIdentity{}) || e.machineIdentity != identity || e.machineExpiry != expiry || e.machineCredential != requestCredentialCommitment("native-machine", token) || e.classification != security.MachineActor || e.enrolledIdentity != (security.Identity{}) || e.humanNamespaceQualified || !admission.AuthTime().IsZero() || admission.CheckManagement(ctx, current, clock.Now()) == nil {
		t.Fatal("machine credential invented human/JWT facts")
	}
}
