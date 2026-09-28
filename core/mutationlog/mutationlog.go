// Package mutationlog is the single source of truth for the stream of
// graph mutations that flows through Lantern's leaderless full-replica
// replication design.
//
// The log is an in-memory, append-only ring buffer. Each [Entry] carries a
// monotonically increasing [Seq] (assigned atomically by [Log.Append]), an
// [hlc.Timestamp] supplied by the caller, and an opaque [MutationOp]
// payload. Keeping the payload as an interface keeps this package proto-free
// so it can be reused both by internal replication (#181, #184) and by
// external CDC (#180) without a circular dependency on pb/.
//
// A bounded number of entries are retained for replay. When a subscriber
// requests a Seq older than [Log.FirstSeq] that entry has already been
// evicted, so [Log.Subscribe] returns [ErrGapped] and the caller is then
// expected to snapshot and resubscribe (RFC §7). The replay window is
// delivered ahead of live traffic without being bounded by the subscriber
// buffer. Writes arriving during replay are caught up from the retained
// ring before the bounded live tail is registered; an actual eviction still
// gaps the subscriber (see [Log.Subscribe]).
//
// A [WAL] hook lets a future durability layer (RFC D1) intercept appends
// before they fan out. The default [NopWAL] is a no-op, matching today's
// in-memory-only behaviour.
//
// This package depends only on the standard library and core/hlc.
package mutationlog

import (
	"errors"
	"math"
	"sync"

	"github.com/anaregdesign/lantern/core/hlc"
)

// ErrGapped is returned to a subscriber when its requested fromSeq has
// already been evicted from the ring buffer. Callers must snapshot and
// resubscribe from a fresh sequence number.
var ErrGapped = errors.New("mutationlog: requested sequence has been evicted")

// ErrClosed is returned by [Log.Append] and [Log.Subscribe] after [Log.Close]
// has been called.
var ErrClosed = errors.New("mutationlog: log is closed")

// MutationOp is an opaque payload carried by an [Entry]. The mutationlog
// package never inspects it; callers (proto encoders, applicators, CDC
// emitters) own the concrete types.
type MutationOp any

// Entry is a single durable record in the log.
type Entry struct {
	// Seq is the log-local position of the entry. It increases by exactly
	// one with each successful [Log.Append] call within a single [Log].
	Seq uint64
	// HLC is the Hybrid Logical Clock stamp supplied by the caller at
	// append time. Different origins may produce entries whose HLC ordering
	// disagrees with the local Seq ordering; that is by design.
	HLC hlc.Timestamp
	// Op is the opaque mutation payload.
	Op MutationOp
}

// WAL is the hook surface for a future write-ahead-log implementation.
// Implementations must be safe for concurrent use. [Log.Append] calls
// [WAL.Write] while holding the log mutex. The legacy Append path permits a
// buffered write and does not provide crash durability. By contrast,
// [Log.CommitWithPublication] requires a nil Write result to mean the entry
// is recoverable at the WAL's configured durability boundary before the
// callback runs; a durable WAL may need a synchronous flush for that claim.
// [NopWAL] provides only in-process ordering, with no crash recovery.
type WAL interface {
	Write(Entry) error
}

// NopWAL is a WAL that discards every entry. It is the default.
type NopWAL struct{}

// Write implements [WAL].
func (NopWAL) Write(Entry) error { return nil }

// Options configures a [Log].
//
// A zero Options is valid: Capacity defaults to 1024 entries and WAL
// defaults to [NopWAL]. SubscriberBuffer defaults to 512.
type Options struct {
	// Capacity is the maximum number of entries retained in the ring buffer
	// for replay. Must be > 0; defaults to 1024 when zero.
	Capacity int
	// SubscriberBuffer is the per-subscriber outbound channel size. Smaller
	// values increase back-pressure sensitivity; defaults to 512 when zero.
	//
	// After replay catches up, fan-out uses a non-blocking send and closes
	// a slow live subscriber on a full channel (see [Log.Append]). During
	// replay, new entries remain in the retained ring instead. 512 gives
	// ~256 ms of live headroom at 2k writes/s.
	SubscriberBuffer int
	// WAL receives every appended entry before it fans out to subscribers.
	// Nil defaults to [NopWAL].
	WAL WAL
	// OnDrop, when non-nil, is invoked synchronously from the dispatcher
	// goroutine each time a live fan-out drops an entry to a subscriber.
	// The cause argument is one of the DropCause* constants in this
	// package. Implementations must not block (they run on the hot
	// fan-out path) and must not call back into the [Log] — the
	// dispatcher holds the subscriber mutex at the call site (#260).
	OnDrop func(cause string)
}

