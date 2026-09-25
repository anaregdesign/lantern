package service

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// This is a private, unwired FileWAL payload codec for the Edge Delete
// prerequisite. The FileWAL frame owns the checksum and the local log seq;
// this payload owns the origin seq, receipt metadata, and graph projection.
// Neither this format nor its decoder establishes replay or peer transport.
const (
	receiptEdgeDeleteWALMagic      = "LRED\x01\x00\x00\x00"
	receiptEdgeDeleteWALMaxBytes   = 8 << 20 // below FileWAL's 32 MiB frame cap
	receiptEdgeDeleteWALHeaderSize = 8 + 16 + 8 + 8 + 4 + 16 + 16 + 32 + 8 + 8 + 4 + 4
	receiptEdgeDeleteWALItemSize   = 49 + 16 + 4 + 4 + 1 + 32 + 8 + 1 + 4 + 4
	receiptEdgeDeleteWALAcceptSize = 4 + 4 + 4
)

var (
	errReceiptEdgeDeleteWAL         = errors.New("service: invalid receipt Edge Delete WAL payload")
	errReceiptEdgeDeleteWALCapacity = errors.New("service: receipt Edge Delete WAL payload exceeds capacity")
)

func receiptEdgeDeleteWALError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errReceiptEdgeDeleteWAL, fmt.Sprintf(format, args...))
}

func receiptEdgeDeleteWALCapacityError() error {
	return errors.Join(
		errReceiptEdgeDeleteWAL,
		errReceiptEdgeDeleteWALCapacity,
	)
}

// validateReceiptEdgeDeleteWALRequestCapacity proves that even the largest
// receiver-local accepted projection (every request position accepted) fits
// the private WAL format. This bound depends only on validated request bytes,
// so every replica makes the same admission decision before touching state.
func validateReceiptEdgeDeleteWALRequestCapacity(keys []graphcache.EdgeKey[string]) error {
	if len(keys) == 0 ||
		len(keys) > (receiptEdgeDeleteWALMaxBytes-receiptEdgeDeleteWALHeaderSize)/
			(receiptEdgeDeleteWALItemSize+receiptEdgeDeleteWALAcceptSize) {
		return receiptEdgeDeleteWALCapacityError()
	}
	size := receiptEdgeDeleteWALHeaderSize +
		len(keys)*(receiptEdgeDeleteWALItemSize+receiptEdgeDeleteWALAcceptSize)
	for _, key := range keys {
		if err := validateReceiptEdgeDeleteWALKey(key); err != nil {
			return err
		}
		keyBytes := len(key.Tail) + len(key.Head)
		if keyBytes > (receiptEdgeDeleteWALMaxBytes-size)/2 {
			return receiptEdgeDeleteWALCapacityError()
		}
		size += 2 * keyBytes
	}
	return nil
}

// encodeReceiptEdgeDeleteWAL matches FileWAL's payload encoder signature.
// Fixed-width big-endian scalars and uint32 length-prefixed strings have one
// representation. The graph-only Mutation is checked and reconstructed from
// accepted indexed keys; protobuf wire serialization is not part of this
// private WAL format.
func encodeReceiptEdgeDeleteWAL(op mutationlog.MutationOp) ([]byte, error) {
	envelope, ok := op.(*edgeDeleteReceiptEnvelope)
	if !ok {
		return nil, receiptEdgeDeleteWALError("unexpected operation type %T", op)
	}
	size, err := validateReceiptEdgeDeleteWALEnvelope(envelope)
	if err != nil {
		return nil, err
	}
	b := make([]byte, 0, size)
	b = append(b, receiptEdgeDeleteWALMagic...)
	b = append(b, envelope.Origin[:]...)
	b = appendReceiptEdgeDeleteU64(b, envelope.OriginSeq)
	b = appendReceiptEdgeDeleteU64(b, uint64(envelope.HLC.WallNs))
	b = appendReceiptEdgeDeleteU32(b, envelope.HLC.Logical)
	b = append(b, envelope.HLC.NodeID[:]...)
	b = append(b, envelope.Epoch[:]...)
	b = append(b, envelope.PolicyFingerprint[:]...)
	b = appendReceiptEdgeDeleteU64(b, uint64(envelope.TombstoneExpiration.UnixNano()))
	issued := binary.BigEndian.Uint64(envelope.Receipts[0].ID[17:25])
	b = appendReceiptEdgeDeleteU64(b, uint64(envelope.Receipts[0].DeadlineMillis)-issued)
	b = appendReceiptEdgeDeleteU32(b, uint32(len(envelope.Receipts)))
	b = appendReceiptEdgeDeleteU32(b, uint32(len(envelope.Accepted)))
	for i, receipt := range envelope.Receipts {
		b = append(b, receipt.ID[:]...)
		b = append(b, receipt.Group[:]...)
		b = appendReceiptEdgeDeleteU32(b, receipt.Index)
		b = appendReceiptEdgeDeleteU32(b, receipt.Count)
		b = append(b, byte(receipt.Kind))
		b = append(b, receipt.Digest[:]...)
		b = appendReceiptEdgeDeleteU64(b, uint64(receipt.DeadlineMillis))
		b = append(b, receipt.Result[0])
		b = appendReceiptEdgeDeleteKey(b, envelope.OriginalKeys[i])
	}
	for _, accepted := range envelope.Accepted {
		b = appendReceiptEdgeDeleteU32(b, uint32(accepted.Index))
		b = appendReceiptEdgeDeleteKey(b, accepted.Key)
	}
	if len(b) != size {
		return nil, receiptEdgeDeleteWALError("encoded size drift")
	}
	return b, nil
}

