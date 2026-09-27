package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKMSKeyParsing(t *testing.T) {
	key, err := parseKMSKey(testKeyID)
	if err != nil || key.ID() != testKeyID {
		t.Fatalf("valid key rejected: %v", err)
	}
	for _, input := range []string{
		"", "projects/p", "projects/p/locations/l/keyRings/r/cryptoKeys/k/cryptoKeyVersions/1",
		"projects/p/locations/l/keyrings/r/cryptoKeys/k",
		"projects/p/locations/l/keyRings/r/cryptoKeys/",
		"projects/p/locations/l/keyRings/r/cryptoKeys/k\n--log-http",
		"projects/p/locations/l/keyRings/r/cryptoKeys/k?version=1",
		"projects/p/locations/l/keyRings/r/cryptoKeys/こんにちは",
		"projects/p/locations/l/keyRings/r/cryptoKeys/" + strings.Repeat("a", 64),
	} {
		if _, err := parseKMSKey(input); err == nil {
			t.Fatalf("accepted malformed KMS resource: %q", input)
		}
	}
}

func TestGcloudKMSUsesPipesAndBoundsOutput(t *testing.T) {
	binDir := t.TempDir()
	script := `#!/bin/sh
if [ "$1" != "kms" ] || [ "$3" != "--project=example-project" ] ||
   [ "$4" != "--location=global" ] || [ "$5" != "--keyring=private-ring" ] ||
   [ "$6" != "--key=custody" ] || [ "$7" != "--quiet" ]; then
  exit 60
fi
if [ "$CLOUDSDK_CORE_LOG_HTTP" != "false" ] || [ "$CLOUDSDK_CORE_VERBOSITY" != "error" ]; then
  exit 63
fi
for arg in "$@"; do
  case "$arg" in *SUPERSECRETKEY*) exit 61;; esac
done
if [ "$2" = "encrypt" ] && [ "$8" = "--plaintext-file=-" ] &&
   [ "$9" = "--ciphertext-file=-" ]; then
  dd bs=32 count=1 of=/dev/null 2>/dev/null
  printf 'fake-wrapped-data-key'
elif [ "$2" = "decrypt" ] && [ "$8" = "--ciphertext-file=-" ] &&
   [ "$9" = "--plaintext-file=-" ]; then
  cat >/dev/null
  printf 'SUPERSECRETKEY-12345678901234567'
else
  exit 62
fi
`
	if err := os.WriteFile(filepath.Join(binDir, "gcloud"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	key, err := parseKMSKey(testKeyID)
	if err != nil {
		t.Fatal(err)
	}
	plain := []byte("SUPERSECRETKEY-12345678901234567")
	if len(plain) != 32 {
		t.Fatal("synthetic test key must be 32 bytes")
	}
	wrapped, err := key.Wrap(context.Background(), plain)
	if err != nil || string(wrapped) != "fake-wrapped-data-key" {
		t.Fatalf("gcloud stdin-to-stdout wrap: len=%d err=%v", len(wrapped), err)
	}
	unwrapped, err := key.Unwrap(context.Background(), wrapped)
	if err != nil || !bytes.Equal(unwrapped, plain) {
		t.Fatalf("gcloud stdin-to-stdout unwrap: len=%d err=%v", len(unwrapped), err)
	}
	clear(unwrapped)

	if err := os.WriteFile(filepath.Join(binDir, "gcloud"), []byte("#!/bin/sh\nprintf 'sensitive-cli-output' >&2\nexit 41\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	_, err = key.Wrap(context.Background(), plain)
	if err == nil || !strings.Contains(err.Error(), "exit 41") ||
		strings.Contains(err.Error(), "sensitive-cli-output") || strings.Contains(err.Error(), string(plain)) {
		t.Fatalf("gcloud error leaked subprocess content: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "gcloud"), []byte("#!/bin/sh\ndd if=/dev/zero bs=4097 count=1 2>/dev/null\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := key.Wrap(context.Background(), plain); err == nil {
		t.Fatal("oversized gcloud ciphertext was accepted")
	}
	if err := os.WriteFile(filepath.Join(binDir, "gcloud"), []byte("#!/bin/sh\nprintf 'short-key'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := key.Unwrap(context.Background(), wrapped); err == nil {
		t.Fatal("short unwrapped data key was accepted")
	}
	if _, err := key.Wrap(context.Background(), plain[:31]); err == nil {
		t.Fatal("short data key was accepted for wrapping")
	}
	if _, err := key.Unwrap(context.Background(), nil); err == nil {
		t.Fatal("empty wrapped data key was accepted")
	}
}
