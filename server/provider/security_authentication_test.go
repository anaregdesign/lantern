package provider

import (
	"context"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/golang-jwt/jwt/v5"
)

func TestSecurityAuthenticationVerifiedCurrentCut(t *testing.T) {
	config, data, clock := securityRuntimeFixture(t)
	idp := newSecurityProviderFixture(t, &config, clock)
	runtime, closeRuntime, err := NewSecurityRuntime(config, data)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRuntime()
	clock.advance(35 * time.Second)
	raw := idp.token(t, "admin", func(c jwt.MapClaims) { c["roles"] = []string{"data_admin", "cluster_replica"} })
	headers := http.Header{"Authorization": {"Bearer " + raw}, "Cookie": {"session=forged"}, "X-Lantern-Roles": {"data_admin"}}
	ctx, err := runtime.AuthenticateBearer(t.Context(), headers)
	if err != nil {
		t.Fatal(err)
	}
	admission, known := security.AdmissionFromContext(ctx)
	if !known || admission.Identity().Subject != "admin" || !admission.Access().AllowsGlobal(security.SecurityManage) || admission.Access().Allows(security.VertexRead, "users:1") || admission.Browser() {
		t.Fatal("token/header claims changed server Role membership")
	}
	if admission.ExpiresAt().After(clock.Now().Add(28 * time.Second)) {
		t.Fatal("admission outlives local serving bound")
	}
	calls := idp.fetches.Load()
	for name, header := range map[string]http.Header{
		"missing": {}, "cookie only": {"Cookie": {"__Host-lantern-session=" + raw}}, "duplicate": {"Authorization": {"Bearer " + raw, "Bearer " + raw}}, "malformed": {"Authorization": {"Bearer  " + raw}},
		"unknown issuer": {"Authorization": {"Bearer " + idp.token(t, "admin", func(c jwt.MapClaims) { c["iss"] = "https://unknown.example" })}},
		"expired":        {"Authorization": {"Bearer " + idp.token(t, "admin", func(c jwt.MapClaims) { c["exp"] = clock.Now().Add(-time.Second).Unix() })}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := runtime.AuthenticateBearer(t.Context(), header); connect.CodeOf(err) != connect.CodeUnauthenticated {
				t.Fatal(err)
			}
		})
	}
	if idp.fetches.Load() != calls {
		t.Fatal("malformed or unknown Issuer fetched metadata")
	}
	if _, err := runtime.AuthenticateBearer(t.Context(), http.Header{"Authorization": {"Bearer " + idp.token(t, "unknown", nil)}}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("unregistered user admitted", err)
	}
	current, _ := runtime.native.Store().Current()
	identity := admission.Identity()
	_, err = runtime.native.Store().Manage(context.Background(), security.ManagementRequest{ExpectedRevision: current.Sequence(), ChangeID: [16]byte{1}, Actor: identity, Now: clock.Now(), AuthTime: clock.Now(), Changes: []security.Change{{Kind: security.PutPrincipal, Identity: &identity, State: security.Suspended}}})
	if err != nil {
		t.Fatal(err)
	}
	if admission.Check(t.Context(), clock.Now()) == nil {
		t.Fatal("old admission survived policy change")
	}
	if _, err := runtime.AuthenticateBearer(t.Context(), headers); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("valid token bypassed current suspension", err)
	}
}

func TestSecurityAuthenticationUnavailableAndOff(t *testing.T) {
	config, data, clock := securityRuntimeFixture(t)
	idp := newSecurityProviderFixture(t, &config, clock)
	runtime, closeRuntime, err := NewSecurityRuntime(config, data)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRuntime()
	raw := idp.token(t, "admin", nil)
	if _, err := runtime.AuthenticateBearer(t.Context(), http.Header{"Authorization": {"Bearer " + raw}}); connect.CodeOf(err) != connect.CodeUnavailable || idp.fetches.Load() != 0 {
		t.Fatal("unready writer performed credential fetch", err)
	}
	off, closeOff, err := NewSecurityRuntime(SecurityConfig{Mode: "off"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeOff()
	if ctx, err := off.AuthenticateBearer(t.Context(), nil); err != nil {
		t.Fatal(err)
	} else if _, known := security.AdmissionFromContext(ctx); known {
		t.Fatal("OFF invented authenticated authority")
	}
	var absent *SecurityRuntime
	if _, err := absent.AuthenticateBearer(t.Context(), nil); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal(err)
	}
}
