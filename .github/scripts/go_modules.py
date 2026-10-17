#!/usr/bin/env python3
"""Collect every Go module/phase outcome, then fail the existing build job."""

from concurrent.futures import ThreadPoolExecutor
import json
import os
from pathlib import Path
import subprocess
import time


MODULES = (".", "core", "mcp", "pb", "sdks/go", "server")


def collect(root: Path) -> list[dict]:
    coverage = root / "coverage"
    coverage.mkdir(exist_ok=True)
    # Never consume a profile left by an earlier attempt.
    for slug in ("root", "core", "mcp", "pb", "sdks-go", "server", "merged"):
        (coverage / f"{slug}.out").unlink(missing_ok=True)
    (root / "coverage.out").unlink(missing_ok=True)
    def module_phases(module):
        slug = "root" if module == "." else module.replace("/", "-")
        outcomes = []
        for phase in ("vet", "build", "test"):
            command = ["go", phase]
            if phase == "build":
                command += ["-v"]
            if phase == "test":
                command += ["-race", "-shuffle=on", "-coverpkg=./...",
                            "-covermode=atomic", f"-coverprofile={coverage / (slug + '.out')}"]
            command += ["./..."]
            log = coverage / f"{slug}-{phase}.log"
            print(f"RUN go {phase} ({module})", flush=True)
            started = time.monotonic()
            with log.open("wb") as output:
                try:
                    code = subprocess.run(command, cwd=root / module, check=False,
                                          stdout=output, stderr=subprocess.STDOUT).returncode
                except OSError as error:
                    output.write(f"could not start: {error}\n".encode())
                    code = 127
            outcomes.append({"module": module, "phase": phase, "exit": code,
                             "seconds": round(time.monotonic() - started, 3), "log": str(log)})
        return outcomes

    # Install/generate shared inputs before this collector. Each module owns its
    # log/profile; collect all phases even if another worker or phase fails.
    with ThreadPoolExecutor(max_workers=2) as workers:
        collected = list(workers.map(module_phases, MODULES))
    by_key = {(item["phase"], item["module"]): item for group in collected for item in group}
    results = [by_key[phase, module] for phase in ("vet", "build", "test") for module in MODULES]
    for item in results:
        print(f"::group::go {item['phase']} ({item['module']})", flush=True)
        print(Path(item["log"]).read_text(errors="replace"), end="", flush=True)
        print("::endgroup::", flush=True)
        if item["exit"]:
            print(f"::error::go {item['phase']} ({item['module']}) failed ({item['exit']})", flush=True)
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
