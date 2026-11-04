package operationalpolicy

import (
	"encoding/hex"
	"math"
)

// Version is the exact value schema and application contract version.
const Version uint32 = 1

// Policy is one committed desired generation. CompatibilityFingerprint is
// derived Search engine metadata, never an additional editable setting.
type Policy struct {
	Version                  uint32 `json:"version"`
	Generation               uint64 `json:"generation"`
	Values                   Values `json:"values"`
	CompatibilityFingerprint string `json:"compatibility_fingerprint,omitempty"`
}

func (p *Policy) Validate() error {
	if p == nil || p.Version != Version || p.Generation == 0 || p.Values.Validate() != nil {
		return ErrInvalid
	}
	group, _ := p.Values.Group()
	return validateFingerprint(group, p.CompatibilityFingerprint)
}

func (p *Policy) Clone() *Policy {
	if p == nil {
		return nil
	}
	detached := *p
	detached.Values = p.Values.Clone()
	return &detached
}

// Change carries one atomic group update. Zero ExpectedGeneration explicitly
// requests first adoption; a successor always receives the next generation.
// Backup retention acknowledgement belongs to the reviewed immutable intent.
type Change struct {
	ExpectedGeneration             uint64 `json:"expected_generation"`
	Values                         Values `json:"values"`
	CompatibilityFingerprint       string `json:"compatibility_fingerprint,omitempty"`
	RetentionReductionAcknowledged bool   `json:"retention_reduction_acknowledged,omitempty"`
}

func (c Change) Validate() error {
	if c.ExpectedGeneration == math.MaxUint64 || c.Values.Validate() != nil {
		return ErrInvalid
	}
	group, _ := c.Values.Group()
	if c.RetentionReductionAcknowledged && group != GroupBackup {
		return ErrInvalid
	}
	return validateFingerprint(group, c.CompatibilityFingerprint)
}

func (c Change) Clone() Change {
	c.Values = c.Values.Clone()
	return c
}

func validateFingerprint(group Group, fingerprint string) error {
	if group != GroupSearch {
		if fingerprint != "" {
			return ErrInvalid
		}
		return nil
	}
	if len(fingerprint) != 64 {
		return ErrInvalid
	}
	raw, err := hex.DecodeString(fingerprint)
	if err != nil || hex.EncodeToString(raw) != fingerprint {
		return ErrInvalid
	}
	return nil
}

// Policies is the closed desired-policy collection in the security Image.
// A missing group means unadopted only when read from a verified authority.
type Policies struct {
	SlowRPC     *Policy `json:"slow_rpc,omitempty"`
	Rate        *Policy `json:"rate,omitempty"`
	Request     *Policy `json:"request,omitempty"`
	Backup      *Policy `json:"backup,omitempty"`
	Capacity    *Policy `json:"capacity,omitempty"`
	Lifecycle   *Policy `json:"lifecycle,omitempty"`
	Search      *Policy `json:"search,omitempty"`
	Replication *Policy `json:"replication,omitempty"`
}

func (p Policies) ValidateNilAllowed() error {
	for _, group := range Groups() {
		policy := p.Get(group)
		if policy == nil {
			continue
		}
		actual, err := policy.Values.Group()
		if policy.Validate() != nil || err != nil || actual != group {
			return ErrInvalid
		}
	}
	return nil
}

// Get returns a detached policy, never a mutable alias of installed state.
func (p Policies) Get(group Group) *Policy {
	var policy *Policy
	switch group {
	case GroupSlowRPC:
		policy = p.SlowRPC
	case GroupRate:
		policy = p.Rate
	case GroupRequest:
		policy = p.Request
	case GroupBackup:
		policy = p.Backup
	case GroupCapacity:
		policy = p.Capacity
	case GroupLifecycle:
		policy = p.Lifecycle
	case GroupSearch:
		policy = p.Search
	case GroupReplication:
		policy = p.Replication
	}
	return policy.Clone()
}

// Set validates and detaches a group before changing the collection. The
// authority separately enforces adoption, generation and removal rules.
func (p *Policies) Set(group Group, policy *Policy) error {
	if p == nil || !group.Valid() {
		return ErrInvalid
	}
	if policy != nil {
		actual, err := policy.Values.Group()
		if policy.Validate() != nil || err != nil || actual != group {
			return ErrInvalid
		}
	}
	detached := policy.Clone()
	switch group {
	case GroupSlowRPC:
		p.SlowRPC = detached
	case GroupRate:
		p.Rate = detached
	case GroupRequest:
		p.Request = detached
	case GroupBackup:
		p.Backup = detached
	case GroupCapacity:
		p.Capacity = detached
	case GroupLifecycle:
		p.Lifecycle = detached
	case GroupSearch:
		p.Search = detached
	case GroupReplication:
		p.Replication = detached
	}
	return nil
}

func (p Policies) Clone() Policies {
	return Policies{SlowRPC: p.SlowRPC.Clone(), Rate: p.Rate.Clone(), Request: p.Request.Clone(), Backup: p.Backup.Clone(),
		Capacity: p.Capacity.Clone(), Lifecycle: p.Lifecycle.Clone(), Search: p.Search.Clone(), Replication: p.Replication.Clone()}
}
