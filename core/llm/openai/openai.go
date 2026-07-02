// Package openai implements the llm.Model interface against OpenAI's Responses
// API (POST /v1/responses) using strict structured output. It depends only on
// the standard library and the parent llm package, so core stays a leaf.
//
// A non-generic Client captures the endpoint, key, model, and HTTP client; the
// generic New binds a structured-output type T and a fixed system instruction
// into an llm.Model[T]. Go methods cannot take type parameters, so the schema is
// fixed at construction by New rather than per call. Passing an empty API key
// omits the static Authorization header so an injected HTTP client's transport
// can provide cloud-identity authentication.
package openai

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

// DefaultBaseURL is the OpenAI API root used when none is configured.
const DefaultBaseURL = "https://api.openai.com"

// ErrRefusal is returned when the model declines to answer; the refusal message
// is wrapped for context.
var ErrRefusal = errors.New("openai: model refused to answer")

// Effort selects the reasoning effort for reasoning-capable models. The empty
// value lets the server decide and omits the parameter entirely.
type Effort string

const (
	// EffortMinimal requests the least reasoning.
	EffortMinimal Effort = "minimal"
	// EffortLow requests low reasoning.
	EffortLow Effort = "low"
	// EffortMedium requests medium reasoning.
	EffortMedium Effort = "medium"
	// EffortHigh requests high reasoning.
	EffortHigh Effort = "high"
)

// Client is a non-generic, reusable handle to the OpenAI Responses API. It is
// safe for concurrent use. Bind a structured-output type with New.
type Client struct {
	apiKey    string
	model     string
	baseURL   string
	maxTokens int
	http      *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the API root (e.g. a proxy or Azure endpoint). An empty
// string is ignored. The path "/v1/responses" is always appended.
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

// NewClient builds a Client for the given model. A non-empty apiKey
// authenticates as a bearer token; an empty apiKey omits Authorization so a
// WithHTTPClient-injected transport can own auth. model is the provider model
// identifier (caller-supplied, never hard-coded). Defaults: DefaultBaseURL and a
// 60s-timeout http.Client.
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

// New binds output type T, a fixed system instruction, and a reasoning effort to
// client, yielding an llm.Model[T]. The JSON schema is derived from T once; each
// Generate sends the instruction plus the call's input. An empty effort omits
// the reasoning parameter. The model is reusable and concurrent-safe.
func New[T any](client *Client, instruction string, effort Effort) (llm.Model[T], error) {
	schema, err := llm.SchemaFor[T]()
	if err != nil {
		return nil, err
	}
	name := schema.Name
	if name == "" {
		name = "output"
	}
	return &model[T]{client: client, instruction: instruction, name: name, schema: schema.Definition, effort: effort}, nil
}

// model binds an output type T, a fixed instruction, the derived JSON schema, and
// a reasoning effort to a Client. It satisfies llm.Model[T].
type model[T any] struct {
	client      *Client
	instruction string
	name        string
	schema      json.RawMessage
	effort      Effort
}

// Generate sends the instruction plus input, then decodes the response JSON into T.
func (m *model[T]) Generate(ctx context.Context, input string) (llm.Response[T], error) {
	r, err := m.client.generate(ctx, m.instruction, input, m.name, m.schema, m.effort)
	if err != nil {
		return llm.Response[T]{}, err
	}
	var out T
	if err := json.Unmarshal([]byte(r.text), &out); err != nil {
		return llm.Response[T]{}, llm.DecodeFailure("openai", r.finish, err)
	}
	return llm.Response[T]{Output: out, Usage: r.usage, FinishReason: r.finish, Model: r.model}, nil
}

type request struct {
	Model           string           `json:"model"`
	Instructions    string           `json:"instructions,omitempty"`
	Input           string           `json:"input"`
	Text            textConfig       `json:"text"`
	MaxOutputTokens int              `json:"max_output_tokens,omitempty"`
	Reasoning       *reasoningConfig `json:"reasoning,omitempty"`
}

type reasoningConfig struct {
	Effort string `json:"effort"`
}

type textConfig struct {
	Format formatConfig `json:"format"`
}

type formatConfig struct {
	Type   string          `json:"type"`
	Name   string          `json:"name"`
	Strict bool            `json:"strict"`
	Schema json.RawMessage `json:"schema"`
}

type response struct {
	Model             string `json:"model"`
	Status            string `json:"status"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Output []struct {
		Content []struct {
			Type    string `json:"type"`
			Text    string `json:"text"`
			Refusal string `json:"refusal"`
		} `json:"content"`
	} `json:"output"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// result is generate's non-generic output: the raw JSON text plus metadata. The
// generic New closure decodes text into T.
type result struct {
	text   string
	usage  llm.Usage
	finish llm.FinishReason
	model  string
}

func (c *Client) generate(ctx context.Context, instruction, input, name string, schema json.RawMessage, effort Effort) (result, error) {
	var reasoning *reasoningConfig
	if effort != "" {
		reasoning = &reasoningConfig{Effort: string(effort)}
	}
	body, err := json.Marshal(request{
		Model:        c.model,
		Instructions: instruction,
		Input:        input,
		Text: textConfig{Format: formatConfig{
			Type:   "json_schema",
			Name:   name,
			Strict: true,
			Schema: schema,
		}},
		MaxOutputTokens: c.maxTokens,
		Reasoning:       reasoning,
	})
	if err != nil {
		return result{}, fmt.Errorf("openai: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/responses", bytes.NewReader(body))
	if err != nil {
		return result{}, fmt.Errorf("openai: build request: %w", err)
	}
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		return result{}, fmt.Errorf("openai: do request: %w", err)
	}
	defer httpResp.Body.Close()

	// Classify failures BEFORE reading an unbounded body: the error path
	// reads at most llm.ErrBodyLimit bytes and maps the status onto the
	// shared retryability taxonomy (#852).
	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(httpResp.Body, llm.ErrBodyLimit))
		return result{}, llm.ClassifyHTTP("openai", httpResp.StatusCode, httpResp.Header, bytes.TrimSpace(body))
	}
	payload, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return result{}, fmt.Errorf("openai: read response: %w", err)
	}

	var r response
	if err := json.Unmarshal(payload, &r); err != nil {
		return result{}, fmt.Errorf("openai: decode response: %w", err)
	}
	for _, out := range r.Output {
		for _, part := range out.Content {
			if part.Type == "refusal" {
				return result{}, fmt.Errorf("%w: %s", ErrRefusal, part.Refusal)
			}
			if part.Type == "output_text" {
				return result{
					text:   part.Text,
					usage:  llm.Usage{InputTokens: r.Usage.InputTokens, OutputTokens: r.Usage.OutputTokens, TotalTokens: r.Usage.TotalTokens},
					finish: finishReason(r),
					model:  r.Model,
				}, nil
			}
		}
	}
	return result{}, errors.New("openai: no output_text in response")
}

func finishReason(r response) llm.FinishReason {
	if r.Status == "completed" {
		return llm.FinishStop
	}
	if r.IncompleteDetails != nil && r.IncompleteDetails.Reason == "max_output_tokens" {
		return llm.FinishLength
	}
	return llm.FinishOther
}
