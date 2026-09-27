package main

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"io"
	"math"
)

const (
	custodyMagic      = "LNTCUST1"
	custodyVersion    = 1
	custodyCipher     = 1 // AES-256-GCM, 96-bit nonce, 128-bit tag
	custodyChunkSize  = 1 << 20
	maxDataFrames     = 1<<32 - 1
	maxWrappedKeySize = 4096
	headerSize        = 8 + 1 + 1 + 4 + sha256.Size + 4 + 2
	frameHeaderSize   = 8 + 1 + 4
	endSize           = 8 + 8 + sha256.Size
	dataFrame         = 1
	endFrame          = 2
)

type keyWrapper interface {
	ID() string
	Wrap(context.Context, []byte) ([]byte, error)
	Unwrap(context.Context, []byte) ([]byte, error)
}

type fileResult struct {
	size   uint64
	digest [sha256.Size]byte
}

type custodyHeader struct {
	raw     []byte
	prefix  [4]byte
	wrapped []byte
}

func encryptStream(ctx context.Context, dst io.Writer, src io.Reader, keys keyWrapper, expected [sha256.Size]byte) (fileResult, error) {
	var dek [32]byte
	if _, err := rand.Read(dek[:]); err != nil {
		return fileResult{}, errors.New("generate data key failed")
	}
	defer clear(dek[:])

	var prefix [4]byte
	if _, err := rand.Read(prefix[:]); err != nil {
		return fileResult{}, errors.New("generate nonce failed")
	}
	wrapped, err := keys.Wrap(ctx, dek[:])
	if err != nil {
		return fileResult{}, errors.New("wrap data key failed")
	}
	header, err := newHeader(keys.ID(), prefix, wrapped)
	if err != nil {
		return fileResult{}, err
	}
	if err := writeExact(dst, header.raw); err != nil {
		return fileResult{}, errors.New("write custody header failed")
	}
	aead, err := newAEAD(dek[:])
	if err != nil {
		return fileResult{}, err
	}
	headerHash := sha256.Sum256(header.raw)
	chunk := make([]byte, custodyChunkSize)
	frame := make([]byte, frameHeaderSize+custodyChunkSize+aead.Overhead())
	defer clear(chunk)
	defer clear(frame)
	digest := sha256.New()
	var size, count uint64
	for {
		if ctx.Err() != nil {
			return fileResult{}, errors.New("encryption cancelled")
		}
		n, readErr := io.ReadFull(src, chunk)
		if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
			return fileResult{}, errors.New("read original failed")
		}
		if n > 0 {
			if count >= maxDataFrames || size > math.MaxUint64-uint64(n) {
				return fileResult{}, errors.New("original exceeds custody format limits")
			}
			_, _ = digest.Write(chunk[:n])
			size += uint64(n)
			if err := writeFrame(dst, aead, headerHash, prefix, count, dataFrame, chunk[:n], frame); err != nil {
				return fileResult{}, err
			}
			count++
		}
		if readErr != nil {
			break
		}
	}
	var result fileResult
	result.size = size
	copy(result.digest[:], digest.Sum(nil))
	if subtle.ConstantTimeCompare(result.digest[:], expected[:]) != 1 {
		return fileResult{}, errors.New("original SHA-256 does not match expected digest")
	}
	var end [endSize]byte
	binary.BigEndian.PutUint64(end[:8], count)
	binary.BigEndian.PutUint64(end[8:16], size)
	copy(end[16:], result.digest[:])
	if err := writeFrame(dst, aead, headerHash, prefix, count, endFrame, end[:], frame); err != nil {
		return fileResult{}, err
	}
	return result, nil
}

func decryptStream(ctx context.Context, dst io.Writer, src io.Reader, keys keyWrapper, expected [sha256.Size]byte) (fileResult, error) {
	header, err := readHeader(src)
	if err != nil {
		return fileResult{}, err
	}
	keyHash := sha256.Sum256([]byte(keys.ID()))
	if subtle.ConstantTimeCompare(header.raw[14:14+sha256.Size], keyHash[:]) != 1 {
		return fileResult{}, errors.New("unexpected custody key")
	}
	dek, err := keys.Unwrap(ctx, header.wrapped)
	if err != nil {
		return fileResult{}, errors.New("unwrap data key failed")
	}
	defer clear(dek)
	if len(dek) != 32 {
		return fileResult{}, errors.New("invalid unwrapped data key")
	}
	aead, err := newAEAD(dek)
	if err != nil {
		return fileResult{}, err
	}
	headerHash := sha256.Sum256(header.raw)
	frame := make([]byte, custodyChunkSize+aead.Overhead())
	defer clear(frame)
	digest := sha256.New()
	var size, count uint64
	shortChunk := false
	for {
		if ctx.Err() != nil {
			return fileResult{}, errors.New("decryption cancelled")
		}
		var fh [frameHeaderSize]byte
		if _, err := io.ReadFull(src, fh[:]); err != nil {
			return fileResult{}, errors.New("missing or truncated custody end frame")
		}
		seq := binary.BigEndian.Uint64(fh[:8])
		kind := fh[8]
		length := binary.BigEndian.Uint32(fh[9:])
		if seq != count || (kind != dataFrame && kind != endFrame) {
			return fileResult{}, errors.New("invalid custody frame order or type")
		}
		if kind == dataFrame && (shortChunk || length <= uint32(aead.Overhead()) || length > uint32(len(frame))) {
			return fileResult{}, errors.New("invalid custody data frame length")
		}
		if kind == endFrame && length != endSize+uint32(aead.Overhead()) {
			return fileResult{}, errors.New("invalid custody end frame length")
		}
		if _, err := io.ReadFull(src, frame[:length]); err != nil {
			return fileResult{}, errors.New("truncated custody frame")
		}
		nonce, aad := frameAuth(headerHash, header.prefix, fh, seq)
		plain, err := aead.Open(frame[:0], nonce[:], frame[:length], aad[:])
		if err != nil {
			return fileResult{}, errors.New("custody frame authentication failed")
		}
		if kind == endFrame {
			if binary.BigEndian.Uint64(plain[:8]) != count || binary.BigEndian.Uint64(plain[8:16]) != size {
				return fileResult{}, errors.New("custody length or frame count mismatch")
			}
			var result fileResult
			result.size = size
			copy(result.digest[:], plain[16:])
			computed := digest.Sum(nil)
			if subtle.ConstantTimeCompare(result.digest[:], computed) != 1 ||
				subtle.ConstantTimeCompare(result.digest[:], expected[:]) != 1 {
				return fileResult{}, errors.New("original SHA-256 does not match expected digest")
			}
			var trailing [1]byte
			n, readErr := src.Read(trailing[:])
			if n != 0 || readErr != io.EOF {
				return fileResult{}, errors.New("unexpected data after custody end frame")
			}
			return result, nil
		}
		if count >= maxDataFrames || size > math.MaxUint64-uint64(len(plain)) {
			return fileResult{}, errors.New("custody file exceeds format limits")
		}
		if err := writeExact(dst, plain); err != nil {
			return fileResult{}, errors.New("write private original failed")
		}
		_, _ = digest.Write(plain)
		size += uint64(len(plain))
		count++
		shortChunk = len(plain) < custodyChunkSize
	}
}

