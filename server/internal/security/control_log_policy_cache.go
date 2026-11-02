package security

import (
	"bytes"
	"sync"
)

const logPolicyCandidateCacheEntries = 8

// This budget counts retained canonical envelope bytes. Decoded capabilities
// and per-call detached copies are separately bounded by eight entries and
// the existing fixed cohort/schema limits; this is not a total-heap ceiling.
const logPolicyCandidateCacheBytes = 1 << 20

type logPolicyCandidateCache struct {
	mu      sync.Mutex
	owner   *s2cTrust
	context [32]byte
	items   map[string]*s2cHistoricalH
	order   []string
	bytes   int
}

func logPolicyCandidateCacheContext(t *s2cTrust) [32]byte {
	if t == nil || t.genesis == nil || t.genesis.state == nil || t.genesis.state.projection == nil {
		return [32]byte{}
	}
	type originDigest struct {
		ID     uint32
		Digest [32]byte
	}
	digests := make([]originDigest, 0, len(t.origins))
	for _, origin := range t.origins {
		digests = append(digests, originDigest{origin.ID, t.originDigests[origin.ID]})
	}
	return s2cHash("log-policy-verifier-context-v1", struct {
		Scope, Members, Genesis, Membership [32]byte
		Cut                                 SemanticCut
		Configuration                       S1ExecutionConfig
		Roots                               s2CapsuleRoots
		Bounds                              s2cBounds
		Voters                              []s2cMember
		Origins                             []s2cOriginDescriptor
		OriginDigests                       []originDigest
	}{t.scope, t.memberSet, t.genesis.state.prefix, t.genesis.state.membership, t.genesis.state.projection.cut, t.genesis.state.configuration, t.genesis.roots, t.bounds, t.members, t.origins, digests})
}

// Memoization covers only exact bytes already authenticated under this exact
// immutable native trust. Copies/changed contexts cannot borrow the cache.
// Every caller receives detached capabilities, including all signature bytes.
func (c *logPolicyCandidateCache) get(t *s2cTrust, raw []byte) *s2cHistoricalH {
	if c == nil || c.owner != t || c.context != logPolicyCandidateCacheContext(t) {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return cloneLogPolicyCandidate(c.items[string(raw)])
}
func (c *logPolicyCandidateCache) put(t *s2cTrust, h *s2cHistoricalH) {
	if c == nil || c.owner != t || h == nil || len(h.candidate) > logPolicyCandidateCacheBytes || c.context != logPolicyCandidateCacheContext(t) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = map[string]*s2cHistoricalH{}
	}
	if c.items[h.candidate] != nil {
		return
	}
	for len(c.order) >= logPolicyCandidateCacheEntries || c.bytes > logPolicyCandidateCacheBytes-len(h.candidate) {
		key := c.order[0]
		c.order = c.order[1:]
		c.bytes -= len(key)
		delete(c.items, key)
	}
	c.items[h.candidate] = cloneLogPolicyCandidate(h)
	c.order = append(c.order, h.candidate)
	c.bytes += len(h.candidate)
}
func cloneLogPolicyCandidate(h *s2cHistoricalH) *s2cHistoricalH {
	if h == nil {
		return nil
	}
	copy := *h
	if h.handoff != nil {
		handoff := *h.handoff
		copy.handoff = &handoff
		if handoff.authorization != nil {
			authorization := *handoff.authorization
			handoff.authorization = &authorization
		}
	}
	copy.logCertificate = cloneLogPolicyCertificate(h.logCertificate)
	return &copy
}
func cloneLogPolicyCertificate(c *logPolicySupportCertificate) *logPolicySupportCertificate {
	if c == nil {
		return nil
	}
	clone := *c
	clone.voters = append([]logPolicySupportAttestation(nil), c.voters...)
	clone.origins = append([]logPolicySupportAttestation(nil), c.origins...)
	for i := range clone.voters {
		clone.voters[i].Signature = bytes.Clone(clone.voters[i].Signature)
	}
	for i := range clone.origins {
		clone.origins[i].Signature = bytes.Clone(clone.origins[i].Signature)
	}
	return &clone
}
