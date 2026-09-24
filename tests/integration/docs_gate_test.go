package integration_test

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/cli/parser"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/reflect/protoreflect"
	"gopkg.in/yaml.v3"
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
	var definition struct {
		Permissions map[string]string `yaml:"permissions"`
		Jobs        map[string]struct {
			Needs           []string          `yaml:"needs"`
			If              string            `yaml:"if"`
			Permissions     map[string]string `yaml:"permissions"`
			ContinueOnError bool              `yaml:"continue-on-error"`
			Outputs         map[string]string `yaml:"outputs"`
			Steps           []struct {
				ID              string            `yaml:"id"`
				Uses            string            `yaml:"uses"`
				Run             string            `yaml:"run"`
				ContinueOnError bool              `yaml:"continue-on-error"`
				With            map[string]string `yaml:"with"`
			} `yaml:"steps"`
		} `yaml:"jobs"`
	}
	// Other jobs use scalar `needs`; decode only the release chain below.
	var document yaml.Node
	if err := yaml.Unmarshal(workflow, &document); err != nil {
		t.Fatalf("parse Dart SDK workflow: %v", err)
	}
	root := document.Content[0]
	for i := 0; i < len(root.Content); i += 2 {
		if root.Content[i].Value != "jobs" {
			continue
		}
		jobs := root.Content[i+1]
		for j := 1; j < len(jobs.Content); j += 2 {
			job := jobs.Content[j]
			for k := 0; k < len(job.Content); k += 2 {
				if job.Content[k].Value == "needs" && job.Content[k+1].Kind == yaml.ScalarNode {
					value := job.Content[k+1]
					job.Content[k+1] = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{value}}
				}
			}
		}
	}
	if err := document.Decode(&definition); err != nil {
		t.Fatalf("decode Dart SDK workflow: %v", err)
	}
	if !reflect.DeepEqual(definition.Permissions, map[string]string{"contents": "read"}) {
		t.Errorf("workflow default permissions must be contents-read only: %v", definition.Permissions)
	}

	expected := map[string]struct {
		needs       []string
		permissions map[string]string
		condition   string
		contracts   []string
	}{
		"release-preflight": {
			needs:       []string{"gate"},
			permissions: map[string]string{"contents": "read"},
			condition:   "startsWith(github.ref, 'refs/tags/sdks/dart/v')",
			contracts: []string{
				`[[ "$TAG" =~ ^sdks/dart/v[0-9]+\.[0-9]+\.[0-9]+$ ]]`,
				`test "$(git rev-parse HEAD)" = "$GITHUB_SHA"`,
				`test "$(git rev-parse "refs/tags/$TAG^{commit}")" = "$GITHUB_SHA"`,
				`test "$(awk '$1 == "version:" { print $2; exit }' sdks/dart/pubspec.yaml)" = "$version"`,
				`grep -Fx "## $version" sdks/dart/CHANGELOG.md`,
				`git archive "$GITHUB_SHA:sdks/dart"`,
				"dart pub get --enforce-lockfile --no-example",
				`dart pub publish -C "$source_dir" --to-archive="$archive"`,
				`tar -xzf "$archive" -C "$package_dir"`,
				`test ! -e "$package_dir/offline"`,
				`test ! -e "$package_dir/example/pubspec.yaml"`,
				`PUB_CACHE="$isolated_pub_cache" dart pub get --no-example`,
				`PUB_CACHE="$isolated_pub_cache" dart analyze`,
				`PUB_CACHE="$isolated_pub_cache" dart test`,
				"python3 sdks/dart/scripts/release.py preflight",
			},
		},
		"publish": {
			needs:       []string{"release-preflight"},
			permissions: map[string]string{"contents": "read", "id-token": "write"},
			condition:   "needs.release-preflight.outputs.publish_required == 'true'",
			contracts: []string{
				`echo "$ARCHIVE_SHA256  $archive" | sha256sum --check --strict`,
				`dart pub publish --force --from-archive="$archive"`,
			},
		},
		"verify-published": {
			needs:       []string{"release-preflight", "publish"},
			permissions: map[string]string{"contents": "read"},
			condition:   "${{ !cancelled() && needs.release-preflight.result == 'success' && (needs.publish.result == 'success' || (needs.publish.result == 'skipped' && needs.release-preflight.outputs.publish_required == 'false')) }}",
			contracts:   []string{"python3 sdks/dart/scripts/release.py verify", `--sha256 "$ARCHIVE_SHA256"`},
		},
		"release": {
			needs:       []string{"release-preflight", "verify-published"},
			permissions: map[string]string{"contents": "write"},
			condition:   "${{ !cancelled() && needs.release-preflight.result == 'success' && needs.verify-published.result == 'success' }}",
			contracts: []string{
				`test "$TAG" = "sdks/dart/v$VERSION"`,
				`gh release create "$TAG" --title "$TAG"`,
				`gh release edit "$TAG" --title "$TAG"`,
			},
		},
	}
	for name, want := range expected {
		t.Run(name, func(t *testing.T) {
			job, ok := definition.Jobs[name]
			if !ok {
				t.Fatalf("missing release-chain job %s", name)
			}
			if !reflect.DeepEqual(job.Needs, want.needs) {
				t.Errorf("needs = %v; want %v", job.Needs, want.needs)
			}
			if !reflect.DeepEqual(job.Permissions, want.permissions) {
				t.Errorf("permissions = %v; want %v", job.Permissions, want.permissions)
			}
			if got := strings.Join(strings.Fields(job.If), " "); got != want.condition {
				t.Errorf("release gating condition = %q; want %q", got, want.condition)
			}
			if job.ContinueOnError {
				t.Error("release-chain job may not continue on error")
			}
			var commands strings.Builder
			for _, step := range job.Steps {
				commands.WriteString(step.Run)
				if step.ContinueOnError {
					t.Error("release-chain step may not continue on error")
				}
				if strings.HasPrefix(step.Uses, "actions/checkout@") {
					if name == "publish" || name == "release" {
						t.Error("privileged jobs may not check out repository code")
					}
					if step.With["ref"] != "${{ github.sha }}" || step.With["persist-credentials"] != "false" {
						t.Error("release checks must use the exact workflow SHA without persisted credentials")
					}
				}
				if strings.HasPrefix(step.Uses, "actions/download-artifact@") && step.With["artifact-ids"] != "${{ needs.release-preflight.outputs.archive_artifact_id }}" {
					t.Error("release-chain jobs must consume the immutable preflight artifact ID")
				}
				if name == "release" && strings.Contains(step.Uses, "setup-dart") {
					t.Error("Release writer may not set up pub.dev credentials")
				}
			}
			for _, contract := range want.contracts {
				if !strings.Contains(commands.String(), contract) {
					t.Errorf("missing release contract %q", contract)
				}
			}
		})
	}
	for name, job := range definition.Jobs {
		if job.Permissions["id-token"] == "write" && name != "publish" {
			t.Errorf("unexpected OIDC authority in job %s", name)
		}
		if job.Permissions["contents"] == "write" && name != "release" {
			t.Errorf("unexpected Release authority in job %s", name)
		}
		for _, step := range job.Steps {
			if strings.Contains(step.Run, "gh release ") && name != "release" {
				t.Errorf("job %s mutates/releases outside the verified Release writer", name)
			}
		}
	}
	for key, value := range map[string]string{
		"version":             "${{ steps.candidate.outputs.version }}",
		"archive_sha256":      "${{ steps.candidate.outputs.archive_sha256 }}",
		"archive_artifact_id": "${{ steps.archive.outputs.artifact-id }}",
		"publish_required":    "${{ steps.state.outputs.publish_required }}",
	} {
		if definition.Jobs["release-preflight"].Outputs[key] != value {
			t.Errorf("preflight output %s no longer carries verified candidate/state", key)
		}
	}
	if !strings.Contains(string(workflow), "python3 -B -m unittest discover -s scripts -p release_test.py") {
		t.Error("Dart CI must run the pub.dev failure-path and archive regression tests")
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
		`"sdks/dart/**"`,
		`"tests/integration/dart_offline_sqlite_test.dart"`,
		`"proto/**"`,
		`"buf.yaml"`,
		`"buf.gen.yaml"`,
		`"generate.go"`,
		`"go.mod"`,
		`"go.sum"`,
		`"go.work"`,
		`"go.work.sum"`,
		`"core/search/**"`,
		`"core/graphcache/**"`,
		`"server/**"`,
		`"testdata/search/**"`,
		"docs/decisions/0001-dart-mobile-transport.md",
		"docs/decisions/0002-dart-offline-repository-contract.md",
		`"AGENTS.md"`,
		`"README.md"`,
		"CONTRIBUTING.md",
		`".github/workflows/dart-sdk.yml"`,
		"name: Generate, analyze, and test",
		"dart pub get --enforce-lockfile --no-example",
		"dart analyze lib test",
		"if: needs.changes.outputs.full == 'true'",
		"bufbuild/buf-setup-action@a47c93e0b1648d5651a065437926377d060baa99",
		"version: 1.71.0",
		"name: Minimum Dart 3.11",
		"name: Build API documentation",
		"name: Build and verify isolated parent publish archive",
		`dart pub publish -C "$source_dir" --to-archive="$archive"`,
		`tar -xzf "$archive" -C "$package_dir"`,
		`mapfile -d '' pubspecs`,
		`PUB_CACHE="$isolated_pub_cache" dart pub get --no-example`,
		`PUB_CACHE="$isolated_pub_cache" dart analyze`,
		"dart pub global run pana --json",
		"name: Check Flutter example",
		"flutter test --no-pub integration_test/mobile_smoke_test.dart -d emulator-5554",
		"name: Start iOS simulator boot",
		"name: Wait for iOS simulator",
		"flutter build ios --debug --no-codesign --no-pub",
		"name: Run classified iOS native smoke",
		`bash tool/ios_smoke_ci.sh run-attempt "$DEVICE_ID" initial`,
		"name: Create independently clean retry simulator",
		"name: Retry classified iOS native smoke on fresh simulator",
		"name: Finalize bounded iOS launch diagnostics",
		"name: Upload bounded iOS launch diagnostics",
		"name: Require classified iOS smoke success",
		"name: Record exact Android revision evidence",
		"name: Record exact iOS revision evidence",
		`--arg commit "$GITHUB_SHA"`,
		`.recordedAt | fromdateiso8601`,
		`.contentFree == true`,
		`.physicalDevice == false`,
		"lantern-android-revision-${{ github.sha }}",
		"lantern-ios-revision-${{ github.sha }}",
		"name: Gate",
		"needs: [changes, test, minimum-dart, offline-test, offline-minimum-dart, offline-sqlite, android, ios]",
		"require_result offline-test \"$OFFLINE_TEST_RESULT\" success",
		"require_result offline-minimum-dart \"$OFFLINE_MINIMUM_DART_RESULT\" success",
		"require_result offline-sqlite \"$OFFLINE_SQLITE_RESULT\" success",
		"require_result minimum-dart \"$MINIMUM_DART_RESULT\" success",
		"require_result android \"$ANDROID_RESULT\" success",
		"require_result ios \"$IOS_RESULT\" success",
		"needs: [gate]",
	} {
		if !strings.Contains(text, contract) {
			t.Errorf("Dart SDK workflow is missing scoped-gate contract %q", contract)
		}
	}

	pubignore, err := os.ReadFile(filepath.Join(repoRoot, "sdks", "dart", ".pubignore"))
	if err != nil {
		t.Fatalf("read parent Dart .pubignore: %v", err)
	}
	pubignoreText := "\n" + string(pubignore) + "\n"
	for _, excluded := range []string{"example/*", "offline/", "offline_sqlite/"} {
		if !strings.Contains(pubignoreText, "\n"+excluded+"\n") {
			t.Errorf("parent Dart publish archive no longer excludes %q", excluded)
		}
	}
	if !strings.Contains(pubignoreText, "\n!example/lantern_client_example.dart\n") {
		t.Error("parent Dart publish archive no longer retains its standalone online example")
	}

	contributing, err := os.ReadFile(filepath.Join(repoRoot, "CONTRIBUTING.md"))
	if err != nil {
		t.Fatalf("read CONTRIBUTING.md: %v", err)
	}
	if !strings.Contains(string(contributing), "lib test tool && dart pub get --enforce-lockfile") {
		t.Error("documented offline format gate does not include tool sources")
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
		"name: Run classified iOS native smoke",
		"name: Create independently clean retry simulator",
		"name: Retry classified iOS native smoke on fresh simulator",
		"name: Finalize bounded iOS launch diagnostics",
		"name: Upload bounded iOS launch diagnostics",
		"name: Require classified iOS smoke success",
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
	for _, contract := range []string{
		"timeout-minutes: 45",
		"IOS_SMOKE_LAUNCH_TIMEOUT_SECONDS: 180",
		"IOS_SMOKE_TOTAL_TIMEOUT_SECONDS: 480",
		"steps.ios_smoke_initial.outputs.classification == 'launch_stall'",
		`test "$retry_device" != "$DEVICE_ID"`,
		"if: always()",
		"lantern-ios-smoke-diagnostics-${{ github.sha }}-${{ github.run_attempt }}",
		"if: always() && steps.ios_diagnostics_finalize.outcome == 'success'",
		`echo "SMOKE_DEVICE_ID=$RETRY_DEVICE_ID"`,
		`--arg id "$SMOKE_DEVICE_ID"`,
		`xcrun simctl delete "$retry_device"`,
		"- name: Record exact iOS revision evidence\n        if: success()",
		"- name: Upload sanitized iOS revision evidence\n        if: success()",
	} {
		if !strings.Contains(ios, contract) {
			t.Errorf("iOS job is missing classified launch contract %q", contract)
		}
	}
	if strings.Contains(ios, `simctl erase "$DEVICE_ID"`) {
		t.Error("iOS launch retry still erases and reuses the wedged simulator")
	}
	for _, contract := range []string{
		"IOS_SMOKE_LAUNCH_TIMEOUT_SECONDS: 180",
		"IOS_SMOKE_TOTAL_TIMEOUT_SECONDS: 480",
		"timeout-minutes: 10",
	} {
		if got := strings.Count(ios, contract); got != 2 {
			t.Errorf("both iOS attempts must retain %q; got %d occurrences", contract, got)
		}
	}

	helper, err := os.ReadFile(filepath.Join(repoRoot, "sdks", "dart", "example", "tool", "ios_smoke_ci.sh"))
	if err != nil {
		t.Fatalf("read iOS smoke helper: %v", err)
	}
	helperText := string(helper)
	logHelper, err := os.ReadFile(filepath.Join(repoRoot, "sdks", "dart", "example", "tool", "ios_smoke_log.py"))
	if err != nil {
		t.Fatalf("read iOS smoke log helper: %v", err)
	}
	logHelperText := string(logHelper)
	for _, contract := range []string{
		"IOS_SMOKE_LAUNCH_TIMEOUT_SECONDS:-180",
		"IOS_SMOKE_TOTAL_TIMEOUT_SECONDS:-480",
		"record_classification launch_stall",
		"classification=test_failure",
		"capture_diagnostics",
		"finalize_diagnostics",
		"simctl listapps",
		"simctl get_app_container",
		"log show --last 5m",
		`python3 "$log_helper" stream "$log" "$phases"`,
		`[[ -f "$phases/body_started" ]]`,
		`[[ -f "$phases/build_done" ]]`,
		"simctl create",
		`[[ "$retry_device" != "$source_device" ]]`,
	} {
		if !strings.Contains(helperText, contract) {
			t.Errorf("iOS smoke helper is missing contract %q", contract)
		}
	}
	for _, contract := range []string{
		"MOBILE_SMOKE_BODY_STARTED",
		"Xcode build done.",
		"MAX_FILE_BYTES = 262144",
		"MAX_ARTIFACT_BYTES = 2097152",
		"MAX_FILES = 32",
		"<redacted-url>",
	} {
		if !strings.Contains(logHelperText, contract) {
			t.Errorf("iOS smoke log helper is missing contract %q", contract)
		}
	}
	if strings.Contains(helperText, `tee -a "$log"`) {
		t.Error("iOS smoke helper still writes an unbounded live log")
	}
	if got := strings.Count(helperText, `if ! kill -0 "$runner_pid" 2>/dev/null; then`); got != 2 {
		t.Errorf("iOS smoke helper must recheck runner liveness before both timeout kills; got %d checks", got)
	}

	for _, contract := range []string{
		"routes backend/search-only changes through the current-Dart unit and real-wire gates",
		"stable `Gate` job",
		"required result set for either scope",
		"Only that silent `launch_stall` may retry",
		"full attempt to 480 seconds",
		"allows 180 seconds after `Xcode build done.`",
		"outer step remains 10 minutes",
		"newly\ncreated simulator",
		"diagnostics are always\nuploaded",
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

// TestReleaseDoesNotDeployGKE keeps artifact publication independent of the
// retired project-managed GKE deployment target.
func TestReleaseDoesNotDeployGKE(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}

	releaseWorkflow, err := os.ReadFile(filepath.Join(repoRoot, ".github", "workflows", "docker-publish.yml"))
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	for _, retired := range []string{
		"deploy-gke",
		"GKE_CLUSTER",
		"GCP_DEPLOY_SA",
		"google-github-actions/get-gke-credentials",
	} {
		if strings.Contains(string(releaseWorkflow), retired) {
			t.Errorf("release workflow retains retired GKE deployment reference %q", retired)
		}
	}
	if _, err := os.Stat(filepath.Join(repoRoot, ".github", "workflows", "_deploy-gke.yml")); !os.IsNotExist(err) {
		t.Errorf("retired GKE workflow must be absent; stat error: %v", err)
	}
}

// TestHelmDeploymentDefaults preserves the chart's startup, discovery, and
// optional monitoring contracts independently of any deployment provider.
func TestHelmDeploymentDefaults(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}

	chartDir := filepath.Join(repoRoot, "deploy", "helm", "lantern")
	values, err := os.ReadFile(filepath.Join(chartDir, "values.yaml"))
	if err != nil {
		t.Fatalf("read Helm values: %v", err)
	}
	if !regexp.MustCompile(`(?m)^enableServiceLinks:\s+false$`).Match(values) {
		t.Error("Helm defaults must disable Kubernetes ServiceLinks")
	}
	metricKeep := regexp.MustCompile(`(?m)^\s*regex:\s*(.+)$`).FindSubmatch(values)
	if len(metricKeep) != 2 {
		t.Fatal("Helm defaults must define one GMP metric allowlist")
	}
	keep, err := regexp.Compile("^(?:" + strings.TrimSpace(string(metricKeep[1])) + ")$")
	if err != nil {
		t.Fatalf("compile GMP metric allowlist: %v", err)
	}
	for _, metric := range []string{
		"grpc_server_handling_seconds_bucket",
		"lantern_anti_entropy_cycles_total",
		"lantern_anti_entropy_gaps_found_total",
		"lantern_replication_lag_seq",
		"lantern_replication_dropped_total",
		"lantern_search_config_match",
		"lantern_search_index_healthy",
		"lantern_snapshot_replayed_total",
		"lantern_backup_failures_total",
		"lantern_vertices",
		"process_resident_memory_bytes",
	} {
		if !keep.MatchString(metric) {
			t.Errorf("GMP metric allowlist drops required signal %q", metric)
		}
	}
	for _, metric := range []string{
		"lantern_illuminate_calls_total",
		"lantern_illuminate_duration_seconds_bucket",
		"lantern_search_duration_seconds_bucket",
		"lantern_scan_duration_seconds_bucket",
		"lantern_batch_size_bucket",
		"go_memstats_mallocs_total",
	} {
		if keep.MatchString(metric) {
			t.Errorf("GMP metric allowlist retains high-volume signal %q", metric)
		}
	}
	headlessService, err := os.ReadFile(filepath.Join(chartDir, "templates", "service.yaml"))
	if err != nil {
		t.Fatalf("read Helm headless Service template: %v", err)
	}
	if !regexp.MustCompile(`(?m)^\s*publishNotReadyAddresses:\s+true$`).Match(headlessService) {
		t.Error("Helm peer-discovery Service must publish bootstrapping pods")
	}
	statefulSet, err := os.ReadFile(filepath.Join(chartDir, "templates", "statefulset.yaml"))
	if err != nil {
		t.Fatalf("read Helm StatefulSet template: %v", err)
	}
	for _, contract := range []string{
		"{{- with .Values.runtime.goMemoryLimit }}",
		"- name: GOMEMLIMIT",
		"value: {{ . | quote }}",
		"startupProbe:",
		"initialDelaySeconds: {{ .Values.probes.startup.initialDelaySeconds }}",
		"periodSeconds: {{ .Values.probes.startup.periodSeconds }}",
		"timeoutSeconds: {{ .Values.probes.startup.timeoutSeconds }}",
		"failureThreshold: {{ .Values.probes.startup.failureThreshold }}",
	} {
		if !strings.Contains(string(statefulSet), contract) {
			t.Errorf("Helm StatefulSet is missing startup guard %q", contract)
		}
	}
	for _, contract := range []string{
		"runtime:",
		"  goMemoryLimit: 384MiB",
		"  startup:",
		"    initialDelaySeconds: 60",
		"    periodSeconds: 5",
		"    failureThreshold: 36",
	} {
		if !strings.Contains(string(values), contract) {
			t.Errorf("Helm defaults are missing startup budget %q", contract)
		}
	}
	for _, name := range []string{"statefulset.yaml", "admin-deployment.yaml", "mcp-deployment.yaml"} {
		template, err := os.ReadFile(filepath.Join(chartDir, "templates", name))
		if err != nil {
			t.Fatalf("read Helm template %s: %v", name, err)
		}
		if !strings.Contains(string(template), "enableServiceLinks: {{ .Values.enableServiceLinks }}") {
			t.Errorf("Helm template %s does not apply the ServiceLink policy", name)
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
