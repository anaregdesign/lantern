package backup

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/service"
)

// This private codec is a framing prerequisite for ADR 0010, not a backup
// producer or restore path. Its caller must capture Graph, Receipts, and
// Origins under one publication cut, then verify the WAL frontier and choose
// the active epoch before installing anything. A graph-only Snapshot header
// is rejected even when receipt rows happen to be present. The existing .lbk
// graph dump never enters this codec and cannot certify receipt continuity.
const (
	wholeStateArchiveMagic      = "LANTARCH"
	wholeStateArchiveVersion    = uint16(1)
	wholeStateArchiveReceipts   = uint16(1)
	wholeStateArchiveMaxFrame   = 32 << 20
	wholeStateArchiveMaxBytes   = 512 << 20
	wholeStateArchiveHeaderSize = 8 + 2 + 2 + 4 + 16 + 32 + 8 + 8 + 4 + 8
	wholeStateArchiveFooterSize = 8 + 8 + 8 + sha256.Size
	wholeStateArchiveReceiptMin = 49 + 16 + 4 + 4 + 1 + 32 + 24 + 1 + 8 + 4
	wholeStateArchiveOriginSize = 16 + 8 + 8 + 4 + 16

	wholeStateGraphRecord   = byte(1)
	wholeStateReceiptRecord = byte(2)
	wholeStateOriginRecord  = byte(3)
	wholeStateFooterRecord  = byte(255)
)

var errWholeStateArchive = errors.New("backup: invalid whole-state archive")

type wholeStateArchive struct {
	// Graph is an ordered, counted replication Snapshot frame stream, retaining
	// causal floors and the per-contribution edge decomposition that .lbk
	// discards. The codec validates each graph payload but cannot prove that
	// Graph, Receipts, and Origins came from one atomic publication cut; a
	// future producer and installer must establish that cut before serving.
	Graph    []*pb.SnapshotResponse
	Receipts mutationreceipt.Snapshot
	Policy   mutationreceipt.Config
	Origins  []service.OriginState
}

func wholeStateArchiveError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errWholeStateArchive, fmt.Sprintf(format, args...))
}

// encodeWholeStateArchive writes a deterministic, bounded v1 container. A
// SHA-256 footer detects accidental truncation or corruption; it is not an
// authenticity signature and cannot prove that the producer captured one
// atomic graph/receipt/origin cut.
func encodeWholeStateArchive(w io.Writer, a wholeStateArchive) error {
	if err := validateWholeStateArchive(a); err != nil {
		return err
	}
	var out bytes.Buffer
	out.WriteString(wholeStateArchiveMagic)
	writeArchiveU16(&out, wholeStateArchiveVersion)
	writeArchiveU16(&out, wholeStateArchiveReceipts)
	writeArchiveU32(&out, 0)
	out.Write(a.Receipts.Epoch[:])
	out.Write(a.Receipts.PolicyFingerprint[:])
	writeArchiveU64(&out, uint64(a.Receipts.ClockHighWaterMillis))
	writeArchiveU64(&out, uint64(a.Policy.Retention/time.Millisecond))
	writeArchiveU32(&out, uint32(a.Policy.MaxEntries))
	writeArchiveU64(&out, a.Policy.MaxBytes)
	for _, frame := range a.Graph {
		payload, err := (proto.MarshalOptions{Deterministic: true}).Marshal(frame)
		if err != nil {
			return wholeStateArchiveError("marshal graph frame: %v", err)
		}
		if err := validateArchiveGraphFrameWire(payload); err != nil {
			return err
		}
		if err := writeArchiveRecord(&out, wholeStateGraphRecord, payload); err != nil {
			return err
		}
	}
	for _, receipt := range a.Receipts.Receipts {
		payload := encodeArchiveReceipt(receipt)
		if err := writeArchiveRecord(&out, wholeStateReceiptRecord, payload); err != nil {
			return err
		}
	}
	for _, origin := range a.Origins {
		payload := encodeArchiveOrigin(origin)
		if err := writeArchiveRecord(&out, wholeStateOriginRecord, payload); err != nil {
			return err
		}
	}
	if out.Len()+5+wholeStateArchiveFooterSize > wholeStateArchiveMaxBytes {
		return wholeStateArchiveError("archive exceeds byte limit")
	}
	digest := sha256.Sum256(out.Bytes())
	var footer bytes.Buffer
	writeArchiveU64(&footer, uint64(len(a.Graph)))
	writeArchiveU64(&footer, uint64(len(a.Receipts.Receipts)))
	writeArchiveU64(&footer, uint64(len(a.Origins)))
	footer.Write(digest[:])
	if err := writeArchiveRecord(&out, wholeStateFooterRecord, footer.Bytes()); err != nil {
		return err
	}
	n, err := w.Write(out.Bytes())
	if err != nil {
		return fmt.Errorf("backup: write whole-state archive: %w", err)
	}
	if n != out.Len() {
		return fmt.Errorf("backup: write whole-state archive: %w", io.ErrShortWrite)
	}
	return nil
}

