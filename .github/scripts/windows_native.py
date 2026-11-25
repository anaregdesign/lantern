#!/usr/bin/env python3
"""Run the selected native Windows boundary tests; empty matches fail."""

import argparse
import os
from pathlib import Path
import re
import subprocess
import sys


RUST_TESTS = ("transport::tests::native_health_tls_checks_server_root_and_client_identity",)


def require_rust(output, name):
    # Child Go server output can interrupt libtest's name ... ok line.
    # Require the exact selected name and the complete one-test result instead
    # of treating an interleaved successful run as a missing test.
    clean = re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", output)
    names = re.findall(r"(?m)^test " + re.escape(name) + r" \.\.\.", clean)
    summaries = re.findall(r"(?m)^test result: (\w+)\. (\d+) passed; (\d+) failed; (\d+) ignored;", clean)
    if len(names) != 1 or summaries != [("ok", "1", "0", "0")]:
        raise ValueError("selected native Rust test did not pass: " + name)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--evidence", type=Path, required=True)
    args = parser.parse_args()
    if os.name != "nt":
        parser.error("native Windows execution required")
    sys.stdout.reconfigure(encoding="utf-8", errors="replace")
    args.evidence.mkdir(parents=True, exist_ok=False)
    root = Path.cwd()
    env = os.environ.copy()
    def run(name, command, directory=root):
        result = subprocess.run(command, cwd=directory, env=env,
                                stdout=subprocess.PIPE, stderr=subprocess.STDOUT, check=False)
        (args.evidence / (name + ".log")).write_bytes(result.stdout)
        output = result.stdout.decode("utf-8", errors="replace")
        print(output, end="", flush=True)
        if result.returncode:
            raise ValueError(f"{name} failed ({result.returncode})")
        return output

    directory = root / "sdks/rust"
    name = "smoke::native_tls_roots_are_available_by_default"
    require_rust(run("native-roots", ["cargo", "test", "--locked", "--no-default-features",
                 "--lib", name, "--", "--exact"], directory), name)
    name = "value::tests::timestamps_validate_full_proto_range_and_distinguish_zero_time"
    require_rust(run("native-values", ["cargo", "test", "--locked", "--lib", name,
                 "--", "--exact"], directory), name)
    for index, name in enumerate(RUST_TESTS):
        require_rust(run(f"native-wire-{index}", ["cargo", "test", "--locked", "--all-features",
            "--lib", name, "--", "--exact", "--test-threads=1"], directory), name)


if __name__ == "__main__":
    main()