const (
	defaultCapacity         = 1024
	defaultSubscriberBuffer = 512
)

// Drop-cause labels passed to [Options.OnDrop]. New causes may be added
// in future revisions; consumers should treat unknown values as opaque
// strings rather than enumerate (#260).
const (
	// DropCauseBufferFull is reported when the dispatcher's non-blocking
	// send to a subscriber's outbound channel fails because the channel
	// is full. The subscriber is marked gapped, its channel is closed,
	// and it is unregistered from the log.
	DropCauseBufferFull = "buffer_full"
)

// Log is an append-only, bounded, in-memory mutation log.
//
// The zero value is not ready for use; construct with [New].
//
// Locking model (#260, #745): two mutexes split the hot path so [Log.Append]
// no longer iterates subscribers under its own critical section.
//
//   - mu  is an RWMutex protecting the ring buffer, sequence counters, and
//     the closed flag. The mutation paths take the write lock: Append (WAL
//     write + store + seq update + handoff to the dispatcher), Subscribe
//     (replay + sub registration), and Close. The pure-read status methods
//     (FirstSeq, LastSeq, Len, Evicted) take only the read lock, so a burst
//     of status polling no longer serializes against itself and proceeds
//     concurrently whenever no append/subscribe is mid-flight (#745).
//   - subsMu protects the subscribers map and per-subscriber state.
//     Held by the dispatcher goroutine during fan-out and by cancel.
//
// Lock order when both are taken: mu → subsMu. The dispatcher only takes
// subsMu, and Append only takes mu (the channel send happens under mu to
// preserve global Seq ordering), so the two paths cannot deadlock.
type Log struct {
	mu       sync.RWMutex
	capacity int
	subBuf   int
	wal      WAL
	// ring is a fixed-size circular buffer allocated once at construction.
	// Valid entries occupy positions [head, head+size) modulo capacity.
	// Append is O(1) at all fill levels; eviction is a single head bump.
	ring       []Entry
	head       int    // index of the oldest entry when size > 0
	size       int    // number of valid entries; 0 <= size <= capacity
	firstSeq   uint64 // seq of ring[head] when size > 0; meaningless otherwise
	lastSeq    uint64 // seq of last appended entry; 0 means none appended yet
	evicted    uint64 // total entries dropped by ring-buffer eviction
	hasEntries bool
	closed     bool
	// unusableErr records an indeterminate WAL failure or an interrupted
	// publication after WAL success. The next seq may already exist in
	// durable storage, so neither Append nor CommitWithPublication may reuse it.
	unusableErr error
	// legacyWALUncertain tracks an unclassified error from the older Append
	// path. Append keeps its retry semantics, but a receipt-capable commit
	// cannot prove a unique durable history after such an error.
	legacyWALUncertain bool

	// Dispatcher pipeline (#260). Append hands entries off to a single
	// background goroutine that performs the per-subscriber fan-out, so
	// writer latency is independent of subscriber count.
	dispatch       chan Entry
	dispatcherDone chan struct{}
	onDrop         func(cause string)

	subsMu      sync.Mutex
	subscribers map[*subscription]struct{}
}

// subscription is the live state for one subscriber.
type subscription struct {
	ch chan Entry
	// startSeq is the first Seq the dispatcher may deliver. Earlier entries
	// belong to retained replay/catch-up windows and must not be duplicated.
	startSeq uint64
	filter   func(Entry) bool
	gapped   bool // set when a fan-out drop turned into a gap
}

// New constructs a [Log] with the given options. It also starts a
// background dispatcher goroutine; call [Log.Close] to stop it.
func New(opts Options) *Log {
	capacity := opts.Capacity
	if capacity <= 0 {
		capacity = defaultCapacity
	}
	subBuf := opts.SubscriberBuffer
	if subBuf <= 0 {
		subBuf = defaultSubscriberBuffer
	}
	wal := opts.WAL
	if wal == nil {
		wal = NopWAL{}
	}
	l := &Log{
		capacity:    capacity,
		subBuf:      subBuf,
		wal:         wal,
		ring:        make([]Entry, capacity),
		subscribers: make(map[*subscription]struct{}),
		// Inbox sized to SubscriberBuffer mirrors the headroom budget
		// documented on Options.SubscriberBuffer: a dispatcher stall of
		// that many entries is tolerated before Append blocks waiting
		// for the dispatcher to drain.
		dispatch:       make(chan Entry, subBuf),
		dispatcherDone: make(chan struct{}),
		onDrop:         opts.OnDrop,
	}
	go l.dispatcher()
	return l
}

