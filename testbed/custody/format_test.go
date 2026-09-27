package main

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testKeyID = "projects/example-project/locations/global/keyRings/private-ring/cryptoKeys/custody"

type fakeWrapper struct {
	id          string
	wrapped     [][]byte
	unwrapped   int
	wrapError   error
	unwrapError error
}

func (w *fakeWrapper) ID() string {
	if w.id != "" {
		return w.id
	}
	return testKeyID
}

func (w *fakeWrapper) Wrap(ctx context.Context, key []byte) ([]byte, error) {
	if w.wrapError != nil {
		return nil, w.wrapError
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	w.wrapped = append(w.wrapped, bytes.Clone(key))
	aead := testAEAD()
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, key, nil), nil
}

func (w *fakeWrapper) Unwrap(ctx context.Context, wrapped []byte) ([]byte, error) {
	w.unwrapped++
	if w.unwrapError != nil {
		return nil, w.unwrapError
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	aead := testAEAD()
	if len(wrapped) != aead.NonceSize()+32+aead.Overhead() {
		return nil, errors.New("invalid fake wrapped key")
	}
	return aead.Open(nil, wrapped[:aead.NonceSize()], wrapped[aead.NonceSize():], nil)
}

func testAEAD() cipher.AEAD {
	// Test-only fixed wrapping key; the data keys and nonces are still random.
	block, err := aes.NewCipher(bytes.Repeat([]byte{0x42}, 32))
	if err != nil {
		panic(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		panic(err)
	}
	return aead
}

func sealTestBytes(t *testing.T, keys *fakeWrapper, plaintext []byte) []byte {
	t.Helper()
	var encrypted bytes.Buffer
	digest := sha256.Sum256(plaintext)
	result, err := encryptStream(context.Background(), &encrypted, bytes.NewReader(plaintext), keys, digest)
	if err != nil || result.size != uint64(len(plaintext)) || result.digest != digest {
		t.Fatalf("encryptStream: result=%+v err=%v", result, err)
	}
	return bytes.Clone(encrypted.Bytes())
}

func TestCustodyFormatRoundTrip(t *testing.T) {
	keys := &fakeWrapper{}
	var first []byte
	for _, content := range [][]byte{nil, []byte("signed original"), bytes.Repeat([]byte{0x7a}, custodyChunkSize+19)} {
		encrypted := sealTestBytes(t, keys, content)
		if bytes.Equal(content, []byte("signed original")) {
			first = encrypted
		}
		if bytes.Contains(encrypted, []byte("signed original")) {
			t.Fatal("ciphertext contains known plaintext")
		}
		expected := sha256.Sum256(content)
		var recovered bytes.Buffer
		result, err := decryptStream(context.Background(), &recovered, bytes.NewReader(encrypted), keys, expected)
		if err != nil || !bytes.Equal(recovered.Bytes(), content) || result.digest != expected || result.size != uint64(len(content)) {
			t.Fatalf("round trip (%d bytes): result=%+v err=%v", len(content), result, err)
		}
	}
	second := sealTestBytes(t, keys, []byte("signed original"))
	if bytes.Equal(first, second) {
		t.Fatal("encrypting the same original twice reused artifact randomness")
	}
	if len(keys.wrapped) != 4 || bytes.Equal(keys.wrapped[0], keys.wrapped[1]) ||
		bytes.Equal(keys.wrapped[1], keys.wrapped[2]) || bytes.Equal(keys.wrapped[1], keys.wrapped[3]) {
		t.Fatal("each artifact must have a fresh data key")
	}
}

func TestCustodyFormatRejectsCorruptionWithoutOutput(t *testing.T) {
	keys := &fakeWrapper{}
	plain := bytes.Repeat([]byte("A"), custodyChunkSize*2+31)
	encrypted := sealTestBytes(t, keys, plain)
	expected := sha256.Sum256(plain)
	frameSize := frameHeaderSize + custodyChunkSize + 16
	first := headerSize + int(binary.BigEndian.Uint16(encrypted[headerSize-2:headerSize]))
	second := first + frameSize
	third := second + frameSize
	end := third + frameHeaderSize + 31 + 16
	if end+frameHeaderSize+endSize+16 != len(encrypted) {
		t.Fatal("unexpected test frame layout")
	}
	mutate := func(index int) []byte {
		changed := bytes.Clone(encrypted)
		changed[index] ^= 1
		return changed
	}
	reordered := bytes.Clone(encrypted)
	copy(reordered[first:second], encrypted[second:third])
	copy(reordered[second:third], encrypted[first:second])
	dropped := append(bytes.Clone(encrypted[:first]), encrypted[second:]...)
	duplicated := append(bytes.Clone(encrypted[:second]), encrypted[first:]...)
	aead, err := newAEAD(keys.wrapped[0])
	if err != nil {
		t.Fatal(err)
	}
	headerHash := sha256.Sum256(encrypted[:first])
	var prefix [4]byte
	copy(prefix[:], encrypted[14+sha256.Size:14+sha256.Size+4])
	var authenticatedShort bytes.Buffer
	authenticatedShort.Write(encrypted[:second])
	frame := make([]byte, frameHeaderSize+custodyChunkSize+aead.Overhead())
	if err := writeFrame(&authenticatedShort, aead, headerHash, prefix, 1, dataFrame, plain[2*custodyChunkSize:], frame); err != nil {
		t.Fatal(err)
	}
	if err := writeFrame(&authenticatedShort, aead, headerHash, prefix, 2, dataFrame, plain[custodyChunkSize:2*custodyChunkSize], frame); err != nil {
		t.Fatal(err)
	}
	authenticatedShort.Write(encrypted[end:])
	otherKey := &fakeWrapper{id: "projects/another-project/locations/global/keyRings/private-ring/cryptoKeys/custody"}
	cases := []struct {
		name     string
		data     []byte
		keys     *fakeWrapper
		expected [sha256.Size]byte
	}{
		{"header truncated", encrypted[:headerSize-1], keys, expected},
		{"magic", mutate(0), keys, expected},
		{"version", mutate(8), keys, expected},
		{"suite", mutate(9), keys, expected},
		{"chunk size", mutate(10), keys, expected},
		{"key fingerprint", mutate(14), keys, expected},
		{"nonce prefix", mutate(14 + sha256.Size), keys, expected},
		{"wrapped length", mutate(headerSize - 1), keys, expected},
		{"wrapped key", mutate(headerSize), keys, expected},
		{"data frame sequence", mutate(first), keys, expected},
		{"data frame type", mutate(first + 8), keys, expected},
		{"data frame length", mutate(first + 9), keys, expected},
		{"data frame payload", mutate(first + frameHeaderSize), keys, expected},
		{"reordered frames", reordered, keys, expected},
		{"omitted frame", dropped, keys, expected},
		{"repeated frame", duplicated, keys, expected},
		{"authenticated short chunk before another", authenticatedShort.Bytes(), keys, expected},
		{"missing end", encrypted[:end], keys, expected},
		{"truncated end tag", encrypted[:len(encrypted)-1], keys, expected},
		{"end frame payload", mutate(end + frameHeaderSize), keys, expected},
		{"authenticated end count", resealTestEnd(t, encrypted, end, keys.wrapped[0], func(data []byte) { data[7] ^= 1 }), keys, expected},
		{"authenticated end size", resealTestEnd(t, encrypted, end, keys.wrapped[0], func(data []byte) { data[15] ^= 1 }), keys, expected},
		{"authenticated end SHA-256", resealTestEnd(t, encrypted, end, keys.wrapped[0], func(data []byte) { data[16] ^= 1 }), keys, expected},
		{"trailing byte", append(bytes.Clone(encrypted), 0), keys, expected},
		{"different KMS key", encrypted, otherKey, expected},
		{"different original digest", encrypted, keys, sha256.Sum256([]byte("different"))},
	}
	specificErrors := map[string]string{
		"authenticated short chunk before another": "invalid custody data frame length",
		"authenticated end count":                  "custody length or frame count mismatch",
		"authenticated end size":                   "custody length or frame count mismatch",
		"authenticated end SHA-256":                "original SHA-256 does not match expected digest",
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := privateTestDir(t)
			in := filepath.Join(dir, "private.enc")
			out := filepath.Join(dir, "original")
			if err := os.WriteFile(in, tc.data, 0o600); err != nil {
				t.Fatal(err)
			}
			want := specificErrors[tc.name]
			if _, err := processFile(context.Background(), "decrypt", in, out, tc.keys, tc.expected); err == nil ||
				want != "" && !strings.Contains(err.Error(), want) {
				t.Fatalf("invalid ciphertext was accepted or failed for the wrong reason: %v", err)
			}
			assertNoOutput(t, dir, out)
		})
	}
	if otherKey.unwrapped != 0 {
		t.Fatal("wrong key resource must fail before KMS unwrap")
	}
	if keys.unwrapped == 0 {
		t.Fatal("corruption tests never reached authenticated decryption")
	}
}

