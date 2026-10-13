package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

func TestNativeRPCRequiresCurrentRuntime(t *testing.T) {
	r := NewAuthenticationAdapter(AuthenticationOptions{Credentials: NewCredentialAdapter("off", BrowserConfig{}, nil), Now: time.Now})
	_, handler := graphv1connect.NewLanternNativeAuthServiceHandler(r, SecurityHandlerOptions()...)
	server := httptest.NewServer(r.NativeRPCHandler(handler))
	defer server.Close()
	client := graphv1connect.NewLanternNativeAuthServiceClient(server.Client(), server.URL)
	_, err := client.BeginNativeLogin(t.Context(), connect.NewRequest(&pb.BeginNativeLoginRequest{Issuer: "https://accounts.google.com", ClientProfileId: "mobile"}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("OFF became native bootstrap", err)
	}
	_, err = client.GetNativeSession(t.Context(), connect.NewRequest(&pb.GetNativeSessionRequest{}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("OFF became native session", err)
	}
}

func TestNativeRPCEnvelopeAndDeadline(t *testing.T) {
	r := &AuthenticationAdapter{}
	called := false
	handler := r.NativeRPCHandler(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		called = true
		deadline, ok := req.Context().Deadline()
		if !ok || time.Until(deadline) > 30*time.Second {
			t.Error("unbounded completion")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	request := func() *http.Request {
		return httptest.NewRequest(http.MethodPost, "https://lantern.example"+graphv1connect.LanternNativeAuthServiceCompleteNativeLoginProcedure, strings.NewReader("{}"))
	}
	for _, edit := range []func(*http.Request){func(r *http.Request) { r.Method = http.MethodGet }, func(r *http.Request) { r.URL.RawQuery = "token=secret" }, func(r *http.Request) { r.ContentLength = NativeAuthMaxBytes + 1 }, func(r *http.Request) { r.Header.Set("Content-Encoding", "gzip") }} {
		req := request()
		edit(req)
		called = false
		handler.ServeHTTP(httptest.NewRecorder(), req)
		if called {
			t.Fatal("invalid envelope reached handler")
		}
	}
	called = false
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, request())
	if !called || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("native response cache/deadline contract")
	}
	if nativeBootstrapProcedure("/graph.v1.LanternNativeAuthService/Unknown") || nativeBootstrapProcedure(graphv1connect.LanternNativeAuthServiceGetNativeSessionProcedure) {
		t.Fatal("broad bootstrap exception")
	}
	short, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	handler.ServeHTTP(httptest.NewRecorder(), request().WithContext(short))
}
