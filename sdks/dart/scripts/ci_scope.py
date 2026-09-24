#!/usr/bin/env python3
"""Classify the Dart package and native mobile gates for one git change range."""

from __future__ import annotations

import os
from pathlib import Path
import re
import subprocess


PACKAGE_ONLY = {
    "go.mod",
    "go.sum",
    "go.work",
    "go.work.sum",
    "tests/integration/dart_offline_sqlite_test.dart",
    "tests/integration/dart_identity_offline_sqlite_test.dart",
    "testbed/scripts/dart_identity_fixture.sh",
    "AGENTS.md",
    "README.md",
    "CONTRIBUTING.md",
}
MOBILE_EXACT = {
    "buf.yaml",
    "buf.gen.yaml",
    "generate.go",
    "docs/decisions/0001-dart-mobile-transport.md",
    "docs/decisions/0002-dart-offline-repository-contract.md",
    ".github/workflows/dart-sdk.yml",
}
GO_DIRECTIVE = re.compile(r"^\s*(go|toolchain)\s+(\S+)\s*(?://.*)?$", re.MULTILINE)


def go_directives(content: str) -> tuple[tuple[str, str], ...]:
    """Only Go language/toolchain directives require a native toolchain rerun."""
    return tuple(sorted(GO_DIRECTIVE.findall(content)))


def is_go_toolchain_file(path: str) -> bool:
    return path == "go.work" or path == "go.mod" or path.endswith("/go.mod")


def is_dart_mobile_path(path: str) -> bool:
    if not path.startswith("sdks/dart/"):
        return False
    if path.endswith((".md", ".mdx", ".rst", ".txt")) or "/evidence/" in path:
        return False
    if path in {
        "sdks/dart/.pubignore",
        "sdks/dart/scripts/release.py",
        "sdks/dart/scripts/release_test.py",
    }:
        return False
    if path.rsplit("/", 1)[-1].upper() in {"LICENSE", "NOTICE"}:
        return False
    return True


def classify(
    paths: list[str], *, release_tag: bool = False, toolchain_changed: bool = False
) -> tuple[bool, bool]:
    """Return (full package-quality gate, Android/iOS native gate)."""
    if release_tag:
        return True, True
    mobile = toolchain_changed or any(
        is_dart_mobile_path(path) or path.startswith("proto/") or path in MOBILE_EXACT
        for path in paths
    )
    full = mobile or any(
        path.startswith("sdks/dart/") or path in PACKAGE_ONLY for path in paths
    )
    return full, mobile


def git(*args: str) -> bytes:
    return subprocess.run(["git", *args], check=True, capture_output=True).stdout


def file_at(revision: str, path: str) -> str:
    result = subprocess.run(["git", "show", f"{revision}:{path}"], capture_output=True)
    if result.returncode:
        # Adding or removing a Go module is itself a toolchain-surface change.
        return ""
    return result.stdout.decode("utf-8")


def classify_range(base: str, head: str) -> tuple[bool, bool]:
    paths = [
        path.decode("utf-8")
        for path in git("diff", "--name-only", "--no-renames", "-z", base, head).split(
            b"\0"
        )
        if path
    ]
    toolchain_changed = any(
        go_directives(file_at(base, path)) != go_directives(file_at(head, path))
        for path in paths
        if is_go_toolchain_file(path)
    )
    return classify(paths, toolchain_changed=toolchain_changed)


def main() -> None:
    github_ref = os.environ["GITHUB_REF"]
    if github_ref.startswith("refs/tags/"):
        full, mobile = classify([], release_tag=True)
    elif os.environ["GITHUB_EVENT_NAME"] == "pull_request":
        base = os.environ["PR_BASE_SHA"]
        head = os.environ["PR_HEAD_SHA"]
        full, mobile = classify_range(
            git("merge-base", base, head).decode().strip(), head
        )
    else:
        base = os.environ["PUSH_BEFORE_SHA"]
        head = os.environ["GITHUB_SHA"]
        if re.fullmatch(r"0+", base):
            full, mobile = classify([], release_tag=True)
        else:
            full, mobile = classify_range(base, head)

    output = f"full={str(full).lower()}\nmobile={str(mobile).lower()}\n"
    with Path(os.environ["GITHUB_OUTPUT"]).open("a", encoding="utf-8") as stream:
        stream.write(output)
    with Path(os.environ["GITHUB_STEP_SUMMARY"]).open("a", encoding="utf-8") as stream:
        stream.write(
            f"Full Dart package matrix: {full}; Android/iOS native matrix: {mobile}\n"
        )
    print(output, end="")


if __name__ == "__main__":
    main()
