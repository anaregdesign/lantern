package function

type Consumer[T any] func(T)
type Supplier[T any] func() T
type Predicate[T any] func(T) bool
type Function[T, R any] func(T) R
type Operator[T any] func(T, T) T

type Loader[S comparable, T any] func(S) (T, bool)
