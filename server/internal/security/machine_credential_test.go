package security

import (
	"bytes"
	"testing"
	"time"
)

func TestMachineCredentialRoleStateAndRotation(t *testing.T) {
	image, token, now := machineImageFixture(t)
	snapshot, err := CompileImage(image, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	identity, expiry, valid := snapshot.MachineAccess(token, now)
	access, active := snapshot.AccessFor(identity)
	if !valid || !active || identity.Kind != MachinePrincipal || !expiry.Equal(now.Add(time.Hour)) || !access.Allows(VertexRead, "orders:1") || access.Allows(VertexRead, "orders:private:1") || access.AllowsGlobal(SecurityManage) {
		t.Fatal("credential bypassed named Role-only permissions")
	}
	if bytes.Contains(snapshot.image, []byte(token)) {
		t.Fatal("raw bearer entered signed sys state")
	}
	for _, instant := range []time.Time{now.Add(-time.Second), expiry, expiry.Add(time.Second)} {
		if _, _, valid := snapshot.MachineAccess(token, instant); valid {
			t.Fatal("credential lifetime ignored")
		}
	}
	for _, invalid := range []string{token + "=", "Bearer " + token, token[:len(token)-1], "opaque-cookie", "jwt.header.signature"} {
		if _, _, valid := snapshot.MachineAccess(invalid, now); valid {
			t.Fatal("credential kind or canonical format confused")
		}
	}
	for i := range image.Principals {
		if image.Principals[i].Identity == identity {
			image.Principals[i].State = Suspended
		}
	}
	snapshot, err = CompileImage(image, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, valid := snapshot.MachineAccess(token, now); valid {
		t.Fatal("suspension bypassed")
	}
}

func TestMachineCredentialImageRejectsAmbiguousBindings(t *testing.T) {
	image, _, _ := machineImageFixture(t)
	for _, change := range []func(*MachineCredential){
		func(c *MachineCredential) { c.Identity = testIdentity() }, func(c *MachineCredential) { c.Identity.MachineName = "unknown" }, func(c *MachineCredential) { c.Digest = "plaintext" }, func(c *MachineCredential) { c.CreatedAt = c.ExpiresAt }, func(c *MachineCredential) { c.ExpiresAt = c.CreatedAt.Add(MaxMachineCredentialLifetime + time.Second) },
	} {
		candidate := image
		credential := image.MachineCredentials[0]
		change(&credential)
		candidate.MachineCredentials = []MachineCredential{credential}
		if _, err := CompileImage(candidate, DefaultPolicyLimits()); err == nil {
			t.Fatal("invalid machine binding accepted")
		}
	}
	image.MachineCredentials = append(image.MachineCredentials, image.MachineCredentials[0])
	if _, err := CompileImage(image, DefaultPolicyLimits()); err == nil {
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
