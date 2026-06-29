package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/core/llm"
)

type weather struct {
	City string `json:"city"`
	High int    `json:"high"`
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestGenerate(t *testing.T) {
	var gotPath, gotKey, gotVersion string
	var gotBody request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"claude-opus","stop_reason":"end_turn",`+
			`"content":[{"type":"text","text":"{\"city\":\"Tokyo\",\"high\":31}"}],`+
			`"usage":{"input_tokens":12,"output_tokens":7}}`)
	}))
	defer srv.Close()

	m, err := New[weather](NewClient("sk-test", "claude-opus", WithBaseURL(srv.URL)), "report weather", EffortHigh)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := m.Generate(context.Background(), "Tokyo")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	if resp.Output != (weather{City: "Tokyo", High: 31}) {
		t.Errorf("Output = %+v, want Tokyo/31", resp.Output)
	}
	if resp.Usage != (llm.Usage{InputTokens: 12, OutputTokens: 7, TotalTokens: 19}) {
		t.Errorf("Usage = %+v", resp.Usage)
	}
	if resp.FinishReason != llm.FinishStop {
		t.Errorf("FinishReason = %q, want stop", resp.FinishReason)
	}
	if resp.Model != "claude-opus" {
		t.Errorf("Model = %q", resp.Model)
	}
	if gotPath != "/v1/messages" {
		t.Errorf("path = %q", gotPath)
	}
	if gotKey != "sk-test" {
		t.Errorf("x-api-key = %q", gotKey)
	}
	if gotVersion != DefaultVersion {
		t.Errorf("anthropic-version = %q", gotVersion)
	}
	if gotBody.Model != "claude-opus" || gotBody.System != "report weather" || gotBody.MaxTokens != DefaultMaxTokens {
		t.Errorf("request = %+v", gotBody)
	}
	if gotBody.OutputConfig == nil || gotBody.OutputConfig.Format.Type != "json_schema" {
		t.Errorf("output_config = %+v", gotBody.OutputConfig)
	}
	if gotBody.Thinking == nil || gotBody.Thinking.Type != "enabled" || gotBody.Thinking.Effort != "high" {
		t.Errorf("thinking = %+v, want enabled/high", gotBody.Thinking)
	}
}

func TestGenerateWithInjectedAuthTransport(t *testing.T) {
	var gotAPIKey, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("x-api-key")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"claude-opus","stop_reason":"end_turn",`+
			`"content":[{"type":"text","text":"{\"city\":\"Tokyo\",\"high\":31}"}],`+
			`"usage":{"input_tokens":12,"output_tokens":7}}`)
	}))
	defer srv.Close()

	h := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("x-api-key"); got != "" {
			t.Errorf("pre-injected x-api-key = %q, want empty", got)
		}
		r.Header.Set("Authorization", "******")
		return http.DefaultTransport.RoundTrip(r)
	})}
	m, err := New[weather](NewClient("", "claude-opus", WithBaseURL(srv.URL), WithHTTPClient(h)), "report weather", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := m.Generate(context.Background(), "Tokyo"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotAPIKey != "" {
		t.Errorf("x-api-key = %q, want empty", gotAPIKey)
	}
	if gotAuth != "******" {
		t.Errorf("Authorization = %q, want injected transport value", gotAuth)
	}
}

func TestGenerateRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"stop_reason":"refusal","content":[]}`)
	}))
	defer srv.Close()

	m, _ := New[weather](NewClient("k", "m", WithBaseURL(srv.URL)), "x", "")
	if _, err := m.Generate(context.Background(), "y"); !errors.Is(err, ErrRefusal) {
		t.Fatalf("err = %v, want ErrRefusal", err)
	}
}

func TestGenerateLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"stop_reason":"max_tokens",`+
			`"content":[{"type":"text","text":"{\"city\":\"A\",\"high\":1}"}]}`)
	}))
	defer srv.Close()

	m, _ := New[weather](NewClient("k", "m", WithBaseURL(srv.URL)), "x", "")
	resp, err := m.Generate(context.Background(), "y")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.FinishReason != llm.FinishLength {
		t.Errorf("FinishReason = %q, want length", resp.FinishReason)
	}
}

func TestGenerateHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, "bad key")
	}))
	defer srv.Close()

	m, _ := New[weather](NewClient("k", "m", WithBaseURL(srv.URL)), "x", "")
	_, err := m.Generate(context.Background(), "y")
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v, want 401", err)
	}
}
