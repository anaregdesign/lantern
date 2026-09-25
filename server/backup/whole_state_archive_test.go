package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/service"
)

func wholeStateArchiveFixture(t *testing.T) wholeStateArchive {
	return wholeStateArchiveFixtureAt(t, time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC))
}

func wholeStateArchiveFixtureAt(t *testing.T, issued time.Time) wholeStateArchive {
	t.Helper()
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
		{Entry: &pb.SnapshotResponse_Vertex{Vertex: &pb.SnapshotVertex{Vertex: &pb.Vertex{Key: "head"}, Hlc: frameHLC}}},
		{Entry: &pb.SnapshotResponse_Edge{Edge: &pb.SnapshotEdge{Tail: "tail", Head: "head", Contributions: []*pb.SnapshotEdgeContribution{
			{Weight: 1.5, ContribId: intent.ContribID[:], Hlc: frameHLC},
		}}}},
		{Entry: &pb.SnapshotResponse_Footer{Footer: &pb.SnapshotFooter{VertexCount: 2, EdgeCount: 1}}},
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
	if got.Graph[3].GetEdge().GetContributions()[0].GetContribId()[0] != 7 {
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
		{"RPC receipt metadata", func(a *wholeStateArchive) {
			a.Graph[0].GetHeader().ReceiptMetadata = &pb.SnapshotReceiptMetadata{}
		}},
		{"RPC receipt footer count", func(a *wholeStateArchive) {
			a.Graph[len(a.Graph)-1].GetFooter().ReceiptCount = 1
		}},
		{"receipt policy", func(a *wholeStateArchive) { a.Policy.MaxBytes++ }},
		{"missing origin", func(a *wholeStateArchive) { a.Origins = nil }},
		{"origin cutoff drift", func(a *wholeStateArchive) { a.Origins[0].LastSeq++ }},
		{"graph footer drift", func(a *wholeStateArchive) { a.Graph[len(a.Graph)-1].GetFooter().EdgeCount++ }},
		{"graph order", func(a *wholeStateArchive) { a.Graph[1], a.Graph[3] = a.Graph[3], a.Graph[1] }},
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

// An archive producer must refuse malformed graph frames, and a decoder must
// still refuse the same frames if an attacker repairs the container checksum.
func TestWholeStateArchiveRejectsInvalidGraphPayload(t *testing.T) {
	badTimestamp := func() *timestamppb.Timestamp { return &timestamppb.Timestamp{Seconds: 253402300800} }
	insertBody := func(a *wholeStateArchive, frames ...*pb.SnapshotResponse) {
		a.Graph = append(a.Graph[:1], append(frames, a.Graph[1:]...)...)
	}
	for _, tc := range []struct {
		name string
		want string
		edit func(*wholeStateArchive)
	}{
		{"nil vertex", "invalid live vertex", func(a *wholeStateArchive) { a.Graph[1].GetVertex().Vertex = nil }},
		{"invalid vertex expiration", "invalid live vertex", func(a *wholeStateArchive) { a.Graph[1].GetVertex().Vertex.Expiration = badTimestamp() }},
		{"invalid vertex value timestamp", "invalid live vertex", func(a *wholeStateArchive) {
			a.Graph[1].GetVertex().Vertex.Value = &pb.Vertex_Timestamp{Timestamp: badTimestamp()}
		}},
		{"invalid vertex value duration", "invalid live vertex", func(a *wholeStateArchive) {
			a.Graph[1].GetVertex().Vertex.Value = &pb.Vertex_Duration{Duration: &durationpb.Duration{Seconds: 315576000001}}
		}},
		{"false vertex nil marker", "invalid live vertex", func(a *wholeStateArchive) {
			a.Graph[1].GetVertex().Vertex.Value = &pb.Vertex_Nil{Nil: false}
		}},
		{"invalid live vertex HLC", "invalid live vertex", func(a *wholeStateArchive) {
			a.Graph[1].GetVertex().Hlc = &pb.HLCTimestamp{WallNs: 1, NodeId: make([]byte, 16)}
		}},
		{"duplicate vertex", "duplicate live vertex", func(a *wholeStateArchive) { a.Graph[2].GetVertex().Vertex.Key = "tail" }},
		{"dangling edge", "live edge head is absent", func(a *wholeStateArchive) { a.Graph[3].GetEdge().Head = "missing" }},
		{"empty edge", "invalid live edge", func(a *wholeStateArchive) { a.Graph[3].GetEdge().Contributions = nil }},
		{"invalid edge Put floor", "invalid live edge Put floor", func(a *wholeStateArchive) {
			a.Graph[3].GetEdge().Hlc = &pb.HLCTimestamp{WallNs: 1, NodeId: make([]byte, 16)}
		}},
		{"missing Add HLC", "invalid live edge Add HLC", func(a *wholeStateArchive) {
			a.Graph[3].GetEdge().Contributions[0].Hlc = nil
		}},
		{"short Add ContribID", "invalid live edge ContribID length", func(a *wholeStateArchive) {
			a.Graph[3].GetEdge().Contributions[0].ContribId = []byte{7}
		}},
		{"zero Add ContribID", "zero live edge Add ContribID", func(a *wholeStateArchive) {
			a.Graph[3].GetEdge().Contributions[0].ContribId = make([]byte, 24)
		}},
		{"duplicate Add ContribID", "duplicate live edge Add ContribID", func(a *wholeStateArchive) {
			edge := a.Graph[3].GetEdge()
			edge.Contributions = append(edge.Contributions, proto.Clone(edge.Contributions[0]).(*pb.SnapshotEdgeContribution))
		}},
		{"Add does not follow Put floor", "invalid live edge Add HLC", func(a *wholeStateArchive) {
			a.Graph[3].GetEdge().Hlc = proto.Clone(a.Graph[3].GetEdge().Contributions[0].GetHlc()).(*pb.HLCTimestamp)
		}},
		{"duplicate Put contribution", "duplicate live edge Put contribution", func(a *wholeStateArchive) {
			a.Graph[3].GetEdge().Contributions = []*pb.SnapshotEdgeContribution{{Weight: 1}, {Weight: 2}}
		}},
		{"Put contribution HLC mismatch", "live edge Put contribution HLC mismatch", func(a *wholeStateArchive) {
			edge := a.Graph[3].GetEdge()
			edge.Hlc = proto.Clone(edge.Contributions[0].GetHlc()).(*pb.HLCTimestamp)
			edge.Contributions[0].ContribId = nil
			edge.Contributions[0].Hlc = proto.Clone(edge.Hlc).(*pb.HLCTimestamp)
			edge.Contributions[0].Hlc.WallNs++
		}},
		{"Put contribution lacks floor HLC", "live edge Put contribution lacks its floor HLC", func(a *wholeStateArchive) {
			edge := a.Graph[3].GetEdge()
			edge.Hlc = proto.Clone(edge.Contributions[0].GetHlc()).(*pb.HLCTimestamp)
			edge.Contributions[0].ContribId = nil
			edge.Contributions[0].Hlc = nil
		}},
		{"invalid contribution expiration", "invalid live edge contribution", func(a *wholeStateArchive) {
			a.Graph[3].GetEdge().Contributions[0].Expiration = badTimestamp()
		}},
		{"duplicate edge", "duplicate live edge", func(a *wholeStateArchive) {
			a.Graph = append(a.Graph[:4], append([]*pb.SnapshotResponse{proto.Clone(a.Graph[3]).(*pb.SnapshotResponse)}, a.Graph[4:]...)...)
			a.Graph[len(a.Graph)-1].GetFooter().EdgeCount++
		}},
		{"invalid vertex barrier", "invalid vertex causal barrier", func(a *wholeStateArchive) {
			insertBody(a, &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_VertexCausalBarrier{VertexCausalBarrier: &pb.SnapshotVertexCausalBarrier{Key: "expired"}}})
			a.Graph[len(a.Graph)-1].GetFooter().VertexCausalBarrierCount++
		}},
		{"duplicate vertex barrier", "duplicate vertex causal barrier", func(a *wholeStateArchive) {
			barrier := &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_VertexCausalBarrier{VertexCausalBarrier: &pb.SnapshotVertexCausalBarrier{Key: "expired", Hlc: a.Graph[0].GetHeader().GetCutoffHlc()}}}
			insertBody(a, barrier, proto.Clone(barrier).(*pb.SnapshotResponse))
			a.Graph[len(a.Graph)-1].GetFooter().VertexCausalBarrierCount += 2
		}},
		{"vertex older than its barrier", "live vertex is older than its causal barrier", func(a *wholeStateArchive) {
			barrier := proto.Clone(a.Graph[0].GetHeader().GetCutoffHlc()).(*pb.HLCTimestamp)
			older := proto.Clone(barrier).(*pb.HLCTimestamp)
			older.WallNs--
			a.Graph[1].GetVertex().Hlc = older
			insertBody(a, &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_VertexCausalBarrier{VertexCausalBarrier: &pb.SnapshotVertexCausalBarrier{Key: "tail", Hlc: barrier}}})
			a.Graph[len(a.Graph)-1].GetFooter().VertexCausalBarrierCount++
		}},
		{"invalid edge barrier", "invalid edge causal barrier", func(a *wholeStateArchive) {
			insertBody(a, &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_EdgeCausalBarrier{EdgeCausalBarrier: &pb.SnapshotEdgeCausalBarrier{Tail: "tail", Head: "head"}}})
			a.Graph[len(a.Graph)-1].GetFooter().EdgeCausalBarrierCount++
		}},
		{"edge floor differs from barrier", "live edge Put floor differs from its causal barrier", func(a *wholeStateArchive) {
			barrier := proto.Clone(a.Graph[0].GetHeader().GetCutoffHlc()).(*pb.HLCTimestamp)
			floor := proto.Clone(barrier).(*pb.HLCTimestamp)
			floor.WallNs--
			a.Graph[3].GetEdge().Hlc = floor
			insertBody(a, &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_EdgeCausalBarrier{EdgeCausalBarrier: &pb.SnapshotEdgeCausalBarrier{Tail: "tail", Head: "head", Hlc: barrier}}})
			a.Graph[len(a.Graph)-1].GetFooter().EdgeCausalBarrierCount++
		}},
		{"edge floor lacks barrier", "live edge Put floor lacks a causal barrier", func(a *wholeStateArchive) {
			floor := proto.Clone(a.Graph[0].GetHeader().GetCutoffHlc()).(*pb.HLCTimestamp)
			floor.WallNs--
			a.Graph[3].GetEdge().Hlc = floor
		}},
		{"invalid vertex tombstone", "invalid vertex tombstone", func(a *wholeStateArchive) {
			insertBody(a, &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_VertexTombstone{VertexTombstone: &pb.SnapshotVertexTombstone{Key: "dead", Hlc: a.Graph[0].GetHeader().GetCutoffHlc(), Expiration: badTimestamp()}}})
			a.Graph[len(a.Graph)-1].GetFooter().VertexTombstoneCount++
		}},
		{"vertex barrier and tombstone overlap", "vertex causal barrier and tombstone overlap", func(a *wholeStateArchive) {
			stamp := a.Graph[0].GetHeader().GetCutoffHlc()
			insertBody(a,
				&pb.SnapshotResponse{Entry: &pb.SnapshotResponse_VertexCausalBarrier{VertexCausalBarrier: &pb.SnapshotVertexCausalBarrier{Key: "dead", Hlc: stamp}}},
				&pb.SnapshotResponse{Entry: &pb.SnapshotResponse_VertexTombstone{VertexTombstone: &pb.SnapshotVertexTombstone{Key: "dead", Hlc: stamp, Expiration: timestamppb.New(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))}}},
			)
			a.Graph[len(a.Graph)-1].GetFooter().VertexCausalBarrierCount++
			a.Graph[len(a.Graph)-1].GetFooter().VertexTombstoneCount++
		}},
		{"explicit vertex and tombstone overlap", "live vertex and tombstone overlap", func(a *wholeStateArchive) {
			stamp := a.Graph[0].GetHeader().GetCutoffHlc()
			insertBody(a, &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_VertexTombstone{
				VertexTombstone: &pb.SnapshotVertexTombstone{
					Key:        "tail",
					Hlc:        stamp,
					Expiration: timestamppb.New(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)),
				},
			}})
			a.Graph[len(a.Graph)-1].GetFooter().VertexTombstoneCount++
		}},
		{"invalid edge tombstone", "invalid edge tombstone", func(a *wholeStateArchive) {
			insertBody(a, &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_EdgeTombstone{EdgeTombstone: &pb.SnapshotEdgeTombstone{Tail: "tail", Head: "head", Hlc: a.Graph[0].GetHeader().GetCutoffHlc()}}})
			a.Graph[len(a.Graph)-1].GetFooter().EdgeTombstoneCount++
		}},
		{"edge barrier and tombstone overlap", "edge causal barrier and tombstone overlap", func(a *wholeStateArchive) {
			stamp := proto.Clone(a.Graph[0].GetHeader().GetCutoffHlc()).(*pb.HLCTimestamp)
			stamp.WallNs--
			a.Graph[3].GetEdge().Hlc = stamp
			insertBody(a,
				&pb.SnapshotResponse{Entry: &pb.SnapshotResponse_EdgeCausalBarrier{EdgeCausalBarrier: &pb.SnapshotEdgeCausalBarrier{Tail: "tail", Head: "head", Hlc: stamp}}},
				&pb.SnapshotResponse{Entry: &pb.SnapshotResponse_EdgeTombstone{EdgeTombstone: &pb.SnapshotEdgeTombstone{Tail: "tail", Head: "head", Hlc: stamp, Expiration: timestamppb.New(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))}}},
			)
			a.Graph[len(a.Graph)-1].GetFooter().EdgeCausalBarrierCount++
			a.Graph[len(a.Graph)-1].GetFooter().EdgeTombstoneCount++
		}},
		{"edge Add not newer than tombstone", "live edge Add does not follow tombstone", func(a *wholeStateArchive) {
			insertBody(a, &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_EdgeTombstone{EdgeTombstone: &pb.SnapshotEdgeTombstone{
				Tail: "tail", Head: "head", Hlc: a.Graph[0].GetHeader().GetCutoffHlc(),
				Expiration: timestamppb.New(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)),
			}}})
			a.Graph[len(a.Graph)-1].GetFooter().EdgeTombstoneCount++
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := wholeStateArchiveFixture(t)
			tc.edit(&a)
			if err := encodeWholeStateArchive(&bytes.Buffer{}, a); !errors.Is(err, errWholeStateArchive) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("encode malformed graph = %v, want %q", err, tc.want)
			}
			raw := uncheckedArchiveWithGraph(t, a)
			if decoded, err := decodeWholeStateArchive(bytes.NewReader(raw)); !errors.Is(err, errWholeStateArchive) ||
				!strings.Contains(err.Error(), tc.want) || len(decoded.Graph) != 0 {
				t.Fatalf("decode malformed graph = %+v, %v, want %q", decoded, err, tc.want)
			}
		})
	}
}