// FirstSeq returns the lowest Seq still resident in the ring buffer. It
// returns 0 (and false) when the log is empty.
func (l *Log) FirstSeq() (uint64, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if !l.hasEntries {
		return 0, false
	}
	return l.firstSeq, true
}

// LastSeq returns the Seq of the most recently appended entry. It returns
// 0 (and false) when the log is empty.
func (l *Log) LastSeq() (uint64, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if !l.hasEntries {
		return 0, false
	}
	return l.lastSeq, true
}

// Cap returns the configured ring-buffer capacity.
func (l *Log) Cap() int {
	// capacity is set once at construction; no lock needed.
	return l.capacity
}

// Len returns the number of entries currently resident in the ring buffer.
// 0 <= Len() <= Cap().
func (l *Log) Len() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.size
}

// Evicted returns the cumulative count of entries dropped from the ring
// buffer because Append at full capacity displaced the oldest entry. The
// counter is monotonic for the lifetime of the Log.
func (l *Log) Evicted() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.evicted
}

// RetainedEntries returns the ring's current entries in replica-local seq
// order. The slice is owned by the caller; each Entry.Op remains the original
// immutable payload. Subscribe admission uses this bounded view to determine
// whether a portable per-origin cursor needs an already-evicted mutation.
func (l *Log) RetainedEntries() []Entry {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]Entry, l.size)
	for i := range out {
		out[i] = l.ring[(l.head+i)%l.capacity]
	}
	return out
}

// Append assigns the next sequence number to op, persists the entry through
// the WAL hook, stores it in the ring buffer, and hands it off to the
// dispatcher goroutine which performs per-subscriber fan-out. The
// returned [Entry] carries the assigned Seq.
//
// Append is safe for concurrent use; calls are serialised so Seq strictly
// increases by one for each successful return. The hand-off to the
// dispatcher happens under the log mutex so the dispatcher observes
// entries in strict Seq order (#260); under sustained overload Append
// will block briefly waiting for the dispatcher to drain its inbox.
//
// The optional last argument is a SeqStamper: when non-nil it is
// invoked with the assigned seq while [Log] still holds its lock and
// before the entry is placed into the ring or handed to the
// dispatcher. This is the seam that lets producers stamp the
// originating-writer's seq onto the in-flight payload (typically
// `pb.Mutation.Seq`) without racing the dispatcher, which would
// otherwise read the payload concurrently as it fans out to
// subscribers. The leaderless Subscribe contract (#415) keys per-hop
// dedup on (origin, origin_seq), so this stamping is mandatory for
// any payload that re-enters the system via Subscribe relay.
//
// This legacy append path assumes a failed WAL.Write definitely aborted and
// permits retry with the same seq. A real WAL failure may be indeterminate,
// so Append is not a receipt-safe commit seam. New receipt-capable writes
// must use [Log.CommitWithPublication], which fails closed on that ambiguity.
func (l *Log) Append(op MutationOp, ts hlc.Timestamp, stampers ...SeqStamper) (Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.writeReadyLocked(); err != nil {
		return Entry{}, err
	}
	seq := l.lastSeq + 1
	for _, s := range stampers {
		if s != nil {
			s(seq)
		}
	}
	entry := Entry{Seq: seq, HLC: ts, Op: op}
	if err := l.wal.Write(entry); err != nil {
		var aborted *DefiniteWALAbort
		if !errors.As(err, &aborted) {
			l.legacyWALUncertain = true
		}
		return Entry{}, err
	}
	l.storeLocked(entry)
	l.lastSeq = seq
	l.hasEntries = true
	// Hand-off to dispatcher. The send is under l.mu so concurrent
	// Append calls cannot reorder entries on the inbox channel; the
	// dispatcher consumes single-threaded so subscribers observe Seq
	// in strictly increasing order. If the inbox is full Append blocks
	// here — this is the writer-side back-pressure that protects the
	// dispatcher from runaway producers.
	l.dispatch <- entry
	return entry, nil
}

// SeqStamper is invoked synchronously by [Log.Append] with the
// freshly-assigned seq while the log mutex is still held. Implementers
// typically write the seq onto a field of the in-flight payload so
// that subsequent readers (notably the dispatcher fanning the entry
// out to subscribers) observe a payload whose own seq matches the
// log entry's seq. The stamper MUST NOT block and MUST NOT call back
// into the Log.
type SeqStamper func(seq uint64)

