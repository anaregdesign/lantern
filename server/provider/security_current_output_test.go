package provider

import (
	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/server/internal/security"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCurrentRPCFactoryRequiresOwnedRequestBeforeAllocation(t *testing.T) {
	calls := 0
	factory := func(...connect.HandlerOption) (string, http.Handler) { calls++; return "/rpc", http.NotFoundHandler() }
	r := &SecurityRuntime{}
	_, h := r.publicRPCHandler("/rpc", factory)
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/rpc", nil))
	if calls != 1 {
		t.Fatal("legacy handler factory changed")
	}
	r.current = &security.CurrentAuthority{}
	_, h = r.publicRPCHandler("/rpc", factory)
	func() {
		defer func() {
			if recover() != http.ErrAbortHandler {
				t.Fatal("missing current owner did not abort")
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/rpc", nil))
	}()
	if calls != 1 {
		t.Fatal("current encoder allocated before request credit")
	}
}
