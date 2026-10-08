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

    def __post_init__(self):
        if (len(set(self.glob_args)) != len(self.glob_args)
                or any(type(i) is not int or not 0 < i < len(self.command) for i in self.glob_args)):
            raise ValueError("invalid pathname glob argument indexes")


def steps(evidence: Path) -> tuple[Step, ...]:
    plan = []
    def add(name, directory, *cmd, empty=False, glob_args=()):
        plan.append(Step(name, directory, tuple(cmd), empty, glob_args))
    add("node-frozen-install", "sdks/node", "bun", "install", "--frozen-lockfile")
    add("node-prebuild", "sdks/node", "bun", "run", "build")
    add("admin-frozen-install", "admin", "bun", "install", "--frozen-lockfile", "--force")
    add("example-frozen-preflight", "sdks/dart/example", "flutter", "pub", "get", "--enforce-lockfile")
    add("gofmt", ".", "gofmt", "-l", ".", empty=True)
    for mod in [".", "core", "mcp", "pb", "sdks/go", "server"]:
        add("go-test-" + mod.replace("/", "-"), mod, "go", "test", "./...")
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
        add("dart-" + name, "sdks/dart", "dart", *cmd)
    add("offline-format", "sdks/dart/offline", "dart", "format", "--output=none", "--set-exit-if-changed", "lib", "test", "tool")
    for name, cmd in [("get", ["dart", "pub", "get", "--enforce-lockfile"]), ("analyze", ["dart", "analyze"]), ("test", ["dart", "test"]), ("doc", ["dart", "doc", "--output", str(evidence / "offline-dartdoc"), "--validate-links"])]:
        add("offline-" + name, "sdks/dart/offline", "python3", "-B", "tool/paired_source_gate.py", "--", *cmd)
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
    add("rust-server-build", ".", "go", "build", "-o", "sdks/rust/target/lantern-smoke", "./server/cmd")
    add("rust-authfixture-build", ".", "go", "build", "-o", "sdks/rust/target/lantern-authfixture", "./server/cmd/authfixture")
    add("rust-real-wire", "sdks/rust", "cargo", "test", "--locked", "--all-features", "--", "--ignored", "--test-threads=1", "--skip", "scoped_changes::tests::real_public_scoped_wire")
    add("rust-oidc-scoped-wire", ".", "go", "test", "./tests/integration", "-run", "^TestAuth_OIDCRustScopedChangesFacadeRealConnect$", "-count=1", "-v")
    add("rust-audit", "sdks/rust", "cargo", "audit", "--deny", "warnings", "--file", "Cargo.lock")
    for script in ["format:check", "lint", "typecheck", "test", "build"]:
        add("admin-" + script.replace(":", "-"), "admin", "bun", "run", script)
    add("admin-playwright", "admin", "bun", "run", "test:e2e", "--workers=4")
    add("docs-tests", ".", "python3", "-B", "-m", "unittest", "discover", "-s", ".github/scripts", "-p", "test_*.py")
    add("final-diff", ".", "git", "diff", "--check")
    add("final-clean", ".", "git", "status", "--porcelain", empty=True)
    return tuple(plan)
