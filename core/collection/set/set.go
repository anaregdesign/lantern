package set

import (
	"github.com/anaregdesign/lantern/core/model/function"
	"sync"
)

type Set[T comparable] struct {
	mu  sync.RWMutex
	set map[T]struct{}
}

func NewSet[T comparable]() *Set[T] {
	return &Set[T]{
		set: make(map[T]struct{}),
	}
}

func (s *Set[T]) Add(value T) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.set[value] = struct{}{}
}

func (s *Set[T]) Remove(value T) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.set, value)
}

func (s *Set[T]) Has(value T) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, ok := s.set[value]
	return ok
}

func (s *Set[T]) Size() int {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return len(s.set)
}

func (s *Set[T]) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.set = make(map[T]struct{})
}

func (s *Set[T]) Values() []T {
	s.mu.RLock()
	defer s.mu.RUnlock()

	values := make([]T, 0, len(s.set))
	for k, _ := range s.set {
		values = append(values, k)
	}
	return values
}

func (s *Set[T]) ForEach(consumer function.Consumer[T]) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for value := range s.set {
		consumer(value)
	}
}

func (s *Set[T]) Filter(predicate function.Predicate[T]) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for k, _ := range s.set {
		if !predicate(k) {
			delete(s.set, k)
		}
	}
}

func (s *Set[T]) Reduce(operator function.Operator[T]) T {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result T
	for value := range s.set {
		result = operator(result, value)
	}
	return result
}

func (s *Set[T]) AnyMatch(predicate function.Predicate[T]) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for value := range s.set {
		if predicate(value) {
			return true
		}
	}
	return false
}

func (s *Set[T]) AllMatch(predicate function.Predicate[T]) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for value := range s.set {
		if !predicate(value) {
			return false
		}
	}
	return true
}

func (s *Set[T]) NoneMatch(predicate function.Predicate[T]) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for value := range s.set {
		if predicate(value) {
			return false
		}
	}
	return true
}
