#!/usr/bin/env python3
"""Run locked paired-source checks; never qualify a hosted publish archive."""
import argparse
import base64
import json
from pathlib import Path
import re
import subprocess

PACKAGE = Path(__file__).resolve().parents[1]
PARENT = PACKAGE.parent
LOCK = PACKAGE / "pubspec.lock"
SOURCE_LOCK = PACKAGE / "tool/paired_source.pubspec.lock"
OVERRIDE = PACKAGE / "pubspec_overrides.yaml"
STATE = PACKAGE / ".dart_tool/paired_source_gate.json"
OVERRIDE_BYTES = b"dependency_overrides:\n  lantern_client:\n    path: ..\n"


def prepare(refresh=False):
    parent = (PARENT / "pubspec.yaml").read_text()
    offline = (PACKAGE / "pubspec.yaml").read_text()
    version = re.search(r"^version: (\d+\.\d+\.\d+)$", parent, re.M)
    if not version or not re.search(rf"^  lantern_client: \^{re.escape(version[1])}$", offline, re.M):
        raise ValueError("paired parent source must match the offline hosted dependency floor")
    if STATE.exists() or OVERRIDE.exists():
        raise ValueError("existing override or interrupted paired-source state; inspect before cleanup")
    if not refresh and not SOURCE_LOCK.is_file():
        raise ValueError("missing generated paired-source lockfile")
    STATE.parent.mkdir(exist_ok=True)
    STATE.write_text(json.dumps({"lock": base64.b64encode(LOCK.read_bytes()).decode() if LOCK.exists() else None}))
    try:
        OVERRIDE.write_bytes(OVERRIDE_BYTES)
        if not refresh:
            LOCK.write_bytes(SOURCE_LOCK.read_bytes())
        command = ["dart", "pub", "get"]
        if not refresh:
            command.append("--enforce-lockfile")
        subprocess.run(command, cwd=PACKAGE, check=True)
        if refresh:
            SOURCE_LOCK.write_bytes(LOCK.read_bytes())
        print(f"Paired-source parent {version[1]} resolved; hosted archive qualification remains separate.", flush=True)
    except BaseException:
        cleanup()
        raise


def cleanup():
    if not STATE.exists():
        return
    if not OVERRIDE.exists() or OVERRIDE.read_bytes() != OVERRIDE_BYTES:
        raise ValueError("paired-source override changed; refusing to overwrite user state")
    previous = json.loads(STATE.read_text())["lock"]
    if previous is None:
        LOCK.unlink(missing_ok=True)
    else:
        LOCK.write_bytes(base64.b64decode(previous, validate=True))
    OVERRIDE.unlink()
    STATE.unlink()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    group = parser.add_mutually_exclusive_group()
    group.add_argument("--prepare", action="store_true")
    group.add_argument("--cleanup", action="store_true")
    group.add_argument("--refresh-lock", action="store_true")
    parser.add_argument("command", nargs=argparse.REMAINDER)
    args = parser.parse_args()
    if args.cleanup:
        cleanup()
        return
    prepare(args.refresh_lock)
    if args.prepare:
        return
    try:
        command = args.command
        if command[:1] == ["--"]:
            command = command[1:]
        if command:
            subprocess.run(command, cwd=PACKAGE, check=True)
    finally:
        cleanup()


if __name__ == "__main__":
    main()
