"""Fresh local prose qualification; no component receipts or imported evidence."""

import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import re
import shutil
import subprocess
import sys
import unittest
from urllib.parse import unquote, urlsplit

import ci_docs
import evidence_only_pr
from local_gate_plan import Step


# Narrower than hosted docs routing. These files are explanatory entry points;
# canonical docs/, instructions, deployment and benchmark contracts stay full.
PROSE_DIRS = ci_docs.DOC_DIRS - {".github", "pb", "testbed", "testbed/bench",
    "testbed/dart-transport-probe", "deploy/compose", "deploy/helm/lantern"}
TEST_SUITES = ((".github/scripts", "test_*.py"),
               ("sdks/dart/scripts", "*_test.py"),
               ("sdks/dart/offline/tool", "*_test.py"),
               ("sdks/rust/ci", "test_*.py"))


def prose_path(path):
    value = PurePosixPath(path)
    return ci_docs.is_document(path) and (path == "README.md" or
        (str(value.parent) in PROSE_DIRS and value.name in {"README.md", "CHANGELOG.md"}))


def literals(text):
    """Reject angle markup; keep fenced/indented examples and inline literals."""
    # HTML block bodies may be executable examples without fence/indent markers.
    # Reject the entire file at either revision, including ambiguous/unclosed
    # markup and autolinks, rather than trying to parse HTML as Markdown prose.
    if "<" in text:
        raise ValueError("HTML or angle markup is outside prose eligibility")
    result, block, marker = [], [], None
    for line in text.splitlines(keepends=True):
        fence = re.match(r"^ {0,3}(`{3,}|~{3,})", line)
        if marker is not None:
            block.append(line)
            if fence and fence[1][0] == marker[0] and len(fence[1]) >= len(marker):
                result.append("".join(block))
                block, marker = [], None
        elif fence:
            block, marker = [line], fence[1]
        else:
            if line.startswith(("    ", "\t")):
                result.append(line)
            start, width = None, None
            for match in re.finditer(r"`+", line):
                if start is None:
                    start, width = match.start(), len(match[0])
                elif len(match[0]) == width:
                    result.append(line[start:match.end()])
                    start, width = None, None
            if start is not None:
                raise ValueError("multiline or unclosed inline literal")
    if marker is not None:
        raise ValueError("unclosed Markdown fence")
    return result


def select(session, base):
    if any(hashlib.sha256(path.read_bytes()).hexdigest() != expected for path, expected in session.runner_hashes.items()):
        raise ValueError("loaded runner/classifier changed")
    snapshot = session.snapshot()
    ignored = subprocess.check_output(["git", "ls-files", "--others", "--ignored", "--exclude-standard", "-z", "--",
        *(directory for directory, _ in TEST_SUITES)], cwd=session.root)
    if session.ignored_inputs() or any(b"/__pycache__/" not in path for path in ignored.split(b"\0") if path):
        raise ValueError("ignored validation input")
    head, _, _ = snapshot
    branch = session.git("symbolic-ref", "--short", "HEAD")
    if session.env.get("GITHUB_ACTIONS") or branch in {"main", "master"} or session.git("tag", "--points-at", head):
        raise ValueError("prose eligibility is only for a local untagged feature branch")
    target = session.git("rev-parse", "--verify", "refs/remotes/origin/main^{commit}")
    base = session.git("rev-parse", "--verify", base + "^{commit}")
    if base != target or subprocess.run(["git", "merge-base", "--is-ancestor", base, head],
            cwd=session.root, check=False).returncode:
        raise ValueError("base must be target origin/main and an ancestor of HEAD")
    raw = subprocess.check_output(["git", "diff", "--name-status", "--no-renames", "-z", base, head], cwd=session.root)
    fields = raw.decode().split("\0")
    if fields[-1] == "":
        fields.pop()
    if len(fields) % 2:
        raise ValueError("invalid changed paths")
    changes = dict(zip(fields[1::2], fields[0::2]))
    if not ci_docs.documentation_only(list(changes)) or not all(prose_path(p) and s == "M" for p, s in changes.items()):
        raise ValueError("non-prose, unknown, added, removed or mixed inputs")
    for path in changes:
        contents = []
        for revision in (base, head):
            metadata = session.git("ls-tree", revision, "--", path).split()
            if metadata[:2] != ["100644", "blob"]:
                raise ValueError("prose inputs must be regular tracked text at base and head")
            data = subprocess.check_output(["git", "show", f"{revision}:{path}"], cwd=session.root)
            if b"\0" in data:
                raise ValueError("NUL in prose input")
            contents.append(data.decode("utf-8"))
        if literals(contents[0]) != literals(contents[1]):
            raise ValueError("code example or literal changed")
    return base, snapshot, changes


