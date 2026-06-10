package mcp

import (
	"errors"
	"strings"
	"testing"

	client "github.com/anaregdesign/lantern/sdks/go"
)

func TestMapSDKError_InvalidArgument(t *testing.T) {
	err := mapSDKError("remember_fact", client.ErrInvalidArgument)
	if !errors.Is(err, client.ErrInvalidArgument) {
		t.Fatalf("errors.Is sentinel lost")
	}
	// The sentinel already stringifies to "invalid argument"; the wrapper must
	// not prepend a redundant literal (regression guard for #546).
	if got, want := err.Error(), "remember_fact: invalid argument"; got != want {
		t.Fatalf("message shape: got %q want %q", got, want)
	}
	if strings.Contains(err.Error(), "invalid argument: invalid argument") {
		t.Fatalf("doubled label not removed: %v", err)
	}
}

// TestMapSDKError_InvalidArgumentJoined reproduces the real dogfooding path
// (#546): the SDK joins ErrInvalidArgument with the underlying connect error,
// so the message must surface the sentinel once, never doubled.
func TestMapSDKError_InvalidArgumentJoined(t *testing.T) {
	joined := errors.Join(
		client.ErrInvalidArgument,
		errors.New("invalid_argument: expiration exceeds LANTERN_TOMBSTONE_TTL=24h0m0s"),
	)
	err := mapSDKError("remember_fact", joined)
	if !errors.Is(err, client.ErrInvalidArgument) {
		t.Fatalf("errors.Is sentinel lost")
	}
	if strings.Contains(err.Error(), "invalid argument: invalid argument") {
		t.Fatalf("doubled label leaked through joined error: %v", err)
	}
	if !strings.HasPrefix(err.Error(), "remember_fact: invalid argument") {
		t.Fatalf("missing tool prefix / sentinel: %v", err)
	}
}

func TestMapSDKError_ResourceExhausted(t *testing.T) {
	err := mapSDKError("recall_related", client.ErrResourceExhausted)
	if !errors.Is(err, client.ErrResourceExhausted) {
		t.Fatalf("errors.Is sentinel lost")
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Fatalf("missing back-off hint: %v", err)
	}
}

func TestMapSDKError_NotFound(t *testing.T) {
	err := mapSDKError("recall_fact", client.ErrNotFound)
	if !errors.Is(err, client.ErrNotFound) {
		t.Fatalf("errors.Is sentinel lost")
	}
	// "not found" comes from the sentinel itself; it must appear once, not
	// doubled (the redundant literal label was removed in #546).
	if got, want := err.Error(), "recall_fact: not found"; got != want {
		t.Fatalf("message shape: got %q want %q", got, want)
	}
	if strings.Contains(err.Error(), "not found: not found") {
		t.Fatalf("doubled label not removed: %v", err)
	}
}

func TestMapSDKError_Generic(t *testing.T) {
	base := errors.New("some other failure")
	err := mapSDKError("forget", base)
	if !errors.Is(err, base) {
		t.Fatalf("errors.Is base error lost")
	}
	if !strings.HasPrefix(err.Error(), "forget:") {
		t.Fatalf("missing tool prefix: %v", err)
	}
}
