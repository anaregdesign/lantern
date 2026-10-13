#!/usr/bin/env python3
"""Required Build & Test aggregation; executed Q failure never becomes NOT_RUN."""

import os
from pathlib import Path


def accepted(values):
    if values.get("SCOPE_RESULT") != "success" or values.get("MODULE_RESULT") != "success":
        return False
    exempt = values.get("DOCS_ONLY") == "true" or values.get("EVIDENCE_ONLY") == "true"
    if exempt:
        return values.get("F_RESULT") == "skipped" and values.get("Q_RESULT") == "skipped"
    if values.get("F_RESULT") != "success":
        return False
    q = values.get("Q_RESULT")
    return q == "success" or (values.get("Q_REQUIRED") == "false" and q == "skipped")


def main():
    values = dict(os.environ)
    result = accepted(values)
    statuses = {key: values.get(key, "UNKNOWN") for key in
                ("SCOPE_RESULT", "MODULE_RESULT", "F_RESULT", "Q_RESULT", "Q_REQUIRED")}
    line = f"Current authority result: {statuses}; accepted={result}. Q skipped means NOT_RUN, never real-source qualification."
    print(line)
    if summary := values.get("GITHUB_STEP_SUMMARY"):
        with Path(summary).open("a") as stream:
            stream.write(line + "\n")
    raise SystemExit(0 if result else 1)


if __name__ == "__main__":
    main()
