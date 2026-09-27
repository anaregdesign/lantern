package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"
)

const kmsTimeout = 2 * time.Minute

type kmsKey struct {
	project  string
	location string
	ring     string
	name     string
}

func parseKMSKey(resource string) (kmsKey, error) {
	parts := strings.Split(resource, "/")
	if len(parts) != 8 || parts[0] != "projects" || parts[2] != "locations" ||
		parts[4] != "keyRings" || parts[6] != "cryptoKeys" ||
		!validKMSPart(parts[1]) || !validKMSPart(parts[3]) ||
		!validKMSPart(parts[5]) || !validKMSPart(parts[7]) {
		return kmsKey{}, errors.New("invalid Cloud KMS CryptoKey resource")
	}
	return kmsKey{project: parts[1], location: parts[3], ring: parts[5], name: parts[7]}, nil
}

func validKMSPart(part string) bool {
	if len(part) == 0 || len(part) > 63 {
		return false
	}
	for i := 0; i < len(part); i++ {
		b := part[i]
		if b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' ||
			b >= '0' && b <= '9' || b == '_' || b == '-' {
			continue
		}
		return false
	}
	return true
}

func (k kmsKey) ID() string {
	return fmt.Sprintf("projects/%s/locations/%s/keyRings/%s/cryptoKeys/%s",
		k.project, k.location, k.ring, k.name)
}

func (k kmsKey) Wrap(ctx context.Context, dek []byte) ([]byte, error) {
	if len(dek) != 32 {
		return nil, errors.New("invalid data key length for Cloud KMS")
	}
	out, err := k.invoke(ctx, "encrypt", dek, maxWrappedKeySize)
	if err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, errors.New("cloud KMS returned an empty wrapped key")
	}
	return out, nil
}

func (k kmsKey) Unwrap(ctx context.Context, wrapped []byte) ([]byte, error) {
	if len(wrapped) == 0 || len(wrapped) > maxWrappedKeySize {
		return nil, errors.New("invalid wrapped data key length")
	}
	out, err := k.invoke(ctx, "decrypt", wrapped, 32)
	if err != nil {
		return nil, err
	}
	if len(out) != 32 {
		clear(out)
		return nil, errors.New("cloud KMS returned an invalid data key")
	}
	return out, nil
}

func (k kmsKey) invoke(parent context.Context, operation string, input []byte, limit int) ([]byte, error) {
	if operation != "encrypt" && operation != "decrypt" {
		return nil, errors.New("unsupported Cloud KMS operation")
	}
	ctx, cancel := context.WithTimeout(parent, kmsTimeout)
	defer cancel()
	args := []string{"kms", operation,
		"--project=" + k.project,
		"--location=" + k.location,
		"--keyring=" + k.ring,
		"--key=" + k.name,
		"--quiet",
	}
	if operation == "encrypt" {
		args = append(args, "--plaintext-file=-", "--ciphertext-file=-")
	} else {
		args = append(args, "--ciphertext-file=-", "--plaintext-file=-")
	}
	cmd := exec.CommandContext(ctx, "gcloud", args...)
	cmd.Stdin = bytes.NewReader(input)
	output := &limitedOutput{limit: limit}
	cmd.Stdout = output
	cmd.Stderr = io.Discard
	cmd.Env = append(os.Environ(), "CLOUDSDK_CORE_LOG_HTTP=false", "CLOUDSDK_CORE_VERBOSITY=error")
	if err := cmd.Run(); err != nil {
		clear(output.bytes)
		if ctx.Err() != nil {
			return nil, fmt.Errorf("cloud KMS %s cancelled or timed out", operation)
		}
		if output.exceeded {
			return nil, fmt.Errorf("cloud KMS %s response exceeded size limit", operation)
		}
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			return nil, fmt.Errorf("cloud KMS %s failed (exit %d)", operation, exit.ExitCode())
		}
		return nil, fmt.Errorf("cloud KMS %s invocation failed", operation)
	}
	return output.bytes, nil
}

type limitedOutput struct {
	bytes    []byte
	limit    int
	exceeded bool
}

func (o *limitedOutput) Write(data []byte) (int, error) {
	if len(data) > o.limit-len(o.bytes) {
		o.exceeded = true
		return 0, errors.New("cloud KMS response exceeded size limit")
	}
	o.bytes = append(o.bytes, data...)
	return len(data), nil
}
