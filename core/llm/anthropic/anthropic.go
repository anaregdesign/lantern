// Package anthropic implements the llm.Model interface against Anthropic's
// Messages API (POST /v1/messages) using structured JSON-schema output. It
// depends only on the standard library and the parent llm package, so core
// stays a leaf.
//
// A non-generic Client captures the endpoint, key, model, and HTTP client; the
// generic New binds a structured-output type T and a fixed system instruction
// into an llm.Model[T]. Go methods cannot take type parameters, so the schema is
// fixed at construction by New rather than per call. Passing an empty API key
// omits the static x-api-key header so an injected HTTP client's transport can
// provide cloud-identity authentication.
//
// WithVertex retargets the client at Vertex AI's rawPredict endpoint
// (projects/{project}/locations/{location}/publishers/anthropic/...), which
// carries the model in the URL and the API version in the body's
// anthropic_version field; combine it with an injected Google-credential
// transport for service-account or ADC authentication.
package anthropic

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

// DefaultBaseURL is the Anthropic API root used when none is configured.
const DefaultBaseURL = "https://api.anthropic.com"

// DefaultVersion is the anthropic-version header sent when none is configured.
const DefaultVersion = "2023-06-01"

// DefaultMaxTokens caps output when WithMaxTokens is not set; the Messages API
// requires max_tokens, so unlike OpenAI it cannot be left to the server.
const DefaultMaxTokens = 1024

// vertexAnthropicVersion is the anthropic_version body value required by Vertex
// AI's rawPredict endpoint. Vertex carries the API version in the body rather
// than the anthropic-version header.
const vertexAnthropicVersion = "vertex-2023-10-16"

// ErrRefusal is returned when the model declines to answer.
var ErrRefusal = errors.New("anthropic: model refused to answer")

// Effort selects the extended-thinking effort for thinking-capable models. The
// empty value disables extended thinking and omits the parameter entirely.
type Effort string

const (
	// EffortLow requests low thinking effort.
	EffortLow Effort = "low"
	// EffortMedium requests medium thinking effort.
	EffortMedium Effort = "medium"
	// EffortHigh requests high thinking effort.
	EffortHigh Effort = "high"
)

