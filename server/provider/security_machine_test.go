package provider

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestSecurityMachinePrivateBootstrapInput(t *testing.T) {
	config, _, clock := securityRuntimeFixture(t)
	token, err := security.NewMachineToken()
	if err != nil {
		t.Fatal(err)
	}
	now := clock.Now()
	raw, _ := json.Marshal([]map[string]any{{"name": "worker", "role_ids": []string{"reader"}, "credentials": []map[string]any{{"token": token, "created_at": now, "expires_at": now.Add(time.Hour)}}}})
	path := filepath.Join(t.TempDir(), "machines.json")
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	machines, err := loadSecurityMachines(path)
	if err != nil || len(machines) != 1 {
		t.Fatal("private bootstrap rejected", err)
	}
	encoded, _ := json.Marshal(machines)
	if strings.Contains(string(encoded), token) || machines[0].Credentials[0].Digest == "" {
		t.Fatal("bootstrap retained raw token")
	}
	prefix := "orders:"
	config.Bootstrap.Roles = []security.Role{{ID: "reader", Rules: []security.PermissionRule{{ID: "read", Effect: security.Allow, Action: security.VertexRead, Resource: security.DataResource, Prefix: &prefix}}}}
	config.Bootstrap.Machines = machines
	if err := config.Bootstrap.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, variant := range []string{"public", "symlink", "duplicate", "unknown", "invalid-token", "oversized"} {
		t.Run(variant, func(t *testing.T) {
			candidate := filepath.Join(t.TempDir(), "input.json")
			content := raw
			perm := os.FileMode(0600)
			switch variant {
			case "public":
				perm = 0644
			case "symlink":
				if err := os.Symlink(path, candidate); err != nil {
					t.Fatal(err)
				}
			case "duplicate":
				content = []byte(`[{"name":"worker","name":"other"}]`)
			case "unknown":
				content = []byte(`[{"name":"worker","permissions":["all"]}]`)
			case "invalid-token":
				content = []byte(strings.ReplaceAll(string(raw), token, "a-short-password"))
			case "oversized":
				content = []byte(strings.Repeat("x", 1<<20+1))
			}
			if variant != "symlink" {
				if err := os.WriteFile(candidate, content, perm); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := loadSecurityMachines(candidate); err == nil || strings.Contains(err.Error(), token) {
				t.Fatal("unsafe input accepted or secret disclosed")
			}
		})
	}
}

func TestSecurityMachineAuthenticationUsesCurrentRoleCut(t *testing.T) {
	config, data, clock := securityRuntimeFixture(t)
	token, err := security.NewMachineToken()
	if err != nil {
		t.Fatal(err)
	}
	digest, _ := security.MachineTokenDigest(token)
	identity := security.Identity{Kind: security.MachinePrincipal, MachineName: "worker"}
	prefix, denied := "orders:", "orders:private:"
	config.Bootstrap.Roles = []security.Role{{ID: "reader", Rules: []security.PermissionRule{
		{ID: "read", Effect: security.Allow, Action: security.VertexRead, Resource: security.DataResource, Prefix: &prefix},
		{ID: "private", Effect: security.Deny, Action: security.VertexRead, Resource: security.DataResource, Prefix: &denied},
	}}}
	config.Bootstrap.Machines = []security.BootstrapMachine{{Name: "worker", RoleIDs: []string{"reader"}, Credentials: []security.MachineCredential{{Identity: identity, Digest: digest, CreatedAt: clock.Now(), ExpiresAt: clock.Now().Add(time.Hour)}}}}
	runtime, cleanup, err := NewSecurityRuntime(config, data)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	clock.advance(35 * time.Second)
	headers := http.Header{"Authorization": {"Bearer " + token}, "X-Lantern-Roles": {"security_admin"}}
	ctx, err := runtime.AuthenticateBearer(t.Context(), headers)
	if err != nil {
		t.Fatal(err)
	}
	admission, known := security.AdmissionFromContext(ctx)
	if !known || admission.Identity() != identity || !admission.AuthTime().IsZero() || admission.Browser() || !admission.Access().Allows(security.VertexRead, "orders:1") || admission.Access().Allows(security.VertexRead, "orders:private:1") || admission.Access().AllowsGlobal(security.SecurityManage) {
		t.Fatal("machine/header claim confused with OIDC or Role policy")
	}
	current, _ := runtime.native.Store().Current()
	image := current.Snapshot().Image()
	for i := range image.Principals {
		if image.Principals[i].Identity == identity {
			image.Principals[i].State = security.Suspended
		}
	}
	if _, err := runtime.native.Store().Commit(t.Context(), current.Sequence(), [16]byte{2}, image); err != nil {
		t.Fatal(err)
	}
	if admission.Check(t.Context(), clock.Now()) == nil {
		t.Fatal("captured machine admission survived policy loss")
	}
	if _, err := runtime.AuthenticateBearer(t.Context(), headers); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatal("current suspension bypassed", err)
	}
}
