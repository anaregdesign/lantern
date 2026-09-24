package mutationlog

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestResumeLogFromFileWALRestoresBoundedTailAndNextWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "mutations.wal")
	wal, err := CreateFileWAL(path, fileWALStringEncode)
	if err != nil {
		t.Fatal(err)
	}
	for seq := uint64(1); seq <= 5; seq++ {
		if err := wal.Write(fileWALEntry(seq, string(rune('a'+seq-1)))); err != nil {
			t.Fatal(err)
		}
	}
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var restored []Entry
	log, owner, err := ResumeLogFromFileWAL(path, Options{Capacity: 2, SubscriberBuffer: 2}, fileWALStringEncode, fileWALStringDecode, func(entry Entry) error {
		restored = append(restored, entry)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if len(restored) != 5 {
		t.Fatalf("restored %d entries, want 5", len(restored))
	}
	if first, ok := log.FirstSeq(); !ok || first != 4 {
		t.Fatalf("FirstSeq = (%d, %t), want (4, true)", first, ok)
	}
	if last, ok := log.LastSeq(); !ok || last != 5 {
		t.Fatalf("LastSeq = (%d, %t), want (5, true)", last, ok)
	}
	if log.Len() != 2 || log.Cap() != 2 || log.Evicted() != 3 {
		t.Fatalf("ring = len %d, cap %d, evicted %d; want 2, 2, 3", log.Len(), log.Cap(), log.Evicted())
	}
	if got := log.RetainedEntries(); !reflect.DeepEqual(got, restored[3:]) {
		t.Fatalf("retained = %+v, want %+v", got, restored[3:])
	}
	if _, _, err := log.Subscribe(3); !errors.Is(err, ErrGapped) {
		t.Fatalf("Subscribe from evicted seq = %v, want ErrGapped", err)
	}
	ch, cancel, err := log.Subscribe(4)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	for seq := uint64(4); seq <= 5; seq++ {
		select {
		case entry := <-ch:
			if entry.Seq != seq {
				t.Fatalf("replay Seq = %d, want %d", entry.Seq, seq)
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout replaying Seq %d", seq)
		}
	}
	// Replay reconstructed only the in-memory ring; the file is byte-for-byte
	// unchanged until an actual post-recovery commit.
	unchanged, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(unchanged, before) {
		t.Fatal("restore rewrote the WAL")
	}
	entry, err := log.CommitWithPublication("f", fileWALEntry(6, "f").HLC, nil)
	if err != nil || entry.Seq != 6 {
		t.Fatalf("post-restore commit = (%+v, %v), want Seq 6", entry, err)
	}
	select {
	case live := <-ch:
		if live.Seq != 6 {
			t.Fatalf("live Seq = %d, want 6", live.Seq)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout receiving post-restore live entry")
	}
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := log.Append("closed", fileWALEntry(7, "closed").HLC); !errors.Is(err, ErrClosed) {
		t.Fatalf("Append after owner.Close = %v, want ErrClosed", err)
	}
	var replayed []Entry
	if err := ReplayFileWAL(path, fileWALStringDecode, func(entry Entry) error {
		replayed = append(replayed, entry)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(replayed) != 6 || replayed[5].Seq != 6 || !reflect.DeepEqual(replayed[:5], restored) {
		t.Fatalf("WAL after resume = %+v, want original five plus Seq 6", replayed)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(after, before) {
		t.Fatal("post-restore commit rewrote the WAL prefix")
	}
}

func TestResumeLogFromFileWALEmptyStartsAtOne(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.wal")
	wal, err := CreateFileWAL(path, fileWALStringEncode)
	if err != nil {
		t.Fatal(err)
	}
	if err := wal.Close(); err != nil {
		t.Fatal(err)
	}
	visits := 0
	log, owner, err := ResumeLogFromFileWAL(path, Options{}, fileWALStringEncode, fileWALStringDecode, func(Entry) error {
		visits++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	if visits != 0 || log.Len() != 0 || log.Evicted() != 0 {
		t.Fatalf("empty restore = visits %d, len %d, evicted %d", visits, log.Len(), log.Evicted())
	}
	if _, ok := log.FirstSeq(); ok {
		t.Fatal("empty Log has FirstSeq")
	}
	if _, ok := log.LastSeq(); ok {
		t.Fatal("empty Log has LastSeq")
	}
	entry, err := log.CommitWithPublication("first", fileWALEntry(1, "first").HLC, nil)
	if err != nil || entry.Seq != 1 {
		t.Fatalf("first commit = (%+v, %v), want Seq 1", entry, err)
	}
}

func TestResumeLogFromFileWALRejectsConfigurationAndRestoreFailure(t *testing.T) {
	path, before := makeTwoRecordFileWAL(t)
	visits := 0
	log, owner, err := ResumeLogFromFileWAL(path, Options{WAL: NopWAL{}}, fileWALStringEncode, fileWALStringDecode, func(Entry) error {
		visits++
		return nil
	})
	if err == nil || log != nil || owner != nil || visits != 0 {
		t.Fatalf("configured WAL = (%v, %v, %v), visits %d; want rejection before replay", log, owner, err, visits)
	}
	log, owner, err = ResumeLogFromFileWAL(path, Options{}, nil, fileWALStringDecode, func(Entry) error { return nil })
	if err == nil || log != nil || owner != nil {
		t.Fatalf("nil codec = (%v, %v, %v), want rejection", log, owner, err)
	}
	restoreErr := errors.New("application restore failed")
	log, owner, err = ResumeLogFromFileWAL(path, Options{}, fileWALStringEncode, fileWALStringDecode, func(entry Entry) error {
		visits++
		if entry.Seq == 2 {
			return restoreErr
		}
		return nil
	})
	if !errors.Is(err, restoreErr) || log != nil || owner != nil || visits != 2 {
		t.Fatalf("restore failure = (%v, %v, %v), visits %d; want no returned resources", log, owner, err, visits)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("failed restore modified the WAL")
	}
}

func TestResumeLogFromFileWALRejectsGapAndOverflowBeforeRestore(t *testing.T) {
	_, valid := makeTwoRecordFileWAL(t)
	second := len(fileWALMagic) + fileWALFrameHeader + int(binary.BigEndian.Uint32(valid[len(fileWALMagic):]))
	for _, seq := range []uint64{0, 3, math.MaxUint64} {
		t.Run(fmt.Sprintf("seq_%d", seq), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bad.wal")
			broken := append([]byte(nil), valid...)
			binary.BigEndian.PutUint64(broken[second+fileWALFrameHeader:], seq)
			body := broken[second+fileWALFrameHeader:]
			crc := crc32.Checksum(broken[second:second+4], fileWALCRC)
			crc = crc32.Update(crc, fileWALCRC, body)
			binary.BigEndian.PutUint32(broken[second+4:second+8], crc)
			if err := os.WriteFile(path, broken, 0o600); err != nil {
				t.Fatal(err)
			}
			visits := 0
			log, owner, err := ResumeLogFromFileWAL(path, Options{}, fileWALStringEncode, fileWALStringDecode, func(Entry) error {
				visits++
				return nil
			})
			if !errors.Is(err, ErrFileWALSequence) || log != nil || owner != nil || visits != 0 {
				t.Fatalf("Seq %d = (%v, %v, %v), visits %d; want sequence rejection before restore", seq, log, owner, err, visits)
			}
		})
	}
}

func TestAttachResumedFileWALRejectsFrontierMismatchAndClosesWriter(t *testing.T) {
	path, _ := makeTwoRecordFileWAL(t)
	wal, err := ResumeFileWAL(path, fileWALStringEncode, fileWALStringDecode, func(Entry) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	tail := &Log{capacity: 2, ring: make([]Entry, 2)}
	tail.storeLocked(fileWALEntry(1, "first"))
	tail.lastSeq = 1
	tail.hasEntries = true
	log, owner, err := attachResumedFileWAL(Options{Capacity: 2}, wal, tail)
	if !errors.Is(err, ErrFileWALSequence) || log != nil || owner != nil {
		t.Fatalf("frontier mismatch = (%v, %v, %v), want rejection", log, owner, err)
	}
	if err := wal.Write(fileWALEntry(3, "third")); !errors.Is(err, ErrFileWALClosed) {
		t.Fatalf("mismatched writer Write = %v, want closed", err)
	}
}
