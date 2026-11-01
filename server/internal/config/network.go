package config

import (
	"github.com/anaregdesign/lantern/server/internal/envconfig"
)

// NetConfig groups the primary listener and message-size / concurrency caps.
//
//   - LANTERN_PORT                       (default 6380)
//   - LANTERN_MAX_RECV_MSG_BYTES         (default 16 MiB; request messages)
//   - LANTERN_MAX_SEND_MSG_BYTES         (default 16 MiB; responses and the
//     certified full-mutation SubscribeResponse admission bound)
//   - LANTERN_MAX_CONCURRENT_STREAMS     (default 1024; 0 = unlimited)
type NetConfig struct {
	Port                 int
	MaxRecvMsgBytes      int
	MaxSendMsgBytes      int
	MaxConcurrentStreams uint32
}

// RateLimitConfig is the process-wide token bucket policy.
//
//   - LANTERN_RATE_LIMIT_RPS             0 disables rate limiting (default 0)
//   - LANTERN_RATE_LIMIT_BURST           token bucket burst (default 2x RPS)
type RateLimitConfig struct {
	RPS   float64
	Burst int
}

func loadNetwork() NetConfig {
	return NetConfig{
		Port:                 envconfig.Int("LANTERN_PORT", 6380),
		MaxRecvMsgBytes:      envconfig.Int("LANTERN_MAX_RECV_MSG_BYTES", 16*1024*1024),
		MaxSendMsgBytes:      envconfig.Int("LANTERN_MAX_SEND_MSG_BYTES", 16*1024*1024),
		MaxConcurrentStreams: envconfig.Uint32("LANTERN_MAX_CONCURRENT_STREAMS", 1024),
	}
}

func loadRateLimit() RateLimitConfig {
	rps := envconfig.Float("LANTERN_RATE_LIMIT_RPS", 0)
	burst := envconfig.Int("LANTERN_RATE_LIMIT_BURST", int(2*rps))
	if burst <= 0 && rps > 0 {
		burst = int(2 * rps)
	}
	return RateLimitConfig{RPS: rps, Burst: burst}
}
