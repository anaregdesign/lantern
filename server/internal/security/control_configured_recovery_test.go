package security

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestConfiguredRecoveryResponseIsReadOnlyExactRetainedHeader(t *testing.T) {
	n, keys, desired := configuredTestNativeCluster(t, 3)
	header, operation := configuredTestHeader(t, n.fixture, keys, desired)
	raw := configuredTestSeal(t, n.fixture, keys[1], header, operation)
	n.nodes[1].gate.Lock()
	_, reserveErr := n.nodes[1].reserveOriginLocked(header.ID, operation.digest, s2cCredit{})
	n.nodes[1].gate.Unlock()
	if reserveErr != nil {
		t.Fatal(reserveErr)
	}
	digest, err := n.nodes[1].persistSealedOriginH(raw)
	if err != nil {
		t.Fatal(err)
	}
	n.selectValue(1, digest, 1, 2)
	n.send(s2cAccept, 1, 3)
	owner := configuredTestEndorser(t, n, keys, 3)
	k := n.nodes[3]
	request := configuredRecoveryRequest{k.trust.scope, k.replayState.slot + 1, k.replayState.prefix}
	input, _ := json.Marshal(request)
	before := k.p.count
	response, err := owner.network.configuredRecoveryResponse(t.Context(), input, func() error { return owner.network.check(t.Context()) })
	if err != nil || !bytes.Equal(response, []byte(k.accepted.raw)) || k.p.count != before {
		t.Fatal("retained header lookup changed or manufactured protocol state", err)
	}
	decoded, err := s2cDecodeMessage(k.trust, response)
	if err != nil || decoded.kind != s2cAccept || s2cVerifyP1(k.trust, decoded) != nil {
		t.Fatal("retained response lost native proposer/P1/H proof", err)
	}
	for name, edit := range map[string]func(*configuredRecoveryRequest){
		"other scope":  func(r *configuredRecoveryRequest) { r.Scope[0] ^= 1 },
		"zero slot":    func(r *configuredRecoveryRequest) { r.Slot = 0 },
		"other slot":   func(r *configuredRecoveryRequest) { r.Slot++ },
		"other prefix": func(r *configuredRecoveryRequest) { r.Prefix[0] ^= 1 },
	} {
		t.Run(name, func(t *testing.T) {
			bad := request
			edit(&bad)
			raw, _ := json.Marshal(bad)
			response, _ := owner.network.configuredRecoveryResponse(t.Context(), raw, func() error { return owner.network.check(t.Context()) })
			if len(response) != 0 || k.p.count != before {
				t.Fatal("incompatible hint request disclosed or changed native state")
			}
		})
	}
	if response, err := owner.network.configuredRecoveryResponse(t.Context(), input, nil); err == nil || len(response) != 0 {
		t.Fatal("unadmitted caller received accepted evidence")
	}
}
