// Package provider: llm.go wires the core/llm backends into the server's
// DI graph (#828, follow-up to #818). LANTERN_LLM_* env config selects a
// provider and auth mode; the result is a type-erased *LLMEngine that
// google/wire can carry (wire cannot take type parameters), and the
// generic BindLLM[T] adapter mints a concrete llm.Model[T] per call site —
// the same workaround the GraphCache[string, *Vertex] binding uses.
//
// LANTERN_LLM_PROVIDER=disabled (the default) yields a nil *LLMEngine and
// wire still composes: nil-Engine is a first-class state, not an error.
// No service handler consumes the Engine yet — the scope here is wiring
// only; the first consumer arrives with a future feature.
package provider

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/anaregdesign/lantern/core/llm"
	"github.com/anaregdesign/lantern/core/llm/anthropic"
	"github.com/anaregdesign/lantern/core/llm/gemini"
	"github.com/anaregdesign/lantern/core/llm/openai"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/llmauth"
)

// LLM provider names for LLMConfig.Provider / LANTERN_LLM_PROVIDER.
const (
	LLMDisabled  = "disabled"
	LLMOpenAI    = "openai"
	LLMAnthropic = "anthropic"
	LLMGemini    = "gemini"
)

// LLM auth modes for LLMConfig.Auth / LANTERN_LLM_AUTH (#826/#854): the
// API key stays optional so token-based platforms (Azure OpenAI with
// Entra ID, Vertex AI with ADC) inject a credentialed http.Client instead.
const (
	LLMAuthAPIKey               = "api-key"
	LLMAuthAzureManagedIdentity = "azure-managed-identity"
	LLMAuthAzureClientSecret    = "azure-client-secret"
	LLMAuthGoogleADC            = "google-adc"
	LLMAuthGoogleServiceAccount = "google-service-account"
)

// LLMConfig is the focused LANTERN_LLM_* slice (SRP: providers take the
// slice they need, never *Config).
//
//   - LANTERN_LLM_PROVIDER   (default "disabled") — disabled|openai|anthropic|gemini
//   - LANTERN_LLM_MODEL      — provider model id (required unless disabled)
//   - LANTERN_LLM_API_KEY    — secret for auth=api-key (empty otherwise)
//   - LANTERN_LLM_BASE_URL   — endpoint override (Azure OpenAI resource,
//     OpenAI-compatible gateway, region-pinned Gemini, ...)
//   - LANTERN_LLM_MAX_TOKENS (default 0 = provider default)
//   - LANTERN_LLM_AUTH       (default "api-key") — see LLMAuth* constants
//   - LANTERN_LLM_AZURE_TENANT_ID / _AZURE_CLIENT_ID / _AZURE_CLIENT_SECRET
//     — auth=azure-client-secret credentials
//   - LANTERN_LLM_GOOGLE_CREDENTIALS_FILE — auth=google-service-account
//     key file path (google-adc reads the ambient environment instead)
type LLMConfig struct {
	Provider              string
	Model                 string
	APIKey                string
	BaseURL               string
	MaxTokens             int
	Auth                  string
	AzureTenantID         string
	AzureClientID         string
	AzureClientSecret     string
	GoogleCredentialsFile string
}

func loadLLMConfig() LLMConfig {
	return LLMConfig{
		Provider:              envconfig.String("LANTERN_LLM_PROVIDER", LLMDisabled),
		Model:                 envconfig.String("LANTERN_LLM_MODEL", ""),
		APIKey:                envconfig.String("LANTERN_LLM_API_KEY", ""),
		BaseURL:               envconfig.String("LANTERN_LLM_BASE_URL", ""),
		MaxTokens:             envconfig.Int("LANTERN_LLM_MAX_TOKENS", 0),
		Auth:                  envconfig.String("LANTERN_LLM_AUTH", LLMAuthAPIKey),
		AzureTenantID:         envconfig.String("LANTERN_LLM_AZURE_TENANT_ID", ""),
		AzureClientID:         envconfig.String("LANTERN_LLM_AZURE_CLIENT_ID", ""),
		AzureClientSecret:     envconfig.String("LANTERN_LLM_AZURE_CLIENT_SECRET", ""),
		GoogleCredentialsFile: envconfig.String("LANTERN_LLM_GOOGLE_CREDENTIALS_FILE", ""),
	}
}

// NewLLMConfig is the wire selector for the LANTERN_LLM_* slice.
func NewLLMConfig(c *Config) LLMConfig { return c.LLM }

// LLMEngine is the type-erased, provider-bound handle the wire graph
// carries. It captures exactly one constructed backend client plus the
// shared knobs; BindLLM turns it into a concrete llm.Model[T] per call
// site. A nil *LLMEngine means LANTERN_LLM_PROVIDER=disabled.
type LLMEngine struct {
	provider  string
	openai    *openai.Client
	anthropic *anthropic.Client
	gemini    *gemini.Client
}

// Provider reports which backend the engine is bound to.
func (e *LLMEngine) Provider() string {
	if e == nil {
		return LLMDisabled
	}
	return e.provider
}

