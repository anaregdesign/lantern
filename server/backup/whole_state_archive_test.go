package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"reflect"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/service"
)

func wholeStateArchiveFixture(t *testing.T) wholeStateArchive {
	t.Helper()
	issued := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	policy := mutationreceipt.Config{Epoch: mutationreceipt.Epoch{9}, Retention: time.Hour, MaxEntries: 4, MaxBytes: 4096}
	store, err := mutationreceipt.New(policy)
	if err != nil {
		t.Fatal(err)
	}
	id, err := mutationreceipt.NewID(policy.Epoch, issued, [24]byte{1})
	if err != nil {
		t.Fatal(err)
	}
	intent := mutationreceipt.Intent{ID: id, Group: mutationreceipt.GroupID{8}, Count: 1, Kind: mutationreceipt.AddEdge,
		HasContrib: true, ContribID: mutationreceipt.ContribID{7}, Digest: mutationreceipt.IntentDigest([]byte("edge-add"))}
	tx, err := store.Begin(issued)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Abort()
	if class, _, err := tx.Classify([]mutationreceipt.Intent{intent}); err != nil || class != mutationreceipt.Fresh {
		t.Fatalf("classify = %v, %v", class, err)
	}
	if err := tx.Reserve([][]byte{[]byte("original-result")}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Stage(); err != nil {
		t.Fatal(err)
	}
	tx.Commit()
	receipts, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	policy.ClockHighWater = receipts.ClockHighWater()
	origin := hlc.NodeID{2}
	ts := hlc.Timestamp{WallNs: issued.UnixNano(), Logical: 3, NodeID: origin}
	frameHLC := &pb.HLCTimestamp{WallNs: ts.WallNs, Logical: ts.Logical, NodeId: origin[:]}
	graph := []*pb.SnapshotResponse{
		{Entry: &pb.SnapshotResponse_Header{Header: &pb.SnapshotHeader{
			CutoffSeqPerOrigin: map[string]uint64{hex.EncodeToString(origin[:]): 7},
			CutoffLocalSeq:     11, CutoffHlc: frameHLC,
			Format: pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1,
		}}},
		{Entry: &pb.SnapshotResponse_Vertex{Vertex: &pb.SnapshotVertex{Vertex: &pb.Vertex{Key: "tail"}, Hlc: frameHLC}}},
		{Entry: &pb.SnapshotResponse_Edge{Edge: &pb.SnapshotEdge{Tail: "tail", Head: "head", Contributions: []*pb.SnapshotEdgeContribution{
			{Weight: 1.5, ContribId: intent.ContribID[:], Hlc: frameHLC},
		}}}},
		{Entry: &pb.SnapshotResponse_Footer{Footer: &pb.SnapshotFooter{VertexCount: 1, EdgeCount: 1}}},
	}
	return wholeStateArchive{Graph: graph, Receipts: receipts, Policy: policy,
		Origins: []service.OriginState{{Origin: origin, LastSeq: 7, LastHLC: ts}}}
}

func encodedWholeStateArchive(t *testing.T, a wholeStateArchive) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := encodeWholeStateArchive(&out, a); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

type archiveShortWriter struct{}

func (archiveShortWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestWholeStateArchiveRejectsShortWrite(t *testing.T) {
	err := encodeWholeStateArchive(archiveShortWriter{}, wholeStateArchiveFixture(t))
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("short write = %v, want io.ErrShortWrite", err)
	}
}

func TestWholeStateArchiveRoundTrip(t *testing.T) {
	a := wholeStateArchiveFixture(t)
	raw := encodedWholeStateArchive(t, a)
	got, err := decodeWholeStateArchive(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Graph) != len(a.Graph) || !reflect.DeepEqual(got.Receipts, a.Receipts) ||
		!reflect.DeepEqual(got.Origins, a.Origins) || got.Policy != a.Policy {
		t.Fatalf("archive round trip drift: got %+v", got)
	}
	for i := range a.Graph {
		if !proto.Equal(got.Graph[i], a.Graph[i]) {
			t.Fatalf("graph frame %d changed", i)
		}
	}
	if got.Graph[2].GetEdge().GetContributions()[0].GetContribId()[0] != 7 {
		t.Fatal("Add contribution identity was folded away")
	}
	var second bytes.Buffer
	if err := encodeWholeStateArchive(&second, got); err != nil || !bytes.Equal(second.Bytes(), raw) {
		t.Fatalf("archive encoding is not deterministic: %v", err)
	}
}

func TestWholeStateArchiveRejectsDamageAndDowngrade(t *testing.T) {
	raw := encodedWholeStateArchive(t, wholeStateArchiveFixture(t))
	mutate := func(f func([]byte)) []byte { copied := append([]byte(nil), raw...); f(copied); return copied }
	withFooterDigest := func(b []byte) []byte {
		footer := len(b) - wholeStateArchiveFooterSize - 5
		checksum := sha256.Sum256(b[:footer])
		copy(b[footer+5+24:], checksum[:])
		return b
	}
	for _, tc := range []struct {
		name string
		raw  []byte
	}{
		{"legacy lbk", []byte{0x02, 0x0a, 0x00}},
		{"truncated", raw[:len(raw)-1]},
		{"extra byte", append(append([]byte(nil), raw...), 0)},
		{"bad magic", mutate(func(b []byte) { b[0] ^= 1 })},
		{"future version", mutate(func(b []byte) { b[9] = 2 })},
		{"missing receipt feature", mutate(func(b []byte) { b[11] = 0 })},
		{"unknown feature", mutate(func(b []byte) { b[11] = 3 })},
		{"nonzero reserved", mutate(func(b []byte) { b[15] = 1 })},
		{"checksum damage", mutate(func(b []byte) { b[wholeStateArchiveHeaderSize+6] ^= 1 })},
		{"footer count damage", withFooterDigest(mutate(func(b []byte) { b[len(b)-wholeStateArchiveFooterSize] ^= 1 }))},
		{"oversized record", mutate(func(b []byte) {
			binary.BigEndian.PutUint32(b[wholeStateArchiveHeaderSize+1:], wholeStateArchiveMaxFrame+1)
		})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got, err := decodeWholeStateArchive(bytes.NewReader(tc.raw)); !errors.Is(err, errWholeStateArchive) || len(got.Graph) != 0 {
				t.Fatalf("damaged archive returned %+v, %v", got, err)
			}
		})
	}
}

