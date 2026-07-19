package integration_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/cli/parser"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// TestTraversalDocumentationGate keeps the maintained consumer examples and
// UI copy aligned with the family-verb grammar. The wire RPC and SDK method
// intentionally remain named Illuminate; this gate only rejects retired CLI
// grammar and routes, then parses every documented family command through the
// production parser.
func TestTraversalDocumentationGate(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}

	files := map[string]struct {
		required []string
		retired  []string
	}{
		"README.md": {
			required: []string{"bfs: {", "ppr: {", "community: {"},
		},
		"sdks/node/example/main.ts": {
			required: []string{"bfs: {", "ppr: {", "community: {"},
			retired:  []string{"Algorithm", "algorithm:"},
		},
		"sdks/go/example/main.go": {
			required: []string{"client.WithBFS", "client.WithPPR", "client.WithLocalCommunity"},
		},
		"sdks/go/README.md": {
			required: []string{"type Reduction = pb.Reduction"},
			retired:  []string{"type Algorithm = pb.Algorithm"},
		},
		"admin/app/components/cli/CliPage/CliPage.tsx": {
			required: []string{"bfs alice 2 5 reduction=spt"},
			retired:  []string{"illuminate alice 2 5"},
		},
		"admin/README.md": {
			required: []string{"`cli.tsx`", "CLI workspace"},
			retired:  []string{"`illuminate.tsx`"},
		},
		"admin/Caddyfile": {
			required: []string{"/cli, /ops"},
			retired:  []string{"/illuminate"},
		},
		"mcp/examples/README.md": {
			required: []string{"**CLI** (`/cli`)"},
			retired:  []string{"/illuminate"},
		},
		".github/agents/User.agent.md": {
			required: []string{
				"lantern-cli bfs user:alice 1 5",
				"lantern-cli bfs user:alice 2 10 reduction=mst objective=min",
			},
			retired: []string{"lantern-cli illuminate"},
		},
	}

	for path, checks := range files {
		t.Run(path, func(t *testing.T) {
			contents, readErr := os.ReadFile(filepath.Join(repoRoot, path))
			if readErr != nil {
				t.Fatalf("read %s: %v", path, readErr)
			}
			text := string(contents)
			for _, want := range checks.required {
				if !strings.Contains(text, want) {
					t.Errorf("%s is missing maintained example/copy %q", path, want)
				}
			}
			for _, retired := range checks.retired {
				if strings.Contains(text, retired) {
					t.Errorf("%s still contains retired traversal grammar/copy %q", path, retired)
				}
			}
		})
	}

	for _, example := range []string{
		"bfs user:42 3 8 reduction=spt objective=max",
		"pagerank user:42 10 restart_prob=0.15 epsilon=0.0001",
		"community user:42 30 reduction=mst objective=min",
		"bfs alice 2 5 reduction=spt",
		"lantern-cli bfs user:alice 1 5",
		"lantern-cli bfs user:alice 2 10 reduction=mst objective=min",
	} {
		t.Run(example, func(t *testing.T) {
			parseTraversalDocumentationCommand(t, example)
		})
	}
}

// TestDartPublishingContractGate keeps the tag-driven pub.dev OIDC release
// path aligned with the documented publishing contract after the one-time
// manual first release.
func TestDartPublishingContractGate(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}

	workflowPath := filepath.Join(repoRoot, ".github", "workflows", "dart-sdk.yml")
	workflow, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read Dart SDK workflow: %v", err)
	}
	publishStart := strings.Index(string(workflow), "\n  publish:\n")
	if publishStart == -1 {
		t.Fatal("Dart SDK workflow is missing the publish job")
	}
	publishWorkflow := string(workflow)[publishStart:]

	for _, contract := range []string{
		"startsWith(github.ref, 'refs/tags/sdks/dart/v')",
		"timeout-minutes: 15",
		"id-token: write",
		"uses: dart-lang/setup-dart@65eb853c7ba17dde3be364c3d2858773e7144260 # v1",
		"dart pub get --enforce-lockfile --no-example",
		"dart pub publish --force",
		`gh release create "$TAG" --title "$TAG"`,
	} {
		if !strings.Contains(publishWorkflow, contract) {
			t.Errorf("Dart SDK workflow is missing publishing contract %q", contract)
		}
	}
	for _, retired := range []string{
		"Enforce first-release epic blockers",
		"First Dart release is blocked by open Issue",
	} {
		if strings.Contains(string(workflow), retired) {
			t.Errorf("Dart SDK workflow still contains retired first-release contract %q", retired)
		}
	}

	contributing, err := os.ReadFile(filepath.Join(repoRoot, "CONTRIBUTING.md"))
	if err != nil {
		t.Fatalf("read CONTRIBUTING.md: %v", err)
	}
	for _, contract := range []string{
		"The one-time manual first publish completed with `0.1.0`",
		"Later releases are tag-driven only",
		"manual `dart pub publish`",
	} {
		if !strings.Contains(string(contributing), contract) {
			t.Errorf("CONTRIBUTING.md is missing Dart release contract %q", contract)
		}
	}
	if strings.Contains(string(contributing), "git switch --detach sdks/dart/v0.1.0") {
		t.Error("CONTRIBUTING.md still contains the retired manual first-publish procedure")
	}
}

