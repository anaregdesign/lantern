package provider

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestSecurityManagementAuthorizationAdmissionAndNavigationBoundary(t *testing.T) {
	runtime, clock, session, csrf := browserSessionFixture(t)
	ctx, _, err := runtime.authenticateBrowser(browserFixtureRequest(session, csrf), true)
	if err != nil {
		t.Fatal(err)
	}
	admission, _ := security.AdmissionFromContext(ctx)
	current, _ := runtime.native.Store().Current()
	issuer, _ := current.Snapshot().Issuer(admission.Identity().Issuer)
	issuer.URL = "https://new.example"
	issuer.EnvOwned = false
	issuer.ConfigRevision = 0
	prepared, err := runtime.native.Store().PrepareManagement(t.Context(), security.ManagementRequest{ExpectedRevision: current.Sequence(), ChangeID: [16]byte{9}, Actor: admission.Identity(), Admission: admission, Now: clock.Now(), Changes: []security.Change{{Kind: security.PutIssuer, Issuer: &issuer}}})
	if err != nil {
		t.Fatal(err)
	}
	start, startURL, err := runtime.beginManagementAuthorization(ctx, admission, prepared.Binding)
	if err != nil || startURL == "" {
		t.Fatal(err)
	}
	status, err := runtime.readManagementAuthorization(ctx, admission, start.ID)
	if err != nil || status.State != security.AuthorizationPending {
		t.Fatal(status, err)
	}
	for _, path := range []string{"/auth/management-authorization/invalid", "/auth/management-authorization/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA?purpose=login"} {
		request := browserFixtureRequest(session, csrf)
		request.Method = http.MethodGet
		request.URL, err = url.Parse("https://admin.example" + path)
		if err != nil {
			t.Fatal(err)
		}
		response := httptest.NewRecorder()
		runtime.AuthHTTPHandler().ServeHTTP(response, request)
		if response.Code == http.StatusFound || len(response.Result().Cookies()) != 0 {
			t.Fatal("unbound navigation started authentication", response.Code)
		}
	}
	after, _ := runtime.native.Store().Current()
	if after.Digest() != current.Digest() {
		t.Fatal("approval start rotated the ordinary session")
	}
}
