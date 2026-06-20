package client

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

// fakeIncSearch is a LanternServiceClient stub for the incremental-search
// driver tests. It records every query it is asked to search, optionally
// delays a chosen query so a slower in-flight call can be overtaken, and
// replays per-query canned hits (or a single canned error for every query).
type fakeIncSearch struct {
	graphv1connect.LanternServiceClient
	mu    sync.Mutex
	reqs  []*pb.SearchVerticesRequest
	delay map[string]time.Duration
	hits  map[string][]*pb.SearchHit
	err   error
}

func (f *fakeIncSearch) SearchVertices(ctx context.Context, req *connect.Request[pb.SearchVerticesRequest]) (*connect.Response[pb.SearchVerticesResponse], error) {
	q := req.Msg.GetQuery()
	f.mu.Lock()
	f.reqs = append(f.reqs, req.Msg)
	d := f.delay[q]
	h := f.hits[q]
	e := f.err
	f.mu.Unlock()
	if e != nil {
		return nil, e
	}
	if d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return connect.NewResponse(&pb.SearchVerticesResponse{Hits: h}), nil
}

func (f *fakeIncSearch) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.reqs))
	for i, r := range f.reqs {
		out[i] = r.GetQuery()
	}
	return out
}

func (f *fakeIncSearch) firstReq() *pb.SearchVerticesRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) == 0 {
		return nil
	}
	return f.reqs[0]
}

// newDriver wires a fake client into a *Lantern and returns a started driver
// that is closed on test cleanup.
func newDriver(t *testing.T, fake *fakeIncSearch, opts ...IncrementalSearchOption) *IncrementalSearch {
	t.Helper()
	l := mustLantern(t)
	l.client = fake
	ctx, cancel := context.WithCancel(context.Background())
	is := l.NewIncrementalSearch(ctx, opts...)
	t.Cleanup(func() {
		_ = is.Close()
		cancel()
	})
	return is
}

// waitUpdate reads one SearchUpdate or fails the test on timeout.
func waitUpdate(t *testing.T, is *IncrementalSearch) SearchUpdate {
	t.Helper()
	select {
	case u := <-is.Updates():
		return u
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a SearchUpdate")
		return SearchUpdate{}
	}
}

func TestIncrementalSearchOptions(t *testing.T) {
	t.Run("constants are the documented defaults", func(t *testing.T) {
		if defaultIncrementalDebounce != 150*time.Millisecond {
			t.Errorf("default debounce = %v, want 150ms", defaultIncrementalDebounce)
		}
		if defaultMinQueryLength != 1 {
			t.Errorf("default minQueryLength = %d, want 1", defaultMinQueryLength)
		}
	})

	t.Run("apply", func(t *testing.T) {
		o := incrementalSearchOptions{}
		for _, apply := range []IncrementalSearchOption{
			WithDebounce(7 * time.Millisecond),
			WithIncrementalSearchLimit(9),
			WithIncrementalSearchPrefix("user."),
			WithMinQueryLength(3),
		} {
			apply(&o)
		}
		if o.debounce != 7*time.Millisecond {
			t.Errorf("debounce = %v, want 7ms", o.debounce)
		}
		if o.limit != 9 {
			t.Errorf("limit = %d, want 9", o.limit)
		}
		if o.prefix != "user." {
			t.Errorf("prefix = %q, want %q", o.prefix, "user.")
		}
		if o.minQueryLength != 3 {
			t.Errorf("minQueryLength = %d, want 3", o.minQueryLength)
		}
	})
}

func TestIncrementalSearch_ForwardsAndDelivers(t *testing.T) {
	fake := &fakeIncSearch{hits: map[string][]*pb.SearchHit{
		"hello": {{Key: "user.hi", Score: 1.5}},
	}}
	is := newDriver(t, fake, WithDebounce(5*time.Millisecond),
		WithIncrementalSearchLimit(7), WithIncrementalSearchPrefix("user."))

	is.Search("hello")
	u := waitUpdate(t, is)

	if u.Err != nil {
		t.Fatalf("unexpected err: %v", u.Err)
	}
	if u.Query != "hello" {
		t.Errorf("query = %q, want %q", u.Query, "hello")
	}
	if len(u.Hits) != 1 || u.Hits[0].Key != "user.hi" || u.Hits[0].Score != 1.5 {
		t.Errorf("hits = %+v, want one user.hi/1.5", u.Hits)
	}
	req := fake.firstReq()
	if req == nil {
		t.Fatal("no SearchVertices request recorded")
	}
	if req.GetLimit() != 7 {
		t.Errorf("limit = %d, want 7", req.GetLimit())
	}
	if req.GetPrefix() != "user." {
		t.Errorf("prefix = %q, want %q", req.GetPrefix(), "user.")
	}
}

