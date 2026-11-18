"""Bind signed device captures to their original Git inputs during release review."""

from pathlib import Path
import re
import subprocess


# These are post-capture validators and their tests, excluded from both archives.
# Capture tooling, signed targets, package inputs and release policy stay bound.
READERS = frozenset(
    path + suffix + ".py"
    for path in (
        "sdks/dart/scripts/physical_contribution_release_gate",
        "sdks/dart/offline/tool/physical_release_gate",
    )
    for suffix in ("", "_test")
)


def check_capture_source(tested, candidate, root=Path(".")):
    if not all(isinstance(sha, str) and re.fullmatch(r"[0-9a-f]{40}", sha)
               for sha in (candidate, tested)):
        raise ValueError("physical capture requires full original and candidate Git SHAs")
    if tested == candidate:
        return
    command = ["git", "-C", str(root)]
    ancestor = subprocess.run([*command, "merge-base", "--is-ancestor", tested, candidate],
                              stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
    if ancestor.returncode:
        raise ValueError("physical capture must come from an ancestor of the evidence-only child")
    delta = subprocess.check_output([*command, "diff", "--raw", "-z", "--no-renames",
                                     tested, candidate, "--"]).split(b"\0")
    for header, path in zip(delta[:-1:2], delta[1::2], strict=True):
        if (not header.startswith(b":100644 100644 ") or not header.endswith(b" M")
                or path.decode("utf-8") not in READERS):
            raise ValueError("device inputs changed since physical capture")
