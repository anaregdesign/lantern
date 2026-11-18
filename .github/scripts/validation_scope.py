#!/usr/bin/env python3
"""Small changed-path map for fresh local checks and native Windows coverage."""

import json
import os
from pathlib import Path, PurePosixPath
import subprocess

import ci_docs
import dependency_scope
import evidence_only_pr


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
             "server/internal/config/",
             "server/internal/dataruntime/",
             "server/service/receipt", "server/service/runtime")
NATIVE_RUST = ("sdks/rust/src/transport", "sdks/rust/src/test_server.rs",
               "sdks/rust/src/value", "sdks/rust/src/smoke.rs")

# Finite read-only reader/preflight dependencies, not a blanket .github exemption.
# Other executable fixtures, workflows, selectors, build/security/protocol policy
# and new files stay full. These consumers still reject changed/missing measurement
# inputs; selecting their contracts never carries a benchmark or release PASS.
HARNESS_INPUTS = {
    **dict.fromkeys((".github/scripts/bench_time_profile.py",
                     ".github/scripts/test_bench_time_profile.py",
                     ".github/scripts/test_retained_heap_pair.py",
                     ".github/scripts/test_local_release_qualification.py"), "bench-time"),
    **dict.fromkeys((".github/scripts/verify_root_release_binaries.py",
                     ".github/scripts/test_verify_root_release_binaries.py"), "release-binaries"),
    **dict.fromkeys((".github/scripts/git_test_fixture.py",
                     ".github/scripts/test_git_test_fixture.py",
                     ".github/scripts/test_local_gate_session.py"), "git-fixture"),
}


PRODUCT_FIXTURE_INPUTS = dict.fromkeys((
    ".github/scripts/controlled_native_provider_fixture.py",
    ".github/scripts/test_controlled_native_provider_fixture.py",
    "server/cmd/controlled_native_provider_gate_test.go",
    "server/cmd/controlled_native_owner_window_gate_test.go",
    "server/cmd/controlled_native_lease_gate_test.go",
    "server/cmd/controlled_native_publisher_gate_test.go",
    "server/internal/membershippublisher/controlled_clock_fixture.go",
    "server/internal/membershippublisher/controlled_clock_fixture_test.go",
    "server/internal/membershippublisher/publisher.go",
), "controlled-native-provider")


# The Python-only evidence readers run on the host before physical collection.
RELEASE_HOST = {
    f"sdks/dart/{directory}/{name}{suffix}.py"
    for directory, names in [
        ("scripts", ("physical_contribution_release_gate", "release_host_checks")),
        ("offline/tool", ("physical_release_gate", "physical_receipt_attestation",
                          "physical_result_attestation", "physical_release_source", "release_contract")),
    ]
    for name in names for suffix in ("", "_test")
}

# Archive tooling also runs on the host; its changes select package/archive CI.
RELEASE_ARCHIVE = {"sdks/dart/scripts/release.py", "sdks/dart/scripts/release_test.py"}


def dependency_lock(path):
    """Dependency lock filenames, never clock/block/locking source names."""
    name = PurePosixPath(path).name
    return (name in {"package-lock.json", "npm-shrinkwrap.json", "lockfile"}
            or name.endswith((".lock", ".lockb", ".lock.json")))


def product_fixture_group(path, status):
    # Executable product composition is not a read-only harness exemption.
    # Keep this separate from harness_group: PR/main source Q remains required.
    return PRODUCT_FIXTURE_INPUTS.get(path) if status == "M" else None


def harness_group(path, status):
    # Additions/deletions can change the dependency graph; only existing regular
    # modifications have this reviewed closure. Git callers verify file modes.
    return HARNESS_INPUTS.get(path) if status == "M" else None


def harness_required(result):
    # Early real tools are useful for their changed inputs, not another minute
    # of duplicate work on every unrelated product edit. Manual broad runs keep
    # their independent preflight; normal module/security checks remain blocking.
    return result["full"] or result["generation"] or bool(result["harness_contracts"])


def full(reason):
    return dict(full=True, reason=reason, families=sorted(LANGUAGES),
                go_modules=list(GO_CONSUMERS), generation=True,
                windows_required=True, windows_rust_required=True, harness_contracts=[], fixture_contracts=[], semantic={})


