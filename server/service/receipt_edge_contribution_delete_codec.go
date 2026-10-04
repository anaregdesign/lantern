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

const (
	receiptEdgeContributionDeleteWALMagic      = "LRCD\x01\x00\x00\x00"
	receiptEdgeContributionDeleteWALMaxBytes   = 8 << 20
	receiptEdgeContributionDeleteWALHeaderSize = 8 + 16 + 8 + 8 + 4 + 16 + 16 + 32 + 8 + 8 + 4 + 4
	receiptEdgeContributionDeleteWALItemSize   = 49 + 16 + 4 + 4 + 1 + 32 + 8 + 1 + 4 + 4 + 24
	receiptEdgeContributionDeleteWALAcceptSize = 4 + 4 + 4 + 24
)

var (
	errReceiptEdgeContributionDeleteWAL         = errors.New("service: invalid receipt contribution Delete WAL payload")
	errReceiptEdgeContributionDeleteWALCapacity = errors.New("service: receipt contribution Delete WAL payload exceeds capacity")
)

func receiptEdgeContributionDeleteWALError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errReceiptEdgeContributionDeleteWAL, fmt.Sprintf(format, args...))
}

func receiptEdgeContributionDeleteWALCapacityError() error {
	return errors.Join(errReceiptEdgeContributionDeleteWAL, errReceiptEdgeContributionDeleteWALCapacity)
}

func validateReceiptEdgeContributionDeleteWALKey(key graphcache.EdgeContributionKey[string]) error {
	if key.Tail == "" || key.Head == "" || !utf8.ValidString(key.Tail) || !utf8.ValidString(key.Head) ||
		key.ContribID.IsZero() ||
		len(key.Tail) > receiptEdgeContributionDeleteWALMaxBytes ||
		len(key.Head) > receiptEdgeContributionDeleteWALMaxBytes {
		return receiptEdgeContributionDeleteWALError("invalid contribution identity")
	}
	return nil
}

// Bound the all-accepted WAL before preparing the graph; relays may accept
// more positions than the origin without changing the original results.
func validateReceiptEdgeContributionDeleteWALRequestCapacity(
	keys []graphcache.EdgeContributionKey[string],
) error {
	if len(keys) == 0 ||
		len(keys) > (receiptEdgeContributionDeleteWALMaxBytes-receiptEdgeContributionDeleteWALHeaderSize)/
			(receiptEdgeContributionDeleteWALItemSize+receiptEdgeContributionDeleteWALAcceptSize) {
		return receiptEdgeContributionDeleteWALCapacityError()
	}
	size := receiptEdgeContributionDeleteWALHeaderSize +
		len(keys)*(receiptEdgeContributionDeleteWALItemSize+receiptEdgeContributionDeleteWALAcceptSize)
	for _, key := range keys {
		if err := validateReceiptEdgeContributionDeleteWALKey(key); err != nil {
			return err
		}
		keyBytes := len(key.Tail) + len(key.Head)
		if keyBytes > (receiptEdgeContributionDeleteWALMaxBytes-size)/2 {
			return receiptEdgeContributionDeleteWALCapacityError()
		}
		size += 2 * keyBytes
	}
	return nil
}

func encodeReceiptEdgeContributionDeleteWAL(op mutationlog.MutationOp) ([]byte, error) {
	e, ok := op.(*edgeContributionDeleteReceiptEnvelope)
	if !ok {
		return nil, receiptEdgeContributionDeleteWALError("unexpected operation type %T", op)
	}
	size, err := validateReceiptEdgeContributionDeleteWALEnvelope(e)
	if err != nil {
		return nil, err
	}
	b := make([]byte, 0, size)
	magic := receiptEdgeContributionDeleteWALMagic
	if e.NamespaceFormat != "" {
		magic = "LRCD\x02\x00\x00\x00"
	}
	b = append(b, magic...)
	b = append(b, e.Origin[:]...)
	b = binary.BigEndian.AppendUint64(b, e.OriginSeq)
	b = binary.BigEndian.AppendUint64(b, uint64(e.HLC.WallNs))
	b = binary.BigEndian.AppendUint32(b, e.HLC.Logical)
	b = append(b, e.HLC.NodeID[:]...)
	b = append(b, e.Epoch[:]...)
	b = append(b, e.PolicyFingerprint[:]...)
	b = binary.BigEndian.AppendUint64(b, uint64(e.TombstoneExpiration.UnixNano()))
	issued := binary.BigEndian.Uint64(e.Receipts[0].ID[17:25])
	b = binary.BigEndian.AppendUint64(b, uint64(e.Receipts[0].DeadlineMillis)-issued)
	b = binary.BigEndian.AppendUint32(b, uint32(len(e.Receipts)))
	b = binary.BigEndian.AppendUint32(b, uint32(len(e.Accepted)))
	for i, receipt := range e.Receipts {
		b = append(b, receipt.ID[:]...)
		b = append(b, receipt.Group[:]...)
		b = binary.BigEndian.AppendUint32(b, receipt.Index)
		b = binary.BigEndian.AppendUint32(b, receipt.Count)
		b = append(b, byte(receipt.Kind))
		b = append(b, receipt.Digest[:]...)
		b = binary.BigEndian.AppendUint64(b, uint64(receipt.DeadlineMillis))
		b = append(b, receipt.Result[0])
		b = appendReceiptEdgeContributionDeleteKey(b, e.OriginalKeys[i])
	}
	for _, accepted := range e.Accepted {
		b = binary.BigEndian.AppendUint32(b, uint32(accepted.Index))
		b = appendReceiptEdgeContributionDeleteKey(b, accepted.Key)
	}
	if len(b) != size {
		return nil, receiptEdgeContributionDeleteWALError("encoded size drift")
	}
	return b, nil
}

