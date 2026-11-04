package security

import (
	"bytes"
	"fmt"
	"sync"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/operationalpolicy"
)

func TestOperationalPolicyCandidateCacheDetachedExactTrust(t *testing.T) {
	f, _, candidate := operationalPolicyTestCandidate(t, operationalpolicy.GroupSearch, 1)
	wire := candidate.candidate
	if len(f.trust.operationalPolicyCache.items) != 1 {
		t.Fatal("verified candidate not retained")
	}
	candidate.handoff.authorization.credentialEvidence[0] ^= 1
	candidate.operationalCertificate.voters[0].Signature[0] ^= 1
	candidate.operationalCertificate.origins[0].Signature[0] ^= 1
	again, err := verifyOperationalPolicyCandidate(f.trust, []byte(wire))
	if err != nil || again.handoff.authorization.credentialEvidence == candidate.handoff.authorization.credentialEvidence ||
		bytes.Equal(again.operationalCertificate.voters[0].Signature, candidate.operationalCertificate.voters[0].Signature) ||
		bytes.Equal(again.operationalCertificate.origins[0].Signature, candidate.operationalCertificate.origins[0].Signature) {
		t.Fatal("cache retained caller-mutable capability", err)
	}
	copied := *f.trust
	if copied.operationalPolicyCache.get(&copied, []byte(wire)) != nil {
		t.Fatal("copied trust borrowed original cache ownership")
	}
	copied.bounds.PayloadBytes = uint64(len(wire) - 1)
	if _, err := verifyOperationalPolicyCandidate(&copied, []byte(wire)); err == nil {
		t.Fatal("tighter copied trust borrowed prior verification")
	}
	originalKey := f.trust.origins[0].PublicKey
	f.trust.origins[0].PublicKey[0] ^= 1
	if _, err := verifyOperationalPolicyCandidate(f.trust, []byte(wire)); err == nil {
		t.Fatal("changed origin trust borrowed prior verification")
	}
	f.trust.origins[0].PublicKey = originalKey
	if _, err := verifyOperationalPolicyCandidate(f.trust, append([]byte(wire), 0)); err == nil {
		t.Fatal("different bytes borrowed prior verification")
	}
}

func TestOperationalPolicyCandidateCacheBoundedConcurrent(t *testing.T) {
	f, _, candidate := operationalPolicyTestCandidate(t, operationalpolicy.GroupSlowRPC, 1)
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 8 {
				if _, err := verifyOperationalPolicyCandidate(f.trust, []byte(candidate.candidate)); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	workers.Wait()
	cache := f.trust.operationalPolicyCache
	for i := range operationalPolicyCandidateCacheEntries + 2 {
		copy := *candidate
		copy.candidate = fmt.Sprint(i) + candidate.candidate
		cache.put(f.trust, &copy)
	}
	if len(cache.items) != operationalPolicyCandidateCacheEntries || len(cache.order) != operationalPolicyCandidateCacheEntries || cache.bytes > operationalPolicyCandidateCacheBytes {
		t.Fatal("candidate cache exceeded fixed bounds")
	}
	oversized := *candidate
	oversized.candidate = string(bytes.Repeat([]byte{'x'}, operationalPolicyCandidateCacheBytes+1))
	before := cache.bytes
	cache.put(f.trust, &oversized)
	if cache.bytes != before || cache.items[oversized.candidate] != nil {
		t.Fatal("oversized candidate entered cache")
	}
}

func TestOperationalPolicyCandidateCacheRechecksVerificationContext(t *testing.T) {
	f, h, candidate := operationalPolicyTestCandidate(t, operationalpolicy.GroupSearch, 1)
	for name, mutate := range map[string]func() func(){
		"origin digest": func() func() {
			old := f.trust.originDigests[h.originID]
			f.trust.originDigests[h.originID] = [32]byte{99}
			return func() { f.trust.originDigests[h.originID] = old }
		},
		"configuration": func() func() {
			old := f.trust.genesis.state.configuration
			f.trust.genesis.state.configuration.Policy.MaxRoles++
			return func() { f.trust.genesis.state.configuration = old }
		},
		"genesis cut": func() func() {
			old := f.trust.genesis.state.projection.cut
			f.trust.genesis.state.projection.cut.Domain[0] ^= 1
			return func() { f.trust.genesis.state.projection.cut = old }
		},
	} {
		t.Run(name, func(t *testing.T) {
			restore := mutate()
			defer restore()
			if _, err := verifyOperationalPolicyCandidate(f.trust, []byte(candidate.candidate)); err == nil {
				t.Fatal("changed verifier context reused cached proof")
			}
		})
	}
	// A context dependency need not make otherwise authentic historical bytes
	// invalid. It must still force cold verification instead of memo reuse.
	oldPrefix := f.trust.genesis.state.prefix
	f.trust.genesis.state.prefix[0] ^= 1
	if f.trust.operationalPolicyCache.get(f.trust, []byte(candidate.candidate)) != nil {
		t.Fatal("changed prefix reused cache")
	}
	f.trust.genesis.state.prefix = oldPrefix
}
