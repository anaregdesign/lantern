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
				path := filepath.Join(dir, fmt.Sprintf("node-%d/authority.json", id))
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal("read exported inline authority", err)
				}
				var document currentJSONProvisioningDocument
				if err := decodeCurrentJSONProvisioning(raw, &document); err != nil {
					t.Fatal("decode exported inline authority", err)
				}
				manifest, err := os.ReadFile(document.Membership.ManifestFile)
				if err != nil {
					t.Fatal("read exported signed membership", err)
				}
				// Export is pure authoring: final state directories intentionally
				// do not exist before explicit initialization.
				c, _, err := currentJSONConfigForTime(document, manifest, authorityTimeProfileDescription, false)
				if err != nil || len(c.Participant.Trust.members) != count || !c.Limits.valid(c.Participant) {
					t.Fatal("export changed original voters or lost capacity", id, err)
				}
				if c.Participant.OwnedOrigin == 0 && id <= min(count, 2) {
					t.Fatal("original origin was removed", id)
				}
				for _, file := range []string{c.Participant.PPath, c.Participant.BPath, document.FloorsFile, c.Membership.Path} {
					if _, err := os.Lstat(file); !os.IsNotExist(err) {
						t.Fatal("original-input export manufactured runtime state", file, err)
					}
				}
			}
		})
	}
}
