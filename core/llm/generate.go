package llm

import "context"

// Generate is a convenience wrapper around a Model[T]: it builds a Request[T]
// from instruction and input and forwards it to m. T is inferred from m, so a
// caller need not spell it out. Construct a Request[T] and call Model.Generate
// directly when more control is needed.
func Generate[T any](ctx context.Context, m Model[T], instruction, input string) (Response[T], error) {
	return m.Generate(ctx, Request[T]{Instruction: instruction, Input: input})
}
