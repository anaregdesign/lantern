package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
)

const (
	receiptBackupSetFormat              = "lantern-receipt-backup-set"
	receiptBackupSetVersion             = uint16(1)
	receiptBackupSetPrefix              = "lantern-receipt-backup-v1-"
	receiptBackupSetArchiveSuffix       = ".active.lar"
	receiptBackupSetWALCutSuffix        = ".active.walcut"
	receiptBackupSetManifestSuffix      = ".set.json"
	receiptBackupSetTempSuffix          = ".tmp"
	receiptBackupSetManifestMaxBytes    = 64 << 10
	receiptBackupSetInstanceMaxBytes    = 255
	receiptBackupSetArchiveRole         = "active_epoch_archive"
	receiptBackupSetArchiveFormat       = "lantern-receipt-whole-state"
	receiptBackupSetWALCutRole          = "active_epoch_wal_cut"
	receiptBackupSetWALCutFormat        = "lantern-receipt-wal-cut"
	receiptBackupSetCanonicalIDDigits   = 20
	receiptBackupSetFilePermissions     = 0o600
	receiptBackupSetDirectoryPerms      = 0o755
	receiptBackupSetExpectedMemberCount = 2
)

type receiptBackupSetMember struct {
	Role    string `json:"role"`
	Format  string `json:"format"`
	Version uint16 `json:"version"`
	Name    string `json:"name"`
	Size    uint64 `json:"size"`
	SHA256  string `json:"sha256"`
}

type receiptBackupSetManifest struct {
	Format          string                   `json:"format"`
	Version         uint16                   `json:"version"`
	Instance        string                   `json:"instance"`
	SetID           string                   `json:"set_id"`
	BackupTimestamp string                   `json:"backup_timestamp"`
	NodeID          string                   `json:"node_id"`
	Generation      string                   `json:"generation"`
	Members         []receiptBackupSetMember `json:"members"`
}

type receiptBackupSet struct {
	id           uint64
	manifestPath string
	memberPaths  []string
	stats        Stats
}

// loadedReceiptBackupSet is the single fully validated input a later durable
// restore layer may consume. Its member bytes are owned by the value; loading
// never consults the live appendable WAL.
type loadedReceiptBackupSet struct {
	receiptBackupSet
	archiveRaw []byte
	archive    wholeStateArchive
	walCut     receiptArchiveWALCut
	nodeID     hlc.NodeID
	generation [16]byte
	createdAt  time.Time
}

func newReceiptBackupSetManifest(
	instance string,
	setID uint64,
	at time.Time,
	product receiptActiveEpochArchiveProduct,
	archiveRaw, walCutRaw []byte,
) (receiptBackupSetManifest, error) {
	if err := validateReceiptBackupSetInstance(instance); err != nil {
		return receiptBackupSetManifest{}, err
	}
	if setID == 0 {
		return receiptBackupSetManifest{}, errors.New("backup: receipt backup set ID is zero")
	}
	if at.IsZero() || at.UnixNano() <= 0 {
		return receiptBackupSetManifest{}, errors.New("backup: receipt backup timestamp is invalid")
	}
	if product.nodeID == (hlc.NodeID{}) || product.generation == ([16]byte{}) {
		return receiptBackupSetManifest{}, errors.New("backup: receipt backup set runtime identity is zero")
	}
	base := receiptBackupSetBase(instance, setID)
	archiveDigest := sha256.Sum256(archiveRaw)
	walCutDigest := sha256.Sum256(walCutRaw)
	return receiptBackupSetManifest{
		Format:          receiptBackupSetFormat,
		Version:         receiptBackupSetVersion,
		Instance:        instance,
		SetID:           receiptBackupSetIDString(setID),
		BackupTimestamp: at.UTC().Format(time.RFC3339Nano),
		NodeID:          hex.EncodeToString(product.nodeID[:]),
		Generation:      hex.EncodeToString(product.generation[:]),
		Members: []receiptBackupSetMember{
			{
				Role: receiptBackupSetArchiveRole, Format: receiptBackupSetArchiveFormat,
				Version: wholeStateArchiveVersion, Name: base + receiptBackupSetArchiveSuffix,
				Size: uint64(len(archiveRaw)), SHA256: hex.EncodeToString(archiveDigest[:]),
			},
			{
				Role: receiptBackupSetWALCutRole, Format: receiptBackupSetWALCutFormat,
				Version: receiptArchiveWALCutVersion, Name: base + receiptBackupSetWALCutSuffix,
				Size: uint64(len(walCutRaw)), SHA256: hex.EncodeToString(walCutDigest[:]),
			},
		},
	}, nil
}

