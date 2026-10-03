package security

// PolicyLimits cap compilation/admission, rather than work on each data RPC.
type PolicyLimits struct {
	MaxRoles       int
	MaxRules       int // per Role
	MaxPrefixBytes int // per rule
	MaxTotalBytes  int // prefixes across the complete policy
	MaxAssignments int // per Principal
}

func DefaultPolicyLimits() PolicyLimits {
	return PolicyLimits{MaxRoles: 256, MaxRules: 128, MaxPrefixBytes: 1024,
		MaxTotalBytes: 1 << 20, MaxAssignments: 32}
}

// Operators may lower admission limits, but cannot remove the hard bounds.
func (l PolicyLimits) valid() bool {
	max := DefaultPolicyLimits()
	return l.MaxRoles > 0 && l.MaxRoles <= max.MaxRoles && l.MaxRules > 0 && l.MaxRules <= max.MaxRules &&
		l.MaxPrefixBytes > 0 && l.MaxPrefixBytes <= max.MaxPrefixBytes && l.MaxTotalBytes > 0 &&
		l.MaxTotalBytes <= max.MaxTotalBytes && l.MaxAssignments > 0 && l.MaxAssignments <= max.MaxAssignments
}
