package service

import "github.com/anaregdesign/lantern/server/internal/lifecyclepolicy"

type (
	LifecyclePolicy         = lifecyclepolicy.Policy
	LifecyclePolicySnapshot = lifecyclepolicy.Snapshot
	LifecyclePolicySource   = lifecyclepolicy.Source
	LifecyclePolicyOwner    = lifecyclepolicy.Owner
	ShutdownPlan            = lifecyclepolicy.ShutdownPlan
)

func NewLifecyclePolicyOwner(initial LifecyclePolicySnapshot) (*LifecyclePolicyOwner, error) {
	return lifecyclepolicy.NewOwner(initial)
}

func NewShutdownPlan(source LifecyclePolicySource) (*ShutdownPlan, error) {
	return lifecyclepolicy.NewShutdownPlan(source)
}