// decodeReceiptEdgeDeleteWAL matches FileWAL's payload decoder signature.
// FileWAL verifies its frame CRC before invoking this decoder. All counts and
// string lengths are bounded before allocation; trailing bytes fail closed.
func decodeReceiptEdgeDeleteWAL(raw []byte) (mutationlog.MutationOp, error) {
	if len(raw) < receiptEdgeDeleteWALHeaderSize || len(raw) > receiptEdgeDeleteWALMaxBytes {
		return nil, receiptEdgeDeleteWALError("invalid payload size %d", len(raw))
	}
	r := receiptEdgeDeleteWALReader{raw: raw}
	magic, _ := r.take(len(receiptEdgeDeleteWALMagic))
	if !bytes.Equal(magic, []byte(receiptEdgeDeleteWALMagic)) {
		return nil, receiptEdgeDeleteWALError("unknown version or nonzero reserved header")
	}
	envelope := &edgeDeleteReceiptEnvelope{}
	copy(envelope.Origin[:], r.mustTake(16))
	envelope.OriginSeq = r.u64()
	envelope.HLC.WallNs = int64(r.u64())
	envelope.HLC.Logical = r.u32()
	copy(envelope.HLC.NodeID[:], r.mustTake(16))
	copy(envelope.Epoch[:], r.mustTake(16))
	copy(envelope.PolicyFingerprint[:], r.mustTake(32))
	envelope.TombstoneExpiration = time.Unix(0, int64(r.u64())).UTC()
	retentionMS := r.u64()
	count := r.u32()
	acceptedCount := r.u32()
	if count == 0 || count > uint32(r.remaining()/receiptEdgeDeleteWALItemSize) || acceptedCount > count {
		return nil, receiptEdgeDeleteWALError("invalid item or accepted count")
	}
	envelope.OriginalKeys = make([]graphcache.EdgeKey[string], count)
	envelope.Receipts = make([]mutationreceipt.Receipt, count)
	for i := range envelope.Receipts {
		var receipt mutationreceipt.Receipt
		if !r.copyInto(receipt.ID[:]) || !r.copyInto(receipt.Group[:]) {
			return nil, receiptEdgeDeleteWALError("truncated receipt %d", i)
		}
		receipt.Index = r.u32()
		receipt.Count = r.u32()
		receipt.Kind = mutationreceipt.Kind(r.u8())
		if !r.copyInto(receipt.Digest[:]) {
			return nil, receiptEdgeDeleteWALError("truncated digest %d", i)
		}
		receipt.DeadlineMillis = int64(r.u64())
		receipt.Result = []byte{r.u8()}
		key, err := r.key()
		if err != nil {
			return nil, err
		}
		envelope.Receipts[i], envelope.OriginalKeys[i] = receipt, key
	}
	envelope.Accepted = make([]graphcache.IndexedEdgeDelete[string], acceptedCount)
	for i := range envelope.Accepted {
		index := r.u32()
		key, err := r.key()
		if err != nil {
			return nil, err
		}
		envelope.Accepted[i] = graphcache.IndexedEdgeDelete[string]{Index: int(index), Key: key}
	}
	if r.bad || r.remaining() != 0 {
		return nil, receiptEdgeDeleteWALError("truncated or trailing bytes")
	}
	envelope.Mutation = receiptEdgeDeleteWALMutation(envelope)
	if _, err := validateReceiptEdgeDeleteWALEnvelope(envelope); err != nil {
		return nil, err
	}
	issued := binary.BigEndian.Uint64(envelope.Receipts[0].ID[17:25])
	if retentionMS != uint64(envelope.Receipts[0].DeadlineMillis)-issued {
		return nil, receiptEdgeDeleteWALError("retention and receipt deadline drift")
	}
	return envelope, nil
}

