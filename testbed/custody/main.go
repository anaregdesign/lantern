// Command custody encrypts private receipt originals for separately approved
// immutable storage; it never uploads evidence or accesses device data.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
)

const usage = "usage: custody encrypt|decrypt --in FILE --out FILE --kms-key projects/PROJECT/locations/LOCATION/keyRings/RING/cryptoKeys/KEY --sha256 EXPECTED_ORIGINAL_SHA256\n"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "custody:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return errors.New(usage)
	}
	if args[0] == "help" {
		_, err := io.WriteString(stdout, usage)
		return err
	}
	if args[0] != "encrypt" && args[0] != "decrypt" {
		return errors.New("unknown custody operation; run custody help")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	input := flags.String("in", "", "private original or ciphertext file")
	output := flags.String("out", "", "new file in a private directory")
	resource := flags.String("kms-key", "", "Cloud KMS CryptoKey resource")
	sha := flags.String("sha256", "", "expected SHA-256 of the original")
	if err := flags.Parse(args[1:]); errors.Is(err, flag.ErrHelp) {
		_, writeErr := io.WriteString(stdout, usage)
		return writeErr
	} else if err != nil || flags.NArg() != 0 {
		return errors.New("invalid custody flags; run custody help")
	}
	if *input == "" || *output == "" || *resource == "" || *sha == "" {
		return errors.New("missing required custody flag; run custody help")
	}
	key, err := parseKMSKey(*resource)
	if err != nil {
		return err
	}
	expected, err := parseDigest(*sha)
	if err != nil {
		return err
	}
	_, err = processFile(ctx, args[0], *input, *output, key, expected)
	return err
}

func parseDigest(value string) ([sha256.Size]byte, error) {
	var digest [sha256.Size]byte
	if len(value) != sha256.Size*2 {
		return digest, errors.New("expected original SHA-256 must be 64 hex characters")
	}
	if _, err := hex.Decode(digest[:], []byte(value)); err != nil {
		return digest, errors.New("expected original SHA-256 must be 64 hex characters")
	}
	return digest, nil
}

func processFile(ctx context.Context, operation, inputPath, outputPath string, keys keyWrapper, expected [sha256.Size]byte) (result fileResult, err error) {
	if operation != "encrypt" && operation != "decrypt" {
		return fileResult{}, errors.New("unsupported custody operation")
	}
	info, err := os.Lstat(inputPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return fileResult{}, errors.New("input must be an owner-private regular file, not a symlink")
	}
	src, err := os.Open(inputPath)
	if err != nil {
		return fileResult{}, errors.New("open private input failed")
	}
	defer src.Close()
	opened, err := src.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return fileResult{}, errors.New("private input changed before opening")
	}
	dir := filepath.Dir(outputPath)
	dirInfo, err := os.Lstat(dir)
	if err != nil || !dirInfo.IsDir() || dirInfo.Mode().Perm()&0o077 != 0 {
		return fileResult{}, errors.New("output directory must exist and be owner-private")
	}
	if _, err := os.Lstat(outputPath); err == nil {
		return fileResult{}, errors.New("output already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fileResult{}, errors.New("inspect output destination failed")
	}
	tmp, err := os.CreateTemp(dir, ".lantern-custody-*")
	if err != nil {
		return fileResult{}, errors.New("create private temporary output failed")
	}
	tmpPath := tmp.Name()
	defer func() {
		if closeErr := tmp.Close(); closeErr != nil && !errors.Is(closeErr, os.ErrClosed) {
			err = errors.Join(err, errors.New("close private temporary output failed"))
		}
		if tmpPath != "" {
			if removeErr := os.Remove(tmpPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
				err = errors.Join(err, errors.New("remove private temporary output failed"))
			}
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return fileResult{}, errors.New("set private temporary output mode failed")
	}
	if operation == "encrypt" {
		result, err = encryptStream(ctx, tmp, src, keys, expected)
	} else {
		result, err = decryptStream(ctx, tmp, src, keys, expected)
	}
	if err != nil {
		return fileResult{}, err
	}
	if err := tmp.Sync(); err != nil {
		return fileResult{}, errors.New("sync private temporary output failed")
	}
	if err := tmp.Close(); err != nil {
		return fileResult{}, errors.New("close private temporary output failed")
	}
	if err := os.Link(tmpPath, outputPath); err != nil {
		if errors.Is(err, os.ErrExist) {
			return fileResult{}, errors.New("output already exists")
		}
		return fileResult{}, errors.New("atomically publish private output failed")
	}
	if err := os.Remove(tmpPath); err != nil {
		if undoErr := os.Remove(outputPath); undoErr != nil {
			return fileResult{}, errors.New("temporary output cleanup failed; remove published output and private temporary file")
		}
		return fileResult{}, errors.New("temporary output cleanup failed")
	}
	tmpPath = ""
	return result, nil
}
