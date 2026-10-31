package securitytransport

import (
	"io"
	"net/http"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/encoding/protojson"
)

func (r *AuthenticationAdapter) currentBrowserLogout(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost || req.URL.RawQuery != "" || r.browserOrigin(req, true) != nil || req.Header.Get("Content-Type") != "application/json" {
		browserHTTPError(w, connect.CodeInvalidArgument)
		return
	}
	raw, err := io.ReadAll(io.LimitReader(req.Body, 16<<10+1))
	var request pb.CurrentLogoutRequest
	if err != nil || len(raw) > 16<<10 || oidc.ValidateJSON(raw) != nil || protojson.Unmarshal(raw, &request) != nil {
		browserHTTPError(w, connect.CodeInvalidArgument)
		return
	}
	profile, err := DecodeCurrentProfile(request.Profile)
	if err != nil || profile != r.current.Profile() || request.LocalOnly && (request.PrepareOnly || request.Review != nil) || request.PrepareOnly && request.Review != nil {
		browserHTTPError(w, connect.CodeInvalidArgument)
		return
	}
	if request.LocalOnly {
		csrf, err := browserCookie(req, csrfCookieName)
		if err != nil || !exactBrowserHeader(req.Header, browserCSRFHeader, csrf) {
			browserHTTPError(w, connect.CodePermissionDenied)
			return
		}
		if r.current.BindPublicMetadata(req.Context(), security.CurrentLocalCookieClear) != nil {
			browserHTTPError(w, connect.CodeUnavailable)
			return
		}
		browserClearCookie(w, sessionCookieName, http.SameSiteStrictMode)
		browserClearCookie(w, csrfCookieName, http.SameSiteStrictMode)
		r.writeBrowserProto(w, req.Context(), &pb.SessionRevocation{LocalCookieCleared: true})
		return
	}
	ctx, _, err := r.authenticateBrowser(req, true)
	if err != nil {
		browserHTTPError(w, connect.CodeOf(err))
		return
	}
	a, _ := security.AdmissionFromContext(ctx)
	if request.PrepareOnly {
		review, err := r.workflow.PrepareLogout(ctx, a)
		if err != nil {
			browserHTTPError(w, workflowCode(err))
			return
		}
		wire, err := EncodeCurrentSessionReview(review)
		if err != nil || a.BindCurrentSelfOutput(ctx) != nil {
			browserHTTPError(w, connect.CodeUnavailable)
			return
		}
		r.writeBrowserProto(w, req.Context(), &pb.SessionRevocation{CurrentReview: wire})
		return
	}
	review, err := DecodeCurrentSessionReview(r.current, request.Review)
	if err != nil || review.Operation.Actor() != a.Identity() {
		browserHTTPError(w, connect.CodeInvalidArgument)
		return
	}
	result, err := r.workflow.ApplyLogout(ctx, a, review)
	if err != nil {
		browserHTTPError(w, workflowCode(err))
		return
	}
	response := &pb.SessionRevocation{LocalCookieCleared: true}
	if result != nil {
		response.CurrentResult = EncodeCurrentResult(*result)
	}
	browserClearCookie(w, sessionCookieName, http.SameSiteStrictMode)
	browserClearCookie(w, csrfCookieName, http.SameSiteStrictMode)
	r.writeBrowserProto(w, req.Context(), response)
}
