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
	factory := func(procedure string, _ ...connect.HandlerOption) (string, http.Handler) {
		calls++
		if procedure != "" {
			t.Fatal("OFF factory received a selected procedure", procedure)
		}
		return "/rpc", http.NotFoundHandler()
	}
	r := &SecurityRuntime{}
	_, h := r.publicRPCHandler("/rpc", factory)
	for range 2 {
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/rpc", nil))
	}
	if calls != 1 {
		t.Fatal("legacy handler factory changed")
	}
	r.current = &security.CurrentAuthority{}
	for _, output := range []*security.CurrentOutput{nil, {}} {
		r.output = output
		_, h = r.publicRPCHandler("/rpc", factory)
		func() {
			defer func() {
				if recover() != http.ErrAbortHandler {
					t.Fatal("missing current request owner did not abort")
				}
			}()
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/rpc", nil))
		}()
	}
	if calls != 1 {
		t.Fatal("current encoder allocated before request credit")
	}
}
