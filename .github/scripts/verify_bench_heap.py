#!/usr/bin/env python3
"""Require tagged heap-observer tests without linking them into normal cmd."""

import json
from pathlib import Path
import subprocess
import sys


MODULE = "github.com/anaregdesign/lantern/server"
OBSERVER = MODULE + "/benchheap"
TAG = "lantern_bench_heap"
TARGETS = ("./cmd", "./provider", "./service", "./benchheap")
CONTROLLED_TAGS = TAG + ",lantern_test_time"
CONTROLLED_TEST = "TestBenchHeapRefusalSurvivesCancellation"


def commands():
    return (
        ("normal dependencies", ("go", "list", "-mod=readonly", "-deps", "./cmd")),
        ("tagged dependencies", ("go", "list", "-mod=readonly", "-tags=" + TAG, "-deps", "./cmd")),
        ("tagged tests", ("go", "test", "-mod=readonly", "-race", "-count=1", "-json",
                          "-tags=" + TAG, *TARGETS, "-run", "^TestBenchHeap")),
        ("controlled refusal test", ("go", "test", "-mod=readonly", "-race", "-count=1", "-json",
                                    "-tags=" + CONTROLLED_TAGS, "./cmd", "-run", "^" + CONTROLLED_TEST + "$")),
    )


def require_dependencies(text, tagged):
    dependencies = set(text.splitlines())
    if MODULE + "/cmd" not in dependencies or (OBSERVER in dependencies) != tagged:
        raise ValueError("normal/tagged cmd heap-observer dependency boundary failed")


def require_tests(text, targets=TARGETS, required=()):
    expected = {MODULE + target[1:] for target in targets}
    observed = {package: {"run": set(), "pass": set(), "package_pass": 0}
                for package in expected}
    for line in text.splitlines():
        event = json.loads(line)
        package, action, name = event.get("Package"), event.get("Action"), event.get("Test")
        if package not in expected or action in ("fail", "skip"):
            raise ValueError("tagged observer test failed, skipped or selected an unexpected package")
        if name:
            if not name.startswith("TestBenchHeap"):
                raise ValueError("unexpected tagged observer test")
            if action in ("run", "pass"):
                if name in observed[package][action]:
                    raise ValueError("duplicate tagged observer test event")
                observed[package][action].add(name)
        elif action == "pass":
            observed[package]["package_pass"] += 1
    for package, result in observed.items():
        if (not any("/" not in name for name in result["run"])
                or result["run"] != result["pass"]
                or result["package_pass"] != 1):
            raise ValueError("missing or incomplete tagged observer tests: " + package)
    for package, name in required:
        if package not in observed or name not in observed[package]["pass"]:
            raise ValueError("required controlled observer test missing: " + name)
    return {package: sorted(result["pass"]) for package, result in sorted(observed.items())}


def verify(root):
    selected = None
    for index, (label, command) in enumerate(commands()):
        print("RUN " + label + ": " + " ".join(command), flush=True)
        # Stream test JSON unchanged so a CI timeout retains completed events.
        process = subprocess.Popen(command, cwd=root / "server", stdout=subprocess.PIPE,
                                   stderr=None, text=True)
        lines = []
        try:
            for line in process.stdout:
                print(line, end="", flush=True)
                lines.append(line)
            code = process.wait()
        finally:
            process.stdout.close()
            if process.poll() is None:
                process.kill()
                process.wait()
        if code:
            raise ValueError(label + " failed: " + str(code))
        output = "".join(lines)
        if index < 2:
            require_dependencies(output, tagged=index == 1)
        elif index == 2:
            selected = require_tests(output)
        else:
            controlled = require_tests(output, ("./cmd",), ((MODULE + "/cmd", CONTROLLED_TEST),))
            selected[MODULE + "/cmd"] = sorted(set(selected[MODULE + "/cmd"]) | set(controlled[MODULE + "/cmd"]))
    return selected


def main():
    try:
        selected = verify(Path(__file__).resolve().parents[2])
    except (OSError, ValueError) as error:
        print("::error::" + str(error), file=sys.stderr, flush=True)
        return 1
    print(json.dumps({"status": "PASS", "tag": TAG, "tests": selected}), flush=True)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
