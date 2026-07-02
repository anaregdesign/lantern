// Package gemini implements the llm.Model interface against Google's Gemini
// generateContent API using a response schema (responseMimeType=application/json
// + responseSchema). It depends only on the standard library and the parent llm
// package, so core stays a leaf.
//
// A non-generic Client captures the endpoint, key, model, and HTTP client; the
// generic New binds a structured-output type T and a fixed system instruction
// into an llm.Model[T]. Go methods cannot take type parameters, so the schema is
// fixed at construction by New rather than per call. Passing an empty API key
// omits the static x-goog-api-key header so an injected HTTP client's transport
// can provide cloud-identity authentication.
//
// WithVertexAI retargets the client at Vertex AI's generateContent endpoint
// (projects/{project}/locations/{location}/publishers/google/...), whose request
// and response bodies match the Gemini Developer API; combine it with an injected
// Google-credential transport for service-account or ADC authentication.
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
	apiKey           string
	model            string
	baseURL          string
	vertexAIProject  string
	vertexAILocation string
	maxTokens        int
	http             *http.Client
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the API root (e.g. a proxy or the Vertex AI host). An
// empty string is ignored. The Developer API path
// "/v1beta/models/{model}:generateContent" is appended unless WithVertexAI selects
// the Vertex AI publisher-model path.
func WithBaseURL(u string) Option {
	return func(c *Client) {
		if u != "" {
			c.baseURL = strings.TrimRight(u, "/")
		}
	}
}

// WithVertexAI retargets the client at Vertex AI's generateContent endpoint for the
// given Google Cloud project and location (e.g. "us-central1" or "global"). The
// request and response bodies are identical to the Developer API; only the URL
// changes to projects/{project}/locations/{location}/publishers/google/models/
// {model}:generateContent. Unless WithBaseURL is also set, the API root defaults
// to the regional Vertex AI host. Empty project or location is ignored.
func WithVertexAI(project, location string) Option {
	return func(c *Client) {
		if project == "" || location == "" {
			return
		}
		c.vertexAIProject = project
		c.vertexAILocation = location
		if c.baseURL == DefaultBaseURL {
			c.baseURL = vertexAIHost(location)
		}
	}
}

