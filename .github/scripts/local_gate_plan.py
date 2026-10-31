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


def affected_steps(plan, scope):
    """Select fresh development checks; full fallback retains the exact old plan."""
    if scope["full"]:
        return plan
    families, modules = set(scope["families"]), set(scope["go_modules"])
    names = {"final-diff", "final-clean"}
    if modules:
        # Go root integration consumes the built Node SDK. Complete installation
        # before Go traverses node_modules; never mutate it concurrently (#1646).
        names |= {"node-frozen-install", "node-prebuild", "admin-frozen-install",
                  "example-frozen-preflight", "gofmt", "go-build", "go-lint"}
        names |= {"go-test-" + module.replace("/", "-") for module in modules}
        if "server" in modules:
            names |= {"server-vet", "server-bench-heap-tests"}
    prefixes = {"node": "node-", "admin": "admin-", "dart": "dart-",
                "offline": "offline-", "sqlite": "sqlite-", "example": "example-",
                "rust": "rust-"}
    # Affected development checks defer distribution/current functional/native
    # wire/performance qualification to the existing hosted/milestone gates.
    deferred = {"node-verify-package", "dart-publish-dry-run", "sqlite-performance-probe",
                "rust-server-build", "rust-authfixture-build", "rust-real-wire",
                "current-functional-wire", "rust-audit"}
    generation = {s.name for s in plan if s.name.endswith("-generate")} | {"go-generate", "generated-drift", "probe-generated-drift"}
    for family, prefix in prefixes.items():
        if family in families:
            names |= {s.name for s in plan if s.name.startswith(prefix)} - deferred - generation
    if "admin" in families:
        names |= {"node-frozen-install", "node-prebuild"}
    if "example" in families:
        names.add("example-frozen-preflight")
    if "docs" in families:
        names.add("docs-tests")
    groups = scope.get("harness_contracts", [])
    if groups:
        names.add("harness-contracts")
    if scope["generation"]:
        names |= generation
    return tuple(Step(s.name, s.cwd, s.command + tuple(
        arg for group in groups for arg in ("--group", group)), s.empty,
        s.glob_args, s.reject_line_prefixes) if s.name == "harness-contracts" else s
        for s in plan if s.name in names)