func encodeReceiptBackupSetManifest(manifest receiptBackupSetManifest) ([]byte, error) {
	if _, err := validateReceiptBackupSetManifest(manifest); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("backup: encode receipt backup-set manifest: %w", err)
	}
	if len(raw) == 0 || len(raw) > receiptBackupSetManifestMaxBytes {
		return nil, errors.New("backup: receipt backup-set manifest has an invalid size")
	}
	return raw, nil
}

func decodeReceiptBackupSetManifest(raw []byte) (receiptBackupSetManifest, error) {
	if len(raw) == 0 || len(raw) > receiptBackupSetManifestMaxBytes {
		return receiptBackupSetManifest{}, errors.New("backup: receipt backup-set manifest has an invalid size")
	}
	var manifest receiptBackupSetManifest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return receiptBackupSetManifest{}, fmt.Errorf("backup: decode receipt backup-set manifest: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return receiptBackupSetManifest{}, errors.New("backup: receipt backup-set manifest has trailing data")
		}
		return receiptBackupSetManifest{}, fmt.Errorf("backup: decode receipt backup-set manifest trailer: %w", err)
	}
	canonical, err := json.Marshal(manifest)
	if err != nil {
		return receiptBackupSetManifest{}, fmt.Errorf("backup: re-encode receipt backup-set manifest: %w", err)
	}
	if !bytes.Equal(raw, canonical) {
		return receiptBackupSetManifest{}, errors.New("backup: receipt backup-set manifest is not canonical")
	}
	if _, err := validateReceiptBackupSetManifest(manifest); err != nil {
		return receiptBackupSetManifest{}, err
	}
	return manifest, nil
}

func validateReceiptBackupSetManifest(manifest receiptBackupSetManifest) (uint64, error) {
	if manifest.Format != receiptBackupSetFormat || manifest.Version != receiptBackupSetVersion {
		return 0, errors.New("backup: receipt backup-set manifest has an unsupported format or version")
	}
	if err := validateReceiptBackupSetInstance(manifest.Instance); err != nil {
		return 0, err
	}
	setID, err := strconv.ParseUint(manifest.SetID, 10, 64)
	if err != nil || setID == 0 || manifest.SetID != receiptBackupSetIDString(setID) {
		return 0, errors.New("backup: receipt backup-set manifest has a malformed set ID")
	}
	backupTime, err := time.Parse(time.RFC3339Nano, manifest.BackupTimestamp)
	if err != nil || backupTime.IsZero() || backupTime.UnixNano() <= 0 ||
		backupTime.UTC().Format(time.RFC3339Nano) != manifest.BackupTimestamp {
		return 0, errors.New("backup: receipt backup-set manifest has a malformed backup timestamp")
	}
	nodeID, err := decodeReceiptBackupSetIdentity(manifest.NodeID)
	if err != nil || nodeID == ([16]byte{}) {
		return 0, errors.New("backup: receipt backup-set manifest has a malformed NodeID")
	}
	generation, err := decodeReceiptBackupSetIdentity(manifest.Generation)
	if err != nil || generation == ([16]byte{}) {
		return 0, errors.New("backup: receipt backup-set manifest has a malformed generation")
	}
	if len(manifest.Members) != receiptBackupSetExpectedMemberCount {
		return 0, errors.New("backup: receipt backup-set manifest has missing or unknown members")
	}
	base := receiptBackupSetBase(manifest.Instance, setID)
	expected := []receiptBackupSetMember{
		{
			Role: receiptBackupSetArchiveRole, Format: receiptBackupSetArchiveFormat,
			Version: wholeStateArchiveVersion, Name: base + receiptBackupSetArchiveSuffix,
		},
		{
			Role: receiptBackupSetWALCutRole, Format: receiptBackupSetWALCutFormat,
			Version: receiptArchiveWALCutVersion, Name: base + receiptBackupSetWALCutSuffix,
		},
	}
	roles := make(map[string]struct{}, len(manifest.Members))
	names := make(map[string]struct{}, len(manifest.Members))
	for i, member := range manifest.Members {
		if _, ok := roles[member.Role]; ok {
			return 0, errors.New("backup: receipt backup-set manifest has duplicate member roles")
		}
		if _, ok := names[member.Name]; ok {
			return 0, errors.New("backup: receipt backup-set manifest has duplicate member names")
		}
		roles[member.Role] = struct{}{}
		names[member.Name] = struct{}{}
		if member.Role != expected[i].Role || member.Format != expected[i].Format ||
			member.Version != expected[i].Version || member.Name != expected[i].Name {
			return 0, errors.New("backup: receipt backup-set manifest has unknown or noncanonical members")
		}
		if !safeReceiptBackupMemberName(member.Name) {
			return 0, errors.New("backup: receipt backup-set manifest contains an unsafe member path")
		}
		switch member.Role {
		case receiptBackupSetArchiveRole:
			if member.Size == 0 || member.Size > wholeStateArchiveMaxBytes {
				return 0, errors.New("backup: receipt backup-set archive size is invalid")
			}
		case receiptBackupSetWALCutRole:
			if member.Size != receiptArchiveWALCutSize {
				return 0, errors.New("backup: receipt backup-set WAL-cut size is invalid")
			}
		default:
			return 0, errors.New("backup: receipt backup-set manifest has an unknown member")
		}
		if _, err := decodeReceiptBackupSetDigest(member.SHA256); err != nil {
			return 0, err
		}
	}
	return setID, nil
}

