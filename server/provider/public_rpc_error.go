package provider

import (
	"errors"
	"net/http"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// Boundary rejection happens before a generated handler can encode its error.
// Preserve the request's Connect, gRPC or gRPC-Web status and expose no policy
// or credential details. Browser login/navigation has a separate HTTP contract.
func publicRPCError(w http.ResponseWriter, req *http.Request, code connect.Code) {
	security.BindCurrentFailure(w)
	w.Header().Set("Cache-Control", "no-store")
	_ = connect.NewErrorWriter().Write(w, req, connect.NewError(code, errors.New("public RPC admission rejected")))
}