def affected(changes, semantic=None, dependency_owners=()):
    if not changes:
        return full("empty or unavailable diff")
    semantic = semantic or {}
    prose = ci_docs.documentation_only(list(changes))
    families, modules, harness, fixtures = set(), set(), set(), set()
    for module in dependency_owners:
        modules.update(GO_CONSUMERS[module])
    generation = False
    backend_dependencies = bool(set(dependency_owners) & {"core", "pb", "server"})
    native_go = native_rust = backend_dependencies
    for path, status in changes.items():
        value = PurePosixPath(path)
        if (path != value.as_posix() or value.is_absolute() or ".." in value.parts
                or status not in {"A", "M", "D"}):
            return full("unknown path or change type")
        if path in semantic and semantic[path] != "package-dependency":
            continue
        if (path in RELEASE_HOST or path in RELEASE_ARCHIVE) and status == "M":
            fixtures.add("release-host")
            continue
        if prose:
            families.add("docs")
            continue
        if path.startswith("server/benchheap/") or (path.startswith("server/") and value.match("bench_heap*.go")):
            # Tagged observer unit tests do not run in an ordinary Go module test.
            fixtures.add("bench-heap-observer")
        if group := harness_group(path, status):
            harness.add(group)
            continue
        if group := product_fixture_group(path, status):
            fixtures.add(group)
            # A fixture consumer edit cannot change its production dependencies.
            # The ordinary publisher is production code and keeps that closure.
            if path == "server/internal/membershippublisher/publisher.go":
                modules |= GO_CONSUMERS["server"]
            native_go = native_rust = True
            continue
        if path in PRODUCT_FIXTURE_INPUTS:
            return full("new or deleted product fixture input")
        if (path.startswith("server/cmd/controlled_native_")
                or path.startswith("server/internal/membershippublisher/")
                and {"controlled", "fixture"} <= set(value.stem.split("_"))):
            return full("unmapped controlled-native fixture input")
        # Instructions/build policy cannot narrow its own qualification.
        if path not in semantic and (path.startswith((".github/", ".agents/", "deploy/"))
                or value.name in {"AGENTS.md", "CONTRIBUTING.md", "CLAUDE.md", "Makefile", "Dockerfile", "package.json"}
                or value.suffix in {".mod", ".sum", ".yaml", ".yml", ".toml", ".lock"}
                or dependency_lock(path) or "config" in value.name
                or "/generated/" in path or "/src/gen/" in path or "/xtask/" in path
                or "codegen" in value.name):
            return full("policy, configuration or dependency input: " + path)
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
            if path == "admin/test/cli-grammar/verbs.json":
                modules.add(".")
        elif path.startswith("sdks/rust/"):
            families.add("rust")
            native_rust |= path.startswith(NATIVE_RUST) or status != "M" or path in semantic
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
            native_rust |= path.startswith(("server/provider/", "server/cmd/", "server/internal/listenerlaunch/",
                                            "server/internal/config/"))
    return dict(full=False, reason="mapped affected inputs", semantic=semantic, families=sorted(families),
                backend_dependencies=backend_dependencies, dependency_owners=sorted(dependency_owners),
                go_modules=sorted(modules), generation=generation,
                windows_required=native_go or native_rust, windows_rust_required=native_rust,
                harness_contracts=sorted(harness), fixture_contracts=sorted(fixtures))


def changed(root, base, head="HEAD"):
    def git(*args):
        return subprocess.check_output(["git", *args], cwd=root)
    fields = git("diff", "--name-status", "--no-renames", "-z", base, head).decode().split("\0")
    if fields.pop() != "" or len(fields) % 2:
        raise ValueError("invalid Git diff")
    changes = dict(zip(fields[1::2], fields[0::2]))
    for path, status in changes.items():
        modes = set()
        for revision in ([head] if status == "A" else [base] if status == "D" else [base, head]):
            mode = git("ls-tree", revision, "--", path).split()[0]
            modes.add(mode)
            if mode not in {b"100644", b"100755"} or len(modes) > 1:
                raise ValueError("nonregular input")
    return changes


def classify_revisions(root, base, head):
    """Return one dependency decision for the complete committed range."""
    try:
        changes = changed(root, base, head)
        semantic, owners = dependency_scope.classify(root, base, head, changes, GO_CONSUMERS)
        result = affected(changes, semantic, owners)
        return result | hosted(result, changes) | {"evidence_candidate": evidence_only_pr.is_candidate(changes)}
    except (ValueError, OSError, AttributeError, IndexError, TypeError, subprocess.CalledProcessError) as error:
        result = full(f"classification unavailable: {type(error).__name__}")
        return result | hosted(result, {})


