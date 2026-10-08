#!/usr/bin/env python3
"""Local, live-process validation carry. No receipt import or resume interface."""

from __future__ import annotations

import argparse
from contextlib import contextmanager, ExitStack
from dataclasses import dataclass
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import platform
import re
import shlex
import shutil
import subprocess
import sys
import tarfile
import tempfile
import time

import ci_docs
from local_gate_plan import Step, steps


GO_DIRS = ("core/", "pb/", "mcp/", "server/", "sdks/go/", "cli/")
SHARED_DIRS = ("proto/", "tests/", "testbed/", "testdata/")
KNOWN_DIRS = GO_DIRS + SHARED_DIRS + ("admin/", "sdks/dart/", "sdks/node/",
                                     "sdks/rust/", "sdks/python.archived/", "deploy/", "docs/", ".github/", ".vscode/")
ROOT_INPUTS = {"go.mod", "go.sum", "go.work", "go.work.sum", "generate.go",
               "Makefile", "Dockerfile", "LICENSE", "CLAUDE.md", "AGENTS.md",
               "CONTRIBUTING.md", "README.md", "buf.yaml", "buf.gen.yaml", "buf.lock",
               ".dockerignore", ".gitattributes", ".gitignore", ".golangci.yml",
               ".goreleaser.yaml", ".mailmap"}
ELIGIBLE = {"go-test-core", "go-test-mcp", "go-test-pb", "go-test-sdks-go",
            "go-test-server", "server-vet"}
# Outputs of the fixed installation/build steps. These are never exported into
# eligible execution inputs; their owning unsupported gates still execute.
BUILD_OUTPUT_DIRS = (".github/scripts/__pycache__/", "sdks/node/node_modules/", "admin/node_modules/",
                    "sdks/node/dist/", "admin/dist/", "sdks/rust/target/",
                    "sdks/dart/.dart_tool/", "sdks/dart/offline/.dart_tool/",
                    "sdks/dart/example/.dart_tool/", "sdks/dart/example/build/",
                    "sdks/dart/offline_sqlite/.dart_tool/", "sdks/dart/offline_sqlite/build/",
                    "testbed/dart-transport-probe/connect/.dart_tool/",
                    "testbed/dart-transport-probe/grpc/.dart_tool/")


