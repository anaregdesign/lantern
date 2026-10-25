package provider

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/server/decisiontrace"
)

func TestInitializeSecurityRequiresExplicitEnvironmentProvisioning(t *testing.T) {
	for _, config := range []SecurityConfig{{Mode: "off"}, {Mode: "oidc", MaterialFile: "/operator/identity.json", BrowserOrigin: "https://admin.example"}} {
		if counts, err := InitializeSecurity(context.Background(), config); err == nil || !strings.Contains(err.Error(), "environment JSON") || counts != (decisiontrace.AcquisitionCounts{}) {
			t.Fatal("initialization accepted an implicit or legacy source", err)
		}
	}
}

func TestInitializeSecurityStorageCreatesOnlyFreshPrivateSpool(t *testing.T) {
	if !securityTargetMatches(runtime.GOOS) {
		t.Skip("generated native security requires a supported Linux or Darwin target")
	}
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(parent, "node-1")
	spool := provisionedSnapshotSpool(state)
	config := SecurityConfig{Mode: "oidc", MaterialFile: filepath.Join(parent, "security.json"), StateDirectory: state, CustodyDirectory: state, TargetPlatform: runtime.GOOS, ProvisionedSnapshotSpool: &spool}
	if err := initializeSecurityStorage(config); err != nil {
		t.Fatal("explicit setup did not create private storage", err)
	}
	for _, path := range []string{state, spool.Directory} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
			t.Fatal("explicit setup storage is missing or unsafe", err)
		}
	}
	if err := spool.Validate(); err != nil {
		t.Fatal("initialized Snapshot storage does not meet runtime contract", err)
	}
	if err := initializeSecurityStorage(config); err == nil {
		t.Fatal("existing native setup was silently reused")
	}
	if err := os.Remove(spool.Directory); err != nil {
		t.Fatal(err)
	}
	if _, err := loadGraphSnapshotSpoolForSecurity(config); err != nil {
		t.Fatal("static read unexpectedly changed after storage removal", err)
	}
	if err := spool.Validate(); err == nil {
		t.Fatal("runtime accepted missing spool after clean setup")
	}
	if _, err := os.Stat(spool.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("configuration/runtime validation repaired missing storage", err)
	}
}

func TestInitializeSecurityStorageKeepsCustodySeparateAndSpoolInactiveForReceipt(t *testing.T) {
	if !securityTargetMatches(runtime.GOOS) {
		t.Skip("native runtime requires supported target")
	}
	clearSecurityEnvironment(t)
	t.Setenv("LANTERN_RECEIPT_WAL_MODE", "restart")
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	state, custody := filepath.Join(root, "journal-node"), filepath.Join(root, "custody-node")
	spool := provisionedSnapshotSpool(state)
	config := SecurityConfig{Mode: "oidc", MaterialFile: filepath.Join(root, "identity.json"), StateDirectory: state, CustodyDirectory: custody, TargetPlatform: runtime.GOOS, ProvisionedSnapshotSpool: &spool}
	if err := initializeSecurityStorage(config); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{state, custody} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
			t.Fatal("separate private native storage not created", err)
		}
	}
	if _, err := os.Stat(spool.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("receipt representation created inactive graph spool", err)
	}
	if err := initializeSecurityStorage(config); err == nil {
		t.Fatal("retained final directories accepted as fresh")
	}
}
