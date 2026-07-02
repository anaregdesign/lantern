package llm

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// Typed failure taxonomy (#852). Every provider backend maps a non-200 HTTP
// response through ClassifyHTTP so callers can branch on class instead of
// string-matching provider error text:
//
//   - errors.Is(err, ErrRateLimited)  → back off (honour APIError.RetryAfter)
//   - errors.Is(err, ErrUnavailable)  → transient; retry with backoff
//   - errors.Is(err, ErrUnauthorized) → credential/config problem; do NOT retry
//   - errors.Is(err, ErrBadRequest)   → deterministic request problem; do NOT retry
//
// errors.As(&APIError{}) recovers the provider, status code, Retry-After
// hint, and a bounded excerpt of the response body.
//
// ErrTruncated is a separate, response-level condition: the model's output
// was cut by the max-token cap and the structured JSON could not be decoded.
// Retrying without raising the cap will fail the same way.
var (
	// ErrRateLimited marks HTTP 429. Retryable after a backoff; when the
	// provider sent Retry-After it is parsed into APIError.RetryAfter.
	ErrRateLimited = errors.New("llm: rate limited")
	// ErrUnavailable marks transient provider-side failures — HTTP 5xx
	// (including Anthropic's 529 overloaded), plus 408/409. Retryable.
	ErrUnavailable = errors.New("llm: provider unavailable")
	// ErrUnauthorized marks HTTP 401/403 — a credential or permission
	// problem. Not retryable.
	ErrUnauthorized = errors.New("llm: unauthorized")
	// ErrBadRequest marks the remaining 4xx — a deterministic problem with
	// the request (schema, model id, payload shape). Not retryable.
	ErrBadRequest = errors.New("llm: bad request")
	// ErrTruncated marks a Generate whose output was cut by the max-token
	// cap before the structured JSON completed (FinishLength + decode
	// failure). Raise the cap (WithMaxTokens) instead of retrying.
	ErrTruncated = errors.New("llm: output truncated by max tokens")
)

// ErrBodyLimit bounds how many bytes of a provider error body are retained
// on an APIError (and therefore how much can ever reach a log line). Provider
// backends read error bodies through a reader capped at this size.
const ErrBodyLimit = 2048

// APIError is the structured form of a non-200 provider response. Unwrap
// returns the classification sentinel, so errors.Is picks the class and
// errors.As recovers the detail.
type APIError struct {
	// Provider is the backend that produced the failure: "anthropic",
	// "gemini", or "openai".
	Provider string
	// StatusCode is the HTTP status of the response.
	StatusCode int
	// RetryAfter is the parsed Retry-After hint for rate limits; zero when
	// the header was absent or unparseable.
	RetryAfter time.Duration
	// Body is the response body, truncated to ErrBodyLimit bytes.
	Body string

	sentinel error
}

func (e *APIError) Error() string {
	return fmt.Sprintf("%s: status %d: %s", e.Provider, e.StatusCode, e.Body)
}

func (e *APIError) Unwrap() error { return e.sentinel }

// ClassifyHTTP builds the APIError for a non-200 provider response. body may
// be arbitrarily large; it is truncated to ErrBodyLimit. The Retry-After
// header is honoured in both delta-seconds and HTTP-date forms (a past date
// yields zero).
func ClassifyHTTP(provider string, status int, header http.Header, body []byte) error {
	if len(body) > ErrBodyLimit {
		body = body[:ErrBodyLimit]
	}
	e := &APIError{
		Provider:   provider,
		StatusCode: status,
		Body:       string(body),
	}
	switch {
	case status == http.StatusTooManyRequests:
		e.sentinel = ErrRateLimited
		e.RetryAfter = parseRetryAfter(header.Get("Retry-After"))
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		e.sentinel = ErrUnauthorized
	case status == http.StatusRequestTimeout || status == http.StatusConflict:
		e.sentinel = ErrUnavailable
	case status >= 500:
		// Includes Anthropic's non-standard 529 overloaded_error.
		e.sentinel = ErrUnavailable
	default:
		e.sentinel = ErrBadRequest
	}
	return e
}

// parseRetryAfter accepts the two wire forms of Retry-After: delta-seconds
// ("30") and HTTP-date. Absent, unparseable, or already-elapsed values yield
// zero.
func parseRetryAfter(v string) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

// DecodeFailure is the shared Generate-side diagnosis for a JSON decode
// failure (#852): when generation stopped at the token cap the decode failure
// is a symptom, not the cause, so it is wrapped as ErrTruncated with the
// remedy in the message. Any other decode failure stays a plain decode error.
// Provider backends call this from their Generate implementations.
func DecodeFailure(provider string, finish FinishReason, decodeErr error) error {
	if finish == FinishLength {
		return fmt.Errorf("%s: output truncated at the max-token cap before the structured JSON completed (raise WithMaxTokens): %w",
			provider, errors.Join(ErrTruncated, decodeErr))
	}
	return fmt.Errorf("%s: decode output: %w", provider, decodeErr)
}