func newHeader(keyID string, prefix [4]byte, wrapped []byte) (custodyHeader, error) {
	if len(wrapped) == 0 || len(wrapped) > maxWrappedKeySize {
		return custodyHeader{}, errors.New("invalid wrapped data key length")
	}
	raw := make([]byte, headerSize+len(wrapped))
	copy(raw[:8], custodyMagic)
	raw[8] = custodyVersion
	raw[9] = custodyCipher
	binary.BigEndian.PutUint32(raw[10:14], custodyChunkSize)
	keyHash := sha256.Sum256([]byte(keyID))
	copy(raw[14:14+sha256.Size], keyHash[:])
	copy(raw[14+sha256.Size:14+sha256.Size+4], prefix[:])
	binary.BigEndian.PutUint16(raw[headerSize-2:headerSize], uint16(len(wrapped)))
	copy(raw[headerSize:], wrapped)
	return custodyHeader{raw: raw, prefix: prefix, wrapped: wrapped}, nil
}

func readHeader(src io.Reader) (custodyHeader, error) {
	raw := make([]byte, headerSize)
	if _, err := io.ReadFull(src, raw); err != nil {
		return custodyHeader{}, errors.New("truncated custody header")
	}
	if string(raw[:8]) != custodyMagic || raw[8] != custodyVersion ||
		raw[9] != custodyCipher || binary.BigEndian.Uint32(raw[10:14]) != custodyChunkSize {
		return custodyHeader{}, errors.New("unsupported custody header parameters")
	}
	length := int(binary.BigEndian.Uint16(raw[headerSize-2:]))
	if length == 0 || length > maxWrappedKeySize {
		return custodyHeader{}, errors.New("invalid wrapped data key length")
	}
	raw = append(raw, make([]byte, length)...)
	if _, err := io.ReadFull(src, raw[headerSize:]); err != nil {
		return custodyHeader{}, errors.New("truncated wrapped data key")
	}
	var prefix [4]byte
	copy(prefix[:], raw[14+sha256.Size:14+sha256.Size+4])
	return custodyHeader{raw: raw, prefix: prefix, wrapped: raw[headerSize:]}, nil
}

func newAEAD(dek []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(dek)
	if err != nil {
		return nil, errors.New("invalid AES-256 data key")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil || aead.NonceSize() != 12 || aead.Overhead() != 16 {
		return nil, errors.New("unsupported AES-GCM parameters")
	}
	return aead, nil
}

func writeFrame(dst io.Writer, aead cipher.AEAD, headerHash [sha256.Size]byte, prefix [4]byte, seq uint64, kind byte, plain, frame []byte) error {
	var fh [frameHeaderSize]byte
	binary.BigEndian.PutUint64(fh[:8], seq)
	fh[8] = kind
	binary.BigEndian.PutUint32(fh[9:], uint32(len(plain)+aead.Overhead()))
	nonce, aad := frameAuth(headerHash, prefix, fh, seq)
	copy(frame[:frameHeaderSize], fh[:])
	sealed := aead.Seal(frame[frameHeaderSize:frameHeaderSize], nonce[:], plain, aad[:])
	if err := writeExact(dst, frame[:frameHeaderSize+len(sealed)]); err != nil {
		return errors.New("write custody frame failed")
	}
	return nil
}

func frameAuth(headerHash [sha256.Size]byte, prefix [4]byte, fh [frameHeaderSize]byte, seq uint64) ([12]byte, [sha256.Size + frameHeaderSize]byte) {
	var nonce [12]byte
	copy(nonce[:4], prefix[:])
	binary.BigEndian.PutUint64(nonce[4:], seq)
	var aad [sha256.Size + frameHeaderSize]byte
	copy(aad[:sha256.Size], headerHash[:])
	copy(aad[sha256.Size:], fh[:])
	return nonce, aad
}

func writeExact(dst io.Writer, data []byte) error {
	n, err := dst.Write(data)
	if err == nil && n != len(data) {
		return io.ErrShortWrite
	}
	return err
}