// validateReceiptEdgeDeleteWALEntry checks metadata that belongs to the
// enclosing FileWAL frame. Entry.Seq is relay-local and must not be compared
// with the origin-local OriginSeq.
func validateReceiptEdgeDeleteWALEntry(entry mutationlog.Entry) error {
	if entry.Seq == 0 {
		return receiptEdgeDeleteWALError("zero FileWAL local seq")
	}
	envelope, ok := entry.Op.(*edgeDeleteReceiptEnvelope)
	if !ok {
		return receiptEdgeDeleteWALError("unexpected operation type %T", entry.Op)
	}
	if !entry.HLC.Equal(envelope.HLC) {
		return receiptEdgeDeleteWALError("FileWAL HLC differs from envelope HLC")
	}
	_, err := validateReceiptEdgeDeleteWALEnvelope(envelope)
	return err
}

func validateReceiptEdgeDeleteWALEnvelope(e *edgeDeleteReceiptEnvelope) (int, error) {
	if e == nil || e.Origin == (hlc.NodeID{}) || e.OriginSeq == 0 ||
		e.HLC.WallNs <= 0 || e.HLC.NodeID != e.Origin ||
		e.Epoch == (mutationreceipt.Epoch{}) || e.PolicyFingerprint == ([32]byte{}) {
		return 0, receiptEdgeDeleteWALError("invalid origin, HLC, epoch, or policy metadata")
	}
	expirationNs := e.TombstoneExpiration.UnixNano()
	if e.TombstoneExpiration.IsZero() || expirationNs <= 0 ||
		!time.Unix(0, expirationNs).Equal(e.TombstoneExpiration) {
		return 0, receiptEdgeDeleteWALError("invalid tombstone expiration")
	}
	count := len(e.Receipts)
	if count == 0 || count > (receiptEdgeDeleteWALMaxBytes-receiptEdgeDeleteWALHeaderSize)/receiptEdgeDeleteWALItemSize ||
		len(e.OriginalKeys) != count || len(e.Accepted) > count {
		return 0, receiptEdgeDeleteWALError("invalid request alignment or item count")
	}
	if err := validateReceiptEdgeDeleteWALRequestCapacity(e.OriginalKeys); err != nil {
		return 0, err
	}
	size := receiptEdgeDeleteWALHeaderSize + count*receiptEdgeDeleteWALItemSize + len(e.Accepted)*receiptEdgeDeleteWALAcceptSize
	group := e.Receipts[0].Group
	if group == (mutationreceipt.GroupID{}) {
		return 0, receiptEdgeDeleteWALError("zero logical-call ID")
	}
	seen := make(map[mutationreceipt.ID]struct{}, count)
	var retentionMS int64
	for i, receipt := range e.Receipts {
		key := e.OriginalKeys[i]
		if err := validateReceiptEdgeDeleteWALKey(key); err != nil {
			return 0, err
		}
		if size > receiptEdgeDeleteWALMaxBytes-len(key.Tail)-len(key.Head) {
			return 0, receiptEdgeDeleteWALCapacityError()
		}
		size += len(key.Tail) + len(key.Head)
		if _, duplicate := seen[receipt.ID]; duplicate {
			return 0, receiptEdgeDeleteWALError("duplicate operation ID at item %d", i)
		}
		seen[receipt.ID] = struct{}{}
		id, err := mutationreceipt.DecodeID(receipt.ID[:])
		if err != nil || id != receipt.ID || !bytes.Equal(receipt.ID[1:17], e.Epoch[:]) {
			return 0, receiptEdgeDeleteWALError("invalid operation ID or epoch at item %d", i)
		}
		issued := binary.BigEndian.Uint64(receipt.ID[17:25])
		if issued > math.MaxInt64 || receipt.DeadlineMillis <= int64(issued) {
			return 0, receiptEdgeDeleteWALError("invalid receipt deadline at item %d", i)
		}
		horizon := receipt.DeadlineMillis - int64(issued)
		if horizon < int64(time.Hour/time.Millisecond) || horizon > int64((30*24*time.Hour)/time.Millisecond) ||
			(i != 0 && horizon != retentionMS) {
			return 0, receiptEdgeDeleteWALError("inconsistent retention at item %d", i)
		}
		retentionMS = horizon
		if receipt.Group != group || receipt.Index != uint32(i) || receipt.Count != uint32(count) ||
			receipt.Kind != mutationreceipt.DeleteEdge || receipt.HasContrib || receipt.ContribID != (mutationreceipt.ContribID{}) ||
			receipt.Digest != edgeDeleteDigest(key.Tail, key.Head) || len(receipt.Result) != 1 || receipt.Result[0] > 1 {
			return 0, receiptEdgeDeleteWALError("intent, index, or result drift at item %d", i)
		}
	}
	previous := -1
	for i, item := range e.Accepted {
		if item.Index <= previous || item.Index >= count || item.Index < 0 ||
			item.Key != e.OriginalKeys[item.Index] {
			return 0, receiptEdgeDeleteWALError("accepted index or key drift at item %d", i)
		}
		previous = item.Index
		key := item.Key
		if size > receiptEdgeDeleteWALMaxBytes-len(key.Tail)-len(key.Head) {
			return 0, receiptEdgeDeleteWALCapacityError()
		}
		size += len(key.Tail) + len(key.Head)
	}
	if size > receiptEdgeDeleteWALMaxBytes {
		return 0, receiptEdgeDeleteWALCapacityError()
	}
	if !proto.Equal(e.Mutation, receiptEdgeDeleteWALMutation(e)) {
		return 0, receiptEdgeDeleteWALError("graph projection drift")
	}
	return size, nil
}

