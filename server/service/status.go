package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"runtime"
	"runtime/debug"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// StatusInfo is the static slice of GetServerStatus that the wire layer
// can fully populate at boot from the various Config sub-structs. The
// dynamic pieces — startedAt, uptime, vertex/edge counts — are filled in
// at request time by GetServerStatus itself.
//
// Kept as a plain value struct (no constructor) so tests can build one
// inline without dragging in provider/.
type StatusInfo struct {
	Version            string
	DefaultTTL         time.Duration
	MaxBatchSize       uint32
	MaxKeyBytes        uint32
	ScanDefaultLimit   uint32
	ScanMaxLimit       uint32
	TLSEnabled         bool
	ReplicationEnabled bool
}

// WithStatusInfo records the boot-time configuration snapshot that
// GetServerStatus echoes back to admin clients. Builder-pattern sibling
// of WithScanLimits / WithReplication etc. — kept additive so existing
// tests that construct LanternService without status info still compile.
func (s *LanternService) WithStatusInfo(info StatusInfo) *LanternService {
	s.statusInfo = info
	return s
}

// MarkStarted records the wall-clock instant the server began serving
// requests. Intended to be called from the LanternServer lifecycle right
// before the Connect listener starts accepting so the value reflects
// "ready to serve" rather than wire-init time. Safe to call multiple
// times — only the first call wins so a hot-reload that re-invokes Run
// does not reset the uptime
// gauge mid-flight.
func (s *LanternService) MarkStarted(t time.Time) {
	s.startedAtOnce.Do(func() {
		s.startedAt = t
	})
}

// Uptime returns the duration since MarkStarted was called, or 0 when the
// server has not yet been marked started. Exposed so the gateway-side
// /v1/health probe (#316) can include uptime_seconds in its JSON body
// without re-deriving the start instant.
func (s *LanternService) Uptime() time.Duration {
	if s.startedAt.IsZero() {
		return 0
	}
	return time.Since(s.startedAt)
}

// GetServerStatus returns a snapshot of the server's identity, build
// info, configuration ceilings, and current live vertex/edge counts. The
// cache-count fields are read directly from the in-memory index — O(1)
// and safe to call from any RPC. When WithStatusInfo was not wired (the
// test path) every static field defaults to its zero value; the response
// is still well-formed.
func (s *LanternService) GetServerStatus(ctx context.Context, _ *pb.GetServerStatusRequest) (*pb.GetServerStatusResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	now := time.Now()
	resp := &pb.GetServerStatusResponse{
		Version:            statusVersion(s.statusInfo.Version),
		GoVersion:          runtime.Version(),
		DefaultTtl:         durationpb.New(s.statusInfo.DefaultTTL),
		MaxBatchSize:       s.statusInfo.MaxBatchSize,
		MaxKeyBytes:        s.statusInfo.MaxKeyBytes,
		ScanDefaultLimit:   s.statusInfo.ScanDefaultLimit,
		ScanMaxLimit:       s.statusInfo.ScanMaxLimit,
		TlsEnabled:         s.statusInfo.TLSEnabled,
		ReplicationEnabled: s.statusInfo.ReplicationEnabled,
		VertexCount:        uint64(s.cache.VertexCount()),
		EdgeCount:          uint64(s.cache.EdgeCount()),
		Search:             s.searchCapabilities(),
	}
	if !s.startedAt.IsZero() {
		resp.StartedAt = timestamppb.New(s.startedAt)
		resp.Uptime = durationpb.New(now.Sub(s.startedAt))
	}
	return resp, nil
}

func (s *LanternService) searchCapabilities() *pb.SearchCapabilities {
	capabilities := &pb.SearchCapabilities{
		Enabled:               s.search.Enabled,
		PositionsEnabled:      s.search.Enabled && s.search.PositionsEnabled,
		DefaultLimit:          s.search.DefaultLimit,
		MaxLimit:              s.search.MaxLimit,
		DefaultMatchMode:      matchModeToPB(s.search.DefaultMode),
		DefaultMinShouldMatch: s.search.DefaultMinShould,
		MaxFuzziness:          searchMaxFuzziness,
		AnalyzerVersion:       searchAnalyzerVersion,
		ProjectionVersion:     searchProjectionVersion,
	}
	canonical := fmt.Sprintf(
		"enabled=%t\npositions=%t\ndefault_limit=%d\nmax_limit=%d\ndefault_match_mode=%d\ndefault_min_should_match=%d\nmax_fuzziness=%d\nanalyzer=%s\nprojection=%s\n",
		capabilities.Enabled,
		s.search.PositionsEnabled,
		capabilities.DefaultLimit,
		capabilities.MaxLimit,
		capabilities.DefaultMatchMode,
		capabilities.DefaultMinShouldMatch,
		capabilities.MaxFuzziness,
		capabilities.AnalyzerVersion,
		capabilities.ProjectionVersion,
	)
	sum := sha256.Sum256([]byte(canonical))
	capabilities.ConfigFingerprint = hex.EncodeToString(sum[:])
	return capabilities
}

// statusVersion returns the configured version string, falling back to
// the VCS-stamped main module version baked in by `go build` (modern
// Go embeds it in runtime/debug.BuildInfo). When neither is available
// — typically a `go run ./server/cmd` invocation in development — the
// string "dev" is returned so admin clients always render *something*.
func statusVersion(configured string) string {
	if configured != "" {
		return configured
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if info.Main.Version != "" && info.Main.Version != "(devel)" {
			return info.Main.Version
		}
		for _, s := range info.Settings {
			if s.Key == "vcs.revision" && s.Value != "" {
				if len(s.Value) > 12 {
					return s.Value[:12]
				}
				return s.Value
			}
		}
	}
	return "dev"
}
