package service

import (
	"context"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/server/internal/authn"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

const nativeAppleCallbackPrefix = "/auth/native/apple/callback/"

func (r *AuthenticationAdapter) nativeAppleCallback(w http.ResponseWriter, req *http.Request) {
	fail := func(code connect.Code) {
		if r.current != nil && r.current.BindPublicMetadata(req.Context(), security.CurrentGenericFailure) != nil {
			panic(http.ErrAbortHandler)
		}
		browserHTTPError(w, code)
	}
	if r.workflow == nil || !r.workflow.NativeEnabled() {
		fail(connect.CodeFailedPrecondition)
		return
	}
	profile, input, err := decodeNativeAppleCallback(req)
	if err != nil {
		fail(connect.CodeInvalidArgument)
		return
	}
	ctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
	defer cancel()
	pkg, err := r.workflow.StageAppleCallback(ctx, profile, input)
	if err != nil {
		fail(connect.CodeOf(nativeError(err)))
		return
	}
	// The stock plugin consumes provider code/id_token/state. The package comes
	// only from committed trust; no request can choose a redirect destination.
	query := url.Values{"code": {input.AuthorizationCode}, "id_token": {input.IDToken}, "state": {input.State}}
	w.Header().Set("Location", "intent://callback?"+query.Encode()+"#Intent;package="+pkg+";scheme=signinwithapple;end")
	w.WriteHeader(http.StatusSeeOther)
}

func decodeNativeAppleCallback(req *http.Request) (string, oidc.NativeInput, error) {
	media, params, err := mime.ParseMediaType(req.Header.Get("Content-Type"))
	if req.Method != http.MethodPost || req.URL.RawQuery != "" || req.URL.ForceQuery || req.URL.RawPath != "" || req.Header.Get("Content-Encoding") != "" || len(req.Header.Values("Authorization")) != 0 ||
		err != nil || media != "application/x-www-form-urlencoded" || len(params) > 1 || len(params) == 1 && !strings.EqualFold(params["charset"], "utf-8") {
		return "", oidc.NativeInput{}, authn.ErrInvalidInput
	}
	profile := strings.TrimPrefix(req.URL.Path, nativeAppleCallbackPrefix)
	if profile == "" || len(profile) > 64 || strings.Contains(profile, "/") {
		return "", oidc.NativeInput{}, authn.ErrInvalidInput
	}
	raw, err := io.ReadAll(io.LimitReader(req.Body, NativeAuthMaxBytes+1))
	if err != nil || len(raw) > NativeAuthMaxBytes {
		return "", oidc.NativeInput{}, authn.ErrInvalidInput
	}
	form, err := url.ParseQuery(string(raw))
	if err != nil {
		return "", oidc.NativeInput{}, authn.ErrInvalidInput
	}
	for name, values := range form {
		if len(values) != 1 || name != "code" && name != "id_token" && name != "state" && name != "user" {
			return "", oidc.NativeInput{}, authn.ErrInvalidInput
		}
	}
	input := oidc.NativeInput{Provider: "apple", IDToken: form.Get("id_token"), AuthorizationCode: form.Get("code"), State: form.Get("state")}
	if input.IDToken == "" || len(input.IDToken) > 16<<10 || input.AuthorizationCode == "" || len(input.AuthorizationCode) > 4096 || input.State == "" || len(input.State) > oidc.MaxNativeStateBytes || len(form.Get("user")) > 8<<10 {
		return "", oidc.NativeInput{}, authn.ErrInvalidInput
	}
	return profile, input, nil
}
