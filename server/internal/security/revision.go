package security

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"math"

	"github.com/anaregdesign/lantern/server/internal/keyspace"
)

const revisionPrefix = "LNSEC01\n" + keyspace.Version + "\x00"
const revisionHeaderBytes = len(revisionPrefix) + 16 + 32 + 8 + 32 + 16 + 4

var ErrInvalidRevision = errors.New("invalid signed security revision")

// Revision is a signed complete image from the original security writer. Its
// private fields and owned encoding prevent mutation after validation.
type Revision struct {
	generation [16]byte
	writer     [32]byte
	sequence   uint64
	previous   [32]byte
	changeID   [16]byte
	snapshot   *Snapshot
	encoded    []byte
	digest     [32]byte
}

// SignRevision is used only by the pinned writer, after complete image
// validation. Generation and change ID are random operator/transaction IDs.
func SignRevision(generation [16]byte, sequence uint64, previous [32]byte,
	changeID [16]byte, snapshot *Snapshot, privateKey ed25519.PrivateKey) (*Revision, error) {
	if len(privateKey) != ed25519.PrivateKeySize ||
		!bytes.Equal(privateKey, ed25519.NewKeyFromSeed(privateKey[:ed25519.SeedSize])) ||
		!validRevisionHeader(generation, sequence, previous, changeID) || snapshot == nil {
		return nil, ErrInvalidRevision
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	writer := sha256.Sum256(publicKey)
	encoded := make([]byte, 0, revisionHeaderBytes+len(snapshot.image)+ed25519.SignatureSize)
	encoded = append(encoded, revisionPrefix...)
	encoded = append(encoded, generation[:]...)
	encoded = append(encoded, writer[:]...)
	encoded = binary.BigEndian.AppendUint64(encoded, sequence)
	encoded = append(encoded, previous[:]...)
	encoded = append(encoded, changeID[:]...)
	encoded = binary.BigEndian.AppendUint32(encoded, uint32(len(snapshot.image)))
	encoded = append(encoded, snapshot.image...)
	signature := ed25519.Sign(privateKey, encoded)
	encoded = append(encoded, signature...)
	return &Revision{generation: generation, writer: writer, sequence: sequence,
		previous: previous, changeID: changeID, snapshot: snapshot,
		encoded: encoded, digest: sha256.Sum256(encoded)}, nil
}

// DecodeRevision authenticates bounded bytes before decoding the image. The
// pinned key comes from operator authority, never from the relaying peer.
func DecodeRevision(encoded []byte, pinnedKey ed25519.PublicKey, limits PolicyLimits) (*Revision, error) {
	if len(pinnedKey) != ed25519.PublicKeySize || len(encoded) <= revisionHeaderBytes+ed25519.SignatureSize ||
		len(encoded) > revisionHeaderBytes+MaxImageBytes+ed25519.SignatureSize || !bytes.HasPrefix(encoded, []byte(revisionPrefix)) {
		return nil, ErrInvalidRevision
	}
	unsigned := encoded[:len(encoded)-ed25519.SignatureSize]
	if !ed25519.Verify(pinnedKey, unsigned, encoded[len(unsigned):]) {
		return nil, ErrInvalidRevision
	}
	revision := &Revision{}
	offset := len(revisionPrefix)
	copy(revision.generation[:], encoded[offset:offset+16])
	offset += 16
	copy(revision.writer[:], encoded[offset:offset+32])
	offset += 32
	revision.sequence = binary.BigEndian.Uint64(encoded[offset : offset+8])
	offset += 8
	copy(revision.previous[:], encoded[offset:offset+32])
	offset += 32
	copy(revision.changeID[:], encoded[offset:offset+16])
	offset += 16
	imageBytes := binary.BigEndian.Uint32(encoded[offset : offset+4])
	offset += 4
	if revision.writer != sha256.Sum256(pinnedKey) || int64(imageBytes) != int64(len(unsigned)-offset) ||
		!validRevisionHeader(revision.generation, revision.sequence, revision.previous, revision.changeID) {
		return nil, ErrInvalidRevision
	}
	snapshot, err := DecodeImage(unsigned[offset:], limits)
	if err != nil {
		return nil, err
	}
	revision.snapshot = snapshot
	revision.encoded = bytes.Clone(encoded)
	revision.digest = sha256.Sum256(encoded)
	return revision, nil
}

func validRevisionHeader(generation [16]byte, sequence uint64, previous [32]byte, changeID [16]byte) bool {
	return generation != [16]byte{} && changeID != [16]byte{} && sequence != 0 && sequence != math.MaxUint64 &&
		(sequence == 1 && previous == [32]byte{} || sequence > 1 && previous != [32]byte{})
}

func (r *Revision) Generation() [16]byte { return r.generation }
func (r *Revision) Sequence() uint64     { return r.sequence }
func (r *Revision) Digest() [32]byte     { return r.digest }
func (r *Revision) Snapshot() *Snapshot  { return r.snapshot }
func (r *Revision) Encode() []byte       { return bytes.Clone(r.encoded) }
