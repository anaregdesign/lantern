package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	"github.com/anaregdesign/lantern/server/service"
)

const (
	receiptBackupSetFormat               = "lantern-receipt-backup-set"
	receiptBackupSetVersion              = uint16(1)
	receiptBackupSetPrefix               = "lantern-receipt-backup-"
	receiptBackupSetArchiveSuffix        = ".active.lar"
	receiptBackupSetWALCutSuffix         = ".active.walcut"
	receiptBackupSetRetiredCatalogSuffix = ".retired.lret"
	receiptBackupSetManifestSuffix       = ".set.json"
	receiptBackupSetTempSuffix           = ".tmp"
	receiptBackupSetManifestMaxBytes     = 64 << 10
	receiptBackupSetInstanceMaxBytes     = 255
	receiptBackupSetArchiveRole          = "active_epoch_archive"
	receiptBackupSetArchiveFormat        = "lantern-receipt-whole-state"
	receiptBackupSetWALCutRole           = "active_epoch_wal_cut"
	receiptBackupSetWALCutFormat         = "lantern-receipt-wal-cut"
	receiptBackupSetRetiredCatalogRole   = "retired_receipt_catalog"
	receiptBackupSetRetiredCatalogFormat = "lantern-retired-receipt-catalog"
	receiptBackupSetCanonicalIDDigits    = 20
	receiptBackupSetFilePermissions      = 0o600
	receiptBackupSetDirectoryPerms       = 0o755
	receiptBackupSetExpectedMemberCount  = 3
	receiptBackupOriginsDigestDomain     = "lantern-receipt-backup-set/origin-cutoffs\x00"
	receiptBackupPoliciesDigestDomain    = "lantern-receipt-backup-set/retired-policies\x00"
	receiptBackupPublicationDigestDomain = "lantern-receipt-backup-set/certified-publication\x00"
)

var (
	receiptBackupSetUnsupportedPrefixes = [...]string{
		"lantern-receipt-backup-v1-",
		"lantern-receipt-backup-v2-",
	}

	// ErrReceiptBackupSetNotFound reports that no committed receipt backup-set
	// manifest exists for the requested instance.
	ErrReceiptBackupSetNotFound = errors.New("backup: receipt backup set not found")

	// ErrUnsupportedReceiptBackupSet reports an obsolete marker namespace or
	// unsupported manifest schema. No obsolete backup-set format is decoded or
	// migrated.
	ErrUnsupportedReceiptBackupSet = errors.New("backup: unsupported receipt backup-set format")
)

type receiptBackupSetMember struct {
	Role    string `json:"role"`
	Format  string `json:"format"`
	Version uint16 `json:"version"`
	Name    string `json:"name"`
	Size    uint64 `json:"size"`
	SHA256  string `json:"sha256"`
}

type receiptBackupSetPolicyMetadata struct {
	Epoch             string `json:"epoch"`
	RetentionMillis   uint64 `json:"retention_millis"`
	MaxEntries        uint64 `json:"max_entries"`
	MaxBytes          uint64 `json:"max_bytes"`
	PolicyFingerprint string `json:"policy_fingerprint_sha256"`
}

type receiptBackupSetRetiredMetadata struct {
	Format               string `json:"format"`
	Version              uint16 `json:"version"`
	ActiveEpoch          string `json:"active_epoch"`
	ClockHighWaterMillis int64  `json:"clock_high_water_millis"`
	MaxEntries           uint64 `json:"max_entries"`
	MaxBytes             uint64 `json:"max_bytes"`
	EpochCount           uint64 `json:"epoch_count"`
	ReceiptCount         uint64 `json:"receipt_count"`
	PolicySetSHA256      string `json:"policy_set_sha256"`
}

type receiptBackupSetHLCMetadata struct {
	WallNanos int64  `json:"wall_nanos"`
	Logical   uint32 `json:"logical"`
	NodeID    string `json:"node_id"`
}

type receiptBackupSetWALWitnessMetadata struct {
	Sequence    uint64 `json:"sequence"`
	Offset      uint64 `json:"offset"`
	SHA256      string `json:"sha256"`
	ChainSHA256 string `json:"chain_sha256"`
}

type receiptBackupSetCutMetadata struct {
	ReceiptClockHighWaterMillis int64                              `json:"receipt_clock_high_water_millis"`
	LocalSequence               uint64                             `json:"local_sequence"`
	SnapshotHLC                 receiptBackupSetHLCMetadata        `json:"snapshot_hlc"`
	OriginCount                 uint64                             `json:"origin_count"`
	OriginCutoffsSHA256         string                             `json:"origin_cutoffs_sha256"`
	WALCut                      receiptBackupSetWALWitnessMetadata `json:"wal_cut"`
	WALTip                      receiptBackupSetWALWitnessMetadata `json:"wal_tip"`
}

type receiptBackupSetManifest struct {
	Format            string                          `json:"format"`
	Version           uint16                          `json:"version"`
	Instance          string                          `json:"instance"`
	SetID             string                          `json:"set_id"`
	BackupTimestamp   string                          `json:"backup_timestamp"`
	NodeID            string                          `json:"node_id"`
	Generation        string                          `json:"generation"`
	PublicationSHA256 string                          `json:"publication_sha256"`
	ActivePolicy      receiptBackupSetPolicyMetadata  `json:"active_policy"`
	RetiredCatalog    receiptBackupSetRetiredMetadata `json:"retired_catalog"`
	Cut               receiptBackupSetCutMetadata     `json:"cut"`
	Members           []receiptBackupSetMember        `json:"members"`
}

