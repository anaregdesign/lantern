package provider

import (
	"github.com/anaregdesign/lantern/server/internal/security"
	"path/filepath"
	"testing"
)

func TestCurrentRuntimeNeverFallsBackFromMissingProvisioning(t *testing.T) {
	_, data, _ := securityRuntimeFixture(t)
	c := SecurityConfig{Mode: "oidc", Profile: "current-v2", StoreMode: "fresh", BrowserOrigin: "https://admin.example", CurrentConfigFile: filepath.Join(t.TempDir(), "absent.json")}
	if runtime, cleanup, err := NewSecurityRuntime(c, data); err == nil || runtime != nil || cleanup != nil {
		t.Fatal("missing independent inputs became public runtime")
	}
	r := &SecurityRuntime{current: &security.CurrentAuthority{}}
	if r.currentAttemptPrefix() != "" || r.currentAttemptAffinity([32]byte{1}) != "" {
		t.Fatal("missing process incarnation invented callback affinity")
	}
	r.attemptProcess = [32]byte{1}
	if r.currentAttemptAffinity([32]byte{}) != "" {
		t.Fatal("empty attempt accepted")
	}
	if _, err := r.readCurrentManagementAuthorization(t.Context(), nil, [32]byte{1}, "another-process"); err == nil {
		t.Fatal("foreign process affinity reached purpose owner")
	}
}

func TestCurrentRuntimeFloorExportRequiresActualOwner(t *testing.T) {
	for _, runtime := range []*SecurityRuntime{nil, {}, {current: &security.CurrentAuthority{}}} {
		if raw, err := runtime.ExportCurrentAuthorityFloors(); err == nil || len(raw) != 0 {
			t.Fatal("unopened owner manufactured resume floors")
		}
	}
}