// decodeWholeStateArchive returns no partial state. It rejects missing,
// reordered, duplicate, unknown, oversized, or damaged records before a
// future installer may inspect the decoded value.
func decodeWholeStateArchive(r io.Reader) (wholeStateArchive, error) {
	raw, err := io.ReadAll(io.LimitReader(r, wholeStateArchiveMaxBytes+1))
	if err != nil {
		return wholeStateArchive{}, fmt.Errorf("backup: read whole-state archive: %w", err)
	}
	if len(raw) > wholeStateArchiveMaxBytes || len(raw) < wholeStateArchiveHeaderSize+5+wholeStateArchiveFooterSize {
		return wholeStateArchive{}, wholeStateArchiveError("invalid archive size")
	}
	if string(raw[:8]) != wholeStateArchiveMagic || binary.BigEndian.Uint16(raw[8:10]) != wholeStateArchiveVersion ||
		binary.BigEndian.Uint16(raw[10:12]) != wholeStateArchiveReceipts || binary.BigEndian.Uint32(raw[12:16]) != 0 {
		return wholeStateArchive{}, wholeStateArchiveError("unknown magic, version, features, or reserved header")
	}
	var a wholeStateArchive
	a.Receipts.Version = 1
	copy(a.Receipts.Epoch[:], raw[16:32])
	copy(a.Receipts.PolicyFingerprint[:], raw[32:64])
	highWater := binary.BigEndian.Uint64(raw[64:72])
	retentionMS := binary.BigEndian.Uint64(raw[72:80])
	maxEntries := binary.BigEndian.Uint32(raw[80:84])
	maxBytes := binary.BigEndian.Uint64(raw[84:92])
	if highWater > math.MaxInt64 || retentionMS > uint64(math.MaxInt64/int64(time.Millisecond)) || maxEntries > math.MaxInt32 {
		return wholeStateArchive{}, wholeStateArchiveError("out-of-range receipt policy")
	}
	a.Receipts.ClockHighWaterMillis = int64(highWater)
	a.Policy = mutationreceipt.Config{
		Epoch: a.Receipts.Epoch, Retention: time.Duration(retentionMS) * time.Millisecond,
		MaxEntries: int(maxEntries), MaxBytes: maxBytes, ClockHighWater: time.UnixMilli(a.Receipts.ClockHighWaterMillis),
	}
	off, phase := wholeStateArchiveHeaderSize, byte(wholeStateGraphRecord)
	for off < len(raw) {
		start := off
		if len(raw)-off < 5 {
			return wholeStateArchive{}, wholeStateArchiveError("truncated record prefix")
		}
		kind, size := raw[off], binary.BigEndian.Uint32(raw[off+1:off+5])
		off += 5
		if size == 0 || size > wholeStateArchiveMaxFrame || uint64(size) > uint64(len(raw)-off) {
			return wholeStateArchive{}, wholeStateArchiveError("invalid record length")
		}
		payload := raw[off : off+int(size)]
		off += int(size)
		if kind == wholeStateFooterRecord {
			if off != len(raw) || len(payload) != wholeStateArchiveFooterSize {
				return wholeStateArchive{}, wholeStateArchiveError("missing or trailing footer bytes")
			}
			want := sha256.Sum256(raw[:start])
			if !bytes.Equal(payload[24:], want[:]) || binary.BigEndian.Uint64(payload[:8]) != uint64(len(a.Graph)) ||
				binary.BigEndian.Uint64(payload[8:16]) != uint64(len(a.Receipts.Receipts)) ||
				binary.BigEndian.Uint64(payload[16:24]) != uint64(len(a.Origins)) {
				return wholeStateArchive{}, wholeStateArchiveError("footer count or checksum mismatch")
			}
			if err := validateWholeStateArchive(a); err != nil {
				return wholeStateArchive{}, err
			}
			return a, nil
		}
		if kind < phase || kind > wholeStateOriginRecord {
			return wholeStateArchive{}, wholeStateArchiveError("unknown or reordered record kind %d", kind)
		}
		phase = kind
		switch kind {
		case wholeStateGraphRecord:
			if err := validateArchiveGraphFrameWire(payload); err != nil {
				return wholeStateArchive{}, err
			}
			frame := &pb.SnapshotResponse{}
			if err := proto.Unmarshal(payload, frame); err != nil || frame.GetEntry() == nil {
				return wholeStateArchive{}, wholeStateArchiveError("invalid graph frame")
			}
			a.Graph = append(a.Graph, frame)
		case wholeStateReceiptRecord:
			receipt, err := decodeArchiveReceipt(payload)
			if err != nil {
				return wholeStateArchive{}, err
			}
			a.Receipts.Receipts = append(a.Receipts.Receipts, receipt)
		case wholeStateOriginRecord:
			origin, err := decodeArchiveOrigin(payload)
			if err != nil {
				return wholeStateArchive{}, err
			}
			a.Origins = append(a.Origins, origin)
		}
	}
	return wholeStateArchive{}, wholeStateArchiveError("missing footer")
}

