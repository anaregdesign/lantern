package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
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
