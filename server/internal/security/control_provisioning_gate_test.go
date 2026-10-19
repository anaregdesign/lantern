package security

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestCurrentProvisioningFixedVoterFixtureExport(t *testing.T) {
	for _, count := range []int{1, 2, 4} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("LANTERN_CURRENT_FIXTURE_DIR", dir)
			t.Setenv("LANTERN_CURRENT_FIXTURE_ISSUER", "https://127.0.0.1:12345")
			t.Setenv("LANTERN_CURRENT_FIXTURE_VOTERS", fmt.Sprint(count))
			for _, name := range []string{"LANTERN_CURRENT_FIXTURE_HOSTS", "LANTERN_CURRENT_FIXTURE_QUERY_ROLES_FILE", "LANTERN_CURRENT_FIXTURE_BOOTSTRAP_FILE", "LANTERN_NATIVE_FIXTURE_SERVE", "LANTERN_CURRENT_MEMBERSHIP_GATE", "LANTERN_CURRENT_MEMBERSHIP_CONTAINER", "LANTERN_CURRENT_DATA_HA"} {
				t.Setenv(name, "")
			}
			TestCurrentProvisioningExportPublicFixture(t)
			for id := 1; id <= count; id++ {
				path := filepath.Join(dir, fmt.Sprintf("node-%d/node.json", id))
				p, err := LoadCurrentProvisioning(path)
				if err != nil || len(p.config.Participant.Trust.members) != count || !p.config.Limits.valid(p.config.Participant) {
					t.Fatal("export changed original voters or lost capacity", id, err)
				}
				if p.config.Participant.OwnedOrigin == 0 && id <= min(count, 2) {
					t.Fatal("original origin was removed", id)
				}
				for _, file := range []string{p.config.Participant.PPath, p.config.Participant.BPath, p.floors, p.config.Membership.Path} {
					if _, err := os.Lstat(file); !os.IsNotExist(err) {
						t.Fatal("original-input export manufactured runtime state", file, err)
					}
				}
			}
		})
	}
}
