package client

import (
	"context"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// ServerStatus is the flat snapshot returned by Lantern.GetServerStatus.
// Exposed as a true Go alias of the generated proto type so callers can
// freely pass it across SDK / pb boundaries without conversion — matches
// the Vertex / Edge alias pattern already established in client.go.
type ServerStatus = pb.GetServerStatusResponse

// ServerStartedAt returns the wall-clock instant the server began
// serving requests, or the zero time when the server did not include
// the timestamp (e.g. WithStatusInfo was never wired in a test path).
func ServerStartedAt(s *ServerStatus) time.Time {
	if s == nil || s.StartedAt == nil {
		return time.Time{}
	}
	return s.StartedAt.AsTime()
}

// ServerUptime returns the server's reported uptime (now - started_at on
// the server's clock), or 0 when the server did not include the field.
// Prefer this over computing uptime from the local clock so a client
// with a skewed clock still sees a sensible value.
func ServerUptime(s *ServerStatus) time.Duration {
	if s == nil || s.Uptime == nil {
		return 0
	}
	return s.Uptime.AsDuration()
}

// GetServerStatus returns the server's flat status snapshot — build/
// version, configuration ceilings, TLS / replication flags, and live
// vertex / edge counts. Cheap to call (O(1) on the server) and intended
// for the admin UI's "Ops" tab plus lightweight smoke-test tooling.
//
// The returned pointer is always non-nil on success; default-zero fields
// are valid and mean "the server did not surface this value" (typically
// because WithStatusInfo was not wired in a test path).
func (l *Lantern) GetServerStatus(ctx context.Context) (*ServerStatus, error) {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	resp, err := unary(ctx, &pb.GetServerStatusRequest{}, l.client.GetServerStatus)
	if err != nil {
		return nil, err
	}
	return resp, nil
}