func decodeReceiptEdgeContributionDeleteWAL(raw []byte) (mutationlog.MutationOp, error) {
	if len(raw) < receiptEdgeContributionDeleteWALHeaderSize || len(raw) > receiptEdgeContributionDeleteWALMaxBytes {
		return nil, receiptEdgeContributionDeleteWALError("invalid payload size %d", len(raw))
	}
	r := receiptEdgeContributionDeleteWALReader{receiptEdgeDeleteWALReader: receiptEdgeDeleteWALReader{raw: raw}}
	magic := r.mustTake(8)
	if !bytes.Equal(magic, []byte(receiptEdgeContributionDeleteWALMagic)) && !bytes.Equal(magic, []byte("LRCD\x02\x00\x00\x00")) {
		return nil, receiptEdgeContributionDeleteWALError("unknown version or nonzero reserved header")
	}
	e := &edgeContributionDeleteReceiptEnvelope{}
	if magic[4] == 2 {
		e.NamespaceFormat = "namespaced-v1"
	}
	copy(e.Origin[:], r.mustTake(16))
	e.OriginSeq = r.u64()
	e.HLC.WallNs = int64(r.u64())
	e.HLC.Logical = r.u32()
	copy(e.HLC.NodeID[:], r.mustTake(16))
	copy(e.Epoch[:], r.mustTake(16))
	copy(e.PolicyFingerprint[:], r.mustTake(32))
	e.TombstoneExpiration = time.Unix(0, int64(r.u64())).UTC()
	retentionMS := r.u64()
	count, acceptedCount := r.u32(), r.u32()
	if count == 0 || count > uint32(r.remaining()/receiptEdgeContributionDeleteWALItemSize) ||
		acceptedCount > count {
		return nil, receiptEdgeContributionDeleteWALError("invalid item or accepted count")
	}
	e.OriginalKeys = make([]graphcache.EdgeContributionKey[string], count)
	e.Receipts = make([]mutationreceipt.Receipt, count)
	for i := range e.Receipts {
		var receipt mutationreceipt.Receipt
		if !r.copyInto(receipt.ID[:]) || !r.copyInto(receipt.Group[:]) {
			return nil, receiptEdgeContributionDeleteWALError("truncated receipt %d", i)
		}
		receipt.Index, receipt.Count = r.u32(), r.u32()
		receipt.Kind = mutationreceipt.Kind(r.u8())
		if !r.copyInto(receipt.Digest[:]) {
			return nil, receiptEdgeContributionDeleteWALError("truncated digest %d", i)
		}
		receipt.DeadlineMillis = int64(r.u64())
		receipt.Result = []byte{r.u8()}
		key, err := r.key()
		if err != nil {
			return nil, err
		}
		receipt.Resource, err = receiptResourceIdentity(e.NamespaceFormat, key.Tail, key.Head)
		if err != nil {
			return nil, receiptEdgeContributionDeleteWALError("invalid original resource identity")
		}
		e.Receipts[i], e.OriginalKeys[i] = receipt, key
	}
	e.Accepted = make([]graphcache.IndexedEdgeContributionDelete[string], acceptedCount)
	for i := range e.Accepted {
		index := r.u32()
		key, err := r.key()
		if err != nil {
			return nil, err
		}
		e.Accepted[i] = graphcache.IndexedEdgeContributionDelete[string]{Index: int(index), Key: key}
	}
	if r.bad || r.remaining() != 0 {
		return nil, receiptEdgeContributionDeleteWALError("truncated or trailing bytes")
	}
	e.Mutation = receiptEdgeContributionDeleteWALMutation(e)
	if _, err := validateReceiptEdgeContributionDeleteWALEnvelope(e); err != nil {
		return nil, err
	}
	issued := binary.BigEndian.Uint64(e.Receipts[0].ID[17:25])
	if retentionMS != uint64(e.Receipts[0].DeadlineMillis)-issued {
		return nil, receiptEdgeContributionDeleteWALError("retention and receipt deadline drift")
	}
	return e, nil
}

