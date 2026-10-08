#!/usr/bin/env python3
"""Collect every Go module/phase outcome, then fail the existing build job."""

import json
import os
from pathlib import Path
import subprocess


MODULES = (".", "core", "mcp", "pb", "sdks/go", "server")


def collect(root: Path) -> list[dict]:
    coverage = root / "coverage"
    coverage.mkdir(exist_ok=True)
    # Never consume a profile left by an earlier attempt.
    for slug in ("root", "core", "mcp", "pb", "sdks-go", "server", "merged"):
        (coverage / f"{slug}.out").unlink(missing_ok=True)
    (root / "coverage.out").unlink(missing_ok=True)
    results = []
    for phase in ("vet", "build", "test"):
        for module in MODULES:
            slug = "root" if module == "." else module.replace("/", "-")
            command = ["go", phase]
            if phase == "build":
                command += ["-v"]
            if phase == "test":
                command += ["-race", "-shuffle=on", "-coverpkg=./...",
                            "-covermode=atomic", f"-coverprofile={coverage / (slug + '.out')}"]
            command += ["./..."]
            print(f"::group::go {phase} ({module})", flush=True)
            try:
                code = subprocess.run(command, cwd=root / module, check=False).returncode
            except OSError as error:
                print(f"::error::go {phase} ({module}) could not start: {error}", flush=True)
                code = 127
            print("::endgroup::", flush=True)
            results.append({"module": module, "phase": phase, "exit": code})
            if code != 0:
                print(f"::error::go {phase} ({module}) failed ({code})", flush=True)
    if (coverage / "root.out").is_file():
        (root / "coverage.out").write_bytes((coverage / "root.out").read_bytes())
    return results


def main() -> int:
    root = Path.cwd().resolve()
    results = collect(root)
    (root / "coverage/modules.json").write_text(json.dumps(results, indent=2) + "\n")
    summary = "| Module | Phase | Exit |\n| --- | --- | --- |\n" + "".join(
        f"| {item['module']} | {item['phase']} | {item['exit']} |\n" for item in results)
    if path := os.environ.get("GITHUB_STEP_SUMMARY"):
        with open(path, "a") as output:
            output.write(summary)
    return int(any(item["exit"] != 0 for item in results))


if __name__ == "__main__":
    raise SystemExit(main())