func validateReceiptBackupSetInstance(instance string) error {
	if instance == "" || len(instance) > receiptBackupSetInstanceMaxBytes || !utf8.ValidString(instance) {
		return errors.New("backup: receipt backup-set instance token is invalid")
	}
	return nil
}

func decodeReceiptBackupSetIdentity(value string) ([16]byte, error) {
	var identity [16]byte
	if len(value) != hex.EncodedLen(len(identity)) || strings.ToLower(value) != value {
		return identity, errors.New("invalid identity")
	}
	raw, err := hex.DecodeString(value)
	if err != nil {
		return identity, err
	}
	copy(identity[:], raw)
	return identity, nil
}

func decodeReceiptBackupSetDigest(value string) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	if len(value) != hex.EncodedLen(len(digest)) || strings.ToLower(value) != value {
		return digest, errors.New("backup: receipt backup-set member digest is malformed")
	}
	raw, err := hex.DecodeString(value)
	if err != nil {
		return digest, fmt.Errorf("backup: receipt backup-set member digest is malformed: %w", err)
	}
	copy(digest[:], raw)
	if digest == ([sha256.Size]byte{}) {
		return digest, errors.New("backup: receipt backup-set member digest is zero")
	}
	return digest, nil
}

func validateReceiptBackupSetManifestIdentity(
	manifest receiptBackupSetManifest,
	nodeID hlc.NodeID,
	generation [16]byte,
) error {
	manifestNodeID, err := decodeReceiptBackupSetIdentity(manifest.NodeID)
	if err != nil || manifestNodeID != [16]byte(nodeID) {
		return errors.New("backup: receipt backup-set manifest NodeID differs from captured runtime")
	}
	manifestGeneration, err := decodeReceiptBackupSetIdentity(manifest.Generation)
	if err != nil || manifestGeneration != generation {
		return errors.New("backup: receipt backup-set manifest generation differs from captured runtime")
	}
	return nil
}

