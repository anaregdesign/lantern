package provider

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestSecurityRuntimeWriterBootstrapAndRestart(t *testing.T) {
	config, data, clock := securityRuntimeFixture(t)
	runtime, cleanup, err := NewSecurityRuntime(config, data)
	if err != nil {
		t.Fatal(err)
	}
	current, known := runtime.native.Store().Current()
	if !known || current.Snapshot().Image().BootstrapDigest == "" {
		t.Fatal("bootstrap was not durable")
	}
	if err = runtime.authorityCheck(t.Context(), current); !errors.Is(err, security.ErrAuthorityUnavailable) {
		t.Fatal("restart barrier skipped", err)
	}
	clock.advance(35 * time.Second)
	if err = runtime.authorityCheck(t.Context(), current); err != nil {
		t.Fatal(err)
	}
	identity := security.Identity{Kind: security.OIDCPrincipal, Issuer: config.Bootstrap.Issuer.URL, Subject: "admin"}
	_, err = runtime.native.Store().Manage(context.Background(), security.ManagementRequest{ExpectedRevision: current.Sequence(), ChangeID: [16]byte{9}, Actor: identity, Authentication: security.Authentication{Provenance: security.BrowserCode, Class: security.EndUser, IssuerConfigRevision: 1}, AuthTime: clock.Now(), Now: clock.Now(), Changes: []security.Change{{Kind: security.PutPrincipal, Identity: &identity, State: security.Suspended}}})
	if err != nil {
		t.Fatal(err)
	}
	cleanup()
	// Reuse a new candidate graph on restart; partially initialized candidates
	// are never passed back into production composition after a failure.
	_, newData, _ := securityRuntimeFixture(t)
	config.StoreMode = "restart"
	restarted, closeRestarted, err := NewSecurityRuntime(config, newData)
	if err != nil {
		t.Fatal(err)
	}
	defer closeRestarted()
	recovered, _ := restarted.native.Store().Current()
	if _, allowed := recovered.Snapshot().AccessFor(identity); allowed {
		t.Fatal("bootstrap resurrected suspended administrator")
	}
	if err = restarted.authorityCheck(t.Context(), recovered); err == nil {
		t.Fatal("restart reused old authority")
	}
}
func TestSecurityRuntimeFailedBootstrapReleasesPath(t *testing.T) {
	config, data, _ := securityRuntimeFixture(t)
	config.Bootstrap.AdminSubjects = []string{"admin", "admin"}
	if _, _, err := NewSecurityRuntime(config, data); err == nil {
		t.Fatal("duplicate bootstrap accepted")
	}
	config.Bootstrap.AdminSubjects = []string{"admin"}
	runtime, cleanup, err := NewSecurityRuntime(config, data)
	if err != nil {
		t.Fatal("failed validation created persistent state", err)
	}
	cleanup()
	_, newData, _ := securityRuntimeFixture(t)
	config.StoreMode = "restart"
	config.Bootstrap.AdminSubjects = []string{"conflicting"}
	if _, _, err := NewSecurityRuntime(config, newData); !errors.Is(err, security.ErrBootstrapLocked) {
		t.Fatal("same-revision conflict ignored", err)
	}
	_, anotherData, _ := securityRuntimeFixture(t)
	config.Bootstrap.AdminSubjects = []string{"admin"}
	_, cleanup, err = NewSecurityRuntime(config, anotherData)
	if err != nil {
		t.Fatal("failed bootstrap leaked lease", err)
	}
	cleanup()
	_ = runtime
}
func TestSecurityRuntimeOffHasNoOIDCPrerequisites(t *testing.T) {
	runtime, cleanup, err := NewSecurityRuntime(SecurityConfig{Mode: "off"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if runtime.mode != "off" || runtime.native != nil || runtime.verifier != nil || runtime.authority != nil {
		t.Fatal("OFF constructed protected runtime")
	}
}
