package integration_test

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/llm/gemini"
	"github.com/anaregdesign/lantern/core/llm/openai"
	"github.com/anaregdesign/lantern/server/llmauth"
)

// These tests exercise the cloud-credential → provider wiring end to end against
// the live provider endpoints. They are skipped unless the relevant environment
// variables are set, so the default `go test ./...` run (and CI) never reaches
// the network. The point they prove: an empty API key plus an llmauth transport
// (Google service account / ADC, or an Azure service principal) is sufficient to
// authenticate, because the provider attaches no auth of its own and the
// transport injects the bearer token.

// cloudAuthAnswer is the structured-output schema for the smoke prompt; the JSON
// Schema sent to the provider is derived from this type.
type cloudAuthAnswer struct {
	Reply string `json:"reply"`
}

const cloudAuthInstruction = `Reply with a JSON object whose "reply" field is the word "pong".`

// TestVertexGeminiGoogleCredential drives the Service Account / ADC → Vertex
// Gemini path: llmauth builds a Google-credential HTTP client whose transport
// supplies the OAuth bearer token, and the gemini provider (empty API key,
// WithVertex) targets the Vertex generateContent endpoint.
//
// Env:
//
//	LANTERN_IT_VERTEX_PROJECT      (required) Google Cloud project ID — gates the test
//	LANTERN_IT_VERTEX_LOCATION     (optional) Vertex location, default "global"
//	LANTERN_IT_VERTEX_MODEL        (optional) model ID, default "gemini-2.5-flash"
//	GOOGLE_APPLICATION_CREDENTIALS (optional) path to a service-account key JSON;
//	                               when set the test uses the service-account
//	                               transport, otherwise it falls back to ADC.
func TestVertexGeminiGoogleCredential(t *testing.T) {
	project := os.Getenv("LANTERN_IT_VERTEX_PROJECT")
	if project == "" {
		t.Skip("set LANTERN_IT_VERTEX_PROJECT to run the Vertex Gemini cloud-auth test")
	}
	location := envOrDefault("LANTERN_IT_VERTEX_LOCATION", "global")
	modelID := envOrDefault("LANTERN_IT_VERTEX_MODEL", "gemini-2.5-flash")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	authClient, err := googleAuthClient(ctx, t)
	if err != nil {
		t.Fatalf("build google credential client: %v", err)
	}

	client := gemini.NewClient("", modelID,
		gemini.WithVertex(project, location),
		gemini.WithHTTPClient(authClient),
		gemini.WithMaxTokens(64),
	)
	model, err := gemini.New[cloudAuthAnswer](client, cloudAuthInstruction, "")
	if err != nil {
		t.Fatalf("gemini.New: %v", err)
	}

	resp, err := model.Generate(ctx, "ping")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Output.Reply == "" {
		t.Errorf("empty reply, want non-empty; response = %+v", resp.Output)
	}
	t.Logf("vertex gemini ok: reply=%q model=%q usage=%+v", resp.Output.Reply, resp.Model, resp.Usage)
}

// TestAzureOpenAIServicePrincipal drives the Azure Service Principal → Azure
// OpenAI (GPT) path: llmauth builds a client-secret credential transport, and the
// openai provider (empty API key, Azure base URL) targets the deployment's
// /openai/v1/responses surface.
//
// Env (all required to run; otherwise skipped):
//
//	LANTERN_IT_AZURE_OPENAI_ENDPOINT   e.g. https://myres.openai.azure.com/openai
//	LANTERN_IT_AZURE_OPENAI_DEPLOYMENT the model deployment name
//	AZURE_TENANT_ID                    service-principal tenant
//	AZURE_CLIENT_ID                    service-principal app (client) ID
//	AZURE_CLIENT_SECRET                service-principal secret
func TestAzureOpenAIServicePrincipal(t *testing.T) {
	endpoint := os.Getenv("LANTERN_IT_AZURE_OPENAI_ENDPOINT")
	deployment := os.Getenv("LANTERN_IT_AZURE_OPENAI_DEPLOYMENT")
	tenant := os.Getenv("AZURE_TENANT_ID")
	clientID := os.Getenv("AZURE_CLIENT_ID")
	secret := os.Getenv("AZURE_CLIENT_SECRET")
	if endpoint == "" || deployment == "" || tenant == "" || clientID == "" || secret == "" {
		t.Skip("set LANTERN_IT_AZURE_OPENAI_ENDPOINT, LANTERN_IT_AZURE_OPENAI_DEPLOYMENT, AZURE_TENANT_ID, AZURE_CLIENT_ID and AZURE_CLIENT_SECRET to run the Azure OpenAI service-principal test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	authClient, err := llmauth.NewAzureClientSecretHTTPClient(tenant, clientID, secret,
		llmauth.WithTimeout(60*time.Second))
	if err != nil {
		t.Fatalf("build azure service-principal client: %v", err)
	}

	client := openai.NewClient("", deployment,
		openai.WithBaseURL(endpoint),
		openai.WithHTTPClient(authClient),
		openai.WithMaxTokens(512),
	)
	// Azure's current GA chat models are reasoning models, where output tokens
	// also fund reasoning; low effort plus a larger cap keeps the smoke prompt
	// within budget.
	model, err := openai.New[cloudAuthAnswer](client, cloudAuthInstruction, openai.EffortLow)
	if err != nil {
		t.Fatalf("openai.New: %v", err)
	}

	resp, err := model.Generate(ctx, "ping")
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if resp.Output.Reply == "" {
		t.Errorf("empty reply, want non-empty; response = %+v", resp.Output)
	}
	t.Logf("azure openai ok: reply=%q model=%q usage=%+v", resp.Output.Reply, resp.Model, resp.Usage)
}

// googleAuthClient builds the Google-credential HTTP client used by the Vertex
// test: a service-account transport when GOOGLE_APPLICATION_CREDENTIALS points at
// a key file, otherwise Application Default Credentials.
func googleAuthClient(ctx context.Context, t *testing.T) (*http.Client, error) {
	t.Helper()
	if keyPath := os.Getenv("GOOGLE_APPLICATION_CREDENTIALS"); keyPath != "" {
		key, err := os.ReadFile(keyPath)
		if err != nil {
			t.Fatalf("read service-account key %q: %v", keyPath, err)
		}
		return llmauth.NewGoogleServiceAccountHTTPClient(ctx, key, llmauth.WithTimeout(60*time.Second))
	}
	return llmauth.NewGoogleDefaultHTTPClient(ctx, llmauth.WithTimeout(60*time.Second))
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
