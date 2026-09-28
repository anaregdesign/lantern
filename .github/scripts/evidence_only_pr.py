#!/usr/bin/env python3
"""Select the exact eight-file physical evidence PR fast path."""

from __future__ import annotations

import argparse
import os
from pathlib import Path
import subprocess
import sys


ROOT = Path(__file__).resolve().parents[2]
EVIDENCE_DIR = Path("sdks/dart/example/evidence/offline-release")
REQUIRED = frozenset(
    str(EVIDENCE_DIR / name)
    for name in (
        "android.json", "ios.json", "android-cdc.json", "ios-cdc.json",
        "android-receipt.json", "ios-receipt.json",
        "android-receipt-marker.json", "ios-receipt-marker.json",
    )
)


def git(*args: str) -> str:
    return subprocess.check_output(["git", *args], text=True).strip()


def changed_files(base: str, head: str) -> dict[str, str]:
    raw = subprocess.check_output(
        ["git", "diff", "--name-status", "--no-renames", "-z", base, head]
    )
    fields = raw.decode("utf-8").split("\0")
    if fields[-1] == "":
        fields.pop()
    if len(fields) % 2:
        raise ValueError("invalid Git name-status output")
    return dict(zip(fields[1::2], fields[0::2]))


def is_candidate(changes: dict[str, str]) -> bool:
    return set(changes) == REQUIRED and all(
        status in {"A", "M"} for status in changes.values()
    )


def classify_range(base: str, head: str) -> bool:
    return is_candidate(changed_files(git("merge-base", base, head), head))


def validate(head: str) -> None:
    sys.path.insert(0, str(ROOT / "sdks/dart/offline/tool"))
    import physical_release_gate

    tested = git("rev-parse", f"{head}^")
    physical_release_gate.validate_pr_evidence(head, tested)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("mode", choices=("classify", "validate"))
    parser.add_argument("--base")
    parser.add_argument("--head")
    args = parser.parse_args()
    head = args.head or os.environ.get("PR_HEAD_SHA", "")
    if args.mode == "classify":
        base = args.base or os.environ.get("PR_BASE_SHA", "")
        candidate = bool(base and head and classify_range(base, head))
        print(f"evidence_only={str(candidate).lower()}")
        if output := os.environ.get("GITHUB_OUTPUT"):
            with Path(output).open("a", encoding="utf-8") as stream:
                stream.write(f"evidence_only={str(candidate).lower()}\n")
    else:
        if not head:
            parser.error("--head or PR_HEAD_SHA is required")
        validate(head)
        print("exact-source physical evidence PR passed")


if __name__ == "__main__":
    main()
