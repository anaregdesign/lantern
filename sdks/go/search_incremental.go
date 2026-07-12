// Package client: search_incremental.go owns IncrementalSearch, the
// search-as-you-type driver that wraps SearchVertices with debounce,
// in-flight cancellation, and stale-drop so a UI can fire a fresh search on
// every keystroke and only ever observe the result of the LATEST query.
//
// It is the full-text analogue of the admin SPA's vertex picker: rapid query
// updates are coalesced and debounced, a newer query cancels the previous
// in-flight RPC, and a late reply from a superseded query is dropped (never
// delivered out of order). The wire surface is unchanged — IncrementalSearch
// is pure client-side orchestration over the existing SearchVertices RPC, so
// it needs no proto or server support.
package client

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// defaultIncrementalDebounce is the debounce window applied between the last
// Search call and the dispatched RPC when WithDebounce is not supplied. It
// mirrors the admin SPA's SUGGEST_DEBOUNCE_MS so the Go and browser
// search-as-you-type surfaces feel identical.
const defaultIncrementalDebounce = 150 * time.Millisecond

// defaultMinQueryLength is the shortest query (in runes) that triggers an RPC
// when WithMinQueryLength is not supplied. A shorter query short-circuits to
// an empty result without a round-trip.
const defaultMinQueryLength = 1

// SearchUpdate is one delivery on IncrementalSearch.Updates: the query that
// produced it paired with either its ranked hits or the error the
// SearchVertices call returned. On a non-nil Err, Hits is nil. Query is always
// the exact string passed to Search, so a consumer rendering results can
// confirm the delivery still matches the text currently in the input box.
type SearchUpdate struct {
	Query string
	Hits  []SearchHit
	Err   error
}

// IncrementalSearchOption configures NewIncrementalSearch.
type IncrementalSearchOption func(*incrementalSearchOptions)

type incrementalSearchOptions struct {
	debounce       time.Duration
	limit          uint32
	prefix         string
	minQueryLength int
}

// WithDebounce sets the quiet period that must elapse after the most recent
// Search call before the driver issues the RPC. Bursts of keystrokes within
// the window collapse to a single search of the final query. The default is
// 150ms; pass 0 to dispatch on the next driver tick without waiting.
func WithDebounce(d time.Duration) IncrementalSearchOption {
	return func(o *incrementalSearchOptions) { o.debounce = d }
}

// WithIncrementalSearchLimit forwards a per-call hit cap to every
// SearchVertices RPC the driver issues, exactly like WithSearchLimit on a
// one-shot call. 0 (the default) lets the server apply its configured default.
func WithIncrementalSearchLimit(n uint32) IncrementalSearchOption {
	return func(o *incrementalSearchOptions) { o.limit = n }
}

// WithIncrementalSearchPrefix scopes every issued search to vertices whose key
// carries the given prefix, exactly like WithSearchPrefix on a one-shot call.
// An empty prefix (the default) searches every live vertex.
func WithIncrementalSearchPrefix(p string) IncrementalSearchOption {
	return func(o *incrementalSearchOptions) { o.prefix = p }
}

// WithMinQueryLength sets the shortest query, counted in runes, that triggers
// an RPC. A shorter (e.g. empty) query is answered immediately with an
// empty-hit SearchUpdate and no round-trip, so a cleared input box resets the
// result list without a wasted call. The default is 1; pass 0 to search even
// the empty query (the server returns zero hits for it).
func WithMinQueryLength(n int) IncrementalSearchOption {
	return func(o *incrementalSearchOptions) { o.minQueryLength = n }
}

// IncrementalSearch is a search-as-you-type driver over SearchVertices.
// Construct one with Lantern.NewIncrementalSearch, push queries with Search as
// the user types, and consume ranked results from Updates. It is safe to call
// Search from one goroutine while ranging over Updates in another.
//
// Lifecycle: the driver runs until the context passed to NewIncrementalSearch
// is cancelled or Close is called. After shutdown Updates is no longer written
// (a consumer ranging over it should also select on its own done signal) and
// further Search calls are dropped.
type IncrementalSearch struct {
	l    *Lantern
	opts incrementalSearchOptions

	updates chan SearchUpdate

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu           sync.Mutex
	closed       bool
	timer        *time.Timer
	searchCancel context.CancelFunc
	searchEpoch  uint64
	workers      sync.WaitGroup

	// epoch is bumped once per input, before its debounce starts. A reply is
	// delivered only while epoch still equals the value that input recorded,
	// so an old RPC cannot publish during the next input's debounce window.
	epoch     atomic.Uint64
	deliverMu sync.Mutex

	closeOnce sync.Once
}