// TestDartWorkflowGate keeps the change-scoped fast path from weakening the
// release and SDK-surface matrix. Backend-only changes may skip platform work,
// but the stable aggregate gate must require every full-matrix job when Dart,
// proto, toolchain, workflow, or release inputs change.
func TestDartWorkflowGate(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}

	workflow, err := os.ReadFile(filepath.Join(repoRoot, ".github", "workflows", "dart-sdk.yml"))
	if err != nil {
		t.Fatalf("read Dart SDK workflow: %v", err)
	}
	text := string(workflow)
	for _, contract := range []string{
		"name: Classify changes",
		"docs/decisions/0001-dart-mobile-transport.md",
		"name: Generate, analyze, and test",
		"dart pub get --enforce-lockfile --no-example",
		"dart analyze lib test",
		"if: needs.changes.outputs.full == 'true'",
		"bufbuild/buf-setup-action@a47c93e0b1648d5651a065437926377d060baa99",
		"version: 1.71.0",
		"name: Minimum Dart 3.11",
		"name: Build API documentation",
		"dart pub publish --dry-run",
		"dart pub global run pana --json",
		"name: Check Flutter example",
		"flutter test --no-pub integration_test/mobile_smoke_test.dart -d emulator-5554",
		"name: Start iOS simulator boot",
		"name: Wait for iOS simulator",
		"flutter build ios --debug --no-codesign --no-pub",
		"name: Gate",
		"needs: [changes, test, minimum-dart, android, ios]",
		"require_result minimum-dart \"$MINIMUM_DART_RESULT\" success",
		"require_result android \"$ANDROID_RESULT\" success",
		"require_result ios \"$IOS_RESULT\" success",
		"needs: [gate]",
	} {
		if !strings.Contains(text, contract) {
			t.Errorf("Dart SDK workflow is missing scoped-gate contract %q", contract)
		}
	}
	if strings.Contains(text, "flutter build apk --debug") {
		t.Error("Dart SDK workflow duplicates the APK build already performed by the Android native smoke")
	}

	iosStart := strings.Index(text, "\n  ios:\n")
	if iosStart < 0 {
		t.Fatal("Dart SDK workflow is missing the iOS job")
	}
	gateOffset := strings.Index(text[iosStart:], "\n  gate:\n")
	if gateOffset < 0 {
		t.Fatal("Dart SDK workflow is missing the aggregate gate after the iOS job")
	}
	ios := text[iosStart : iosStart+gateOffset]
	last := -1
	for _, step := range []string{
		"name: Start iOS simulator boot",
		"name: Start Lantern",
		"name: Check and build iOS example",
		"name: Wait for iOS simulator",
		"name: Run iOS native smoke",
	} {
		index := strings.Index(ios, step)
		if index < 0 {
			t.Fatalf("iOS job is missing %q", step)
		}
		if index <= last {
			t.Fatalf("iOS step %q is out of order", step)
		}
		last = index
	}

	contributing, err := os.ReadFile(filepath.Join(repoRoot, "CONTRIBUTING.md"))
	if err != nil {
		t.Fatalf("read CONTRIBUTING.md: %v", err)
	}
	for _, contract := range []string{
		"routes backend/search-only changes through the current-Dart unit and real-wire gates",
		"stable `Gate` job",
		"required result set for either scope",
	} {
		if !strings.Contains(string(contributing), contract) {
			t.Errorf("CONTRIBUTING.md is missing Dart workflow contract %q", contract)
		}
	}
}

