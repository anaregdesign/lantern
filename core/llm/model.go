package llm

import "context"

// Model is a single-shot, structured-output language model whose output conforms
// to the Go type T. Given a Request, Generate returns a Response whose Output is
// a fully decoded value of T. A Model keeps no state between calls: every
// Generate is independent, with no conversation history.
//
// T is the structured-output schema: the JSON Schema sent to the provider is
// derived from T (see SchemaFor), and the provider's JSON answer is decoded back
// into a T. Binding the type to the interface keeps a backend's input and output
// in lockstep and lets callers work with ordinary Go values instead of raw JSON.
//
// Concrete implementations wrap a specific backend (OpenAI, Anthropic Claude,
// Google Gemini, a local model, ...) — typically a thin generic adapter over a
// shared, non-generic client. Implementations should be safe for concurrent use
// by multiple goroutines and must honor ctx cancellation.
type Model[T any] interface {
	Generate(ctx context.Context, req Request[T]) (Response[T], error)
}

// ModelFunc adapts an ordinary function to the Model interface. It is handy for
// tests and for lightweight, stateless backends.
type ModelFunc[T any] func(ctx context.Context, req Request[T]) (Response[T], error)

// Generate calls f(ctx, req).
func (f ModelFunc[T]) Generate(ctx context.Context, req Request[T]) (Response[T], error) {
	return f(ctx, req)
}

// Request is a single, self-contained generation request for output type T. It
// carries no conversation history.
//
// Instruction is the system-level directive describing the task — the role and
// the "how". Input is the user-provided payload the model should act on — the
// "what". The output schema is not carried here: it is derived from T by the
// model (see SchemaFor). Keeping the directive separate from the payload lets a
// caller reuse one Instruction across many Inputs.
type Request[T any] struct {
	Instruction string
	Input       string
}

// Response is a Model's answer to a Request[T]. Output is the decoded result;
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
