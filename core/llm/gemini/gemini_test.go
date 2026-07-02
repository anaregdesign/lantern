package gemini

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
	var gotPath, gotKey string
	var gotBody request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-goog-api-key")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"modelVersion":"gemini-3","candidates":[{"content":{"parts":[`+
			`{"text":"{\"city\":\"Tokyo\",\"high\":31}"}]},"finishReason":"STOP"}],`+
			`"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":7,"totalTokenCount":19}}`)
	}))
	defer srv.Close()

	m, err := New[weather](NewClient("sk-test", "gemini-3", WithBaseURL(srv.URL)), "report weather", EffortHigh)
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
	if resp.Model != "gemini-3" {
		t.Errorf("Model = %q", resp.Model)
	}
	if gotPath != "/v1beta/models/gemini-3:generateContent" {
		t.Errorf("path = %q", gotPath)
	}
	if gotKey != "sk-test" {
		t.Errorf("x-goog-api-key = %q", gotKey)
	}
	if gotBody.SystemInstruction == nil || len(gotBody.SystemInstruction.Parts) != 1 ||
		gotBody.SystemInstruction.Parts[0].Text != "report weather" {
		t.Errorf("systemInstruction = %+v", gotBody.SystemInstruction)
	}
	if gotBody.GenerationConfig.ResponseMimeType != "application/json" {
		t.Errorf("responseMimeType = %q", gotBody.GenerationConfig.ResponseMimeType)
	}
	if gotBody.GenerationConfig.ThinkingConfig == nil || gotBody.GenerationConfig.ThinkingConfig.ThinkingLevel != "high" {
		t.Errorf("thinkingConfig = %+v, want level high", gotBody.GenerationConfig.ThinkingConfig)
	}
}

func TestGenerateWithInjectedAuthTransport(t *testing.T) {
	var gotAPIKey, gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("x-goog-api-key")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"modelVersion":"gemini-3","candidates":[{"content":{"parts":[`+
			`{"text":"{\"city\":\"Tokyo\",\"high\":31}"}]},"finishReason":"STOP"}],`+
			`"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":7,"totalTokenCount":19}}`)
	}))
	defer srv.Close()

	h := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("x-goog-api-key"); got != "" {
			t.Errorf("pre-injected x-goog-api-key = %q, want empty", got)
		}
		r.Header.Set("Authorization", "******")
		return http.DefaultTransport.RoundTrip(r)
	})}
	m, err := New[weather](NewClient("", "gemini-3", WithBaseURL(srv.URL), WithHTTPClient(h)), "report weather", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := m.Generate(context.Background(), "Tokyo"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if gotAPIKey != "" {
		t.Errorf("x-goog-api-key = %q, want empty", gotAPIKey)
	}
	if gotAuth != "******" {
		t.Errorf("Authorization = %q, want injected transport value", gotAuth)
	}
}

func TestGenerateVertexAI(t *testing.T) {
	var gotPath, gotKey, gotAuth string
	var gotBody request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-goog-api-key")
		gotAuth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"modelVersion":"gemini-2.5-flash","candidates":[{"content":{"parts":[`+
			`{"text":"{\"city\":\"Tokyo\",\"high\":31}"}]},"finishReason":"STOP"}],`+
			`"usageMetadata":{"promptTokenCount":12,"candidatesTokenCount":7,"totalTokenCount":19}}`)
	}))
	defer srv.Close()

	// An injected transport supplies the bearer token, mirroring a Google
	// service-account or ADC credential client.
	h := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("x-goog-api-key"); got != "" {
			t.Errorf("pre-injected x-goog-api-key = %q, want empty", got)
		}
		r.Header.Set("Authorization", "Bearer ya29.token")
		return http.DefaultTransport.RoundTrip(r)
	})}
	m, err := New[weather](NewClient("", "gemini-2.5-flash",
		WithVertexAI("my-proj", "us-central1"), WithBaseURL(srv.URL), WithHTTPClient(h)), "report weather", "")
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
	if want := "/v1/projects/my-proj/locations/us-central1/publishers/google/models/gemini-2.5-flash:generateContent"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
	if gotKey != "" {
		t.Errorf("x-goog-api-key = %q, want empty", gotKey)
	}
	if gotAuth != "Bearer ya29.token" {
		t.Errorf("Authorization = %q, want injected bearer", gotAuth)
	}
	if gotBody.GenerationConfig.ResponseMimeType != "application/json" {
		t.Errorf("responseMimeType = %q", gotBody.GenerationConfig.ResponseMimeType)
	}
}

