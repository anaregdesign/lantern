package provider

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNewLLMEngine_ProviderSelection is the #828 selection table:
// disabled (and empty) compose a nil engine without error — the
// first-class "no LLM" state — each real provider constructs its client,
// and malformed provider/model config fails fast at boot.
func TestNewLLMEngine_ProviderSelection(t *testing.T) {
	cases := []struct {
		name         string
		cfg          LLMConfig
		wantProvider string
		wantErr      string
	}{
		{name: "default disabled", cfg: LLMConfig{Provider: LLMDisabled}, wantProvider: LLMDisabled},
		{name: "empty means disabled", cfg: LLMConfig{}, wantProvider: LLMDisabled},
		{name: "openai", cfg: LLMConfig{Provider: LLMOpenAI, Model: "gpt-5.5", APIKey: "sk-x"}, wantProvider: LLMOpenAI},
		{name: "anthropic", cfg: LLMConfig{Provider: LLMAnthropic, Model: "claude-opus-4-8", APIKey: "sk-x"}, wantProvider: LLMAnthropic},
		{name: "gemini", cfg: LLMConfig{Provider: LLMGemini, Model: "gemini-2.5-pro", APIKey: "sk-x"}, wantProvider: LLMGemini},
		{name: "unknown provider", cfg: LLMConfig{Provider: "ollama"}, wantErr: "LANTERN_LLM_PROVIDER"},
		{name: "missing model", cfg: LLMConfig{Provider: LLMOpenAI, APIKey: "sk-x"}, wantErr: "LANTERN_LLM_MODEL"},
		{name: "api-key mode without key", cfg: LLMConfig{Provider: LLMOpenAI, Model: "gpt-5.5"}, wantErr: "LANTERN_LLM_API_KEY"},
		{name: "unknown auth mode", cfg: LLMConfig{Provider: LLMOpenAI, Model: "gpt-5.5", Auth: "vault"}, wantErr: "LANTERN_LLM_AUTH"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e, err := NewLLMEngine(tc.cfg)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want mention of %s", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewLLMEngine: %v", err)
			}
			if e.Provider() != tc.wantProvider {
				t.Fatalf("Provider() = %q, want %q", e.Provider(), tc.wantProvider)
			}
			if tc.wantProvider == LLMDisabled && e != nil {
				t.Fatal("disabled must yield a nil engine")
			}
		})
	}
}

// TestNewLLMEngine_AuthModes constructs each injectable-auth mode with
// fake credentials and no network (#826): construction must fail fast on
// missing credential vars and succeed when they are present.
func TestNewLLMEngine_AuthModes(t *testing.T) {
	base := LLMConfig{Provider: LLMOpenAI, Model: "gpt-5.5"}

	t.Run("azure-client-secret requires the credential triple", func(t *testing.T) {
		cfg := base
		cfg.Auth = LLMAuthAzureClientSecret
		if _, err := NewLLMEngine(cfg); err == nil || !strings.Contains(err.Error(), "AZURE_TENANT_ID") {
			t.Fatalf("err = %v", err)
		}
		cfg.AzureTenantID = "11111111-1111-1111-1111-111111111111"
		cfg.AzureClientID = "22222222-2222-2222-2222-222222222222"
		cfg.AzureClientSecret = "fake-secret"
		e, err := NewLLMEngine(cfg)
		if err != nil {
			t.Fatalf("construction with fake credentials must not hit the network: %v", err)
		}
		if e.Provider() != LLMOpenAI {
			t.Fatalf("provider = %q", e.Provider())
		}
	})

	t.Run("google-service-account requires and reads the key file", func(t *testing.T) {
		cfg := base
		cfg.Auth = LLMAuthGoogleServiceAccount
		if _, err := NewLLMEngine(cfg); err == nil || !strings.Contains(err.Error(), "GOOGLE_CREDENTIALS_FILE") {
			t.Fatalf("err = %v", err)
		}
		// A structurally valid service-account key with a fake RSA key is
		// enough for token-source construction; no network is involved.
		keyFile := filepath.Join(t.TempDir(), "sa.json")
		if err := os.WriteFile(keyFile, []byte(fakeServiceAccountJSON), 0o600); err != nil {
			t.Fatalf("write key: %v", err)
		}
		cfg.GoogleCredentialsFile = keyFile
		if _, err := NewLLMEngine(cfg); err != nil {
			t.Fatalf("construction with fake service account: %v", err)
		}
	})

	t.Run("missing key file surfaces the path error", func(t *testing.T) {
		cfg := base
		cfg.Auth = LLMAuthGoogleServiceAccount
		cfg.GoogleCredentialsFile = filepath.Join(t.TempDir(), "absent.json")
		if _, err := NewLLMEngine(cfg); err == nil || !strings.Contains(err.Error(), "GOOGLE_CREDENTIALS_FILE") {
			t.Fatalf("err = %v", err)
		}
	})
}

