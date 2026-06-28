package llm

// Usage reports the token accounting a model returns with a Response. The three
// counts are the common ground between providers: InputTokens is OpenAI's
// prompt_tokens (Responses API input_tokens) and Gemini's promptTokenCount;
// OutputTokens is OpenAI's completion_tokens (output_tokens) and Gemini's
// candidatesTokenCount; TotalTokens is OpenAI's total_tokens and Gemini's
// totalTokenCount. A count is zero when the backend does not report it.
type Usage struct {
	InputTokens  int
	OutputTokens int
	TotalTokens  int
}

// FinishReason is the normalized reason a model stopped generating, mapped from
// each provider's own vocabulary onto a small backend-agnostic set.
//
// The mapping from the major providers is:
//
//	FinishStop          OpenAI "stop"            Gemini "STOP"
//	FinishLength        OpenAI "length"          Gemini "MAX_TOKENS"
//	FinishContentFilter OpenAI "content_filter"  Gemini "SAFETY", "RECITATION"
//	FinishOther         anything else (e.g. tool/function calls) or unset
type FinishReason string

const (
	// FinishStop means the model emitted a natural, complete stop.
	FinishStop FinishReason = "stop"
	// FinishLength means generation was truncated by a token limit; the Output
	// may be incomplete and fail to satisfy the schema.
	FinishLength FinishReason = "length"
	// FinishContentFilter means generation was cut short by a safety or content
	// policy filter.
	FinishContentFilter FinishReason = "content_filter"
	// FinishOther covers any other or unspecified reason.
	FinishOther FinishReason = "other"
)
