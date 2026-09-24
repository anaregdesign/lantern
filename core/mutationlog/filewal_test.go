package mutationlog

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/core/hlc"
)

func fileWALStringEncode(op MutationOp) ([]byte, error) {
	s, ok := op.(string)
	if !ok {
		return nil, errors.New("want string payload")
	}
	return []byte(s), nil
}

func fileWALStringDecode(b []byte) (MutationOp, error) { return string(b), nil }

func fileWALEntry(seq uint64, op string) Entry {
	var id hlc.NodeID
	id[0] = 0xa5
	return Entry{Seq: seq, HLC: hlc.Timestamp{WallNs: int64(seq) * 100, Logical: uint32(seq), NodeID: id}, Op: op}
}

func makeTwoRecordFileWAL(t *testing.T) (string, []byte) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mutations.wal")
	w, err := CreateFileWAL(path, fileWALStringEncode)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range []Entry{fileWALEntry(1, "first"), fileWALEntry(2, "second")} {
		if err := w.Write(entry); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, data
}

func TestFileWALRoundTripAndCreateOnly(t *testing.T) {
	path, _ := makeTwoRecordFileWAL(t)
	if _, err := CreateFileWAL(path, fileWALStringEncode); !errors.Is(err, os.ErrExist) {
		t.Fatalf("recreate existing WAL: %v, want os.ErrExist", err)
	}
	var got []Entry
	if err := ReplayFileWAL(path, fileWALStringDecode, func(e Entry) error {
		got = append(got, e)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := []Entry{fileWALEntry(1, "first"), fileWALEntry(2, "second")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("replayed = %+v, want %+v", got, want)
	}
}

func TestFileWALResumeRestoresBeforeAppend(t *testing.T) {
	path, original := makeTwoRecordFileWAL(t)
	var restored []Entry
	w, err := ResumeFileWAL(path, fileWALStringEncode, fileWALStringDecode, func(e Entry) error {
		restored = append(restored, e)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []Entry{fileWALEntry(1, "first"), fileWALEntry(2, "second")}; !reflect.DeepEqual(restored, want) {
		t.Fatalf("restored = %+v, want %+v", restored, want)
	}
	if w.lastSeq != 2 || w.offset != int64(len(original)) {
		t.Fatalf("resume frontier = (%d, %d), want (2, %d)", w.lastSeq, w.offset, len(original))
	}
	var aborted *DefiniteWALAbort
	if err := w.Write(fileWALEntry(4, "gap")); !errors.As(err, &aborted) || !errors.Is(err, ErrFileWALSequence) {
		t.Fatalf("Write gap after resume = %v, want definite sequence abort", err)
	}
	if err := w.Write(fileWALEntry(3, "third")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var replayed []Entry
	if err := ReplayFileWAL(path, fileWALStringDecode, func(e Entry) error {
		replayed = append(replayed, e)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	want := append(restored, fileWALEntry(3, "third"))
	if !reflect.DeepEqual(replayed, want) {
		t.Fatalf("replayed = %+v, want %+v", replayed, want)
	}
}

func TestFileWALResumeEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mutations.wal")
	w, err := CreateFileWAL(path, fileWALStringEncode)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	visits := 0
	w, err = ResumeFileWAL(path, fileWALStringEncode, fileWALStringDecode, func(Entry) error {
		visits++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if visits != 0 || w.lastSeq != 0 || w.offset != int64(len(fileWALMagic)) {
		t.Fatalf("empty resume = visits %d, seq %d, offset %d", visits, w.lastSeq, w.offset)
	}
	if err := w.Write(fileWALEntry(1, "first")); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestFileWALResumeRejectsDamageBeforeRestore(t *testing.T) {
	_, valid := makeTwoRecordFileWAL(t)
	second := len(fileWALMagic) + fileWALFrameHeader + int(binary.BigEndian.Uint32(valid[len(fileWALMagic):]))
	tests := []struct {
		name   string
		mutate func([]byte) []byte
		decode func([]byte) (MutationOp, error)
		want   error
	}{
		{name: "torn header", mutate: func(b []byte) []byte { return b[:second+3] }, want: ErrFileWALTornTail},
		{name: "torn body", mutate: func(b []byte) []byte { return b[:len(b)-2] }, want: ErrFileWALTornTail},
		{name: "bad checksum", mutate: func(b []byte) []byte {
			b[len(b)-1] ^= 0xff
			return b
		}, want: ErrFileWALCorrupt},
		{name: "bad length", mutate: func(b []byte) []byte {
			binary.BigEndian.PutUint32(b[second:second+4], maxFileWALBody+1)
			return b
		}, want: ErrFileWALCorrupt},
		{name: "sequence gap", mutate: func(b []byte) []byte {
			binary.BigEndian.PutUint64(b[second+fileWALFrameHeader:], 3)
			body := b[second+fileWALFrameHeader:]
			crc := crc32.Checksum(b[second:second+4], fileWALCRC)
			crc = crc32.Update(crc, fileWALCRC, body)
			binary.BigEndian.PutUint32(b[second+4:second+8], crc)
			return b
		}, want: ErrFileWALSequence},
		{name: "bad magic", mutate: func(b []byte) []byte {
			b[0] ^= 0xff
			return b
		}, want: ErrFileWALCorrupt},
		{name: "decode failure", mutate: func(b []byte) []byte { return b }, decode: func(b []byte) (MutationOp, error) {
			if string(b) == "second" {
				return nil, errors.New("unsupported payload")
			}
			return string(b), nil
		}, want: ErrFileWALCorrupt},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "mutations.wal")
			damaged := tt.mutate(append([]byte(nil), valid...))
			if err := os.WriteFile(path, damaged, 0o600); err != nil {
				t.Fatal(err)
			}
			decode := tt.decode
			if decode == nil {
				decode = fileWALStringDecode
			}
			visits := 0
			w, err := ResumeFileWAL(path, fileWALStringEncode, decode, func(Entry) error {
				visits++
				return nil
			})
			if !errors.Is(err, tt.want) || w != nil || visits != 0 {
				t.Fatalf("Resume = (%v, %v), visits=%d; want %v and no restore", w, err, visits, tt.want)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, damaged) {
				t.Fatal("Resume modified the damaged WAL")
			}
		})
	}
}

func TestFileWALResumeRestoreFailureNeverOpensWriter(t *testing.T) {
	path, before := makeTwoRecordFileWAL(t)
	restoreErr := errors.New("restore failed")
	visits := 0
	w, err := ResumeFileWAL(path, fileWALStringEncode, fileWALStringDecode, func(Entry) error {
		visits++
		return restoreErr
	})
	if !errors.Is(err, restoreErr) || w != nil || visits != 1 {
		t.Fatalf("Resume = (%v, %v), visits=%d; want restore error after first entry", w, err, visits)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatal("restore failure modified the WAL")
	}
}

func TestFileWALWriteRejectsOffsetOverflowBeforeIO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mutations.wal")
	w, err := CreateFileWAL(path, fileWALStringEncode)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.offset = math.MaxInt64
	var aborted *DefiniteWALAbort
	if err := w.Write(fileWALEntry(1, "first")); !errors.As(err, &aborted) || !errors.Is(err, ErrFileWALTooLarge) {
		t.Fatalf("Write at max offset = %v, want definite overflow abort", err)
	}
	if w.unusable {
		t.Fatal("pre-I/O offset rejection poisoned WAL")
	}
	w.offset = int64(len(fileWALMagic))
	if err := w.Write(fileWALEntry(1, "first")); err != nil {
		t.Fatalf("Write after offset rejection: %v", err)
	}
}

func TestFileWALReplayRejectsDamageBeforeVisit(t *testing.T) {
	path, valid := makeTwoRecordFileWAL(t)
	second := len(fileWALMagic) + fileWALFrameHeader + int(binary.BigEndian.Uint32(valid[len(fileWALMagic):]))
	tests := []struct {
		name   string
		mutate func([]byte) []byte
		want   error
	}{
		{
			name:   "torn tail",
			mutate: func(data []byte) []byte { return data[:len(data)-2] },
			want:   ErrFileWALTornTail,
		},
		{
			name:   "partial frame header",
			mutate: func(data []byte) []byte { return data[:second+3] },
			want:   ErrFileWALTornTail,
		},
		{
			name: "checksum corruption",
			mutate: func(data []byte) []byte {
				data[len(data)-1] ^= 0xff
				return data
			},
			want: ErrFileWALCorrupt,
		},
		{
			name: "invalid frame length",
			mutate: func(data []byte) []byte {
				binary.BigEndian.PutUint32(data[second:second+4], maxFileWALBody+1)
				return data
			},
			want: ErrFileWALCorrupt,
		},
		{
			name: "sequence gap with valid checksum",
			mutate: func(data []byte) []byte {
				binary.BigEndian.PutUint64(data[second+fileWALFrameHeader:], 3)
				body := data[second+fileWALFrameHeader:]
				crc := crc32.Checksum(data[second:second+4], fileWALCRC)
				crc = crc32.Update(crc, fileWALCRC, body)
				binary.BigEndian.PutUint32(data[second+4:second+8], crc)
				return data
			},
			want: ErrFileWALSequence,
		},
		{
			name: "bad version header",
			mutate: func(data []byte) []byte {
				data[0] ^= 0xff
				return data
			},
			want: ErrFileWALCorrupt,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := tt.mutate(append([]byte(nil), valid...))
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			visits := 0
			err := ReplayFileWAL(path, fileWALStringDecode, func(Entry) error {
				visits++
				return nil
			})
			if !errors.Is(err, tt.want) || visits != 0 {
				t.Fatalf("Replay = %v, visits=%d; want %v and no visits", err, visits, tt.want)
			}
		})
	}
}

func TestFileWALReplayRejectsDecodeFailureBeforeVisit(t *testing.T) {
	path, _ := makeTwoRecordFileWAL(t)
	decodeErr := errors.New("unsupported envelope")
	visits := 0
	err := ReplayFileWAL(path, func(b []byte) (MutationOp, error) {
		if string(b) == "second" {
			return nil, decodeErr
		}
		return string(b), nil
	}, func(Entry) error {
		visits++
		return nil
	})
	if !errors.Is(err, ErrFileWALCorrupt) || visits != 0 {
		t.Fatalf("Replay = %v, visits=%d; want corrupt and no visits", err, visits)
	}
}

func TestFileWALPreIOAbortRetainsSequence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mutations.wal")
	w, err := CreateFileWAL(path, func(op MutationOp) ([]byte, error) {
		if op == "bad" {
			return nil, errors.New("encode rejected")
		}
		return fileWALStringEncode(op)
	})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	for _, entry := range []Entry{
		fileWALEntry(2, "wrong seq"),
		fileWALEntry(1, "bad"),
		fileWALEntry(1, strings.Repeat("x", maxFileWALBody-fileWALBodyHeader+1)),
	} {
		var abort *DefiniteWALAbort
		if err := w.Write(entry); !errors.As(err, &abort) {
			t.Fatalf("Write(%q) = %v, want definite abort", entry.Op.(string)[:min(len(entry.Op.(string)), 16)], err)
		}
	}
	if err := w.Write(fileWALEntry(1, "first")); err != nil {
		t.Fatalf("Write after definite abort: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var got []Entry
	if err := ReplayFileWAL(path, fileWALStringDecode, func(e Entry) error {
		got = append(got, e)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Seq != 1 || got[0].Op != "first" {
		t.Fatalf("replayed = %+v, want only seq 1 first", got)
	}
}

type lostSyncAckFile struct{ *os.File }

func (f *lostSyncAckFile) Sync() error {
	if err := f.File.Sync(); err != nil {
		return err
	}
	return errors.New("lost acknowledgement after sync")
}

func TestFileWALSyncErrorIsIndeterminate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mutations.wal")
	w, err := CreateFileWAL(path, fileWALStringEncode)
	if err != nil {
		t.Fatal(err)
	}
	w.file = &lostSyncAckFile{File: w.file.(*os.File)}
	l := New(Options{Capacity: 4, WAL: w})
	defer l.Close()
	published := false
	if _, err := l.CommitWithPublication("first", fileWALEntry(1, "first").HLC, func(Entry) { published = true }); !errors.Is(err, ErrWALIndeterminate) {
		t.Fatalf("Commit = %v, want indeterminate", err)
	}
	if published {
		t.Fatal("published after lost WAL acknowledgement")
	}
	if err := w.Write(fileWALEntry(1, "retry")); !errors.Is(err, ErrFileWALUnusable) {
		t.Fatalf("FileWAL retry = %v, want unusable", err)
	}
	if _, err := l.Append("retry", fileWALEntry(1, "retry").HLC); !errors.Is(err, ErrWALIndeterminate) {
		t.Fatalf("Log retry = %v, want indeterminate", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var got []Entry
	if err := ReplayFileWAL(path, fileWALStringDecode, func(e Entry) error {
		got = append(got, e)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Seq != 1 || got[0].Op != "first" {
		t.Fatalf("replayed = %+v, want potentially committed seq 1", got)
	}
}

type shortWriteFile struct{ *os.File }

func (f *shortWriteFile) Write(p []byte) (int, error) { return f.File.Write(p[:len(p)/2]) }

func TestFileWALShortWritePoisonsAndLeavesTornTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mutations.wal")
	w, err := CreateFileWAL(path, fileWALStringEncode)
	if err != nil {
		t.Fatal(err)
	}
	w.file = &shortWriteFile{File: w.file.(*os.File)}
	if err := w.Write(fileWALEntry(1, "first")); !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("Write = %v, want short write", err)
	}
	if err := w.Write(fileWALEntry(1, "retry")); !errors.Is(err, ErrFileWALUnusable) {
		t.Fatalf("retry = %v, want unusable", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ReplayFileWAL(path, fileWALStringDecode, func(Entry) error { return nil }); !errors.Is(err, ErrFileWALTornTail) {
		t.Fatalf("Replay = %v, want torn tail", err)
	}
}
