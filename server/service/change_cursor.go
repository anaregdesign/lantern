package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"sort"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

const changeCursorPlainBytes = 4096
const maxChangeOrigins = 128
const changeCursorLifetime = 10 * time.Minute

// Compatible HA clocks may differ by this qualified bound. This tolerance
// never restarts a cursor lifetime or provides authentication authority.
const changeCursorClockMargin = 2 * time.Second

// ChangeCursorKey is operator-shared across compatible replicas. Key bytes and
// per-origin progress never appear in public capabilities or diagnostics.
type ChangeCursorKey struct {
	Version uint32
	Key     [32]byte
}

type changeCursorCodec struct {
	current uint32
	keys    map[uint32]cipher.AEAD
}

func newChangeCursorCodec(keys []ChangeCursorKey, current uint32) (*changeCursorCodec, error) {
	if len(keys) == 0 || len(keys) > 4 || current == 0 {
		return nil, errors.New("CDC requires a bounded versioned cursor key ring")
	}
	codec := &changeCursorCodec{current: current, keys: make(map[uint32]cipher.AEAD, len(keys))}
	for _, key := range keys {
		if key.Version == 0 || key.Key == [32]byte{} || codec.keys[key.Version] != nil {
			return nil, errors.New("invalid CDC cursor key ring")
		}
		block, err := aes.NewCipher(key.Key[:])
		if err != nil {
			return nil, err
		}
		codec.keys[key.Version], err = cipher.NewGCM(block)
		if err != nil {
			return nil, err
		}
	}
	if codec.keys[current] == nil {
		return nil, errors.New("missing current CDC cursor key")
	}
	return codec, nil
}

func changeRequestBinding(req *pb.WatchChangesRequest) [32]byte {
	// Prefix is the exact literal string; no normalization or wildcard syntax.
	payload := append([]byte("lantern.cdc.scope.v1\x00"), byte(req.GetProjection()))
	payload = append(payload, req.GetPrefix()...)
	return sha256.Sum256(payload)
}

func changeCursorAAD(version uint32) []byte {
	aad := []byte("lantern.cdc.cursor.v1\x00")
	return binary.BigEndian.AppendUint32(aad, version)
}

func (c *changeCursorCodec) seal(binding, request [32]byte, next map[string]uint64, now time.Time) ([]byte, error) {
	if len(next) > maxChangeOrigins {
		return nil, changeGapError()
	}
	plain := make([]byte, changeCursorPlainBytes)
	plain[0] = 1
	copy(plain[1:33], binding[:])
	copy(plain[33:65], request[:])
	binary.BigEndian.PutUint64(plain[65:73], uint64(now.Add(changeCursorLifetime).Unix()))
	binary.BigEndian.PutUint16(plain[73:75], uint16(len(next)))
	origins := make([]string, 0, len(next))
	for origin := range next {
		origins = append(origins, origin)
	}
	sort.Strings(origins)
	offset := 75
	for _, origin := range origins {
		decoded, err := hex.DecodeString(origin)
		if err != nil || len(decoded) != 16 || hex.EncodeToString(decoded) != origin || zeroNodeID(decoded) || next[origin] == 0 {
			return nil, changeGapError()
		}
		copy(plain[offset:offset+16], decoded)
		binary.BigEndian.PutUint64(plain[offset+16:offset+24], next[origin])
		offset += 24
	}
	aead := c.keys[c.current]
	output := make([]byte, 4+aead.NonceSize(), 4+aead.NonceSize()+len(plain)+aead.Overhead())
	binary.BigEndian.PutUint32(output[:4], c.current)
	if _, err := rand.Read(output[4:]); err != nil {
		return nil, err
	}
	return aead.Seal(output, output[4:], plain, changeCursorAAD(c.current)), nil
}

func (c *changeCursorCodec) open(encoded []byte, binding, request [32]byte, now time.Time) (map[string]uint64, error) {
	if len(encoded) < 4 {
		return nil, changeGapError()
	}
	version := binary.BigEndian.Uint32(encoded[:4])
	aead := c.keys[version]
	if aead == nil || len(encoded) != 4+aead.NonceSize()+changeCursorPlainBytes+aead.Overhead() {
		return nil, changeGapError()
	}
	plain, err := aead.Open(nil, encoded[4:4+aead.NonceSize()], encoded[4+aead.NonceSize():], changeCursorAAD(version))
	if err != nil || plain[0] != 1 || string(plain[1:33]) != string(binding[:]) || string(plain[33:65]) != string(request[:]) {
		return nil, changeGapError()
	}
	expiry := int64(binary.BigEndian.Uint64(plain[65:73]))
	if now.Unix() >= expiry || expiry > now.Add(changeCursorLifetime+changeCursorClockMargin).Unix() {
		return nil, changeGapError()
	}
	count := int(binary.BigEndian.Uint16(plain[73:75]))
	if count > maxChangeOrigins {
		return nil, changeGapError()
	}
	next := make(map[string]uint64, count)
	offset, previous := 75, ""
	for range count {
		origin := hex.EncodeToString(plain[offset : offset+16])
		seq := binary.BigEndian.Uint64(plain[offset+16 : offset+24])
		if seq == 0 || zeroNodeID(plain[offset:offset+16]) || origin <= previous {
			return nil, changeGapError()
		}
		next[origin], previous = seq, origin
		offset += 24
	}
	for _, padding := range plain[offset:] {
		if padding != 0 {
			return nil, changeGapError()
		}
	}
	return next, nil
}
