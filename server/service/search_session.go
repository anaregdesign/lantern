package service

import (
	"container/list"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
)

const (
	searchCursorVersion = uint8(1)
	maxSearchCursorSize = 4096
)

var (
	errSearchCursorInvalid = errors.New("search cursor invalid")
	errSearchCursorStale   = errors.New("search cursor stale")
)

// searchSessionStore owns endpoint-sticky, bounded result snapshots. A global
// index generation cursor is intentionally not used: the production churn
// scenario mutates the index hundreds of times per second, which would make a
// strict cursor stale between virtually every pair of pages (#1065).
type searchSessionStore struct {
	mu sync.Mutex

	sessions map[string]*list.Element
	lru      *list.List
	bytes    int64

	ttl      time.Duration
	maxCount int
	maxHits  int
	maxBytes int64

	signingKey          [32]byte
	endpointFingerprint string
	now                 func() time.Time
}

type searchSessionEntry struct {
	id          string
	requestHash [32]byte
	hits        []*pb.SearchHit
	limited     bool
	expiresAt   time.Time
	bytes       int64
}

type searchCursorPayload struct {
	Version             uint8  `json:"v"`
	SessionID           string `json:"s"`
	Offset              int    `json:"o"`
	RequestHash         string `json:"r"`
	ConfigFingerprint   string `json:"c"`
	EndpointFingerprint string `json:"e"`
	ExpiresUnixNano     int64  `json:"x"`
}

func newSearchSessionStore(limits SearchLimits) *searchSessionStore {
	store := &searchSessionStore{
		sessions: make(map[string]*list.Element),
		lru:      list.New(),
		ttl:      limits.CursorTTL,
		maxCount: limits.MaxSessions,
		maxHits:  limits.MaxSessionHits,
		maxBytes: limits.MaxSessionBytes,
		now:      time.Now,
	}
	if _, err := rand.Read(store.signingKey[:]); err != nil {
		// Failure of the OS CSPRNG is unrecoverable for signed cursor issuance.
		// Keep the all-zero key only so the server remains constructible; create
		// refuses to issue sessions when it observes that sentinel.
		store.signingKey = [32]byte{}
	}
	sum := sha256.Sum256(store.signingKey[:])
	store.endpointFingerprint = hex.EncodeToString(sum[:16])
	return store
}

func (s *searchSessionStore) maxRetainedHits() int {
	if s == nil {
		return 0
	}
	return s.maxHits
}

// create retains one complete bounded result snapshot and returns a cursor
// positioned after the already-returned first page. admitted=false means the
// first page remains usable but continuation_limited must be surfaced.
func (s *searchSessionStore) create(requestHash [32]byte, configFingerprint string, hits []*pb.SearchHit, limited bool, firstOffset int) (cursor []byte, admitted bool, err error) {
	if s == nil || firstOffset <= 0 || firstOffset >= len(hits) {
		return nil, false, nil
	}
	if s.signingKey == ([32]byte{}) {
		return nil, false, errors.New("search cursor signing key unavailable")
	}
	owned := cloneSearchHits(hits)
	size := searchSessionSize(owned)
	if len(owned) > s.maxHits || size > s.maxBytes {
		return nil, false, nil
	}
	idBytes := make([]byte, 18)
	if _, err := rand.Read(idBytes); err != nil {
		return nil, false, fmt.Errorf("mint search session id: %w", err)
	}
	now := s.now()
	entry := &searchSessionEntry{
		id:          base64.RawURLEncoding.EncodeToString(idBytes),
		requestHash: requestHash,
		hits:        owned,
		limited:     limited,
		expiresAt:   now.Add(s.ttl),
		bytes:       size,
	}

	s.mu.Lock()
	s.purgeExpiredLocked(now)
	for s.lru.Len() >= s.maxCount || s.bytes+entry.bytes > s.maxBytes {
		if s.lru.Len() == 0 {
			s.mu.Unlock()
			return nil, false, nil
		}
		s.removeElementLocked(s.lru.Back())
	}
	element := s.lru.PushFront(entry)
	s.sessions[entry.id] = element
	s.bytes += entry.bytes
	s.mu.Unlock()

	payload := s.payload(entry, firstOffset, configFingerprint)
	cursor, err = s.encode(payload)
	if err != nil {
		s.remove(entry.id)
		return nil, false, err
	}
	return cursor, true, nil
}

