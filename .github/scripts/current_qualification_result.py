#!/usr/bin/env python3
"""Required Build & Test aggregation; executed configured-source-integration failure never becomes NOT_RUN."""

import os
from pathlib import Path


def accepted(values):
    if values.get("SCOPE_RESULT") != "success" or values.get("MODULE_RESULT") != "success":
        return False
    exempt = values.get("DOCS_ONLY") == "true" or values.get("EVIDENCE_ONLY") == "true"
    windows = values.get("WINDOWS_RESULT")
    if exempt or values.get("WINDOWS_REQUIRED") == "false":
        if windows != "skipped":
            return False
    elif values.get("WINDOWS_REQUIRED") != "true" or windows != "success":
        return False
    if exempt:
        return (
            values.get("FUNCTIONAL_VALIDATION_RESULT") == "skipped"
            and values.get("CONFIGURED_SOURCE_INTEGRATION_RESULT") == "skipped"
        )
    if values.get("FUNCTIONAL_VALIDATION_RESULT") != "success":
        return False
    source_integration = values.get("CONFIGURED_SOURCE_INTEGRATION_RESULT")
    return source_integration == "success" or (
        values.get("CONFIGURED_SOURCE_INTEGRATION_REQUIRED") == "false"
        and source_integration == "skipped"
    )


def main():
    values = dict(os.environ)
    result = accepted(values)
    statuses = {key: values.get(key, "UNKNOWN") for key in (
        "SCOPE_RESULT", "MODULE_RESULT", "FUNCTIONAL_VALIDATION_RESULT",
        "CONFIGURED_SOURCE_INTEGRATION_RESULT", "CONFIGURED_SOURCE_INTEGRATION_REQUIRED",
        "WINDOWS_REQUIRED", "WINDOWS_RESULT",
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
