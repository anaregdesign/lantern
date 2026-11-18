#!/usr/bin/env python3
"""Select the Dart workflow jobs that consume a change."""

from __future__ import annotations

import os
from pathlib import Path
import re
import subprocess
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[3] / ".github/scripts"))
import ci_docs
import evidence_only_pr
import validation_scope


JOBS = {"parent", "offline", "sqlite", "mobile", "full"}
DART_WORKFLOW = ".github/workflows/dart-sdk.yml"
EXAMPLE_DEPENDENCIES = {"sdks/dart/example/pubspec.yaml", "sdks/dart/example/pubspec.lock"}
WIRE = {"parent", "offline", "sqlite"}
# These are live shell and Go inputs of the hosted Dart jobs. A change under
# another integration-test path is unclassified and keeps the broad fallback.
HOST_FIXTURES = {
    "tests/integration/dart_offline_sqlite_test.dart": {"sqlite"},
    "tests/integration/dart_identity_offline_sqlite_test.dart": {"sqlite"},
    "tests/integration/dart_data_client_test.dart": {"parent"},
    "tests/integration/dart_contribution_delete_test.dart": {"parent"},
    "testbed/scripts/dart_identity_fixture.sh": {"sqlite"},
    "testbed/scripts/native_head_fixture.sh": {"sqlite"},
    "testbed/scripts/dart_receipt_fixture.sh": WIRE,
    "testbed/scripts/native_receipt_fixture.sh": WIRE,
    "tests/integration/auth_test.go": WIRE,
    "tests/integration/auth_authority_helpers_test.go": WIRE,
    ".github/scripts/configured_time_input.py": WIRE,
}


def selected_jobs(changes, impact):
    if not changes or impact["reason"].startswith("classification unavailable"):
        return JOBS.copy(), "unavailable change range"
    paths = {path for path in changes if not ci_docs.is_document(path)}
    if not paths:
        return set(), "documentation"
    semantics = impact.get("semantic", {})
    jobs = WIRE.copy() if impact.get("backend_dependencies") else set()
    if paths <= EXAMPLE_DEPENDENCIES and all(semantics.get(path) == "package-dependency" for path in paths):
        jobs.add("example_lock")
    if "release-host" in impact.get("fixture_contracts", ()):
        jobs.add("release_host")
        paths.difference_update(path for path in validation_scope.RELEASE_HOST if changes.get(path) == "M")
    if semantics.get(DART_WORKFLOW) == "setup-dart":
        # The standard Dart, minimum-version and hosted-archive jobs invoke
        # this action. Flutter-owned jobs use a separate installation.
        jobs.update(("parent", "offline", "full"))
    paths.difference_update(path for path, kind in semantics.items() if kind != "package-dependency")
    if not paths:
        reason = "release host contracts" if "release_host" in jobs else "verified dependency and Action consumers"
        return jobs, reason

    # Keep exact-source physical evidence, distribution tools and isolated
    # package tests separate from app-source changes selected for simulators.
    for path in sorted(paths.copy()):
        if path.startswith("sdks/dart/example/evidence/"):
            jobs.update(WIRE | {"full"})
        elif path == "sdks/dart/.pubignore" or path in validation_scope.RELEASE_ARCHIVE:
            jobs.update({"parent", "offline", "full"})
        elif "/test/support/" in path or path.endswith("/test/real_wire_test.dart"):
            # Host integration and multiple packages import these fixtures.
            # Keep their normal package closure, including SQLite and example.
            continue
        elif path.startswith("sdks/dart/test/"):
            jobs.update({"parent", "full"})
        elif path.startswith("sdks/dart/offline/test/"):
            jobs.update({"offline", "full"})
        elif path.startswith("sdks/dart/offline_sqlite/test/"):
            jobs.add("sqlite")
        else:
            continue
        paths.remove(path)
    for path in sorted(paths.intersection(HOST_FIXTURES)):
        jobs.update(HOST_FIXTURES[path])
        paths.remove(path)
    if not paths:
        return jobs, "package tests, archive or fixture consumers"
    # Release tools, tests, fixtures and action pins above have more precise
    # consumers. Apply the shared module map to the remaining inputs so that
    # adding an unrelated file cannot broaden a known test-only change.
    residual = validation_scope.affected(
        {p: changes[p] for p in paths}, {p: semantics[p] for p in paths if p in semantics}
    )
    if residual["full"]:
        return jobs | JOBS, "shared or unclassified inputs"

    if "server" in residual["go_modules"] or "controlled-native-provider" in residual["fixture_contracts"]:
        jobs.update(WIRE)
    for family in residual["families"]:
        if family in {"dart", "offline", "sqlite", "example"}:
            jobs.add({"dart": "parent", "example": "mobile"}.get(family, family))
            jobs.add("full")
    if "example" in residual["families"] and any(path.startswith("sdks/dart/example/") for path in paths):
        # The SQLite real-wire job also runs the maintained example application.
        jobs.add("sqlite")
    return jobs, "affected modules and SDK consumers"


def resource_profile(event, ref):
    if event == "push" and re.fullmatch(r"refs/tags/sdks/dart/v[0-9]+\.[0-9]+\.[0-9]+", ref):
        return "parent-contracts"
    if event == "push" and re.fullmatch(r"refs/tags/sdks/dart/offline/v[0-9]+\.[0-9]+\.[0-9]+", ref):
        return "full"
    return "unit-contracts"


def main():
    env = dict(os.environ)
    jobs, reason = JOBS.copy(), "release, manual run, or unavailable change range"
    docs_only = evidence_only = False
    try:
        revisions = ci_docs.change_range(env)
        if revisions is not None:
            paths = evidence_only_pr.changed_files(*revisions)
            docs_only = ci_docs.documentation_only(list(paths))
            evidence_only = env["GITHUB_EVENT_NAME"] == "pull_request" and evidence_only_pr.is_candidate(paths)
            if docs_only or evidence_only:
                jobs, reason = set(), "documentation or physical evidence"
            else:
                impact = validation_scope.classify_revisions(Path.cwd(), *revisions)
                jobs, reason = selected_jobs(paths, impact)
    except (KeyError, UnicodeError, ValueError, OSError, subprocess.CalledProcessError):
        jobs, reason = JOBS.copy(), "unavailable change range"
        docs_only = evidence_only = False
    if docs_only:
        ci_docs.validate(revisions)

    profile = resource_profile(env["GITHUB_EVENT_NAME"], env["GITHUB_REF"])
    flags = {name: name in jobs for name in sorted(JOBS | {"release_host", "example_lock"})}
    flags.update(evidence_only=evidence_only, docs_only=docs_only)
    output = f"resource_profile={profile}\n" + "".join(
        f"{name}={str(selected).lower()}\n" for name, selected in flags.items()
    )
    with Path(env["GITHUB_OUTPUT"]).open("a", encoding="utf-8") as stream:
        stream.write(output)
    with Path(env["GITHUB_STEP_SUMMARY"]).open("a", encoding="utf-8") as stream:
        print(f"Dart jobs ({reason}): {', '.join(sorted(jobs)) or 'none'}. "
              f"Resource profile: {profile}.", file=stream)
    print(output, end="")


if __name__ == "__main__":
    main()