func validateWholeStateArchive(a wholeStateArchive) error {
	if a.Receipts.Version != 1 || a.Receipts.Epoch == (mutationreceipt.Epoch{}) || a.Policy.Epoch != a.Receipts.Epoch ||
		a.Receipts.ClockHighWaterMillis < 0 || a.Policy.MaxEntries <= 0 || a.Policy.MaxEntries > math.MaxInt32 ||
		a.Policy.ClockHighWater.UnixMilli() != a.Receipts.ClockHighWaterMillis {
		return wholeStateArchiveError("invalid receipt header or policy")
	}
	if _, err := mutationreceipt.NewFromSnapshot(a.Policy, a.Receipts); err != nil {
		return wholeStateArchiveError("invalid receipt snapshot: %v", err)
	}
	if err := validateArchiveGraph(a.Graph, a.Origins); err != nil {
		return err
	}
	return nil
}

func validateArchiveGraph(frames []*pb.SnapshotResponse, origins []service.OriginState) error {
	if len(frames) < 2 || frames[0] == nil || frames[0].GetHeader() == nil || frames[len(frames)-1] == nil || frames[len(frames)-1].GetFooter() == nil {
		return wholeStateArchiveError("graph snapshot lacks header or footer")
	}
	header, footer := frames[0].GetHeader(), frames[len(frames)-1].GetFooter()
	if header.GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1 {
		return wholeStateArchiveError("graph Snapshot is not receipt format v1")
	}
	if _, ok := archiveHLC(header.GetCutoffHlc()); !ok {
		return wholeStateArchiveError("invalid graph cutoff HLC")
	}
	var counts [6]uint64
	lastRank := 0
	vertexBarriers := make(map[string]hlc.Timestamp)
	edgeBarriers := make(map[archiveEdgeKey]hlc.Timestamp)
	vertexTombstones := make(map[string]struct{})
	edgeTombstones := make(map[archiveEdgeKey]hlc.Timestamp)
	vertices := make(map[string]struct{})
	edges := make(map[archiveEdgeKey]struct{})
	for _, frame := range frames[1 : len(frames)-1] {
		if frame == nil {
			return wholeStateArchiveError("nil graph frame")
		}
		rank := 0
		switch entry := frame.GetEntry().(type) {
		case *pb.SnapshotResponse_VertexCausalBarrier:
			rank = 1
			barrier := entry.VertexCausalBarrier
			if barrier == nil || barrier.GetKey() == "" || !validArchiveHLC(barrier.GetHlc()) {
				return wholeStateArchiveError("invalid vertex causal barrier")
			}
			if _, exists := vertexBarriers[barrier.GetKey()]; exists {
				return wholeStateArchiveError("duplicate vertex causal barrier")
			}
			vertexBarriers[barrier.GetKey()], _ = archiveHLC(barrier.GetHlc())
		case *pb.SnapshotResponse_EdgeCausalBarrier:
			rank = 2
			barrier := entry.EdgeCausalBarrier
			if barrier == nil || barrier.GetTail() == "" || barrier.GetHead() == "" || !validArchiveHLC(barrier.GetHlc()) {
				return wholeStateArchiveError("invalid edge causal barrier")
			}
			key := archiveEdgeKey{barrier.GetTail(), barrier.GetHead()}
			if _, exists := edgeBarriers[key]; exists {
				return wholeStateArchiveError("duplicate edge causal barrier")
			}
			edgeBarriers[key], _ = archiveHLC(barrier.GetHlc())
		case *pb.SnapshotResponse_VertexTombstone:
			rank = 3
			marker := entry.VertexTombstone
			if marker == nil || marker.GetKey() == "" || !validArchiveHLC(marker.GetHlc()) || !validArchiveTimestamp(marker.GetExpiration()) {
				return wholeStateArchiveError("invalid vertex tombstone")
			}
			if _, exists := vertexTombstones[marker.GetKey()]; exists {
				return wholeStateArchiveError("duplicate vertex tombstone")
			}
			if _, exists := vertexBarriers[marker.GetKey()]; exists {
				return wholeStateArchiveError("vertex causal barrier and tombstone overlap")
			}
			vertexTombstones[marker.GetKey()] = struct{}{}
		case *pb.SnapshotResponse_EdgeTombstone:
			rank = 4
			marker := entry.EdgeTombstone
			if marker == nil || marker.GetTail() == "" || marker.GetHead() == "" || !validArchiveHLC(marker.GetHlc()) || !validArchiveTimestamp(marker.GetExpiration()) {
				return wholeStateArchiveError("invalid edge tombstone")
			}
			key := archiveEdgeKey{marker.GetTail(), marker.GetHead()}
			if _, exists := edgeTombstones[key]; exists {
				return wholeStateArchiveError("duplicate edge tombstone")
			}
			if _, exists := edgeBarriers[key]; exists {
				return wholeStateArchiveError("edge causal barrier and tombstone overlap")
			}
			edgeTombstones[key], _ = archiveHLC(marker.GetHlc())
		case *pb.SnapshotResponse_Vertex:
			rank = 5
			item := entry.Vertex
			if item == nil || item.GetVertex() == nil ||
				!validOptionalArchiveHLC(item.GetHlc()) || !validOptionalArchiveTimestamp(item.GetVertex().GetExpiration()) ||
				!validArchiveVertexValue(item.GetVertex()) {
				return wholeStateArchiveError("invalid live vertex")
			}
			if _, exists := vertices[item.GetVertex().GetKey()]; exists {
				return wholeStateArchiveError("duplicate live vertex")
			}
			if barrier, exists := vertexBarriers[item.GetVertex().GetKey()]; exists {
				var liveHLC hlc.Timestamp
				if item.GetHlc() != nil {
					liveHLC, _ = archiveHLC(item.GetHlc())
				}
				if liveHLC.Less(barrier) {
					return wholeStateArchiveError("live vertex is older than its causal barrier")
				}
			}
			vertices[item.GetVertex().GetKey()] = struct{}{}
		case *pb.SnapshotResponse_Edge:
			rank = 6
			item := entry.Edge
			key := archiveEdgeKey{item.GetTail(), item.GetHead()}
			if err := validateArchiveEdge(item, edgeTombstones[key]); err != nil {
				return err
			}
			if _, exists := edges[key]; exists {
				return wholeStateArchiveError("duplicate live edge")
			}
			var putFloor hlc.Timestamp
			if item.GetHlc() != nil {
				putFloor, _ = archiveHLC(item.GetHlc())
			}
			if barrier, exists := edgeBarriers[key]; exists {
				if putFloor != barrier {
					return wholeStateArchiveError("live edge Put floor differs from its causal barrier")
				}
			} else if putFloor != (hlc.Timestamp{}) {
				return wholeStateArchiveError("live edge Put floor lacks a causal barrier")
			}
			if _, exists := vertices[key.tail]; !exists {
				return wholeStateArchiveError("live edge tail is absent")
			}
			if _, exists := vertices[key.head]; !exists {
				return wholeStateArchiveError("live edge head is absent")
			}
			edges[key] = struct{}{}
		default:
			return wholeStateArchiveError("unexpected graph frame")
		}
		if rank < lastRank {
			return wholeStateArchiveError("reordered graph frame")
		}
		lastRank = rank
		counts[rank-1]++
	}
	if counts != [6]uint64{footer.GetVertexCausalBarrierCount(), footer.GetEdgeCausalBarrierCount(),
		footer.GetVertexTombstoneCount(), footer.GetEdgeTombstoneCount(), footer.GetVertexCount(), footer.GetEdgeCount()} {
		return wholeStateArchiveError("graph footer count mismatch")
	}
	if len(origins) != len(header.GetCutoffSeqPerOrigin()) {
		return wholeStateArchiveError("origin state and graph cutoff mismatch")
	}
	var previous [16]byte
	for i, origin := range origins {
		if origin.Origin == ([16]byte{}) || origin.LastSeq == 0 || origin.LastHLC.WallNs <= 0 ||
			origin.LastHLC.NodeID != origin.Origin || (i != 0 && bytes.Compare(previous[:], origin.Origin[:]) >= 0) {
			return wholeStateArchiveError("invalid or unsorted origin state")
		}
		previous = origin.Origin
		if header.GetCutoffSeqPerOrigin()[hex.EncodeToString(origin.Origin[:])] != origin.LastSeq {
			return wholeStateArchiveError("origin state and graph cutoff mismatch")
		}
	}
	return nil
}