// TestSearchDocumentationGate keeps the canonical search guide synchronized
// with the wire schema and every maintained user-facing surface. Tables remain
// readable hand-written prose, while descriptor reflection makes a new option,
// response field, capability, or typed reason fail the ordinary root suite
// until its contract is documented (#1069).
func TestSearchDocumentationGate(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	read := func(path string) string {
		t.Helper()
		contents, readErr := os.ReadFile(filepath.Join(repoRoot, path))
		if readErr != nil {
			t.Fatalf("read %s: %v", path, readErr)
		}
		return string(contents)
	}

	guide := read("docs/search.md")
	file := pb.File_graph_v1_graph_proto
	for _, name := range []string{
		"SearchOptions",
		"SearchVerticesRequest",
		"SearchVerticesResponse",
		"SearchCapabilities",
	} {
		message := file.Messages().ByName(protoreflect.Name(name))
		if message == nil {
			t.Fatalf("proto descriptor is missing %s", name)
		}
		for i := 0; i < message.Fields().Len(); i++ {
			field := message.Fields().Get(i)
			want := "`" + string(field.Name()) + "`"
			if !strings.Contains(guide, want) {
				t.Errorf("docs/search.md is missing %s.%s as %s", name, field.Name(), want)
			}
		}
	}
	for _, name := range []string{
		"MatchMode",
		"SearchProjection",
		"SearchHitProjectionStatus",
		"SearchErrorReason",
		"SearchIndexHealth",
	} {
		enum := file.Enums().ByName(protoreflect.Name(name))
		if enum == nil {
			t.Fatalf("proto descriptor is missing %s", name)
		}
		for i := 0; i < enum.Values().Len(); i++ {
			value := enum.Values().Get(i)
			want := "`" + string(value.Name()) + "`"
			if !strings.Contains(guide, want) {
				t.Errorf("docs/search.md is missing %s.%s as %s", name, value.Name(), want)
			}
		}
	}

	linkChecks := map[string]string{
		"README.md":                         "docs/search.md",
		"server/README.md":                  "docs/search.md",
		"docs/env.md":                       "SearchVertices contract](search.md)",
		"docs/replication.md":               "SearchVertices contract](search.md",
		"docs/ha-runbook.md":                "SearchVertices contract](search.md)",
		"sdks/go/README.md":                 "docs/search.md",
		"sdks/go/doc.go":                    "docs/search.md",
		"sdks/node/README.md":               "docs/search.md",
		"sdks/node/src/index.ts":            "docs/search.md",
		"sdks/dart/README.md":               "docs/search.md",
		"sdks/dart/lib/lantern_client.dart": "docs/search.md",
		"cli/cmd/search.go":                 "docs/search.md",
		"cli/parser/help.go":                "docs/search.md",
		"admin/app/lib/cli/verbs.ts":        "docs/search.md",
		"admin/app/components/browse-vertices/BrowseVerticesPage/BrowseVerticesPage.tsx": "docs/search.md",
		"admin/app/components/ops/SearchStatusCard/SearchStatusCard.tsx":                 "docs/search.md",
	}
	for path, want := range linkChecks {
		if !strings.Contains(read(path), want) {
			t.Errorf("%s is missing canonical search contract link %q", path, want)
		}
	}

	examples := map[string][]string{
		"sdks/go/example/search.go":       {"SearchVertices(", "NewIncrementalSearch("},
		"sdks/node/example/search.ts":     {"searchVertices(", "incrementalSearch("},
		"sdks/dart/example/lib/main.dart": {"searchVertices(", "incrementalSearch("},
	}
	for path, required := range examples {
		contents := read(path)
		for _, want := range required {
			if !strings.Contains(contents, want) {
				t.Errorf("%s is missing compiling search example %q", path, want)
			}
		}
	}

	const start = "<!-- search-cli-grammar:start -->"
	const end = "<!-- search-cli-grammar:end -->"
	startAt := strings.Index(guide, start)
	endAt := strings.Index(guide, end)
	if startAt < 0 || endAt < 0 || endAt <= startAt {
		t.Fatal("docs/search.md is missing ordered search CLI grammar markers")
	}
	for _, line := range strings.Split(guide[startAt+len(start):endAt], "\n") {
		command := strings.TrimSpace(line)
		if command == "" || strings.HasPrefix(command, "```") {
			continue
		}
		t.Run(command, func(t *testing.T) {
			parseSearchDocumentationCommand(t, command)
		})
	}
}