// storeLocked inserts entry into the circular ring buffer. When the ring
// is at capacity the oldest entry is overwritten in place and head is
// advanced by one slot; this keeps Append at O(1) regardless of fill
// level. See issue #252 for the regression that prompted the rewrite.
//
// The caller must hold l.mu.
func (l *Log) storeLocked(entry Entry) {
	if l.size < l.capacity {
		if l.size == 0 {
			l.firstSeq = entry.Seq
		}
		l.ring[(l.head+l.size)%l.capacity] = entry
		l.size++
		return
	}
	// Capacity reached: overwrite the slot at head, advance head, and
	// recompute firstSeq from the new head position.
	l.ring[l.head] = entry
	l.head = (l.head + 1) % l.capacity
	l.firstSeq = l.ring[l.head].Seq
	l.evicted++
}

// dispatcher consumes entries handed off by [Log.Append] and fans each
// one out to every live subscriber. It runs on its own goroutine started
// by [New] and exits when [Log.Close] closes the inbox channel; the
// exit path closes any subscribers still registered so callers blocked
// in <-ch observe channel closure (#260).
func (l *Log) dispatcher() {
	defer close(l.dispatcherDone)
	for entry := range l.dispatch {
		l.fanout(entry)
	}
	// Inbox closed by Close. Drain any subscribers that haven't been
	// cancelled. Close holds l.mu before closing the inbox so no Append
	// can race with us here.
	l.subsMu.Lock()
	defer l.subsMu.Unlock()
	for sub := range l.subscribers {
		close(sub.ch)
		delete(l.subscribers, sub)
	}
}

// fanout delivers entry to every live subscriber registered at the time of
// the call. A subscriber whose outbound channel is full is marked gapped,
// has its channel closed, and is unregistered; the optional
// [Options.OnDrop] hook is then invoked with [DropCauseBufferFull].
//
// Live delivery uses a non-blocking send to preserve the back-pressure
// semantics promised by [Options.SubscriberBuffer]: a slow subscriber
// loses its subscription rather than stalling fan-out for everyone else.
//
// fanout runs exclusively on the dispatcher goroutine, so the only
// other goroutines that contend on subsMu are Subscribe (briefly, to
// register) and the per-subscription cancel func.
func (l *Log) fanout(entry Entry) {
	l.subsMu.Lock()
	defer l.subsMu.Unlock()
	for sub := range l.subscribers {
		if sub.gapped {
			continue
		}
		// Entries below startSeq were already delivered as part of the
		// replay window during Subscribe; skip them to avoid duplicate
		// delivery (#260).
		if entry.Seq < sub.startSeq {
			continue
		}
		if sub.filter != nil && !sub.filter(entry) {
			continue
		}
		select {
		case sub.ch <- entry:
		default:
			sub.gapped = true
			close(sub.ch)
			delete(l.subscribers, sub)
			if l.onDrop != nil {
				l.onDrop(DropCauseBufferFull)
			}
		}
	}
}

// Subscribe returns a channel that receives entries starting at fromSeq.
// Any entries with Seq >= fromSeq still resident in the ring buffer are
// replayed first, in order, followed by live entries.
//
// If fromSeq is older than [Log.FirstSeq] the entry has already been
// evicted and Subscribe returns [ErrGapped]; the caller must snapshot and
// resubscribe from a fresh sequence number.
//
// The returned cancel func unregisters the subscriber and closes the
// channel. It is safe to call cancel more than once; subsequent calls
// return nil.
//
// Replay vs. back-pressure (#812): a forwarder delivers retained entries
// with a blocking send, then catches up from the ring in bounded windows.
// Registering the live subscriber before replay drains would fill its small
// buffer even while the complete tail is retained. The switch to live
// delivery is atomic with the last ring check; actual eviction still closes
// the channel as a gap. Once live, slow consumers remain subject to the
// SubscriberBuffer limit and drop rather than blocking writers.
func (l *Log) Subscribe(fromSeq uint64) (<-chan Entry, func() error, error) {
	return l.SubscribeFiltered(fromSeq, nil)
}