// page validates a continuation before touching the result snapshot, then
// returns owned hit clones so direct in-process callers cannot mutate the LRU.
func (s *searchSessionStore) page(cursor []byte, requestHash [32]byte, configFingerprint string, limit int) (hits []*pb.SearchHit, next []byte, truncated, limited bool, err error) {
	if s == nil || limit <= 0 {
		return nil, nil, false, false, errSearchCursorInvalid
	}
	payload, err := s.decode(cursor)
	if err != nil {
		return nil, nil, false, false, err
	}
	wantHash := base64.RawURLEncoding.EncodeToString(requestHash[:])
	if payload.RequestHash != wantHash || payload.ConfigFingerprint != configFingerprint || payload.EndpointFingerprint != s.endpointFingerprint {
		return nil, nil, false, false, fmt.Errorf("%w: request, configuration, or endpoint mismatch", errSearchCursorInvalid)
	}
	now := s.now()
	if payload.ExpiresUnixNano <= now.UnixNano() {
		s.remove(payload.SessionID)
		return nil, nil, false, false, fmt.Errorf("%w: cursor expired", errSearchCursorStale)
	}

	s.mu.Lock()
	s.purgeExpiredLocked(now)
	element, ok := s.sessions[payload.SessionID]
	if !ok {
		s.mu.Unlock()
		return nil, nil, false, false, fmt.Errorf("%w: session evicted or expired", errSearchCursorStale)
	}
	entry := element.Value.(*searchSessionEntry)
	if entry.requestHash != requestHash || entry.expiresAt.UnixNano() != payload.ExpiresUnixNano || payload.Offset <= 0 || payload.Offset >= len(entry.hits) {
		s.mu.Unlock()
		return nil, nil, false, false, fmt.Errorf("%w: session position mismatch", errSearchCursorInvalid)
	}
	start := payload.Offset
	end := min(start+limit, len(entry.hits))
	hits = cloneSearchHits(entry.hits[start:end])
	limited = entry.limited
	s.lru.MoveToFront(element)
	if end < len(entry.hits) {
		next, err = s.encode(s.payload(entry, end, configFingerprint))
		if err != nil {
			s.mu.Unlock()
			return nil, nil, false, false, err
		}
	}
	truncated = end < len(entry.hits) || entry.limited
	s.mu.Unlock()
	return hits, next, truncated, limited, nil
}

func (s *searchSessionStore) payload(entry *searchSessionEntry, offset int, configFingerprint string) searchCursorPayload {
	return searchCursorPayload{
		Version:             searchCursorVersion,
		SessionID:           entry.id,
		Offset:              offset,
		RequestHash:         base64.RawURLEncoding.EncodeToString(entry.requestHash[:]),
		ConfigFingerprint:   configFingerprint,
		EndpointFingerprint: s.endpointFingerprint,
		ExpiresUnixNano:     entry.expiresAt.UnixNano(),
	}
}

func (s *searchSessionStore) encode(payload searchCursorPayload) ([]byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal search cursor: %w", err)
	}
	mac := hmac.New(sha256.New, s.signingKey[:])
	_, _ = mac.Write(raw)
	signature := mac.Sum(nil)
	encodedPayload := base64.RawURLEncoding.EncodeToString(raw)
	encodedSignature := base64.RawURLEncoding.EncodeToString(signature)
	return []byte(encodedPayload + "." + encodedSignature), nil
}

func (s *searchSessionStore) decode(cursor []byte) (searchCursorPayload, error) {
	if len(cursor) == 0 || len(cursor) > maxSearchCursorSize {
		return searchCursorPayload{}, fmt.Errorf("%w: empty or oversized", errSearchCursorInvalid)
	}
	parts := strings.Split(string(cursor), ".")
	if len(parts) != 2 {
		return searchCursorPayload{}, fmt.Errorf("%w: malformed envelope", errSearchCursorInvalid)
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return searchCursorPayload{}, fmt.Errorf("%w: payload encoding", errSearchCursorInvalid)
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return searchCursorPayload{}, fmt.Errorf("%w: signature encoding", errSearchCursorInvalid)
	}
	mac := hmac.New(sha256.New, s.signingKey[:])
	_, _ = mac.Write(raw)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return searchCursorPayload{}, fmt.Errorf("%w: signature mismatch", errSearchCursorInvalid)
	}
	var payload searchCursorPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		return searchCursorPayload{}, fmt.Errorf("%w: payload JSON", errSearchCursorInvalid)
	}
	if payload.Version != searchCursorVersion || payload.SessionID == "" || payload.RequestHash == "" || payload.EndpointFingerprint == "" {
		return searchCursorPayload{}, fmt.Errorf("%w: unsupported or incomplete payload", errSearchCursorInvalid)
	}
	return payload, nil
}

func (s *searchSessionStore) purgeExpiredLocked(now time.Time) {
	for element := s.lru.Back(); element != nil; {
		previous := element.Prev()
		if !element.Value.(*searchSessionEntry).expiresAt.After(now) {
			s.removeElementLocked(element)
		}
		element = previous
	}
}

func (s *searchSessionStore) remove(id string) {
	s.mu.Lock()
	if element, ok := s.sessions[id]; ok {
		s.removeElementLocked(element)
	}
	s.mu.Unlock()
}

func (s *searchSessionStore) removeElementLocked(element *list.Element) {
	entry := element.Value.(*searchSessionEntry)
	delete(s.sessions, entry.id)
	s.bytes -= entry.bytes
	s.lru.Remove(element)
}

func cloneSearchHits(hits []*pb.SearchHit) []*pb.SearchHit {
	out := make([]*pb.SearchHit, 0, len(hits))
	for _, hit := range hits {
		if hit == nil {
			continue
		}
		out = append(out, proto.Clone(hit).(*pb.SearchHit))
	}
	return out
}

func searchSessionSize(hits []*pb.SearchHit) int64 {
	var size int64
	for _, hit := range hits {
		size += int64(proto.Size(hit)) + 24
	}
	return size
}