func TestWholeStateArchiveAcceptsCausalAndLocalOnlyGraphPayload(t *testing.T) {
	a := wholeStateArchiveFixture(t)
	cutoff := a.Graph[0].GetHeader().GetCutoffHlc()
	putFloor := proto.Clone(cutoff).(*pb.HLCTimestamp)
	putFloor.WallNs--
	deadline := timestamppb.New(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC))
	body := []*pb.SnapshotResponse{
		{Entry: &pb.SnapshotResponse_VertexCausalBarrier{VertexCausalBarrier: &pb.SnapshotVertexCausalBarrier{Key: "expired-v", Hlc: cutoff}}},
		{Entry: &pb.SnapshotResponse_EdgeCausalBarrier{EdgeCausalBarrier: &pb.SnapshotEdgeCausalBarrier{Tail: "tail", Head: "head", Hlc: putFloor}}},
		{Entry: &pb.SnapshotResponse_VertexTombstone{VertexTombstone: &pb.SnapshotVertexTombstone{Key: "deleted-v", Hlc: cutoff, Expiration: deadline}}},
		{Entry: &pb.SnapshotResponse_EdgeTombstone{EdgeTombstone: &pb.SnapshotEdgeTombstone{Tail: "deleted-v", Head: "head", Hlc: cutoff, Expiration: deadline}}},
	}
	a.Graph = append(a.Graph[:1], append(body, a.Graph[1:]...)...)
	a.Graph[len(a.Graph)-1].GetFooter().VertexCausalBarrierCount = 1
	a.Graph[len(a.Graph)-1].GetFooter().EdgeCausalBarrierCount = 1
	a.Graph[len(a.Graph)-1].GetFooter().VertexTombstoneCount = 1
	a.Graph[len(a.Graph)-1].GetFooter().EdgeTombstoneCount = 1
	a.Graph[5].GetVertex().Hlc = nil // local-only live vertex has no HLC
	a.Graph[5].GetVertex().Vertex.Value = &pb.Vertex_Nil{Nil: true}
	edge := a.Graph[7].GetEdge()
	add := edge.Contributions[0]
	edge.Hlc = putFloor
	edge.Contributions = []*pb.SnapshotEdgeContribution{
		{Weight: 2, Hlc: putFloor}, // zero ContribID is the single Put row
		add,                        // Add retains its own later causal position
	}
	raw := encodedWholeStateArchive(t, a)
	if decoded, err := decodeWholeStateArchive(bytes.NewReader(raw)); err != nil || len(decoded.Graph) != len(a.Graph) {
		t.Fatalf("causal graph archive = %d frames, %v", len(decoded.Graph), err)
	}
}

