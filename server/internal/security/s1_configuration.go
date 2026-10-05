package security

// S1ExecutionConfig is the complete versioned deterministic admission config.
// Policy limits are semantic review context; the complete config also scopes
// the certified genesis/prefix. S2 must certify/distribute this exact value.
type S1ExecutionConfig struct {
	Version  uint16
	Policy   PolicyLimits
	Capacity S1Capacity
}

func (c S1ExecutionConfig) valid() bool {
	return c.Version == S1Version && c.Policy.valid() && c.Capacity.valid()
}

func (c S1ExecutionConfig) Digest() [32]byte {
	if !c.valid() {
		return [32]byte{}
	}
	return s1Digest("execution-configuration", c)
}

func s1PolicyConfiguration(limits PolicyLimits) [32]byte {
	if !limits.valid() {
		return [32]byte{}
	}
	return s1Digest("policy-configuration", struct {
		Version uint16
		Policy  PolicyLimits
	}{S1Version, limits})
}