type receiptBackupSetEnvelope struct {
	Format  string `json:"format"`
	Version uint16 `json:"version"`
}

type receiptBackupSet struct {
	id           uint64
	manifestPath string
	memberPaths  []string
	stats        Stats
}

type receiptBackupSetDecoded struct {
	archive         wholeStateArchive
	activeSHA256    [sha256.Size]byte
	walCut          receiptArchiveWALCut
	retired         mutationreceipt.RetiredCatalogSnapshot
	retiredMetadata retiredCatalogArchiveMetadata
}

// loadedReceiptBackupSet is the single fully validated input a later durable
// restore layer may consume. Its member bytes are owned by the value; loading
// never consults the live appendable WAL.
type loadedReceiptBackupSet struct {
	receiptBackupSet
	archiveRaw []byte
	archive    wholeStateArchive
	walCut     receiptArchiveWALCut
	retiredRaw []byte
	retired    mutationreceipt.RetiredCatalogSnapshot
	nodeID     hlc.NodeID
	generation [16]byte
	createdAt  time.Time
}

// ReceiptBackupSetEvidence is the immutable, fully validated input for a later
// durable restore layer. Archive and RetiredCatalog own their bytes. WALCut is
// the exact closed FileWAL witness captured with the same active Store,
// retired catalog, NodeID, and generation-chain head.
type ReceiptBackupSetEvidence struct {
	SetID           uint64
	BackupTimestamp time.Time
	NodeID          hlc.NodeID
	Generation      [16]byte
	WALCut          mutationlog.FileWALTipWitness
	Stats           Stats
	Archive         []byte
	RetiredCatalog  []byte
}

// LoadReceiptBackupSet validates and loads one committed receipt backup set
// without consulting the live appendable WAL.
func LoadReceiptBackupSet(
	dir, instance, manifestPath string,
) (ReceiptBackupSetEvidence, error) {
	if err := validateReceiptBackupSetInstance(instance); err != nil {
		return ReceiptBackupSetEvidence{}, err
	}
	b := &Backupper{
		cfg: Config{Dir: dir, InstanceID: instance},
		fs:  newReceiptBackupFS(),
	}
	loaded, err := b.loadReceiptBackupSet(manifestPath)
	if err != nil {
		return ReceiptBackupSetEvidence{}, err
	}
	return receiptBackupSetEvidence(loaded), nil
}

// LoadLatestReceiptBackupSet loads the highest-ID committed receipt backup set
// recognized for instance. Once selected, an invalid newest set fails closed;
// the loader never falls back to an older set.
func LoadLatestReceiptBackupSet(dir, instance string) (ReceiptBackupSetEvidence, error) {
	if err := validateReceiptBackupSetInstance(instance); err != nil {
		return ReceiptBackupSetEvidence{}, err
	}
	b := &Backupper{
		cfg: Config{Dir: dir, InstanceID: instance},
		fs:  newReceiptBackupFS(),
	}
	loaded, err := b.loadLatestReceiptBackupSet()
	if err != nil {
		return ReceiptBackupSetEvidence{}, err
	}
	return receiptBackupSetEvidence(loaded), nil
}

func receiptBackupSetEvidence(loaded loadedReceiptBackupSet) ReceiptBackupSetEvidence {
	return ReceiptBackupSetEvidence{
		SetID:           loaded.id,
		BackupTimestamp: loaded.createdAt,
		NodeID:          loaded.nodeID,
		Generation:      loaded.generation,
		WALCut: mutationlog.FileWALTipWitness{
			Seq:         loaded.walCut.cutSeq,
			Offset:      loaded.walCut.cutOffset,
			SHA256:      loaded.walCut.cutSHA256,
			ChainSHA256: loaded.walCut.cutChainSHA256,
		},
		Stats:          loaded.stats,
		Archive:        bytes.Clone(loaded.archiveRaw),
		RetiredCatalog: bytes.Clone(loaded.retiredRaw),
	}
}