// NewIncrementalSearch starts a search-as-you-type driver bound to ctx and
// returns it. The driver tracks every debounce callback and SearchVertices
// call so cancellation and Close can wait for all owned work. Cancel ctx or
// call Close to stop it.
func (l *Lantern) NewIncrementalSearch(ctx context.Context, opts ...IncrementalSearchOption) *IncrementalSearch {
	o := incrementalSearchOptions{
		debounce:       defaultIncrementalDebounce,
		minQueryLength: defaultMinQueryLength,
	}
	for _, apply := range opts {
		apply(&o)
	}
	if o.debounce < 0 {
		o.debounce = 0
	}
	if o.minQueryLength < 0 {
		o.minQueryLength = 0
	}
	dctx, cancel := context.WithCancel(ctx)
	s := &IncrementalSearch{
		l:       l,
		opts:    o,
		updates: make(chan SearchUpdate, 1),
		ctx:     dctx,
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	go func() {
		<-dctx.Done()
		s.shutdown()
	}()
	return s
}

// Search makes query the latest input immediately: it invalidates buffered
// output, stops the previous debounce timer, and cancels the active RPC before
// starting the new debounce window. It never blocks on network work. A call
// after Close (or ctx cancellation) is a no-op.
func (s *IncrementalSearch) Search(query string) {
	s.mu.Lock()
	if s.closed || s.ctx.Err() != nil {
		s.mu.Unlock()
		return
	}
	epoch := s.epoch.Add(1)
	s.stopTimerLocked()
	s.cancelSearchLocked()
	s.invalidateBuffered()

	if len([]rune(query)) < s.opts.minQueryLength {
		s.mu.Unlock()
		// Too short to search: reset immediately, without waiting for debounce.
		s.deliver(epoch, SearchUpdate{Query: query, Hits: []SearchHit{}})
		return
	}

	// Account for the timer callback before publishing the timer pointer.
	// shutdown changes closed under the same lock, so Wait cannot race an Add.
	s.workers.Add(1)
	s.timer = time.AfterFunc(s.opts.debounce, func() {
		defer s.workers.Done()
		s.dispatch(epoch, query)
	})
	s.mu.Unlock()
}

// Updates is the stream of search results, one SearchUpdate per dispatched
// query, most-recent-wins: the channel is buffered to one and the driver
// replaces an unconsumed update rather than blocking, so a slow consumer
// always reads the latest result rather than a backlog. Errors (including
// ErrFailedPrecondition when the server's search index is disabled) arrive as
// SearchUpdate.Err.
func (s *IncrementalSearch) Updates() <-chan SearchUpdate {
	return s.updates
}

// Close stops the driver and waits for its goroutine to exit, cancelling any
// in-flight SearchVertices call. It is idempotent and safe to call from any
// goroutine; it always returns nil (the error return mirrors io.Closer for
// composability).
func (s *IncrementalSearch) Close() error {
	s.cancel()
	s.shutdown()
	<-s.done
	return nil
}

// dispatch starts the RPC only if the input that armed this timer is still
// current. The timer callback itself owns the blocking RPC, so shutdown can
// deterministically wait for every background worker.
func (s *IncrementalSearch) dispatch(epoch uint64, query string) {
	s.mu.Lock()
	if s.closed || s.ctx.Err() != nil || s.epoch.Load() != epoch {
		s.mu.Unlock()
		return
	}
	s.timer = nil
	sctx, cancel := context.WithCancel(s.ctx)
	s.searchCancel = cancel
	s.searchEpoch = epoch
	s.mu.Unlock()

	s.doSearch(sctx, epoch, query)

	s.mu.Lock()
	if s.searchEpoch == epoch {
		s.searchCancel = nil
		s.searchEpoch = 0
	}
	s.mu.Unlock()
}

// doSearch issues one SearchVertices call and delivers its result unless the
// call was cancelled (superseded by a newer query) before it returned.
func (s *IncrementalSearch) doSearch(ctx context.Context, epoch uint64, query string) {
	opts := make([]SearchOption, 0, 2)
	if s.opts.limit > 0 {
		opts = append(opts, WithSearchLimit(s.opts.limit))
	}
	if s.opts.prefix != "" {
		opts = append(opts, WithSearchPrefix(s.opts.prefix))
	}
	hits, err := s.l.SearchVertices(ctx, query, opts...)
	if ctx.Err() != nil {
		return // superseded or shut down: drop the stale reply
	}
	s.deliver(epoch, SearchUpdate{Query: query, Hits: hits, Err: err})
}

// deliver publishes u on Updates with most-recent-wins semantics, but only
// while epoch is still current — a reply whose epoch was overtaken by a newer
// dispatch is dropped. The deliver lock serialises the drop-then-send against
// a concurrent short-circuit delivery from the driver goroutine.
func (s *IncrementalSearch) deliver(epoch uint64, u SearchUpdate) {
	s.deliverMu.Lock()
	defer s.deliverMu.Unlock()
	if s.epoch.Load() != epoch {
		return
	}
	select {
	case <-s.updates: // discard a still-unread update so the newest wins
	default:
	}
	select {
	case s.updates <- u:
	default:
	}
}

// invalidateBuffered removes a result that was delivered before the newest
// input but not yet consumed. Callers must hold s.mu; deliverMu serializes the
// drain against a finishing RPC.
func (s *IncrementalSearch) invalidateBuffered() {
	s.deliverMu.Lock()
	defer s.deliverMu.Unlock()
	select {
	case <-s.updates:
	default:
	}
}

// stopTimerLocked stops and releases the pending debounce timer. A timer that
// has already started owns its workers.Done call; a timer stopped here never
// runs, so this method balances the Add performed by Search.
func (s *IncrementalSearch) stopTimerLocked() {
	if s.timer == nil {
		return
	}
	if s.timer.Stop() {
		s.workers.Done()
	}
	s.timer = nil
}

func (s *IncrementalSearch) cancelSearchLocked() {
	if s.searchCancel == nil {
		return
	}
	s.searchCancel()
	s.searchCancel = nil
	s.searchEpoch = 0
}

// shutdown is shared by Close and parent-context cancellation. It prevents
// new timers, cancels current work, and closes done only after every timer/RPC
// callback has returned.
func (s *IncrementalSearch) shutdown() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		s.closed = true
		s.epoch.Add(1)
		s.stopTimerLocked()
		s.cancelSearchLocked()
		s.mu.Unlock()
		s.workers.Wait()
		close(s.done)
	})
}
