package provider

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestGraphSnapshotSpoolConfigUsesOnlyImmutableProfile(t *testing.T) {
	clearSecurityEnvironment(t)
	state := filepath.Join(t.TempDir(), "node-1")
	spool := provisionedSnapshotSpool(state)
	config := SecurityConfig{Mode: "oidc", MaterialFile: filepath.Join(t.TempDir(), "security.json"), StateDirectory: state, ProvisionedSnapshotSpool: &spool}
	loaded, err := loadGraphSnapshotSpoolForSecurity(config)
	if err != nil || loaded != spool || loaded.MaxDiskBytes != 128<<20 || loaded.MaxFrameBytes != 32<<20 {
		t.Fatal("generated setup did not preserve its finite Snapshot bounds", err)
	}
	if _, err := os.Stat(spool.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("static configuration loading created the spool", err)
	}
	if err := loaded.Validate(); err == nil {
		t.Fatal("ordinary startup accepted absent private spool storage")
	}
	for _, key := range []string{"LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR", "LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_DISK_BYTES", "LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_FRAME_BYTES", "LANTERN_GRAPH_SNAPSHOT_SPOOL_TYPO"} {
		for _, value := range []string{"", "0", "unrelated"} {
			t.Run(key+"/"+value, func(t *testing.T) {
				t.Setenv(key, value)
				if _, err := loadGraphSnapshotSpoolForSecurity(config); err == nil {
					t.Fatal("separate Snapshot input was ignored or overrode provisioned bounds")
				}
			})
		}
	}
	config.ProvisionedSnapshotSpool = nil
	if _, err := loadGraphSnapshotSpoolForSecurity(config); err == nil {
		t.Fatal("missing provisioned spool silently acquired defaults")
	}
}

func TestGraphSnapshotSpoolSelectionPreservesSignedProfileAcrossRepresentations(t *testing.T) {
	clearSecurityEnvironment(t)
	state := filepath.Join(t.TempDir(), "node-1")
	spool := provisionedSnapshotSpool(state)
	config := SecurityConfig{Mode: "oidc", StateDirectory: state, ProvisionedSnapshotSpool: &spool}
	for _, mode := range []ReceiptWALMode{ReceiptWALModeGraphOnly, ReceiptWALModeFresh, ReceiptWALModeRestart} {
		active, err := selectGraphSnapshotSpoolForSecurity(config, mode)
		if err != nil || mode == ReceiptWALModeGraphOnly && active != spool || mode != ReceiptWALModeGraphOnly && active != (GraphSnapshotSpoolConfig{}) {
			t.Fatal("incorrect active spool for serving representation", mode, err)
		}
		if *config.ProvisionedSnapshotSpool != spool {
			t.Fatal("runtime representation rewrote signed spool material")
		}
		if _, err := os.Stat(spool.Directory); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("pure selection accessed or created missing spool", err)
		}
	}
	bad := spool
	bad.MaxDiskBytes = 0
	config.ProvisionedSnapshotSpool = &bad
	for _, mode := range []ReceiptWALMode{ReceiptWALModeGraphOnly, ReceiptWALModeFresh, ReceiptWALModeRestart} {
		if _, err := selectGraphSnapshotSpoolForSecurity(config, mode); err == nil {
			t.Fatal("inactive representation bypassed invalid immutable spool profile", mode)
		}
	}
}

func TestProvisionedSnapshotSpoolRejectsChangedPathsAndInvalidBounds(t *testing.T) {
	state := filepath.Join(t.TempDir(), "node-1")
	for _, change := range []func(*GraphSnapshotSpoolConfig){
		func(c *GraphSnapshotSpoolConfig) { *c = GraphSnapshotSpoolConfig{} },
		func(c *GraphSnapshotSpoolConfig) { c.Directory = filepath.Join(t.TempDir(), "other") },
		func(c *GraphSnapshotSpoolConfig) { c.Directory += "/../snapshot-spool" },
		func(c *GraphSnapshotSpoolConfig) { c.MaxDiskBytes = 0 },
		func(c *GraphSnapshotSpoolConfig) { c.MaxFrameBytes = 0 },
		func(c *GraphSnapshotSpoolConfig) { c.MaxFrameBytes = int(c.MaxDiskBytes) + 1 },
	} {
		spool := provisionedSnapshotSpool(state)
		change(&spool)
		if err := validateProvisionedSnapshotSpool(spool, state); err == nil {
			t.Fatal("invalid persisted spool path/capacity accepted")
		}
	}
}

