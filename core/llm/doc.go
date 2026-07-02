// Package llm defines backend-agnostic abstractions for single-shot,
// structured-output language models.
//
// The interaction model is deliberately narrow: there is no conversation state.
// A caller describes the desired output as a Go type T, fixes the model's system
// instruction at construction, supplies an Input (the payload to act on), and
// receives a Response[T] whose Output is a decoded value of T. This single turn maps
// cleanly onto the structured-output / JSON-mode features of the major providers
// (OpenAI, Anthropic Claude, Google Gemini, ...), each of which is expected to
// land later as a concrete Model implementation behind the interface defined
// here.
//
// The central abstraction is the generic Model[T] interface, paired with
// Response[T]. The JSON Schema describing T is derived from the Go type by
// SchemaFor, so callers rarely write schema documents by hand. The system
// instruction is fixed when the model is built, so callers supply only an input.
// Alongside the decoded Output, Response[T] carries the metadata that backends
// report in common — token Usage, a normalized FinishReason, and the resolved
// Model identifier.
//
// This package is a leaf: it depends on nothing beyond the standard library, so
// concrete backends (which need provider SDKs or HTTP clients) belong in their
// own subpackages or modules that import this one, keeping the abstraction
// dependency-free.
//
// # Error handling
//
// Provider HTTP failures carry a shared, errors.Is-able taxonomy (#852) so
// callers can build retry loops and circuit breakers without string-matching
// provider text:
//
//	ErrRateLimited   429; retry after backing off (APIError.RetryAfter when sent)
//	ErrUnavailable   5xx (incl. Anthropic 529), 408, 409; transient, retry
//	ErrUnauthorized  401/403; credential problem, do not retry
//	ErrBadRequest    other 4xx; deterministic request problem, do not retry
//
// errors.As(&APIError{}) recovers the provider name, status code, Retry-After
// hint, and a body excerpt bounded by ErrBodyLimit. Separately, ErrTruncated
// marks a Generate whose structured output was cut by the max-token cap —
// raise the cap rather than retrying. Refusals and safety blocks remain
// provider-typed (anthropic.ErrRefusal, openai.ErrRefusal, gemini.ErrBlocked)
// because their semantics differ per provider.
//
// # Auth matrix (#854)
//
// The supported provider x endpoint x credential combinations, each pinned
// by a request-shape test in the provider package (URL path + auth header),
// so a broken combination fails CI instead of a user:
//
//	Provider   Endpoint          Credential            Recipe
//	---------  ----------------  --------------------  ------------------------------------------
//	openai     native            API key (Bearer)      NewClient(key, model)
//	openai     Azure OpenAI      Entra (MI/secret)     NewClient("", model, WithBaseURL(resource),
//	                                                     WithHTTPClient(llmauth.NewAzure*HTTPClient(...)))
//	openai     Azure OpenAI      static api-key        NewClient(key, model, WithBaseURL(resource+"/openai"),
//	                                                     WithAPIKeyHeader("api-key"))
//	anthropic  native            API key (x-api-key)   NewClient(key, model)
//	anthropic  Vertex AI         ADC / service acct    NewClient("", model, WithVertexAI(project, location),
//	                                                     WithHTTPClient(llmauth.NewGoogle*HTTPClient(...)))
//	gemini     native            API key (x-goog-...)  NewClient(key, model)
//	gemini     Vertex AI         ADC / service acct    NewClient("", model, WithVertexAI(project, location),
//	                                                     WithHTTPClient(llmauth.NewGoogle*HTTPClient(...)))
//
// llmauth (server/llmauth) builds the credentialed http.Client for the
// token rows: Azure credentials sit behind an explicit expiry-aware
// singleflight cache, and Google token sources behind
// oauth2.ReuseTokenSource, so no combination pays a token round-trip per
// request.
package llm
