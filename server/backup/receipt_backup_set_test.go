package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/service"
)

func completedReceiptBackupSet(
	t *testing.T,
) (*Backupper, wholeStateArchive, service.ReceiptWholeStateBackupCapture, *receiptBackupSetSource, loadedReceiptBackupSet) {
	t.Helper()
	archive := wholeStateArchiveFixture(t)
	capture := producerBackupCapture(archive)
	source := &receiptBackupSetSource{capture: capture}
	b := newReceiptBackupSetTestBackupper(t, t.TempDir(), "set-owner", 0, source, archive.Policy)
	b.now = func() time.Time { return time.Unix(0, 1234).UTC() }
	stats, err := b.BackupNow(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Members != receiptBackupSetExpectedMemberCount || stats.Bytes <= 0 {
		t.Fatalf("receipt set stats = %+v", stats)
	}
	sets, err := b.collectReceiptBackupSets()
	if err != nil || len(sets) != 1 {
		t.Fatalf("complete sets = %+v, %v", sets, err)
	}
	loaded, err := b.loadReceiptBackupSet(sets[0].manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	return b, archive, capture, source, loaded
}

func newReceiptBackupSetDiscoveryBackupper(
	t *testing.T,
	dir, instance string,
) *Backupper {
	t.Helper()
	archive := wholeStateArchiveFixture(t)
	return newReceiptBackupSetTestBackupper(
		t,
		dir,
		instance,
		0,
		&receiptBackupSetSource{capture: producerBackupCapture(archive)},
		archive.Policy,
	)
}

func writeReceiptBackupSetAt(
	t *testing.T,
	b *Backupper,
	setID int64,
) loadedReceiptBackupSet {
	t.Helper()
	b.now = func() time.Time { return time.Unix(0, setID).UTC() }
	if _, err := b.BackupNow(t.Context()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(
		b.cfg.Dir,
		receiptBackupSetBase(b.cfg.InstanceID, uint64(setID))+receiptBackupSetManifestSuffix,
	)
	loaded, err := b.loadReceiptBackupSet(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.id != uint64(setID) {
		t.Fatalf("fixture set ID = %d, want %d", loaded.id, setID)
	}
	return loaded
}

func cloneReceiptBackupSetAt(
	t *testing.T,
	b *Backupper,
	source loadedReceiptBackupSet,
	setID uint64,
) loadedReceiptBackupSet {
	t.Helper()
	manifestRaw, err := os.ReadFile(source.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := decodeReceiptBackupSetManifest(manifestRaw)
	if err != nil {
		t.Fatal(err)
	}
	manifest.SetID = receiptBackupSetIDString(setID)
	manifest.Members = append([]receiptBackupSetMember(nil), manifest.Members...)
	base := receiptBackupSetBase(b.cfg.InstanceID, setID)
	for i, suffix := range []string{
		receiptBackupSetArchiveSuffix,
		receiptBackupSetWALCutSuffix,
	} {
		memberRaw, err := os.ReadFile(source.memberPaths[i])
		if err != nil {
			t.Fatal(err)
		}
		manifest.Members[i].Name = base + suffix
		if err := os.WriteFile(
			filepath.Join(b.cfg.Dir, manifest.Members[i].Name),
			memberRaw,
			receiptBackupSetFilePermissions,
		); err != nil {
			t.Fatal(err)
		}
	}
	manifestRaw, err = encodeReceiptBackupSetManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(b.cfg.Dir, base+receiptBackupSetManifestSuffix)
	if err := os.WriteFile(manifestPath, manifestRaw, receiptBackupSetFilePermissions); err != nil {
		t.Fatal(err)
	}
	loaded, err := b.loadReceiptBackupSet(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

type receiptBackupPathState struct {
	mode os.FileMode
	raw  []byte
}

func receiptBackupDirectoryState(
	t *testing.T,
	dir string,
) map[string]receiptBackupPathState {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	state := make(map[string]receiptBackupPathState, len(entries))
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		var raw []byte
		switch {
		case info.Mode().IsRegular():
			raw, err = os.ReadFile(path)
		case info.Mode()&os.ModeSymlink != 0:
			var target string
			target, err = os.Readlink(path)
			raw = []byte(target)
		}
		if err != nil {
			t.Fatal(err)
		}
		state[entry.Name()] = receiptBackupPathState{mode: info.Mode(), raw: raw}
	}
	return state
}

func TestReceiptBackupSetPersistsOneImmutableValidatedCapture(t *testing.T) {
	b, archive, capture, source, loaded := completedReceiptBackupSet(t)
	if source.calls.Load() != 1 {
		t.Fatalf("CaptureForBackup calls = %d, want 1", source.calls.Load())
	}
	if loaded.id != 1234 || loaded.createdAt != time.Unix(0, 1234).UTC() {
		t.Fatalf("loaded set identity = %d at %v", loaded.id, loaded.createdAt)
	}
	if loaded.nodeID != capture.NodeID || loaded.generation != capture.Generation {
		t.Fatalf("loaded endpoint identity = %x/%x, want %x/%x",
			loaded.nodeID, loaded.generation, capture.NodeID, capture.Generation)
	}
	if loaded.walCut.cutSeq != capture.WALTip.Seq ||
		loaded.walCut.cutOffset != capture.WALTip.Offset ||
		loaded.walCut.cutSHA256 != capture.WALTip.SHA256 ||
		loaded.walCut.cutChainSHA256 != capture.WALTip.ChainSHA256 ||
		loaded.walCut.tipSeq != capture.WALTip.Seq ||
		loaded.walCut.tipOffset != capture.WALTip.Offset ||
		loaded.walCut.tipSHA256 != capture.WALTip.SHA256 ||
		loaded.walCut.tipChainSHA256 != capture.WALTip.ChainSHA256 {
		t.Fatalf("loaded WAL cut/tip = %+v, want captured witness %+v", loaded.walCut, capture.WALTip)
	}
	var expected bytes.Buffer
	if err := encodeWholeStateArchive(&expected, archive); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded.archiveRaw, expected.Bytes()) {
		t.Fatal("loaded archive bytes differ from the immutable producer archive")
	}
	if !reflect.DeepEqual(loaded.archive.Receipts, archive.Receipts) ||
		!reflect.DeepEqual(loaded.archive.Origins, archive.Origins) {
		t.Fatal("loaded archive lost active receipt state")
	}
	entries, err := os.ReadDir(b.cfg.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != receiptBackupSetExpectedMemberCount+1 {
		t.Fatalf("committed set files = %d, want 3", len(entries))
	}
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), receiptBackupSetTempSuffix) ||
			strings.HasSuffix(entry.Name(), fileSuffix) {
			t.Fatalf("durable receipt set emitted legacy or temporary file %q", entry.Name())
		}
	}

	evidence, err := LoadReceiptBackupSet(b.cfg.Dir, b.cfg.InstanceID, loaded.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.SetID != loaded.id || evidence.BackupTimestamp != loaded.createdAt ||
		evidence.NodeID != capture.NodeID || evidence.Generation != capture.Generation ||
		evidence.WALCut != capture.WALTip || evidence.Stats != loaded.stats ||
		!bytes.Equal(evidence.Archive, loaded.archiveRaw) {
		t.Fatalf("public receipt backup-set evidence = %+v", evidence)
	}

	owned := append([]byte(nil), loaded.archiveRaw...)
	if err := os.WriteFile(loaded.memberPaths[0], bytes.Repeat([]byte{0x5a}, len(owned)), receiptBackupSetFilePermissions); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(loaded.archiveRaw, owned) {
		t.Fatal("loaded archive bytes alias the member file")
	}
}

func TestReceiptBackupSetManifestValidationFailsClosed(t *testing.T) {
	b, _, capture, _, loaded := completedReceiptBackupSet(t)
	raw, err := os.ReadFile(loaded.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := decodeReceiptBackupSetManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := encodeReceiptBackupSetManifest(manifest)
	if err != nil || !bytes.Equal(reencoded, raw) {
		t.Fatalf("canonical manifest round trip = %q, %v", reencoded, err)
	}

	t.Run("noncanonical and trailing bytes", func(t *testing.T) {
		for _, malformed := range [][]byte{
			append(append([]byte(nil), raw...), '\n'),
			append([]byte(" "), raw...),
			append(raw[:len(raw)-1], []byte(`,"unknown":true}`)...),
		} {
			if _, err := decodeReceiptBackupSetManifest(malformed); err == nil {
				t.Fatalf("malformed manifest accepted: %q", malformed)
			}
		}
	})

	t.Run("malformed fields and members", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*receiptBackupSetManifest)
		}{
			{"format", func(m *receiptBackupSetManifest) { m.Format = "unknown" }},
			{"version", func(m *receiptBackupSetManifest) { m.Version++ }},
			{"empty instance", func(m *receiptBackupSetManifest) { m.Instance = "" }},
			{"noncanonical ID", func(m *receiptBackupSetManifest) { m.SetID = "1" }},
			{"zero ID", func(m *receiptBackupSetManifest) { m.SetID = receiptBackupSetIDString(0) }},
			{"timestamp", func(m *receiptBackupSetManifest) { m.BackupTimestamp = "not-a-time" }},
			{"node ID", func(m *receiptBackupSetManifest) { m.NodeID = "00" }},
			{"generation", func(m *receiptBackupSetManifest) { m.Generation = "00" }},
			{"missing member", func(m *receiptBackupSetManifest) { m.Members = m.Members[:1] }},
			{"duplicate member", func(m *receiptBackupSetManifest) { m.Members[1] = m.Members[0] }},
			{"unknown member", func(m *receiptBackupSetManifest) { m.Members[1].Role = "unknown" }},
			{"unsafe member path", func(m *receiptBackupSetManifest) { m.Members[0].Name = "../archive" }},
			{"wrong member name", func(m *receiptBackupSetManifest) { m.Members[0].Name = "other.active.lar" }},
			{"zero member size", func(m *receiptBackupSetManifest) { m.Members[0].Size = 0 }},
			{"malformed digest", func(m *receiptBackupSetManifest) { m.Members[0].SHA256 = "00" }},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				candidate := manifest
				candidate.Members = append([]receiptBackupSetMember(nil), manifest.Members...)
				tc.mutate(&candidate)
				if _, err := encodeReceiptBackupSetManifest(candidate); err == nil {
					t.Fatalf("invalid manifest accepted: %+v", candidate)
				}
			})
		}
	})

	t.Run("captured identity mismatch", func(t *testing.T) {
		wrongNode := manifest
		wrongNode.NodeID = hex.EncodeToString(bytes.Repeat([]byte{0x42}, 16))
		if err := validateReceiptBackupSetManifestIdentity(
			wrongNode,
			capture.NodeID,
			capture.Generation,
		); err == nil {
			t.Fatal("manifest with a foreign NodeID matched the captured runtime")
		}
		wrongGeneration := manifest
		wrongGeneration.Generation = hex.EncodeToString(bytes.Repeat([]byte{0x43}, 16))
		if err := validateReceiptBackupSetManifestIdentity(
			wrongGeneration,
			capture.NodeID,
			capture.Generation,
		); err == nil {
			t.Fatal("manifest with a foreign generation matched the captured runtime")
		}
	})

	t.Run("member size digest and file type", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*testing.T, *Backupper, loadedReceiptBackupSet, receiptBackupSetManifest)
		}{
			{
				name: "size",
				mutate: func(t *testing.T, b *Backupper, loaded loadedReceiptBackupSet, manifest receiptBackupSetManifest) {
					manifest.Members[0].Size++
					raw, err := encodeReceiptBackupSetManifest(manifest)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(loaded.manifestPath, raw, receiptBackupSetFilePermissions); err != nil {
						t.Fatal(err)
					}
				},
			},
			{
				name: "digest",
				mutate: func(t *testing.T, b *Backupper, loaded loadedReceiptBackupSet, manifest receiptBackupSetManifest) {
					manifest.Members[0].SHA256 = hex.EncodeToString(bytes.Repeat([]byte{0x44}, sha256.Size))
					raw, err := encodeReceiptBackupSetManifest(manifest)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(loaded.manifestPath, raw, receiptBackupSetFilePermissions); err != nil {
						t.Fatal(err)
					}
				},
			},
			{
				name: "NodeID differs from archive cutoff",
				mutate: func(t *testing.T, b *Backupper, loaded loadedReceiptBackupSet, manifest receiptBackupSetManifest) {
					manifest.NodeID = hex.EncodeToString(bytes.Repeat([]byte{0x45}, 16))
					raw, err := encodeReceiptBackupSetManifest(manifest)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(loaded.manifestPath, raw, receiptBackupSetFilePermissions); err != nil {
						t.Fatal(err)
					}
				},
			},
			{
				name: "member bytes",
				mutate: func(t *testing.T, b *Backupper, loaded loadedReceiptBackupSet, _ receiptBackupSetManifest) {
					raw, err := os.ReadFile(loaded.memberPaths[0])
					if err != nil {
						t.Fatal(err)
					}
					raw[len(raw)/2] ^= 0xff
					if err := os.WriteFile(loaded.memberPaths[0], raw, receiptBackupSetFilePermissions); err != nil {
						t.Fatal(err)
					}
				},
			},
			{
				name: "WAL cut binds different archive",
				mutate: func(t *testing.T, b *Backupper, loaded loadedReceiptBackupSet, manifest receiptBackupSetManifest) {
					raw, err := os.ReadFile(loaded.memberPaths[1])
					if err != nil {
						t.Fatal(err)
					}
					cut, err := decodeReceiptArchiveWALCut(raw)
					if err != nil {
						t.Fatal(err)
					}
					cut.archiveSHA256[0] ^= 0xff
					raw = encodeReceiptArchiveWALCut(cut)
					if err := os.WriteFile(loaded.memberPaths[1], raw, receiptBackupSetFilePermissions); err != nil {
						t.Fatal(err)
					}
					digest := sha256.Sum256(raw)
					manifest.Members[1].Size = uint64(len(raw))
					manifest.Members[1].SHA256 = hex.EncodeToString(digest[:])
					manifestRaw, err := encodeReceiptBackupSetManifest(manifest)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(loaded.manifestPath, manifestRaw, receiptBackupSetFilePermissions); err != nil {
						t.Fatal(err)
					}
				},
			},
			{
				name: "member symlink",
				mutate: func(t *testing.T, b *Backupper, loaded loadedReceiptBackupSet, _ receiptBackupSetManifest) {
					target := filepath.Join(b.cfg.Dir, "target")
					if err := os.WriteFile(target, []byte("target"), receiptBackupSetFilePermissions); err != nil {
						t.Fatal(err)
					}
					if err := os.Remove(loaded.memberPaths[0]); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(target, loaded.memberPaths[0]); err != nil {
						t.Fatal(err)
					}
				},
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				b, _, _, _, loaded := completedReceiptBackupSet(t)
				raw, err := os.ReadFile(loaded.manifestPath)
				if err != nil {
					t.Fatal(err)
				}
				manifest, err := decodeReceiptBackupSetManifest(raw)
				if err != nil {
					t.Fatal(err)
				}
				tc.mutate(t, b, loaded, manifest)
				if _, err := b.loadReceiptBackupSet(loaded.manifestPath); err == nil {
					t.Fatal("invalid committed set loaded")
				}
			})
		}
	})

	if _, err := b.loadReceiptBackupSet(filepath.Join(b.cfg.Dir, "..", filepath.Base(loaded.manifestPath))); err == nil {
		t.Fatal("manifest path outside the backup directory was accepted")
	}
}

