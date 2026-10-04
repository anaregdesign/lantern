package security

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func TestMachineCredentialRoleStateAndRotation(t *testing.T) {
	store, _, _ := testStore(t, true)
	bootstrap, token, now := machineBootstrapFixture(t)
	if _, err := store.ApplyBootstrap(t.Context(), bootstrap); err != nil {
		t.Fatal(err)
	}
	current, _ := store.Current()
	identity, expiry, valid := current.Snapshot().MachineAccess(token, now)
	access, active := current.Snapshot().AccessFor(identity)
	if !valid || !active || identity.Kind != MachinePrincipal || !expiry.Equal(now.Add(time.Hour)) || !access.Allows(VertexRead, "orders:1") || access.Allows(VertexRead, "orders:private:1") || access.AllowsGlobal(SecurityManage) {
		t.Fatal("credential bypassed named Role-only permissions")
	}
	if bytes.Contains(current.Encode(), []byte(token)) {
		t.Fatal("raw bearer entered signed sys state")
	}
	for _, instant := range []time.Time{now.Add(-time.Second), expiry, expiry.Add(time.Second)} {
		if _, _, valid := current.Snapshot().MachineAccess(token, instant); valid {
			t.Fatal("credential lifetime ignored")
		}
	}
	for _, invalid := range []string{token + "=", "Bearer " + token, token[:len(token)-1], "opaque-cookie", "jwt.header.signature"} {
		if _, _, valid := current.Snapshot().MachineAccess(invalid, now); valid {
			t.Fatal("credential kind or canonical format confused")
		}
	}
	image := current.Snapshot().Image()
	image.MachineCredentials = nil
	if _, err := store.Commit(t.Context(), current.Sequence(), [16]byte{2}, image); !errors.Is(err, ErrBootstrapLocked) {
		t.Fatal("management changed operator credential", err)
	}
	image = current.Snapshot().Image()
	for i := range image.Principals {
		if image.Principals[i].Identity == identity {
			image.Principals[i].State = Suspended
		}
	}
	if _, err := store.Commit(t.Context(), current.Sequence(), [16]byte{3}, image); err != nil {
		t.Fatal(err)
	}
	current, _ = store.Current()
	if _, _, valid := current.Snapshot().MachineAccess(token, now); valid {
		t.Fatal("suspension bypassed")
	}
	bootstrap.Revision++
	if _, err := store.ApplyBootstrap(t.Context(), bootstrap); err != nil {
		t.Fatal(err)
	}
	current, _ = store.Current()
	if _, _, valid := current.Snapshot().MachineAccess(token, now); valid {
		t.Fatal("bootstrap resurrected machine account")
	}
	bootstrap.Revision++
	bootstrap.Machines = nil
	if _, err := store.ApplyBootstrap(t.Context(), bootstrap); err != nil {
		t.Fatal(err)
	}
	bootstrap.Revision--
	if _, err := store.ApplyBootstrap(t.Context(), bootstrap); !errors.Is(err, ErrBootstrapLocked) {
		t.Fatal("old config restored removed credential", err)
	}
}

func TestMachineCredentialImageRejectsAmbiguousBindings(t *testing.T) {
	bootstrap, _, _ := machineBootstrapFixture(t)
	for _, change := range []func(*MachineCredential){
		func(c *MachineCredential) { c.Identity = testIdentity() },
		func(c *MachineCredential) { c.Identity.MachineName = "unknown" },
		func(c *MachineCredential) { c.Digest = "plaintext" },
		func(c *MachineCredential) { c.CreatedAt = c.ExpiresAt },
		func(c *MachineCredential) { c.ExpiresAt = c.CreatedAt.Add(MaxMachineCredentialLifetime + time.Second) },
	} {
		credential := bootstrap.Machines[0].Credentials[0]
		change(&credential)
		candidate := bootstrap
		candidate.Machines = []BootstrapMachine{{Name: "worker", RoleIDs: []string{"reader"}, Credentials: []MachineCredential{credential}}}
		if candidate.Validate() == nil {
			t.Fatal("invalid machine binding accepted")
		}
	}
	bootstrap.Machines[0].Credentials = append(bootstrap.Machines[0].Credentials, bootstrap.Machines[0].Credentials[0])
	if bootstrap.Validate() == nil {
		t.Fatal("duplicate credential accepted")
	}
}

func BenchmarkMachineCredentialAdmission(b *testing.B) {
	raw, err := NewMachineToken()
	if err != nil {
		b.Fatal(err)
	}
	digest, _ := MachineTokenDigest(raw)
	now := time.Now()
	image := testImage()
	identity := Identity{Kind: MachinePrincipal, MachineName: "worker"}
	image.Principals = append(image.Principals, Principal{Identity: identity, State: Active, Assignments: []RoleAssignment{{RoleID: "reader"}}})
	image.MachineCredentials = []MachineCredential{{Identity: identity, Digest: digest, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}}
	snapshot, err := CompileImage(image, DefaultPolicyLimits())
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		if _, _, valid := snapshot.MachineAccess(raw, now); !valid {
			b.Fatal("invalid credential")
		}
	}
}
