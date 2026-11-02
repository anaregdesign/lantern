package security

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
)

func TestLogPolicyCandidateCacheDetachedExactTrust(t *testing.T) {
	f, h, profile, voters, origins := logPolicyCertificateFixture(t, 1)
	proof, err := encodeLogPolicySupportCertificate(f.trust, profile, h, voters, origins)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := encodeLogPolicyCandidate(f.trust, h, proof)
	if err != nil {
		t.Fatal(err)
	}
	wire := candidate.candidate
	if len(f.trust.logPolicyCache.items) != 1 {
		t.Fatal("verified candidate not retained")
	}
	candidate.handoff.authorization.credentialEvidence[0] ^= 1
	candidate.logCertificate.voters[0].Signature[0] ^= 1
	again, err := verifyLogPolicyCandidate(f.trust, []byte(wire))
	if err != nil || again.handoff.authorization.credentialEvidence == candidate.handoff.authorization.credentialEvidence || bytes.Equal(again.logCertificate.voters[0].Signature, candidate.logCertificate.voters[0].Signature) {
		t.Fatal("cache retained mutable caller capability", err)
	}
	copied := *f.trust
	copied.bounds.PayloadBytes = uint64(len(wire) - 1)
	if _, err := verifyLogPolicyCandidate(&copied, []byte(wire)); err == nil {
		t.Fatal("copied tighter bounds borrowed prior verification")
	}
	originalKey := f.trust.origins[0].PublicKey
	f.trust.origins[0].PublicKey[0] ^= 1
	if _, err := verifyLogPolicyCandidate(f.trust, []byte(wire)); err == nil {
		t.Fatal("changed trust reused cached verification")
	}
	f.trust.origins[0].PublicKey = originalKey
	if _, err := verifyLogPolicyCandidate(f.trust, append([]byte(wire), 0)); err == nil {
		t.Fatal("different bytes reused cached verification")
	}
}

func TestLogPolicyCandidateCacheBoundedAndConcurrent(t *testing.T) {
	f, h, profile, voters, origins := logPolicyCertificateFixture(t, 1)
	proof, err := encodeLogPolicySupportCertificate(f.trust, profile, h, voters, origins)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := encodeLogPolicyCandidate(f.trust, h, proof)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 8; j++ {
				if _, err := verifyLogPolicyCandidate(f.trust, []byte(candidate.candidate)); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	workers.Wait()
	cache := f.trust.logPolicyCache
	for i := 0; i < logPolicyCandidateCacheEntries+2; i++ {
		copy := *candidate
		copy.candidate = fmt.Sprint(i) + candidate.candidate
		cache.put(f.trust, &copy)
	}
	if len(cache.items) != logPolicyCandidateCacheEntries || len(cache.order) != logPolicyCandidateCacheEntries || cache.bytes > logPolicyCandidateCacheBytes {
		t.Fatal("cache exceeded fixed bounds")
	}
	oversized := *candidate
	oversized.candidate = string(bytes.Repeat([]byte{'x'}, logPolicyCandidateCacheBytes+1))
	before := cache.bytes
	cache.put(f.trust, &oversized)
	if cache.bytes != before || cache.items[oversized.candidate] != nil {
		t.Fatal("oversized input entered cache")
	}
}

func TestLogPolicyCandidateCacheRechecksEveryVerifierDependency(t *testing.T) {
	f, h, profile, voters, origins := logPolicyCertificateFixture(t, 1)
	proof, err := encodeLogPolicySupportCertificate(f.trust, profile, h, voters, origins)
	if err != nil {
		t.Fatal(err)
	}
	candidate, err := encodeLogPolicyCandidate(f.trust, h, proof)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range map[string]func() func(){
		"originDigest": func() func() {
			old := f.trust.originDigests[h.originID]
			f.trust.originDigests[h.originID] = [32]byte{99}
			return func() { f.trust.originDigests[h.originID] = old }
		},
		"configuration": func() func() {
			old := f.trust.genesis.state.configuration
			f.trust.genesis.state.configuration.Policy.MaxRoles++
			return func() { f.trust.genesis.state.configuration = old }
		},
		"genesisCut": func() func() {
			old := f.trust.genesis.state.projection.cut
			f.trust.genesis.state.projection.cut.Domain[0] ^= 1
			return func() { f.trust.genesis.state.projection.cut = old }
		},
	} {
		t.Run(name, func(t *testing.T) {
			restore := mutate()
			defer restore()
			if _, err := verifyLogPolicyCandidate(f.trust, []byte(candidate.candidate)); err == nil {
				t.Fatal("mutated verifier dependency reused cached proof")
			}
		})
	}
}
