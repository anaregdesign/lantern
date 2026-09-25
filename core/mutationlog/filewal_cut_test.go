package mutationlog

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"os"
	"path/filepath"
	"testing"
)

func fileWALCutValidEntry(entry Entry) error {
	if entry.HLC.WallNs != int64(entry.Seq)*100 {
		return errors.New("frame HLC differs from expected payload")
	}
	return nil
}

func TestInspectFileWALCutHashesExactPrefixAndValidSuffix(t *testing.T) {
	path, original := makeTwoRecordFileWAL(t)
	firstEnd := len(fileWALMagic) + fileWALFrameHeader + fileWALBodyHeader + len("first")
	requested := []uint64{0, 1, 2, 1}
	cuts, err := InspectFileWALCuts(path, requested, fileWALStringDecode, fileWALCutValidEntry)
	if err != nil {
		t.Fatal(err)
	}
	for i, tc := range []struct {
		seq    uint64
		offset int
	}{
		{0, len(fileWALMagic)},
		{1, firstEnd},
		{2, len(original)},
		{1, firstEnd},
	} {
		cut := cuts[i]
		if cut.Seq != tc.seq || cut.Offset != int64(tc.offset) || cut.ObservedLast != 2 ||
			cut.SHA256 != sha256.Sum256(original[:tc.offset]) ||
			cut.ObservedOffset != int64(len(original)) || cut.ObservedSHA256 != sha256.Sum256(original) {
			t.Fatalf("cut %d = %+v", tc.seq, cut)
		}
	}
	if cuts[0].ChainSHA256 != fileWALChainSeed() {
		t.Fatalf("zero cut chain = %x, want seed %x", cuts[0].ChainSHA256, fileWALChainSeed())
	}
	if cuts[2].ChainSHA256 != cuts[2].ObservedChainSHA256 ||
		cuts[2].Offset != cuts[2].ObservedOffset || cuts[2].SHA256 != cuts[2].ObservedSHA256 {
		t.Fatalf("tip cut differs from observed tip: %+v", cuts[2])
	}
	if cuts[1] != cuts[3] {
		t.Fatalf("duplicate cuts differ: %+v != %+v", cuts[1], cuts[3])
	}
	w, err := ResumeFileWAL(path, fileWALStringEncode, fileWALStringDecode, func(Entry) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Write(fileWALEntry(3, "suffix")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(after, original) {
		t.Fatal("valid suffix rewrote the cut prefix")
	}
	cut, err := InspectFileWALCut(path, 1, fileWALStringDecode, fileWALCutValidEntry)
	if err != nil || cut.Offset != int64(firstEnd) || cut.SHA256 != sha256.Sum256(original[:firstEnd]) ||
		cut.ObservedLast != 3 || cut.ObservedOffset != int64(len(after)) || cut.ObservedSHA256 != sha256.Sum256(after) {
		t.Fatalf("cut after valid suffix = %+v, %v", cut, err)
	}
	tip, err := InspectFileWALCut(path, 3, fileWALStringDecode, fileWALCutValidEntry)
	if err != nil || cut.ObservedChainSHA256 != tip.ChainSHA256 {
		t.Fatalf("observed tip chain = %x, tip = %+v, %v", cut.ObservedChainSHA256, tip, err)
	}
	unchanged, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, unchanged) {
		t.Fatalf("inspector mutated FileWAL: %v", err)
	}
}

func TestInspectFileWALCutRejectsUnavailableAndInvalidSuffix(t *testing.T) {
	path, original := makeTwoRecordFileWAL(t)
	if cut, err := InspectFileWALCut(path, 3, fileWALStringDecode, fileWALCutValidEntry); !errors.Is(err, ErrFileWALCutUnavailable) || cut != (FileWALCut{}) {
		t.Fatalf("missing cut = %+v, %v", cut, err)
	}
	if cut, err := InspectFileWALCut(path, 1, nil, fileWALCutValidEntry); err == nil || cut != (FileWALCut{}) {
		t.Fatalf("nil decoder = %+v, %v", cut, err)
	}
	if cuts, err := InspectFileWALCuts(path, nil, fileWALStringDecode, fileWALCutValidEntry); err == nil || cuts != nil {
		t.Fatalf("empty cuts = %+v, %v", cuts, err)
	}
	if cuts, err := InspectFileWALCuts(path, []uint64{1, 3}, fileWALStringDecode, fileWALCutValidEntry); !errors.Is(err, ErrFileWALCutUnavailable) || cuts != nil {
		t.Fatalf("partly unavailable cuts = %+v, %v", cuts, err)
	}
	for _, tc := range []struct {
		name string
		edit func([]byte) []byte
		want error
	}{
		{"torn suffix", func(b []byte) []byte { return b[:len(b)-1] }, ErrFileWALTornTail},
		{"bad CRC in suffix", func(b []byte) []byte { b[len(b)-1] ^= 1; return b }, ErrFileWALCorrupt},
		{"sequence gap in suffix", func(b []byte) []byte {
			second := len(fileWALMagic) + fileWALFrameHeader + fileWALBodyHeader + len("first")
			body := second + fileWALFrameHeader
			binary.BigEndian.PutUint64(b[body:body+8], 3)
			crc := crc32.Checksum(b[second:second+4], fileWALCRC)
			crc = crc32.Update(crc, fileWALCRC, b[body:])
			binary.BigEndian.PutUint32(b[second+4:second+8], crc)
			return b
		}, ErrFileWALSequence},
	} {
		t.Run(tc.name, func(t *testing.T) {
			invalid := tc.edit(bytes.Clone(original))
			badPath := filepath.Join(t.TempDir(), "bad.wal")
			if err := os.WriteFile(badPath, invalid, 0o600); err != nil {
				t.Fatal(err)
			}
			cut, err := InspectFileWALCut(badPath, 0, fileWALStringDecode, fileWALCutValidEntry)
			if !errors.Is(err, tc.want) || cut != (FileWALCut{}) {
				t.Fatalf("invalid suffix = %+v, %v, want %v", cut, err, tc.want)
			}
		})
	}
	decodeErr := errors.New("invalid payload")
	cut, err := InspectFileWALCut(path, 1, func(raw []byte) (MutationOp, error) {
		if string(raw) == "second" {
			return nil, decodeErr
		}
		return string(raw), nil
	}, fileWALCutValidEntry)
	if !errors.Is(err, ErrFileWALCorrupt) || cut != (FileWALCut{}) {
		t.Fatalf("undecodable suffix = %+v, %v", cut, err)
	}
}

func TestInspectFileWALCutDetectsPathReplacementAndSameSizeWrite(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(t *testing.T, path string)
	}{
		{"replacement", func(t *testing.T, path string) {
			t.Helper()
			replacement := filepath.Join(filepath.Dir(path), "replacement.wal")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(replacement, data, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(replacement, path); err != nil {
				t.Fatal(err)
			}
		}},
		{"same-size overwrite", func(t *testing.T, path string) {
			t.Helper()
			f, err := os.OpenFile(path, os.O_RDWR, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := f.WriteAt([]byte("FIRST"), int64(len(fileWALMagic)+fileWALFrameHeader+fileWALBodyHeader)); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path, _ := makeTwoRecordFileWAL(t)
			calls := 0
			cuts, err := InspectFileWALCuts(path, []uint64{0, 1}, func(raw []byte) (MutationOp, error) {
				calls++
				if calls == 1 {
					tc.edit(t, path)
				}
				return string(raw), nil
			}, fileWALCutValidEntry)
			if !errors.Is(err, ErrFileWALCorrupt) || cuts != nil {
				t.Fatalf("mutated path = %+v, %v", cuts, err)
			}
		})
	}
}

func TestInspectFileWALCutHashesBeforeDecoderMutation(t *testing.T) {
	path, raw := makeTwoRecordFileWAL(t)
	firstEnd := len(fileWALMagic) + fileWALFrameHeader + fileWALBodyHeader + len("first")
	cut, err := InspectFileWALCut(path, 1, func(payload []byte) (MutationOp, error) {
		payload[0] ^= 0xff
		return string(payload), nil
	}, fileWALCutValidEntry)
	if err != nil || cut.SHA256 != sha256.Sum256(raw[:firstEnd]) || cut.ObservedSHA256 != sha256.Sum256(raw) {
		t.Fatalf("decoder changed raw digest = %+v, %v", cut, err)
	}
}
