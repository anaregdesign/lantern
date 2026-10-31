package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/internal/securitytransport"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// These are expected original input bindings, not authority or restart grants.
type queryCurrentNode struct {
	AuthorityFile  string   `json:"authority_file"`
	StartupFile    string   `json:"startup_file"`
	FloorsFile     string   `json:"floors_file"`
	CustodyBinding [32]byte `json:"custody_binding"`
}

func generateQueryFixture(ctx context.Context, dir string, ports []int, mode, binary, exporter string) (_ fixture, err error) {
	if len(ports) != 3 || mode != "off" && mode != "oidc" || !filepath.IsAbs(binary) || !filepath.IsAbs(exporter) {
		return fixture{}, errors.New("current query requires three public nodes, exact mode and absolute Server/exporter binaries")
	}
	serverSource, err := queryBinaryProvenance(binary)
	if err != nil {
		return fixture{}, err
	}
	exporterSource, err := queryBinaryProvenance(exporter)
	if err != nil || len(serverSource.Revision) != 40 || exporterSource.Revision != serverSource.Revision || exporterSource.Modified != serverSource.Modified {
		return fixture{}, errors.New("current query requires same-source Server and original-input exporter")
	}
	// Build transport inputs before installing the original OIDC authority.
	f, err := generateTopologyProfile(dir, ports, nil, "off", "", false, false)
	if err != nil {
		return fixture{}, err
	}
	dir, err = filepath.EvalSymlinks(dir)
	if err != nil {
		return fixture{}, err
	}
	first := fixture{Nodes: f.Nodes[:1], CAFile: f.CAFile}
	stop, err := addFixtureQuery(&first, dir)
	if err != nil {
		return fixture{}, err
	}
	success := false
	defer func() {
		if !success {
			stop()
		}
	}()
	f.Query, f.queryStop = first.Query, stop
	f.Query.Mode, f.Query.SecurityProfile = mode, "current-v2"
	f.Query.Server, f.Query.Exporter = serverSource, &exporterSource
	id, err := randomID()
	if err != nil {
		return fixture{}, err
	}
	f.Query.FixtureID = hex.EncodeToString(id[:])
	for i := range f.Nodes {
		env := f.Nodes[i].Environment
		env["LANTERN_AUTH_MODE"], env["LANTERN_SEARCH_ENABLED"] = mode, "true"
		env["LANTERN_LOG_LEVEL"] = "info"
		env["LANTERN_BACKUP_ENABLED"], env["LANTERN_BACKUP_RESTORE_ON_START"] = "false", "false"
	}
	if mode == "oidc" {
		rolesFile, err := writeJSON(dir, "query-roles.json", append(fixtureRoles(), queryRole()))
		if err != nil {
			return fixture{}, err
		}
		provisionDir := filepath.Join(dir, "authority")
		if err := os.Mkdir(provisionDir, 0700); err != nil {
			return fixture{}, err
		}
		setup, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		command := exec.CommandContext(setup, exporter, "-test.run=^TestCurrentProvisioningExportPublicFixture$", "-test.count=1", "-test.v")
		command.Env, err = fixtureEnvironment(fixtureNode{}, nil)
		if err != nil {
			return fixture{}, err
		}
		command.Env = append(command.Env, "LANTERN_CURRENT_FIXTURE_DIR="+provisionDir, "LANTERN_CURRENT_FIXTURE_ISSUER="+f.Query.Issuer, "LANTERN_CURRENT_FIXTURE_QUERY_ROLES_FILE="+rolesFile)
		raw, runErr := command.CombinedOutput()
		_, saveErr := writeFile(dir, "query-provision-export.log", raw)
		if runErr != nil || saveErr != nil || !strings.Contains(string(raw), "--- PASS: TestCurrentProvisioningExportPublicFixture") || strings.Contains(string(raw), "--- SKIP:") {
			return fixture{}, errors.Join(errors.New("current query original-input exporter did not execute successfully; retain raw log"), runErr, saveErr)
		}
		var exported struct {
			MembershipExpiresAt time.Time          `json:"membership_expires_at"`
			Nodes               []queryCurrentNode `json:"nodes"`
		}
		if err := readQueryJSON(filepath.Join(provisionDir, "query-provision.json"), &exported); err != nil || len(exported.Nodes) != 3 || time.Until(exported.MembershipExpiresAt) < time.Minute {
			return fixture{}, errors.New("complete fresh original current query provisioning required")
		}
		if err := attachFixtureSecurity(&f, dir); err != nil {
			return fixture{}, err
		}
		f.Query.CurrentNodes = exported.Nodes
		if exported.MembershipExpiresAt.Before(f.Query.ExpiresAt) {
			f.Query.ExpiresAt = exported.MembershipExpiresAt
		}
		for i, node := range exported.Nodes {
			if node.AuthorityFile != filepath.Join(provisionDir, fmt.Sprintf("node-%d/authority.json", i+1)) || !filepath.IsAbs(node.FloorsFile) || node.CustodyBinding == [32]byte{} {
				return fixture{}, errors.New("original current node/custody identity mismatch")
			}
			raw, err := os.ReadFile(node.AuthorityFile)
			if err != nil {
				return fixture{}, err
			}
			var authority struct {
				Membership struct {
					ManifestFile string `json:"manifest_file"`
				}
			}
			if json.Unmarshal(raw, &authority) != nil {
				return fixture{}, errors.New("invalid historical authority")
			}
			manifest, err := os.ReadFile(authority.Membership.ManifestFile)
			if err != nil {
				return fixture{}, err
			}
			originalProfile, err := security.ProvisioningAuthorityProfile(raw, manifest, runtime.GOOS)
			if err != nil {
				return fixture{}, err
			}
			profile := securitytransport.EncodeCurrentProfile(originalProfile)
			if i == 0 {
				f.Query.CurrentProfile, f.Query.CurrentBinding = profile, originalProfile.Binding()
			} else if !proto.Equal(profile, f.Query.CurrentProfile) {
				return fixture{}, errors.New("current query cohort profiles differ")
			}

			f.Query.CurrentNodes[i].StartupFile = f.Nodes[i].StartupFile
		}
		token, err := os.ReadFile(filepath.Join(provisionDir, "machine.token"))
		if err != nil {
			return fixture{}, err
		}
		f.TokenFile, err = writeJSON(dir, "query-machine-tokens.json", []string{string(token)})
		if err != nil {
			return fixture{}, err
		}
	}
	success = true
	return f, nil
}

