package security

import "errors"

var ErrUnknownChange = errors.New("security change is outside retained history")

// ChangeStatus reports a proven retained commit. An evicted/unknown ID does not
// prove non-commit; callers must treat it as an indeterminate history boundary.
func (s *Store) ChangeStatus(id [16]byte) (ChangeResult, error) {
	if s == nil || s.faulted.Load() {
		return ChangeResult{}, ErrStoreUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.faulted.Load() {
		return ChangeResult{}, ErrStoreUnavailable
	}
	record, known := s.changes[id]
	if !known {
		return ChangeResult{}, ErrUnknownChange
	}
	return record.result, nil
}
