package service

import (
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
)

// Original canonical envelopes, not status callers, supply these identities.
// Legacy unclassified receipts remain unproven; an empty identity never grants
// scoped disclosure. Physical data keys are decoded exactly once.
func receiptResourceIdentity(format, key, head string) (mutationreceipt.ResourceIdentity, error) {
	if format == "" {
		return mutationreceipt.ResourceIdentity{}, nil
	}
	if format != keyspace.Version {
		return mutationreceipt.ResourceIdentity{}, keyspace.ErrInvalidKey
	}
	logical, err := keyspace.LogicalKey(key)
	if err != nil {
		return mutationreceipt.ResourceIdentity{}, err
	}
	resource := mutationreceipt.ResourceIdentity{Key: logical}
	if head != "" {
		resource.Head, err = keyspace.LogicalKey(head)
		if err != nil {
			return mutationreceipt.ResourceIdentity{}, err
		}
	}
	return resource, nil
}

func receiptResourceMatches(receipt mutationreceipt.Receipt, format, key, head string) bool {
	resource, err := receiptResourceIdentity(format, key, head)
	return err == nil && receipt.Resource == resource
}
