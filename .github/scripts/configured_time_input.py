#!/usr/bin/env python3
"""Prepare the declared read-only time input on an owned disposable CI runner."""

import os
from pathlib import Path
import platform
import stat
import subprocess
import sys
import tempfile


SOURCE_INPUT = Path("/etc/ntp.conf")
SOURCE_BYTES = b"server time.asia.apple.com\n"


def inspect_input():
    try:
        metadata = SOURCE_INPUT.lstat()
    except FileNotFoundError:
        return "missing"
    if not stat.S_ISREG(metadata.st_mode):
        raise ValueError("source input must be a regular file")
    with SOURCE_INPUT.open("rb") as source:
        content = source.read(4097)
    if content != SOURCE_BYTES:
        return "different"
    if metadata.st_uid != 0 or stat.S_IMODE(metadata.st_mode) != 0o444:
        return "matching_content"
    return "ready"


def prepare():
    # The workflow supplies runner.environment explicitly. This helper must
    # never provision a developer machine or a persistent self-hosted runner.
    if (os.environ.get("GITHUB_ACTIONS") != "true"
            or os.environ.get("LANTERN_CI_DISPOSABLE_SOURCE_INPUT") != "github-hosted"
            or platform.system() not in ("Linux", "Darwin")):
        raise ValueError("owned disposable Linux/macOS CI runner required")
    directory = Path(os.environ.get("RUNNER_TEMP", ""))
    if not directory.is_absolute() or not directory.is_dir():
        raise ValueError("owned runner temporary directory required")
    before = inspect_input()
    print("configured_source_input_before=" + before, flush=True)
    if before != "ready":
        # Only this fixed deployment input is installed. No clock setter,
        # daemon, DNS, network probe or trust-store operation is performed.
        with tempfile.NamedTemporaryFile(dir=directory) as source:
            source.write(SOURCE_BYTES)
            source.flush()
            command = ["install", "-m", "0444", source.name, str(SOURCE_INPUT)]
            if os.geteuid() != 0:
                command = ["sudo", "-n"] + command
            subprocess.run(command, check=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if inspect_input() != "ready":
        raise ValueError("read-only configured source input verification failed")
    print("configured_source_input_ready=true", flush=True)


def main():
    try:
        if len(sys.argv) != 1:
            raise ValueError("no source or path override accepted")
        prepare()
    except (OSError, ValueError, subprocess.SubprocessError):
        # Child errors and paths remain private. File preparation alone never
        # claims native acquisition, a usable anchor or configured-source-integration qualification.
        print("configured_source_input_preparation_failed", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
