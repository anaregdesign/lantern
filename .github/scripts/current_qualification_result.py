#!/usr/bin/env python3
"""Required Build & Test aggregation; executed configured-source-integration failure never becomes NOT_RUN."""

import os
from pathlib import Path


def accepted(values):
    if values.get("SCOPE_RESULT") != "success" or values.get("MODULE_RESULT") != "success":
        return False
    exempt = values.get("DOCS_ONLY") == "true" or values.get("EVIDENCE_ONLY") == "true"
    for flag, outcome in (
        ("WINDOWS_REQUIRED", "WINDOWS_RESULT"),
        ("FUNCTIONAL_REQUIRED", "FUNCTIONAL_VALIDATION_RESULT"),
        ("CONFIGURED_SOURCE_INTEGRATION_REQUIRED", "CONFIGURED_SOURCE_INTEGRATION_RESULT"),
        ("FUZZ_REQUIRED", "FUZZ_RESULT"),
        ("DEPLOYMENT_REQUIRED", "DEPLOYMENT_RESULT"),
    ):
        selected = values.get(flag)
        if selected not in {"true", "false"}:
            return False
        expected = "success" if selected == "true" and not exempt else "skipped"
        if values.get(outcome) != expected:
            return False
    return True


def main():
    values = dict(os.environ)
    result = accepted(values)
    statuses = {key: values.get(key, "UNKNOWN") for key in (
        "SCOPE_RESULT", "MODULE_RESULT", "FUNCTIONAL_VALIDATION_RESULT",
        "CONFIGURED_SOURCE_INTEGRATION_RESULT", "CONFIGURED_SOURCE_INTEGRATION_REQUIRED",
        "WINDOWS_REQUIRED", "WINDOWS_RESULT", "FUNCTIONAL_REQUIRED",
        "FUZZ_REQUIRED", "FUZZ_RESULT", "DEPLOYMENT_REQUIRED", "DEPLOYMENT_RESULT",
    )}
    line = (
        f"Current authority result: {statuses}; accepted={result}. "
        "configured-source-integration skipped means NOT_RUN; "
        "it does not establish configured time-source integration."
    )
    print(line)
    if summary := values.get("GITHUB_STEP_SUMMARY"):
        with Path(summary).open("a") as stream:
            stream.write(line + "\n")
    raise SystemExit(0 if result else 1)


if __name__ == "__main__":
    main()