func TestWholeStateArchiveAcceptsNewerAddOverRetainedEdgeTombstone(t *testing.T) {
	a := wholeStateArchiveFixture(t)
	older := proto.Clone(a.Graph[0].GetHeader().GetCutoffHlc()).(*pb.HLCTimestamp)
	older.WallNs--
	marker := &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_EdgeTombstone{EdgeTombstone: &pb.SnapshotEdgeTombstone{
		Tail: "tail", Head: "head", Hlc: older,
		Expiration: timestamppb.New(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)),
	}}}
	a.Graph = append(a.Graph[:1], append([]*pb.SnapshotResponse{marker}, a.Graph[1:]...)...)
	a.Graph[len(a.Graph)-1].GetFooter().EdgeTombstoneCount++
	raw := encodedWholeStateArchive(t, a)
	if _, err := decodeWholeStateArchive(bytes.NewReader(raw)); err != nil {
		t.Fatalf("newer Add over retained tombstone: %v", err)
	}
}

// Forge a correctly framed and checksummed archive after bypassing the
// producer's semantic checks. This tests the decoder rather than checksum
// rejection; it is never used by the production path.
func uncheckedArchiveWithGraph(t *testing.T, a wholeStateArchive) []byte {
	t.Helper()
	baseline := encodedWholeStateArchive(t, wholeStateArchiveFixture(t))
	var out bytes.Buffer
	out.Write(baseline[:wholeStateArchiveHeaderSize])
	for _, frame := range a.Graph {
		payload, err := (proto.MarshalOptions{Deterministic: true}).Marshal(frame)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeArchiveRecord(&out, wholeStateGraphRecord, payload); err != nil {
			t.Fatal(err)
		}
	}
	for _, receipt := range a.Receipts.Receipts {
		if err := writeArchiveRecord(&out, wholeStateReceiptRecord, encodeArchiveReceipt(receipt)); err != nil {
			t.Fatal(err)
		}
	}
	for _, origin := range a.Origins {
		if err := writeArchiveRecord(&out, wholeStateOriginRecord, encodeArchiveOrigin(origin)); err != nil {
			t.Fatal(err)
		}
	}
	digest := sha256.Sum256(out.Bytes())
	var footer bytes.Buffer
	writeArchiveU64(&footer, uint64(len(a.Graph)))
	writeArchiveU64(&footer, uint64(len(a.Receipts.Receipts)))
	writeArchiveU64(&footer, uint64(len(a.Origins)))
	footer.Write(digest[:])
	if err := writeArchiveRecord(&out, wholeStateFooterRecord, footer.Bytes()); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