func validateReceiptEdgeContributionDeleteWALEntry(entry mutationlog.Entry) error {
	if entry.Seq == 0 {
		return receiptEdgeContributionDeleteWALError("zero FileWAL local seq")
	}
	e, ok := entry.Op.(*edgeContributionDeleteReceiptEnvelope)
	if !ok {
		return receiptEdgeContributionDeleteWALError("unexpected operation type %T", entry.Op)
	}
	if !entry.HLC.Equal(e.HLC) {
		return receiptEdgeContributionDeleteWALError("FileWAL HLC differs from envelope HLC")
	}
	_, err := validateReceiptEdgeContributionDeleteWALEnvelope(e)
	return err
}

func validateReceiptEdgeContributionDeleteWALEnvelope(
	e *edgeContributionDeleteReceiptEnvelope,
) (int, error) {
	if e == nil || validateDataFormat(e.NamespaceFormat) != nil || e.Origin == (hlc.NodeID{}) || e.OriginSeq == 0 ||
		e.HLC.WallNs <= 0 || e.HLC.NodeID != e.Origin ||
		e.Epoch == (mutationreceipt.Epoch{}) || e.PolicyFingerprint == ([32]byte{}) {
		return 0, receiptEdgeContributionDeleteWALError("invalid origin, HLC, epoch, or policy metadata")
	}
	expirationNs := e.TombstoneExpiration.UnixNano()
	if e.TombstoneExpiration.IsZero() || expirationNs <= 0 ||
		!time.Unix(0, expirationNs).Equal(e.TombstoneExpiration) {
		return 0, receiptEdgeContributionDeleteWALError("invalid tombstone expiration")
	}
	count := len(e.Receipts)
	if count == 0 || count > (receiptEdgeContributionDeleteWALMaxBytes-receiptEdgeContributionDeleteWALHeaderSize)/
		receiptEdgeContributionDeleteWALItemSize || len(e.OriginalKeys) != count || len(e.Accepted) > count {
		return 0, receiptEdgeContributionDeleteWALError("invalid request alignment or item count")
	}
	if err := validateReceiptEdgeContributionDeleteWALRequestCapacity(e.OriginalKeys); err != nil {
		return 0, err
	}
	size := receiptEdgeContributionDeleteWALHeaderSize + count*receiptEdgeContributionDeleteWALItemSize +
		len(e.Accepted)*receiptEdgeContributionDeleteWALAcceptSize
	group := e.Receipts[0].Group
	if group == (mutationreceipt.GroupID{}) {
		return 0, receiptEdgeContributionDeleteWALError("zero logical-call ID")
	}
	seen := make(map[mutationreceipt.ID]struct{}, count)
	var retentionMS int64
	for i, receipt := range e.Receipts {
		key := e.OriginalKeys[i]
		if err := validateReceiptDataIdentity(e.NamespaceFormat, key.Tail, key.Head); err != nil {
			return 0, receiptEdgeContributionDeleteWALError("invalid physical identity")
		}
		if !receiptResourceMatches(receipt, e.NamespaceFormat, key.Tail, key.Head) {
			return 0, receiptEdgeContributionDeleteWALError("original resource provenance drift")
		}
		if err := validateReceiptEdgeContributionDeleteWALKey(key); err != nil {
			return 0, err
		}
		if size > receiptEdgeContributionDeleteWALMaxBytes-len(key.Tail)-len(key.Head) {
			return 0, receiptEdgeContributionDeleteWALCapacityError()
		}
		size += len(key.Tail) + len(key.Head)
		if _, duplicate := seen[receipt.ID]; duplicate {
			return 0, receiptEdgeContributionDeleteWALError("duplicate operation ID at item %d", i)
		}
		seen[receipt.ID] = struct{}{}
		id, err := mutationreceipt.DecodeID(receipt.ID[:])
		if err != nil || id != receipt.ID || !bytes.Equal(receipt.ID[1:17], e.Epoch[:]) {
			return 0, receiptEdgeContributionDeleteWALError("invalid operation ID or epoch at item %d", i)
		}
		issued := binary.BigEndian.Uint64(receipt.ID[17:25])
		if issued > math.MaxInt64 || receipt.DeadlineMillis <= int64(issued) {
			return 0, receiptEdgeContributionDeleteWALError("invalid receipt deadline at item %d", i)
		}
		horizon := receipt.DeadlineMillis - int64(issued)
		if horizon < int64(time.Hour/time.Millisecond) ||
			horizon > int64((30*24*time.Hour)/time.Millisecond) ||
			(i != 0 && horizon != retentionMS) {
			return 0, receiptEdgeContributionDeleteWALError("inconsistent retention at item %d", i)
		}
		retentionMS = horizon
		if receipt.Group != group || receipt.Index != uint32(i) || receipt.Count != uint32(count) ||
			receipt.Kind != mutationreceipt.DeleteEdgeContribution || receipt.HasContrib || receipt.LifecycleReduction ||
			receipt.ContribID != (mutationreceipt.ContribID{}) ||
			receipt.Digest != edgeContributionDeleteDigest(key, e.NamespaceFormat) ||
			len(receipt.Result) != 1 || receipt.Result[0] > 1 {
			return 0, receiptEdgeContributionDeleteWALError("intent, index, or result drift at item %d", i)
		}
	}
	previous := -1
	for i, item := range e.Accepted {
		if item.Index <= previous || item.Index >= count || item.Index < 0 ||
			item.Key != e.OriginalKeys[item.Index] {
			return 0, receiptEdgeContributionDeleteWALError("accepted index or key drift at item %d", i)
		}
		previous = item.Index
		key := item.Key
		if size > receiptEdgeContributionDeleteWALMaxBytes-len(key.Tail)-len(key.Head) {
			return 0, receiptEdgeContributionDeleteWALCapacityError()
		}
		size += len(key.Tail) + len(key.Head)
	}
	if size > receiptEdgeContributionDeleteWALMaxBytes {
		return 0, receiptEdgeContributionDeleteWALCapacityError()
	}
	if !proto.Equal(e.Mutation, receiptEdgeContributionDeleteWALMutation(e)) {
		return 0, receiptEdgeContributionDeleteWALError("graph projection drift")
	}
	return size, nil
}

