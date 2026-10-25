package security

import (
	"context"
	"crypto/ed25519"
	"errors"
)

// The production private constructor supplies the actual native time backend;
// it has no clock/bool injection port. Public Wire startup does not select it.
// A first bounded anchor is required before even opening a current M/P/B family.
func openCurrentAuthorityOwner(ctx context.Context, c s3aConfig, originKey, browserOrigin string, fresh bool, floors s3aFloors) (_ *authorityOriginOwner, err error) {
	return openCurrentAuthorityOwnerWithCustody(ctx, c, originKey, browserOrigin, fresh, floors, nil)
}

// Custody is a private durable lifecycle boundary, never an injected clock or
// a readiness assertion. Private protocol fixtures can still own their floors.
func openCurrentAuthorityOwnerWithCustody(ctx context.Context, c s3aConfig, originKey, browserOrigin string, fresh bool, floors s3aFloors, custody *currentCustody) (_ *authorityOriginOwner, err error) {
	if c.timeOwner != nil {
		return nil, errS3AConfig
	}
	var key ed25519.PrivateKey
	if c.Participant.OwnedOrigin != 0 {
		key, err = loadAuthorityOriginKey(c, originKey)
		if err != nil {
			return nil, err
		}
	} else if originKey != "" {
		return nil, errS3AConfig
	}
	transferred := false
	defer func() {
		if !transferred {
			clear(key)
		}
	}()
	clock, err := newCurrentAuthorityTimeOwner()
	if err != nil {
		return nil, err
	}
	defer func() {
		if !transferred {
			clock.close()
			err = withAuthorityTimeAcquisitionCounts(err, clock.acquisitionCounts())
		}
	}()
	if err = waitAuthorityTimeAnchor(ctx, clock); err != nil {
		return nil, err
	}
	if err = bindAuthorityNetworkTime(&c, clock); err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if custody != nil {
		if err = custody.running(); err != nil {
			return nil, err
		}
	}
	n, err := openS3AOwner(c, fresh, floors)
	if err != nil {
		return nil, err
	}
	n.ownedTime = true
	origin, err := attachAuthorityOrigin(n, key, browserOrigin)
	if err != nil {
		return nil, errors.Join(err, n.Close())
	}
	transferred = true
	return origin, nil
}
