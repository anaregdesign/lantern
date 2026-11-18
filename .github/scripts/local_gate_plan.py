"""Canonical local gate commands; carry never removes a required step."""

from dataclasses import dataclass
from pathlib import Path


@dataclass(frozen=True)
class Step:
    name: str
    cwd: str
    command: tuple[str, ...]
    empty: bool = False
    # Argument indexes are pathname operands; every other argv value is literal.
    glob_args: tuple[int, ...] = ()
    reject_line_prefixes: tuple[str, ...] = ()

    def __post_init__(self):
        if (len(set(self.glob_args)) != len(self.glob_args)
                or any(type(i) is not int or not 0 < i < len(self.command) for i in self.glob_args)):
            raise ValueError("invalid pathname glob argument indexes")


def steps(evidence: Path) -> tuple[Step, ...]:
    plan = []
    def add(name, directory, *cmd, empty=False, glob_args=(), reject_line_prefixes=()):
        plan.append(Step(name, directory, tuple(cmd), empty, glob_args, reject_line_prefixes))
    add("harness-contracts", ".", "python3", "-B", ".github/scripts/harness_contracts.py")
    add("node-frozen-install", "sdks/node", "bun", "install", "--frozen-lockfile")
    add("node-prebuild", "sdks/node", "bun", "run", "build")
    add("admin-frozen-install", "admin", "bun", "install", "--frozen-lockfile", "--force")
    add("example-frozen-preflight", "sdks/dart/example", "flutter", "pub", "get", "--enforce-lockfile")
    add("gofmt", ".", "gofmt", "-l", ".", empty=True)
    for mod in [".", "core", "mcp", "pb", "sdks/go", "server"]:
        add("go-test-" + mod.replace("/", "-"), mod, "go", "test", "./...")
    add("server-bench-heap-tests", ".", "python3", "-B", ".github/scripts/verify_bench_heap.py")
    add("server-vet", "server", "go", "vet", "./...")
    add("go-build", ".", "go", "build", "./...")
    add("go-lint", ".", "make", "lint")
    for name, cmd in [("buf-format", ["format", "-d", "--exit-code"]), ("buf-lint", ["lint"])]:
        add(name, ".", "go", "run", "github.com/bufbuild/buf/cmd/buf@v1.70.0", *cmd)
    add("go-generate", ".", "go", "generate", "./...")
    add("wire-generate", "server", "go", "tool", "wire", "./cmd")
    add("envdoc-generate", "server", "go", "run", "./internal/envdoc/cmd", "-out", "../docs/env.md")
    add("node-generate", "sdks/node", "bun", "run", "codegen")
    add("dart-generate", ".", "bash", "sdks/dart/scripts/codegen.sh")
    add("rust-generate", "sdks/rust", "cargo", "xtask", "codegen")
    add("probe-generate", ".", "bash", "testbed/dart-transport-probe/scripts/codegen.sh")
    add("generated-drift", ".", "git", "status", "--porcelain", empty=True)
    add("probe-generated-drift", ".", "git", "status", "--porcelain", empty=True)
    for mod in [".", "core", "mcp", "pb", "sdks/go", "server"]:
        add("govulncheck-" + mod.replace("/", "-"), mod, "go", "run", "golang.org/x/vuln/cmd/govulncheck@v1.8.0", "./...")
    for script in ["lint", "format:check", "typecheck", "example:typecheck", "test", "build", "verify:package"]:
        add("node-" + script.replace(":", "-"), "sdks/node", "bun", "run", script)
    add("dart-format", "sdks/dart", "dart", "format", "--output=none", "--set-exit-if-changed", "lib/lantern_client.dart", "lib/src/*.dart", "test", glob_args=(5,))
    for name, cmd in [("get", ["pub", "get", "--enforce-lockfile"]), ("analyze", ["analyze", "lib", "test"]), ("test", ["test"]), ("doc", ["doc", "--output", str(evidence / "dartdoc"), "--validate-links"]), ("publish-dry-run", ["pub", "publish", "--dry-run"])]:
        add("dart-" + name, "sdks/dart", "dart", *cmd,
            reject_line_prefixes=("  warning:", "warning:") if name == "doc" else ())
    add("offline-format", "sdks/dart/offline", "dart", "format", "--output=none", "--set-exit-if-changed", "lib", "test", "tool")
    for name, cmd in [("get", ["dart", "pub", "get", "--enforce-lockfile"]), ("analyze", ["dart", "analyze"]), ("test", ["dart", "test"]), ("doc", ["dart", "doc", "--output", str(evidence / "offline-dartdoc"), "--validate-links"])]:
        add("offline-" + name, "sdks/dart/offline", "python3", "-B", "tool/paired_source_gate.py", "--", *cmd,
            reject_line_prefixes=("  warning:", "warning:") if name == "doc" else ())
    add("example-format", "sdks/dart/example", "dart", "format", "--output=none", "--set-exit-if-changed", "lib", "test", "integration_test")
    for name, cmd in [("analyze", ["analyze"]), ("test", ["test"])]:
        add("example-" + name, "sdks/dart/example", "flutter", *cmd)
    add("sqlite-format", "sdks/dart/offline_sqlite", "dart", "format", "--output=none", "--set-exit-if-changed", "lib", "test", "tool")
    for name, cmd in [("get", ["flutter", "pub", "get", "--enforce-lockfile"]), ("analyze", ["flutter", "analyze"]), ("test", ["flutter", "test"]), ("crash-probe", ["dart", "run", "tool/crash_probe.dart"]), ("performance-probe", ["dart", "run", "tool/performance_probe.dart"])]:
        add("sqlite-" + name, "sdks/dart/offline_sqlite", *cmd)
    add("rust-format", "sdks/rust", "cargo", "fmt", "--all", "--", "--check")
    add("rust-clippy", "sdks/rust", "cargo", "clippy", "--locked", "--all-targets", "--all-features", "--", "-D", "warnings")
    add("rust-test", "sdks/rust", "cargo", "test", "--locked", "--all-features")
    add("rust-doc", "sdks/rust", "cargo", "doc", "--locked", "--no-deps", "--all-features")
    add("rust-server-build", ".", "go", "test", "-c", "-tags=lantern_test_time", "-o", "sdks/rust/target/lantern-smoke", "./server/cmd")
    add("rust-authfixture-build", ".", "go", "build", "-o", "sdks/rust/target/lantern-authfixture", "./server/cmd/authfixture")
    # Public current authority is owned by the native-time SDK4 root gate;
    # the standalone server/authfixture cannot supply its provisioned cohort.
    add("rust-real-wire", "sdks/rust", "cargo", "test", "--locked", "--all-features", "--", "--ignored", "--test-threads=1", "--skip", "scoped_changes::tests::real_public_scoped_wire", "--skip", "security::tests::real_public_current_wire")
    # Complete controlled-source functional validation includes scoped Rust.
    # Configured-source integration remains an independent CI/release gate.
    add("current-functional-wire", ".", "python3", "-B", ".github/scripts/current_sdk4_gate.py", "run", "--proof-kind", "functional-validation", "--fresh-child", "--evidence", str(evidence / "current-sdk4"))
    add("native-login-wire", ".", "python3", "-B", ".github/scripts/native_login_gate.py", "--fresh-child", "--evidence", str(evidence / "native-login"))
    add("rust-audit", "sdks/rust", "cargo", "audit", "--deny", "warnings", "--file", "Cargo.lock")
    for script in ["format:check", "lint", "typecheck", "test", "build"]:
        add("admin-" + script.replace(":", "-"), "admin", "bun", "run", script)
    add("admin-playwright", "admin", "bun", "run", "test:e2e", "--workers=4")
    add("docs-tests", ".", "python3", "-B", "-m", "unittest", "discover", "-s", ".github/scripts", "-p", "test_*.py")
    add("final-diff", ".", "git", "diff", "--check")
    add("final-clean", ".", "git", "status", "--porcelain", empty=True)
    return tuple(plan)


