#!/usr/bin/env python3
"""Conservative prose-only CI scope; source, fixtures and contracts fail closed."""

from __future__ import annotations

import os
from pathlib import Path, PurePosixPath
import subprocess

import evidence_only_pr


ROOT_DOCS = {"README.md", "AGENTS.md", "CLAUDE.md", "CONTRIBUTING.md"}
DOC_DIRS = {
    ".github", "admin", "core", "pb", "server", "mcp", "mcp/examples",
    "sdks/go", "sdks/node", "sdks/rust", "sdks/dart", "sdks/dart/offline",
    "sdks/dart/offline_sqlite", "sdks/dart/example", "sdks/python.archived",
    "testbed", "testbed/bench", "testbed/dart-transport-probe",
    "deploy/compose", "deploy/helm/lantern",
}
# Generated output and the canonical native-toolchain/SDK contracts still
# exercise their owning gates even though their extension is Markdown.
CONTRACT_DOCS = {
    "docs/env.md",
    "docs/decisions/0001-dart-mobile-transport.md",
    "docs/decisions/0002-dart-offline-repository-contract.md",
    "docs/decisions/0011-native-rust-sdk.md",
    "docs/rust-sdk-v0.1-contract.md",
}


def is_document(path: str) -> bool:
    value = PurePosixPath(path)
    if path != value.as_posix() or value.is_absolute() or ".." in value.parts:
        return False
    if path in CONTRACT_DOCS:
        return False
    return path in ROOT_DOCS or (
        value.suffix == ".md"
        and (path.startswith("docs/") or str(value.parent) in DOC_DIRS)
    )


def documentation_only(paths: list[str]) -> bool:
    return bool(paths) and all(is_document(path) for path in paths)


def change_range(environ: dict[str, str]) -> tuple[str, str] | None:
    # Tags, manual runs and scheduled sweeps always retain the full gates.
    event = environ.get("GITHUB_EVENT_NAME", "")
    if environ.get("GITHUB_REF", "").startswith("refs/tags/"):
        return None
    if event == "pull_request":
        base, head = environ["PR_BASE_SHA"], environ["PR_HEAD_SHA"]
        return evidence_only_pr.git("merge-base", base, head), head
    if event == "push":
        base, head = environ["PUSH_BEFORE_SHA"], environ["GITHUB_SHA"]
        if not base or set(base) == {"0"}:
            return None
        return base, head
    return None


def classify(environ: dict[str, str]) -> tuple[bool, tuple[str, str] | None]:
    revisions = change_range(environ)
    if revisions is None:
        return False, None
    # --no-renames includes both the removed source and the added document.
    changes = evidence_only_pr.changed_files(*revisions)
    return documentation_only(list(changes)), revisions


def validate(revisions: tuple[str, str]) -> None:
    subprocess.run(["git", "diff", "--check", *revisions], check=True)
    for path, status in evidence_only_pr.changed_files(*revisions).items():
        if status == "D":
            continue
        mode = evidence_only_pr.git("ls-tree", revisions[1], "--", path).split()[0]
        if mode != "100644":
            raise ValueError(f"documentation must be a regular text file: {path}")
        content = subprocess.check_output(["git", "show", f"{revisions[1]}:{path}"])
        if b"\0" in content:
            raise ValueError(f"NUL in documentation: {path}")
        content.decode("utf-8")


def main() -> None:
    docs_only, revisions = classify(dict(os.environ))
    if docs_only:
        assert revisions is not None
        validate(revisions)
    output = f"docs_only={str(docs_only).lower()}\n"
    print(output, end="")
    with Path(os.environ["GITHUB_OUTPUT"]).open("a", encoding="utf-8") as stream:
        stream.write(output)
    if summary := os.environ.get("GITHUB_STEP_SUMMARY"):
        with Path(summary).open("a", encoding="utf-8") as stream:
            stream.write(f"Documentation-only changes: {docs_only}\n")


if __name__ == "__main__":
    main()
