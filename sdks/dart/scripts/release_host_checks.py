#!/usr/bin/env python3
"""Exercise release conditions, captured-marker bytes and the restart journal on the host."""

import argparse
from pathlib import Path
import subprocess
import sys


SCRIPTS = Path(__file__).resolve().parent
DART = SCRIPTS.parent


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--python-only", action="store_true",
                        help="run release-workflow and evidence-reader tests only")
    args = parser.parse_args()
    # Use repository fixtures. Never read a private device record, launch an app,
    # or contact a publication service as part of this host check.
    suites = {
        SCRIPTS: ("ci_scope_test", "release_test", "physical_contribution_release_gate_test"),
        DART / "offline/tool": ("release_contract_test", "physical_result_attestation_test",
                                "physical_receipt_attestation_test", "physical_release_gate_test",
                                "physical_release_source_test"),
    }
    for directory, modules in suites.items():
        subprocess.run([sys.executable, "-B", "-m", "unittest", *modules], cwd=directory, check=True)
    if not args.python_only:
        example = DART / "example"
        subprocess.run(["flutter", "pub", "get", "--enforce-lockfile"], cwd=example, check=True)
        subprocess.run(["flutter", "test", "--no-pub", "test/physical_result_marker_test.dart",
                        "test/receipt_attestation_test.dart"], cwd=example, check=True)


if __name__ == "__main__":
    main()
