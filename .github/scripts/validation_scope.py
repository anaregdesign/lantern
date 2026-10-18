#!/usr/bin/env python3
"""Small changed-path map for fresh local checks and native Windows coverage."""

import json
import os
from pathlib import Path, PurePosixPath
import subprocess

import ci_docs


GO_CONSUMERS = {
    "core": {"core", "server", "."},
    "pb": {"pb", "sdks/go", "mcp", "server", "."},
    "sdks/go": {"sdks/go", "mcp", "."},
    "mcp": {"mcp", "."}, "server": {"server", "."}, ".": {"."},
}
LANGUAGES = {"node", "admin", "dart", "offline", "sqlite", "example", "rust"}
NATIVE_GO = ("core/privatefile/", "core/mutationlog/", "core/mutationreceipt/",
             "core/hlc/", "server/backup/", "server/provider/", "server/cmd/",
             "server/internal/security/", "server/internal/authn/",
             "server/internal/listenerlaunch/", "server/internal/envconfig/",
             "server/service/receipt", "server/service/runtime")
NATIVE_RUST = ("sdks/rust/src/transport", "sdks/rust/src/test_server.rs",
               "sdks/rust/src/value", "sdks/rust/src/smoke.rs")


def full(reason):
    return dict(full=True, reason=reason, families=sorted(LANGUAGES),
                go_modules=list(GO_CONSUMERS), generation=True,
                windows_required=True, windows_rust_required=True)


def affected(changes):
    if not changes:
        return full("empty or unavailable diff")
    families, modules = set(), set()
    native_go = native_rust = generation = False
    for path, status in changes.items():
        value = PurePosixPath(path)
        if (path != value.as_posix() or value.is_absolute() or ".." in value.parts
                or status not in {"A", "M", "D"}):
            return full("unknown path or change type")
        # Instructions/build policy cannot narrow its own qualification.
        if (path.startswith((".github/", ".agents/", "deploy/"))
                or value.name in {"AGENTS.md", "CONTRIBUTING.md", "CLAUDE.md", "Makefile", "Dockerfile", "package.json"}
                or value.suffix in {".mod", ".sum", ".yaml", ".yml", ".toml", ".lock"}
                or "lock" in value.name or "config" in value.name
                or "/generated/" in path or "/src/gen/" in path or "/xtask/" in path
                or "codegen" in value.name):
            return full("policy, configuration or dependency input")
        if ci_docs.is_document(path):
            families.add("docs")
            continue
        if path.startswith("proto/") or path in {"generate.go", "buf.lock"}:
            families |= LANGUAGES
            modules |= set(GO_CONSUMERS)
            generation = native_go = native_rust = True
        elif path.startswith("sdks/node/"):
            families |= {"node", "admin"}
        elif path.startswith("admin/"):
            families.add("admin")
        elif path.startswith("sdks/rust/"):
            families.add("rust")
            native_rust |= path.startswith(NATIVE_RUST) or status != "M"
        elif path.startswith("sdks/dart/offline_sqlite/"):
            families |= {"sqlite", "example"}
        elif path.startswith("sdks/dart/offline/"):
            families |= {"offline", "sqlite", "example"}
        elif path.startswith("sdks/dart/example/"):
            families.add("example")
        elif path.startswith("sdks/dart/"):
            families |= {"dart", "offline", "sqlite", "example"}
        elif path.startswith(("tests/", "testdata/", "testbed/")):
            # Shared wire fixtures can consume every SDK. Do not guess their graph.
            return full("shared integration or fixture input")
        else:
            owner = next((m for m in GO_CONSUMERS if m != "." and path.startswith(m + "/")), None)
            if owner is None and (path.startswith("cli/") or ("/" not in path and path.endswith(".go"))):
                owner = "."
            if owner is None or not path.endswith(".go"):
                return full("unmapped input: " + path)
            modules |= GO_CONSUMERS[owner]
            if owner == "pb":
                families |= LANGUAGES
                generation = native_go = native_rust = True
            native_go |= path.startswith(NATIVE_GO) or "_windows" in value.name or status != "M"
            # Backend native composition also changes the Windows Rust fixture.
            native_rust |= path.startswith(("server/provider/", "server/cmd/", "server/internal/listenerlaunch/"))
    if families == {"docs"}:
        return full("documentation uses the existing explicit prose gate")
    return dict(full=False, reason="mapped affected inputs", families=sorted(families),
                go_modules=sorted(modules), generation=generation,
                windows_required=native_go or native_rust, windows_rust_required=native_rust)


def changed(root, base, head="HEAD"):
    def git(*args):
        return subprocess.check_output(["git", *args], cwd=root)
    base = git("merge-base", base, head).decode().strip()
    fields = git("diff", "--name-status", "--no-renames", "-z", base, head).decode().split("\0")
    if fields.pop() != "" or len(fields) % 2:
        raise ValueError("invalid Git diff")
    changes = dict(zip(fields[1::2], fields[0::2]))
    for path, status in changes.items():
        for revision in ([head] if status == "A" else [base] if status == "D" else [base, head]):
            mode = git("ls-tree", revision, "--", path).split()[0]
            if mode not in {b"100644", b"100755"}:
                raise ValueError("nonregular input")
    return changes


def local_scope(root, base):
    try:
        def resolve(ref):
            return subprocess.check_output(["git", "rev-parse", "--verify", ref + "^{commit}"],
                                           cwd=root, stderr=subprocess.DEVNULL).decode().strip()
        target = resolve("refs/remotes/origin/main")
        if resolve(base) != target or subprocess.run(["git", "merge-base", "--is-ancestor", target, "HEAD"],
                cwd=root, check=False, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
            return full("base must be fetched origin/main and an ancestor of HEAD")
        return affected(changed(root, target))
    except Exception as error:
        return full("classification unavailable: " + type(error).__name__)


def main():
    try:
        revisions = ci_docs.change_range(dict(os.environ))
        result = affected(changed(Path.cwd(), *revisions)) if revisions else full("manual or unavailable range")
    except Exception as error:
        result = full("classification unavailable: " + type(error).__name__)
    print(json.dumps(result, sort_keys=True))
    with Path(os.environ["GITHUB_OUTPUT"]).open("a") as output:
        for name in ("windows_required", "windows_rust_required"):
            output.write(f"{name}={str(result[name]).lower()}\n")
    if path := os.environ.get("GITHUB_STEP_SUMMARY"):
        with Path(path).open("a") as summary:
            summary.write("Native coverage selection: " + json.dumps(result, sort_keys=True) + "\n")


if __name__ == "__main__":
    main()
