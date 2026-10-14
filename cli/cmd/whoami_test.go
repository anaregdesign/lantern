package cmd

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/spf13/cobra"
)

type whoamiWire struct {
	graphv1connect.UnimplementedLanternSecurityServiceHandler
	id  *string
	err error
}

func (f *whoamiWire) GetCurrentPrincipal(context.Context, *connect.Request[pb.GetCurrentPrincipalRequest]) (*connect.Response[pb.GetCurrentPrincipalResponse], error) {
	if f.err != nil {
		return nil, f.err
	}
	b := func() []byte { return bytes.Repeat([]byte{1}, 32) }
	g := func() []byte { return bytes.Repeat([]byte{1}, 16) }
	return connect.NewResponse(&pb.GetCurrentPrincipalResponse{UserId: f.id, Identity: &pb.SecurityIdentity{Kind: pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC}, Version: &pb.SecurityVersion{
		CurrentProfile: &pb.CurrentAuthorityProfile{Version: 2, Domain: b(), Cohort: b(), Generation: g(), Protocol: b(), TimeProfile: b(), Membership: b(), Configuration: b()},
		CurrentCut:     &pb.CurrentSemanticCut{Version: 1, Domain: b(), Cohort: b(), Generation: g(), Sequence: 1, Previous: b(), Projection: b(), Frontier: b(), Fences: b(), Policy: b()}, AdmissionBinding: b(),
	}}), nil
}

func TestWhoamiWirePresenceAndAuthentication(t *testing.T) {
	oldAddress, oldTLS, oldToken, oldCompression := flagAddress, flagTLS, flagToken, flagCompression
	t.Cleanup(func() {
		flagAddress, flagTLS, flagToken, flagCompression = oldAddress, oldTLS, oldToken, oldCompression
	})
	f := &whoamiWire{}
	_, h := graphv1connect.NewLanternSecurityServiceHandler(f)
	s := httptest.NewUnstartedServer(h)
	p := new(http.Protocols)
	p.SetUnencryptedHTTP2(true)
	s.Config.Protocols = p
	s.Start()
	defer s.Close()
	flagAddress, flagTLS, flagToken, flagCompression = strings.TrimPrefix(s.URL, "http://"), false, "current-fixture", "none"
	for _, id := range []*string{new("11111111111111111111111111111111"), nil} {
		f.id = id
		var out bytes.Buffer
		command := &cobra.Command{Use: "whoami", RunE: whoamiCmd.RunE}
		command.SetOut(&out)
		command.SetArgs(nil)
		if err := command.ExecuteContext(t.Context()); err != nil {
			t.Fatal(err)
		}
		want := "{\"user_id\":null}\n"
		if id != nil {
			want = "{\"user_id\":\"" + *id + "\"}\n"
		}
		if out.String() != want {
			t.Fatal("ambiguous user ID", out.String())
		}
	}
	f.err = connect.NewError(connect.CodeUnauthenticated, errors.New("signed out"))
	var out bytes.Buffer
	command := &cobra.Command{Use: "whoami", RunE: whoamiCmd.RunE, SilenceUsage: true, SilenceErrors: true}
	command.SetOut(&out)
	command.SetArgs(nil)
	if err := command.ExecuteContext(t.Context()); exitCode(err) != 2 || out.Len() != 0 {
		t.Fatal("authentication error became identity", err, out.String())
	}
}