def affected_steps(plan, scope, base="origin/main"):
    """Select unit suites, their actual prerequisites and cheap host contracts."""
    families = set(scope["families"])
    modules = set(scope["go_modules"])
    groups = scope.get("harness_contracts", [])
    fixtures = set(scope.get("fixture_contracts", []))
    names = {"final-diff", "final-clean"}
    extra = []
    if "release-host" in fixtures or scope["full"]:
        extra.append(Step("dart-release-host-contracts", ".", (
            "python3", "-B", "sdks/dart/scripts/release_host_checks.py", "--python-only")))
    elif "setup-dart" in scope.get("semantic", {}).values():
        extra.append(Step("dart-workflow-conditions", "sdks/dart/scripts", (
            "python3", "-B", "-m", "unittest", "ci_scope_test")))
    if "." in modules:
        # Root's ./... includes cross-SDK wire tests. Those belong to CI.
        extra.append(Step("go-test-cli", ".", ("go", "test", "./cli/...")))
    names.update("go-test-" + m.replace("/", "-") for m in modules - {"."})
    if scope["full"] or "bench-heap-observer" in fixtures:
        names.add("server-bench-heap-tests")
    if scope["full"]:
        # Unmapped inputs select broad unit coverage, never release workloads.
        extra.append(Step("go-test-tools", ".", ("go", "test", "./testbed/...", "./deploy/...")))
        names.add("docs-tests")
    if families & {"node", "admin"}:
        names.update(("node-frozen-install", "node-prebuild"))
    if "node" in families:
        names.add("node-test")
    if "admin" in families:
        names.update(("admin-frozen-install", "admin-test"))
    if "dart" in families:
        names.update(("dart-get", "dart-test"))
    if "offline" in families:
        # The offline-test wrapper already resolves its frozen paired dependencies.
        names.add("offline-test")
    if "sqlite" in families:
        names.update(("sqlite-get", "sqlite-test"))
    if "example" in families:
        names.update(("example-frozen-preflight", "example-test"))
    if "rust" in families:
        names.add("rust-test")
    if "docs" in families:
        extra.append(Step("docs-text-links", ".", (
            "python3", "-B", ".github/scripts/local_prose_gate.py", "documents", base, "HEAD")))
    if groups or scope["full"]:
        names.add("harness-contracts")
    final = {"final-diff", "final-clean"}
    selected = []
    for step in plan:
        if step.name not in names or step.name in final:
            continue
        if step.name == "harness-contracts" and groups:
            step = Step(step.name, step.cwd, step.command + tuple(
                arg for group in groups for arg in ("--group", group)))
        selected.append(step)
    # Check workflow conditions and release fixtures before slower unit suites.
    selected = tuple(extra) + tuple(selected)
    for step in plan:
        if step.name == "final-diff" and scope.get("base_sha"):
            selected += (Step(step.name, ".", ("git", "diff", "--check", base, "HEAD")),)
        elif step.name in final:
            selected += (step,)
    if "controlled-native-provider" not in fixtures:
        return selected
    # These are affected-only product contracts. The canonical full79 commands
    # stay unchanged, and no provider/serving child is selected by these patterns.
    python = () if "docs-tests" in names else (Step("controlled-native-python-contracts", ".", (
        "python3", "-B", "-m", "unittest", "discover", "-s", ".github/scripts", "-p",
        "test_controlled_native_provider_fixture.py")),)
    selector = (
        "^(TestControlledNativeProvider(Contracts|InputBoundaries|CohortContracts|CaptureFailureFinalizes|"
        "WindowContracts|OwnerWindowContracts|WholeOwnerCapacity|PublisherRenewal|NativeBoundsLeaseVerification|"
        "NativeLeaseMonitor|OwnerStopAcknowledgement|MonitorUsesOwnedNativeBounds)|"
        "TestControlledNativeWindowBounds(RefusesUnavailableWithoutHostFallback|IndependentOfManagementReadiness)|"
        "TestInheritedProfileControlsNeutralized|TestControlledFixtureClock(OwnsAdmissionPublicationAndObservations|"
        "PreservesFiniteBudgetAndModes))$"
    )
    tagged = (
        Step("server-controlled-native-contracts", "server", (
            "go", "test", "-tags=lantern_test_time", "-count=1", "-timeout=10m", "-run", selector,
            "./cmd", "./provider", "./internal/membershippublisher")),
        # Full maximum-horizon replay remains in required PR/main CI. Local
        # capture changes exercise their exact bounded subtests without that loop.
        Step("server-controlled-native-capture-contracts", "server", (
            "go", "test", "-tags=lantern_test_time", "-count=1", "-timeout=10m", "-run",
            "^TestControlledNativeProviderSignedRenewalTranscript$/"
            "^(captured_atomically_replaced_signed_inputs|bounded_open_ENOENT_capture|unsafe_renewable_input_refused)$",
            "./cmd")),
    )
    final = {"final-diff", "final-clean"}
    return (python + tuple(s for s in selected if s.name not in final) + tagged
            + tuple(s for s in selected if s.name in final))