// vertexAIHost returns the Vertex AI API root for a location. The "global" location
// uses the location-less host; every other location uses a regional host.
func vertexAIHost(location string) string {
	if location == "global" {
		return "https://aiplatform.googleapis.com"
	}
	return "https://" + location + "-aiplatform.googleapis.com"
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
// authenticates via the x-goog-api-key header; an empty apiKey omits that header
// so a WithHTTPClient-injected transport can own auth. model is the provider
// model identifier (caller-supplied, never hard-coded). Defaults: DefaultBaseURL
// and a 60s-timeout http.Client.
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
// into Gemini's OpenAPI-3.0-subset responseSchema via an ALLOWLIST walk
// (#853): only the fields Gemini documents survive, everything else is
// dropped or translated. The previous one-key blocklist
// (delete "additionalProperties") rotted every time SchemaFor learned a new
// keyword — a []byte field's contentEncoding:base64 already produced a
// schema Gemini rejected with a remote 400. The allowlist fails closed:
// an unknown keyword can never reach the wire.
func sanitizeSchema(raw json.RawMessage) (json.RawMessage, error) {
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	return json.Marshal(sanitizeNode(doc))
}

// geminiFormats is the per-type format allowlist: Gemini accepts only these
// format values; anything else (e.g. "uri", "email") is dropped rather than
// forwarded — a missing format is valid, a foreign one is a 400.
var geminiFormats = map[string]map[string]bool{
	"string":  {"date-time": true, "enum": true},
	"integer": {"int32": true, "int64": true},
	"number":  {"float": true, "double": true},
}

// sanitizeNode rewrites one schema node (and its children) into the Gemini
// subset. Non-object nodes pass through (enum value arrays, bools, ...).
func sanitizeNode(node any) any {
	obj, ok := node.(map[string]any)
	if !ok {
		return node
	}

	// []byte translation: SchemaFor emits {"type":"string",
	// "contentEncoding":"base64"} — Gemini rejects the keyword, but a plain
	// string still round-trips (encoding/json decodes base64 strings into
	// []byte), so preserve the field and move the encoding note into the
	// description instead of erroring.
	if enc, ok := obj["contentEncoding"].(string); ok {
		note := "content encoding: " + enc
		if desc, ok := obj["description"].(string); ok && desc != "" {
			obj["description"] = desc + " (" + note + ")"
		} else {
			obj["description"] = note
		}
	}

	out := make(map[string]any, len(obj))
	typ, _ := obj["type"].(string)
	for key, val := range obj {
		switch key {
		case "type", "description", "nullable", "enum", "required",
			"minItems", "maxItems", "propertyOrdering":
			out[key] = sanitizeNode(val)
		case "format":
			// Constrain format per type: forward only the values Gemini
			// documents for the node's declared type.
			if f, ok := val.(string); ok && geminiFormats[typ][f] {
				out[key] = f
			}
		case "items":
			out[key] = sanitizeNode(val)
		case "properties":
			props, ok := val.(map[string]any)
			if !ok {
				continue
			}
			sanitized := make(map[string]any, len(props))
			for name, sub := range props {
				sanitized[name] = sanitizeNode(sub)
			}
			out[key] = sanitized
		default:
			// Unknown keyword (additionalProperties, contentEncoding, $schema,
			// pattern, ...): dropped. The allowlist fails closed by design.
		}
	}
	return out
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
		return llm.Response[T]{}, llm.DecodeFailure("gemini", r.finish, err)
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
	// PromptFeedback carries prompt-level (pre-generation) blocking: when the
	// PROMPT itself is rejected, candidates is empty and blockReason names the
	// filter — the only signal distinguishing a block from an empty response
	// (#852).
	PromptFeedback struct {
		BlockReason string `json:"blockReason"`
	} `json:"promptFeedback"`
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

// endpoint returns the generateContent URL for the configured mode. Vertex AI mode
// (set by WithVertexAI) targets the publisher-model path; otherwise the Gemini
// Developer API path is used.
func (c *Client) endpoint() string {
	if c.vertexAIProject != "" {
		return c.baseURL + "/v1/projects/" + c.vertexAIProject + "/locations/" + c.vertexAILocation +
			"/publishers/google/models/" + c.model + ":generateContent"
	}
	return c.baseURL + "/v1beta/models/" + c.model + ":generateContent"
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

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(), bytes.NewReader(body))
	if err != nil {
		return result{}, fmt.Errorf("gemini: build request: %w", err)
	}
	if c.apiKey != "" {
		httpReq.Header.Set("x-goog-api-key", c.apiKey)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		return result{}, fmt.Errorf("gemini: do request: %w", err)
	}
	defer httpResp.Body.Close()

	// Classify failures BEFORE reading an unbounded body: the error path
	// reads at most llm.ErrBodyLimit bytes and maps the status onto the
	// shared retryability taxonomy (#852).
	if httpResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(httpResp.Body, llm.ErrBodyLimit))
		return result{}, llm.ClassifyHTTP("gemini", httpResp.StatusCode, httpResp.Header, bytes.TrimSpace(body))
	}
	payload, err := io.ReadAll(httpResp.Body)
	if err != nil {
		return result{}, fmt.Errorf("gemini: read response: %w", err)
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
	// A blocked PROMPT produces no candidates at all; surface it as the same
	// typed ErrBlocked instead of the generic no-content error (#852).
	if r.PromptFeedback.BlockReason != "" {
		return result{}, fmt.Errorf("%w: prompt blocked: %s", ErrBlocked, r.PromptFeedback.BlockReason)
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
