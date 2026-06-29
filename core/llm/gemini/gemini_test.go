package gemini

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

	m, err := New[weather](NewClient("sk-test", "gemini-3", WithBaseURL(srv.URL)), "report weather")
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
}

func TestGenerateBlocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"parts":[]},"finishReason":"SAFETY"}]}`)
	}))
	defer srv.Close()

	m, _ := New[weather](NewClient("k", "m", WithBaseURL(srv.URL)), "x")
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

	m, _ := New[weather](NewClient("k", "m", WithBaseURL(srv.URL)), "x")
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

	m, _ := New[weather](NewClient("k", "m", WithBaseURL(srv.URL)), "x")
	_, err := m.Generate(context.Background(), "y")
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("err = %v, want 401", err)
	}
}
