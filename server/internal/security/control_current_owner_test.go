package security

import (
	"errors"
	"os"
	"testing"

	"github.com/anaregdesign/lantern/core/privatefile"
)

func TestCurrentAuthorityOwnerRejectsInjectedTimeBeforeIO(t *testing.T) {
	clock, _ := fakeAuthorityTimeOwner(t)
	if owner, err := openCurrentAuthorityOwner(t.Context(), s3aConfig{timeOwner: clock}, "/unused-origin-key", "https://admin.example", true, s3aFloors{}); owner != nil || err == nil {
		t.Fatal("current native constructor accepted injected time")
	}
	if owner, err := openCurrentAuthorityOwner(t.Context(), s3aConfig{}, "/unused-origin-key", "https://admin.example", true, s3aFloors{}); owner != nil || err == nil {
		t.Fatal("missing independent origin bootstrap")
	}
}

func TestCurrentCustodyRunningBarrierNativeGate(t *testing.T) {
	if os.Getenv("LANTERN_CURRENT_CUSTODY_GATE") != "1" {
		t.Skip("explicit production native custody container gate")
	}
	// Historical custody-barrier evidence is a private fixture input. Product
	// startup still requires the full supplied declarative document separately.
	raw, err := os.ReadFile(os.Getenv("LANTERN_CURRENT_FIXTURE_AUTHORITY_FILE"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := LoadCurrentProvisioningJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	_, custody, floors, err := openCurrentCustody(p, "fresh")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = custody.close() }()
	failure := errors.New("injected RUNNING directory sync uncertainty")
	custody.io.syncDirectory = func(path string) error {
		return errors.Join(privatefile.SyncDirectory(path), failure)
	}
	owner, err := openCurrentAuthorityOwnerWithCustody(t.Context(), p.config, "", "https://admin.example", true, floors, custody)
	if owner != nil || !errors.Is(err, failure) {
		if owner != nil {
			_ = owner.network.Close()
		}
		t.Fatal("native startup crossed uncertain RUNNING barrier", err)
	}
	for _, path := range []string{p.config.Membership.Path, p.config.Participant.PPath, p.config.Participant.BPath} {
		for _, suffix := range []string{"", ".tip", ".lease"} {
			if _, err := os.Lstat(path + suffix); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("native family opened before RUNNING durability succeeded", path+suffix, err)
			}
		}
	}
	if currentCustodyReadState(t, p).Phase != "RUNNING" {
		t.Fatal("barrier fault lost its forensic lifecycle record")
	}
	t.Log("native anchor acquired; RUNNING uncertainty refused before any M/P/B file, tip or lease operation")
}
