package llm

import "context"

// Model is a single-shot, structured-output language model whose output conforms
// to the Go type T. Given an input, Generate returns a Response whose Output is
// a fully decoded value of T. A Model keeps no state between calls: every
// Generate is independent, with no conversation history.
//
// T is the structured-output schema: the JSON Schema sent to the provider is
// derived from T (see SchemaFor), and the provider's JSON answer is decoded back
// into a T. The system instruction is fixed when the Model is built, so Generate
// takes only the input to act on. Instruction and schema are the two halves of
// one task definition and are confirmed together, at construction: binding T to
// the interface forces the schema there, and the instruction lives with the
// model rather than the input (Go methods cannot take type parameters).
//
// Concrete implementations wrap a specific backend (OpenAI, Anthropic Claude,
// Google Gemini, a local model, ...) — typically a thin generic adapter over a
// shared, non-generic client that captures the instruction. Implementations
// should be safe for concurrent use by multiple goroutines and must honor ctx
// cancellation.
type Model[T any] interface {
	Generate(ctx context.Context, input string) (Response[T], error)
}

// ModelFunc adapts an ordinary function to the Model interface; capture the
// instruction in the closure. Handy for tests and lightweight, stateless backends.
type ModelFunc[T any] func(ctx context.Context, input string) (Response[T], error)

// Generate calls f(ctx, input).
func (f ModelFunc[T]) Generate(ctx context.Context, input string) (Response[T], error) {
	return f(ctx, input)
}

// Response is a Model's answer to an input. Output is the decoded result;
// the remaining fields carry the metadata that both OpenAI and Gemini report
// alongside the content.
//
// Output is the structured result, already decoded into T. Usage reports token
// consumption. FinishReason is the normalized reason generation stopped. Model
// is the provider's resolved model/version identifier (OpenAI "model", Gemini
// "modelVersion").
type Response[T any] struct {
	Output       T
	Usage        Usage
	FinishReason FinishReason
	Model        string
}