func validateReceiptEdgeDeleteWALKey(key graphcache.EdgeKey[string]) error {
	if key.Tail == "" || key.Head == "" || !utf8.ValidString(key.Tail) || !utf8.ValidString(key.Head) ||
		len(key.Tail) > receiptEdgeDeleteWALMaxBytes || len(key.Head) > receiptEdgeDeleteWALMaxBytes {
		return receiptEdgeDeleteWALError("invalid edge identity")
	}
	return nil
}

func receiptEdgeDeleteWALMutation(e *edgeDeleteReceiptEnvelope) *pb.Mutation {
	accepted := make([]*pb.EdgeKey, len(e.Accepted))
	for i, item := range e.Accepted {
		accepted[i] = &pb.EdgeKey{Tail: item.Key.Tail, Head: item.Key.Head}
	}
	return &pb.Mutation{
		Origin: append([]byte(nil), e.Origin[:]...), Seq: e.OriginSeq, Hlc: hlcToProto(e.HLC),
		Op:                  &pb.MutationOp{Op: &pb.MutationOp_DeleteEdges{DeleteEdges: &pb.DeleteEdgesRequest{Edges: accepted}}},
		TombstoneExpiration: timestamppb.New(e.TombstoneExpiration),
	}
}

func appendReceiptEdgeDeleteU32(b []byte, v uint32) []byte {
	return binary.BigEndian.AppendUint32(b, v)
}
func appendReceiptEdgeDeleteU64(b []byte, v uint64) []byte {
	return binary.BigEndian.AppendUint64(b, v)
}

func appendReceiptEdgeDeleteKey(b []byte, key graphcache.EdgeKey[string]) []byte {
	b = appendReceiptEdgeDeleteU32(b, uint32(len(key.Tail)))
	b = append(b, key.Tail...)
	b = appendReceiptEdgeDeleteU32(b, uint32(len(key.Head)))
	return append(b, key.Head...)
}

type receiptEdgeDeleteWALReader struct {
	raw []byte
	off int
	bad bool
}

func (r *receiptEdgeDeleteWALReader) remaining() int { return len(r.raw) - r.off }

func (r *receiptEdgeDeleteWALReader) take(n int) ([]byte, bool) {
	if r.bad || n < 0 || n > r.remaining() {
		r.bad = true
		return nil, false
	}
	b := r.raw[r.off : r.off+n]
	r.off += n
	return b, true
}

func (r *receiptEdgeDeleteWALReader) mustTake(n int) []byte {
	b, _ := r.take(n)
	return b
}

func (r *receiptEdgeDeleteWALReader) copyInto(dst []byte) bool {
	b, ok := r.take(len(dst))
	if ok {
		copy(dst, b)
	}
	return ok
}

func (r *receiptEdgeDeleteWALReader) u8() byte {
	b := r.mustTake(1)
	if b == nil {
		return 0
	}
	return b[0]
}

func (r *receiptEdgeDeleteWALReader) u32() uint32 {
	b := r.mustTake(4)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint32(b)
}

func (r *receiptEdgeDeleteWALReader) u64() uint64 {
	b := r.mustTake(8)
	if b == nil {
		return 0
	}
	return binary.BigEndian.Uint64(b)
}

func (r *receiptEdgeDeleteWALReader) key() (graphcache.EdgeKey[string], error) {
	tailLen := r.u32()
	if tailLen == 0 || tailLen > uint32(r.remaining()) {
		return graphcache.EdgeKey[string]{}, receiptEdgeDeleteWALError("invalid tail length")
	}
	tail := string(r.mustTake(int(tailLen)))
	headLen := r.u32()
	if headLen == 0 || headLen > uint32(r.remaining()) {
		return graphcache.EdgeKey[string]{}, receiptEdgeDeleteWALError("invalid head length")
	}
	head := string(r.mustTake(int(headLen)))
	key := graphcache.EdgeKey[string]{Tail: tail, Head: head}
	return key, validateReceiptEdgeDeleteWALKey(key)
}
