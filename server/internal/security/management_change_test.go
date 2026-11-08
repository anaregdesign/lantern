package security

import (
	"testing"

	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
)

func TestManagementChangeRejectsAmbiguousAndDirectGrantShapes(t *testing.T) {
	identity := testIdentity()
	for _, change := range []Change{
		{Kind: "direct_permission", Identity: &identity},
		{Kind: PutPrincipal, Identity: &identity, State: Active, Role: &Role{ID: "escape"}},
		{Kind: PutPrincipal, Identity: &identity, State: Deleted},
		{Kind: PutAssignment, Identity: &identity, RoleID: "role", State: Active},
		{Kind: PutIssuer, Issuer: &Issuer{URL: "https://issuer.example", ClientID: "a", APIAudience: "b", Algorithms: []string{"RS256"}, EnvOwned: true}},
		{Kind: PutRole, Role: &Role{ID: "role", Rules: []PermissionRule{dataRule(VertexRead, "")}}},
	} {
		if err := change.validate(); err == nil {
			t.Fatal("invalid typed shape accepted", change.Kind)
		}
	}
}

func TestManagementLogPolicyChangeClosedShape(t *testing.T) {
	base := Change{Kind: PutLogPolicy, LogPolicy: &LogPolicyChange{Version: LogPolicyVersion, Level: LogLevelInfo}}
	if err := base.validate(); err != nil || base.target() != "log_policy" {
		t.Fatal("valid managed log operation rejected", err)
	}
	identity := testIdentity()
	for _, mutate := range []func(*Change){
		func(c *Change) { c.LogPolicy = nil },
		func(c *Change) { c.LogPolicy = &LogPolicyChange{Version: 2, Level: LogLevelInfo} },
		func(c *Change) { c.LogPolicy = &LogPolicyChange{Version: 1, Level: "INFO"} },
		func(c *Change) { c.Kind = "log_policy.delete" },
		func(c *Change) { c.Issuer = &Issuer{} },
		func(c *Change) { c.IssuerURL = "https://issuer.example" },
		func(c *Change) { c.Role = &Role{} },
		func(c *Change) { c.RoleID = "reader" },
		func(c *Change) { c.Identity = &identity },
		func(c *Change) { c.State = Active },
		func(c *Change) { c.PreserveSecret = true },
		func(c *Change) { c.PreserveNativeSecrets = []string{"ios"} },
		func(c *Change) { c.RegistrationPolicy = &RegistrationPolicy{} },
	} {
		candidate := base
		mutate(&candidate)
		if candidate.validate() == nil {
			t.Fatal("ambiguous managed log operation admitted")
		}
	}
	for _, change := range []Change{
		{Kind: RevokeSessions, Identity: &identity, LogPolicy: base.LogPolicy},
		{Kind: PutRole, Role: &Role{ID: "reader"}, LogPolicy: base.LogPolicy},
	} {
		if change.validate() == nil {
			t.Fatal("log payload admitted on another operation")
		}
	}
	state := s1Fixture(t, s1Image())
	if _, err := NewS1Operation(testIdentity(), state.projection.Cut(), s1Changes(base, base), DefaultPolicyLimits()); err == nil {
		t.Fatal("duplicate policy targets admitted")
	}
	var decoded Change
	if s2StrictJSON([]byte(`{"kind":"log_policy.put","log_policy":{"version":1,"level":"info","generation":7}}`), &decoded, DefaultPolicyLimits()) == nil {
		t.Fatal("caller-owned generation admitted")
	}
}

func TestManagementOperationalClosedShapeAndSingleProof(t *testing.T) {
	state := s1Fixture(t, s1Image())
	base := operationalPolicyRuntimeCommand(operationalpolicy.GroupSlowRPC).Changes[0]
	if base.validate() != nil || base.target() != "operational_policy:slow_rpc" {
		t.Fatal("valid closed operation refused")
	}
	for _, changes := range [][]Change{
		{base, operationalPolicyRuntimeCommand(operationalpolicy.GroupRate).Changes[0]},
		{base, {Kind: PutLogPolicy, LogPolicy: &LogPolicyChange{Version: LogPolicyVersion, Level: LogLevelInfo}}},
		{{Kind: PutRole, Role: &Role{ID: "ordinary"}, OperationalPolicy: base.OperationalPolicy}},
		{{Kind: PutOperationalPolicy}},
	} {
		if _, err := NewS1Operation(testIdentity(), state.projection.Cut(), s1Changes(changes...), DefaultPolicyLimits()); err == nil {
			t.Fatal("ambiguous/unsupported operation reached original construction")
		}
	}
}
