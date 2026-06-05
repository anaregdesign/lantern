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
	if !strings.Contains(err.Error(), "invalid argument") {
		t.Fatalf("missing branch label: %v", err)
	}
	if !strings.Contains(err.Error(), "remember_fact") {
		t.Fatalf("missing tool prefix: %v", err)
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
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing branch label: %v", err)
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