def hosted(result, changes):
    broad = result["full"]
    semantic = result["semantic"]
    production = [p for p in changes if p not in semantic and not ci_docs.is_document(p)
                  and not harness_group(p, changes[p]) and p not in RELEASE_HOST]
    backend = result.get("backend_dependencies", False) or any(p.startswith(("server/", "core/", "pb/", "proto/")) for p in production)
    go = bool(result["go_modules"])
    fixture = "controlled-native-provider" in result["fixture_contracts"]
    wire_dependency = (bool(set(result.get("dependency_owners", ())) & {"sdks/go", "mcp"}) or
                       any(p.startswith(("sdks/node/", "sdks/dart/", "sdks/rust/")) for p in semantic
                           if semantic[p] == "package-dependency"))
    authority = (broad or backend or fixture or wire_dependency or ".github/workflows/current-sdk4.yml" in semantic
                 or any(p.startswith(("sdks/go/", "sdks/node/src/", "sdks/rust/src/",
                                      "sdks/dart/lib/")) for p in production))
    languages = []
    if broad or go:
        languages.append({"language": "go", "build-mode": "autobuild"})
    if broad or any(p.startswith((".github/workflows/", ".github/actions/")) for p in changes):
        languages.append({"language": "actions", "build-mode": "none"})
    return dict(
        go_required=go, functional_required=authority,
        verify_dependencies=broad or bool(result.get("dependency_owners")),
        generation_required=broad or result.get("backend_dependencies", False) or result["generation"] or any(
            p.startswith(("server/cmd/", "server/provider/", "server/internal/config/",
                          "server/internal/envconfig/", "server/internal/envdoc/")) for p in production),
        server_checks_required=broad or backend or fixture,
        fuzz_required=broad or result.get("backend_dependencies", False) or any(p.endswith(".go") for p in production),
        deployment_required=broad or result.get("backend_dependencies", False) or any(p.startswith(("server/", "core/privatefile/")) for p in production),
        admin_required=broad or "admin" in result["families"] or any(
            p.startswith(("proto/", "server/provider/", "server/internal/config/")) for p in production),
        node_required=broad or result.get("backend_dependencies", False) or "node" in result["families"] or any(
            p.startswith(("server/", "proto/", "core/search/", "core/graphcache/", "sdks/go/")) for p in production),
        rust_required=broad or "rust" in result["families"] or backend,
        codeql_languages=languages,
    )


def local_scope(root, base):
    try:
        def resolve(ref):
            return subprocess.check_output(["git", "rev-parse", "--verify", ref + "^{commit}"],
                                           cwd=root, stderr=subprocess.DEVNULL).decode().strip()
        target = resolve("refs/remotes/origin/main")
        if resolve(base) != target or subprocess.run(["git", "merge-base", "--is-ancestor", target, "HEAD"],
                cwd=root, check=False, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
            return full("base must be fetched origin/main and an ancestor of HEAD")
        return classify_revisions(root, target, "HEAD") | {"base_sha": target}
    except Exception as error:
        return full("classification unavailable: " + type(error).__name__)


def main():
    revisions = None
    try:
        revisions = ci_docs.change_range(dict(os.environ))
        result = classify_revisions(Path.cwd(), *revisions) if revisions else full("manual or unavailable range")
    except Exception as error:
        result = full("classification unavailable: " + type(error).__name__)
    if os.environ.get("GITHUB_EVENT_NAME") == "pull_request" and result.get("evidence_candidate"):
        for key in ("node_required", "rust_required", "admin_required"):
            result[key] = False
        result["codeql_languages"] = []
    if "go_required" not in result:
        result.update(hosted(result, {}))
    result["harness_contracts_required"] = harness_required(result)
    print(json.dumps(result, sort_keys=True))
    with Path(os.environ["GITHUB_OUTPUT"]).open("a") as output:
        for name, value in result.items():
            if isinstance(value, bool) or name == "codeql_languages":
                output.write(f"{name}={json.dumps(value, separators=(',', ':'))}\n")
        output.write("go_modules=" + " ".join(result["go_modules"]) + "\n")
    if path := os.environ.get("GITHUB_STEP_SUMMARY"):
        with Path(path).open("a") as summary:
            summary.write("Affected validation: " + json.dumps(result, sort_keys=True) + "\n")


if __name__ == "__main__":
    main()