func resealTestEnd(t *testing.T, ciphertext []byte, end int, key []byte, edit func([]byte)) []byte {
	t.Helper()
	aead, err := newAEAD(key)
	if err != nil {
		t.Fatal(err)
	}
	headerEnd := headerSize + int(binary.BigEndian.Uint16(ciphertext[headerSize-2:headerSize]))
	headerHash := sha256.Sum256(ciphertext[:headerEnd])
	var prefix [4]byte
	copy(prefix[:], ciphertext[14+sha256.Size:14+sha256.Size+4])
	var fh [frameHeaderSize]byte
	copy(fh[:], ciphertext[end:end+frameHeaderSize])
	nonce, aad := frameAuth(headerHash, prefix, fh, binary.BigEndian.Uint64(fh[:8]))
	plain, err := aead.Open(nil, nonce[:], ciphertext[end+frameHeaderSize:], aad[:])
	if err != nil {
		t.Fatal(err)
	}
	edit(plain)
	modified := bytes.Clone(ciphertext)
	copy(modified[end+frameHeaderSize:], aead.Seal(nil, nonce[:], plain, aad[:]))
	return modified
}

func TestCustodyFormatRejectsLengthsAndShortWrites(t *testing.T) {
	keys := &fakeWrapper{}
	content := sealTestBytes(t, keys, []byte("original"))
	for _, test := range []struct {
		name string
		edit func([]byte)
	}{
		{"wrapped length zero", func(b []byte) { binary.BigEndian.PutUint16(b[headerSize-2:], 0) }},
		{"wrapped length oversized", func(b []byte) { binary.BigEndian.PutUint16(b[headerSize-2:], maxWrappedKeySize+1) }},
		{"frame length oversized", func(b []byte) {
			offset := headerSize + int(binary.BigEndian.Uint16(b[headerSize-2:]))
			binary.BigEndian.PutUint32(b[offset+9:], ^uint32(0))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			b := bytes.Clone(content)
			test.edit(b)
			if _, err := decryptStream(context.Background(), io.Discard, bytes.NewReader(b), keys, sha256.Sum256([]byte("original"))); err == nil {
				t.Fatal("unbounded or invalid frame length was accepted")
			}
		})
	}
	short := shortWriter{}
	if _, err := encryptStream(context.Background(), short, strings.NewReader("original"), keys, sha256.Sum256([]byte("original"))); err == nil {
		t.Fatal("short header write must fail")
	}
}

type shortWriter struct{}

func (shortWriter) Write([]byte) (int, error) { return 0, nil }

func assertNoOutput(t *testing.T, dir, output string) {
	t.Helper()
	if _, err := os.Lstat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("unexpected output after failure: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".lantern-custody-") {
			t.Fatal("temporary plaintext or ciphertext survived failure")
		}
	}
}