// NewLLMEngine builds the engine from config. disabled → (nil, nil) so the
// wire graph composes without an LLM; any other provider constructs its
// client eagerly (fail-fast at boot for malformed config), including the
// injectable-auth http.Client when Auth is not api-key.
func NewLLMEngine(cfg LLMConfig) (*LLMEngine, error) {
	switch cfg.Provider {
	case "", LLMDisabled:
		return nil, nil
	case LLMOpenAI, LLMAnthropic, LLMGemini:
	default:
		return nil, fmt.Errorf("LANTERN_LLM_PROVIDER=%q: must be one of disabled|openai|anthropic|gemini", cfg.Provider)
	}
	if cfg.Model == "" {
		return nil, fmt.Errorf("LANTERN_LLM_MODEL is required when LANTERN_LLM_PROVIDER=%s", cfg.Provider)
	}

	if cfg.Auth == "" {
		cfg.Auth = LLMAuthAPIKey
	}
	httpClient, err := llmAuthHTTPClient(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.Auth == LLMAuthAPIKey && cfg.APIKey == "" {
		return nil, fmt.Errorf("LANTERN_LLM_API_KEY is required for LANTERN_LLM_AUTH=api-key (use a token auth mode for keyless platforms)")
	}

	e := &LLMEngine{provider: cfg.Provider}
	switch cfg.Provider {
	case LLMOpenAI:
		var opts []openai.Option
		if cfg.BaseURL != "" {
			opts = append(opts, openai.WithBaseURL(cfg.BaseURL))
		}
		if cfg.MaxTokens > 0 {
			opts = append(opts, openai.WithMaxTokens(cfg.MaxTokens))
		}
		if httpClient != nil {
			opts = append(opts, openai.WithHTTPClient(httpClient))
		}
		e.openai = openai.NewClient(cfg.APIKey, cfg.Model, opts...)
	case LLMAnthropic:
		var opts []anthropic.Option
		if cfg.BaseURL != "" {
			opts = append(opts, anthropic.WithBaseURL(cfg.BaseURL))
		}
		if cfg.MaxTokens > 0 {
			opts = append(opts, anthropic.WithMaxTokens(cfg.MaxTokens))
		}
		if httpClient != nil {
			opts = append(opts, anthropic.WithHTTPClient(httpClient))
		}
		e.anthropic = anthropic.NewClient(cfg.APIKey, cfg.Model, opts...)
	case LLMGemini:
		var opts []gemini.Option
		if cfg.BaseURL != "" {
			opts = append(opts, gemini.WithBaseURL(cfg.BaseURL))
		}
		if cfg.MaxTokens > 0 {
			opts = append(opts, gemini.WithMaxTokens(cfg.MaxTokens))
		}
		if httpClient != nil {
			opts = append(opts, gemini.WithHTTPClient(httpClient))
		}
		e.gemini = gemini.NewClient(cfg.APIKey, cfg.Model, opts...)
	}
	return e, nil
}

// llmAuthHTTPClient builds the credentialed http.Client for the token
// auth modes (#826): the backend then runs with an empty API key and the
// transport injects the bearer. api-key returns nil (backends use their
// default client).
func llmAuthHTTPClient(cfg LLMConfig) (*http.Client, error) {
	switch cfg.Auth {
	case "", LLMAuthAPIKey:
		return nil, nil
	case LLMAuthAzureManagedIdentity:
		return llmauth.NewAzureManagedIdentityHTTPClient()
	case LLMAuthAzureClientSecret:
		if cfg.AzureTenantID == "" || cfg.AzureClientID == "" || cfg.AzureClientSecret == "" {
			return nil, fmt.Errorf("LANTERN_LLM_AUTH=azure-client-secret requires LANTERN_LLM_AZURE_TENANT_ID, _AZURE_CLIENT_ID and _AZURE_CLIENT_SECRET")
		}
		return llmauth.NewAzureClientSecretHTTPClient(cfg.AzureTenantID, cfg.AzureClientID, cfg.AzureClientSecret)
	case LLMAuthGoogleADC:
		return llmauth.NewGoogleDefaultHTTPClient(context.Background())
	case LLMAuthGoogleServiceAccount:
		if cfg.GoogleCredentialsFile == "" {
			return nil, fmt.Errorf("LANTERN_LLM_AUTH=google-service-account requires LANTERN_LLM_GOOGLE_CREDENTIALS_FILE")
		}
		raw, err := os.ReadFile(cfg.GoogleCredentialsFile)
		if err != nil {
			return nil, fmt.Errorf("LANTERN_LLM_GOOGLE_CREDENTIALS_FILE: %w", err)
		}
		return llmauth.NewGoogleServiceAccountHTTPClient(context.Background(), raw)
	default:
		return nil, fmt.Errorf("LANTERN_LLM_AUTH=%q: must be one of api-key|azure-managed-identity|azure-client-secret|google-adc|google-service-account", cfg.Auth)
	}
}

// BindLLM mints a concrete llm.Model[T] from the type-erased engine: T
// fixes the structured-output schema, instruction the task framing, and
// effort ("minimal"|"low"|"medium"|"high"; "" = provider default) the
// reasoning depth. Generic, so it lives OUTSIDE the wire graph — call
// sites bind their own T against the injected engine.
//
// A nil engine returns ErrLLMDisabled so features degrade explicitly
// rather than nil-panicking at Generate time.
func BindLLM[T any](e *LLMEngine, instruction, effort string) (llm.Model[T], error) {
	if e == nil {
		return nil, ErrLLMDisabled
	}
	switch e.provider {
	case LLMOpenAI:
		return openai.New[T](e.openai, instruction, openai.Effort(effort))
	case LLMAnthropic:
		return anthropic.New[T](e.anthropic, instruction, anthropic.Effort(effort))
	case LLMGemini:
		return gemini.New[T](e.gemini, instruction, gemini.Effort(effort))
	}
	return nil, fmt.Errorf("llm: unknown provider %q", e.provider)
}

// ErrLLMDisabled is returned by BindLLM when LANTERN_LLM_PROVIDER=disabled
// (the default). Callers treat it as "feature off", not a failure.
var ErrLLMDisabled = fmt.Errorf("llm: disabled (set LANTERN_LLM_PROVIDER to enable)")
