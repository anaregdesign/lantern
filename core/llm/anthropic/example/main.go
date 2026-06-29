// Command example shows the public API of the core/llm/anthropic package: build
// a reusable Client, bind a structured-output type with New, and call Generate
// to get a fully decoded value plus token usage and finish reason.
//
// It needs a real Anthropic key, so it reads two env vars and is a no-op without
// them. Run it from the core module with:
//
//	export ANTHROPIC_API_KEY=sk-ant-...   # your key
//	export ANTHROPIC_MODEL=claude-opus    # any model id (never hard-coded)
//	go run ./llm/anthropic/example
package main

import (
	"context"
	"errors"
	"log"
	"os"
	"time"

	"github.com/anaregdesign/lantern/core/llm"
	"github.com/anaregdesign/lantern/core/llm/anthropic"
)

// weather is the structured-output schema: New derives a strict JSON Schema from
// it, and Generate decodes the model's JSON answer back into this type. Use
// `json` tags for field names and the parent llm package's schema rules.
type weather struct {
	City string `json:"city"`
	High int    `json:"high"`
}

func main() {
	log.SetFlags(0)

	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	model := os.Getenv("ANTHROPIC_MODEL")
	if apiKey == "" || model == "" {
		log.Println("set ANTHROPIC_API_KEY and ANTHROPIC_MODEL to run this example")
		return
	}

	// A Client is non-generic, reusable, and concurrent-safe — it captures the
	// key, model, and HTTP setup. The model id comes from the caller (env here),
	// never hard-coded. Options tune the endpoint, version, and token cap.
	client := anthropic.NewClient(apiKey, model,
		anthropic.WithMaxTokens(256),
		// anthropic.WithBaseURL("https://my-proxy.example.com"), // proxy
	)

	// New binds output type T plus a fixed instruction into an llm.Model[T].
	// Reuse one Client across many models with different instructions/types.
	m, err := anthropic.New[weather](client, "Report the weather as structured data.")
	if err != nil {
		log.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	resp, err := m.Generate(ctx, "What's the weather in Tokyo today? Make up a plausible high.")
	if err != nil {
		// A model decline maps to ErrRefusal; everything else is a real error.
		if errors.Is(err, anthropic.ErrRefusal) {
			log.Printf("refused: %v", err)
			return
		}
		log.Fatalf("Generate: %v", err)
	}

	// Output is the decoded weather; the rest is normalized metadata.
	log.Printf("city=%s high=%d", resp.Output.City, resp.Output.High)
	log.Printf("model=%s finish=%s tokens=%d", resp.Model, resp.FinishReason, resp.Usage.TotalTokens)
	if resp.FinishReason == llm.FinishLength {
		log.Println("note: output was truncated by max tokens")
	}
}
