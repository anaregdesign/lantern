package replication

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/peerauth"
)

type snapshotAdmissionHTTPClientFunc func(*http.Request) (*http.Response, error)

func (f snapshotAdmissionHTTPClientFunc) Do(request *http.Request) (*http.Response, error) {
	return f(request)
}

type snapshotAdmissionTestBody struct {
	io.Reader
	closed bool
}

func (b *snapshotAdmissionTestBody) Close() error {
	b.closed = true
	return nil
}

func TestSnapshotRequestContextKeepsLegacyResponseAndCallerCancellation(t *testing.T) {
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	body := &snapshotAdmissionTestBody{Reader: strings.NewReader("snapshot")}
	want := &http.Response{StatusCode: http.StatusOK, Body: body}
	requests := 0
	ctx, client := snapshotRequestContext(parent, snapshotAdmissionHTTPClientFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if err := checkSnapshotContext(request.Context()); err != nil {
			t.Fatal("legacy request lost its caller context", err)
		}
		return want, nil
	}), false)
	if err := checkSnapshotContext(ctx); err != nil {
		t.Fatal("legacy context requires native evidence", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://snapshot.invalid/Snapshot", nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := client.Do(request)
	if err != nil || got != want || requests != 1 || body.closed {
		t.Fatalf("legacy response changed: same=%t requests=%d closed=%t err=%v", got == want, requests, body.closed, err)
	}
	if _, err := io.ReadAll(got.Body); err != nil {
		t.Fatal(err)
	}
	if err := got.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if err := checkSnapshotContext(ctx); err != nil {
		t.Fatal("closing a response canceled its independent caller context", err)
	}
	cancel()
	if err := checkSnapshotContext(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("staged processing ignored caller cancellation: %v", err)
	}
}

func TestSnapshotRequestContextRejectsMissingNativeResponseAdmission(t *testing.T) {
	body := &snapshotAdmissionTestBody{Reader: strings.NewReader("unadmitted snapshot")}
	ctx, client := snapshotRequestContext(t.Context(), snapshotAdmissionHTTPClientFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
	}), true)
	if err := checkSnapshotContext(ctx); !errors.Is(err, peerauth.ErrMembership) {
		t.Fatalf("native context admitted work before its response proof: %v", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://snapshot.invalid/Snapshot", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if response != nil || !errors.Is(err, peerauth.ErrMembership) || !body.closed {
		t.Fatalf("unadmitted native response survived: response=%t closed=%t err=%v", response != nil, body.closed, err)
	}
	if err := checkSnapshotContext(ctx); !errors.Is(err, peerauth.ErrMembership) {
		t.Fatalf("failed native capture became eligible for staged processing: %v", err)
	}
}

func TestSnapshotRequestContextDoesNotRefreshByReusingClient(t *testing.T) {
	requests := 0
	base := snapshotAdmissionHTTPClientFunc(func(*http.Request) (*http.Response, error) {
		requests++
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("snapshot"))}, nil
	})
	ctx, client := snapshotRequestContext(t.Context(), base, false)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://snapshot.invalid/Snapshot", nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response, err := client.Do(request); err == nil || response != nil || requests != 1 {
		t.Fatalf("Snapshot wrapper issued a fresh request: requests=%d response=%t err=%v", requests, response != nil, err)
	}
	// A separate Snapshot owns a separate capture rather than reusing that
	// wrapper or overwriting another Snapshot's response admission.
	other, otherClient := snapshotRequestContext(t.Context(), base, false)
	if ctx.Value(snapshotAdmissionContextKey{}) == other.Value(snapshotAdmissionContextKey{}) {
		t.Fatal("independent Snapshot requests shared an admission holder")
	}
	response, err = otherClient.Do(request.WithContext(other))
	if err != nil || requests != 2 {
		t.Fatalf("independent Snapshot could not use its own capture: requests=%d err=%v", requests, err)
	}
	_ = response.Body.Close()
}

func TestSnapshotRequestContextCanceledBeforeTransportAndPreservesTransportError(t *testing.T) {
	for _, native := range []bool{false, true} {
		parent, cancel := context.WithCancel(t.Context())
		cancel()
		requests := 0
		ctx, client := snapshotRequestContext(parent, snapshotAdmissionHTTPClientFunc(func(*http.Request) (*http.Response, error) {
			requests++
			return nil, errors.New("unexpected transport call")
		}), native)
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://snapshot.invalid/Snapshot", nil)
		if err != nil {
			t.Fatal(err)
		}
		if response, err := client.Do(request); response != nil || !errors.Is(err, context.Canceled) || requests != 0 {
			t.Fatalf("canceled Snapshot entered transport: native=%t requests=%d err=%v", native, requests, err)
		}
		if err := checkSnapshotContext(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("native=%t obscured caller cancellation with admission error: %v", native, err)
		}
	}
	want := errors.New("controlled Snapshot transport failure")
	ctx, client := snapshotRequestContext(t.Context(), snapshotAdmissionHTTPClientFunc(func(*http.Request) (*http.Response, error) {
		return nil, want
	}), true)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://snapshot.invalid/Snapshot", nil)
	if err != nil {
		t.Fatal(err)
	}
	if response, err := client.Do(request); response != nil || err != want {
		t.Fatalf("Snapshot transport error changed: response=%t err=%v", response != nil, err)
	}
}
