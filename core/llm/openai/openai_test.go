package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

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
	var gotPath, gotAuth string
	var gotBody request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"gpt-5.5","status":"completed",`+
			`"output":[{"content":[{"type":"output_text","text":"{\"city\":\"Tokyo\",\"high\":31}"}]}],`+
			`"usage":{"input_tokens":12,"output_tokens":7,"total_tokens":19}}`)
	}))
	defer srv.Close()

	m, err := New[weather](NewClient("sk-test", "gpt-5.5", WithBaseURL(srv.URL)), "report weather", EffortHigh)
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
	if resp.Model != "gpt-5.5" {
		t.Errorf("Model = %q", resp.Model)
	}
	if gotPath != "/v1/responses" {
		t.Errorf("path = %q", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("auth = %q", gotAuth)
	}
	if gotBody.Model != "gpt-5.5" || gotBody.Instructions != "report weather" || !gotBody.Text.Format.Strict {
		t.Errorf("request = %+v", gotBody)
	}
	if gotBody.Reasoning == nil || gotBody.Reasoning.Effort != "high" {
		t.Errorf("reasoning = %+v, want effort high", gotBody.Reasoning)
	}
}

func TestGenerateWithInjectedAuthTransport(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"gpt-5.5","status":"completed",`+
			`"output":[{"content":[{"type":"output_text","text":"{\"city\":\"Tokyo\",\"high\":31}"}]}],`+
			`"usage":{"input_tokens":12,"output_tokens":7,"total_tokens":19}}`)
	}))
	defer srv.Close()

	h := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("pre-injected Authorization = %q, want empty", got)
		}
		r.Header.Set("Authorization", "******")
		return http.DefaultTransport.RoundTrip(r)
	})}
	m, err := New[weather](NewClient("", "gpt-5.5", WithBaseURL(srv.URL), WithHTTPClient(h)), "report weather", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := m.Generate(context.Background(), "Tokyo"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotAuth != "******" {
		t.Errorf("Authorization = %q, want injected transport value", gotAuth)
	}
}

func TestGenerateRefusal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"status":"completed","output":[{"content":[{"type":"refusal","refusal":"no"}]}]}`)
	}))
	defer srv.Close()

	m, _ := New[weather](NewClient("k", "m", WithBaseURL(srv.URL)), "x", "")
	if _, err := m.Generate(context.Background(), "y"); !errors.Is(err, ErrRefusal) {
		t.Fatalf("err = %v, want ErrRefusal", err)
	}
}

func TestGenerateLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},`+
			`"output":[{"content":[{"type":"output_text","text":"{\"city\":\"A\",\"high\":1}"}]}]}`)
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
	cases := []struct {
		name       string
		status     int
		retryAfter string
		sentinel   error
	}{
		{"401 unauthorized", http.StatusUnauthorized, "", llm.ErrUnauthorized},
		{"429 rate limited with Retry-After", http.StatusTooManyRequests, "7", llm.ErrRateLimited},
		{"500 unavailable", http.StatusInternalServerError, "", llm.ErrUnavailable},
		{"404 bad request", http.StatusNotFound, "", llm.ErrBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.retryAfter != "" {
					w.Header().Set("Retry-After", tc.retryAfter)
				}
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, `{"error":{}}`)
			}))
			defer srv.Close()

			m, _ := New[weather](NewClient("k", "m", WithBaseURL(srv.URL)), "x", "")
			_, err := m.Generate(context.Background(), "y")
			if !errors.Is(err, tc.sentinel) {
				t.Fatalf("err = %v, want sentinel for status %d", err, tc.status)
			}
			var api *llm.APIError
			if !errors.As(err, &api) {
				t.Fatalf("err = %v, want APIError", err)
			}
			if api.Provider != "openai" || api.StatusCode != tc.status {
				t.Fatalf("APIError = %+v", api)
			}
			if tc.retryAfter != "" && api.RetryAfter != 7*time.Second {
				t.Fatalf("RetryAfter = %v, want 7s", api.RetryAfter)
			}
			if !strings.Contains(err.Error(), strconv.Itoa(tc.status)) {
				t.Fatalf("Error() = %q, want status in message", err.Error())
			}
		})
	}
}

func TestGenerateTruncated(t *testing.T) {
	// max_output_tokens cut generation mid-JSON: the decode failure must be
	// diagnosed as ErrTruncated with the WithMaxTokens remedy (#852).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"},`+
			`"output":[{"content":[{"type":"output_text","text":"{\"city\":\"Tok"}]}]}`)
	}))
	defer srv.Close()

	m, _ := New[weather](NewClient("k", "m", WithBaseURL(srv.URL)), "x", "")
	_, err := m.Generate(context.Background(), "y")
	if !errors.Is(err, llm.ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
}

// TestGenerateAzureAPIKeyHeader pins the classic Azure OpenAI static-key
// row of the #854 auth matrix: WithAPIKeyHeader("api-key") sends the key
// in that header (no Authorization), and WithBaseURL carries the Azure
// resource path so the request lands on <resource>/openai/v1/responses.
func TestGenerateAzureAPIKeyHeader(t *testing.T) {
	var gotPath, gotAPIKey, gotAuthz string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAPIKey = r.Header.Get("api-key")
		gotAuthz = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"gpt-5.5","status":"completed",`+
			`"output":[{"content":[{"type":"output_text","text":"{\"city\":\"Tokyo\",\"high\":31}"}]}],`+
			`"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`)
	}))
	defer srv.Close()

	m, err := New[weather](NewClient("azure-key", "gpt-5.5",
		WithBaseURL(srv.URL+"/openai"),
		WithAPIKeyHeader("api-key"),
	), "weather", EffortLow)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := m.Generate(context.Background(), "Tokyo"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotPath != "/openai/v1/responses" {
		t.Errorf("path = %q, want /openai/v1/responses", gotPath)
	}
	if gotAPIKey != "azure-key" {
		t.Errorf("api-key header = %q, want azure-key", gotAPIKey)
	}
	if gotAuthz != "" {
		t.Errorf("Authorization must be absent in api-key-header mode; got %q", gotAuthz)
	}
}
