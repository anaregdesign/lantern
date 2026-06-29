// Package gemini implements the llm.Model interface against Google's Gemini
// generateContent API using a response schema (responseMimeType=application/json
// + responseSchema). It depends only on the standard library and the parent llm
// package, so core stays a leaf.
//
// A non-generic Client captures the endpoint, key, model, and HTTP client; the
// generic New binds a structured-output type T and a fixed system instruction
// into an llm.Model[T]. Go methods cannot take type parameters, so the schema is
// fixed at construction by New rather than per call.
package gemini

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/core/llm"
)

// DefaultBaseURL is the Gemini API root used when none is configured.
const DefaultBaseURL = "https://generativelanguage.googleapis.com"

// ErrBlocked is returned when generation is stopped by a safety or recitation
// filter and no usable output is produced.
var ErrBlocked = errors.New("gemini: response blocked")

// Effort selects the thinking level for thinking-capable models. The empty value
// lets the server decide and omits the parameter entirely.
type Effort string

const (
	// EffortLow requests low thinking.
	EffortLow Effort = "low"
	// EffortMedium requests medium thinking.
	EffortMedium Effort = "medium"
	// EffortHigh requests high thinking.
	EffortHigh Effort = "high"
)

// Client is a non-generic, reusable handle to the Gemini generateContent API. It
// is safe for concurrent use. Bind a structured-output type with New.
type Client struct {
	apiKey    string
	model     string
	baseURL   string
	maxTokens int
	http      *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the API root (e.g. a proxy). An empty string is ignored.
// The path "/v1beta/models/{model}:generateContent" is always appended.
func WithBaseURL(u string) Option {
	return func(c *Client) {
		if u != "" {
			c.baseURL = strings.TrimRight(u, "/")
		}
	}
}

// WithHTTPClient sets the HTTP client used for requests. A nil client is ignored.
func WithHTTPClient(h *http.Client) Option {
	return func(c *Client) {
		if h != nil {
			c.http = h
		}
	}
}

// WithMaxTokens caps output tokens. Zero (the default) lets the server decide.
func WithMaxTokens(n int) Option {
	return func(c *Client) {
		if n > 0 {
			c.maxTokens = n
		}
	}
}

// NewClient builds a Client for the given model. apiKey authenticates via the
// x-goog-api-key header; model is the provider model identifier (caller-supplied,
// never hard-coded). Defaults: DefaultBaseURL and a 60s-timeout http.Client.
func NewClient(apiKey, model string, opts ...Option) *Client {
	c := &Client{
		apiKey:  apiKey,
		model:   model,
		baseURL: DefaultBaseURL,
		http:    &http.Client{Timeout: 60 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// New binds output type T, a fixed system instruction, and a thinking effort to
// client, yielding an llm.Model[T]. The JSON schema is derived from T once; each
// Generate sends the instruction plus the call's input. An empty effort lets the
// server decide. The model is reusable and concurrent-safe.
func New[T any](client *Client, instruction string, effort Effort) (llm.Model[T], error) {
	schema, err := llm.SchemaFor[T]()
	if err != nil {
		return nil, err
	}
	def, err := sanitizeSchema(schema.Definition)
	if err != nil {
		return nil, fmt.Errorf("gemini: sanitize schema: %w", err)
	}
	return &model[T]{client: client, instruction: instruction, schema: def, effort: effort}, nil
}

// sanitizeSchema rewrites the canonical JSON Schema produced by llm.SchemaFor
// into the subset Gemini's responseSchema accepts. Gemini follows an OpenAPI 3.0
// subset that rejects "additionalProperties", so it is stripped recursively.
func sanitizeSchema(raw json.RawMessage) (json.RawMessage, error) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	stripUnsupported(doc)
	return json.Marshal(doc)
}

// stripUnsupported walks a decoded JSON document and removes keys Gemini rejects.
func stripUnsupported(node any) {
	switch v := node.(type) {
	case map[string]any:
		delete(v, "additionalProperties")
		for _, child := range v {
			stripUnsupported(child)
		}
	case []any:
		for _, child := range v {
			stripUnsupported(child)
		}
	}
}

// model binds an output type T, a fixed instruction, the derived JSON schema, and
// a thinking effort to a Client. It satisfies llm.Model[T].
type model[T any] struct {
	client      *Client
	instruction string
	schema      json.RawMessage
	effort      Effort
}

// Generate sends the instruction plus input, then decodes the response JSON into T.
func (m *model[T]) Generate(ctx context.Context, input string) (llm.Response[T], error) {
	r, err := m.client.generate(ctx, m.instruction, input, m.schema, m.effort)
	if err != nil {
		return llm.Response[T]{}, err
	}
	var out T
	if err := json.Unmarshal([]byte(r.text), &out); err != nil {
		return llm.Response[T]{}, fmt.Errorf("gemini: decode output: %w", err)
	}
	return llm.Response[T]{Output: out, Usage: r.usage, FinishReason: r.finish, Model: r.model}, nil
}

type request struct {
	SystemInstruction *content         `json:"systemInstruction,omitempty"`
	Contents          []content        `json:"contents"`
	GenerationConfig  generationConfig `json:"generationConfig"`
}

type content struct {
	Role  string `json:"role,omitempty"`
	Parts []part `json:"parts"`
}

type part struct {
	Text string `json:"text"`
}

type generationConfig struct {
	ResponseMimeType string          `json:"responseMimeType"`
	ResponseSchema   json.RawMessage `json:"responseSchema"`
	MaxOutputTokens  int             `json:"maxOutputTokens,omitempty"`
	ThinkingConfig   *thinkingConfig `json:"thinkingConfig,omitempty"`
}

type thinkingConfig struct {
	ThinkingLevel string `json:"thinkingLevel"`
}

type response struct {
	ModelVersion string `json:"modelVersion"`
	Candidates   []struct {
		Content      content `json:"content"`
		FinishReason string  `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		TotalTokenCount      int `json:"totalTokenCount"`
	} `json:"usageMetadata"`
}

// result is generate's non-generic output: the raw JSON text plus metadata. The
// generic New closure decodes text into T.
type result struct {
	text   string
	usage  llm.Usage
	finish llm.FinishReason
	model  string
}

func (c *Client) generate(ctx context.Context, instruction, input string, schema json.RawMessage, effort Effort) (result, error) {
	var thinking *thinkingConfig
	if effort != "" {
		thinking = &thinkingConfig{ThinkingLevel: string(effort)}
	}
	body, err := json.Marshal(request{
		SystemInstruction: &content{Parts: []part{{Text: instruction}}},
		Contents:          []content{{Role: "user", Parts: []part{{Text: input}}}},
		GenerationConfig: generationConfig{
			ResponseMimeType: "application/json",
			ResponseSchema:   schema,
			MaxOutputTokens:  c.maxTokens,
			ThinkingConfig:   thinking,
		},
	})
	if err != nil {
		return result{}, fmt.Errorf("gemini: marshal request: %w", err)
	}

	url := c.baseURL + "/v1beta/models/" + c.model + ":generateContent"
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return result{}, fmt.Errorf("gemini: build request: %w", err)
	}
	httpReq.Header.Set("x-goog-api-key", c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		return result{}, fmt.Errorf("gemini: do request: %w", err)
	}
	defer httpResp.Body.Close()

	payload, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return result{}, fmt.Errorf("gemini: read response: %w", err)
	}
	if httpResp.StatusCode != http.StatusOK {
		return result{}, fmt.Errorf("gemini: status %d: %s", httpResp.StatusCode, strings.TrimSpace(string(payload)))
	}

	var r response
	if err := json.Unmarshal(payload, &r); err != nil {
		return result{}, fmt.Errorf("gemini: decode response: %w", err)
	}
	usage := llm.Usage{
		InputTokens:  r.UsageMetadata.PromptTokenCount,
		OutputTokens: r.UsageMetadata.CandidatesTokenCount,
		TotalTokens:  r.UsageMetadata.TotalTokenCount,
	}
	for _, cand := range r.Candidates {
		for _, p := range cand.Content.Parts {
			if p.Text != "" {
				return result{text: p.Text, usage: usage, finish: finishReason(cand.FinishReason), model: r.ModelVersion}, nil
			}
		}
		if cand.FinishReason == "SAFETY" || cand.FinishReason == "RECITATION" {
			return result{}, fmt.Errorf("%w: %s", ErrBlocked, cand.FinishReason)
		}
	}
	return result{}, errors.New("gemini: no content in response")
}

func finishReason(r string) llm.FinishReason {
	switch r {
	case "STOP":
		return llm.FinishStop
	case "MAX_TOKENS":
		return llm.FinishLength
	case "SAFETY", "RECITATION":
		return llm.FinishContentFilter
	default:
		return llm.FinishOther
	}
}
