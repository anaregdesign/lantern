// Package mutationreceipt provides bounded, in-memory bookkeeping for the
// mutation receipts specified by ADR 0010. It does not publish mutations,
// write a WAL, or provide an RPC. A caller must keep its graph, log, and
// receipt cuts under one commit gate before enabling receipt-capable writes.
package mutationreceipt

import (
	"encoding/binary"
	"errors"
	"time"
)

const (
	idVersion = byte(1)
	idSize    = 1 + 16 + 8 + 24
)

// Epoch is the deployment-wide random cluster identity. It is not an auth
// principal and must survive token rotation.
type Epoch [16]byte

// GroupID identifies one logical call containing request-index-aligned items.
type GroupID [16]byte

// ContribID identifies an Add contribution independently of its operation ID.
type ContribID [24]byte

// ID is the 49-byte v1 operation identity: version, epoch, UTC issuance
// milliseconds in network byte order, and 24 random bytes.
type ID [idSize]byte

var ErrInvalidID = errors.New("mutationreceipt: invalid operation ID")

// NewID encodes caller-generated randomness. It never generates entropy on
// behalf of a client, which must persist the returned ID before first send.
func NewID(epoch Epoch, issuedAt time.Time, random [24]byte) (ID, error) {
	if epoch == (Epoch{}) || random == ([24]byte{}) {
		return ID{}, ErrInvalidID
	}
	ms := issuedAt.UnixMilli()
	if ms < 0 {
		return ID{}, ErrInvalidID
	}
	var id ID
	id[0] = idVersion
	copy(id[1:17], epoch[:])
	binary.BigEndian.PutUint64(id[17:25], uint64(ms))
	copy(id[25:], random[:])
	return id, nil
}

// DecodeID rejects malformed, zero-entropy and unknown-version byte strings.
// Freshness and epoch membership depend on the store policy and are checked
// again when an ID is used.
func DecodeID(raw []byte) (ID, error) {
	if len(raw) != idSize {
		return ID{}, ErrInvalidID
	}
	var id ID
	copy(id[:], raw)
	if _, _, err := id.parts(); err != nil {
		return ID{}, err
	}
	return id, nil
}

// Bytes returns a copy of the wire representation.
func (id ID) Bytes() []byte { return append([]byte(nil), id[:]...) }

func (id ID) parts() (Epoch, int64, error) {
	var epoch Epoch
	if id[0] != idVersion {
		return epoch, 0, ErrInvalidID
	}
	copy(epoch[:], id[1:17])
	if epoch == (Epoch{}) {
		return epoch, 0, ErrInvalidID
	}
	var random [24]byte
	copy(random[:], id[25:])
	if random == ([24]byte{}) {
		return epoch, 0, ErrInvalidID
	}
	issued := binary.BigEndian.Uint64(id[17:25])
	if issued > uint64(^uint64(0)>>1) {
		return epoch, 0, ErrInvalidID
	}
	return epoch, int64(issued), nil
}
