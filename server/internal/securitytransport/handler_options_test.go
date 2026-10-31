package securitytransport

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestSecurityJSONRejectsUnknownFields(t *testing.T) {
	handler := connect.NewUnaryHandler("/test.Control/Apply", func(_ context.Context, request *connect.Request[pb.ApplySecurityChangesRequest]) (*connect.Response[pb.ApplySecurityChangesResponse], error) {
		if err := securityRequestError(request.Msg); err != nil {
			return nil, err
		}
		return connect.NewResponse(&pb.ApplySecurityChangesResponse{}), nil
	}, SecurityHandlerOptions()...)
	invoke := func(data string) int {
		request := httptest.NewRequest(http.MethodPost, "/test.Control/Apply", strings.NewReader(data))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Connect-Protocol-Version", "1")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response.Code
	}
	for _, data := range []string{
		`{"expectedRevision":"1","changeId":"AQEBAQEBAQEBAQEBAQEBAQ==","changes":[],"permissions":[]}`,
		`{"expectedRevision":"1","changes":[{"putUser":{"identity":{"kind":"SECURITY_PRINCIPAL_KIND_OIDC","issuer":"https://idp.example","subject":"alice"},"state":"SECURITY_PRINCIPAL_STATE_ACTIVE","permissions":["all"]}}]}`,
		`{"expectedRevision":"1","expectedRevision":"2"}`,
		`{"changes":[{"putRole":{"id":"r","rules":[{"id":"r","effect":99}]}}]}`,
	} {
		// Unknown numeric enums survive protojson intentionally; typed validation
		// must reject them before any control-plane effect.
		if status := invoke(data); status != http.StatusBadRequest {
			t.Fatalf("accepted %s", data)
		}
	}
	if status := invoke(`{"expectedRevision":"1","changeId":"AQEBAQEBAQEBAQEBAQEBAQ==","changes":[{"deleteRole":"reader"}]}`); status != http.StatusOK {
		t.Fatal("valid control JSON rejected", status)
	}
}
