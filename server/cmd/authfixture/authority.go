package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/server/internal/peerauth"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/internal/securitytransport"
)

// These private host-side values are only independent original provisioning
// input. They are never serialized into public fixture metadata or admissions.
type fixtureAuthorityInput struct {
	Issuer                         *security.Issuer `json:"issuer,omitempty"`
	AdminSubjects                  []string         `json:"admin_subjects"`
	HumanSubjectNamespaceQualified bool             `json:"human_subject_namespace_qualified"`
	Roles                          []security.Role  `json:"roles"`
	Machines                       []map[string]any `json:"machines"`
}

func fixtureRepositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			if info, err := os.Stat(filepath.Join(dir, "server/internal/security")); err == nil && info.IsDir() {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("original provisioning needs a same-source exporter or repository checkout")
		}
		dir = parent
	}
}

func runOriginalProvisioner(ctx context.Context, exporter, directory, issuer, bootstrap string, count int, extra ...string) error {
	setup, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	var command *exec.Cmd
	if exporter != "" {
		if !filepath.IsAbs(exporter) {
			return errors.New("absolute same-source authority provisioner required")
		}
		command = exec.CommandContext(setup, exporter, "-test.run=^TestCurrentProvisioningExportPublicFixture$", "-test.count=1", "-test.v")
	} else {
		root, err := fixtureRepositoryRoot()
		if err != nil {
			return err
		}
		command = exec.CommandContext(setup, "go", "test", "./server/internal/security", "-run", "^TestCurrentProvisioningExportPublicFixture$", "-count=1", "-v")
		command.Dir = root
	}
	environment, err := fixtureEnvironment(fixtureNode{}, nil)
	if err != nil {
		return err
	}
	command.Env = append(environment, "LANTERN_CURRENT_FIXTURE_DIR="+directory, "LANTERN_CURRENT_FIXTURE_ISSUER="+issuer, "LANTERN_CURRENT_FIXTURE_VOTERS="+strconv.Itoa(count), "LANTERN_CURRENT_FIXTURE_BOOTSTRAP_FILE="+bootstrap)
	command.Env = append(command.Env, extra...)
	raw, runErr := command.CombinedOutput()
	_, saveErr := writeFile(filepath.Dir(directory), filepath.Base(directory)+"-provision.log", raw)
	if runErr != nil || saveErr != nil || !strings.Contains(string(raw), "--- PASS: TestCurrentProvisioningExportPublicFixture") || strings.Contains(string(raw), "--- SKIP:") {
		return errors.Join(errors.New("original authority provisioning failed; retain private log"), runErr, saveErr)
	}
	return nil
}

func provisionFixtureAuthority(ctx context.Context, f *fixture, directory, exporter string) error {
	if f == nil || f.security == nil || f.security.Issuer == nil || len(f.Nodes) < 1 || len(f.Nodes) > 31 {
		return errors.New("complete original authority inputs required")
	}
	directory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return err
	}
	original, err := writeJSON(directory, "authority-original.json", f.security)
	if err != nil {
		return err
	}
	dir := filepath.Join(directory, "authority")
	if err = os.Mkdir(dir, 0700); err != nil {
		return err
	}
	var extra []string
	if f.compose {
		extra = []string{"LANTERN_AUTH_FIXTURE_COMPOSE=1"}
	}
	if err = runOriginalProvisioner(ctx, exporter, dir, f.security.Issuer.URL, original, len(f.Nodes), extra...); err != nil {
		return err
	}
	var metadata struct {
		Profile security.CurrentProfile `json:"profile"`
	}
	if err := readQueryJSON(filepath.Join(dir, "provision.json"), &metadata); err != nil || metadata.Profile.Binding() == "" {
		return errors.New("complete original authority profile required")
	}
	profile := metadata.Profile
	if err := attachFixtureSecurity(f, directory); err != nil {
		return err
	}
	f.authorityProfile = securitytransport.EncodeCurrentProfile(profile)
	if len(f.dataMembership.Members) > 0 {
		f.dataMembership.Domain.AuthMode = "oidc"
		f.dataMembership.Domain.CurrentProfile = profile.Binding()
		f.dataMembership.Domain.SecurityGeneration = profile.Generation
		raw, err := peerauth.SignManifest(f.dataMembership, f.dataOperator)
		if err != nil {
			return err
		}
		if err = replaceFixtureFile(filepath.Join(directory, "membership.json"), raw, 0600); err != nil {
			return err
		}
	}
	clear(f.dataOperator)
	f.dataOperator = ed25519.PrivateKey(nil)
	return nil
}
