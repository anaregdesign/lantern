#!/usr/bin/env python3
"""Conservative configured-source obligation; unknown changes require configured-source-integration."""

import json
import os
from pathlib import Path, PurePosixPath

import ci_docs
import evidence_only_pr
import validation_scope


# These application-only paths cannot change native source acquisition or its
# Go composition/build inputs. functional-validation still exercises the public application path.
# Additions/deletions/renames and any path outside this small reviewed set need
# configured-source-integration. In particular all CI, fixtures, policy, manifests/locks, native/deployment,
# native/runtime Server and Core, generated and public SDK source retain configured-source-integration by default.
# Exact profiling handlers do not include public_metrics.go: its certified-runtime
# readiness checks are production composition and retain configured-source-integration.
APPLICATION_PATHS = ("admin/app/", "sdks/dart/offline/lib/", "sdks/dart/offline_sqlite/lib/",
                     "core/collection/", "core/nlp/", "core/model/", "core/search/")
DIAGNOSTICS = {"server/provider/pprof.go", "server/provider/pprof_test.go",
               "server/provider/runtime_profiling.go", "server/provider/runtime_profiling_test.go"}
APPLICATION_SUFFIXES = {".ts", ".tsx", ".css", ".dart", ".go"}


def requires_qualification(changes):
    if not changes:
        return True
    for path, status in changes.items():
        p = PurePosixPath(path)
        # Reader-only inputs cannot alter the product's native source acquisition.
        # Their focused consumer contracts are selected by the same finite map;
        # unknown/mixed boundary changes below still require integration.
        if validation_scope.harness_group(path, status):
            continue
        if (status != "M" or p.is_absolute() or ".." in p.parts or p.as_posix() != path
                or not (path.startswith(APPLICATION_PATHS) or path in DIAGNOSTICS)
                or p.suffix not in APPLICATION_SUFFIXES):
            return True
    return False


def classify(environ):
    try:
        revisions = ci_docs.change_range(environ)
        if revisions is None:
            return True, "missing or unsupported change range"
        changes = evidence_only_pr.changed_files(*revisions)
        required = requires_qualification(changes)
        # A symlink or type change is not an application source edit.
        for path in changes:
            for revision in revisions:
                if evidence_only_pr.git("ls-tree", revision, "--", path).split()[0] != "100644":
                    return True, "nonregular or missing source"
        return required, "boundary or unknown change" if required else "reviewed application or harness-only edits"
    except Exception as error:
        return True, f"classification unavailable: {type(error).__name__}"


def main():
    required, reason = classify(dict(os.environ))
    result = f"configured_source_integration_required={str(required).lower()}\n"
    print(json.dumps({"configured_source_integration_required": required, "reason": reason}))
    with Path(os.environ["GITHUB_OUTPUT"]).open("a") as stream:
        stream.write(result)
    if summary := os.environ.get("GITHUB_STEP_SUMMARY"):
        with Path(summary).open("a") as stream:
            stream.write(f"Configured source integration required: {required} ({reason}). configured-source-integration NOT_RUN is not release qualification.\n")


if __name__ == "__main__":
    main()
