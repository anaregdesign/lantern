package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
)

func archiveWALStringEncode(op mutationlog.MutationOp) ([]byte, error) {
	value, ok := op.(string)
	if !ok {
		return nil, errors.New("want string WAL payload")
	}
	return []byte(value), nil
}

func archiveWALStringDecode(raw []byte) (mutationlog.MutationOp, error) {
	if len(raw) == 0 || string(raw) == "broken" {
		return nil, errors.New("invalid WAL payload")
	}
	return string(raw), nil
}

func archiveWALValidEntry(entry mutationlog.Entry) error {
	if entry.HLC.WallNs != int64(entry.Seq) || entry.HLC.NodeID != (hlc.NodeID{1}) {
		return errors.New("WAL frame HLC or origin differs from payload contract")
	}
	return nil
}

func archiveWALFixture(t *testing.T, cutSeq, tailSeq uint64) ([]byte, string) {
	t.Helper()
	archive := wholeStateArchiveFixture(t)
	archive.Graph[0].GetHeader().CutoffLocalSeq = cutSeq
	raw := encodedWholeStateArchive(t, archive)
	path := filepath.Join(t.TempDir(), "mutations.wal")
	w, err := mutationlog.CreateFileWAL(path, archiveWALStringEncode)
	if err != nil {
		t.Fatal(err)
	}
	for seq := uint64(1); seq <= tailSeq; seq++ {
		entry := mutationlog.Entry{Seq: seq, HLC: hlc.Timestamp{WallNs: int64(seq), NodeID: hlc.NodeID{1}}, Op: "payload"}
		if err := w.Write(entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return raw, path
}

func stageBoundArchive(t *testing.T, raw, manifest []byte, path string) (*receiptWholeStateStage, error) {
	t.Helper()
	archive, err := decodeWholeStateArchive(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	return stageReceiptWholeStateArchiveAtWALCut(context.Background(), raw, manifest, path,
		archiveWALStringDecode, archiveWALValidEntry, archive.Policy, time.Hour, nil)
}

func TestReceiptArchiveWALCutBindsExactArchiveAndFilePrefix(t *testing.T) {
	for _, tc := range []struct {
		cut  uint64
		tail uint64
	}{
		{0, 0},
		{0, 2},
		{1, 2},
		{2, 2},
	} {
		raw, path := archiveWALFixture(t, tc.cut, tc.tail)
		before, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := bindReceiptArchiveFileWAL(raw, path, archiveWALStringDecode, archiveWALValidEntry)
		if err != nil {
			t.Fatalf("cut %d tail %d: %v", tc.cut, tc.tail, err)
		}
		again, err := bindReceiptArchiveFileWAL(raw, path, archiveWALStringDecode, archiveWALValidEntry)
		if err != nil || !bytes.Equal(manifest, again) {
			t.Fatalf("manifest is not deterministic: %v", err)
		}
		bound, err := decodeReceiptArchiveWALCut(manifest)
		if err != nil || bound.archiveSHA256 != sha256.Sum256(raw) || bound.localSeq != tc.cut {
			t.Fatalf("decoded manifest = %+v, %v", bound, err)
		}
		if tc.cut == 0 && (bound.walOffset != 8 || bound.walSHA256 != sha256.Sum256(before[:8])) {
			t.Fatalf("zero cut does not bind FileWAL magic: %+v", bound)
		}
		stage, err := stageBoundArchive(t, raw, manifest, path)
		if err != nil || stage == nil || stage.cutoffLocalSeq != tc.cut || stage.receipts == nil {
			t.Fatalf("bound stage = %+v, %v", stage, err)
		}
		after, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatalf("binding mutated WAL: %v", err)
		}
	}
}

func TestReceiptArchiveWALCutAcceptsValidSuffixButNotInvalidSuffix(t *testing.T) {
	raw, path := archiveWALFixture(t, 1, 1)
	manifest, err := bindReceiptArchiveFileWAL(raw, path, archiveWALStringDecode, archiveWALValidEntry)
	if err != nil {
		t.Fatal(err)
	}
	w, err := mutationlog.ResumeFileWAL(path, archiveWALStringEncode, archiveWALStringDecode, func(mutationlog.Entry) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Write(mutationlog.Entry{Seq: 2, HLC: hlc.Timestamp{WallNs: 2, NodeID: hlc.NodeID{1}}, Op: "suffix"}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if stage, err := stageBoundArchive(t, raw, manifest, path); err != nil || stage == nil {
		t.Fatalf("valid suffix rejected: %+v, %v", stage, err)
	}
	full, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		edit func([]byte) []byte
	}{
		{"torn suffix", func(b []byte) []byte { return b[:len(b)-1] }},
		{"bad suffix checksum", func(b []byte) []byte { b[len(b)-1] ^= 1; return b }},
		{"invalid suffix payload", func(b []byte) []byte {
			copy(b[len(b)-len("suffix"):], []byte("broken"))
			bodyStart := len(b) - len("suffix") - 36
			frameStart := bodyStart - 8
			crc := fileWALFrameCRC(b[frameStart:frameStart+4], b[bodyStart:])
			binary.BigEndian.PutUint32(b[frameStart+4:frameStart+8], crc)
			return b
		}},
		{"frame HLC differs from decoded payload contract", func(b []byte) []byte {
			bodyStart := len(b) - len("suffix") - 36
			frameStart := bodyStart - 8
			binary.BigEndian.PutUint64(b[bodyStart+8:bodyStart+16], 99)
			crc := fileWALFrameCRC(b[frameStart:frameStart+4], b[bodyStart:])
			binary.BigEndian.PutUint32(b[frameStart+4:frameStart+8], crc)
			return b
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := tc.edit(bytes.Clone(full))
			if err := os.WriteFile(path, bad, 0o600); err != nil {
				t.Fatal(err)
			}
			stage, err := stageBoundArchive(t, raw, manifest, path)
			if err == nil || stage != nil {
				t.Fatalf("invalid suffix produced stage: %+v, %v", stage, err)
			}
			if err := os.WriteFile(path, full, 0o600); err != nil {
				t.Fatal(err)
			}
		})
	}
	// A prefix-only binding deliberately cannot prove a suffix observed at
	// manifest creation was not later lost. This must not authorize serving.
	bound, err := decodeReceiptArchiveWALCut(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, bound.walOffset); err != nil {
		t.Fatal(err)
	}
	if stage, err := stageBoundArchive(t, raw, manifest, path); err != nil || stage == nil {
		t.Fatalf("prefix-only manifest unexpectedly rejected lost valid suffix: %+v, %v", stage, err)
	}
}

func TestReceiptArchiveWALCutRejectsSubstitutionAndMalformedManifest(t *testing.T) {
	raw, path := archiveWALFixture(t, 2, 2)
	manifest, err := bindReceiptArchiveFileWAL(raw, path, archiveWALStringDecode, archiveWALValidEntry)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		raw      []byte
		manifest []byte
		path     string
	}{
		{"wrong archive", append(bytes.Clone(raw), 0), manifest, path},
		{"short manifest", raw, manifest[:len(manifest)-1], path},
		{"long manifest", raw, append(bytes.Clone(manifest), 0), path},
		{"changed manifest", raw, func() []byte { b := bytes.Clone(manifest); b[60] ^= 1; return b }(), path},
		{"wrong cut", raw, func() []byte {
			cut, err := decodeReceiptArchiveWALCut(manifest)
			if err != nil {
				t.Fatal(err)
			}
			cut.localSeq = 1
			return encodeReceiptArchiveWALCut(cut)
		}(), path},
		{"short WAL", raw, manifest, func() string {
			short := filepath.Join(t.TempDir(), "short.wal")
			if err := os.WriteFile(short, []byte("LNWAL01\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return short
		}()},
		{"different WAL", raw, manifest, func() string {
			otherPath := filepath.Join(t.TempDir(), "other.wal")
			w, err := mutationlog.CreateFileWAL(otherPath, archiveWALStringEncode)
			if err != nil {
				t.Fatal(err)
			}
			for seq := uint64(1); seq <= 2; seq++ {
				if err := w.Write(mutationlog.Entry{Seq: seq, HLC: hlc.Timestamp{WallNs: int64(seq), NodeID: hlc.NodeID{1}}, Op: "other"}); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			return otherPath
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			archive := wholeStateArchiveFixture(t)
			stage, err := stageReceiptWholeStateArchiveAtWALCut(context.Background(), tc.raw, tc.manifest, tc.path,
				archiveWALStringDecode, archiveWALValidEntry, archive.Policy, time.Hour, nil)
			if err == nil || stage != nil {
				t.Fatalf("substitution produced stage: %+v, %v", stage, err)
			}
		})
	}
	for _, tc := range []struct {
		name string
		edit func([]byte)
	}{
		{"unsupported version", func(b []byte) { binary.BigEndian.PutUint16(b[8:10], 2) }},
		{"reserved bits", func(b []byte) { b[10] = 1 }},
		{"offset below header", func(b []byte) { binary.BigEndian.PutUint64(b[52:60], 7) }},
		{"nonzero cut at header offset", func(b []byte) { binary.BigEndian.PutUint64(b[52:60], 8) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := bytes.Clone(manifest)
			tc.edit(bad)
			sum := sha256.Sum256(bad[:len(bad)-sha256.Size])
			copy(bad[len(bad)-sha256.Size:], sum[:])
			if _, err := decodeReceiptArchiveWALCut(bad); !errors.Is(err, errReceiptArchiveWALCut) {
				t.Fatalf("malformed manifest decoded: %v", err)
			}
		})
	}
}

func fileWALFrameCRC(length, body []byte) uint32 {
	// Mirror the stable FileWAL CRC only to create an otherwise valid test
	// frame whose application payload is rejected by the supplied decoder.
	crc := crc32.Checksum(length, crc32.MakeTable(crc32.Castagnoli))
	return crc32.Update(crc, crc32.MakeTable(crc32.Castagnoli), body)
}
