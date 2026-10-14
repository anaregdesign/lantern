package integration_test

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"google.golang.org/protobuf/proto"
)

// Same isolated signed-provider fixture, production verification/current owner
// and actual public transport as native login. This does not qualify a provider
// account or device, and the configured signup policy exists only in the fixture.
func runNativeRegistrationWireGate(t *testing.T, ctx context.Context, f *nativeLoginWireFixture, servers []*httptest.Server, controls []graphv1connect.LanternSecurityServiceClient, admin string, transport *http.Client) {
	native := graphv1connect.NewLanternNativeAuthServiceClient(transport, servers[0].URL)
	data := graphv1connect.NewLanternServiceClient(transport, servers[0].URL)
	ready := func(t *testing.T) {
		t.Helper()
		nativeWireAwait(t, ctx, func() error {
			_, e := controls[0].GetCurrentPrincipal(ctx, securityWireRequest(admin, &pb.GetCurrentPrincipalRequest{}))
			return e
		})
	}
	begin := func(t *testing.T, issuer, profile string, original *pb.NativeRegistrationReview) *pb.BeginNativeLoginResponse {
		t.Helper()
		ready(t)
		r, e := native.BeginNativeRegistration(ctx, connect.NewRequest(&pb.BeginNativeRegistrationRequest{Issuer: issuer, ClientProfileId: profile, RecoveryReview: original}))
		if e != nil {
			t.Fatal("registration begin", e)
		}
		if r.Msg.Attempt == nil {
			t.Fatal("registration begin omitted provider attempt")
		}
		return r.Msg.Attempt
	}
	googleRequest := func(t *testing.T, start *pb.BeginNativeLoginResponse, subject string, original *pb.NativeRegistrationReview) *connect.Request[pb.PrepareNativeRegistrationRequest] {
		req := connect.NewRequest(&pb.PrepareNativeRegistrationRequest{AttemptId: start.AttemptId, CompletionSecret: start.CompletionSecret, RecoveryReview: original, Provider: &pb.PrepareNativeRegistrationRequest_Google{Google: &pb.GoogleNativeCredential{IdToken: f.token(t, nativeGoogleIssuer, "fixture.apps.googleusercontent.com", subject, "", nil)}}})
		req.Header().Set("Lantern-Native-Affinity", start.Affinity)
		return req
	}
	prepare := func(t *testing.T, subject string, original *pb.NativeRegistrationReview) *pb.PrepareNativeRegistrationResponse {
		t.Helper()
		start := begin(t, nativeGoogleIssuer, "google", original)
		r, e := native.PrepareNativeRegistration(ctx, googleRequest(t, start, subject, original))
		if e != nil {
			t.Fatal("registration prepare", e)
		}
		if len(r.Msg.Proof) != 43 || r.Msg.Review == nil || len(r.Msg.Review.UserId) != 32 || r.Msg.Review.Actor.Subject != subject {
			t.Fatal("registration review binding")
		}
		return r.Msg
	}
	applyRequest := func(p *pb.PrepareNativeRegistrationResponse) *connect.Request[pb.ApplyNativeRegistrationRequest] {
		r := connect.NewRequest(&pb.ApplyNativeRegistrationRequest{Review: p.Review, Proof: p.Proof})
		r.Header().Set("Lantern-Native-Affinity", p.Affinity)
		return r
	}
	apply := func(t *testing.T, p *pb.PrepareNativeRegistrationResponse) *pb.ApplyNativeRegistrationResponse {
		t.Helper()
		r, e := native.ApplyNativeRegistration(ctx, applyRequest(p))
		if e != nil {
			t.Fatal("registration apply", e)
		}
		if r.Msg.GetCurrentResult().GetOriginal().GetDisposition() != pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_APPLIED {
			t.Fatal("registration not applied", r.Msg.GetCurrentResult())
		}
		return r.Msg
	}
	status := func(t *testing.T, p *pb.PrepareNativeRegistrationResponse) *pb.GetNativeRegistrationStatusResponse {
		t.Helper()
		var result *pb.GetNativeRegistrationStatusResponse
		nativeWireAwait(t, ctx, func() error {
			r := connect.NewRequest(&pb.GetNativeRegistrationStatusRequest{Review: p.Review, Proof: p.Proof})
			r.Header().Set("Lantern-Native-Affinity", p.Affinity)
			v, e := native.GetNativeRegistrationStatus(ctx, r)
			if e == nil {
				result = v.Msg
			}
			return e
		})
		return result
	}
	inspect := func(t *testing.T, credential string) *pb.NativeSession {
		t.Helper()
		var result *pb.NativeSession
		nativeWireAwait(t, ctx, func() error {
			v, e := native.GetNativeSession(ctx, securityWireRequest(credential, &pb.GetNativeSessionRequest{}))
			if e == nil {
				result = v.Msg.Session
			}
			return e
		})
		return result
	}
	checkSession := func(t *testing.T, r *pb.ApplyNativeRegistrationResponse, p *pb.PrepareNativeRegistrationResponse) *pb.NativeSession {
		t.Helper()
		if r.Credential == "" || r.Session == nil || r.SignInRequired {
			t.Fatal("registration session unavailable")
		}
		s := inspect(t, r.Credential)
		if s.GetUserId() != p.Review.UserId || s.GetPersonalPrefix() != "users:"+s.GetUserId()+":" || !proto.Equal(s.Identity, p.Review.Actor) || s.ExpiresAt.AsTime().Sub(s.CreatedAt.AsTime()) != 8*time.Hour {
			t.Fatal("installed personal area/session mismatch")
		}
		principal, e := controls[0].GetCurrentPrincipal(ctx, securityWireRequest(r.Credential, &pb.GetCurrentPrincipalRequest{}))
		if e != nil || principal.Msg.UserId == nil || principal.Msg.GetUserId() != s.GetUserId() {
			t.Fatal("self user_id without management grant", e)
		}
		return s
	}
	var alice *pb.ApplyNativeRegistrationResponse
	var alicePrepared *pb.PrepareNativeRegistrationResponse
	t.Run("registration Google personal data and account isolation", func(t *testing.T) {
		ready(t)
		policy, e := controls[0].GetRegistrationPolicy(ctx, securityWireRequest(admin, &pb.GetRegistrationPolicyRequest{}))
		if e != nil || !policy.Msg.GetPolicy().GetEnabled() || len(policy.Msg.Policy.Profiles) != 3 {
			t.Fatal("operator registration policy read", e)
		}
		alicePrepared = prepare(t, "registration-a", nil)
		// Prepare proves identity but grants no ordinary data authority.
		if _, e := data.PutVertex(ctx, securityWireRequest(alicePrepared.Proof, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "users:" + alicePrepared.Review.UserId + ":profile:name", Value: &pb.Vertex_String_{String_: "private"}}})); connect.CodeOf(e) != connect.CodeUnauthenticated {
			t.Fatal("registration proof admitted as data", e)
		}
		alice = apply(t, alicePrepared)
		a := checkSession(t, alice, alicePrepared)
		if a.AuthTime != nil {
			t.Fatal("Google registration fabricated signed auth_time")
		}
		bPrepared := prepare(t, "registration-b", nil)
		bob := apply(t, bPrepared)
		b := checkSession(t, bob, bPrepared)
		if _, e := controls[0].GetRegistrationPolicy(ctx, securityWireRequest(bob.Credential, &pb.GetRegistrationPolicyRequest{})); connect.CodeOf(e) != connect.CodePermissionDenied {
			t.Fatal("personal user read management policy", e)
		}
		if a.GetUserId() == b.GetUserId() {
			t.Fatal("shared UID")
		}
		ready(t)
		login, e := native.BeginNativeLogin(ctx, connect.NewRequest(&pb.BeginNativeLoginRequest{Issuer: nativeGoogleIssuer, ClientProfileId: "google"}))
		if e != nil {
			t.Fatal(e)
		}
		reloginRequest := connect.NewRequest(&pb.CompleteNativeLoginRequest{AttemptId: login.Msg.AttemptId, CompletionSecret: login.Msg.CompletionSecret, Provider: &pb.CompleteNativeLoginRequest_Google{Google: &pb.GoogleNativeCredential{IdToken: f.token(t, nativeGoogleIssuer, "fixture.apps.googleusercontent.com", "registration-a", "", nil)}}})
		reloginRequest.Header().Set("Lantern-Native-Affinity", login.Msg.Affinity)
		relogin, e := native.CompleteNativeLogin(ctx, reloginRequest)
		if e != nil || relogin.Msg.Session.GetUserId() != a.GetUserId() || inspect(t, relogin.Msg.Credential).GetUserId() != a.GetUserId() {
			t.Fatal("ordinary re-login/restore changed installed user_id", e)
		}
		for _, entry := range []struct{ credential, prefix string }{{alice.Credential, a.GetPersonalPrefix()}, {bob.Credential, b.GetPersonalPrefix()}} {
			for _, suffix := range []string{"profile:name", "settings:locale", "arbitrary-key"} {
				key := entry.prefix + suffix
				if _, e := data.PutVertex(ctx, securityWireRequest(entry.credential, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: "private"}}})); e != nil {
					t.Fatal("personal write", e)
				}
				if _, e := data.GetVertex(ctx, securityWireRequest(entry.credential, &pb.GetVertexRequest{Key: key})); e != nil {
					t.Fatal("personal read", e)
				}
			}
		}
		if _, e := data.GetVertex(ctx, securityWireRequest(bob.Credential, &pb.GetVertexRequest{Key: a.GetPersonalPrefix() + "profile:name"})); connect.CodeOf(e) != connect.CodePermissionDenied {
			t.Fatal("cross UID read", e)
		}
		// A mixed batch is refused atomically, not partially installed in A's area.
		if _, e := data.PutVertices(ctx, securityWireRequest(alice.Credential, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: a.GetPersonalPrefix() + "denied-batch", Value: &pb.Vertex_String_{String_: "must not exist"}}, {Key: b.GetPersonalPrefix() + "cross", Value: &pb.Vertex_String_{String_: "forbidden"}}}})); connect.CodeOf(e) != connect.CodePermissionDenied {
			t.Fatal("cross UID batch", e)
		}
		if _, e := data.GetVertex(ctx, securityWireRequest(alice.Credential, &pb.GetVertexRequest{Key: a.GetPersonalPrefix() + "denied-batch"})); connect.CodeOf(e) != connect.CodeNotFound {
			t.Fatal("denied batch partially wrote", e)
		}
		if _, e := data.DeleteVertex(ctx, securityWireRequest(alice.Credential, &pb.DeleteVertexRequest{Key: a.GetPersonalPrefix() + "arbitrary-key"})); e != nil {
			t.Fatal("personal delete", e)
		}
		page, e := data.ScanVertexKeys(ctx, securityWireRequest(alice.Credential, &pb.ScanVertexKeysRequest{Prefix: a.GetPersonalPrefix(), Limit: 1}))
		if e != nil || len(page.Msg.NextCursor) == 0 || len(page.Msg.Keys) != 1 || !strings.HasPrefix(page.Msg.Keys[0], a.GetPersonalPrefix()) {
			t.Fatal("personal query cursor", e)
		}
		if _, e := data.ScanVertexKeys(ctx, securityWireRequest(bob.Credential, &pb.ScanVertexKeysRequest{Prefix: b.GetPersonalPrefix(), Limit: 1, Cursor: page.Msg.NextCursor})); e == nil {
			t.Fatal("another UID resumed personal query cursor")
		}
		again := apply(t, alicePrepared)
		if again.Credential != "" || again.Session != nil || !again.SignInRequired {
			t.Fatal("duplicate Apply issued another raw credential")
		}
	})
	t.Run("registration personal stream closes on current withdrawal", func(t *testing.T) {
		p := prepare(t, "registration-stream", nil)
		r := apply(t, p)
		s := checkSession(t, r, p)
		changes := graphv1connect.NewLanternChangeServiceClient(transport, servers[0].URL)
		streamCtx, stop := context.WithTimeout(ctx, 5*time.Second)
		defer stop()
		stream, e := changes.WatchChanges(streamCtx, securityWireRequest(r.Credential, &pb.WatchChangesRequest{Prefix: s.GetPersonalPrefix(), Bootstrap: true, Projection: pb.ChangeProjection_CHANGE_PROJECTION_VALUE}))
		if e != nil {
			t.Fatal(e)
		}
		defer func() { _ = stream.Close() }()
		if !stream.Receive() || !stream.Msg().Bootstrap || len(stream.Msg().Cursor) == 0 {
			t.Fatal("personal CDC bootstrap", stream.Err())
		}
		cursor := bytes.Clone(stream.Msg().Cursor)
		key := s.GetPersonalPrefix() + "settings:theme"
		if _, e := data.PutVertex(ctx, securityWireRequest(r.Credential, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: "dark"}}})); e != nil {
			t.Fatal(e)
		}
		seen := false
		for stream.Receive() {
			for _, event := range stream.Msg().Invalidations {
				if !strings.HasPrefix(event.GetVertexKey(), s.GetPersonalPrefix()) {
					t.Fatal("personal CDC escaped bound UID")
				}
				seen = seen || event.GetVertexKey() == key && event.GetVertex().GetString_() == "dark"
			}
			if seen {
				break
			}
		}
		if !seen {
			t.Fatal("personal CDC value missing", stream.Err())
		}
		wrong, e := changes.WatchChanges(streamCtx, securityWireRequest(alice.Credential, &pb.WatchChangesRequest{Prefix: alice.Session.GetPersonalPrefix(), Cursor: cursor, Projection: pb.ChangeProjection_CHANGE_PROJECTION_VALUE}))
		if e == nil {
			if wrong.Receive() {
				t.Fatal("another UID resumed personal CDC cursor")
			}
			e = wrong.Err()
			_ = wrong.Close()
		}
		if e == nil {
			t.Fatal("cross UID cursor ended without refusal")
		}
		nativeWireApply(t, ctx, controls[0], admin, &pb.SecurityChange{Operation: &pb.SecurityChange_DeleteAssignment{DeleteAssignment: &pb.SecurityRoleAssignment{Identity: s.Identity, RoleId: "personal_user"}}})
		for stream.Receive() {
			if len(stream.Msg().Invalidations) != 0 {
				t.Fatal("withdrawn personal stream published data")
			}
		}
		if stream.Err() == nil || errors.Is(stream.Err(), context.DeadlineExceeded) || connect.CodeOf(stream.Err()) == connect.CodeDeadlineExceeded {
			t.Fatal("withdrawn personal stream did not close at current fence", stream.Err())
		}
		ready(t)
		self, e := controls[0].GetCurrentPrincipal(ctx, securityWireRequest(r.Credential, &pb.GetCurrentPrincipalRequest{}))
		if e != nil || self.Msg.GetUserId() != s.GetUserId() || len(self.Msg.Roles) != 0 {
			t.Fatal("self ID required a Role or revived withdrawn grants", e)
		}
	})
	t.Run("registration Apply response loss and fresh original recovery", func(t *testing.T) {
		p := prepare(t, "registration-loss", nil)
		original := proto.Clone(p.Review).(*pb.NativeRegistrationReview)
		calls := 0
		loss := &http.Client{Transport: currentWireTransport{closeIdle: func() {}, roundTrip: func(r *http.Request) (*http.Response, error) {
			calls++
			res, e := transport.Transport.RoundTrip(r)
			if e != nil {
				return nil, e
			}
			reader := io.Reader(res.Body)
			if res.Header.Get("Content-Encoding") == "gzip" {
				decoded, err := gzip.NewReader(res.Body)
				if err != nil {
					t.Fatal("loss fixture gzip", err)
				}
				defer func() { _ = decoded.Close() }()
				reader = decoded
			}
			raw, readErr := io.ReadAll(reader)
			_ = res.Body.Close()
			var result pb.ApplyNativeRegistrationResponse
			decodeErr := proto.Unmarshal(raw, &result)
			if readErr != nil || res.StatusCode != http.StatusOK || decodeErr != nil || result.GetCurrentResult().GetOriginal().GetDisposition() != pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_APPLIED {
				t.Errorf("loss did not follow committed registration: status=%d encoding=%q bytes=%d read=%v decode=%v disposition=%v", res.StatusCode, res.Header.Get("Content-Encoding"), len(raw), readErr, decodeErr, result.GetCurrentResult().GetOriginal().GetDisposition())
			}
			return nil, io.ErrUnexpectedEOF
		}}}
		lossy := graphv1connect.NewLanternNativeAuthServiceClient(loss, servers[0].URL)
		if _, e := lossy.ApplyNativeRegistration(ctx, applyRequest(p)); e == nil || calls != 1 {
			t.Fatal("lost Apply retried or claimed success")
		}
		restored := prepare(t, "registration-loss", original)
		got := status(t, restored).GetCurrentResult()
		if !proto.Equal(got.ChangeId, original.ChangeId) || !bytes.Equal(got.IntentDigest, original.IntentDigest) || got.GetOriginal().GetDisposition() != pb.CurrentSecurityDisposition_CURRENT_SECURITY_DISPOSITION_APPLIED {
			t.Fatal("status did not retain original")
		}
		if _, e := native.ApplyNativeRegistration(ctx, applyRequest(restored)); e == nil {
			t.Fatal("recovery acquired Apply")
		}
		wrong := begin(t, nativeGoogleIssuer, "google", original)
		if _, e := native.PrepareNativeRegistration(ctx, googleRequest(t, wrong, "registration-b", original)); e == nil {
			t.Fatal("another account recovered original")
		}
		if calls != 1 {
			t.Fatal("original replayed")
		}
	})
	t.Run("registration Prepare response loss never dispatches", func(t *testing.T) {
		start := begin(t, nativeGoogleIssuer, "google", nil)
		request := googleRequest(t, start, "registration-unapplied", nil)
		var observed pb.PrepareNativeRegistrationResponse
		loss := &http.Client{Transport: currentWireTransport{closeIdle: func() {}, roundTrip: func(r *http.Request) (*http.Response, error) {
			res, e := transport.Transport.RoundTrip(r)
			if e != nil {
				return nil, e
			}
			raw, e := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if e != nil || res.StatusCode != http.StatusOK || proto.Unmarshal(raw, &observed) != nil || observed.Review == nil {
				t.Error("loss must follow prepared response")
			}
			return nil, io.ErrUnexpectedEOF
		}}}
		if _, e := graphv1connect.NewLanternNativeAuthServiceClient(loss, servers[0].URL).PrepareNativeRegistration(ctx, request); e == nil {
			t.Fatal("prepare response loss claimed success")
		}
		if _, e := native.PrepareNativeRegistration(ctx, request); e == nil {
			t.Fatal("consumed completion replayed")
		}
		recovered := prepare(t, "registration-unapplied", observed.Review)
		if status(t, recovered).GetCurrentResult().GetOriginal() != nil {
			t.Fatal("Prepare loss committed registration")
		}
		if _, e := native.ApplyNativeRegistration(ctx, applyRequest(recovered)); e == nil {
			t.Fatal("unknown original became replay authority")
		}
	})
	t.Run("registration Apple single exchange", func(t *testing.T) {
		for _, profile := range []string{"apple_ios", "apple_web"} {
			t.Run(profile, func(t *testing.T) {
				start := begin(t, nativeAppleIssuer, profile, nil)
				apple := f.appleSubject(t, start, "registration-"+profile, false)
				if profile == "apple_web" {
					values := url.Values{"code": {apple.AuthorizationCode}, "id_token": {apple.IdentityToken}, "state": {apple.State}}
					r, e := http.NewRequestWithContext(ctx, http.MethodPost, servers[0].URL+"/auth/native/apple/callback/"+profile, strings.NewReader(values.Encode()))
					if e != nil {
						t.Fatal(e)
					}
					r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
					c := &http.Client{Transport: transport.Transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
					res, e := c.Do(r)
					if e != nil {
						t.Fatal(e)
					}
					_, _ = io.Copy(io.Discard, res.Body)
					_ = res.Body.Close()
					if res.StatusCode != http.StatusSeeOther {
						t.Fatal("registration callback", res.StatusCode)
					}
				}
				before := f.exchanges.Load()
				req := connect.NewRequest(&pb.PrepareNativeRegistrationRequest{AttemptId: start.AttemptId, CompletionSecret: start.CompletionSecret, Provider: &pb.PrepareNativeRegistrationRequest_Apple{Apple: apple}})
				req.Header().Set("Lantern-Native-Affinity", start.Affinity)
				p, e := native.PrepareNativeRegistration(ctx, req)
				if e != nil {
					t.Fatal("Apple registration prepare", e)
				}
				s := checkSession(t, apply(t, p.Msg), p.Msg)
				if s.AuthTime == nil || time.Since(s.AuthTime.AsTime()) < 50*time.Minute || f.exchanges.Load() != before+1 {
					t.Fatal("registration changed signed auth_time or repeated exchange")
				}
				_ = status(t, p.Msg)
				if f.exchanges.Load() != before+1 {
					t.Fatal("status repeated Apple exchange")
				}
			})
		}
	})
	t.Run("registration legacy enrollment and restrictive policy", func(t *testing.T) {
		legacy := prepare(t, "registration-legacy", nil)
		r := apply(t, legacy)
		s := checkSession(t, r, legacy)
		if _, e := data.PutVertex(ctx, securityWireRequest(r.Credential, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "orders:legacy-preserved", Value: &pb.Vertex_String_{String_: "old grant"}}})); e != nil {
			t.Fatal("registration removed old grant", e)
		}
		nativeWireApply(t, ctx, controls[0], admin, &pb.SecurityChange{Operation: &pb.SecurityChange_DeleteAssignment{DeleteAssignment: &pb.SecurityRoleAssignment{Identity: s.Identity, RoleId: "personal_user"}}})
		policy := &pb.SecurityRegistrationPolicy{Version: 1, Enabled: false, RoleId: "personal_user"}
		nativeWireApply(t, ctx, controls[0], admin, &pb.SecurityChange{Operation: &pb.SecurityChange_PutRegistrationPolicy{PutRegistrationPolicy: policy}})
		ready(t)
		closed, e := controls[0].GetRegistrationPolicy(ctx, securityWireRequest(admin, &pb.GetRegistrationPolicyRequest{}))
		if e != nil || !proto.Equal(closed.Msg.Policy, policy) {
			t.Fatal("closed policy read", e)
		}
		again := prepare(t, "registration-legacy", nil)
		if again.Review.UserId != s.GetUserId() {
			t.Fatal("repeat changed UID")
		}
		repeated := apply(t, again)
		_ = checkSession(t, repeated, again)
		if _, e := data.PutVertex(ctx, securityWireRequest(repeated.Credential, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: s.GetPersonalPrefix() + "withdrawn", Value: &pb.Vertex_String_{String_: "forbidden"}}})); connect.CodeOf(e) != connect.CodePermissionDenied {
			t.Fatal("repeat resurrected assignment", e)
		}
		start := begin(t, nativeGoogleIssuer, "google", nil)
		if _, e := native.PrepareNativeRegistration(ctx, googleRequest(t, start, "registration-closed", nil)); e == nil {
			t.Fatal("closed registration admitted new UID")
		}
		// Closing signup leaves existing sessions and personal data access intact.
		nativeWireAwait(t, ctx, func() error {
			_, e := data.GetVertex(ctx, securityWireRequest(alice.Credential, &pb.GetVertexRequest{Key: alice.Session.GetPersonalPrefix() + "profile:name"}))
			return e
		})
		if status(t, alicePrepared).GetCurrentResult().GetOriginal() == nil {
			t.Fatal(errors.New("closure lost original disclosure"))
		}
	})
}