func TestGraphSnapshotSpoolConfigRequiresExplicitHAStorage(t *testing.T) {
	t.Setenv("LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR", "")
	t.Setenv("LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_DISK_BYTES", "0")
	t.Setenv("LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_FRAME_BYTES", "0")
	config, err := loadGraphSnapshotSpoolConfig()
	if err != nil || config != (GraphSnapshotSpoolConfig{}) {
		t.Fatalf("unset graph spool did not remain disabled: %+v, %v", config, err)
	}
	for _, mode := range []ReceiptWALMode{ReceiptWALModeGraphOnly, ReceiptWALModeFresh, ReceiptWALModeRestart} {
		if err := validateGraphSnapshotSpoolConfig(config, mode, PeerPlaneConfig{}); err != nil {
			t.Fatalf("unused zero spool broke single-node or receipt runtime %q: %v", mode, err)
		}
	}
	peers := PeerPlaneConfig{ListenAddress: "127.0.0.1:6391"}
	if err := validateGraphSnapshotSpoolConfig(config, ReceiptWALModeGraphOnly, peers); err == nil {
		t.Fatal("graph-only HA inferred temporary storage")
	}
	directory, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(directory, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR", directory)
	t.Setenv("LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_DISK_BYTES", strconv.Itoa(128<<20))
	t.Setenv("LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_FRAME_BYTES", strconv.Itoa(32<<20))
	config, err = loadGraphSnapshotSpoolConfig()
	if err != nil || config.Directory != directory || config.MaxDiskBytes != 128<<20 || config.MaxFrameBytes != 32<<20 {
		t.Fatalf("explicit capacity was not parsed exactly: %+v, %v", config, err)
	}
	if err := validateGraphSnapshotSpoolConfig(config, ReceiptWALModeGraphOnly, peers); err != nil {
		t.Fatal(err)
	}
	if NewGraphSnapshotSpoolConfig(&Config{GraphSnapshotSpool: config}) != config {
		t.Fatal("focused provider changed the spool capacity")
	}
	if err := validateGraphSnapshotSpoolConfig(config, ReceiptWALModeFresh, peers); err == nil {
		t.Fatal("receipt runtime silently ignored graph-only spool settings")
	}
}

func TestGraphSnapshotSpoolConfigRejectsPartialMalformedOrUnsafeStorage(t *testing.T) {
	for _, test := range []struct {
		name, key, value string
	}{
		{"missing directory", "LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR", ""},
		{"relative directory", "LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR", "relative-spool"},
		{"missing quota", "LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_DISK_BYTES", "0"},
		{"negative quota", "LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_DISK_BYTES", "-1"},
		{"malformed quota", "LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_DISK_BYTES", "128MiB"},
		{"quota overflow", "LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_DISK_BYTES", "18446744073709551616"},
		{"missing frame limit", "LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_FRAME_BYTES", "0"},
		{"negative frame limit", "LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_FRAME_BYTES", "-1"},
		{"malformed frame limit", "LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_FRAME_BYTES", "32MiB"},
		{"frame limit overflow", "LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_FRAME_BYTES", "18446744073709551615"},
		{"unknown capacity key", "LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_DSK_BYTES", "134217728"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(directory, 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR", directory)
			t.Setenv("LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_DISK_BYTES", "134217728")
			t.Setenv("LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_FRAME_BYTES", "33554432")
			t.Setenv(test.key, test.value)
			config, err := loadGraphSnapshotSpoolConfig()
			if err == nil {
				err = validateGraphSnapshotSpoolConfig(config, ReceiptWALModeGraphOnly, PeerPlaneConfig{})
			}
			if err == nil {
				t.Fatal("partial, malformed or unsafe spool configuration was accepted")
			}
		})
	}
	for _, unsafe := range []string{"missing", "public directory", "regular file"} {
		t.Run(unsafe, func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			directory := filepath.Join(root, "spool")
			switch unsafe {
			case "public directory":
				if err := os.Mkdir(directory, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(directory, 0755); err != nil {
					t.Fatal(err)
				}
			case "regular file":
				if err := os.WriteFile(directory, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			config := GraphSnapshotSpoolConfig{Directory: directory, MaxDiskBytes: 128 << 20, MaxFrameBytes: 32 << 20}
			if err := validateGraphSnapshotSpoolConfig(config, ReceiptWALModeGraphOnly, PeerPlaneConfig{}); err == nil {
				t.Fatal("spool directory ownership contract was not checked")
			}
		})
	}
}