type archiveEdgeKey struct{ tail, head string }

// A nil HLC represents a local-only live row with no recorded causal floor.
// Every explicit floor must be a complete nonzero timestamp and node identity.
func archiveHLC(stamp *pb.HLCTimestamp) (hlc.Timestamp, bool) {
	if stamp == nil || stamp.GetWallNs() <= 0 || len(stamp.GetNodeId()) != 16 {
		return hlc.Timestamp{}, false
	}
	var id hlc.NodeID
	copy(id[:], stamp.GetNodeId())
	if id == (hlc.NodeID{}) {
		return hlc.Timestamp{}, false
	}
	return hlc.Timestamp{WallNs: stamp.GetWallNs(), Logical: stamp.GetLogical(), NodeID: id}, true
}

func validArchiveHLC(stamp *pb.HLCTimestamp) bool {
	_, ok := archiveHLC(stamp)
	return ok
}

func validOptionalArchiveHLC(stamp *pb.HLCTimestamp) bool {
	return stamp == nil || validArchiveHLC(stamp)
}

func validArchiveTimestamp(stamp *timestamppb.Timestamp) bool {
	return stamp != nil && stamp.CheckValid() == nil
}

func validOptionalArchiveTimestamp(stamp *timestamppb.Timestamp) bool {
	return stamp == nil || stamp.CheckValid() == nil
}

