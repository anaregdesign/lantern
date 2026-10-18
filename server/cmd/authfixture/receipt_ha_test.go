package main

import "testing"

func TestReceiptHAControlPartitionsOnlyData(t *testing.T) {
	control := &receiptHAControl{}
	data := "/graph.v1.LanternReplicationService/Subscribe"
	policy := "/graph.v1.LanternSecurityPeerService/GetPolicySnapshot"
	for _, tc := range []struct {
		caller, target int
		path           string
		allow          bool
	}{
		{1, 2, data, true}, {2, 1, data, true}, {1, 3, data, false}, {3, 2, data, false},
		{0, 1, data, false}, {1, 0, data, false}, {3, 0, policy, false}, {1, 3, policy, false},
		{1, 2, "/graph.v1.LanternService/GetVertex", false},
	} {
		if got := control.allowed(tc.caller, tc.target, tc.path); got != tc.allow {
			t.Fatalf("%+v got %t", tc, got)
		}
	}
	control.connected.Store(true)
	if !control.allowed(3, 2, data) || control.allowed(1, 0, data) || control.allowed(0, 3, data) {
		t.Fatal("relay broadened the separate control participant's data plane")
	}
}
