package main

import (
	"context"
	"strings"
	"testing"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/service"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestCurrentQueryReadinessRejectsFallbackAndWrongOriginalProfile(t *testing.T) {
	p := security.CurrentProfile{Version: 2, Domain: [32]byte{1}, Cohort: [32]byte{2}, Generation: [16]byte{3}, Protocol: [32]byte{4}, Time: [32]byte{5}, Membership: [32]byte{6}, Configuration: [32]byte{7}}
	profile := service.EncodeCurrentProfile(p)
	q := &queryFixture{Mode: "oidc", SecurityProfile: "current-v2", CurrentProfile: profile, CurrentBinding: p.Binding()}
	raw, err := protojson.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	if !queryCapabilitiesReady(q, 0, "AUTH_MODE_OIDC", 2, 1, raw) {
		t.Fatal("current full profile rejected")
	}
	for _, tc := range []struct {
		mode            string
		version, member uint32
		raw             []byte
	}{
		{"AUTH_MODE_OFF", 1, 0, nil}, {"AUTH_MODE_OIDC", 1, 1, raw}, {"AUTH_MODE_OIDC", 2, 0, raw}, {"AUTH_MODE_OIDC", 2, 2, raw}, {"AUTH_MODE_OIDC", 2, 1, nil}, {"AUTH_MODE_OIDC", 2, 1, []byte(`{}`)},
	} {
		if queryCapabilitiesReady(q, 0, tc.mode, tc.version, tc.member, tc.raw) {
			t.Fatal("current fallback accepted", tc.mode, tc.version, tc.member)
		}
	}
	wrong := proto.Clone(profile).(*pb.CurrentAuthorityProfile)
	wrong.TimeProfile[0] ^= 1
	changed, _ := protojson.Marshal(wrong)
	if queryCapabilitiesReady(q, 0, "AUTH_MODE_OIDC", 2, 1, changed) {
		t.Fatal("another native profile accepted")
	}
	q.CurrentBinding = "current-v2:" + strings.Repeat("0", 64)
	if queryCapabilitiesReady(q, 0, "AUTH_MODE_OIDC", 2, 1, raw) {
		t.Fatal("wrong original binding accepted")
	}
	q.Mode, q.CurrentProfile, q.CurrentBinding = "off", nil, ""
	if !queryCapabilitiesReady(q, 0, "AUTH_MODE_OFF", 1, 0, nil) || queryCapabilitiesReady(q, 0, "AUTH_MODE_OIDC", 2, 1, raw) {
		t.Fatal("OFF selected current preparation claimed active current authority")
	}
	q.SecurityProfile = ""
	if queryCapabilitiesReady(q, 0, "AUTH_MODE_OFF", 1, 0, nil) {
		t.Fatal("missing explicit profile accepted")
	}
}

func TestCurrentQueryGenerationRefusesOtherProfilesAndUnpinnedBinaries(t *testing.T) {
	for _, tc := range []struct {
		ports                  []int
		mode, binary, exporter string
	}{
		{[]int{1}, "oidc", "/server", "/exporter"}, {[]int{1, 2, 3}, "unknown", "/server", "/exporter"}, {[]int{1, 2, 3}, "off", "relative", "/exporter"}, {[]int{1, 2, 3}, "oidc", "/server", ""}, {[]int{1, 2, 3}, "oidc", "/missing/server", "/missing/exporter"},
	} {
		if _, err := generateCurrentQueryFixture(context.Background(), t.TempDir(), tc.ports, tc.mode, tc.binary, tc.exporter); err == nil {
			t.Fatal("unqualified current fixture accepted")
		}
	}
}