def digest(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def encoded(value: object) -> bytes:
    return (json.dumps(value, sort_keys=True, separators=(",", ":")) + "\n").encode()


def stable_go_settings(configuration: dict) -> dict:
    settings = configuration.copy()
    # go env constructs a fresh builder. GOGCCFLAGS includes its random work
    # directory mapped to the fixed /tmp/go-build output (cmd/go/internal/work).
    # Preserve every flag, map kind/base/destination and all user CGO settings;
    # normalize only the generated decimal work-directory suffix. Go marks
    # GOGCCFLAGS read-only, and the producing toolchain source is fingerprinted.
    flags = shlex.split(settings["GOGCCFLAGS"])
    generated = 0
    for index, flag in enumerate(flags):
        match = re.fullmatch(r"-(ffile|fdebug)-prefix-map=(/.*?/)go-build[0-9]+=/tmp/go-build", flag)
        if match:
            generated += 1
            flags[index] = f"-{match[1]}-prefix-map={match[2]}go-build<work>=/tmp/go-build"
    if generated > 1:
        raise ValueError("ambiguous generated Go compiler work-directory flags")
    settings["GOGCCFLAGS"] = flags
    return settings


def known(path: str) -> bool:
    value = PurePosixPath(path)
    return (path == value.as_posix() and not value.is_absolute() and ".." not in value.parts
            and (path in ROOT_INPUTS or path.startswith(KNOWN_DIRS)
                 or ci_docs.is_document(path) or (len(value.parts) == 1 and value.suffix == ".go")))


def policy(path: str) -> bool:
    # Changes to any dependency/lock/config/gate input invalidate every receipt.
    value = PurePosixPath(path)
    return (path.startswith((".github/", ".agents/", ".vscode/"))
            or value.name in {"AGENTS.md", "CONTRIBUTING.md", "CLAUDE.md", "Makefile", "Dockerfile"}
            or value.suffix in {".yaml", ".yml", ".toml", ".lock", ".mod", ".sum"}
            or "lock" in value.name or "config" in value.name
            or (len(value.parts) == 1 and path.startswith(".")))


def inputs(step: Step, tree: dict[str, str]) -> dict[str, str]:
    # Go workspace selection couples all module manifests; include all Go source,
    # fixtures and generated inputs conservatively rather than assuming a DAG.
    if step.name in ELIGIBLE:
        return {p: h for p, h in tree.items()
                if policy(p) or p in ROOT_INPUTS or p.startswith(GO_DIRS + SHARED_DIRS)
                or ("/" not in p and p.endswith(".go"))}
    # Unsupported components, including every SDK real-wire gate, always run.
    # Their full-tree closure also propagates backend/proto/shared fixture inputs.
    return tree


@dataclass(frozen=True)
class Receipt:
    fingerprint: str
    head: str
    tree: str
    log: Path
    log_hash: str


class Session:
    def __init__(self, root: Path, evidence: Path):
        self.root = root.resolve()
        self.evidence = evidence.resolve()
        if self.evidence == self.root or self.root in self.evidence.parents:
            raise ValueError("evidence must be outside the checkout")
        self.evidence.mkdir(parents=True, exist_ok=False)
        self.env = os.environ.copy()
        self.env["RUSTDOCFLAGS"] = "-D warnings"
        self.plan = steps(self.evidence)
        self.receipts: dict[str, Receipt] = {}
        self.prior_tree: dict[str, str] | None = None
        self.prior_policy: str | None = None
        self.prior_context: str | None = None
        self.prior_branch: str | None = None
        self.prior_head: str | None = None
        self.report: tuple[Path, str] | None = None
        self.baseline_logs: dict[Path, str] = {}
        self.eligible_root: Path | None = None
        self.attempt = 0
        self.runner_hashes = {p: digest(p.read_bytes()) for p in (
            Path(__file__).resolve(), Path(__file__).with_name("local_gate_plan.py").resolve(),
            Path(ci_docs.__file__).resolve())}

    def git(self, *args: str) -> str:
        return subprocess.check_output(["git", *args], cwd=self.root, text=True).strip()

    def snapshot(self) -> tuple[str, str, dict[str, str]]:
        if self.git("status", "--porcelain", "--untracked-files=all"):
            raise ValueError("qualification requires a clean committed candidate")
        head, tree = self.git("rev-parse", "HEAD"), self.git("rev-parse", "HEAD^{tree}")
        entries = subprocess.check_output(["git", "ls-tree", "-rz", "--full-tree", head], cwd=self.root)
        result = {}
        for entry in entries.split(b"\0"):
            if not entry:
                continue
            metadata, name = entry.split(b"\t", 1)
            mode, kind, blob = metadata.decode().split()
            path = name.decode("utf-8")
            if kind != "blob" or mode not in {"100644", "100755"}:
                raise ValueError(f"unsupported input type: {path}")
            # Bind working bytes as well as immutable Git objects, including modes.
            actual = self.root / path
            if actual.is_symlink() or not actual.is_file():
                raise ValueError(f"missing or symlink input: {path}")
            data = actual.read_bytes()
            # Detect assume-unchanged/skip-worktree, hidden dirty bytes and modes.
            object_header = f"blob {len(data)}\0".encode()
            if (len(blob) != 40 or hashlib.sha1(object_header + data).hexdigest() != blob
                    or bool(actual.stat().st_mode & 0o111) != (mode == "100755")):
                raise ValueError(f"working input differs from committed source: {path}")
            result[path] = digest(encoded([mode, blob, digest(data)]))
        if head != self.git("rev-parse", "HEAD"):
            raise ValueError("candidate changed during snapshot")
        return head, tree, result

    def context(self) -> str:
        go = shutil.which("go", path=self.env.get("PATH"))
        if not go:
            raise ValueError("Go toolchain unavailable")
        version = subprocess.check_output([go, "version"], env=self.env)
        settings = subprocess.check_output([go, "env", "-json"], cwd=self.root, env=self.env)
        configuration = json.loads(settings)
        if configuration.get("GOWORK") != str(self.root / "go.work"):
            raise ValueError("qualification requires this checkout's Go workspace")
        tool_files = {}
        goroot = Path(configuration["GOROOT"])
        for folder in (goroot / "src", goroot / "pkg/tool", goroot / "bin"):
            if not folder.is_dir():
                raise ValueError("incomplete Go toolchain")
            for path in sorted(folder.rglob("*")):
                if path.is_file():
                    tool_files[str(path)] = digest(path.read_bytes())
        for name in ("CC", "CXX"):
            executable = shutil.which(shlex.split(configuration[name])[0], path=self.env.get("PATH"))
            if configuration.get("CGO_ENABLED") == "1" and not executable:
                raise ValueError(f"configured {name} toolchain unavailable")
            if executable:
                tool_files[str(Path(executable).resolve())] = digest(Path(executable).read_bytes())
        git = shutil.which("git", path=self.env.get("PATH"))
        if not git:
            raise ValueError("Git provenance tool unavailable")
        tool_files[str(Path(git).resolve())] = digest(Path(git).read_bytes())
        # Verify pinned downloaded dependency content, without trusting cache mtimes.
        for directory in (".", "core", "mcp", "pb", "sdks/go", "server"):
            subprocess.run([go, "mod", "verify"], cwd=self.root / directory, env=self.env,
                           stdout=subprocess.DEVNULL, check=True)
        # Effective environment is hashed only, never emitted (it may contain secrets).
        # No credentials or new local signing keys are created.
        return digest(encoded([platform.platform(), str(Path(go).resolve()),
                               digest(Path(go).read_bytes()), version.decode(), stable_go_settings(configuration),
                               tool_files, digest(encoded(self.env)),
                               digest(Path(sys.executable).read_bytes())]))

    def intact(self) -> bool:
        try:
            return (self.report is not None
                    and digest(self.report[0].read_bytes()) == self.report[1]
                    and bool(self.baseline_logs)
                    and all(digest(path.read_bytes()) == expected for path, expected in self.baseline_logs.items()))
        except OSError:
            return False

    def ignored_inputs(self) -> bool:
        ignored = subprocess.check_output(
            ["git", "ls-files", "-z", "--others", "--ignored", "--exclude-standard"], cwd=self.root)
        paths = [p.decode("utf-8") for p in ignored.split(b"\0") if p]
        return any(not p.startswith(BUILD_OUTPUT_DIRS)
                   and (policy(p) or p in ROOT_INPUTS or p.startswith(GO_DIRS + SHARED_DIRS) or "/" not in p)
                   for p in paths)

    def carry_allowed(self, tree: dict[str, str], context: str) -> tuple[bool, str]:
        try:
            if self.prior_tree is None:
                return False, "no successful live-session baseline"
            branch = self.git("symbolic-ref", "--short", "HEAD")
            if (self.env.get("GITHUB_ACTIONS") or branch in {"main", "master"}
                    or branch != self.prior_branch
                    or subprocess.run(["git", "merge-base", "--is-ancestor", self.prior_head, "HEAD"],
                                      cwd=self.root, check=False).returncode != 0):
                return False, "carry is restricted to descendants on the same local feature branch"
            if not all(known(path) for path in tree.keys() | self.prior_tree.keys()):
                return False, "unknown path"
            if digest(encoded({p: h for p, h in tree.items() if policy(p)})) != self.prior_policy:
                return False, "gate policy, dependency or configuration changed"
            if context != self.prior_context:
                return False, "toolchain or execution environment changed"
            if not self.intact():
                return False, "missing or altered live-session evidence"
            # Ignored source/fixtures cannot be qualified by committed input hashes.
            if self.ignored_inputs():
                return False, "untracked ignored validation inputs"
            return True, "live runner, matching closure and intact logs"
        except Exception:
            return False, "classifier failure"

    @contextmanager
    def fixed_inputs(self, head: str, tree: dict[str, str], selected: dict[str, str] | None = None):
        """Only immutable Git objects populate the private eligible execution cwd.

        No developer-worktree bytes, ignored files, symlinks or hardlinks enter
        this export. Read-only permissions prevent ordinary test writes. The
        host/process owner remains trusted; this is not an adversarial sandbox.
        """
        selected = tree if selected is None else selected
        if not selected.keys() <= tree.keys():
            raise ValueError("execution closure contains unknown inputs")
        with tempfile.TemporaryDirectory(prefix="committed-inputs-", dir=self.evidence) as directory:
            root = Path(directory)
            try:
                archive = subprocess.check_output(["git", "archive", "--format=tar", head], cwd=self.root)
                with tarfile.open(fileobj=io.BytesIO(archive)) as source:
                    source.extractall(root, filter="data")
                files = {str(p.relative_to(root)): p for p in root.rglob("*") if p.is_file()}
                if files.keys() != tree.keys():
                    raise ValueError("Git export omitted or added candidate inputs")
                entries = subprocess.check_output(["git", "ls-tree", "-rz", "--full-tree", head], cwd=self.root)
                for entry in entries.split(b"\0"):
                    if not entry:
                        continue
                    metadata, name = entry.split(b"\t", 1)
                    mode, kind, blob = metadata.decode().split()
                    path = name.decode("utf-8")
                    actual = files[path]
                    data = actual.read_bytes()
                    if (actual.is_symlink() or kind != "blob" or mode not in {"100644", "100755"}
                            or hashlib.sha1(f"blob {len(data)}\0".encode() + data).hexdigest() != blob
                            or digest(encoded([mode, blob, digest(data)])) != tree[path]):
                        raise ValueError(f"Git export differs from committed input: {path}")
                    if path in selected:
                        actual.chmod(0o555 if mode == "100755" else 0o444)
                    else:
                        actual.unlink()
                for folder in sorted((p for p in root.rglob("*") if p.is_dir()), reverse=True):
                    if any(path.startswith(str(folder.relative_to(root)) + "/") for path in selected):
                        folder.chmod(0o555)
                    else:
                        folder.rmdir()
                root.chmod(0o555)
                yield root
            finally:
                # Restore only this invocation's private export for owned cleanup.
                root.chmod(0o700)
                for path in root.rglob("*"):
                    path.chmod(0o700 if path.is_dir() else 0o600)

    def run(self, step: Step, log: Path) -> tuple[int, bool]:
        root = self.eligible_root if step.name in ELIGIBLE else self.root
        if root is None:
            raise ValueError("eligible execution requires committed read-only inputs")
        command = []
        for index, arg in enumerate(step.command):
            if index in step.glob_args:
                matches = sorted((root / step.cwd).glob(arg))
                if not matches:
                    raise ValueError(f"empty command glob: {arg}")
                command.extend(str(p.relative_to(root / step.cwd)) for p in matches)
            else:
                command.append(arg)
        env = self.env.copy()
        if step.name in ELIGIBLE:
            # Prevent an inherited workspace override selecting developer sources.
            env["GOWORK"] = str(root / "go.work")
            env["PWD"] = str(root / step.cwd)
            if command[0] == "go":
                self.check_go_inputs(root, env)
        if step.name == "admin-playwright":
            # One session owns these ports; preflight and host-slot ownership stay mandatory.
            env.update(LANTERN_E2E_PREVIEW_PORT="44469", LANTERN_PORT="64469",
                       LANTERN_E2E_METRICS_PORT="59469")
        if step.name == "rust-real-wire":
            env["LANTERN_RUST_TEST_SERVER"] = str(self.root / "sdks/rust/target/lantern-smoke")
            env["LANTERN_RUST_TEST_AUTH_FIXTURE"] = str(self.root / "sdks/rust/target/lantern-authfixture")
        with log.open("xb") as output:
            code = subprocess.run(command, cwd=root / step.cwd, env=env,
                                  stdout=output, stderr=subprocess.STDOUT, check=False).returncode
        return code, code == 0 and (not step.empty or not log.read_bytes().strip())

    def check_go_inputs(self, root: Path, env: dict[str, str]) -> None:
        # Overlay flags and external local module/workspace paths can bypass the
        # exported source namespace. They have no carry qualification support.
        flags = subprocess.check_output(["go", "env", "GOFLAGS"], cwd=root, env=env, text=True).strip()
        if flags:
            raise ValueError("nondefault GOFLAGS are unsupported for committed-input qualification")
        work = json.loads(subprocess.check_output(["go", "work", "edit", "-json"], cwd=root, env=env))
        def contained(directory: Path, path: str) -> bool:
            resolved = (directory / path).resolve()
            return resolved == root or root in resolved.parents
        if any(not contained(root, item["DiskPath"]) for item in work.get("Use", [])):
            raise ValueError("external workspace input is unsupported")
        manifests = [(root, work)]
        for directory in (".", "core", "mcp", "pb", "sdks/go", "server"):
            manifest = json.loads(subprocess.check_output(["go", "mod", "edit", "-json"],
                                                         cwd=root / directory, env=env))
            manifests.append((root / directory, manifest))
        for directory, manifest in manifests:
            for replacement in manifest.get("Replace", []):
                target = replacement["New"]
                if not target.get("Version") and not contained(directory, target["Path"]):
                    raise ValueError("external local replacement is unsupported")

    def qualify(self) -> dict:
        self.attempt += 1
        records = []
        execution = ExitStack()
        manifest = {"kind": "local-pre-push", "attempt": self.attempt,
                    "started_utc": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
                    "required": len(self.plan), "passed": False, "steps": records}
        try:
            if any(digest(p.read_bytes()) != h for p, h in self.runner_hashes.items()):
                raise ValueError("runner/classifier changed; restart and execute full gate")
            head, source_tree, tree = self.snapshot()
            branch = subprocess.run(["git", "symbolic-ref", "--short", "HEAD"], cwd=self.root,
                                    capture_output=True, text=True, check=False).stdout.strip()
            context = self.context()
            allow, reason = self.carry_allowed(tree, context)
            try:
                policy_hash = digest(encoded({p: h for p, h in tree.items() if policy(p)}))
                fingerprints = {step.name: digest(encoded([step.__dict__, inputs(step, tree), context]))
                                for step in self.plan}
                execution_tree = inputs(Step("go-test-core", ".", ("go", "test", "./...")), tree)
            except Exception:
                allow, reason, policy_hash = False, "classifier failure", None
                fingerprints = {step.name: digest(encoded([step.__dict__, tree, context])) for step in self.plan}
                execution_tree = tree
            manifest.update(head=head, tree=source_tree, branch=branch, carry_reason=reason)
            self.eligible_root = execution.enter_context(self.fixed_inputs(head, tree, execution_tree))
            next_receipts = {}
            for index, step in enumerate(self.plan):
                fingerprint = fingerprints[step.name]
                receipt = self.receipts.get(step.name)
                carried = (allow and step.name in ELIGIBLE and receipt is not None
                           and receipt.fingerprint == fingerprint)
                if carried:
                    record = {"name": step.name, "mode": "carried", "from_head": receipt.head,
                              "from_tree": receipt.tree, "exit": 0, "log": str(receipt.log),
                              "sha256": receipt.log_hash, "fingerprint": fingerprint}
                else:
                    log = self.evidence / f"{self.attempt:03}-{index + 1:02}-{step.name}.log"
                    print(f"EXECUTE {index + 1}/{len(self.plan)} {step.name}", flush=True)
                    started = time.monotonic()
                    code, passed = self.run(step, log)
                    record = {"name": step.name, "mode": "executed", "exit": code,
                              "passed": passed, "log": str(log), "sha256": digest(log.read_bytes()),
                              "fingerprint": fingerprint, "seconds": round(time.monotonic() - started, 3)}
                    records.append(record)
                    if not passed:
                        raise ValueError(f"required step failed: {step.name}")
                    receipt = Receipt(fingerprint, head, source_tree, log, record["sha256"])
                if carried:
                    records.append(record)
                    print(f"CARRIED {step.name} from {receipt.head}", flush=True)
                if step.name in ELIGIBLE and policy_hash is not None:
                    next_receipts[step.name] = receipt
                record.update(command=list(step.command), cwd=step.cwd)
                record["inputs"] = "committed-readonly-export" if step.name in ELIGIBLE else "owner-frozen-checkout"
            if self.snapshot() != (head, source_tree, tree) or self.context() != context:
                raise ValueError("source or execution context changed during qualification")
            # Check every executed/carried raw log before claiming overall success.
            if any(digest(Path(r["log"]).read_bytes()) != r["sha256"] for r in records):
                raise ValueError("raw evidence changed during qualification")
            manifest["passed"] = True
            self.receipts = next_receipts
            self.baseline_logs = {Path(r["log"]): r["sha256"] for r in records}
            self.prior_tree, self.prior_context = tree, context
            self.prior_branch, self.prior_head = branch, head
            self.prior_policy = policy_hash
        except Exception as error:
            manifest["error"] = str(error)
            self.receipts.clear()
            self.baseline_logs.clear()
            self.prior_tree = None
        finally:
            self.eligible_root = None
            execution.close()
        report = self.evidence / f"{self.attempt:03}-manifest.json"
        manifest.update(executed=sum(r["mode"] == "executed" for r in records),
                        carried=sum(r["mode"] == "carried" for r in records),
                        finished_utc=time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()))
        data = encoded(manifest)
        report.write_bytes(data)
        self.report = report, digest(data)
        print(f"{'PASS' if manifest['passed'] else 'FAIL'} executed={manifest['executed']} "
              f"carried={manifest['carried']} required={manifest['required']} "
              f"head={manifest.get('head', 'unknown')} tree={manifest.get('tree', 'unknown')} "
              f"{report} sha256={self.report[1]}", flush=True)
        return manifest


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("checkout", type=Path)
    parser.add_argument("evidence", type=Path, help="new directory outside the checkout")
    parser.add_argument("--session", action="store_true", help="accept qualify/quit on stdin; no receipt import")
    args = parser.parse_args()
    session = Session(args.checkout, args.evidence)
    result = session.qualify()
    if args.session:
        print("Session ready: commit review fixes, then enter qualify; quit ends all carry eligibility.", flush=True)
        for line in sys.stdin:
            command = line.strip()
            if command == "quit":
                break
            if command != "qualify":
                print("Only qualify/quit accepted; evidence JSON is output-only.", flush=True)
                continue
            result = session.qualify()
    return int(not result["passed"])


if __name__ == "__main__":
    raise SystemExit(main())
