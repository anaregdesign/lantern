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
	wake    chan struct{}

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu     sync.Mutex
	latest string
	dirty  bool

	// epoch is bumped once per dispatched search (real or short-circuit). A
	// reply is delivered only while epoch still equals the value its dispatch
	// recorded, so a late reply from a superseded query is dropped.
	epoch     atomic.Uint64
	deliverMu sync.Mutex

	closeOnce sync.Once
}

// NewIncrementalSearch starts a search-as-you-type driver bound to ctx and
// returns it. The driver owns one background goroutine that debounces Search
// calls, issues SearchVertices, and publishes results to Updates. Cancel ctx
// or call Close to stop it.
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
		wake:    make(chan struct{}, 1),
		ctx:     dctx,
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	go s.run()
	return s
}

// Search records query as the latest pending search and wakes the driver. It
// never blocks: repeated calls before the debounce window elapses overwrite
// the pending query, so only the final keystroke's text is searched. A call
// after Close (or ctx cancellation) is a no-op.
func (s *IncrementalSearch) Search(query string) {
	s.mu.Lock()
	s.latest = query
	s.dirty = true
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
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
	s.closeOnce.Do(func() { s.cancel() })
	<-s.done
	return nil
}

// run is the single driver goroutine. It owns the debounce timer and the
// in-flight search's cancel func, so neither needs its own lock.
func (s *IncrementalSearch) run() {
	defer close(s.done)

	timer := time.NewTimer(s.opts.debounce)
	if !timer.Stop() {
		<-timer.C
	}
	armed := false

	var searchCancel context.CancelFunc
	cancelInFlight := func() {
		if searchCancel != nil {
			searchCancel()
			searchCancel = nil
		}
	}

	for {
		select {
		case <-s.ctx.Done():
			timer.Stop()
			cancelInFlight()
			return

		case <-s.wake:
			// (Re)arm the debounce window on every keystroke.
			if armed && !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(s.opts.debounce)
			armed = true

		case <-timer.C:
			armed = false
			s.mu.Lock()
			query := s.latest
			had := s.dirty
			s.dirty = false
			s.mu.Unlock()
			if !had {
				continue
			}
			// A newer dispatch supersedes any in-flight search.
			cancelInFlight()
			epoch := s.epoch.Add(1)
			if len([]rune(query)) < s.opts.minQueryLength {
				// Too short to search: answer immediately, no round-trip.
				s.deliver(epoch, SearchUpdate{Query: query, Hits: []SearchHit{}})
				continue
			}
			sctx, c := context.WithCancel(s.ctx)
			searchCancel = c
			go s.doSearch(sctx, epoch, query)
		}
	}
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
