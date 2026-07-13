package service

import (
	"crypto/sha256"
	"errors"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestSearchSessionStore(t *testing.T) {
	newStore := func(maxCount, maxHits int, maxBytes int64) (*searchSessionStore, *time.Time) {
		limits := SearchLimits{
			CursorTTL:       time.Minute,
			MaxSessions:     maxCount,
			MaxSessionHits:  maxHits,
			MaxSessionBytes: maxBytes,
		}
		store := newSearchSessionStore(limits)
		now := time.Unix(10_000, 0)
		store.now = func() time.Time { return now }
		return store, &now
	}
	hits := func(n int) []*pb.SearchHit {
		out := make([]*pb.SearchHit, 0, n)
		for i := range n {
			out = append(out, &pb.SearchHit{Key: string(rune('a' + i)), Score: float64(n - i)})
		}
		return out
	}

	t.Run("PagesOwnedSnapshotAndKeepsTailReplayable", func(t *testing.T) {
		store, _ := newStore(4, 10, 1<<20)
		hash := sha256.Sum256([]byte("request"))
		cursor, admitted, err := store.create(hash, "config", hits(5), false, 2)
		if err != nil || !admitted || len(cursor) == 0 {
			t.Fatalf("create = cursor:%d admitted:%t err:%v", len(cursor), admitted, err)
		}
		page, next, truncated, limited, err := store.page(cursor, hash, "config", 2)
		if err != nil || len(page) != 2 || page[0].GetKey() != "c" || page[1].GetKey() != "d" || len(next) == 0 || !truncated || limited {
			t.Fatalf("middle page = hits:%+v next:%d truncated:%t limited:%t err:%v", page, len(next), truncated, limited, err)
		}
		page[0].Key = "mutated"
		tail, final, truncated, limited, err := store.page(next, hash, "config", 2)
		if err != nil || len(tail) != 1 || tail[0].GetKey() != "e" || len(final) != 0 || truncated || limited {
			t.Fatalf("tail page = hits:%+v next:%d truncated:%t limited:%t err:%v", tail, len(final), truncated, limited, err)
		}
		replayed, _, _, _, replayErr := store.page(next, hash, "config", 2)
		if replayErr != nil || len(replayed) != 1 || replayed[0].GetKey() != "e" {
			t.Fatalf("tail replay = hits:%+v err:%v", replayed, replayErr)
		}
		if store.lru.Len() != 1 || len(store.sessions) != 1 || store.bytes == 0 {
			t.Fatalf("tail session retention: lru=%d sessions=%d bytes=%d", store.lru.Len(), len(store.sessions), store.bytes)
		}
	})

	t.Run("RejectsTamperAndRequestOrConfigMismatch", func(t *testing.T) {
		store, _ := newStore(4, 10, 1<<20)
		hash := sha256.Sum256([]byte("request"))
		cursor, admitted, err := store.create(hash, "config", hits(3), false, 1)
		if err != nil || !admitted {
			t.Fatal(err)
		}
		tampered := append([]byte(nil), cursor...)
		tampered[len(tampered)/2] ^= 1
		if _, _, _, _, err := store.page(tampered, hash, "config", 1); !errors.Is(err, errSearchCursorInvalid) {
			t.Fatalf("tamper err = %v", err)
		}
		other := sha256.Sum256([]byte("other"))
		if _, _, _, _, err := store.page(cursor, other, "config", 1); !errors.Is(err, errSearchCursorInvalid) {
			t.Fatalf("request mismatch err = %v", err)
		}
		if _, _, _, _, err := store.page(cursor, hash, "other-config", 1); !errors.Is(err, errSearchCursorInvalid) {
			t.Fatalf("config mismatch err = %v", err)
		}
	})

	t.Run("ExpiredAndEvictedAreTypedStale", func(t *testing.T) {
		store, now := newStore(1, 10, 1<<20)
		firstHash := sha256.Sum256([]byte("first"))
		first, admitted, err := store.create(firstHash, "config", hits(3), false, 1)
		if err != nil || !admitted {
			t.Fatal(err)
		}
		secondHash := sha256.Sum256([]byte("second"))
		if _, admitted, err := store.create(secondHash, "config", hits(3), false, 1); err != nil || !admitted {
			t.Fatal(err)
		}
		if _, _, _, _, err := store.page(first, firstHash, "config", 1); !errors.Is(err, errSearchCursorStale) {
			t.Fatalf("evicted err = %v", err)
		}
		expiring, admitted, err := store.create(firstHash, "config", hits(3), false, 1)
		if err != nil || !admitted {
			t.Fatal(err)
		}
		*now = now.Add(time.Minute - time.Nanosecond)
		if _, _, _, _, err := store.page(expiring, firstHash, "config", 1); err != nil {
			t.Fatalf("cursor expired before nanosecond deadline: %v", err)
		}
		*now = now.Add(time.Nanosecond)
		if _, _, _, _, err := store.page(expiring, firstHash, "config", 1); !errors.Is(err, errSearchCursorStale) {
			t.Fatalf("expired err = %v", err)
		}
	})

	t.Run("OversizedSnapshotIsNotAdmitted", func(t *testing.T) {
		store, _ := newStore(4, 10, 1)
		hash := sha256.Sum256([]byte("request"))
		cursor, admitted, err := store.create(hash, "config", hits(3), true, 1)
		if err != nil || admitted || len(cursor) != 0 {
			t.Fatalf("create oversized = cursor:%d admitted:%t err:%v", len(cursor), admitted, err)
		}
	})
}

func FuzzSearchCursorDecode(f *testing.F) {
	store := newSearchSessionStore(SearchLimits{
		CursorTTL:       time.Minute,
		MaxSessions:     2,
		MaxSessionHits:  10,
		MaxSessionBytes: 1 << 20,
	})
	f.Add([]byte("not-a-cursor"))
	f.Fuzz(func(t *testing.T, cursor []byte) {
		_, _ = store.decode(cursor)
	})
}
