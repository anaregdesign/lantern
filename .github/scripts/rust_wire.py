#!/usr/bin/env python3
"""Run native Rust wire coverage for the local Server's supported profile."""

import platform
import subprocess


# These root-provisioned cases have their own mandatory shared functional-validation/configured-source-integration owner.
ROOT_FIXTURES = (
    "scoped_changes::tests::real_public_scoped_wire",
    "security::tests::real_public_current_wire",
)

def command(system):
    if system not in ("Linux", "Darwin"):
        raise ValueError("unsupported native wire platform")
    result = ["cargo", "test", "--locked", "--all-features", "--", "--ignored", "--test-threads=1"]
    for name in ROOT_FIXTURES:
        result.extend(("--skip", name))
    return result


if __name__ == "__main__":
    system = platform.system()
    raise SystemExit(subprocess.run(command(system), check=False).returncode)
