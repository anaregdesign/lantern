package client

import (
	"bytes"
	"connectrpc.com/connect"
	"context"
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
}

func (f *currentReadFixture) GetCurrentPrincipal(_ context.Context, r *connect.Request[pb.GetCurrentPrincipalRequest]) (*connect.Response[pb.GetCurrentPrincipalResponse], error) {
	f.calls++
	if r.Header().Get("Authorization") != "Bearer current-fixture" {
		f.t.Fatal("credential missing")
	}
	return connect.NewResponse(&pb.GetCurrentPrincipalResponse{Version: f.version}), nil
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
