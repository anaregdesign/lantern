// Package llm defines backend-agnostic abstractions for single-shot,
// structured-output language models.
//
// The interaction model is deliberately narrow: there is no conversation state.
// A caller describes the desired output as a Go type T, supplies an Instruction
// (what the model should do) and an Input (the payload to act on), and receives
// a Response[T] whose Output is a decoded value of T. This single turn maps
// cleanly onto the structured-output / JSON-mode features of the major providers
// (OpenAI, Anthropic Claude, Google Gemini, ...), each of which is expected to
// land later as a concrete Model implementation behind the interface defined
// here.
//
// The central abstraction is the generic Model[T] interface, paired with
// Request[T] and Response[T]. The JSON Schema describing T is derived from the
// Go type by SchemaFor, so callers rarely write schema documents by hand.
// Alongside the decoded Output, Response[T] carries the metadata that backends
// report in common — token Usage, a normalized FinishReason, and the resolved
// Model identifier.
//
// This package is a leaf: it depends on nothing beyond the standard library, so
// concrete backends (which need provider SDKs or HTTP clients) belong in their
// own subpackages or modules that import this one, keeping the abstraction
// dependency-free.
package llm