func TestWholeStateArchiveDecodeRejectsGraphOnlyWithValidChecksum(t *testing.T) {
	for _, format := range []pb.SnapshotFormat{
		pb.SnapshotFormat_SNAPSHOT_FORMAT_UNSPECIFIED,
		pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1,
		pb.SnapshotFormat(99),
	} {
		t.Run(format.String(), func(t *testing.T) {
			raw := append([]byte(nil), encodedWholeStateArchive(t, wholeStateArchiveFixture(t))...)
			frameStart := wholeStateArchiveHeaderSize + 5
			frameSize := int(binary.BigEndian.Uint32(raw[wholeStateArchiveHeaderSize+1:]))
			frame := &pb.SnapshotResponse{}
			if err := proto.Unmarshal(raw[frameStart:frameStart+frameSize], frame); err != nil {
				t.Fatal(err)
			}
			frame.GetHeader().Format = format
			modified, err := (proto.MarshalOptions{Deterministic: true}).Marshal(frame)
			if err != nil {
				t.Fatal(err)
			}
			footerStart := len(raw) - 5 - wholeStateArchiveFooterSize
			corrupt := append([]byte(nil), raw[:wholeStateArchiveHeaderSize+5]...)
			binary.BigEndian.PutUint32(corrupt[wholeStateArchiveHeaderSize+1:], uint32(len(modified)))
			corrupt = append(corrupt, modified...)
			corrupt = append(corrupt, raw[frameStart+frameSize:footerStart]...)
			digest := sha256.Sum256(corrupt)
			corrupt = append(corrupt, raw[footerStart:footerStart+5+24]...)
			corrupt = append(corrupt, digest[:]...)
			if _, err := decodeWholeStateArchive(bytes.NewReader(corrupt)); !errors.Is(err, errWholeStateArchive) {
				t.Fatalf("graph-only or unknown format decoded: %v", err)
			}
		})
	}
}

func TestWholeStateArchiveRejectsInconsistentCut(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*wholeStateArchive)
	}{
		{"legacy graph format", func(a *wholeStateArchive) {
			a.Graph[0].GetHeader().Format = pb.SnapshotFormat_SNAPSHOT_FORMAT_UNSPECIFIED
		}},
		{"graph-only format", func(a *wholeStateArchive) {
			a.Graph[0].GetHeader().Format = pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1
		}},
		{"unknown graph format", func(a *wholeStateArchive) { a.Graph[0].GetHeader().Format = pb.SnapshotFormat(99) }},
		{"receipt policy", func(a *wholeStateArchive) { a.Policy.MaxBytes++ }},
		{"missing origin", func(a *wholeStateArchive) { a.Origins = nil }},
		{"origin cutoff drift", func(a *wholeStateArchive) { a.Origins[0].LastSeq++ }},
		{"graph footer drift", func(a *wholeStateArchive) { a.Graph[len(a.Graph)-1].GetFooter().EdgeCount++ }},
		{"graph order", func(a *wholeStateArchive) { a.Graph[1], a.Graph[2] = a.Graph[2], a.Graph[1] }},
		{"unknown nested graph field", func(a *wholeStateArchive) {
			a.Graph[1].GetVertex().ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
		}},
		{"receipt result drift", func(a *wholeStateArchive) { a.Receipts.Receipts[0].DeadlineMillis-- }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := wholeStateArchiveFixture(t)
			tc.edit(&a)
			if err := encodeWholeStateArchive(&bytes.Buffer{}, a); !errors.Is(err, errWholeStateArchive) {
				t.Fatalf("invalid cut accepted: %v", err)
			}
		})
	}
}