def validate(base, head):
    ci_docs.validate((base, head))
    # Check newly introduced Markdown inline/reference link file targets in the
    # committed tree. External URLs and fragment rendering stay with content review.
    def links(text):
        return set(re.findall(r"\]\(([^\s)]+)(?:\s+[^)]*)?\)|^\s*\[[^]]+\]:\s*(\S+)", text, re.M))
    for path in evidence_only_pr.changed_files(base, head):
        before = subprocess.check_output(["git", "show", f"{base}:{path}"]).decode()
        after = subprocess.check_output(["git", "show", f"{head}:{path}"]).decode()
        for pair in links(after) - links(before):
            url = urlsplit((pair[0] or pair[1]).strip("<>"))
            if url.scheme or url.netloc or not url.path:
                continue
            target = (Path(path).parent / unquote(url.path)).resolve().relative_to(Path.cwd().resolve())
            if not evidence_only_pr.git("ls-tree", head, "--", target.as_posix()):
                raise ValueError(f"missing committed link target: {path} -> {target}")


def qualify(session, base):
    # A requested prose path cannot consume or leave a component-carry baseline,
    # including when classifier failure falls back to full qualification.
    session.receipts.clear()
    session.baseline_logs.clear()
    session.prior_tree = None
    try:
        base, snapshot, changes = select(session, base)
    except Exception as error:
        print(f"FULL REQUIRED: prose eligibility refused: {error}", flush=True)
        return None
    head, tree, _ = snapshot
    # -B avoids writes; the fresh prefix also prevents reading stale local pyc.
    session.env["PYTHONPYCACHEPREFIX"] = str(session.evidence / "python-cache")
    def context():
        git = shutil.which("git", path=session.env.get("PATH"))
        if not git:
            raise ValueError("Git tool unavailable")
        tools = {p: hashlib.sha256(Path(p).read_bytes()).hexdigest() for p in (git, sys.executable)}
        return hashlib.sha256(json.dumps([sys.version, tools, session.env, dict(os.environ)], sort_keys=True).encode()).hexdigest()
    plan = (Step("prose-text-links", ".", (sys.executable, "-B", ".github/scripts/local_prose_gate.py", "validate", base, head)),) + tuple(
        Step("prose-contracts-" + str(index), ".", (sys.executable, "-B", ".github/scripts/local_prose_gate.py", "tests", directory, pattern))
        for index, (directory, pattern) in enumerate(TEST_SUITES))
    manifest = {"kind": "local-prose-pre-push", "base": base, "head": head, "tree": tree,
                "changed_paths": sorted(changes), "required": len(plan), "executed": 0,
                "carried": 0, "passed": False, "steps": []}
    try:
        manifest["context_sha256"] = context()
        for index, step in enumerate(plan):
            log = session.evidence / f"prose-{index + 1}-{step.name}.log"
            code, passed = session.run(step, log)
            manifest["steps"].append({"name": step.name, "mode": "executed", "exit": code,
                "passed": passed, "command": list(step.command), "log": str(log),
                "sha256": hashlib.sha256(log.read_bytes()).hexdigest()})
            if not passed:
                raise ValueError(f"required prose check failed: {step.name}")
        if session.snapshot() != snapshot or select(session, base)[1:] != (snapshot, changes) or context() != manifest["context_sha256"]:
            raise ValueError("candidate or eligibility changed during prose qualification")
        if any(hashlib.sha256(Path(r["log"]).read_bytes()).hexdigest() != r["sha256"] for r in manifest["steps"]):
            raise ValueError("prose raw evidence changed")
        manifest["passed"] = True
    except Exception as error:
        manifest["error"] = str(error)
    manifest["executed"] = len(manifest["steps"])
    # This is fresh path eligibility, never a baseline for component carry.
    report = session.evidence / "prose-manifest.json"
    report.write_text(json.dumps(manifest, sort_keys=True, indent=2) + "\n")
    print(f"{'PASS' if manifest['passed'] else 'FAIL'} prose executed={manifest['executed']} "
          f"carried=0 head={head} tree={tree} {report} sha256={hashlib.sha256(report.read_bytes()).hexdigest()}", flush=True)
    return manifest


if __name__ == "__main__":
    if sys.argv[1] == "validate":
        validate(*sys.argv[2:])
    elif sys.argv[1] == "tests" and tuple(sys.argv[2:]) in TEST_SUITES:
        suite = unittest.defaultTestLoader.discover(sys.argv[2], pattern=sys.argv[3])
        result = unittest.TextTestRunner(verbosity=2).run(suite)
        sys.exit(int(not result.wasSuccessful() or result.testsRun == 0 or bool(result.skipped) or bool(result.expectedFailures)))
    else:
        raise SystemExit("unsupported prose command")