func (b *Backupper) loadLatestReceiptBackupSet() (loadedReceiptBackupSet, error) {
	var selectedID uint64
	var selectedName string
	var duplicateName string
	var selectedUnsupported bool
	var unsupportedName string
	err := b.scanReceiptBackupDirectory(func(entry os.DirEntry) error {
		id, _, kind, ok := parseOwnReceiptBackupSetName(entry.Name(), b.cfg.InstanceID)
		if !ok || (kind != receiptBackupSetManifestFile &&
			kind != receiptBackupSetUnsupportedManifestFile) {
			return nil
		}
		if id > selectedID {
			selectedID = id
			selectedName = entry.Name()
			duplicateName = ""
			selectedUnsupported = kind == receiptBackupSetUnsupportedManifestFile
			unsupportedName = ""
			if selectedUnsupported {
				unsupportedName = entry.Name()
			}
			return nil
		}
		if id == selectedID {
			selectedUnsupported = selectedUnsupported ||
				kind == receiptBackupSetUnsupportedManifestFile
			if kind == receiptBackupSetUnsupportedManifestFile &&
				(unsupportedName == "" || entry.Name() < unsupportedName) {
				unsupportedName = entry.Name()
			}
			if entry.Name() < selectedName {
				duplicateName = selectedName
				selectedName = entry.Name()
			} else if duplicateName == "" || entry.Name() < duplicateName {
				duplicateName = entry.Name()
			}
		}
		return nil
	})
	if errors.Is(err, os.ErrNotExist) {
		return loadedReceiptBackupSet{}, ErrReceiptBackupSetNotFound
	}
	if err != nil {
		return loadedReceiptBackupSet{}, fmt.Errorf(
			"backup: scan receipt backup directory %s: %w",
			b.cfg.Dir,
			err,
		)
	}
	if selectedName == "" {
		return loadedReceiptBackupSet{}, ErrReceiptBackupSetNotFound
	}

	selectedPath := filepath.Join(b.cfg.Dir, selectedName)
	if selectedUnsupported {
		return loadedReceiptBackupSet{}, fmt.Errorf(
			"%w: obsolete receipt backup-set marker %s",
			ErrUnsupportedReceiptBackupSet,
			filepath.Join(b.cfg.Dir, unsupportedName),
		)
	}
	if duplicateName != "" {
		return loadedReceiptBackupSet{}, fmt.Errorf(
			"backup: duplicate receipt backup-set manifest ID %s in %q and %q",
			receiptBackupSetIDString(selectedID),
			selectedName,
			duplicateName,
		)
	}
	loaded, err := b.loadReceiptBackupSet(selectedPath)
	if err != nil {
		return loadedReceiptBackupSet{}, fmt.Errorf(
			"backup: load latest receipt backup set %s: %w",
			selectedPath,
			err,
		)
	}
	if loaded.id != selectedID {
		return loadedReceiptBackupSet{}, errors.New(
			"backup: latest receipt backup-set filename and manifest IDs differ",
		)
	}
	return loaded, nil
}

func newReceiptBackupSetManifest(
	instance string,
	setID uint64,
	at time.Time,
	product receiptBackupSetProduct,
	activeRaw, walCutRaw, retiredRaw []byte,
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
	decoded, err := decodeReceiptBackupSetMembers(activeRaw, walCutRaw, retiredRaw)
	if err != nil {
		return receiptBackupSetManifest{}, err
	}
	return buildReceiptBackupSetManifest(
		instance,
		setID,
		at,
		product.nodeID,
		product.generation,
		activeRaw,
		walCutRaw,
		retiredRaw,
		decoded,
	)
}

