package oidc

import (
	"context"
	"crypto"
	"crypto/sha256"
	"encoding/json"
	"sync"
	"time"

	"github.com/anaregdesign/lantern/server/internal/security"
)

// Trust is supplied from an immutable security revision. ConfigRevision must
// advance on every Issuer update, including disable/re-enable. Generation and
// config revision isolate cache entries even when the URL is unchanged.
type Trust struct {
	Issuer         security.Issuer
	Generation     [16]byte
	ConfigRevision uint64
}

type cachedKeys struct {
	keys         map[keyID]crypto.PublicKey
	discovery    Discovery
	expires      time.Time
	refreshAfter time.Time
	used         uint64
	pending      chan struct{}
}

// KeyCache coalesces refreshes per exact trust configuration and limits both
// memory and unknown-kid traffic. Expired keys are never used on fetch failure.
// No token/negative-kid cache is needed; arbitrary kids cannot grow memory.
type KeyCache struct {
	mu           sync.Mutex
	fetcher      *Fetcher
	entries      map[[32]byte]*cachedKeys
	now          func() time.Time
	used         uint64
	refreshSlots chan struct{}
}

const (
	keyCacheEntries = 64
	keyCacheTTL     = 10 * time.Minute
	keyRefreshFloor = 30 * time.Second
)

func NewKeyCache(fetcher *Fetcher) *KeyCache {
	return NewKeyCacheWithClock(fetcher, time.Now)
}
func NewKeyCacheWithClock(fetcher *Fetcher, clock func() time.Time) *KeyCache {
	if clock == nil {
		clock = time.Now
	}
	return &KeyCache{fetcher: fetcher, entries: make(map[[32]byte]*cachedKeys), now: clock, refreshSlots: make(chan struct{}, 4)}
}

func trustDigest(trust Trust) ([32]byte, error) {
	if !trust.Issuer.Enabled || trust.Generation == [16]byte{} || trust.ConfigRevision == 0 {
		return [32]byte{}, ErrInvalidToken
	}
	encoded, err := json.Marshal(trust)
	if err != nil {
		return [32]byte{}, ErrInvalidToken
	}
	return sha256.Sum256(encoded), nil
}

func (c *KeyCache) Key(ctx context.Context, trust Trust, kid, algorithm string) (crypto.PublicKey, error) {
	if c == nil || c.fetcher == nil || len(kid) == 0 || len(kid) > 256 {
		return nil, ErrInvalidToken
	}
	digest, err := trustDigest(trust)
	if err != nil {
		return nil, err
	}
	for {
		c.mu.Lock()
		now := c.now()
		entry := c.entries[digest]
		if entry == nil {
			if len(c.entries) >= keyCacheEntries {
				var oldest [32]byte
				age := ^uint64(0)
				for key, candidate := range c.entries {
					if candidate.pending == nil && candidate.used < age {
						oldest, age = key, candidate.used
					}
				}
				if age == ^uint64(0) {
					c.mu.Unlock()
					return nil, ErrFetch
				}
				delete(c.entries, oldest)
			}
			entry = &cachedKeys{}
			c.entries[digest] = entry
		}
		c.used++
		entry.used = c.used
		if key := entry.keys[keyID{kid, algorithm}]; key != nil && now.Before(entry.expires) {
			c.mu.Unlock()
			return key, nil
		}
		if pending := entry.pending; pending != nil {
			c.mu.Unlock()
			select {
			case <-ctx.Done():
				return nil, ErrFetch
			case <-pending:
				continue
			}
		}
		if now.Before(entry.refreshAfter) {
			c.mu.Unlock()
			return nil, ErrInvalidToken
		}
		entry.pending = make(chan struct{})
		entry.refreshAfter = now.Add(keyRefreshFloor)
		c.mu.Unlock()

		discovery, keys, fetchErr := c.refresh(ctx, trust)
		c.mu.Lock()
		if fetchErr == nil {
			entry.keys, entry.discovery, entry.expires = keys, discovery, c.now().Add(keyCacheTTL)
		}
		close(entry.pending)
		entry.pending = nil
		key := entry.keys[keyID{kid, algorithm}]
		usable := fetchErr == nil && key != nil && c.now().Before(entry.expires)
		c.mu.Unlock()
		if !usable {
			return nil, ErrInvalidToken
		}
		return key, nil
	}
}

func (c *KeyCache) refresh(ctx context.Context, trust Trust) (Discovery, map[keyID]crypto.PublicKey, error) {
	select {
	case c.refreshSlots <- struct{}{}:
		defer func() { <-c.refreshSlots }()
	case <-ctx.Done():
		return Discovery{}, nil, ErrFetch
	}
	discovery, err := c.fetcher.Discover(ctx, trust.Issuer)
	if err != nil {
		return Discovery{}, nil, err
	}
	var document struct {
		Keys []jsonKey `json:"keys"`
	}
	if err := c.fetcher.GetJSON(ctx, discovery.JWKSURI, &document); err != nil {
		return Discovery{}, nil, err
	}
	keys, err := decodeKeys(document.Keys, trust.Issuer.Algorithms)
	return discovery, keys, err
}