// Client is a non-generic, reusable handle to the Anthropic Messages API. It is
// safe for concurrent use. Bind a structured-output type with New.
type Client struct {
	apiKey         string
	model          string
	baseURL        string
	version        string
	vertexProject  string
	vertexLocation string
	maxTokens      int
	http           *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the API root (e.g. a proxy or the Vertex AI host). An
// empty string is ignored. The native path "/v1/messages" is appended unless
// WithVertex selects the Vertex publisher-model rawPredict path.
func WithBaseURL(u string) Option {
	return func(c *Client) {
		if u != "" {
			c.baseURL = strings.TrimRight(u, "/")
		}
	}
}

// WithVertex retargets the client at Vertex AI's rawPredict endpoint for the
// given Google Cloud project and location (e.g. "us-east5" or "global"). The
// model is carried in the URL and the API version in the body's anthropic_version
// field, so the top-level model field and anthropic-version header are omitted.
// Unless WithBaseURL is also set, the API root defaults to the regional Vertex
// host. Empty project or location is ignored.
func WithVertex(project, location string) Option {
	return func(c *Client) {
		if project == "" || location == "" {
			return
		}
		c.vertexProject = project
		c.vertexLocation = location
		if c.baseURL == DefaultBaseURL {
			c.baseURL = vertexHost(location)
		}
	}
}

// vertexHost returns the Vertex AI API root for a location. The "global" location
// uses the location-less host; every other location uses a regional host.
func vertexHost(location string) string {
	if location == "global" {
		return "https://aiplatform.googleapis.com"
	}
	return "https://" + location + "-aiplatform.googleapis.com"
}

// WithVersion overrides the anthropic-version header. An empty string is ignored.
func WithVersion(v string) Option {
	return func(c *Client) {
		if v != "" {
			c.version = v
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

// WithMaxTokens caps output tokens. Non-positive values keep DefaultMaxTokens.
func WithMaxTokens(n int) Option {
	return func(c *Client) {
		if n > 0 {
			c.maxTokens = n
		}
	}
}

// NewClient builds a Client for the given model. A non-empty apiKey
// authenticates via the x-api-key header; an empty apiKey omits that header so a
// WithHTTPClient-injected transport can own auth. model is the provider model
// identifier (caller-supplied, never hard-coded). Defaults: DefaultBaseURL,
// DefaultVersion, DefaultMaxTokens, and a 60s-timeout http.Client.
func NewClient(apiKey, model string, opts ...Option) *Client {
	c := &Client{
		apiKey:    apiKey,
		model:     model,
		baseURL:   DefaultBaseURL,
		version:   DefaultVersion,
		maxTokens: DefaultMaxTokens,
		http:      &http.Client{Timeout: 60 * time.Second},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// New binds output type T, a fixed system instruction, and a thinking effort to
// client, yielding an llm.Model[T]. The JSON schema is derived from T once; each
// Generate sends the instruction plus the call's input. An empty effort omits
// extended thinking. The model is reusable and concurrent-safe.
func New[T any](client *Client, instruction string, effort Effort) (llm.Model[T], error) {
	schema, err := llm.SchemaFor[T]()
	if err != nil {
		return nil, err
	}
	return &model[T]{client: client, instruction: instruction, schema: schema.Definition, effort: effort}, nil
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
		return llm.Response[T]{}, fmt.Errorf("anthropic: decode output: %w", err)
	}
	return llm.Response[T]{Output: out, Usage: r.usage, FinishReason: r.finish, Model: r.model}, nil
}

type request struct {
	Model            string          `json:"model,omitempty"`
	AnthropicVersion string          `json:"anthropic_version,omitempty"`
	MaxTokens        int             `json:"max_tokens"`
	System           string          `json:"system,omitempty"`
	Messages         []message       `json:"messages"`
	OutputConfig     *outputConfig   `json:"output_config,omitempty"`
	Thinking         *thinkingConfig `json:"thinking,omitempty"`
}

type thinkingConfig struct {
	Type   string `json:"type"`
	Effort string `json:"effort"`
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type outputConfig struct {
	Format formatConfig `json:"format"`
}

type formatConfig struct {
	Type   string          `json:"type"`
	Schema json.RawMessage `json:"schema"`
}

type response struct {
	Model      string `json:"model"`
	StopReason string `json:"stop_reason"`
	Content    []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"content"`
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
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

// endpoint returns the Messages URL for the configured mode. Vertex mode (set by
// WithVertex) targets the publisher-model rawPredict path; otherwise the native
// Anthropic Messages path is used.
func (c *Client) endpoint() string {
	if c.vertexProject != "" {
		return c.baseURL + "/v1/projects/" + c.vertexProject + "/locations/" + c.vertexLocation +
			"/publishers/anthropic/models/" + c.model + ":rawPredict"
	}
	return c.baseURL + "/v1/messages"
}

func (c *Client) generate(ctx context.Context, instruction, input string, schema json.RawMessage, effort Effort) (result, error) {
	var thinking *thinkingConfig
	if effort != "" {
		thinking = &thinkingConfig{Type: "enabled", Effort: string(effort)}
	}
	req := request{
		MaxTokens: c.maxTokens,
		System:    instruction,
		Messages:  []message{{Role: "user", Content: input}},
		OutputConfig: &outputConfig{Format: formatConfig{
			Type:   "json_schema",
			Schema: schema,
		}},
		Thinking: thinking,
	}
	if c.vertexProject != "" {
		req.AnthropicVersion = vertexAnthropicVersion
	} else {
		req.Model = c.model
	}
	body, err := json.Marshal(req)
	if err != nil {
		return result{}, fmt.Errorf("anthropic: marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(body))
	if err != nil {
		return result{}, fmt.Errorf("anthropic: build request: %w", err)
	}
	if c.apiKey != "" {
		httpReq.Header.Set("x-api-key", c.apiKey)
	}
	if c.vertexProject == "" {
		httpReq.Header.Set("anthropic-version", c.version)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		return result{}, fmt.Errorf("anthropic: do request: %w", err)
	}
	defer httpResp.Body.Close()

	payload, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return result{}, fmt.Errorf("anthropic: read response: %w", err)
	}
	if httpResp.StatusCode != http.StatusOK {
		return result{}, fmt.Errorf("anthropic: status %d: %s", httpResp.StatusCode, strings.TrimSpace(string(payload)))
	}

	var r response
	if err := json.Unmarshal(payload, &r); err != nil {
		return result{}, fmt.Errorf("anthropic: decode response: %w", err)
	}
	if r.StopReason == "refusal" {
		return result{}, ErrRefusal
	}
	for _, part := range r.Content {
		if part.Type == "text" {
			return result{
				text:   part.Text,
				usage:  llm.Usage{InputTokens: r.Usage.InputTokens, OutputTokens: r.Usage.OutputTokens, TotalTokens: r.Usage.InputTokens + r.Usage.OutputTokens},
				finish: finishReason(r.StopReason),
				model:  r.Model,
			}, nil
		}
	}
	return result{}, errors.New("anthropic: no text in response")
}

func finishReason(stop string) llm.FinishReason {
	switch stop {
	case "end_turn", "stop_sequence":
		return llm.FinishStop
	case "max_tokens":
		return llm.FinishLength
	default:
		return llm.FinishOther
	}
}
