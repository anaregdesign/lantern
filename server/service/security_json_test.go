package service

import (
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"testing"
)

func TestSecurityJSONRejectsUnknownFields(t *testing.T) {
	codec := securityJSONCodec{}
	for _, data := range []string{
		`{"expectedRevision":"1","changeId":"AQEBAQEBAQEBAQEBAQEBAQ==","changes":[],"permissions":[]}`,
		`{"expectedRevision":"1","changes":[{"putUser":{"identity":{"kind":"SECURITY_PRINCIPAL_KIND_OIDC","issuer":"https://idp.example","subject":"alice"},"state":"SECURITY_PRINCIPAL_STATE_ACTIVE","permissions":["all"]}}]}`,
		`{"expectedRevision":"1","expectedRevision":"2"}`,
		`{"changes":[{"putRole":{"id":"r","rules":[{"id":"r","effect":99}]}}]}`,
	} {
		var request pb.ApplySecurityChangesRequest
		err := codec.Unmarshal([]byte(data), &request)
		// Unknown numeric enums survive protojson intentionally; typed validation
		// must reject them before any control-plane effect.
		if err == nil && strictSecurityMessage(&request) == nil {
			t.Fatalf("accepted %s", data)
		}
	}
	var request pb.ApplySecurityChangesRequest
	if err := codec.Unmarshal([]byte(`{"expectedRevision":"1","changeId":"AQEBAQEBAQEBAQEBAQEBAQ==","changes":[{"deleteRole":"reader"}]}`), &request); err != nil {
		t.Fatal(err)
	}
}
