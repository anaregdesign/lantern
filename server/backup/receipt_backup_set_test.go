package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/mutationreceipt"
	"github.com/anaregdesign/lantern/server/service"
)

func completedReceiptBackupSet(
	t *testing.T,
) (*Backupper, wholeStateArchive, service.ReceiptWholeStateBackupCapture, *receiptBackupSetSource, loadedReceiptBackupSet) {
	t.Helper()
	archive := wholeStateArchiveFixture(t)
	capture := producerBackupCapture(archive)
	capture.WholeState.Retired = producerRetiredSnapshot(
		t,
		archive.Policy,
		archive.Receipts.ClockHighWaterMillis,
		0x7a,
	)
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
		receiptBackupSetRetiredCatalogSuffix,
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
	refreshReceiptBackupPublicationCommitment(t, &manifest)
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

func refreshReceiptBackupPublicationCommitment(
	t *testing.T,
	manifest *receiptBackupSetManifest,
) {
	t.Helper()
	digest, err := receiptBackupPublicationDigest(*manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.PublicationSHA256 = hex.EncodeToString(digest[:])
}

func historicalReceiptBackupSetManifest(
	t *testing.T,
	instance string,
	setID uint64,
) (string, []byte) {
	t.Helper()
	const historicalPrefix = "lantern-receipt-backup-v1-"
	type member struct {
		Role    string `json:"role"`
		Format  string `json:"format"`
		Version uint16 `json:"version"`
		Name    string `json:"name"`
		Size    uint64 `json:"size"`
		SHA256  string `json:"sha256"`
	}
	type manifest struct {
		Format          string   `json:"format"`
		Version         uint16   `json:"version"`
		Instance        string   `json:"instance"`
		SetID           string   `json:"set_id"`
		BackupTimestamp string   `json:"backup_timestamp"`
		NodeID          string   `json:"node_id"`
		Generation      string   `json:"generation"`
		Members         []member `json:"members"`
	}
	base := historicalPrefix + receiptBackupSetScope(instance) + "-" +
		receiptBackupSetIDString(setID)
	raw, err := json.Marshal(manifest{
		Format:          receiptBackupSetFormat,
		Version:         1,
		Instance:        instance,
		SetID:           receiptBackupSetIDString(setID),
		BackupTimestamp: time.Unix(0, int64(setID)).UTC().Format(time.RFC3339Nano),
		NodeID:          strings.Repeat("11", 16),
		Generation:      strings.Repeat("22", 16),
		Members: []member{
			{
				Role: receiptBackupSetArchiveRole, Format: receiptBackupSetArchiveFormat,
				Version: wholeStateArchiveVersion, Name: base + receiptBackupSetArchiveSuffix,
				Size: 1, SHA256: strings.Repeat("33", sha256.Size),
			},
			{
				Role: receiptBackupSetWALCutRole, Format: receiptBackupSetWALCutFormat,
				Version: 2, Name: base + receiptBackupSetWALCutSuffix,
				Size: receiptArchiveWALCutSize, SHA256: strings.Repeat("44", sha256.Size),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return base + receiptBackupSetManifestSuffix, raw
}

func obsoleteReceiptBackupSetMarker(prefix, instance string, setID uint64) string {
	return prefix + receiptBackupSetScope(instance) + "-" +
		receiptBackupSetIDString(setID) + receiptBackupSetManifestSuffix
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
	if !reflect.DeepEqual(loaded.retired, capture.WholeState.Retired) {
		t.Fatal("loaded archive lost retired receipt state")
	}
	manifestRaw, err := os.ReadFile(loaded.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := decodeReceiptBackupSetManifest(manifestRaw)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Version != 1 ||
		manifest.ActivePolicy.Epoch != hex.EncodeToString(archive.Policy.Epoch[:]) ||
		manifest.Cut.ReceiptClockHighWaterMillis != archive.Receipts.ClockHighWaterMillis ||
		manifest.Cut.OriginCount != uint64(len(archive.Origins)) ||
		manifest.RetiredCatalog.EpochCount != uint64(len(capture.WholeState.Retired.Epochs)) ||
		manifest.RetiredCatalog.ReceiptCount != 1 ||
		len(manifest.Members) != 3 ||
		manifest.Members[0].Role != receiptBackupSetArchiveRole ||
		manifest.Members[1].Role != receiptBackupSetWALCutRole ||
		manifest.Members[2].Role != receiptBackupSetRetiredCatalogRole {
		t.Fatalf("receipt backup-set manifest metadata = %+v", manifest)
	}
	walCutRaw, err := os.ReadFile(loaded.memberPaths[1])
	if err != nil {
		t.Fatal(err)
	}
	retiredRaw, err := os.ReadFile(loaded.memberPaths[2])
	if err != nil {
		t.Fatal(err)
	}
	rebuilt, err := newReceiptBackupSetManifest(
		b.cfg.InstanceID,
		loaded.id,
		loaded.createdAt,
		receiptBackupSetProduct{nodeID: capture.NodeID, generation: capture.Generation},
		loaded.archiveRaw,
		walCutRaw,
		retiredRaw,
	)
	if err != nil {
		t.Fatal(err)
	}
	rebuiltRaw, err := encodeReceiptBackupSetManifest(rebuilt)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rebuiltRaw, manifestRaw) {
		t.Fatal("identical receipt backup-set members produced nondeterministic manifest bytes")
	}
	entries, err := os.ReadDir(b.cfg.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != receiptBackupSetExpectedMemberCount+1 {
		t.Fatalf("committed set files = %d, want %d", len(entries), receiptBackupSetExpectedMemberCount+1)
	}
	wantPrefix := "lantern-receipt-backup-" + receiptBackupSetScope(b.cfg.InstanceID) + "-"
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Name(), wantPrefix) {
			t.Fatalf("receipt backup set emitted noncanonical filename %q", entry.Name())
		}
		if strings.HasSuffix(entry.Name(), receiptBackupSetTempSuffix) ||
			strings.HasSuffix(entry.Name(), fileSuffix) {
			t.Fatalf("durable receipt set emitted obsolete or temporary file %q", entry.Name())
		}
	}

	evidence, err := LoadReceiptBackupSet(b.cfg.Dir, b.cfg.InstanceID, loaded.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.SetID != loaded.id || evidence.BackupTimestamp != loaded.createdAt ||
		evidence.NodeID != capture.NodeID || evidence.Generation != capture.Generation ||
		evidence.WALCut != capture.WALTip || evidence.Stats != loaded.stats ||
		!bytes.Equal(evidence.Archive, loaded.archiveRaw) ||
		!bytes.Equal(evidence.RetiredCatalog, loaded.retiredRaw) {
		t.Fatalf("public receipt backup-set evidence = %+v", evidence)
	}

	ownedArchive := bytes.Clone(evidence.Archive)
	ownedRetired := bytes.Clone(evidence.RetiredCatalog)
	loaded.archiveRaw[0] ^= 0xff
	loaded.retiredRaw[0] ^= 0xff
	if !bytes.Equal(evidence.Archive, ownedArchive) ||
		!bytes.Equal(evidence.RetiredCatalog, ownedRetired) {
		t.Fatal("public evidence bytes alias the loaded member buffers")
	}
}

func TestReceiptBackupSetPreservesRetiredEvidence(t *testing.T) {
	archive := wholeStateArchiveFixture(t)
	capture := producerBackupCapture(archive)
	capture.WholeState.Retired = producerRetiredSnapshot(
		t,
		archive.Policy,
		archive.Receipts.ClockHighWaterMillis,
		0x7a,
	)
	source := &receiptBackupSetSource{capture: capture}
	dir := t.TempDir()
	b := newReceiptBackupSetTestBackupper(t, dir, "retired-preserved", 0, source, archive.Policy)

	stats, err := b.BackupNow(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if stats.Receipts != len(archive.Receipts.Receipts)+1 || stats.Members != 3 {
		t.Fatalf("retired-aware stats = %+v", stats)
	}
	evidence, err := LoadLatestReceiptBackupSet(dir, b.cfg.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	retired, _, err := decodeRetiredCatalogArchive(evidence.RetiredCatalog)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(retired, capture.WholeState.Retired) {
		t.Fatalf("retired evidence = %+v, want %+v", retired, capture.WholeState.Retired)
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

	t.Run("unsupported schema version", func(t *testing.T) {
		unsupported := manifest
		unsupported.Version = 2
		unsupportedRaw, err := json.Marshal(unsupported)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := decodeReceiptBackupSetManifest(unsupportedRaw); !errors.Is(err, ErrUnsupportedReceiptBackupSet) {
			t.Fatalf("unsupported receipt backup-set schema error = %v, want unsupported", err)
		}
	})

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
			{"publication commitment", func(m *receiptBackupSetManifest) { m.PublicationSHA256 = "00" }},
			{"active epoch", func(m *receiptBackupSetManifest) { m.ActivePolicy.Epoch = "00" }},
			{"active retention", func(m *receiptBackupSetManifest) { m.ActivePolicy.RetentionMillis = 0 }},
			{"active entry overflow", func(m *receiptBackupSetManifest) { m.ActivePolicy.MaxEntries = ^uint64(0) }},
			{"active fingerprint", func(m *receiptBackupSetManifest) { m.ActivePolicy.PolicyFingerprint = "00" }},
			{"retired format", func(m *receiptBackupSetManifest) { m.RetiredCatalog.Format = "unknown" }},
			{"retired version", func(m *receiptBackupSetManifest) { m.RetiredCatalog.Version++ }},
			{"retired active epoch", func(m *receiptBackupSetManifest) { m.RetiredCatalog.ActiveEpoch = "00" }},
			{"retired high-water", func(m *receiptBackupSetManifest) { m.RetiredCatalog.ClockHighWaterMillis++ }},
			{"retired entry cap", func(m *receiptBackupSetManifest) { m.RetiredCatalog.MaxEntries++ }},
			{"retired byte cap", func(m *receiptBackupSetManifest) { m.RetiredCatalog.MaxBytes++ }},
			{"retired epoch count", func(m *receiptBackupSetManifest) { m.RetiredCatalog.EpochCount = m.RetiredCatalog.MaxEntries + 1 }},
			{"retired receipt count", func(m *receiptBackupSetManifest) { m.RetiredCatalog.ReceiptCount = m.RetiredCatalog.MaxEntries + 1 }},
			{"retired epoch count overflow", func(m *receiptBackupSetManifest) { m.RetiredCatalog.EpochCount = ^uint64(0) }},
			{"retired receipt count overflow", func(m *receiptBackupSetManifest) { m.RetiredCatalog.ReceiptCount = ^uint64(0) }},
			{"retired policies", func(m *receiptBackupSetManifest) { m.RetiredCatalog.PolicySetSHA256 = "00" }},
			{"negative high-water", func(m *receiptBackupSetManifest) { m.Cut.ReceiptClockHighWaterMillis = -1 }},
			{"cutoff HLC", func(m *receiptBackupSetManifest) { m.Cut.SnapshotHLC.WallNanos = 0 }},
			{"cutoff NodeID", func(m *receiptBackupSetManifest) { m.Cut.SnapshotHLC.NodeID = "00" }},
			{"origin count overflow", func(m *receiptBackupSetManifest) { m.Cut.OriginCount = ^uint64(0) }},
			{"origin digest", func(m *receiptBackupSetManifest) { m.Cut.OriginCutoffsSHA256 = "00" }},
			{"WAL offset overflow", func(m *receiptBackupSetManifest) { m.Cut.WALCut.Offset = ^uint64(0) }},
			{"WAL cut tip mismatch", func(m *receiptBackupSetManifest) { m.Cut.WALTip.Sequence++ }},
			{"missing member", func(m *receiptBackupSetManifest) { m.Members = m.Members[:1] }},
			{"duplicate member", func(m *receiptBackupSetManifest) { m.Members[1] = m.Members[0] }},
			{"reordered members", func(m *receiptBackupSetManifest) { m.Members[0], m.Members[1] = m.Members[1], m.Members[0] }},
			{"extra member", func(m *receiptBackupSetManifest) { m.Members = append(m.Members, m.Members[0]) }},
			{"unknown member", func(m *receiptBackupSetManifest) { m.Members[1].Role = "unknown" }},
			{"unsafe member path", func(m *receiptBackupSetManifest) { m.Members[0].Name = "../archive" }},
			{"absolute member path", func(m *receiptBackupSetManifest) { m.Members[0].Name = "/tmp/archive" }},
			{"backslash member path", func(m *receiptBackupSetManifest) { m.Members[0].Name = `..\archive` }},
			{"wrong member name", func(m *receiptBackupSetManifest) { m.Members[0].Name = "other.active.lar" }},
			{"zero member size", func(m *receiptBackupSetManifest) { m.Members[0].Size = 0 }},
			{"active member size overflow", func(m *receiptBackupSetManifest) {
				m.Members[0].Size = uint64(wholeStateArchiveMaxBytes) + 1
			}},
			{"WAL member size overflow", func(m *receiptBackupSetManifest) {
				m.Members[1].Size = uint64(receiptArchiveWALCutSize) + 1
			}},
			{"retired member size overflow", func(m *receiptBackupSetManifest) {
				m.Members[2].Size = uint64(wholeStateArchiveMaxBytes) + 1
			}},
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
			name            string
			mutate          func(*testing.T, *Backupper, loadedReceiptBackupSet, receiptBackupSetManifest)
			wantUnsupported bool
		}{
			{
				name: "size",
				mutate: func(t *testing.T, b *Backupper, loaded loadedReceiptBackupSet, manifest receiptBackupSetManifest) {
					manifest.Members[0].Size++
					refreshReceiptBackupPublicationCommitment(t, &manifest)
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
					refreshReceiptBackupPublicationCommitment(t, &manifest)
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
					manifest.Cut.SnapshotHLC.NodeID = manifest.NodeID
					refreshReceiptBackupPublicationCommitment(t, &manifest)
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
					refreshReceiptBackupPublicationCommitment(t, &manifest)
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
				name:            "obsolete LRWLCUT2 member",
				wantUnsupported: true,
				mutate: func(t *testing.T, b *Backupper, loaded loadedReceiptBackupSet, manifest receiptBackupSetManifest) {
					raw, err := os.ReadFile(loaded.memberPaths[1])
					if err != nil {
						t.Fatal(err)
					}
					copy(raw[:8], "LRWLCUT2")
					binary.BigEndian.PutUint16(raw[8:10], 2)
					checksum := sha256.Sum256(raw[:receiptArchiveWALCutPayloadSize])
					copy(raw[receiptArchiveWALCutPayloadSize:], checksum[:])
					if err := os.WriteFile(loaded.memberPaths[1], raw, receiptBackupSetFilePermissions); err != nil {
						t.Fatal(err)
					}
					digest := sha256.Sum256(raw)
					manifest.Members[1].Size = uint64(len(raw))
					manifest.Members[1].SHA256 = hex.EncodeToString(digest[:])
					refreshReceiptBackupPublicationCommitment(t, &manifest)
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
				_, err = b.loadReceiptBackupSet(loaded.manifestPath)
				if err == nil {
					t.Fatal("invalid committed set loaded")
				}
				if tc.wantUnsupported && !errors.Is(err, ErrUnsupportedReceiptBackupSet) {
					t.Fatalf("obsolete committed set error = %v, want unsupported", err)
				}
				if tc.wantUnsupported {
					if _, latestErr := b.loadLatestReceiptBackupSet(); !errors.Is(
						latestErr,
						ErrUnsupportedReceiptBackupSet,
					) {
						t.Fatalf("latest obsolete committed set error = %v, want unsupported", latestErr)
					}
				}
			})
		}
	})

	if _, err := b.loadReceiptBackupSet(filepath.Join(b.cfg.Dir, "..", filepath.Base(loaded.manifestPath))); err == nil {
		t.Fatal("manifest path outside the backup directory was accepted")
	}
}

func TestReceiptBackupSetRejectsCrossCutManifestMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, *receiptBackupSetManifest)
	}{
		{
			name: "active epoch",
			mutate: func(_ *testing.T, manifest *receiptBackupSetManifest) {
				epoch := hex.EncodeToString(bytes.Repeat([]byte{0x31}, 16))
				manifest.ActivePolicy.Epoch = epoch
				manifest.RetiredCatalog.ActiveEpoch = epoch
			},
		},
		{
			name: "active policy",
			mutate: func(t *testing.T, manifest *receiptBackupSetManifest) {
				manifest.ActivePolicy.MaxEntries++
				manifest.RetiredCatalog.MaxEntries++
				rawEpoch, err := decodeReceiptBackupSetIdentity(manifest.ActivePolicy.Epoch)
				if err != nil {
					t.Fatal(err)
				}
				var epoch mutationreceipt.Epoch
				copy(epoch[:], rawEpoch[:])
				store, err := mutationreceipt.New(mutationreceipt.Config{
					Epoch:      epoch,
					Retention:  time.Duration(manifest.ActivePolicy.RetentionMillis) * time.Millisecond,
					MaxEntries: int(manifest.ActivePolicy.MaxEntries),
					MaxBytes:   manifest.ActivePolicy.MaxBytes,
				})
				if err != nil {
					t.Fatal(err)
				}
				fingerprint := store.PolicyFingerprint()
				manifest.ActivePolicy.PolicyFingerprint = hex.EncodeToString(fingerprint[:])
			},
		},
		{
			name: "clock high-water",
			mutate: func(_ *testing.T, manifest *receiptBackupSetManifest) {
				manifest.Cut.ReceiptClockHighWaterMillis--
				manifest.RetiredCatalog.ClockHighWaterMillis--
			},
		},
		{
			name: "snapshot HLC",
			mutate: func(_ *testing.T, manifest *receiptBackupSetManifest) {
				manifest.Cut.SnapshotHLC.Logical++
			},
		},
		{
			name: "origin count",
			mutate: func(_ *testing.T, manifest *receiptBackupSetManifest) {
				manifest.Cut.OriginCount++
			},
		},
		{
			name: "origin cutoffs",
			mutate: func(_ *testing.T, manifest *receiptBackupSetManifest) {
				manifest.Cut.OriginCutoffsSHA256 = hex.EncodeToString(bytes.Repeat([]byte{0x32}, sha256.Size))
			},
		},
		{
			name: "retired policies",
			mutate: func(_ *testing.T, manifest *receiptBackupSetManifest) {
				manifest.RetiredCatalog.PolicySetSHA256 = hex.EncodeToString(bytes.Repeat([]byte{0x33}, sha256.Size))
			},
		},
		{
			name: "WAL frontier",
			mutate: func(_ *testing.T, manifest *receiptBackupSetManifest) {
				manifest.Cut.LocalSequence++
				manifest.Cut.WALCut.Sequence++
				manifest.Cut.WALTip.Sequence++
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
			tc.mutate(t, &manifest)
			refreshReceiptBackupPublicationCommitment(t, &manifest)
			raw, err = encodeReceiptBackupSetManifest(manifest)
			if err != nil {
				t.Fatalf("cross-cut test manifest is not structurally valid: %v", err)
			}
			if err := os.WriteFile(loaded.manifestPath, raw, receiptBackupSetFilePermissions); err != nil {
				t.Fatal(err)
			}
			if _, err := b.loadReceiptBackupSet(loaded.manifestPath); err == nil {
				t.Fatal("cross-cut manifest metadata was accepted")
			}
		})
	}
}

func TestReceiptBackupSetRejectsMissingTruncatedCorruptAndReplacedMembers(t *testing.T) {
	for memberIndex, role := range []string{
		receiptBackupSetArchiveRole,
		receiptBackupSetWALCutRole,
		receiptBackupSetRetiredCatalogRole,
	} {
		for _, mutation := range []string{"missing", "truncated", "corrupt", "replaced"} {
			t.Run(role+"/"+mutation, func(t *testing.T) {
				b, _, _, _, loaded := completedReceiptBackupSet(t)
				path := loaded.memberPaths[memberIndex]
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				switch mutation {
				case "missing":
					err = os.Remove(path)
				case "truncated":
					err = os.WriteFile(path, raw[:len(raw)-1], receiptBackupSetFilePermissions)
				case "corrupt":
					raw[len(raw)/2] ^= 0x80
					err = os.WriteFile(path, raw, receiptBackupSetFilePermissions)
				case "replaced":
					err = os.WriteFile(path, bytes.Repeat([]byte{0xa5}, len(raw)), receiptBackupSetFilePermissions)
				default:
					t.Fatalf("unknown mutation %q", mutation)
				}
				if err != nil {
					t.Fatal(err)
				}
				if _, err := b.loadReceiptBackupSet(loaded.manifestPath); err == nil {
					t.Fatal("invalid member was accepted")
				}
			})
		}
	}
}

func TestReceiptBackupSetRejectsMissingTruncatedCorruptAndReplacedManifest(t *testing.T) {
	for _, mutation := range []string{"missing", "truncated", "corrupt", "replaced", "oversized"} {
		t.Run(mutation, func(t *testing.T) {
			b, _, _, _, loaded := completedReceiptBackupSet(t)
			raw, err := os.ReadFile(loaded.manifestPath)
			if err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "missing":
				err = os.Remove(loaded.manifestPath)
			case "truncated":
				err = os.WriteFile(loaded.manifestPath, raw[:len(raw)-1], receiptBackupSetFilePermissions)
			case "corrupt":
				raw[len(raw)/2] ^= 0x80
				err = os.WriteFile(loaded.manifestPath, raw, receiptBackupSetFilePermissions)
			case "replaced":
				other := writeReceiptBackupSetAt(t, b, int64(loaded.id+1))
				replacement, readErr := os.ReadFile(other.manifestPath)
				if readErr != nil {
					t.Fatal(readErr)
				}
				err = os.WriteFile(loaded.manifestPath, replacement, receiptBackupSetFilePermissions)
			case "oversized":
				err = os.WriteFile(
					loaded.manifestPath,
					bytes.Repeat([]byte{'x'}, receiptBackupSetManifestMaxBytes+1),
					receiptBackupSetFilePermissions,
				)
			default:
				t.Fatalf("unknown mutation %q", mutation)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := b.loadReceiptBackupSet(loaded.manifestPath); err == nil {
				t.Fatal("invalid manifest was accepted")
			}
		})
	}
}

func TestReceiptBackupSetRejectsCrossMemberReplacementAndChangedWAL(t *testing.T) {
	t.Run("retired high-water", func(t *testing.T) {
		b, archive, _, _, loaded := completedReceiptBackupSet(t)
		replacement, err := encodeRetiredCatalogArchive(
			archive.Policy,
			producerEmptyRetiredSnapshot(
				t,
				archive.Policy,
				archive.Receipts.ClockHighWaterMillis-1,
			),
		)
		if err != nil {
			t.Fatal(err)
		}
		replaceReceiptBackupMemberAndDigest(t, loaded, 2, replacement)
		if _, err := b.loadReceiptBackupSet(loaded.manifestPath); err == nil {
			t.Fatal("retired catalog from a lower active cut was accepted")
		}
	})

	t.Run("retired policy set", func(t *testing.T) {
		b, archive, _, _, loaded := completedReceiptBackupSet(t)
		replacement, err := encodeRetiredCatalogArchive(
			archive.Policy,
			producerRetiredSnapshot(
				t,
				archive.Policy,
				archive.Receipts.ClockHighWaterMillis,
				0x6a,
			),
		)
		if err != nil {
			t.Fatal(err)
		}
		replaceReceiptBackupMemberAndDigest(t, loaded, 2, replacement)
		if _, err := b.loadReceiptBackupSet(loaded.manifestPath); err == nil {
			t.Fatal("retired catalog with mismatched policy commitment was accepted")
		}
	})

	t.Run("changed WAL tip", func(t *testing.T) {
		b, _, _, _, loaded := completedReceiptBackupSet(t)
		raw, err := os.ReadFile(loaded.memberPaths[1])
		if err != nil {
			t.Fatal(err)
		}
		walCut, err := decodeReceiptArchiveWALCut(raw)
		if err != nil {
			t.Fatal(err)
		}
		walCut.tipSeq++
		walCut.tipOffset++
		replacement := encodeReceiptArchiveWALCut(walCut)
		replaceReceiptBackupMemberAndDigest(t, loaded, 1, replacement)
		if _, err := b.loadReceiptBackupSet(loaded.manifestPath); err == nil {
			t.Fatal("changed WAL tip was accepted")
		}
	})
}

func replaceReceiptBackupMemberAndDigest(
	t *testing.T,
	loaded loadedReceiptBackupSet,
	memberIndex int,
	replacement []byte,
) {
	t.Helper()
	if err := os.WriteFile(loaded.memberPaths[memberIndex], replacement, receiptBackupSetFilePermissions); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(loaded.manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := decodeReceiptBackupSetManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(replacement)
	manifest.Members[memberIndex].Size = uint64(len(replacement))
	manifest.Members[memberIndex].SHA256 = hex.EncodeToString(digest[:])
	refreshReceiptBackupPublicationCommitment(t, &manifest)
	raw, err = encodeReceiptBackupSetManifest(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(loaded.manifestPath, raw, receiptBackupSetFilePermissions); err != nil {
		t.Fatal(err)
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
			maxBase + receiptBackupSetRetiredCatalogSuffix,
		}
		for _, prefix := range receiptBackupSetUnsupportedPrefixes {
			files = append(files, obsoleteReceiptBackupSetMarker(
				prefix,
				"foreign-owner",
				^uint64(0),
			))
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

	t.Run("older obsolete markers do not hide newest current set", func(t *testing.T) {
		for _, prefix := range receiptBackupSetUnsupportedPrefixes {
			t.Run(prefix, func(t *testing.T) {
				dir := t.TempDir()
				b := newReceiptBackupSetDiscoveryBackupper(t, dir, "newer-current-owner")
				name := obsoleteReceiptBackupSetMarker(prefix, b.cfg.InstanceID, 100)
				if err := os.WriteFile(
					filepath.Join(dir, name),
					[]byte(`{"format":"obsolete"}`),
					receiptBackupSetFilePermissions,
				); err != nil {
					t.Fatal(err)
				}
				newest := writeReceiptBackupSetAt(t, b, 200)
				evidence, err := LoadLatestReceiptBackupSet(dir, b.cfg.InstanceID)
				if err != nil {
					t.Fatal(err)
				}
				if evidence.SetID != newest.id {
					t.Fatalf("latest evidence selected set %d, want %d", evidence.SetID, newest.id)
				}
			})
		}
	})

	t.Run("duplicate lower obsolete IDs do not preempt newer current set", func(t *testing.T) {
		dir := t.TempDir()
		b := newReceiptBackupSetDiscoveryBackupper(t, dir, "ordered-obsolete-owner")
		newest := writeReceiptBackupSetAt(t, b, 200)
		for _, prefix := range receiptBackupSetUnsupportedPrefixes {
			name := obsoleteReceiptBackupSetMarker(prefix, b.cfg.InstanceID, 100)
			if err := os.WriteFile(
				filepath.Join(dir, name),
				[]byte(`{"format":"obsolete"}`),
				receiptBackupSetFilePermissions,
			); err != nil {
				t.Fatal(err)
			}
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		var ordered []os.DirEntry
		for _, prefix := range receiptBackupSetUnsupportedPrefixes {
			want := obsoleteReceiptBackupSetMarker(prefix, b.cfg.InstanceID, 100)
			for _, entry := range entries {
				if entry.Name() == want {
					ordered = append(ordered, entry)
				}
			}
		}
		for _, entry := range entries {
			if entry.Name() == filepath.Base(newest.manifestPath) {
				ordered = append(ordered, entry)
			}
		}
		if len(ordered) != 3 {
			t.Fatalf("ordered marker fixture has %d entries, want 3", len(ordered))
		}
		b.fs.openDir = func(string) (receiptBackupReadDir, error) {
			return &receiptBackupStaticReadDir{entries: ordered}, nil
		}
		evidence, err := b.loadLatestReceiptBackupSet()
		if err != nil {
			t.Fatal(err)
		}
		if evidence.id != newest.id {
			t.Fatalf("ordered discovery selected %d, want %d", evidence.id, newest.id)
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
	t.Run("obsolete versioned markers are terminal unsupported", func(t *testing.T) {
		for prefixIndex, prefix := range receiptBackupSetUnsupportedPrefixes {
			for _, withOlderCurrent := range []bool{false, true} {
				name := prefix
				if withOlderCurrent {
					name += " newer than current"
				}
				t.Run(name, func(t *testing.T) {
					dir := t.TempDir()
					b := newReceiptBackupSetDiscoveryBackupper(t, dir, "obsolete-owner")
					if withOlderCurrent {
						writeReceiptBackupSetAt(t, b, 100)
					}
					obsoleteName := obsoleteReceiptBackupSetMarker(prefix, b.cfg.InstanceID, 200)
					obsoleteRaw := []byte(`{"format":"lantern-receipt-backup-set","version":2}`)
					if prefixIndex == 0 {
						obsoleteName, obsoleteRaw = historicalReceiptBackupSetManifest(
							t,
							b.cfg.InstanceID,
							200,
						)
					}
					obsoletePath := filepath.Join(dir, obsoleteName)
					if err := os.WriteFile(
						obsoletePath,
						obsoleteRaw,
						receiptBackupSetFilePermissions,
					); err != nil {
						t.Fatal(err)
					}
					if _, err := LoadReceiptBackupSet(
						dir,
						b.cfg.InstanceID,
						obsoletePath,
					); !errors.Is(err, ErrUnsupportedReceiptBackupSet) {
						t.Fatalf("direct obsolete set error = %v, want unsupported", err)
					}
					if _, err := LoadLatestReceiptBackupSet(
						dir,
						b.cfg.InstanceID,
					); !errors.Is(err, ErrUnsupportedReceiptBackupSet) {
						t.Fatalf("latest obsolete set error = %v, want unsupported", err)
					}
				})
			}
		}
	})

	t.Run("obsolete marker tied with current maximum is unsupported", func(t *testing.T) {
		for _, prefix := range receiptBackupSetUnsupportedPrefixes {
			dir := t.TempDir()
			b := newReceiptBackupSetDiscoveryBackupper(t, dir, "tied-obsolete-owner")
			writeReceiptBackupSetAt(t, b, 200)
			name := obsoleteReceiptBackupSetMarker(prefix, b.cfg.InstanceID, 200)
			if err := os.WriteFile(
				filepath.Join(dir, name),
				[]byte(`{"format":"obsolete"}`),
				receiptBackupSetFilePermissions,
			); err != nil {
				t.Fatal(err)
			}
			if _, err := b.loadLatestReceiptBackupSet(); !errors.Is(
				err,
				ErrUnsupportedReceiptBackupSet,
			) {
				t.Fatalf("tied obsolete marker error = %v, want unsupported", err)
			}
		}
	})

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
					manifest.Members[2].Name = base + receiptBackupSetRetiredCatalogSuffix
					refreshReceiptBackupPublicationCommitment(t, &manifest)
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
					manifest.Members[2].Name = base + receiptBackupSetRetiredCatalogSuffix
					refreshReceiptBackupPublicationCommitment(t, &manifest)
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
			b.fs.openDir = func(string) (receiptBackupReadDir, error) {
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
			entries, err := os.ReadDir(b.cfg.Dir)
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
			b.fs.openDir = func(string) (receiptBackupReadDir, error) {
				return &receiptBackupStaticReadDir{
					entries: append(append([]os.DirEntry(nil), entries...), manifestEntry),
				}, nil
			}
			if _, err := b.loadLatestReceiptBackupSet(); err == nil ||
				!strings.Contains(err.Error(), "duplicate") {
				t.Fatalf("duplicate manifest error = %v", err)
			}
		})
	})
}
