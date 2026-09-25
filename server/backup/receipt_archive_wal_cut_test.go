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

func TestReceiptArchiveWALCutBindsExactArchiveCutAndObservedTip(t *testing.T) {
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
		if string(manifest[:8]) != "LANTWCUT" ||
			binary.BigEndian.Uint16(manifest[8:10]) != 1 {
			t.Fatalf("WAL-cut envelope = %q version %d, want LANTWCUT version 1",
				manifest[:8],
				binary.BigEndian.Uint16(manifest[8:10]),
			)
		}
		bound, err := decodeReceiptArchiveWALCut(manifest)
		if err != nil {
			t.Fatal(err)
		}
		expected, err := mutationlog.InspectFileWALCut(path, tc.cut, archiveWALStringDecode, archiveWALValidEntry)
		if err != nil {
			t.Fatal(err)
		}
		if bound.archiveSHA256 != sha256.Sum256(raw) ||
			bound.cutSeq != tc.cut || bound.cutOffset != expected.Offset ||
			bound.cutSHA256 != expected.SHA256 || bound.cutChainSHA256 != expected.ChainSHA256 ||
			bound.tipSeq != tc.tail || bound.tipOffset != expected.ObservedOffset ||
			bound.tipSHA256 != expected.ObservedSHA256 || bound.tipChainSHA256 != expected.ObservedChainSHA256 {
			t.Fatalf("decoded manifest = %+v, %v", bound, err)
		}
		if tc.cut == 0 && (bound.cutOffset != receiptArchiveWALZeroOffset ||
			bound.cutSHA256 != sha256.Sum256(before[:receiptArchiveWALZeroOffset])) {
			t.Fatalf("zero cut does not bind FileWAL magic: %+v", bound)
		}
		if tc.tail == 0 && (bound.tipOffset != receiptArchiveWALZeroOffset ||
			bound.tipSHA256 != bound.cutSHA256 || bound.tipChainSHA256 != bound.cutChainSHA256) {
			t.Fatalf("zero tip does not equal zero cut: %+v", bound)
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

func TestReceiptArchiveWALCutRequiresRecordedTipAndAcceptsLaterValidSuffix(t *testing.T) {
	raw, path := archiveWALFixture(t, 1, 2)
	manifest, err := bindReceiptArchiveFileWAL(raw, path, archiveWALStringDecode, archiveWALValidEntry)
	if err != nil {
		t.Fatal(err)
	}
	w, err := mutationlog.ResumeFileWAL(path, archiveWALStringEncode, archiveWALStringDecode, func(mutationlog.Entry) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Write(mutationlog.Entry{Seq: 3, HLC: hlc.Timestamp{WallNs: 3, NodeID: hlc.NodeID{1}}, Op: "suffix"}); err != nil {
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
		{"sequence gap in suffix", func(b []byte) []byte {
			bodyStart := len(b) - len("suffix") - 36
			frameStart := bodyStart - 8
			binary.BigEndian.PutUint64(b[bodyStart:bodyStart+8], 4)
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
	bound, err := decodeReceiptArchiveWALCut(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, bound.tipOffset); err != nil {
		t.Fatal(err)
	}
	if stage, err := stageBoundArchive(t, raw, manifest, path); err != nil || stage == nil {
		t.Fatalf("exact recorded tip rejected after later suffix removal: %+v, %v", stage, err)
	}
	if err := os.Truncate(path, bound.cutOffset); err != nil {
		t.Fatal(err)
	}
	if stage, err := stageBoundArchive(t, raw, manifest, path); err == nil || stage != nil {
		t.Fatalf("lost recorded valid suffix produced stage: %+v, %v", stage, err)
	}
}

func TestReceiptArchiveWALCutRejectsSubstitutionAndWitnessMismatch(t *testing.T) {
	raw, path := archiveWALFixture(t, 1, 2)
	manifest, err := bindReceiptArchiveFileWAL(raw, path, archiveWALStringDecode, archiveWALValidEntry)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := decodeReceiptArchiveWALCut(manifest)
	if err != nil {
		t.Fatal(err)
	}
	editManifest := func(edit func(*receiptArchiveWALCut)) []byte {
		candidate := bound
		edit(&candidate)
		return encodeReceiptArchiveWALCut(candidate)
	}
	zeroCut, err := mutationlog.InspectFileWALCut(path, 0, archiveWALStringDecode, archiveWALValidEntry)
	if err != nil {
		t.Fatal(err)
	}
	rewrittenTipPath := filepath.Join(t.TempDir(), "rewritten-tip.wal")
	rewrittenTip, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	bodyStart := len(rewrittenTip) - len("payload") - 36
	frameStart := bodyStart - 8
	copy(rewrittenTip[len(rewrittenTip)-len("payload"):], []byte("changed"))
	crc := fileWALFrameCRC(rewrittenTip[frameStart:frameStart+4], rewrittenTip[bodyStart:])
	binary.BigEndian.PutUint32(rewrittenTip[frameStart+4:frameStart+8], crc)
	if err := os.WriteFile(rewrittenTipPath, rewrittenTip, 0o600); err != nil {
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
		{"wrong archive cut", raw, editManifest(func(c *receiptArchiveWALCut) {
			c.cutSeq = 0
			c.cutOffset = zeroCut.Offset
			c.cutSHA256 = zeroCut.SHA256
			c.cutChainSHA256 = zeroCut.ChainSHA256
		}), path},
		{"cut offset mismatch", raw, editManifest(func(c *receiptArchiveWALCut) { c.cutOffset++ }), path},
		{"cut digest mismatch", raw, editManifest(func(c *receiptArchiveWALCut) { c.cutSHA256[0] ^= 1 }), path},
		{"cut chain mismatch", raw, editManifest(func(c *receiptArchiveWALCut) { c.cutChainSHA256[0] ^= 1 }), path},
		{"tip sequence mismatch", raw, editManifest(func(c *receiptArchiveWALCut) { c.tipSeq++ }), path},
		{"tip offset mismatch", raw, editManifest(func(c *receiptArchiveWALCut) { c.tipOffset++ }), path},
		{"tip digest mismatch", raw, editManifest(func(c *receiptArchiveWALCut) { c.tipSHA256[0] ^= 1 }), path},
		{"tip chain mismatch", raw, editManifest(func(c *receiptArchiveWALCut) { c.tipChainSHA256[0] ^= 1 }), path},
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
		{"same-size recorded tip rewrite", raw, manifest, rewrittenTipPath},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stage, err := stageBoundArchive(t, tc.raw, tc.manifest, tc.path)
			if err == nil || stage != nil {
				t.Fatalf("substitution produced stage: %+v, %v", stage, err)
			}
		})
	}
}

func TestReceiptArchiveWALCutRejectsMalformedManifest(t *testing.T) {
	raw, path := archiveWALFixture(t, 1, 2)
	manifest, err := bindReceiptArchiveFileWAL(raw, path, archiveWALStringDecode, archiveWALValidEntry)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := decodeReceiptArchiveWALCut(manifest)
	if err != nil {
		t.Fatal(err)
	}
	editManifest := func(edit func(*receiptArchiveWALCut)) []byte {
		candidate := bound
		edit(&candidate)
		return encodeReceiptArchiveWALCut(candidate)
	}
	editRaw := func(edit func([]byte)) []byte {
		candidate := bytes.Clone(manifest)
		edit(candidate)
		sum := sha256.Sum256(candidate[:receiptArchiveWALCutPayloadSize])
		copy(candidate[receiptArchiveWALCutPayloadSize:], sum[:])
		return candidate
	}
	for _, tc := range []struct {
		name        string
		manifest    []byte
		unsupported bool
	}{
		{name: "short", manifest: manifest[:len(manifest)-1]},
		{name: "long", manifest: append(bytes.Clone(manifest), 0)},
		{name: "unknown magic", manifest: editRaw(func(b []byte) { copy(b[:8], "LRWLCUT1") })},
		{
			name:        "obsolete LRWLCUT2",
			manifest:    editRaw(func(b []byte) { copy(b[:8], "LRWLCUT2") }),
			unsupported: true,
		},
		{
			name:        "unsupported version",
			manifest:    editRaw(func(b []byte) { binary.BigEndian.PutUint16(b[8:10], 2) }),
			unsupported: true,
		},
		{name: "reserved bits", manifest: editRaw(func(b []byte) { b[10] = 1 })},
		{name: "cut offset overflow", manifest: editRaw(func(b []byte) { binary.BigEndian.PutUint64(b[52:60], ^uint64(0)) })},
		{name: "tip offset overflow", manifest: editRaw(func(b []byte) { binary.BigEndian.PutUint64(b[132:140], ^uint64(0)) })},
		{name: "cut offset below header", manifest: editManifest(func(c *receiptArchiveWALCut) { c.cutOffset = receiptArchiveWALZeroOffset - 1 })},
		{name: "tip offset below header", manifest: editManifest(func(c *receiptArchiveWALCut) { c.tipOffset = receiptArchiveWALZeroOffset - 1 })},
		{name: "zero archive digest", manifest: editManifest(func(c *receiptArchiveWALCut) { c.archiveSHA256 = [sha256.Size]byte{} })},
		{name: "zero cut digest", manifest: editManifest(func(c *receiptArchiveWALCut) { c.cutSHA256 = [sha256.Size]byte{} })},
		{name: "zero cut chain", manifest: editManifest(func(c *receiptArchiveWALCut) { c.cutChainSHA256 = [sha256.Size]byte{} })},
		{name: "zero tip digest", manifest: editManifest(func(c *receiptArchiveWALCut) { c.tipSHA256 = [sha256.Size]byte{} })},
		{name: "zero tip chain", manifest: editManifest(func(c *receiptArchiveWALCut) { c.tipChainSHA256 = [sha256.Size]byte{} })},
		{name: "zero cut with framed offset", manifest: editManifest(func(c *receiptArchiveWALCut) { c.cutSeq = 0 })},
		{name: "nonzero cut at zero offset", manifest: editManifest(func(c *receiptArchiveWALCut) { c.cutOffset = receiptArchiveWALZeroOffset })},
		{name: "zero tip with framed offset", manifest: editManifest(func(c *receiptArchiveWALCut) { c.tipSeq = 0 })},
		{name: "nonzero tip at zero offset", manifest: editManifest(func(c *receiptArchiveWALCut) { c.tipOffset = receiptArchiveWALZeroOffset })},
		{name: "cut sequence after tip", manifest: editManifest(func(c *receiptArchiveWALCut) { c.cutSeq = c.tipSeq + 1 })},
		{name: "cut offset after tip", manifest: editManifest(func(c *receiptArchiveWALCut) { c.cutOffset = c.tipOffset + 1 })},
		{name: "equal sequence different witness", manifest: editManifest(func(c *receiptArchiveWALCut) { c.tipSeq = c.cutSeq })},
		{name: "later sequence without offset advance", manifest: editManifest(func(c *receiptArchiveWALCut) { c.tipOffset = c.cutOffset })},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := decodeReceiptArchiveWALCut(tc.manifest)
			if !errors.Is(err, errReceiptArchiveWALCut) {
				t.Fatalf("malformed manifest decoded: %v", err)
			}
			if tc.unsupported && !errors.Is(err, errUnsupportedReceiptArchiveWALCut) {
				t.Fatalf("obsolete manifest error = %v, want unsupported", err)
			}
			stage, err := stageBoundArchive(t, raw, tc.manifest, path)
			if !errors.Is(err, errReceiptArchiveWALCut) || stage != nil {
				t.Fatalf("malformed manifest produced stage: %+v, %v", stage, err)
			}
			if tc.unsupported && !errors.Is(err, errUnsupportedReceiptArchiveWALCut) {
				t.Fatalf("obsolete staged manifest error = %v, want unsupported", err)
			}
		})
	}
}

func TestReceiptArchiveWALCutCancellationReturnsNoStage(t *testing.T) {
	raw, path := archiveWALFixture(t, 1, 2)
	manifest, err := bindReceiptArchiveFileWAL(raw, path, archiveWALStringDecode, archiveWALValidEntry)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := decodeWholeStateArchive(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stage, err := stageReceiptWholeStateArchiveAtWALCut(ctx, raw, manifest, path,
		archiveWALStringDecode, archiveWALValidEntry, archive.Policy, time.Hour, nil)
	if !errors.Is(err, context.Canceled) || stage != nil {
		t.Fatalf("pre-canceled context produced stage: %+v, %v", stage, err)
	}

	ctx, cancel = context.WithCancel(context.Background())
	calls := 0
	stage, err = stageReceiptWholeStateArchiveAtWALCut(ctx, raw, manifest, path,
		func(payload []byte) (mutationlog.MutationOp, error) {
			calls++
			if calls == 1 {
				cancel()
			}
			return archiveWALStringDecode(payload)
		},
		archiveWALValidEntry, archive.Policy, time.Hour, nil)
	if !errors.Is(err, context.Canceled) || stage != nil || calls == 0 {
		t.Fatalf("mid-inspection cancellation produced stage: %+v, calls %d, %v", stage, calls, err)
	}
}

func fileWALFrameCRC(length, body []byte) uint32 {
	// Mirror the stable FileWAL CRC only to create an otherwise valid test
	// frame whose application payload is rejected by the supplied decoder.
	crc := crc32.Checksum(length, crc32.MakeTable(crc32.Castagnoli))
	return crc32.Update(crc, crc32.MakeTable(crc32.Castagnoli), body)
}