// TestGKEDeployRecoveryWorkflow keeps normal releases and manual recovery on
// one deployment implementation, including the diagnostics needed to explain
// a StatefulSet rollout timeout without interactive cluster access.
func TestGKEDeployRecoveryWorkflow(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}

	releaseWorkflow, err := os.ReadFile(filepath.Join(repoRoot, ".github", "workflows", "docker-publish.yml"))
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	for _, contract := range []string{
		"uses: ./.github/workflows/_deploy-gke.yml",
		"image_tag: ${{ github.ref_name }}",
		"secrets: inherit",
		"id-token: write",
	} {
		if !strings.Contains(string(releaseWorkflow), contract) {
			t.Errorf("release workflow is missing GKE deployment contract %q", contract)
		}
	}

	deployWorkflow, err := os.ReadFile(filepath.Join(repoRoot, ".github", "workflows", "_deploy-gke.yml"))
	if err != nil {
		t.Fatalf("read reusable GKE deployment workflow: %v", err)
	}
	for _, contract := range []string{
		"workflow_call:",
		"workflow_dispatch:",
		"image_tag:",
		"timeout-minutes: 20",
		"docker buildx imagetools inspect",
		"google-github-actions/auth@7c6bc770dae815cd3e89ee6cdf493a5fab2cc093 # v3",
		"google-github-actions/setup-gcloud@aa5489c8933f4cc7a4f7d45035b3b1440c9c10db # v3",
		"google-github-actions/get-gke-credentials@3da1e46a907576cefaa90c484278bb5b259dd395 # v3",
		"azure/setup-helm@9bc31f4ebc9c6b171d7bfbaa5d006ae7abdb4310 # v5",
		"helm upgrade --install",
		"--wait --timeout 10m --debug",
		"if: failure()",
		"kubectl describe statefulset",
		"kubectl get events",
		"--all-containers --previous --tail=200",
	} {
		if !strings.Contains(string(deployWorkflow), contract) {
			t.Errorf("reusable GKE deployment workflow is missing recovery contract %q", contract)
		}
	}
	for _, action := range []string{
		"google-github-actions/auth",
		"google-github-actions/setup-gcloud",
		"google-github-actions/get-gke-credentials",
		"azure/setup-helm",
	} {
		pinnedUse := regexp.MustCompile(`(?m)^\s*uses:\s+` + regexp.QuoteMeta(action) + `@[0-9a-f]{40}(?:\s+#.*)?$`)
		if !pinnedUse.Match(deployWorkflow) {
			t.Errorf("reusable GKE deployment workflow does not pin %s to a 40-character commit SHA", action)
		}
	}
}

func parseTraversalDocumentationCommand(t *testing.T, command string) {
	t.Helper()
	command = strings.TrimPrefix(command, "lantern-cli ")
	source, err := parser.NewSource(command)
	if err != nil {
		t.Fatalf("tokenise %q: %v", command, err)
	}
	verb, err := parser.Verb(source)
	if err != nil {
		t.Fatalf("parse verb in %q: %v", command, err)
	}
	switch verb {
	case "bfs":
		_, err = parser.BfsParam(source)
	case "pagerank":
		_, err = parser.PagerankParam(source)
	case "community":
		_, err = parser.CommunityParam(source)
	default:
		t.Fatalf("unexpected traversal verb %q", verb)
	}
	if err != nil {
		t.Fatalf("parse %q: %v", command, err)
	}
	if err := parser.EOF(source); err != nil {
		t.Fatalf("trailing token in %q: %v", command, err)
	}
}

func parseSearchDocumentationCommand(t *testing.T, command string) {
	t.Helper()
	source, err := parser.NewSource(command)
	if err != nil {
		t.Fatalf("tokenise %q: %v", command, err)
	}
	verb, err := parser.Verb(source)
	if err != nil {
		t.Fatalf("parse verb in %q: %v", command, err)
	}
	if verb != "search" {
		t.Fatalf("unexpected search documentation verb %q", verb)
	}
	if _, err := parser.SearchParam(source); err != nil {
		t.Fatalf("parse %q: %v", command, err)
	}
	if err := parser.EOF(source); err != nil {
		t.Fatalf("trailing token in %q: %v", command, err)
	}
}