func validArchiveVertexValue(vertex *pb.Vertex) bool {
	switch value := vertex.GetValue().(type) {
	case *pb.Vertex_Timestamp:
		return validArchiveTimestamp(value.Timestamp)
	case *pb.Vertex_Duration:
		return value.Duration != nil && value.Duration.CheckValid() == nil
	case *pb.Vertex_Nil:
		return value.Nil
	default:
		return true
	}
}

func validateArchiveEdge(edge *pb.SnapshotEdge, tombstone hlc.Timestamp) error {
	if edge == nil || edge.GetTail() == "" || edge.GetHead() == "" || len(edge.GetContributions()) == 0 {
		return wholeStateArchiveError("invalid live edge")
	}
	var putFloor hlc.Timestamp
	if edge.GetHlc() != nil {
		var ok bool
		putFloor, ok = archiveHLC(edge.GetHlc())
		if !ok {
			return wholeStateArchiveError("invalid live edge Put floor")
		}
	}
	var seenPut bool
	seenAdds := make(map[[24]byte]struct{})
	for _, contribution := range edge.GetContributions() {
		if contribution == nil || !validOptionalArchiveTimestamp(contribution.GetExpiration()) {
			return wholeStateArchiveError("invalid live edge contribution")
		}
		idBytes := contribution.GetContribId()
		switch len(idBytes) {
		case 0:
			if tombstone != (hlc.Timestamp{}) {
				return wholeStateArchiveError("live edge Put contribution conflicts with tombstone")
			}
			if seenPut {
				return wholeStateArchiveError("duplicate live edge Put contribution")
			}
			seenPut = true
			if contribution.GetHlc() == nil {
				if putFloor != (hlc.Timestamp{}) {
					return wholeStateArchiveError("live edge Put contribution lacks its floor HLC")
				}
			} else {
				stamp, ok := archiveHLC(contribution.GetHlc())
				if !ok || stamp != putFloor {
					return wholeStateArchiveError("live edge Put contribution HLC mismatch")
				}
			}
		case 24:
			var id [24]byte
			copy(id[:], idBytes)
			if id == ([24]byte{}) {
				return wholeStateArchiveError("zero live edge Add ContribID")
			}
			if _, exists := seenAdds[id]; exists {
				return wholeStateArchiveError("duplicate live edge Add ContribID")
			}
			seenAdds[id] = struct{}{}
			addHLC, ok := archiveHLC(contribution.GetHlc())
			if !ok || (putFloor != (hlc.Timestamp{}) && !putFloor.Less(addHLC)) {
				return wholeStateArchiveError("invalid live edge Add HLC")
			}
			if tombstone != (hlc.Timestamp{}) && !tombstone.Less(addHLC) {
				return wholeStateArchiveError("live edge Add does not follow tombstone")
			}
		default:
			return wholeStateArchiveError("invalid live edge ContribID length")
		}
	}
	return nil
}

