package security

import (
	"encoding/binary"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"hash/crc32"
	"os"
	"testing"
)

func s2RestoreTestReadFile(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func s2RestoreTestWriteFile(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
}

func s2RestoreTestReplaceFinalPayload(t *testing.T, path string, payload []byte, mutateHeader ...func([]byte)) {
	t.Helper()
	wal := s2RestoreTestReadFile(t, path)
	offset, last := 8, 8
	for offset < len(wal) {
		last = offset
		offset += 8 + int(binary.BigEndian.Uint32(wal[offset:]))
	}
	body := append(append([]byte(nil), wal[last+8:last+44]...), payload...)
	for _, mutate := range mutateHeader {
		mutate(body[:36])
	}
	frame := binary.BigEndian.AppendUint32(nil, uint32(len(body)))
	crc := crc32.New(crc32.MakeTable(crc32.Castagnoli))
	_, _ = crc.Write(frame)
	_, _ = crc.Write(body)
	frame = binary.BigEndian.AppendUint32(frame, crc.Sum32())
	frame = append(frame, body...)
	s2RestoreTestWriteFile(t, path, append(wal[:last:last], frame...))
	seq := binary.BigEndian.Uint64(body)
	cut, err := mutationlog.InspectFileWALCut(path, seq, func(b []byte) (mutationlog.MutationOp, error) { return append([]byte(nil), b...), nil }, func(mutationlog.Entry) error { return nil })
	if err != nil {
		t.Fatal("forged bytes are not a valid physical WAL", err)
	}
	tip := s2RestoreTestReadFile(t, path+".tip")
	record := tip[len(tip)-44:]
	copy(record[8:40], cut.ChainSHA256[:])
	binary.BigEndian.PutUint32(record[40:], crc32.Checksum(record[:40], crc32.MakeTable(crc32.Castagnoli)))
	s2RestoreTestWriteFile(t, path+".tip", tip)
}
