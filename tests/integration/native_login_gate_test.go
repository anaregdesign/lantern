package integration_test

// Crosses production provider verification, authn orchestration, durable
// current authority, output framing and public Connect clients. Fixtures are
// opt-in and isolated; all data/authority owners are the ordinary composition.
import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"golang.org/x/net/http2"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func runNativeLoginWireGate(t *testing.T, ctx context.Context, f *nativeLoginWireFixture, servers []*httptest.Server, controls []graphv1connect.LanternSecurityServiceClient, admin string, transport *http.Client, exportBoundary *atomic.Pointer[currentExportBoundary]) {
	t.Helper()
	native := graphv1connect.NewLanternNativeAuthServiceClient(transport, servers[0].URL)
	data := graphv1connect.NewLanternServiceClient(transport, servers[0].URL)
	t.Run("ambiguous native wire rejected before provider work", func(t *testing.T) {
		field := func(n protowire.Number, value string) []byte {
			return protowire.AppendString(protowire.AppendTag(nil, n, protowire.BytesType), value)
		}
		valid, _ := proto.Marshal(&pb.BeginNativeLoginRequest{Issuer: nativeGoogleIssuer, ClientProfileId: "google"})
		for _, extra := range [][]byte{field(1, nativeGoogleIssuer), field(100, "unknown")} {
			before := f.fetches.Load()
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, servers[0].URL+graphv1connect.LanternNativeAuthServiceBeginNativeLoginProcedure, bytes.NewReader(append(bytes.Clone(valid), extra...)))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Content-Type", "application/proto")
			response, err := transport.Do(request)
			if err != nil {
				// A decoder rejection precedes login output binding. The existing
				// current-v2 final-Write fence then closes the HTTP/2 stream rather
				// than emitting an unbound response. Do not admit arbitrary errors.
				var streamError http2.StreamError
				if !errors.As(err, &streamError) || streamError.Code != http2.ErrCodeInternal {
					t.Fatal("unexpected native wire failure", err)
				}
			} else {
				_, _ = io.Copy(io.Discard, response.Body)
				_ = response.Body.Close()
				if response.StatusCode != http.StatusBadRequest {
					t.Fatal("ambiguous native input accepted", response.StatusCode)
				}
			}
			if f.fetches.Load() != before {
				t.Fatal("ambiguous native input reached provider work")
			}
		}
	})
	begin := func(issuer, profile string) *pb.BeginNativeLoginResponse {
		t.Helper()
		// This read waits for the same owner's ordinary renewal after prior Apply.
		nativeWireAwait(t, ctx, func() error {
			_, e := controls[0].GetCurrentPrincipal(ctx, securityWireRequest(admin, &pb.GetCurrentPrincipalRequest{}))
			return e
		})
		got, err := native.BeginNativeLogin(ctx, connect.NewRequest(&pb.BeginNativeLoginRequest{Issuer: issuer, ClientProfileId: profile}))
		if err != nil {
			t.Fatal("native begin", err)
		}
		if got.Msg.Configuration == nil || got.Msg.Configuration.ServiceOrigin != "https://native.example" || len(got.Msg.CompletionSecret) != 43 {
			t.Fatal("incomplete native start")
		}
		return got.Msg
	}
	complete := func(start *pb.BeginNativeLoginResponse, request *pb.CompleteNativeLoginRequest) *pb.CompleteNativeLoginResponse {
		t.Helper()
		request.AttemptId, request.CompletionSecret = start.AttemptId, start.CompletionSecret
		req := connect.NewRequest(request)
		req.Header().Set("Lantern-Native-Affinity", start.Affinity)
		got, err := native.CompleteNativeLogin(ctx, req)
		if err != nil {
			t.Fatal("native complete", err)
		}
		if !strings.HasPrefix(got.Msg.Credential, "lnu1_") || got.Msg.Session == nil || got.Msg.Session.CredentialProfile != "native_end_user" || got.Msg.Session.Identity.Subject != "native-user" || got.Msg.Session.Version.CurrentProfile == nil {
			t.Fatal("native issued metadata")
		}
		s := got.Msg.Session
		if s.UserId != nil || s.PersonalPrefix != nil {
			t.Fatal("ordinary login implicitly allocated a legacy user ID")
		}
		if duration := s.ExpiresAt.AsTime().Sub(s.CreatedAt.AsTime()); duration != 8*time.Hour {
			t.Fatal("native absolute lifetime", duration)
		}
		return got.Msg
	}
	inspect := func(credential string) *pb.GetNativeSessionResponse {
		t.Helper()
		var view *pb.GetNativeSessionResponse
		nativeWireAwait(t, ctx, func() error {
			v, e := native.GetNativeSession(ctx, securityWireRequest(credential, &pb.GetNativeSessionRequest{}))
			if e == nil {
				view = v.Msg
			}
			return e
		})
		return view
	}
	googleLogin := func() *pb.CompleteNativeLoginResponse {
		t.Helper()
		return complete(begin(nativeGoogleIssuer, "google"), &pb.CompleteNativeLoginRequest{Provider: &pb.CompleteNativeLoginRequest_Google{Google: &pb.GoogleNativeCredential{IdToken: f.token(t, nativeGoogleIssuer, "fixture.apps.googleusercontent.com", "native-user", "", nil)}}})
	}
	apply := func(change *pb.SecurityChange) {
		nativeWireApply(t, ctx, controls[0], admin, change)
	}

	t.Run("google repeated restored and native RPC", func(t *testing.T) {
		token := f.token(t, nativeGoogleIssuer, "fixture.apps.googleusercontent.com", "native-user", "", nil)
		for range 3 {
			start := begin(nativeGoogleIssuer, "google")
			request := &pb.CompleteNativeLoginRequest{Provider: &pb.CompleteNativeLoginRequest_Google{Google: &pb.GoogleNativeCredential{IdToken: token}}}
			got := complete(start, request)
			if got.Session.AuthTime != nil {
				t.Fatal("unknown auth_time fabricated")
			}
			if _, err := native.CompleteNativeLogin(ctx, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeUnauthenticated {
				t.Fatal("completed transaction replay", err)
			}
			view := inspect(got.Credential)
			if len(view.Roles) != 1 || view.Roles[0].Id != "wire_native" {
				t.Fatal("native login changed grants")
			}
			if _, err := data.PutVertex(ctx, securityWireRequest(got.Credential, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "orders:native", Value: &pb.Vertex_String_{String_: "native visible"}}})); err != nil {
				t.Fatal("native data RPC", err)
			}
			if _, err := data.GetVertex(ctx, securityWireRequest(got.Credential, &pb.GetVertexRequest{Key: "other:denied"})); connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatal("unauthorized native prefix", err)
			}
			before := f.fetches.Load()
			for range 8 {
				if _, err := data.GetVertex(ctx, securityWireRequest(got.Credential, &pb.GetVertexRequest{Key: "orders:native"})); err != nil {
					t.Fatal(err)
				}
			}
			if f.fetches.Load() != before {
				t.Fatal("native hot path fetched provider")
			}
		}
	})
	t.Run("provider profile and existing Principal", func(t *testing.T) {
		for _, tc := range []struct {
			name, subject string
			mutate        func(map[string]any)
		}{{"unknown", "unknown", nil}, {"wrong audience", "native-user", func(c map[string]any) { c["aud"] = "other" }}, {"bare issuer", "native-user", func(c map[string]any) { c["iss"] = "accounts.google.com" }}, {"expired", "native-user", func(c map[string]any) { c["exp"] = time.Now().Add(-time.Minute).Unix() }}} {
			t.Run(tc.name, func(t *testing.T) {
				start := begin(nativeGoogleIssuer, "google")
				token := f.token(t, nativeGoogleIssuer, "fixture.apps.googleusercontent.com", tc.subject, "", tc.mutate)
				_, err := native.CompleteNativeLogin(ctx, connect.NewRequest(&pb.CompleteNativeLoginRequest{AttemptId: start.AttemptId, CompletionSecret: start.CompletionSecret, Provider: &pb.CompleteNativeLoginRequest_Google{Google: &pb.GoogleNativeCredential{IdToken: token}}}))
				if connect.CodeOf(err) != connect.CodeUnauthenticated {
					t.Fatal("invalid provider accepted", err)
				}
			})
		}
		if _, err := data.GetVertex(ctx, securityWireRequest(f.token(t, nativeGoogleIssuer, "fixture.apps.googleusercontent.com", "native-user", "", nil), &pb.GetVertexRequest{Key: "orders:native"})); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatal("provider token became API token", err)
		}
	})
	t.Run("Apple native and Android callback", func(t *testing.T) {
		for _, profile := range []string{"apple_ios", "apple_web"} {
			t.Run(profile, func(t *testing.T) {
				start := begin(nativeAppleIssuer, profile)
				apple := f.apple(t, start, false)
				if profile == "apple_web" {
					values := url.Values{"code": {apple.AuthorizationCode}, "id_token": {apple.IdentityToken}, "state": {apple.State}}
					callback := func() *http.Response {
						req, e := http.NewRequestWithContext(ctx, http.MethodPost, servers[0].URL+"/auth/native/apple/callback/"+profile, strings.NewReader(values.Encode()))
						if e != nil {
							t.Fatal(e)
						}
						req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
						c := &http.Client{Transport: transport.Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
						res, e := c.Do(req)
						if e != nil {
							t.Fatal(e)
						}
						_, _ = io.Copy(io.Discard, res.Body)
						_ = res.Body.Close()
						return res
					}
					response := callback()
					if response.StatusCode != http.StatusSeeOther || !strings.Contains(response.Header.Get("Location"), "scheme=signinwithapple") || strings.Contains(response.Header.Get("Location"), start.CompletionSecret) {
						t.Fatal("Android callback contract", response.StatusCode)
					}
					if callback().StatusCode == http.StatusSeeOther {
						t.Fatal("callback replay")
					}
				}
				before := f.exchanges.Load()
				request := &pb.CompleteNativeLoginRequest{Provider: &pb.CompleteNativeLoginRequest_Apple{Apple: apple}}
				got := complete(start, request)
				if f.exchanges.Load() != before+1 || got.Session.AuthTime == nil || time.Since(got.Session.AuthTime.AsTime()) < 50*time.Minute {
					t.Fatal("Apple exchange/freshness semantics")
				}
				_ = inspect(got.Credential)
				if _, err := native.CompleteNativeLogin(ctx, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeUnauthenticated || f.exchanges.Load() != before+1 {
					t.Fatal("Apple code replay", err)
				}
			})
		}
	})
	t.Run("Apple response loss never retries code", func(t *testing.T) {
		start := begin(nativeAppleIssuer, "apple_ios")
		request := &pb.CompleteNativeLoginRequest{AttemptId: start.AttemptId, CompletionSecret: start.CompletionSecret, Provider: &pb.CompleteNativeLoginRequest_Apple{Apple: f.apple(t, start, true)}}
		before := f.exchanges.Load()
		if _, err := native.CompleteNativeLogin(ctx, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatal("exchange response loss", err)
		}
		if _, err := native.CompleteNativeLogin(ctx, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeUnauthenticated || f.exchanges.Load() != before+1 {
			t.Fatal("uncertain exchange retried", err)
		}
	})
	t.Run("issued response loss leaves no replay or credential recovery", func(t *testing.T) {
		start := begin(nativeGoogleIssuer, "google")
		request := &pb.CompleteNativeLoginRequest{AttemptId: start.AttemptId, CompletionSecret: start.CompletionSecret, Provider: &pb.CompleteNativeLoginRequest_Google{Google: &pb.GoogleNativeCredential{IdToken: f.token(t, nativeGoogleIssuer, "fixture.apps.googleusercontent.com", "native-user", "", nil)}}}
		var issued pb.CompleteNativeLoginResponse
		calls := 0
		loss := &http.Client{Transport: currentWireTransport{closeIdle: func() {}, roundTrip: func(r *http.Request) (*http.Response, error) {
			calls++
			response, err := transport.Transport.RoundTrip(r)
			if err != nil {
				return nil, err
			}
			body, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			if err != nil || response.StatusCode != http.StatusOK || proto.Unmarshal(body, &issued) != nil || issued.Credential == "" {
				t.Error("response loss fixture did not observe durable issuance")
			}
			return nil, io.ErrUnexpectedEOF
		}}}
		lossy := graphv1connect.NewLanternNativeAuthServiceClient(loss, servers[0].URL)
		if _, err := lossy.CompleteNativeLogin(ctx, connect.NewRequest(request)); err == nil || calls != 1 {
			t.Fatal("lost issuance response retried or claimed success", calls)
		}
		// Only the loss fixture saw the response. The client has no recovery API.
		_ = inspect(issued.Credential)
		if _, err := native.CompleteNativeLogin(ctx, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatal("lost issuance resurrected transaction", err)
		}
	})
	t.Run("concurrent Apple completion issues once", func(t *testing.T) {
		start := begin(nativeAppleIssuer, "apple_ios")
		apple := f.apple(t, start, false)
		before := f.exchanges.Load()
		type completionResult struct {
			response *connect.Response[pb.CompleteNativeLoginResponse]
			err      error
		}
		results := make(chan completionResult, 2)
		release := make(chan struct{})
		for range 2 {
			go func() {
				<-release
				req := connect.NewRequest(&pb.CompleteNativeLoginRequest{AttemptId: start.AttemptId, CompletionSecret: start.CompletionSecret, Provider: &pb.CompleteNativeLoginRequest_Apple{Apple: proto.Clone(apple).(*pb.AppleNativeCredential)}})
				req.Header().Set("Lantern-Native-Affinity", start.Affinity)
				r, e := native.CompleteNativeLogin(ctx, req)
				results <- completionResult{r, e}
			}()
		}
		close(release)
		successes := 0
		for range 2 {
			result := <-results
			if result.err == nil {
				successes++
				if result.response.Msg.Credential == "" {
					t.Fatal("success without credential")
				}
			} else if connect.CodeOf(result.err) != connect.CodeUnauthenticated {
				t.Fatal("unexpected concurrent failure", result.err)
			}
		}
		if successes != 1 || f.exchanges.Load() != before+1 {
			t.Fatal("completion or provider exchange repeated", successes)
		}
	})
	t.Run("native logout retained status", func(t *testing.T) {
		start := begin(nativeGoogleIssuer, "google")
		got := complete(start, &pb.CompleteNativeLoginRequest{Provider: &pb.CompleteNativeLoginRequest_Google{Google: &pb.GoogleNativeCredential{IdToken: f.token(t, nativeGoogleIssuer, "fixture.apps.googleusercontent.com", "native-user", "", nil)}}})
		_ = inspect(got.Credential)
		prepared, err := native.PrepareNativeLogout(ctx, securityWireRequest(got.Credential, &pb.PrepareNativeLogoutRequest{}))
		if err != nil {
			t.Fatal(err)
		}
		review := proto.Clone(prepared.Msg.Review).(*pb.CurrentSessionRevocationReview)
		if review == nil || review.ChangeId == nil || len(review.IntentDigest) != 32 {
			t.Fatal("logout recovery identity absent")
		}
		calls := 0
		loss := &http.Client{Transport: currentWireTransport{closeIdle: func() {}, roundTrip: func(r *http.Request) (*http.Response, error) {
			calls++
			response, err := transport.Transport.RoundTrip(r)
			if err != nil {
				return nil, err
			}
			raw, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			var applied pb.ApplyNativeLogoutResponse
			if err != nil || response.StatusCode != http.StatusOK || proto.Unmarshal(raw, &applied) != nil || !applied.ClearLocalSession {
				t.Error("loss must occur after actual logout output")
			}
			return nil, io.ErrUnexpectedEOF
		}}}
		lossy := graphv1connect.NewLanternNativeAuthServiceClient(loss, servers[0].URL)
		if _, err := lossy.ApplyNativeLogout(ctx, securityWireRequest(got.Credential, &pb.ApplyNativeLogoutRequest{Review: review})); err == nil || calls != 1 {
			t.Fatal("lost logout response retried or claimed success", calls)
		}
		nativeWireAwait(t, ctx, func() error {
			_, err := native.GetNativeSession(ctx, securityWireRequest(got.Credential, &pb.GetNativeSessionRequest{}))
			if connect.CodeOf(err) == connect.CodeUnauthenticated {
				return nil
			}
			return errors.New("revoked native credential still usable")
		})
		replacement := googleLogin()
		_ = inspect(replacement.Credential)
		nativeWireAwait(t, ctx, func() error {
			status, err := controls[0].GetSecurityChangeStatus(ctx, securityWireRequest(replacement.Credential, &pb.GetSecurityChangeStatusRequest{CurrentProfile: review.Profile, CurrentChangeId: review.ChangeId, CurrentIntentDigest: review.IntentDigest}))
			if err != nil {
				return err
			}
			if !proto.Equal(status.Msg.GetCurrentResult().GetChangeId(), review.ChangeId) || !bytes.Equal(status.Msg.GetCurrentResult().GetIntentDigest(), review.IntentDigest) {
				return errors.New("status identity changed")
			}
			if status.Msg.GetCurrentResult().GetOriginal().GetDisposition() != pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_APPLIED {
				return errors.New("logout outcome unavailable")
			}
			return nil
		})
		if calls != 1 {
			t.Fatal("status recovery replayed logout")
		}
	})
	t.Run("current Deny suspension and active stream withdrawal", func(t *testing.T) {
		got := googleLogin()
		initial := inspect(got.Credential)
		role := proto.Clone(initial.Roles[0]).(*pb.SecurityRole)
		denied := proto.Clone(role).(*pb.SecurityRole)
		denied.Rules = append(denied.Rules, &pb.SecurityRule{Id: "native-deny", Effect: pb.SecurityEffect_SECURITY_EFFECT_DENY, Action: pb.SecurityAction_SECURITY_ACTION_VERTEX_READ, Resource: &pb.SecurityRule_Prefix{Prefix: "orders:"}})
		apply(&pb.SecurityChange{Operation: &pb.SecurityChange_PutRole{PutRole: denied}})
		fresh := inspect(got.Credential)
		if proto.Equal(initial.Session.Version.CurrentCut, fresh.Session.Version.CurrentCut) {
			t.Fatal("native inspection cached prior policy")
		}
		if _, err := data.GetVertex(ctx, securityWireRequest(got.Credential, &pb.GetVertexRequest{Key: "orders:native"})); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal("current native Deny ignored", err)
		}
		apply(&pb.SecurityChange{Operation: &pb.SecurityChange_PutRole{PutRole: role}})
		_ = inspect(got.Credential)
		identity := got.Session.Identity
		apply(&pb.SecurityChange{Operation: &pb.SecurityChange_PutUser{PutUser: &pb.SecurityUserStateChange{Identity: identity, State: pb.SecurityPrincipalState_SECURITY_PRINCIPAL_STATE_SUSPENDED}}})
		nativeWireAwait(t, ctx, func() error {
			_, err := native.GetNativeSession(ctx, securityWireRequest(got.Credential, &pb.GetNativeSessionRequest{}))
			if connect.CodeOf(err) == connect.CodeUnauthenticated {
				return nil
			}
			return errors.New("suspended native session remained usable")
		})
		apply(&pb.SecurityChange{Operation: &pb.SecurityChange_PutUser{PutUser: &pb.SecurityUserStateChange{Identity: identity, State: pb.SecurityPrincipalState_SECURITY_PRINCIPAL_STATE_ACTIVE}}})
		got = googleLogin()
		_ = inspect(got.Credential)
		change := &pb.SecurityChange{Operation: &pb.SecurityChange_DeleteAssignment{DeleteAssignment: &pb.SecurityRoleAssignment{Identity: identity, RoleId: "wire_native"}}}
		currentWireRevokeActiveExport(t, ctx, transport, servers[0].URL, controls[0], admin, got.Credential, change, exportBoundary)
		_ = inspect(got.Credential)
		if _, err := data.GetVertex(ctx, securityWireRequest(got.Credential, &pb.GetVertexRequest{Key: "orders:native"})); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal("withdrawn native assignment remained usable", err)
		}
	})
}
