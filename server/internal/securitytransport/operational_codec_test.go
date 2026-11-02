package securitytransport

import (
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
	"reflect"
	"testing"
)

func TestOperationalCodecClosedGroupsAndExactValues(t *testing.T) {
	for _, group := range operationalpolicy.Groups() {
		t.Run(string(group), func(t *testing.T) {
			values, err := operationalpolicy.Defaults(group)
			if err != nil {
				t.Fatal(err)
			}
			wire := encodeOperationalValues(values)
			decoded, err := decodeOperationalValues(wire)
			if err != nil || !reflect.DeepEqual(decoded, values) {
				t.Fatalf("round trip: %#v %v", decoded, err)
			}
			fp := ""
			if group == operationalpolicy.GroupSearch {
				fp = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
			}
			change, err := decodeOperationalPolicy(&pb.OperationalPolicyChange{Version: 1, ExpectedGeneration: 9007199254740999, Values: wire, CompatibilityFingerprint: fp})
			if err != nil || change.ExpectedGeneration != 9007199254740999 {
				t.Fatal("exact generation lost", err)
			}
			wire.ProtoReflect().SetUnknown([]byte{0x48, 0x01})
			if _, err := decodeOperationalValues(wire); err == nil {
				t.Fatal("unknown nested input accepted")
			}
		})
	}
	for _, wire := range []*pb.OperationalPolicyChange{nil, {Version: 2}, {Version: 1, Values: &pb.OperationalPolicyValues{}}, {Version: 1, Values: encodeOperationalValues(operationalpolicy.Values{Rate: &operationalpolicy.Rate{RPS: 0, Burst: 1}})}} {
		if _, err := decodeOperationalPolicy(wire); err == nil {
			t.Fatal("malformed typed operation accepted")
		}
	}
}
