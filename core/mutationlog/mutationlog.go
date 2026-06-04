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
// requests a Seq older than [Log.FirstSeq] the channel is closed and
// [ErrGapped] is reported via the cancel func's returned error; the caller
// is then expected to snapshot and resubscribe (RFC §7). Live subscribers
// share a bounded outbound channel — slow consumers exert back-pressure on
// [Log.Append] rather than allowing the log to drift forward without them.
//
// A [WAL] hook lets a future durability layer (RFC D1) intercept appends
// before they fan out. The default [NopWAL] is a no-op, matching today's
// in-memory-only behaviour.
//
// This package depends only on the standard library and core/hlc.
package mutationlog

import (
	"errors"
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
// [WAL.Write] while holding the log mutex, so implementations should keep
// the call cheap (a buffered write is fine; a synchronous fsync is not).
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
	// At sustained write rates a too-small buffer turns transient scheduling
	// jitter into permanent gap-closes: the fan-out path uses a
	// non-blocking send and closes the subscriber on a full channel
	// (see [Log.Append]). 512 gives ~256 ms of headroom at 2k writes/s,
	// which is well above typical scheduler stalls on a loaded host.
	SubscriberBuffer int
	// WAL receives every appended entry before it fans out to subscribers.
	// Nil defaults to [NopWAL].
	WAL WAL
}

const (
	defaultCapacity         = 1024
	defaultSubscriberBuffer = 512
)

// Log is an append-only, bounded, in-memory mutation log.
//
// The zero value is not ready for use; construct with [New].
type Log struct {
	mu       sync.Mutex
	capacity int
	subBuf   int
	wal      WAL
	// ring is a fixed-size circular buffer allocated once at construction.
	// Valid entries occupy positions [head, head+size) modulo capacity.
	// Append is O(1) at all fill levels; eviction is a single head bump.
	ring        []Entry
	head        int    // index of the oldest entry when size > 0
	size        int    // number of valid entries; 0 <= size <= capacity
	firstSeq    uint64 // seq of ring[head] when size > 0; meaningless otherwise
	lastSeq     uint64 // seq of last appended entry; 0 means none appended yet
	evicted     uint64 // total entries dropped by ring-buffer eviction
	hasEntries  bool
	closed      bool
	subscribers map[*subscription]struct{}
}

// subscription is the live state for one subscriber.
type subscription struct {
	ch     chan Entry
	gapped bool // set when a fan-out drop turned into a gap
}

// New constructs a [Log] with the given options.
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
	return &Log{
		capacity:    capacity,
		subBuf:      subBuf,
		wal:         wal,
		ring:        make([]Entry, capacity),
		subscribers: make(map[*subscription]struct{}),
	}
}

// FirstSeq returns the lowest Seq still resident in the ring buffer. It
// returns 0 (and false) when the log is empty.
func (l *Log) FirstSeq() (uint64, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.hasEntries {
		return 0, false
	}
	return l.firstSeq, true
}

// LastSeq returns the Seq of the most recently appended entry. It returns
// 0 (and false) when the log is empty.
func (l *Log) LastSeq() (uint64, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
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
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.size
}

// Evicted returns the cumulative count of entries dropped from the ring
// buffer because Append at full capacity displaced the oldest entry. The
// counter is monotonic for the lifetime of the Log.
func (l *Log) Evicted() uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.evicted
}

// Append assigns the next sequence number to op, persists the entry through
// the WAL hook, stores it in the ring buffer, and fans it out to every live
// subscriber. The returned [Entry] carries the assigned Seq.
//
// Append is safe for concurrent use; calls are serialised so Seq strictly
// increases by one for each successful return.
func (l *Log) Append(op MutationOp, ts hlc.Timestamp) (Entry, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return Entry{}, ErrClosed
	}
	seq := l.lastSeq + 1
	entry := Entry{Seq: seq, HLC: ts, Op: op}
	if err := l.wal.Write(entry); err != nil {
		return Entry{}, err
	}
	l.storeLocked(entry)
	l.lastSeq = seq
	l.hasEntries = true
	l.fanoutLocked(entry)
	return entry, nil
}

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

// fanoutLocked delivers entry to every subscriber. A subscriber that cannot
// receive without blocking has its channel marked gapped and closed on the
// next replay attempt. Live delivery uses a non-blocking send to preserve
// the back-pressure semantics promised by SubscriberBuffer.
func (l *Log) fanoutLocked(entry Entry) {
	for sub := range l.subscribers {
		if sub.gapped {
			continue
		}
		select {
		case sub.ch <- entry:
		default:
			// Subscriber is too slow: mark gapped and close to signal a
			// reconnect-and-snapshot to the caller.
			sub.gapped = true
			close(sub.ch)
			delete(l.subscribers, sub)
		}
	}
}

// Subscribe returns a channel that receives entries starting at fromSeq.
// Any entries with Seq >= fromSeq still resident in the ring buffer are
// replayed immediately; subsequent entries are delivered live.
//
// If fromSeq is older than [Log.FirstSeq] the returned channel is closed
// immediately and the cancel func reports [ErrGapped] when invoked.
//
// The returned cancel func unregisters the subscriber and closes the
// channel. It is safe to call cancel more than once; subsequent calls
// return nil.
//
// Slow subscribers exert back-pressure: each subscriber has a bounded
// outbound channel (see [Options.SubscriberBuffer]). When that channel
// fills, the subscriber is marked gapped, its channel is closed, and the
// caller must snapshot and resubscribe from a fresh Seq.
func (l *Log) Subscribe(fromSeq uint64) (<-chan Entry, func() error, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil, nil, ErrClosed
	}
	if l.hasEntries && fromSeq < l.firstSeq {
		return nil, nil, ErrGapped
	}
	sub := &subscription{ch: make(chan Entry, l.subBuf)}
	// Replay any in-buffer entries with Seq >= fromSeq. Entries are stored
	// in the circular ring at positions [head, head+size); iterate that
	// active window rather than ranging over the backing slice (which
	// contains stale slots past size).
	for i := 0; i < l.size; i++ {
		e := l.ring[(l.head+i)%l.capacity]
		if e.Seq < fromSeq {
			continue
		}
		select {
		case sub.ch <- e:
		default:
			// Backlog already exceeds the subscriber buffer at registration
			// time: caller asked us to replay more history than they can
			// drain, so surface as a gap and refuse the subscription.
			close(sub.ch)
			return nil, nil, ErrGapped
		}
	}
	l.subscribers[sub] = struct{}{}

	var cancelOnce sync.Once
	cancel := func() error {
		cancelOnce.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			if _, ok := l.subscribers[sub]; ok {
				delete(l.subscribers, sub)
				close(sub.ch)
			}
		})
		return nil
	}
	return sub.ch, cancel, nil
}

// Close stops accepting appends and closes every subscriber channel.
// Calling Close more than once returns nil.
func (l *Log) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	l.closed = true
	for sub := range l.subscribers {
		close(sub.ch)
		delete(l.subscribers, sub)
	}
	return nil
}
