package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
)

type offSourceProbeIdentity interface {
	ApprovedOrigins() ([]string, error)
	HTTPClient() *http.Client
}

// Shared by the normal child and the controlled real-App regression.
func offSourcePrivateProbe(ctx context.Context, identity offSourceProbeIdentity) error {
	origins, err := identity.ApprovedOrigins()
	if err != nil {
		return fmt.Errorf("approved origins: %w", err)
	}
	// ApprovedOrigins excludes this member. The three-member cohort has two
	// remote origins, both of which must pass the unchanged private RPC checks.
	if len(origins) != 2 {
		return fmt.Errorf("approved remote origins: got %d, want 2", len(origins))
	}
	for _, origin := range origins {
		if err := offSourcePeerProbe(ctx, identity, origin); err != nil {
			return fmt.Errorf("private peer %s: %w", origin, err)
		}
	}
	return nil
}

func offSourcePeerProbe(ctx context.Context, identity offSourceProbeIdentity, origin string) error {
	client := graphv1connect.NewLanternReplicationServiceClient(identity.HTTPClient(), origin)
	if _, err := client.PeerStatus(ctx, connect.NewRequest(&pb.PeerStatusRequest{NamespaceFormat: keyspace.Version})); err != nil {
		return fmt.Errorf("positive PeerStatus: %w", err)
	}
	request := connect.NewRequest(&pb.PeerStatusRequest{NamespaceFormat: keyspace.Version})
	request.Header().Set("Authorization", "Bearer off-source-negative-fixture")
	// The workload client rejects public credentials before sending them. This
	// proves its client guard, not a server-ingress negative request.
	_, err := client.PeerStatus(ctx, request)
	if err == nil {
		return errors.New("workload client accepted public credentials")
	}
	return nil
}

type offProbeFixtureIdentity struct {
	origins []string
	err     error
	client  *http.Client
}

func (f offProbeFixtureIdentity) ApprovedOrigins() ([]string, error) { return f.origins, f.err }
func (f offProbeFixtureIdentity) HTTPClient() *http.Client           { return f.client }

type offProbeRoundTrip func(*http.Request) (*http.Response, error)

func (f offProbeRoundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type offProbeStatusHandler struct {
	graphv1connect.UnimplementedLanternReplicationServiceHandler
	t *testing.T
}

func (h offProbeStatusHandler) PeerStatus(_ context.Context, r *connect.Request[pb.PeerStatusRequest]) (*connect.Response[pb.PeerStatusResponse], error) {
	if r.Msg.GetNamespaceFormat() != keyspace.Version {
		h.t.Fatal("probe omitted namespace format")
	}
	return connect.NewResponse(&pb.PeerStatusResponse{}), nil
}

func TestOFFPrivateProbeRemoteOriginsAndErrors(t *testing.T) {
	originErr := errors.New("synthetic membership refusal")
	for _, tc := range []struct {
		name          string
		origins       []string
		membershipErr error
		positiveErr   bool
		allowBearer   bool
		wantError     string
		wantPositive  int
	}{
		{name: "two remote peers", origins: []string{"https://peer-1.invalid", "https://peer-2.invalid"}, wantPositive: 2},
		{name: "membership error", membershipErr: originErr, wantError: "approved origins: synthetic membership refusal"},
		{name: "missing peer", origins: []string{"https://peer-1.invalid"}, wantError: "got 1, want 2"},
		{name: "self-inclusive topology", origins: []string{"https://peer-1.invalid", "https://peer-2.invalid", "https://self.invalid"}, wantError: "got 3, want 2"},
		{name: "positive RPC error retained", origins: []string{"https://peer-1.invalid", "https://peer-2.invalid"}, positiveErr: true, wantError: "positive PeerStatus:", wantPositive: 1},
		{name: "public credential refusal required", origins: []string{"https://peer-1.invalid", "https://peer-2.invalid"}, allowBearer: true, wantError: "workload client accepted public credentials", wantPositive: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, handler := graphv1connect.NewLanternReplicationServiceHandler(offProbeStatusHandler{t: t})
			positives := 0
			// Serve Connect requests in memory. No socket, DNS, TLS dial, App or
			// native time/source owner is constructed by these local tests.
			client := &http.Client{Transport: offProbeRoundTrip(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Authorization") != "" {
					if !tc.allowBearer {
						return nil, errors.New("synthetic client credential guard")
					}
				} else {
					positives++
					if tc.positiveErr {
						return nil, errors.New("synthetic peer failure")
					}
				}
				recorder := httptest.NewRecorder()
				handler.ServeHTTP(recorder, r)
				return recorder.Result(), nil
			})}
			err := offSourcePrivateProbe(t.Context(), offProbeFixtureIdentity{origins: tc.origins, err: tc.membershipErr, client: client})
			if tc.wantError == "" {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Fatalf("got %v, want error containing %q", err, tc.wantError)
			}
			if tc.membershipErr != nil && !errors.Is(err, tc.membershipErr) {
				t.Fatal("membership cause lost")
			}
			if positives != tc.wantPositive {
				t.Fatalf("positive probes=%d, want %d", positives, tc.wantPositive)
			}
		})
	}
}
