package client

import (
	"errors"
	"fmt"
	"strings"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/resolver"
	"google.golang.org/grpc/resolver/manual"
)

// roundRobinServiceConfig adds round_robin client-side load balancing on top
// of the default retry policy. Used by NewLanternWithEndpoints and by the
// "dns:///" form of NewLantern so calls fan out across every backend the
// resolver reports.
//
// Retry semantics match defaultServiceConfig (AddEdge stays excluded because
// it is additive and not safe to retry blindly).
const roundRobinServiceConfig = `{
  "loadBalancingConfig": [{"round_robin": {}}],
  "methodConfig": [
    {
      "name": [
        {"service": "graph.v1.LanternService", "method": "GetVertex"},
        {"service": "graph.v1.LanternService", "method": "GetEdge"},
        {"service": "graph.v1.LanternService", "method": "PutVertex"},
        {"service": "graph.v1.LanternService", "method": "PutEdge"},
        {"service": "graph.v1.LanternService", "method": "DeleteVertex"},
        {"service": "graph.v1.LanternService", "method": "DeleteEdge"},
        {"service": "graph.v1.LanternService", "method": "DeleteVertices"},
        {"service": "graph.v1.LanternService", "method": "DeleteEdges"},
        {"service": "graph.v1.LanternService", "method": "Illuminate"}
      ],
      "retryPolicy": {
        "maxAttempts": 4,
        "initialBackoff": "0.1s",
        "maxBackoff": "5s",
        "backoffMultiplier": 2,
        "retryableStatusCodes": ["UNAVAILABLE", "RESOURCE_EXHAUSTED"]
      }
    }
  ]
}`

// NewLanternWithEndpoints dials a fixed set of host:port endpoints and
// returns a connected Lantern client that fans out RPCs across every
// reachable backend using gRPC's built-in round_robin load balancer.
//
// Use this when:
//   - You have an explicit list of pod IPs or hosts (e.g. read from
//     environment, config map, or a non-DNS discovery source) and want
//     transparent client-side load balancing with automatic failover.
//   - You want to start sending traffic immediately to a known set of nodes
//     without depending on DNS TTLs.
//
// For single-endpoint and DNS-based discovery, use NewLantern instead:
//
//   - NewLantern("pod-a:50051")         → single backend, no LB.
//   - NewLantern("dns:///lantern:50051") → DNS resolver + round_robin (works
//     out of the box for k8s Services and Compose service names).
//   - NewLanternWithEndpoints([]string{"pod-a:50051", "pod-b:50051"})
//     → explicit static list + round_robin.
//
// Empty input is rejected with an error so callers cannot accidentally dial
// nothing. Retry policy defaults match NewLantern; override with
// WithDefaultServiceConfig if you need different semantics.
func NewLanternWithEndpoints(endpoints []string, opts ...Option) (*Lantern, error) {
	if len(endpoints) == 0 {
		return nil, errors.New("client: NewLanternWithEndpoints requires at least one endpoint")
	}
	for i, ep := range endpoints {
		if strings.TrimSpace(ep) == "" {
			return nil, fmt.Errorf("client: NewLanternWithEndpoints endpoint[%d] is empty", i)
		}
	}

	o := options{
		batchChunkSize:    defaultBatchChunkSize,
		serviceConfigJSON: roundRobinServiceConfig,
		keepalive:         defaultKeepalive,
	}
	for _, apply := range opts {
		apply(&o)
	}

	r := manual.NewBuilderWithScheme("lantern-static")
	addrs := make([]resolver.Address, 0, len(endpoints))
	for _, ep := range endpoints {
		addrs = append(addrs, resolver.Address{Addr: ep})
	}
	r.InitialState(resolver.State{Addresses: addrs})

	dialOpts := make([]grpc.DialOption, 0, len(o.dialOptions)+4)
	dialOpts = append(dialOpts, grpc.WithResolvers(r))
	dialOpts = append(dialOpts, grpc.WithKeepaliveParams(o.keepalive))
	if o.transportCreds != nil {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(o.transportCreds))
	} else {
		dialOpts = append(dialOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}
	if o.serviceConfigJSON != "" {
		dialOpts = append(dialOpts, grpc.WithDefaultServiceConfig(o.serviceConfigJSON))
	}
	dialOpts = append(dialOpts, o.dialOptions...)

	target := r.Scheme() + ":///lantern"
	conn, err := grpc.NewClient(target, dialOpts...)
	if err != nil {
		return nil, err
	}
	return &Lantern{
		conn:   conn,
		client: pb.NewLanternServiceClient(conn),
		opts:   o,
	}, nil
}