func safeReceiptBackupMemberName(name string) bool {
	return name != "" && name != "." && name != ".." &&
		filepath.Base(name) == name && !strings.ContainsAny(name, `/\`)
}

func receiptBackupSetIDString(id uint64) string {
	return fmt.Sprintf("%0*d", receiptBackupSetCanonicalIDDigits, id)
}

func receiptBackupSetScope(instance string) string {
	sum := sha256.Sum256([]byte(instance))
	return hex.EncodeToString(sum[:])
}

func receiptBackupSetBase(instance string, id uint64) string {
	return receiptBackupSetPrefix + receiptBackupSetScope(instance) + "-" + receiptBackupSetIDString(id)
}

func (b *Backupper) loadReceiptBackupSet(manifestPath string) (loadedReceiptBackupSet, error) {
	if filepath.Dir(filepath.Clean(manifestPath)) != filepath.Clean(b.cfg.Dir) {
		return loadedReceiptBackupSet{}, errors.New("backup: receipt backup-set manifest path escapes the backup directory")
	}
	if err := b.validateReceiptBackupRegularFile(manifestPath); err != nil {
		return loadedReceiptBackupSet{}, err
	}
	raw, err := b.fs.readFile(manifestPath, receiptBackupSetManifestMaxBytes)
	if err != nil {
		return loadedReceiptBackupSet{}, fmt.Errorf("backup: read receipt backup-set manifest: %w", err)
	}
	manifest, err := decodeReceiptBackupSetManifest(raw)
	if err != nil {
		return loadedReceiptBackupSet{}, err
	}
	if manifest.Instance != b.cfg.InstanceID {
		return loadedReceiptBackupSet{}, errors.New("backup: receipt backup-set manifest belongs to another instance")
	}
	setID, err := validateReceiptBackupSetManifest(manifest)
	if err != nil {
		return loadedReceiptBackupSet{}, err
	}
	expectedManifest := receiptBackupSetBase(manifest.Instance, setID) + receiptBackupSetManifestSuffix
	if filepath.Base(manifestPath) != expectedManifest {
		return loadedReceiptBackupSet{}, errors.New("backup: receipt backup-set manifest filename is noncanonical")
	}

	memberRaw := make([][]byte, len(manifest.Members))
	memberPaths := make([]string, len(manifest.Members))
	for i, member := range manifest.Members {
		limit := int64(wholeStateArchiveMaxBytes)
		if member.Role == receiptBackupSetWALCutRole {
			limit = receiptArchiveWALCutSize
		}
		memberPaths[i] = filepath.Join(b.cfg.Dir, member.Name)
		if err := b.validateReceiptBackupRegularFile(memberPaths[i]); err != nil {
			return loadedReceiptBackupSet{}, err
		}
		memberRaw[i], err = b.fs.readFile(memberPaths[i], limit)
		if err != nil {
			return loadedReceiptBackupSet{}, fmt.Errorf("backup: read receipt backup-set member %s: %w", member.Name, err)
		}
		if uint64(len(memberRaw[i])) != member.Size {
			return loadedReceiptBackupSet{}, fmt.Errorf("backup: receipt backup-set member %s size differs", member.Name)
		}
		digest, err := decodeReceiptBackupSetDigest(member.SHA256)
		if err != nil {
			return loadedReceiptBackupSet{}, err
		}
		if sha256.Sum256(memberRaw[i]) != digest {
			return loadedReceiptBackupSet{}, fmt.Errorf("backup: receipt backup-set member %s digest differs", member.Name)
		}
	}
	archive, err := decodeWholeStateArchive(bytes.NewReader(memberRaw[0]))
	if err != nil {
		return loadedReceiptBackupSet{}, err
	}
	walCut, err := decodeReceiptArchiveWALCut(memberRaw[1])
	if err != nil {
		return loadedReceiptBackupSet{}, err
	}
	if walCut.archiveSHA256 != sha256.Sum256(memberRaw[0]) {
		return loadedReceiptBackupSet{}, errors.New("backup: receipt backup-set WAL cut binds different archive bytes")
	}
	if walCut.cutSeq != walCut.tipSeq || walCut.cutOffset != walCut.tipOffset ||
		walCut.cutSHA256 != walCut.tipSHA256 ||
		walCut.cutChainSHA256 != walCut.tipChainSHA256 {
		return loadedReceiptBackupSet{}, errors.New("backup: receipt backup-set WAL cut was not captured from one live tip")
	}
	nodeRaw, _ := decodeReceiptBackupSetIdentity(manifest.NodeID)
	var nodeID hlc.NodeID
	copy(nodeID[:], nodeRaw[:])
	generation, _ := decodeReceiptBackupSetIdentity(manifest.Generation)
	createdAt, _ := time.Parse(time.RFC3339Nano, manifest.BackupTimestamp)
	if err := validateReceiptArchiveCapturedWitness(
		archive,
		mutationlog.FileWALTipWitness{
			Seq: walCut.cutSeq, Offset: walCut.cutOffset,
			SHA256: walCut.cutSHA256, ChainSHA256: walCut.cutChainSHA256,
		},
		nodeID,
		generation,
	); err != nil {
		return loadedReceiptBackupSet{}, err
	}
	stats := receiptArchiveStats(archive)
	stats.Members = len(manifest.Members)
	stats.Bytes = int64(len(raw) + len(memberRaw[0]) + len(memberRaw[1]))
	return loadedReceiptBackupSet{
		receiptBackupSet: receiptBackupSet{
			id: setID, manifestPath: manifestPath, memberPaths: memberPaths, stats: stats,
		},
		archiveRaw: memberRaw[0], archive: archive, walCut: walCut,
		nodeID: nodeID, generation: generation, createdAt: createdAt,
	}, nil
}

func (b *Backupper) validateReceiptBackupRegularFile(path string) error {
	info, err := b.fs.lstat(path)
	if err != nil {
		return fmt.Errorf("backup: inspect receipt backup-set path %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("backup: receipt backup-set path %s is not a regular file", path)
	}
	return nil
}