func TestVertexAIHost(t *testing.T) {
	cases := map[string]string{
		"global":          "https://aiplatform.googleapis.com",
		"us-central1":     "https://us-central1-aiplatform.googleapis.com",
		"asia-northeast1": "https://asia-northeast1-aiplatform.googleapis.com",
	}
	for loc, want := range cases {
		if got := vertexAIHost(loc); got != want {
			t.Errorf("vertexAIHost(%q) = %q, want %q", loc, got, want)
		}
	}
	// WithVertexAI alone derives the regional host; WithBaseURL still overrides it.
	if c := NewClient("", "gemini-2.5-flash", WithVertexAI("p", "us-central1")); c.baseURL != "https://us-central1-aiplatform.googleapis.com" {
		t.Errorf("baseURL = %q, want regional Vertex AI host", c.baseURL)
	}
	if c := NewClient("", "gemini-2.5-flash", WithVertexAI("p", "us-central1"), WithBaseURL("https://proxy.example")); c.baseURL != "https://proxy.example" {
		t.Errorf("baseURL = %q, want proxy override", c.baseURL)
	}
}

func TestGenerateBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[]},"finishReason":"SAFETY"}]}`)
	}))
	defer srv.Close()

	m, _ := New[weather](NewClient("k", "m", WithBaseURL(srv.URL)), "x", "")
	if _, err := m.Generate(context.Background(), "y"); !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
}

func TestGenerateLength(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"{\"city\":\"A\",\"high\":1}"}]},`+
			`"finishReason":"MAX_TOKENS"}]}`)
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
		{"503 unavailable", http.StatusServiceUnavailable, "", llm.ErrUnavailable},
		{"400 bad request", http.StatusBadRequest, "", llm.ErrBadRequest},
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
			if api.Provider != "gemini" || api.StatusCode != tc.status {
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

func TestGeneratePromptBlocked(t *testing.T) {
	// A blocked PROMPT yields no candidates at all; the reason lives only in
	// promptFeedback.blockReason and must surface as ErrBlocked, not the
	// generic no-content error (#852).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"promptFeedback":{"blockReason":"PROHIBITED_CONTENT"}}`)
	}))
	defer srv.Close()

	m, _ := New[weather](NewClient("k", "m", WithBaseURL(srv.URL)), "x", "")
	_, err := m.Generate(context.Background(), "y")
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("err = %v, want ErrBlocked", err)
	}
	if !strings.Contains(err.Error(), "PROHIBITED_CONTENT") {
		t.Fatalf("err = %v, want block reason in message", err)
	}
}

func TestGenerateTruncated(t *testing.T) {
	// MAX_TOKENS cut generation mid-JSON: the decode failure must be
	// diagnosed as ErrTruncated with the WithMaxTokens remedy (#852).
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[{"text":"{\"city\":\"Tok"}]},`+
			`"finishReason":"MAX_TOKENS"}]}`)
	}))
	defer srv.Close()

	m, _ := New[weather](NewClient("k", "m", WithBaseURL(srv.URL)), "x", "")
	_, err := m.Generate(context.Background(), "y")
	if !errors.Is(err, llm.ErrTruncated) {
		t.Fatalf("err = %v, want ErrTruncated", err)
	}
}

func TestResponseSchemaStripsAdditionalProperties(t *testing.T) {
	var gotBody request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &gotBody)
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[`+
			`{"text":"{\"city\":\"Tokyo\",\"high\":1}"}]},"finishReason":"STOP"}]}`)
	}))
	defer srv.Close()

	m, err := New[weather](NewClient("k", "m", WithBaseURL(srv.URL)), "x", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := m.Generate(context.Background(), "y"); err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if strings.Contains(string(gotBody.GenerationConfig.ResponseSchema), "additionalProperties") {
		t.Errorf("responseSchema must not contain additionalProperties: %s", gotBody.GenerationConfig.ResponseSchema)
	}
}