func writeArchiveRecord(w *bytes.Buffer, kind byte, payload []byte) error {
	if len(payload) == 0 || len(payload) > wholeStateArchiveMaxFrame || w.Len() > wholeStateArchiveMaxBytes-5-len(payload) {
		return wholeStateArchiveError("record exceeds byte limit")
	}
	w.WriteByte(kind)
	writeArchiveU32(w, uint32(len(payload)))
	w.Write(payload)
	return nil
}

func writeArchiveU16(w *bytes.Buffer, value uint16) {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], value)
	w.Write(b[:])
}
func writeArchiveU32(w *bytes.Buffer, value uint32) {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], value)
	w.Write(b[:])
}
func writeArchiveU64(w *bytes.Buffer, value uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], value)
	w.Write(b[:])
}

func encodeArchiveReceipt(r mutationreceipt.Receipt) []byte {
	var b bytes.Buffer
	b.Write(r.ID[:])
	b.Write(r.Group[:])
	writeArchiveU32(&b, r.Index)
	writeArchiveU32(&b, r.Count)
	b.WriteByte(byte(r.Kind))
	b.Write(r.Digest[:])
	b.Write(r.ContribID[:])
	if r.HasContrib {
		b.WriteByte(1)
	} else {
		b.WriteByte(0)
	}
	writeArchiveU64(&b, uint64(r.DeadlineMillis))
	writeArchiveU32(&b, uint32(len(r.Result)))
	b.Write(r.Result)
	return b.Bytes()
}

func decodeArchiveReceipt(raw []byte) (mutationreceipt.Receipt, error) {
	var r mutationreceipt.Receipt
	if len(raw) < wholeStateArchiveReceiptMin {
		return r, wholeStateArchiveError("short receipt record")
	}
	copy(r.ID[:], raw[:49])
	copy(r.Group[:], raw[49:65])
	r.Index = binary.BigEndian.Uint32(raw[65:69])
	r.Count = binary.BigEndian.Uint32(raw[69:73])
	r.Kind = mutationreceipt.Kind(raw[73])
	copy(r.Digest[:], raw[74:106])
	copy(r.ContribID[:], raw[106:130])
	if raw[130] > 1 {
		return r, wholeStateArchiveError("invalid contribution flag")
	}
	r.HasContrib = raw[130] == 1
	r.DeadlineMillis = int64(binary.BigEndian.Uint64(raw[131:139]))
	size := binary.BigEndian.Uint32(raw[139:143])
	if uint64(size) != uint64(len(raw)-wholeStateArchiveReceiptMin) {
		return r, wholeStateArchiveError("receipt result length mismatch")
	}
	r.Result = append([]byte(nil), raw[143:]...)
	return r, nil
}

func encodeArchiveOrigin(o service.OriginState) []byte {
	var b bytes.Buffer
	b.Write(o.Origin[:])
	writeArchiveU64(&b, o.LastSeq)
	writeArchiveU64(&b, uint64(o.LastHLC.WallNs))
	writeArchiveU32(&b, o.LastHLC.Logical)
	b.Write(o.LastHLC.NodeID[:])
	return b.Bytes()
}

func decodeArchiveOrigin(raw []byte) (service.OriginState, error) {
	var o service.OriginState
	if len(raw) != wholeStateArchiveOriginSize {
		return o, wholeStateArchiveError("invalid origin record length")
	}
	copy(o.Origin[:], raw[:16])
	o.LastSeq = binary.BigEndian.Uint64(raw[16:24])
	o.LastHLC.WallNs = int64(binary.BigEndian.Uint64(raw[24:32]))
	o.LastHLC.Logical = binary.BigEndian.Uint32(raw[32:36])
	copy(o.LastHLC.NodeID[:], raw[36:52])
	return o, nil
}
