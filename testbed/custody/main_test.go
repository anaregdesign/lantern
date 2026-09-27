package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCustodyPrivateFileLargeStreamingRoundTrip(t *testing.T) {
	dir := privateTestDir(t)
	original := filepath.Join(dir, "original")
	src, err := os.OpenFile(original, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	hasher := sha256.New()
	length := int64(33*custodyChunkSize + 73)
	pattern := bytes.Repeat([]byte("receipt"), 101)
	if _, err := io.CopyN(io.MultiWriter(src, hasher), &repeatingReader{pattern: pattern}, length); err != nil {
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	var digest [sha256.Size]byte
	copy(digest[:], hasher.Sum(nil))
	keys := &fakeWrapper{}
	encrypted := filepath.Join(dir, "encrypted")
	restored := filepath.Join(dir, "restored")
	result, err := processFile(context.Background(), "encrypt", original, encrypted, keys, digest)
	if err != nil || result.digest != digest || result.size != uint64(length) {
		t.Fatalf("encrypt file: result=%+v err=%v", result, err)
	}
	encryptedInfo, err := os.Stat(encrypted)
	if err != nil || encryptedInfo.Mode().Perm() != 0o600 {
		t.Fatalf("encrypted file mode: info=%v err=%v", encryptedInfo, err)
	}
	result, err = processFile(context.Background(), "decrypt", encrypted, restored, keys, digest)
	if err != nil || result.digest != digest || result.size != uint64(length) {
		t.Fatalf("decrypt file: result=%+v err=%v", result, err)
	}
	restoredInfo, err := os.Stat(restored)
	if err != nil || restoredInfo.Mode().Perm() != 0o600 || restoredInfo.Size() != length {
		t.Fatalf("restored file: info=%v err=%v", restoredInfo, err)
	}
	reopened, err := os.Open(restored)
	if err != nil {
		t.Fatal(err)
	}
	actual := sha256.New()
	_, copyErr := io.Copy(actual, reopened)
	closeErr := reopened.Close()
	if copyErr != nil || closeErr != nil || !bytes.Equal(actual.Sum(nil), digest[:]) {
		t.Fatalf("restored bytes differ: copy=%v close=%v", copyErr, closeErr)
	}
	if len(keys.wrapped) != 1 || keys.unwrapped != 1 {
		t.Fatalf("unexpected KMS boundary calls: wrapped=%d unwrapped=%d", len(keys.wrapped), keys.unwrapped)
	}
	assertNoTemporary(t, dir)
}

type repeatingReader struct {
	pattern []byte
	offset  int
}

func (r *repeatingReader) Read(dst []byte) (int, error) {
	for i := range dst {
		dst[i] = r.pattern[r.offset]
		r.offset = (r.offset + 1) % len(r.pattern)
	}
	return len(dst), nil
}

func TestCustodyPrivateFileFailures(t *testing.T) {
	dir := privateTestDir(t)
	input := filepath.Join(dir, "source")
	if err := os.WriteFile(input, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("original"))
	for _, tc := range []struct {
		name     string
		ctx      context.Context
		keys     *fakeWrapper
		expected [sha256.Size]byte
	}{
		{"mismatched original", context.Background(), &fakeWrapper{}, sha256.Sum256([]byte("wrong"))},
		{"KMS failure", context.Background(), &fakeWrapper{wrapError: errors.New("private raw secret")}, digest},
		{"cancelled", cancelledContext(), &fakeWrapper{}, digest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := filepath.Join(dir, "failed-"+strings.ReplaceAll(tc.name, " ", "-"))
			if _, err := processFile(tc.ctx, "encrypt", input, out, tc.keys, tc.expected); err == nil || strings.Contains(err.Error(), "private raw secret") {
				t.Fatal("invalid operation succeeded")
			}
			assertNoOutput(t, dir, out)
		})
	}
	keys := &fakeWrapper{}
	encrypted := filepath.Join(dir, "valid-ciphertext")
	if _, err := processFile(context.Background(), "encrypt", input, encrypted, keys, digest); err != nil {
		t.Fatal(err)
	}
	keys.unwrapError = errors.New("private raw secret")
	failedDecrypt := filepath.Join(dir, "failed-decrypt")
	if _, err := processFile(context.Background(), "decrypt", encrypted, failedDecrypt, keys, digest); err == nil ||
		strings.Contains(err.Error(), "private raw secret") {
		t.Fatalf("unsafe decryption error: %v", err)
	}
	assertNoOutput(t, dir, failedDecrypt)
	if _, err := processFile(context.Background(), "decrypt", encrypted, failedDecrypt, shortKeyWrapper{keys}, digest); err == nil {
		t.Fatal("invalid unwrapped data key was accepted")
	}
	assertNoOutput(t, dir, failedDecrypt)

	// A filesystem destination is never replaced, including through a symlink.
	existing := filepath.Join(dir, "existing")
	if err := os.WriteFile(existing, []byte("unchanged"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := processFile(context.Background(), "encrypt", input, existing, &fakeWrapper{}, digest); err == nil {
		t.Fatal("existing destination was overwritten")
	}
	data, _ := os.ReadFile(existing)
	if string(data) != "unchanged" {
		t.Fatal("existing destination changed")
	}
	link := filepath.Join(dir, "symlink-output")
	if err := os.Symlink(existing, link); err != nil {
		t.Fatal(err)
	}
	if _, err := processFile(context.Background(), "encrypt", input, link, &fakeWrapper{}, digest); err == nil {
		t.Fatal("symlink destination was accepted")
	}
	if _, err := processFile(context.Background(), "encrypt", link, filepath.Join(dir, "bad-input"), &fakeWrapper{}, digest); err == nil {
		t.Fatal("symlink source was accepted")
	}
	if err := os.Chmod(input, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := processFile(context.Background(), "encrypt", input, filepath.Join(dir, "public-input"), &fakeWrapper{}, digest); err == nil {
		t.Fatal("group/world-readable source was accepted")
	}
	if err := os.Chmod(input, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := processFile(context.Background(), "encrypt", input, filepath.Join(dir, "public-dir"), &fakeWrapper{}, digest); err == nil {
		t.Fatal("group/world-readable output directory was accepted")
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	assertNoTemporary(t, dir)
}

type shortKeyWrapper struct{ *fakeWrapper }

func (shortKeyWrapper) Unwrap(context.Context, []byte) ([]byte, error) {
	return make([]byte, 31), nil
}

func TestCustodyCLIValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"missing command", nil},
		{"unknown command", []string{"erase"}},
		{"unknown flag", []string{"encrypt", "--token=hidden-value"}},
		{"missing input", []string{"encrypt", "--out=file"}},
		{"malformed key", []string{"decrypt", "--in=file", "--out=file2", "--kms-key=bad", "--sha256=" + strings.Repeat("a", 64)}},
		{"bad SHA-256", []string{"decrypt", "--in=file", "--out=file2", "--kms-key=" + testKeyID, "--sha256=bad-token"}},
		{"unexpected args", []string{"encrypt", "--in=file", "--out=file2", "--kms-key=" + testKeyID, "--sha256=" + strings.Repeat("a", 64), "extra"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout bytes.Buffer
			err := run(context.Background(), tc.args, &stdout)
			if err == nil || stdout.Len() != 0 || strings.Contains(err.Error(), "hidden-value") || strings.Contains(err.Error(), "bad-token") {
				t.Fatalf("invalid request was accepted or logged a value: %v %q", err, stdout.String())
			}
		})
	}
	var stdout bytes.Buffer
	if err := run(context.Background(), []string{"help"}, &stdout); err != nil || stdout.String() != usage {
		t.Fatalf("help: %q %v", stdout.String(), err)
	}
	empty := sha256.Sum256(nil)
	if _, err := parseDigest(hex.EncodeToString(empty[:])); err != nil {
		t.Fatal(err)
	}
}

func cancelledContext() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func assertNoTemporary(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".lantern-custody-") {
			t.Fatalf("temporary output retained: %s", entry.Name())
		}
	}
}
