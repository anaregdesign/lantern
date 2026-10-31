package securitytransport

import (
	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/server/internal/rpcwire"
)

// SecurityHandlerOptions preserves strict JSON decoding and the control service limits.
func SecurityHandlerOptions() []connect.HandlerOption {
	return []connect.HandlerOption{rpcwire.StrictJSONHandlerOption(), connect.WithReadMaxBytes(4 << 20), connect.WithSendMaxBytes(4 << 20)}
}