func receiptEdgeContributionDeleteWALMutation(
	e *edgeContributionDeleteReceiptEnvelope,
) *pb.Mutation {
	accepted := make([]*pb.EdgeContributionKey, len(e.Accepted))
	for i, item := range e.Accepted {
		accepted[i] = receiptEdgeContributionDeleteWireKey(item.Key)
	}
	return &pb.Mutation{
		NamespaceFormat: e.NamespaceFormat,
		Origin:          append([]byte(nil), e.Origin[:]...), Seq: e.OriginSeq, Hlc: hlcToProto(e.HLC),
		Op: &pb.MutationOp{Op: &pb.MutationOp_DeleteEdgeContributions{
			DeleteEdgeContributions: &pb.DeleteEdgeContributionsRequest{Contributions: accepted},
		}},
		TombstoneExpiration: timestamppb.New(e.TombstoneExpiration),
	}
}

func receiptEdgeContributionDeleteWireKey(
	key graphcache.EdgeContributionKey[string],
) *pb.EdgeContributionKey {
	return &pb.EdgeContributionKey{
		Tail: key.Tail, Head: key.Head, ContribId: append([]byte(nil), key.ContribID[:]...),
	}
}

func appendReceiptEdgeContributionDeleteKey(
	b []byte,
	key graphcache.EdgeContributionKey[string],
) []byte {
	b = binary.BigEndian.AppendUint32(b, uint32(len(key.Tail)))
	b = append(b, key.Tail...)
	b = binary.BigEndian.AppendUint32(b, uint32(len(key.Head)))
	b = append(b, key.Head...)
	return append(b, key.ContribID[:]...)
}

type receiptEdgeContributionDeleteWALReader struct {
	receiptEdgeDeleteWALReader
}

func (r *receiptEdgeContributionDeleteWALReader) key() (graphcache.EdgeContributionKey[string], error) {
	tailLen := r.u32()
	if tailLen == 0 || tailLen > uint32(r.remaining()) {
		return graphcache.EdgeContributionKey[string]{},
			receiptEdgeContributionDeleteWALError("invalid tail length")
	}
	tail := string(r.mustTake(int(tailLen)))
	headLen := r.u32()
	if headLen == 0 || headLen > uint32(r.remaining()) {
		return graphcache.EdgeContributionKey[string]{},
			receiptEdgeContributionDeleteWALError("invalid head length")
	}
	head := string(r.mustTake(int(headLen)))
	var contribID graphcache.ContribID
	if !r.copyInto(contribID[:]) {
		return graphcache.EdgeContributionKey[string]{},
			receiptEdgeContributionDeleteWALError("truncated ContribID")
	}
	key := graphcache.EdgeContributionKey[string]{Tail: tail, Head: head, ContribID: contribID}
	return key, validateReceiptEdgeContributionDeleteWALKey(key)
}