// SubscribeFiltered applies filter before retained replay and live fan-out,
// so excluded entries cannot fill a live subscriber's outbound buffer.
// A nil filter includes every entry. The filter must be non-blocking and safe
// to invoke from the dispatcher; it must not call back into this Log. Even
// filtered entries advance the log-local replay position, and eviction still
// closes a subscriber whose replay tail cannot be proven.
func (l *Log) SubscribeFiltered(fromSeq uint64, filter func(Entry) bool) (<-chan Entry, func() error, error) {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil, nil, ErrClosed
	}
	if l.unusableErr != nil {
		l.mu.Unlock()
		return nil, nil, l.unusableErr
	}
	if l.hasEntries && fromSeq < l.firstSeq {
		l.mu.Unlock()
		return nil, nil, ErrGapped
	}
	// Each replay window is copied under mu. A subsequent window may have
	// been evicted while the previous one was delivered; detect that gap
	// before sending any later entries.
	replay := l.retainedFromLocked(fromSeq)
	next := fromSeq
	exhausted := false
	if len(replay) != 0 {
		last := replay[len(replay)-1].Seq
		exhausted = last == math.MaxUint64
		if !exhausted {
			next = last + 1
		}
	}
	live := &subscription{
		ch:       make(chan Entry, l.subBuf),
		startSeq: l.lastSeq + 1,
		filter:   filter,
	}
	out := make(chan Entry)
	done := make(chan struct{})
	registered := len(replay) == 0
	if registered {
		// Lock order is mu → subsMu. Register before releasing mu so
		// no new entry can slip between an empty replay and the live tail.
		l.subsMu.Lock()
		l.subscribers[live] = struct{}{}
		l.subsMu.Unlock()
	}
	l.mu.Unlock()

	go func() {
		defer close(out)
		for {
			for _, e := range replay {
				if filter != nil && !filter(e) {
					continue
				}
				select {
				case out <- e:
				case <-done:
					return
				}
			}
			if registered {
				for {
					select {
					case e, ok := <-live.ch:
						if !ok {
							return
						}
						select {
						case out <- e:
						case <-done:
							return
						}
					case <-done:
						return
					}
				}
			}

			l.mu.Lock()
			if l.closed || l.unusableErr != nil || (!exhausted && l.hasEntries && next < l.firstSeq) {
				l.mu.Unlock()
				return
			}
			if exhausted {
				l.mu.Unlock()
				// No subsequent sequence is representable. Do not register
				// a live subscriber that could redeliver the queued max entry.
				select {
				case <-done:
				case <-l.dispatcherDone:
				}
				return
			}
			replay = l.retainedFromLocked(next)
			if len(replay) != 0 {
				last := replay[len(replay)-1].Seq
				exhausted = last == math.MaxUint64
				if !exhausted {
					next = last + 1
				}
			} else {
				l.subsMu.Lock()
				select {
				case <-done:
					l.subsMu.Unlock()
					l.mu.Unlock()
					return
				default:
					live.startSeq = l.lastSeq + 1
					l.subscribers[live] = struct{}{}
					registered = true
				}
				l.subsMu.Unlock()
			}
			l.mu.Unlock()
		}
	}()

	var cancelOnce sync.Once
	cancel := func() error {
		cancelOnce.Do(func() {
			// Stop the forwarder even if it is blocked on out <- e, then
			// unregister + close the live channel (guarded by membership so
			// we never double-close against a concurrent dispatcher gap).
			close(done)
			l.subsMu.Lock()
			defer l.subsMu.Unlock()
			if _, ok := l.subscribers[live]; ok {
				delete(l.subscribers, live)
				close(live.ch)
			}
		})
		return nil
	}
	return out, cancel, nil
}

// retainedFromLocked copies one bounded ring window in sequence order.
// The caller holds mu while checking the gap and choosing the live boundary.
func (l *Log) retainedFromLocked(fromSeq uint64) []Entry {
	if l.size == 0 || fromSeq > l.lastSeq {
		return nil
	}
	start := 0
	if fromSeq > l.firstSeq {
		start = int(fromSeq - l.firstSeq)
	}
	entries := make([]Entry, l.size-start)
	for i := range entries {
		entries[i] = l.ring[(l.head+start+i)%l.capacity]
	}
	return entries
}

// Close stops accepting appends, signals the dispatcher to drain, and
// waits for it to exit. The dispatcher closes every still-live
// subscriber channel on its way out. Calling Close more than once
// returns nil.
func (l *Log) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	// Close the inbox while holding l.mu: this guarantees no concurrent
	// Append can be in the middle of `l.dispatch <- entry` (Append
	// holds l.mu across the send), so we cannot panic on send to a
	// closed channel.
	close(l.dispatch)
	l.mu.Unlock()
	<-l.dispatcherDone
	return nil
}