// TestBindLLM covers the type-erased→generic seam: nil engine returns
// ErrLLMDisabled (feature-off, not failure), and a bound model performs a
// full fake generate round trip through the engine's constructed client.
func TestBindLLM(t *testing.T) {
	type verdict struct {
		City string `json:"city"`
		High int    `json:"high"`
	}

	t.Run("nil engine is ErrLLMDisabled", func(t *testing.T) {
		m, err := BindLLM[verdict](nil, "instr", "low")
		if !errors.Is(err, ErrLLMDisabled) || m != nil {
			t.Fatalf("BindLLM(nil) = (%v, %v)", m, err)
		}
	})

	t.Run("bind and fake generate round trip", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"model":"gpt-5.5","status":"completed",`+
				`"output":[{"content":[{"type":"output_text","text":"{\"city\":\"Tokyo\",\"high\":31}"}]}],`+
				`"usage":{"input_tokens":12,"output_tokens":7,"total_tokens":19}}`)
		}))
		defer srv.Close()

		e, err := NewLLMEngine(LLMConfig{
			Provider: LLMOpenAI, Model: "gpt-5.5", APIKey: "sk-test", BaseURL: srv.URL,
		})
		if err != nil {
			t.Fatalf("NewLLMEngine: %v", err)
		}
		m, err := BindLLM[verdict](e, "report weather", "high")
		if err != nil {
			t.Fatalf("BindLLM: %v", err)
		}
		resp, err := m.Generate(context.Background(), "Tokyo")
		if err != nil {
			t.Fatalf("Generate: %v", err)
		}
		if resp.Output != (verdict{City: "Tokyo", High: 31}) {
			t.Fatalf("Output = %+v", resp.Output)
		}
	})
}

// fakeServiceAccountJSON is a structurally valid Google service-account
// key carrying a throwaway 512-bit RSA key generated for this test — it
// grants nothing and never leaves the process.
const fakeServiceAccountJSON = `{
  "type": "service_account",
  "project_id": "fake-project",
  "private_key_id": "fake",
  "private_key": "-----BEGIN RSA PRIVATE KEY-----\nMIIBOgIBAAJBALPKLTiwrRprPlKvj0GhSLcAJFuWJmVCkVvR/hNCd3oh6cWFk3PN\n2NnAUEbabCFm/A0DEXlqiIWyq7CngRZroj0CAwEAAQJACRLU9BUnFYYIYnkarabW\nEGiJoAvB6 q52kj+Pgz3z0lkwOMdNSlqhkraRLCoDUsVDywRj+8y6te3ep8HL7NUW\ngQIhAOZjcvSs0Aju9nMTDYyDqu2zNPv2vaWDCkCE0Bx1XHLpAiEAx9opd2sZTAJI\nS3PitiWDBjOtRy2Zavmxb+50wSDOTfUCIFwPvKrYSaMlAVSdW/BAgOTPGjMskGGk\nfk09gRitkfNlAiBu2Kdb9C1WVjS9JIfSpTUJ9WhyRcz9dJqQpsAe11UZoQIgW2Ig\nLLPu8jQZeS0dGGqmZituKpTs6qDGA9K7pcGpXTk=\n-----END RSA PRIVATE KEY-----\n",
  "client_email": "fake@fake-project.iam.gserviceaccount.com",
  "client_id": "0",
  "token_uri": "https://oauth2.googleapis.com/token"
}`
