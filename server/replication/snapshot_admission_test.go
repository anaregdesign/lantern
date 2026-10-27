package replication

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
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

// This fixture supplies deterministic native elapsed evidence to a real
// peerauth.Admission; it makes no real-provider or transport qualification.
type snapshotAdmissionObservation struct {
	owner   *byte
	now     time.Time
	elapsed time.Duration
}

func (o snapshotAdmissionObservation) Bounds() (time.Time, time.Time) { return o.now, o.now }
func (o snapshotAdmissionObservation) ElapsedSince(prior peerauth.TimeObservation) (time.Duration, error) {
	previous, ok := prior.(snapshotAdmissionObservation)
	if !ok || previous.owner != o.owner || o.elapsed < previous.elapsed {
		return 0, peerauth.ErrMembership
	}
	return o.elapsed - previous.elapsed, nil
}

type snapshotAdmissionExpiryGraph struct {
	*snapshotLifecycleGraph
	afterWrite func()
}

func (g *snapshotAdmissionExpiryGraph) PutVertexWithExpirationHLC(key string, vertex *pb.Vertex, expiration time.Time, stamp hlc.Timestamp) bool {
	result := g.snapshotLifecycleGraph.PutVertexWithExpirationHLC(key, vertex, expiration, stamp)
	if g.afterWrite != nil {
		g.afterWrite()
	}
	return result
}

func TestSnapshotOriginalAdmissionExpiryDiagnosticsAndVerifiedRepair(t *testing.T) {
	for _, phase := range []string{"before live", "after first live write", "source transport failure"} {
		t.Run(phase, func(t *testing.T) {
			public, private, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 10, 27, 0, 0, 0, 0, time.UTC)
			sample := snapshotAdmissionObservation{owner: new(byte), now: now}
			identity, _ := url.Parse("spiffe://lantern.test/snapshot-peer")
			leaf := &x509.Certificate{URIs: []*url.URL{identity}, RawSubjectPublicKeyInfo: []byte("snapshot test peer"), NotBefore: now.Add(-time.Hour), NotAfter: now.Add(time.Hour)}
			member := peerauth.Member{ID: [16]byte{3}, Identity: identity.String(), SPKI: sha256.Sum256(leaf.RawSubjectPublicKeyInfo), Origin: "https://localhost:6381"}
			domain := peerauth.Domain{Deployment: [16]byte{1}, NamespaceFormat: keyspace.Version, AuthMode: "off", TrustDigest: [32]byte{2}}
			manifest := peerauth.Manifest{Version: 1, Domain: domain, IssuedAt: now, ExpiresAt: now.Add(5 * time.Minute), Members: []peerauth.Member{member}}
			signed, err := peerauth.SignManifest(manifest, private)
			if err != nil {
				t.Fatal(err)
			}
			store, err := peerauth.CreateStore(peerauth.StoreOptions{Path: filepath.Join(t.TempDir(), "membership"), Key: public, Domain: domain,
				Time: func() (peerauth.TimeObservation, error) { return sample, nil }}, signed)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := store.Close(); err != nil {
					t.Error(err)
				}
			}()
			state := &tls.ConnectionState{Version: tls.VersionTLS13, PeerCertificates: []*x509.Certificate{leaf}, VerifiedChains: [][]*x509.Certificate{{leaf}}}
			admission, err := store.Admit(state, member.Origin)
			if err != nil {
				t.Fatal(err)
			}
			originalExpiry := admission.ExpiresAt()
			owner := &snapshotResponseAdmission{native: true, admission: admission}
			ctx := context.WithValue(t.Context(), snapshotAdmissionContextKey{}, owner)
			var events []string
			apply := &snapshotLifecycleMutationApplier{events: &events}
			graph := &snapshotAdmissionExpiryGraph{snapshotLifecycleGraph: &snapshotLifecycleGraph{events: &events}}
			expire := func() { sample.elapsed = peerauth.MaxRPCLifetime }
			stream := &snapshotStageObservedStream{SnapshotStream: &snapshotSliceStream{frames: graphInstallerLifecycleFrames()}}
			transportFailure := errors.New("controlled final transport failure after valid native checks")
			if phase == "source transport failure" {
				stream.SnapshotStream.(*snapshotSliceStream).err = transportFailure
			} else if phase == "before live" {
				stream.after = func(open bool) {
					if !open {
						expire()
					}
				}
			} else {
				graph.afterWrite = expire
			}
			installer := testGraphOnlySnapshotInstaller(t, apply, graph)
			result, err := installSnapshot(ctx, installer, stream)
			var failure *snapshotInstallError
			if phase == "source transport failure" {
				if !errors.Is(err, transportFailure) || err.Error() != transportFailure.Error() || !errors.As(err, &failure) {
					t.Fatalf("transport failure after valid native checks was changed: %v", err)
				}
				last := failure.diagnostic.admission
				if last.Reason != "none" || !last.RemainingKnown || last.OriginalBudgetRemaining <= 0 || failure.diagnostic.liveStarted || len(events) != 0 {
					t.Fatalf("transport failure was mislabeled as native rejection: detail=%+v events=%v", failure.diagnostic, events)
				}
				return
			}
			if !errors.Is(err, peerauth.ErrMembership) || err.Error() != peerauth.ErrMembership.Error() || !errors.As(err, &failure) || result.Header != nil {
				t.Fatalf("original native expiry was not preserved: result=%+v err=%v", result, err)
			}
			detail := failure.diagnostic
			if detail.admission.Reason != "native_lifetime_expired" || !detail.admission.RemainingKnown || detail.admission.OriginalBudgetRemaining != 0 || !admission.ExpiresAt().Equal(originalExpiry) {
				t.Fatalf("missing original-budget failure or extended admission: %+v", detail)
			}
			if phase == "before live" {
				if detail.liveStarted || detail.phase != "capture_and_validate" || len(events) != 0 || len(apply.finishes) != 0 {
					t.Fatalf("pre-live expiry touched publication: detail=%+v events=%v", detail, events)
				}
			} else if !detail.liveStarted || detail.phase != "live_replay" || len(apply.finishes) != 1 || apply.finishes[0] || len(apply.cutoffs) != 0 {
				t.Fatalf("live expiry was verified or not retained for repair: detail=%+v events=%v finishes=%v", detail, events, apply.finishes)
			}
			// A signed renewal still cannot extend this captured admission. A later
			// independent attempt may obtain fresh authority and verify the same image.
			manifest.Version++
			signed, err = peerauth.SignManifest(manifest, private)
			if err != nil {
				t.Fatal(err)
			}
			if err := store.Apply(signed); err != nil {
				t.Fatal(err)
			}
			if !errors.Is(admission.Check(t.Context()), peerauth.ErrMembership) {
				t.Fatal("membership renewal extended original Snapshot")
			}
			fresh, err := store.Admit(state, member.Origin)
			if err != nil {
				t.Fatal(err)
			}
			repairedCtx := context.WithValue(t.Context(), snapshotAdmissionContextKey{}, &snapshotResponseAdmission{native: true, admission: fresh})
			graph.afterWrite = nil
			before := len(apply.finishes)
			result, err = installSnapshot(repairedCtx, installer, &snapshotSliceStream{frames: graphInstallerLifecycleFrames()})
			if err != nil || result.Header == nil || len(apply.finishes) != before+1 || !apply.finishes[before] {
				t.Fatalf("fresh-admission verified repair failed: result=%+v finishes=%v err=%v", result, apply.finishes, err)
			}
		})
	}
}