func buildReceiptBackupSetManifest(
	instance string,
	setID uint64,
	at time.Time,
	nodeID hlc.NodeID,
	generation [16]byte,
	activeRaw, walCutRaw, retiredRaw []byte,
	decoded receiptBackupSetDecoded,
) (receiptBackupSetManifest, error) {
	if err := validateReceiptBackupSetCut(decoded, nodeID, generation); err != nil {
		return receiptBackupSetManifest{}, err
	}
	header := decoded.archive.Graph[0].GetHeader()
	cutoff, ok := archiveHLC(header.GetCutoffHlc())
	if !ok {
		return receiptBackupSetManifest{}, wholeStateArchiveError("archive cutoff HLC is invalid")
	}
	retiredReceipts, err := receiptBackupRetiredReceiptCount(decoded.retired)
	if err != nil {
		return receiptBackupSetManifest{}, err
	}
	activeDigest := sha256.Sum256(activeRaw)
	walCutDigest := sha256.Sum256(walCutRaw)
	retiredDigest := sha256.Sum256(retiredRaw)
	originDigest := receiptBackupOriginCutoffsDigest(decoded.archive.Origins)
	retiredPolicyDigest := receiptBackupRetiredPoliciesDigest(decoded.retired)
	base := receiptBackupSetBase(instance, setID)
	manifest := receiptBackupSetManifest{
		Format:          receiptBackupSetFormat,
		Version:         receiptBackupSetVersion,
		Instance:        instance,
		SetID:           receiptBackupSetIDString(setID),
		BackupTimestamp: at.UTC().Format(time.RFC3339Nano),
		NodeID:          hex.EncodeToString(nodeID[:]),
		Generation:      hex.EncodeToString(generation[:]),
		ActivePolicy: receiptBackupSetPolicyMetadata{
			Epoch:             hex.EncodeToString(decoded.archive.Policy.Epoch[:]),
			RetentionMillis:   uint64(decoded.archive.Policy.Retention / time.Millisecond),
			MaxEntries:        uint64(decoded.archive.Policy.MaxEntries),
			MaxBytes:          decoded.archive.Policy.MaxBytes,
			PolicyFingerprint: hex.EncodeToString(decoded.archive.Receipts.PolicyFingerprint[:]),
		},
		RetiredCatalog: receiptBackupSetRetiredMetadata{
			Format:               receiptBackupSetRetiredCatalogFormat,
			Version:              retiredCatalogArchiveVersion,
			ActiveEpoch:          hex.EncodeToString(decoded.retiredMetadata.ActiveEpoch[:]),
			ClockHighWaterMillis: decoded.retiredMetadata.ClockHighWaterMillis,
			MaxEntries:           uint64(decoded.retiredMetadata.MaxEntries),
			MaxBytes:             decoded.retiredMetadata.MaxBytes,
			EpochCount:           uint64(len(decoded.retired.Epochs)),
			ReceiptCount:         retiredReceipts,
			PolicySetSHA256:      hex.EncodeToString(retiredPolicyDigest[:]),
		},
		Cut: receiptBackupSetCutMetadata{
			ReceiptClockHighWaterMillis: decoded.archive.Receipts.ClockHighWaterMillis,
			LocalSequence:               header.GetCutoffLocalSeq(),
			SnapshotHLC: receiptBackupSetHLCMetadata{
				WallNanos: cutoff.WallNs,
				Logical:   cutoff.Logical,
				NodeID:    hex.EncodeToString(cutoff.NodeID[:]),
			},
			OriginCount:         uint64(len(decoded.archive.Origins)),
			OriginCutoffsSHA256: hex.EncodeToString(originDigest[:]),
			WALCut:              receiptBackupWALWitnessMetadata(decoded.walCut, false),
			WALTip:              receiptBackupWALWitnessMetadata(decoded.walCut, true),
		},
		Members: []receiptBackupSetMember{
			{
				Role: receiptBackupSetArchiveRole, Format: receiptBackupSetArchiveFormat,
				Version: wholeStateArchiveVersion, Name: base + receiptBackupSetArchiveSuffix,
				Size: uint64(len(activeRaw)), SHA256: hex.EncodeToString(activeDigest[:]),
			},
			{
				Role: receiptBackupSetWALCutRole, Format: receiptBackupSetWALCutFormat,
				Version: receiptArchiveWALCutVersion, Name: base + receiptBackupSetWALCutSuffix,
				Size: uint64(len(walCutRaw)), SHA256: hex.EncodeToString(walCutDigest[:]),
			},
			{
				Role: receiptBackupSetRetiredCatalogRole, Format: receiptBackupSetRetiredCatalogFormat,
				Version: retiredCatalogArchiveVersion, Name: base + receiptBackupSetRetiredCatalogSuffix,
				Size: uint64(len(retiredRaw)), SHA256: hex.EncodeToString(retiredDigest[:]),
			},
		},
	}
	publicationDigest, err := receiptBackupPublicationDigest(manifest)
	if err != nil {
		return receiptBackupSetManifest{}, err
	}
	manifest.PublicationSHA256 = hex.EncodeToString(publicationDigest[:])
	return manifest, nil
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
	var envelope receiptBackupSetEnvelope
	envelopeDecoder := json.NewDecoder(bytes.NewReader(raw))
	if err := envelopeDecoder.Decode(&envelope); err != nil {
		return receiptBackupSetManifest{}, fmt.Errorf("backup: decode receipt backup-set manifest envelope: %w", err)
	}
	var envelopeTrailing any
	if err := envelopeDecoder.Decode(&envelopeTrailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return receiptBackupSetManifest{}, errors.New("backup: receipt backup-set manifest has trailing data")
		}
		return receiptBackupSetManifest{}, fmt.Errorf("backup: decode receipt backup-set manifest envelope trailer: %w", err)
	}
	if envelope.Format != receiptBackupSetFormat || envelope.Version != receiptBackupSetVersion {
		return receiptBackupSetManifest{}, fmt.Errorf(
			"%w: format=%q version=%d",
			ErrUnsupportedReceiptBackupSet,
			envelope.Format,
			envelope.Version,
		)
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
		return 0, fmt.Errorf("%w: format=%q version=%d", ErrUnsupportedReceiptBackupSet, manifest.Format, manifest.Version)
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
	if err := validateReceiptBackupSetPolicyMetadata(manifest.ActivePolicy); err != nil {
		return 0, err
	}
	if err := validateReceiptBackupSetRetiredMetadata(manifest.RetiredCatalog, manifest.ActivePolicy, manifest.Cut); err != nil {
		return 0, err
	}
	if err := validateReceiptBackupSetCutMetadata(manifest.Cut, manifest.NodeID); err != nil {
		return 0, err
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
		{
			Role: receiptBackupSetRetiredCatalogRole, Format: receiptBackupSetRetiredCatalogFormat,
			Version: retiredCatalogArchiveVersion, Name: base + receiptBackupSetRetiredCatalogSuffix,
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
		case receiptBackupSetArchiveRole, receiptBackupSetRetiredCatalogRole:
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
	publicationDigest, err := receiptBackupPublicationDigest(manifest)
	if err != nil {
		return 0, err
	}
	declaredPublicationDigest, err := decodeReceiptBackupSetDigest(manifest.PublicationSHA256)
	if err != nil || publicationDigest != declaredPublicationDigest {
		return 0, errors.New("backup: receipt backup-set publication commitment differs")
	}
	return setID, nil
}

func validateReceiptBackupSetPolicyMetadata(policy receiptBackupSetPolicyMetadata) error {
	epoch, err := decodeReceiptBackupSetIdentity(policy.Epoch)
	if err != nil || epoch == ([16]byte{}) {
		return errors.New("backup: receipt backup-set active epoch is malformed")
	}
	if policy.RetentionMillis == 0 || policy.RetentionMillis > uint64(math.MaxInt64/int64(time.Millisecond)) ||
		policy.MaxEntries == 0 || policy.MaxEntries > uint64(^uint(0)>>1) || policy.MaxBytes == 0 {
		return errors.New("backup: receipt backup-set active policy is invalid")
	}
	fingerprint, err := decodeReceiptBackupSetDigest(policy.PolicyFingerprint)
	if err != nil {
		return errors.New("backup: receipt backup-set active policy fingerprint is malformed")
	}
	var activeEpoch mutationreceipt.Epoch
	copy(activeEpoch[:], epoch[:])
	store, err := mutationreceipt.New(mutationreceipt.Config{
		Epoch:      activeEpoch,
		Retention:  time.Duration(policy.RetentionMillis) * time.Millisecond,
		MaxEntries: int(policy.MaxEntries),
		MaxBytes:   policy.MaxBytes,
	})
	if err != nil || store.PolicyFingerprint() != fingerprint {
		return errors.New("backup: receipt backup-set active policy fingerprint differs")
	}
	return nil
}

func validateReceiptBackupSetRetiredMetadata(
	retired receiptBackupSetRetiredMetadata,
	active receiptBackupSetPolicyMetadata,
	cut receiptBackupSetCutMetadata,
) error {
	if retired.Format != receiptBackupSetRetiredCatalogFormat ||
		retired.Version != retiredCatalogArchiveVersion {
		return errors.New("backup: receipt backup-set retired catalog format is unsupported")
	}
	if retired.ActiveEpoch != active.Epoch ||
		retired.ClockHighWaterMillis != cut.ReceiptClockHighWaterMillis ||
		retired.MaxEntries != active.MaxEntries ||
		retired.MaxBytes != active.MaxBytes {
		return errors.New("backup: receipt backup-set retired catalog metadata differs from active cut")
	}
	if retired.EpochCount > retired.MaxEntries || retired.ReceiptCount > retired.MaxEntries ||
		retired.EpochCount > uint64(wholeStateArchiveMaxBytes) ||
		retired.ReceiptCount > uint64(wholeStateArchiveMaxBytes) ||
		(retired.EpochCount == 0) != (retired.ReceiptCount == 0) {
		return errors.New("backup: receipt backup-set retired catalog counts are invalid")
	}
	if _, err := decodeReceiptBackupSetDigest(retired.PolicySetSHA256); err != nil {
		return errors.New("backup: receipt backup-set retired policy commitment is malformed")
	}
	return nil
}

func validateReceiptBackupSetCutMetadata(cut receiptBackupSetCutMetadata, nodeID string) error {
	if cut.ReceiptClockHighWaterMillis < 0 ||
		cut.SnapshotHLC.WallNanos <= 0 ||
		cut.SnapshotHLC.NodeID != nodeID ||
		cut.ReceiptClockHighWaterMillis > cut.SnapshotHLC.WallNanos/int64(time.Millisecond) ||
		cut.OriginCount > uint64(wholeStateArchiveMaxBytes) {
		return errors.New("backup: receipt backup-set cut metadata is invalid")
	}
	if _, err := decodeReceiptBackupSetIdentity(cut.SnapshotHLC.NodeID); err != nil {
		return errors.New("backup: receipt backup-set cutoff HLC NodeID is malformed")
	}
	if _, err := decodeReceiptBackupSetDigest(cut.OriginCutoffsSHA256); err != nil {
		return errors.New("backup: receipt backup-set origin-cutoff commitment is malformed")
	}
	walCut, err := receiptBackupWALWitness(cut.WALCut)
	if err != nil {
		return err
	}
	walTip, err := receiptBackupWALWitness(cut.WALTip)
	if err != nil {
		return err
	}
	if walCut != walTip || cut.LocalSequence != walCut.Seq {
		return errors.New("backup: receipt backup-set WAL cut is not the captured live tip")
	}
	return nil
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
		return digest, errors.New("backup: receipt backup-set digest is malformed")
	}
	raw, err := hex.DecodeString(value)
	if err != nil {
		return digest, fmt.Errorf("backup: receipt backup-set digest is malformed: %w", err)
	}
	copy(digest[:], raw)
	if digest == ([sha256.Size]byte{}) {
		return digest, errors.New("backup: receipt backup-set digest is zero")
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

func validateReceiptBackupSetPublication(
	manifest receiptBackupSetManifest,
	manifestRaw, activeRaw, walCutRaw, retiredRaw []byte,
	nodeID hlc.NodeID,
	generation [16]byte,
) (uint64, error) {
	setID, err := validateReceiptBackupSetManifest(manifest)
	if err != nil {
		return 0, err
	}
	if err := validateReceiptBackupSetManifestIdentity(manifest, nodeID, generation); err != nil {
		return 0, err
	}
	canonicalManifest, err := encodeReceiptBackupSetManifest(manifest)
	if err != nil {
		return 0, err
	}
	if !bytes.Equal(canonicalManifest, manifestRaw) {
		return 0, errors.New("backup: receipt backup-set manifest bytes differ")
	}
	decoded, err := decodeReceiptBackupSetMembers(activeRaw, walCutRaw, retiredRaw)
	if err != nil {
		return 0, err
	}
	createdAt, err := time.Parse(time.RFC3339Nano, manifest.BackupTimestamp)
	if err != nil {
		return 0, errors.New("backup: receipt backup-set manifest timestamp differs")
	}
	expected, err := buildReceiptBackupSetManifest(
		manifest.Instance,
		setID,
		createdAt,
		nodeID,
		generation,
		activeRaw,
		walCutRaw,
		retiredRaw,
		decoded,
	)
	if err != nil {
		return 0, err
	}
	if !reflect.DeepEqual(manifest, expected) {
		return 0, errors.New("backup: receipt backup-set manifest metadata differs from its members")
	}
	return setID, nil
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

func decodeReceiptBackupSetMembers(
	activeRaw, walCutRaw, retiredRaw []byte,
) (receiptBackupSetDecoded, error) {
	if len(activeRaw) == 0 || len(activeRaw) > wholeStateArchiveMaxBytes ||
		len(walCutRaw) != receiptArchiveWALCutSize ||
		len(retiredRaw) == 0 || len(retiredRaw) > wholeStateArchiveMaxBytes {
		return receiptBackupSetDecoded{}, errors.New("backup: receipt backup-set member size is invalid")
	}
	archive, err := decodeWholeStateArchive(bytes.NewReader(activeRaw))
	if err != nil {
		return receiptBackupSetDecoded{}, err
	}
	var canonicalActive bytes.Buffer
	if err := encodeWholeStateArchive(&canonicalActive, archive); err != nil {
		return receiptBackupSetDecoded{}, err
	}
	if !bytes.Equal(canonicalActive.Bytes(), activeRaw) {
		return receiptBackupSetDecoded{}, errors.New("backup: active receipt archive is not canonical")
	}
	walCut, err := decodeReceiptArchiveWALCut(walCutRaw)
	if err != nil {
		if errors.Is(err, errUnsupportedReceiptArchiveWALCut) {
			return receiptBackupSetDecoded{}, fmt.Errorf(
				"%w: obsolete WAL-cut member",
				ErrUnsupportedReceiptBackupSet,
			)
		}
		return receiptBackupSetDecoded{}, err
	}
	retired, retiredMetadata, err := decodeRetiredCatalogArchive(retiredRaw)
	if err != nil {
		return receiptBackupSetDecoded{}, err
	}
	canonicalRetired, err := encodeRetiredCatalogArchiveWithoutPolicy(retiredMetadata, retired)
	if err != nil {
		return receiptBackupSetDecoded{}, err
	}
	if !bytes.Equal(canonicalRetired, retiredRaw) {
		return receiptBackupSetDecoded{}, errors.New("backup: retired receipt catalog is not canonical")
	}
	return receiptBackupSetDecoded{
		archive: archive, activeSHA256: sha256.Sum256(activeRaw),
		walCut: walCut, retired: retired, retiredMetadata: retiredMetadata,
	}, nil
}

func validateReceiptBackupSetCut(
	decoded receiptBackupSetDecoded,
	nodeID hlc.NodeID,
	generation [16]byte,
) error {
	if decoded.walCut.archiveSHA256 != decoded.activeSHA256 {
		return errors.New("backup: receipt backup-set WAL cut binds different archive bytes")
	}
	if decoded.walCut.cutSeq != decoded.walCut.tipSeq ||
		decoded.walCut.cutOffset != decoded.walCut.tipOffset ||
		decoded.walCut.cutSHA256 != decoded.walCut.tipSHA256 ||
		decoded.walCut.cutChainSHA256 != decoded.walCut.tipChainSHA256 {
		return errors.New("backup: receipt backup-set WAL cut was not captured from one live tip")
	}
	if decoded.retiredMetadata.ActiveEpoch != decoded.archive.Policy.Epoch ||
		decoded.retiredMetadata.ClockHighWaterMillis != decoded.archive.Receipts.ClockHighWaterMillis ||
		decoded.retiredMetadata.MaxEntries != decoded.archive.Policy.MaxEntries ||
		decoded.retiredMetadata.MaxBytes != decoded.archive.Policy.MaxBytes ||
		decoded.retired.ClockHighWaterMillis != decoded.archive.Receipts.ClockHighWaterMillis {
		return errors.New("backup: active and retired receipt cuts differ")
	}
	return validateReceiptArchiveCapturedWitness(
		decoded.archive,
		mutationlog.FileWALTipWitness{
			Seq: decoded.walCut.cutSeq, Offset: decoded.walCut.cutOffset,
			SHA256: decoded.walCut.cutSHA256, ChainSHA256: decoded.walCut.cutChainSHA256,
		},
		nodeID,
		generation,
	)
}

func receiptBackupWALWitnessMetadata(
	walCut receiptArchiveWALCut,
	tip bool,
) receiptBackupSetWALWitnessMetadata {
	if tip {
		return receiptBackupSetWALWitnessMetadata{
			Sequence: walCut.tipSeq, Offset: uint64(walCut.tipOffset),
			SHA256:      hex.EncodeToString(walCut.tipSHA256[:]),
			ChainSHA256: hex.EncodeToString(walCut.tipChainSHA256[:]),
		}
	}
	return receiptBackupSetWALWitnessMetadata{
		Sequence: walCut.cutSeq, Offset: uint64(walCut.cutOffset),
		SHA256:      hex.EncodeToString(walCut.cutSHA256[:]),
		ChainSHA256: hex.EncodeToString(walCut.cutChainSHA256[:]),
	}
}

func receiptBackupWALWitness(metadata receiptBackupSetWALWitnessMetadata) (mutationlog.FileWALTipWitness, error) {
	if metadata.Offset > math.MaxInt64 {
		return mutationlog.FileWALTipWitness{}, errors.New("backup: receipt backup-set WAL offset overflows int64")
	}
	digest, err := decodeReceiptBackupSetDigest(metadata.SHA256)
	if err != nil {
		return mutationlog.FileWALTipWitness{}, errors.New("backup: receipt backup-set WAL digest is malformed")
	}
	chain, err := decodeReceiptBackupSetDigest(metadata.ChainSHA256)
	if err != nil {
		return mutationlog.FileWALTipWitness{}, errors.New("backup: receipt backup-set WAL chain digest is malformed")
	}
	witness := mutationlog.FileWALTipWitness{
		Seq: metadata.Sequence, Offset: int64(metadata.Offset), SHA256: digest, ChainSHA256: chain,
	}
	if err := validateReceiptArchiveWALWitness(
		"manifest",
		witness.Seq,
		witness.Offset,
		witness.SHA256,
		witness.ChainSHA256,
	); err != nil {
		return mutationlog.FileWALTipWitness{}, err
	}
	return witness, nil
}

func receiptBackupOriginCutoffsDigest(origins []service.OriginState) [sha256.Size]byte {
	var payload bytes.Buffer
	payload.WriteString(receiptBackupOriginsDigestDomain)
	writeArchiveU64(&payload, uint64(len(origins)))
	for _, origin := range origins {
		payload.Write(encodeArchiveOrigin(origin))
	}
	return sha256.Sum256(payload.Bytes())
}

func receiptBackupRetiredPoliciesDigest(
	retired mutationreceipt.RetiredCatalogSnapshot,
) [sha256.Size]byte {
	var payload bytes.Buffer
	payload.WriteString(receiptBackupPoliciesDigestDomain)
	writeArchiveU64(&payload, uint64(len(retired.Epochs)))
	for _, member := range retired.Epochs {
		payload.Write(member.Policy.Epoch[:])
		writeArchiveU64(&payload, uint64(member.Policy.Retention/time.Millisecond))
		writeArchiveU64(&payload, uint64(member.Policy.MaxEntries))
		writeArchiveU64(&payload, member.Policy.MaxBytes)
		payload.Write(member.State.PolicyFingerprint[:])
		writeArchiveU64(&payload, uint64(len(member.State.Receipts)))
	}
	return sha256.Sum256(payload.Bytes())
}

func receiptBackupRetiredReceiptCount(
	retired mutationreceipt.RetiredCatalogSnapshot,
) (uint64, error) {
	var count uint64
	for _, member := range retired.Epochs {
		if uint64(len(member.State.Receipts)) > math.MaxUint64-count {
			return 0, errors.New("backup: retired receipt count overflows uint64")
		}
		count += uint64(len(member.State.Receipts))
	}
	return count, nil
}

func receiptBackupPublicationDigest(
	manifest receiptBackupSetManifest,
) ([sha256.Size]byte, error) {
	var zero [sha256.Size]byte
	nodeID, err := decodeReceiptBackupSetIdentity(manifest.NodeID)
	if err != nil {
		return zero, errors.New("backup: receipt backup-set generation NodeID is malformed")
	}
	generation, err := decodeReceiptBackupSetIdentity(manifest.Generation)
	if err != nil {
		return zero, errors.New("backup: receipt backup-set generation is malformed")
	}
	epoch, err := decodeReceiptBackupSetIdentity(manifest.ActivePolicy.Epoch)
	if err != nil {
		return zero, errors.New("backup: receipt backup-set generation epoch is malformed")
	}
	policy, err := decodeReceiptBackupSetDigest(manifest.ActivePolicy.PolicyFingerprint)
	if err != nil {
		return zero, err
	}
	origins, err := decodeReceiptBackupSetDigest(manifest.Cut.OriginCutoffsSHA256)
	if err != nil {
		return zero, err
	}
	retiredPolicies, err := decodeReceiptBackupSetDigest(manifest.RetiredCatalog.PolicySetSHA256)
	if err != nil {
		return zero, err
	}
	walCut, err := receiptBackupWALWitness(manifest.Cut.WALCut)
	if err != nil {
		return zero, err
	}
	walTip, err := receiptBackupWALWitness(manifest.Cut.WALTip)
	if err != nil {
		return zero, err
	}
	var payload bytes.Buffer
	payload.WriteString(receiptBackupPublicationDigestDomain)
	writeReceiptBackupDigestString(&payload, manifest.Format)
	writeArchiveU16(&payload, manifest.Version)
	writeReceiptBackupDigestString(&payload, manifest.Instance)
	writeReceiptBackupDigestString(&payload, manifest.SetID)
	writeReceiptBackupDigestString(&payload, manifest.BackupTimestamp)
	payload.Write(nodeID[:])
	payload.Write(generation[:])
	payload.Write(epoch[:])
	writeArchiveU64(&payload, manifest.ActivePolicy.RetentionMillis)
	writeArchiveU64(&payload, manifest.ActivePolicy.MaxEntries)
	writeArchiveU64(&payload, manifest.ActivePolicy.MaxBytes)
	payload.Write(policy[:])
	writeReceiptBackupDigestString(&payload, manifest.RetiredCatalog.Format)
	writeArchiveU16(&payload, manifest.RetiredCatalog.Version)
	writeReceiptBackupDigestString(&payload, manifest.RetiredCatalog.ActiveEpoch)
	writeArchiveU64(&payload, uint64(manifest.RetiredCatalog.ClockHighWaterMillis))
	writeArchiveU64(&payload, manifest.RetiredCatalog.MaxEntries)
	writeArchiveU64(&payload, manifest.RetiredCatalog.MaxBytes)
	writeArchiveU64(&payload, manifest.RetiredCatalog.EpochCount)
	writeArchiveU64(&payload, manifest.RetiredCatalog.ReceiptCount)
	payload.Write(retiredPolicies[:])
	writeArchiveU64(&payload, uint64(manifest.Cut.ReceiptClockHighWaterMillis))
	writeArchiveU64(&payload, manifest.Cut.LocalSequence)
	writeArchiveU64(&payload, uint64(manifest.Cut.SnapshotHLC.WallNanos))
	writeArchiveU32(&payload, manifest.Cut.SnapshotHLC.Logical)
	writeReceiptBackupDigestString(&payload, manifest.Cut.SnapshotHLC.NodeID)
	writeArchiveU64(&payload, manifest.Cut.OriginCount)
	payload.Write(origins[:])
	writeReceiptBackupWALWitnessDigest(&payload, walCut)
	writeReceiptBackupWALWitnessDigest(&payload, walTip)
	writeArchiveU64(&payload, uint64(len(manifest.Members)))
	for _, member := range manifest.Members {
		memberDigest, err := decodeReceiptBackupSetDigest(member.SHA256)
		if err != nil {
			return zero, err
		}
		writeReceiptBackupDigestString(&payload, member.Role)
		writeReceiptBackupDigestString(&payload, member.Format)
		writeArchiveU16(&payload, member.Version)
		writeReceiptBackupDigestString(&payload, member.Name)
		writeArchiveU64(&payload, member.Size)
		payload.Write(memberDigest[:])
	}
	return sha256.Sum256(payload.Bytes()), nil
}

func writeReceiptBackupDigestString(payload *bytes.Buffer, value string) {
	writeArchiveU64(payload, uint64(len(value)))
	payload.WriteString(value)
}

func writeReceiptBackupWALWitnessDigest(
	payload *bytes.Buffer,
	witness mutationlog.FileWALTipWitness,
) {
	writeArchiveU64(payload, witness.Seq)
	writeArchiveU64(payload, uint64(witness.Offset))
	payload.Write(witness.SHA256[:])
	payload.Write(witness.ChainSHA256[:])
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
	if _, _, kind, ok := parseOwnReceiptBackupSetName(
		filepath.Base(manifestPath),
		b.cfg.InstanceID,
	); ok && kind == receiptBackupSetUnsupportedManifestFile {
		return loadedReceiptBackupSet{}, fmt.Errorf(
			"%w: obsolete receipt backup-set marker %s",
			ErrUnsupportedReceiptBackupSet,
			manifestPath,
		)
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
	expectedManifestName := receiptBackupSetBase(manifest.Instance, setID) + receiptBackupSetManifestSuffix
	if filepath.Base(manifestPath) != expectedManifestName {
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
	decoded, err := decodeReceiptBackupSetMembers(memberRaw[0], memberRaw[1], memberRaw[2])
	if err != nil {
		return loadedReceiptBackupSet{}, err
	}
	nodeRaw, _ := decodeReceiptBackupSetIdentity(manifest.NodeID)
	var nodeID hlc.NodeID
	copy(nodeID[:], nodeRaw[:])
	generation, _ := decodeReceiptBackupSetIdentity(manifest.Generation)
	createdAt, _ := time.Parse(time.RFC3339Nano, manifest.BackupTimestamp)
	if err := validateReceiptBackupSetCut(decoded, nodeID, generation); err != nil {
		return loadedReceiptBackupSet{}, err
	}
	expectedManifest, err := buildReceiptBackupSetManifest(
		manifest.Instance,
		setID,
		createdAt,
		nodeID,
		generation,
		memberRaw[0],
		memberRaw[1],
		memberRaw[2],
		decoded,
	)
	if err != nil {
		return loadedReceiptBackupSet{}, err
	}
	if !reflect.DeepEqual(manifest, expectedManifest) {
		return loadedReceiptBackupSet{}, errors.New("backup: receipt backup-set manifest metadata differs from its members")
	}
	stats := receiptArchiveStats(decoded.archive)
	retiredReceipts, err := receiptBackupRetiredReceiptCount(decoded.retired)
	if err != nil || retiredReceipts > uint64(^uint(0)>>1)-uint64(stats.Receipts) {
		return loadedReceiptBackupSet{}, errors.New("backup: receipt backup-set receipt count overflows int")
	}
	stats.Receipts += int(retiredReceipts)
	stats.Members = len(manifest.Members)
	stats.Bytes = int64(len(raw) + len(memberRaw[0]) + len(memberRaw[1]) + len(memberRaw[2]))
	return loadedReceiptBackupSet{
		receiptBackupSet: receiptBackupSet{
			id: setID, manifestPath: manifestPath, memberPaths: memberPaths, stats: stats,
		},
		archiveRaw: memberRaw[0], archive: decoded.archive, walCut: decoded.walCut,
		retiredRaw: memberRaw[2], retired: decoded.retired,
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
