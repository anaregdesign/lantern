package security

import (
	"context"
	"errors"
	"time"
)

// The production private constructor supplies the actual native time backend;
// it has no clock/bool injection port. Public Wire startup does not select it.
// A first bounded anchor is required before even opening a current M/P/B family.
func openCurrentAuthorityOwner(ctx context.Context, c s3aConfig, originKey, browserOrigin string, fresh bool, floors s3aFloors) (_ *authorityOriginOwner, err error) {
	if c.timeOwner != nil {
		return nil, errS3AConfig
	}
	key, err := loadAuthorityOriginKey(c, originKey)
	if err != nil {
		return nil, err
	}
	transferred := false
	defer func() {
		if !transferred {
			clear(key)
		}
	}()
	clock, err := newNativeAuthorityTimeOwner()
	if err != nil {
		return nil, err
	}
	defer func() {
		if !transferred {
			clock.close()
		}
	}()
	// Includes one ordinary 32-second loss backoff and a second bounded flight.
	// A five-second startup could never observe recovery from the first loss.
	anchoring, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err = clock.current(); err == nil {
			break
		}
		select {
		case <-anchoring.Done():
			clock.mu.Lock()
			cause := clock.lastError
			clock.mu.Unlock()
			return nil, errors.Join(anchoring.Err(), cause)
		case <-ticker.C:
		}
	}
	if err = bindAuthorityNetworkTime(&c, clock); err != nil {
		return nil, err
	}
	n, err := openS3AOwner(c, fresh, floors)
	if err != nil {
		return nil, err
	}
	n.ownedTime = true
	origin, err := attachAuthorityOrigin(n, key, browserOrigin)
	if err != nil {
		_ = n.Close()
		return nil, err
	}
	transferred = true
	return origin, nil
}
