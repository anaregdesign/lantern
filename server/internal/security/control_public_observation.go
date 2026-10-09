package security

import (
	"context"
	"sync"
)

type CurrentStopObservation uint8

const (
	CurrentStopNotObserved CurrentStopObservation = iota
	CurrentStopWaiting
	CurrentOldCutAuthorizationsStopped
)

const currentPublicObservationCapacity = 512

type currentPublicObservations struct {
	mu    sync.Mutex
	items map[FullChangeID]*OriginalObservation
	order []FullChangeID
}

// ObserveResult adds only volatile observation evidence to a genuine immutable
// original. FIFO eviction, restart and time-epoch loss each restart a fresh
// conservative wait. Wall dates and the original commit's timestamp are never
// used to reconstruct completion. This does not promise physical send/delivery.
func (o *CurrentAuthority) ObserveResult(ctx context.Context, result CurrentChangeResult) CurrentChangeResult {
	result.StopObservation = CurrentStopNotObserved
	if o == nil || o.origin == nil || result.Profile != o.Profile() || result.Progress != CurrentApplied || result.Original == nil || result.Original.disposition != S1Applied || result.Original.id != result.ID || result.Original.operation.digest != result.Intent {
		return result
	}
	cache := &o.observations
	cache.mu.Lock()
	defer cache.mu.Unlock()
	wait := cache.items[result.ID]
	if wait == nil {
		var err error
		wait, err = o.ObserveApplied(ctx, CurrentReview{Profile: result.Profile, ID: result.ID, Operation: result.Original.operation})
		if err != nil {
			return result
		}
		if cache.items == nil {
			cache.items = make(map[FullChangeID]*OriginalObservation)
		}
		if len(cache.order) == currentPublicObservationCapacity {
			delete(cache.items, cache.order[0])
			copy(cache.order, cache.order[1:])
			cache.order = cache.order[:len(cache.order)-1]
		}
		cache.items[result.ID] = wait
		cache.order = append(cache.order, result.ID)
	}
	done, err := wait.Complete(ctx)
	if err != nil {
		// Drop all starts on an uncertain epoch; never retain a derived bool.
		clear(cache.items)
		cache.order = nil
		return result
	}
	result.StopObservation = CurrentStopWaiting
	if done {
		result.StopObservation = CurrentOldCutAuthorizationsStopped
	}
	return result
}