func TestIncrementalSearch_DebounceCoalesces(t *testing.T) {
	fake := &fakeIncSearch{hits: map[string][]*pb.SearchHit{
		"abc": {{Key: "k", Score: 1}},
	}}
	is := newDriver(t, fake, WithDebounce(30*time.Millisecond))

	is.Search("a")
	is.Search("ab")
	is.Search("abc")

	u := waitUpdate(t, is)
	if u.Query != "abc" {
		t.Errorf("query = %q, want %q", u.Query, "abc")
	}
	// Only the final query of the burst hit the wire.
	if got := fake.calls(); len(got) != 1 || got[0] != "abc" {
		t.Errorf("calls = %v, want [abc]", got)
	}
}

func TestIncrementalSearch_CancelsInFlight(t *testing.T) {
	fake := &fakeIncSearch{
		delay: map[string]time.Duration{"slow": 500 * time.Millisecond},
		hits: map[string][]*pb.SearchHit{
			"slow": {{Key: "slow.hit", Score: 9}},
			"fast": {{Key: "fast.hit", Score: 1}},
		},
	}
	is := newDriver(t, fake, WithDebounce(5*time.Millisecond))

	is.Search("slow")
	// Let "slow" dispatch and enter its 500ms delay before superseding it.
	time.Sleep(60 * time.Millisecond)
	is.Search("fast")

	u := waitUpdate(t, is)
	if u.Query != "fast" {
		t.Fatalf("query = %q, want fast (slow must be superseded)", u.Query)
	}
	if len(u.Hits) != 1 || u.Hits[0].Key != "fast.hit" {
		t.Errorf("hits = %+v, want fast.hit", u.Hits)
	}
	// Both were dispatched; "slow" was cancelled, only "fast" delivered.
	if calls := fake.calls(); len(calls) != 2 {
		t.Fatalf("calls = %v, want both slow and fast dispatched", calls)
	}
}

func TestIncrementalSearch_MinQueryLength(t *testing.T) {
	fake := &fakeIncSearch{}
	is := newDriver(t, fake, WithDebounce(5*time.Millisecond), WithMinQueryLength(3))

	is.Search("ab") // shorter than 3 runes
	u := waitUpdate(t, is)

	if u.Query != "ab" {
		t.Errorf("query = %q, want %q", u.Query, "ab")
	}
	if len(u.Hits) != 0 {
		t.Errorf("hits = %+v, want empty", u.Hits)
	}
	if u.Err != nil {
		t.Errorf("err = %v, want nil", u.Err)
	}
	if got := fake.calls(); len(got) != 0 {
		t.Errorf("calls = %v, want no RPC for a too-short query", got)
	}
}

func TestIncrementalSearch_DeliversError(t *testing.T) {
	fake := &fakeIncSearch{err: connect.NewError(connect.CodeFailedPrecondition,
		errors.New("vertex search is disabled on this server"))}
	is := newDriver(t, fake, WithDebounce(5*time.Millisecond))

	is.Search("anything")
	u := waitUpdate(t, is)

	if !errors.Is(u.Err, ErrFailedPrecondition) {
		t.Fatalf("err = %v, want errors.Is ErrFailedPrecondition", u.Err)
	}
}

func TestIncrementalSearch_CloseIsIdempotent(t *testing.T) {
	fake := &fakeIncSearch{}
	l := mustLantern(t)
	l.client = fake
	is := l.NewIncrementalSearch(context.Background(), WithDebounce(5*time.Millisecond))

	if err := is.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := is.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	// A Search after Close must not panic or block.
	is.Search("ignored")
}