func readQueryJSON(path string, target any) error {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) > 64<<10 {
		return errors.New("bounded query fixture document required")
	}
	return json.Unmarshal(raw, target)
}

func queryCapabilitiesReady(q *queryFixture, node int, mode string, version, member uint32, raw json.RawMessage, expected *pb.CurrentAuthorityProfile) bool {
	if q != nil {
		if q.SecurityProfile != "current-v2" {
			return false
		}
		if q.Mode == "off" {
			return mode == "AUTH_MODE_OFF" && version == 1 && len(raw) == 0 && member == 0
		}
		if q.Mode != "oidc" {
			return false
		}
		expected = q.CurrentProfile
	}
	if mode == "AUTH_MODE_OFF" {
		return q == nil && expected == nil && version == 1 && len(raw) == 0 && member == 0
	}
	if mode != "AUTH_MODE_OIDC" || version != 2 || member != uint32(node+1) || expected == nil {
		return false
	}
	var profile pb.CurrentAuthorityProfile
	if protojson.Unmarshal(raw, &profile) != nil || !proto.Equal(&profile, expected) {
		return false
	}
	decoded, err := securitytransport.DecodeCurrentProfile(&profile)
	return err == nil && (q == nil || decoded.Binding() == q.CurrentBinding)
}

func queryBindingHex(binding [32]byte) string { return hex.EncodeToString(binding[:]) }
