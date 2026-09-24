package mutationlog

import (
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
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