func TestLoadLatestReceiptBackupSet(t *testing.T) {
	t.Run("no directory or recognized manifest", func(t *testing.T) {
		for _, dir := range []string{
			filepath.Join(t.TempDir(), "missing"),
			t.TempDir(),
		} {
			evidence, err := LoadLatestReceiptBackupSet(dir, "discovery-owner")
			if !errors.Is(err, ErrReceiptBackupSetNotFound) {
				t.Fatalf("LoadLatestReceiptBackupSet error = %v, want %v", err, ErrReceiptBackupSetNotFound)
			}
			if !reflect.DeepEqual(evidence, ReceiptBackupSetEvidence{}) {
				t.Fatalf("missing-set evidence = %+v, want zero value", evidence)
			}
		}
	})

	t.Run("one valid set is loaded without mutation", func(t *testing.T) {
		dir := t.TempDir()
		b := newReceiptBackupSetDiscoveryBackupper(t, dir, "one-owner")
		loaded := writeReceiptBackupSetAt(t, b, 100)
		before := receiptBackupDirectoryState(t, dir)

		evidence, err := LoadLatestReceiptBackupSet(dir, b.cfg.InstanceID)
		if err != nil {
			t.Fatal(err)
		}
		if evidence.SetID != loaded.id ||
			evidence.BackupTimestamp != loaded.createdAt ||
			evidence.NodeID != loaded.nodeID ||
			evidence.Generation != loaded.generation ||
			!bytes.Equal(evidence.Archive, loaded.archiveRaw) {
			t.Fatalf("latest evidence = %+v, want set %d", evidence, loaded.id)
		}
		after := receiptBackupDirectoryState(t, dir)
		if !reflect.DeepEqual(after, before) {
			t.Fatalf("LoadLatestReceiptBackupSet mutated files:\nbefore=%+v\nafter=%+v", before, after)
		}
	})

	t.Run("multiple valid sets select newest", func(t *testing.T) {
		dir := t.TempDir()
		b := newReceiptBackupSetDiscoveryBackupper(t, dir, "newest-owner")
		writeReceiptBackupSetAt(t, b, 100)
		newest := writeReceiptBackupSetAt(t, b, 200)

		evidence, err := LoadLatestReceiptBackupSet(dir, b.cfg.InstanceID)
		if err != nil {
			t.Fatal(err)
		}
		if evidence.SetID != newest.id || !bytes.Equal(evidence.Archive, newest.archiveRaw) {
			t.Fatalf("latest evidence selected set %d, want %d", evidence.SetID, newest.id)
		}
	})

	t.Run("foreign unrecognized and orphan paths are ignored", func(t *testing.T) {
		dir := t.TempDir()
		instance := "isolated-owner"
		b := newReceiptBackupSetDiscoveryBackupper(t, dir, instance)
		valid := writeReceiptBackupSetAt(t, b, 10)
		maxBase := receiptBackupSetBase(instance, ^uint64(0))
		overflowName := receiptBackupSetPrefix + receiptBackupSetScope(instance) +
			"-18446744073709551616" + receiptBackupSetManifestSuffix
		foreignBase := receiptBackupSetBase("foreign-owner", ^uint64(0))
		unrecognizedPrefix := strings.TrimSuffix(receiptBackupSetPrefix, "-") + "-unknown-"
		files := []string{
			"unrelated",
			foreignBase + receiptBackupSetManifestSuffix,
			unrecognizedPrefix + receiptBackupSetScope(instance) + "-" +
				receiptBackupSetIDString(^uint64(0)) + receiptBackupSetManifestSuffix,
			maxBase + receiptBackupSetManifestSuffix + receiptBackupSetTempSuffix,
			maxBase + receiptBackupSetManifestSuffix + ".bak",
			overflowName,
			receiptBackupSetPrefix + receiptBackupSetScope(instance) + "-" +
				receiptBackupSetIDString(0) + receiptBackupSetManifestSuffix,
			maxBase + receiptBackupSetArchiveSuffix,
			maxBase + receiptBackupSetWALCutSuffix,
		}
		for _, name := range files {
			if err := os.WriteFile(
				filepath.Join(dir, name),
				[]byte("ignored"),
				receiptBackupSetFilePermissions,
			); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Mkdir(filepath.Join(dir, "ignored-directory"), receiptBackupSetDirectoryPerms); err != nil {
			t.Fatal(err)
		}

		evidence, err := LoadLatestReceiptBackupSet(dir, instance)
		if err != nil {
			t.Fatal(err)
		}
		if evidence.SetID != valid.id {
			t.Fatalf("latest evidence selected set %d, want %d", evidence.SetID, valid.id)
		}
	})

	t.Run("canonical maximum ID is selected", func(t *testing.T) {
		dir := t.TempDir()
		b := newReceiptBackupSetDiscoveryBackupper(t, dir, "maximum-owner")
		older := writeReceiptBackupSetAt(t, b, 1)
		maximum := cloneReceiptBackupSetAt(t, b, older, ^uint64(0))

		evidence, err := LoadLatestReceiptBackupSet(dir, b.cfg.InstanceID)
		if err != nil {
			t.Fatal(err)
		}
		if evidence.SetID != maximum.id || maximum.id != ^uint64(0) {
			t.Fatalf("latest evidence selected set %d, want %d", evidence.SetID, uint64(^uint64(0)))
		}
	})
}

func TestLoadLatestReceiptBackupSetFailsClosed(t *testing.T) {
	t.Run("newer invalid marker never falls back", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*testing.T, *Backupper, loadedReceiptBackupSet, loadedReceiptBackupSet) []byte
		}{
			{
				name: "malformed",
				mutate: func(
					_ *testing.T,
					_ *Backupper,
					_, _ loadedReceiptBackupSet,
				) []byte {
					return []byte("{")
				},
			},
			{
				name: "unknown field",
				mutate: func(
					t *testing.T,
					_ *Backupper,
					_, newest loadedReceiptBackupSet,
				) []byte {
					t.Helper()
					raw, err := os.ReadFile(newest.manifestPath)
					if err != nil {
						t.Fatal(err)
					}
					return append(raw[:len(raw)-1], []byte(`,"unknown":true}`)...)
				},
			},
			{
				name: "noncanonical JSON",
				mutate: func(
					t *testing.T,
					_ *Backupper,
					_, newest loadedReceiptBackupSet,
				) []byte {
					t.Helper()
					raw, err := os.ReadFile(newest.manifestPath)
					if err != nil {
						t.Fatal(err)
					}
					return append(raw, '\n')
				},
			},
			{
				name: "unsupported version",
				mutate: func(
					t *testing.T,
					_ *Backupper,
					_, newest loadedReceiptBackupSet,
				) []byte {
					t.Helper()
					raw, err := os.ReadFile(newest.manifestPath)
					if err != nil {
						t.Fatal(err)
					}
					manifest, err := decodeReceiptBackupSetManifest(raw)
					if err != nil {
						t.Fatal(err)
					}
					manifest.Version++
					raw, err = json.Marshal(manifest)
					if err != nil {
						t.Fatal(err)
					}
					return raw
				},
			},
			{
				name: "other instance",
				mutate: func(
					t *testing.T,
					_ *Backupper,
					_, newest loadedReceiptBackupSet,
				) []byte {
					t.Helper()
					raw, err := os.ReadFile(newest.manifestPath)
					if err != nil {
						t.Fatal(err)
					}
					manifest, err := decodeReceiptBackupSetManifest(raw)
					if err != nil {
						t.Fatal(err)
					}
					manifest.Instance = "other-owner"
					base := receiptBackupSetBase(manifest.Instance, newest.id)
					manifest.Members = append([]receiptBackupSetMember(nil), manifest.Members...)
					manifest.Members[0].Name = base + receiptBackupSetArchiveSuffix
					manifest.Members[1].Name = base + receiptBackupSetWALCutSuffix
					raw, err = encodeReceiptBackupSetManifest(manifest)
					if err != nil {
						t.Fatal(err)
					}
					return raw
				},
			},
			{
				name: "filename identity mismatch",
				mutate: func(
					t *testing.T,
					b *Backupper,
					older, newest loadedReceiptBackupSet,
				) []byte {
					t.Helper()
					raw, err := os.ReadFile(newest.manifestPath)
					if err != nil {
						t.Fatal(err)
					}
					manifest, err := decodeReceiptBackupSetManifest(raw)
					if err != nil {
						t.Fatal(err)
					}
					manifest.SetID = receiptBackupSetIDString(older.id)
					base := receiptBackupSetBase(b.cfg.InstanceID, older.id)
					manifest.Members = append([]receiptBackupSetMember(nil), manifest.Members...)
					manifest.Members[0].Name = base + receiptBackupSetArchiveSuffix
					manifest.Members[1].Name = base + receiptBackupSetWALCutSuffix
					raw, err = encodeReceiptBackupSetManifest(manifest)
					if err != nil {
						t.Fatal(err)
					}
					return raw
				},
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				dir := t.TempDir()
				b := newReceiptBackupSetDiscoveryBackupper(t, dir, "marker-owner")
				older := writeReceiptBackupSetAt(t, b, 100)
				newest := writeReceiptBackupSetAt(t, b, 200)
				raw := tc.mutate(t, b, older, newest)
				if err := os.WriteFile(newest.manifestPath, raw, receiptBackupSetFilePermissions); err != nil {
					t.Fatal(err)
				}

				var reads []string
				baseReadFile := b.fs.readFile
				b.fs.readFile = func(path string, limit int64) ([]byte, error) {
					reads = append(reads, path)
					return baseReadFile(path, limit)
				}
				if _, err := b.loadLatestReceiptBackupSet(); err == nil ||
					errors.Is(err, ErrReceiptBackupSetNotFound) {
					t.Fatalf("invalid newest marker error = %v", err)
				}
				for _, path := range reads {
					if strings.Contains(path, receiptBackupSetIDString(older.id)) {
						t.Fatalf("loader fell back to older set through %q", path)
					}
				}
				if _, err := LoadReceiptBackupSet(dir, b.cfg.InstanceID, older.manifestPath); err != nil {
					t.Fatalf("older set is not valid: %v", err)
				}
			})
		}
	})

	t.Run("newer missing or corrupt member never falls back", func(t *testing.T) {
		tests := []struct {
			name   string
			mutate func(*testing.T, loadedReceiptBackupSet)
		}{
			{
				name: "missing",
				mutate: func(t *testing.T, newest loadedReceiptBackupSet) {
					t.Helper()
					if err := os.Remove(newest.memberPaths[0]); err != nil {
						t.Fatal(err)
					}
				},
			},
			{
				name: "corrupt",
				mutate: func(t *testing.T, newest loadedReceiptBackupSet) {
					t.Helper()
					raw, err := os.ReadFile(newest.memberPaths[0])
					if err != nil {
						t.Fatal(err)
					}
					raw[len(raw)/2] ^= 0xff
					if err := os.WriteFile(
						newest.memberPaths[0],
						raw,
						receiptBackupSetFilePermissions,
					); err != nil {
						t.Fatal(err)
					}
				},
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				dir := t.TempDir()
				b := newReceiptBackupSetDiscoveryBackupper(t, dir, "member-owner")
				older := writeReceiptBackupSetAt(t, b, 100)
				newest := writeReceiptBackupSetAt(t, b, 200)
				tc.mutate(t, newest)

				if _, err := LoadLatestReceiptBackupSet(dir, b.cfg.InstanceID); err == nil ||
					errors.Is(err, ErrReceiptBackupSetNotFound) {
					t.Fatalf("invalid newest member error = %v", err)
				}
				if _, err := LoadReceiptBackupSet(dir, b.cfg.InstanceID, older.manifestPath); err != nil {
					t.Fatalf("older set is not valid: %v", err)
				}
			})
		}
	})

	t.Run("recognized non-regular newest marker never falls back", func(t *testing.T) {
		tests := []struct {
			name   string
			create func(*testing.T, string, string)
		}{
			{
				name: "directory",
				create: func(t *testing.T, path, _ string) {
					t.Helper()
					if err := os.Mkdir(path, receiptBackupSetDirectoryPerms); err != nil {
						t.Fatal(err)
					}
				},
			},
			{
				name: "symlink",
				create: func(t *testing.T, path, target string) {
					t.Helper()
					if err := os.Symlink(target, path); err != nil {
						t.Fatal(err)
					}
				},
			},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				dir := t.TempDir()
				b := newReceiptBackupSetDiscoveryBackupper(t, dir, "type-owner")
				older := writeReceiptBackupSetAt(t, b, 100)
				newestPath := filepath.Join(
					dir,
					receiptBackupSetBase(b.cfg.InstanceID, 200)+receiptBackupSetManifestSuffix,
				)
				tc.create(t, newestPath, older.manifestPath)

				if _, err := LoadLatestReceiptBackupSet(dir, b.cfg.InstanceID); err == nil ||
					errors.Is(err, ErrReceiptBackupSetNotFound) {
					t.Fatalf("non-regular newest marker error = %v", err)
				}
				if _, err := LoadReceiptBackupSet(dir, b.cfg.InstanceID, older.manifestPath); err != nil {
					t.Fatalf("older set is not valid: %v", err)
				}
			})
		}
	})

	t.Run("filesystem and duplicate-entry errors are returned", func(t *testing.T) {
		injected := errors.New("injected discovery failure")

		t.Run("read directory", func(t *testing.T) {
			b := newReceiptBackupSetDiscoveryBackupper(t, t.TempDir(), "read-dir-owner")
			b.fs.readDir = func(string) ([]os.DirEntry, error) {
				return nil, injected
			}
			if _, err := b.loadLatestReceiptBackupSet(); !errors.Is(err, injected) {
				t.Fatalf("read directory error = %v, want %v", err, injected)
			}
		})

		t.Run("load selected marker", func(t *testing.T) {
			b := newReceiptBackupSetDiscoveryBackupper(t, t.TempDir(), "load-owner")
			writeReceiptBackupSetAt(t, b, 100)
			baseReadFile := b.fs.readFile
			b.fs.readFile = func(path string, limit int64) ([]byte, error) {
				if strings.HasSuffix(path, receiptBackupSetManifestSuffix) {
					return nil, injected
				}
				return baseReadFile(path, limit)
			}
			if _, err := b.loadLatestReceiptBackupSet(); !errors.Is(err, injected) {
				t.Fatalf("selected marker load error = %v, want %v", err, injected)
			}
		})

		t.Run("duplicate recognized name", func(t *testing.T) {
			b := newReceiptBackupSetDiscoveryBackupper(t, t.TempDir(), "duplicate-owner")
			writeReceiptBackupSetAt(t, b, 100)
			baseReadDir := b.fs.readDir
			entries, err := baseReadDir(b.cfg.Dir)
			if err != nil {
				t.Fatal(err)
			}
			var manifestEntry os.DirEntry
			for _, entry := range entries {
				_, _, kind, ok := parseOwnReceiptBackupSetName(entry.Name(), b.cfg.InstanceID)
				if ok && kind == receiptBackupSetManifestFile {
					manifestEntry = entry
					break
				}
			}
			if manifestEntry == nil {
				t.Fatal("fixture has no manifest entry")
			}
			b.fs.readDir = func(string) ([]os.DirEntry, error) {
				return append(append([]os.DirEntry(nil), entries...), manifestEntry), nil
			}
			if _, err := b.loadLatestReceiptBackupSet(); err == nil ||
				!strings.Contains(err.Error(), "duplicate") {
				t.Fatalf("duplicate manifest error = %v", err)
			}
		})
	})
}
