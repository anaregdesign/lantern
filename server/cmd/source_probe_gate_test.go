package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
	"github.com/anaregdesign/lantern/server/provider"
)

// Shared by the normal child and the controlled real-App regression.
func offSourcePrivateProbe(ctx context.Context, identity *provider.PeerIdentityRuntime, self string) bool {
	origins, err := identity.ApprovedOrigins()
	_, port, portErr := net.SplitHostPort(self)
	if err != nil || portErr != nil || len(origins) != 3 {
		return false
	}
	for _, origin := range origins {
		if strings.HasSuffix(origin, ":"+port) {
			continue
		}
		if err := offSourcePeerProbe(ctx, identity, origin); err != nil {
			return false
		}
	}
	return true
}

func offSourcePeerProbe(ctx context.Context, identity *provider.PeerIdentityRuntime, origin string) error {
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
