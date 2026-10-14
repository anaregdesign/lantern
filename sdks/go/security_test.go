package client

import (
	"bytes"
	"connectrpc.com/connect"
	"context"
	"errors"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"google.golang.org/protobuf/proto"
	"net/http/httptest"
	"testing"
)

func currentVersionFixture() *pb.SecurityVersion {
	b := func(v byte, n int) []byte { return bytes.Repeat([]byte{v}, n) }
	return &pb.SecurityVersion{CurrentProfile: &pb.CurrentAuthorityProfile{Version: 2, Domain: b(1, 32), Cohort: b(2, 32), Generation: b(3, 16), Protocol: b(4, 32), TimeProfile: b(5, 32), Membership: b(6, 32), Configuration: b(7, 32)}, CurrentCut: &pb.CurrentSemanticCut{Version: 1, Domain: b(1, 32), Cohort: b(2, 32), Generation: b(3, 16), Sequence: 3, Previous: b(8, 32), Projection: b(9, 32), Frontier: b(10, 32), Fences: b(11, 32), Policy: b(12, 32)}, AdmissionBinding: b(13, 32)}
}
func TestCurrentSecurityVersionBinding(t *testing.T) {
	v := currentVersionFixture()
	before, err := CurrentSecurityVersionBinding(v)
	if err != nil {
		t.Fatal(err)
	}
	for name, edit := range map[string]func(*pb.SecurityVersion){"profile": func(v *pb.SecurityVersion) { v.CurrentProfile.TimeProfile[0]++ }, "cut": func(v *pb.SecurityVersion) { v.CurrentCut.Frontier[0]++ }, "credential": func(v *pb.SecurityVersion) { v.AdmissionBinding[0]++ }} {
		t.Run(name, func(t *testing.T) {
			copy := proto.Clone(v).(*pb.SecurityVersion)
			edit(copy)
			after, err := CurrentSecurityVersionBinding(copy)
			if err != nil || before == after {
				t.Fatal("binding did not change", err)
			}
		})
	}
	for _, edit := range []func(*pb.SecurityVersion){func(v *pb.SecurityVersion) { v.Revision = 1 }, func(v *pb.SecurityVersion) { v.CurrentProfile.Version = 3 }, func(v *pb.SecurityVersion) { v.CurrentCut.Generation[0]++ }, func(v *pb.SecurityVersion) { v.CurrentCut.Fences = nil }, func(v *pb.SecurityVersion) { v.AdmissionBinding = make([]byte, 32) }} {
		copy := proto.Clone(v).(*pb.SecurityVersion)
		edit(copy)
		if _, err := CurrentSecurityVersionBinding(copy); err == nil {
			t.Fatal("invalid version accepted")
		}
	}
}

type currentReadFixture struct {
	graphv1connect.UnimplementedLanternSecurityServiceHandler
	version *pb.SecurityVersion
	calls   int
	t       *testing.T
	id      *string
	kind    pb.SecurityPrincipalKind
	err     error
}

func (f *currentReadFixture) GetCurrentPrincipal(_ context.Context, r *connect.Request[pb.GetCurrentPrincipalRequest]) (*connect.Response[pb.GetCurrentPrincipalResponse], error) {
	f.calls++
	if r.Header().Get("Authorization") != "Bearer current-fixture" {
		f.t.Fatal("credential missing")
	}
	if f.err != nil {
		return nil, f.err
	}
	return connect.NewResponse(&pb.GetCurrentPrincipalResponse{Version: f.version, UserId: f.id, Identity: &pb.SecurityIdentity{Kind: f.kind}}), nil
}
func TestGetCurrentPrincipalWire(t *testing.T) {
	f := &currentReadFixture{version: currentVersionFixture(), t: t}
	_, h := graphv1connect.NewLanternSecurityServiceHandler(f)
	s := httptest.NewServer(h)
	defer s.Close()
	c, err := NewLantern(s.URL, WithHTTPClient(s.Client()), WithAuthToken("current-fixture"))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err = c.GetCurrentPrincipal(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.version = &pb.SecurityVersion{Revision: 1}
	if _, err = c.GetCurrentPrincipal(context.Background()); err == nil {
		t.Fatal("legacy scalar accepted")
	}
	if f.calls != 2 {
		t.Fatal("unexpected retry", f.calls)
	}
}

func TestGetCurrentPrincipalDoesNotRetryUnavailable(t *testing.T) {
	calls := 0
	interceptor := connect.UnaryInterceptorFunc(func(connect.UnaryFunc) connect.UnaryFunc {
		return func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) {
			calls++
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("current binding response unavailable"))
		}
	})
	c := mustLantern(t, WithRetry(RetryPolicy{MaxAttempts: 3, sleepFn: noSleep}), WithConnectClientOption(connect.WithInterceptors(interceptor)))
	if _, err := c.GetCurrentPrincipal(context.Background()); !errors.Is(err, ErrUnavailable) || calls != 1 {
		t.Fatal("current binding read retried despite its single-attempt contract", calls, err)
	}
}

func TestGetCurrentUserIDWire(t *testing.T) {
	f := &currentReadFixture{version: currentVersionFixture(), t: t, kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC}
	_, h := graphv1connect.NewLanternSecurityServiceHandler(f)
	s := httptest.NewServer(h)
	defer s.Close()
	c, err := NewLantern(s.URL, WithHTTPClient(s.Client()), WithAuthToken("current-fixture"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	for _, id := range []*string{new("11111111111111111111111111111111"), nil, new("22222222222222222222222222222222")} {
		f.id = id
		got, err := c.GetCurrentUserID(t.Context())
		if err != nil || id == nil && got != nil || id != nil && (got == nil || *got != *id) {
			t.Fatal("current ID/presence/account", got, err)
		}
	}
	for _, id := range []string{"", "short", "00000000000000000000000000000000", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		f.id = &id
		if _, err := c.GetCurrentUserID(t.Context()); !errors.Is(err, ErrFailedPrecondition) {
			t.Fatal("malformed ID", err)
		}
	}
	f.kind = pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_MACHINE
	f.id = nil
	if id, err := c.GetCurrentUserID(t.Context()); err != nil || id != nil {
		t.Fatal("machine missing ID", err)
	}
	f.id = new("11111111111111111111111111111111")
	if _, err := c.GetCurrentUserID(t.Context()); !errors.Is(err, ErrFailedPrecondition) {
		t.Fatal("machine acquired user ID", err)
	}
	f.err = connect.NewError(connect.CodeUnauthenticated, errors.New("signed out"))
	if id, err := c.GetCurrentUserID(t.Context()); !errors.Is(err, ErrUnauthenticated) || id != nil {
		t.Fatal("signed out returned cached ID", err)
	}
	if f.calls != 10 {
		t.Fatal("unexpected retry", f.calls)
	}
}

func TestGetCurrentUserIDFailoverDoesNotRotateOrRetry(t *testing.T) {
	calls := 0
	f := &Failover{nodes: []failoverNode{&fakeNode{getCurrentUserIDFn: func(context.Context) (*string, error) {
		calls++
		return nil, ErrUnavailable
	}}, &fakeNode{getCurrentUserIDFn: func(context.Context) (*string, error) {
		t.Fatal("identity inspection rotated")
		return nil, nil
	}}}, retry: &RetryPolicy{MaxAttempts: 3}}
	if id, err := f.GetCurrentUserID(t.Context()); id != nil || !errors.Is(err, ErrUnavailable) || calls != 1 {
		t.Fatal("identity inspection retried or retained an ID", id, err, calls)
	}
}
