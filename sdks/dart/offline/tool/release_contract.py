#!/usr/bin/env python3
"""Check the exact pub archive for the storage-neutral offline release."""

import argparse
from pathlib import Path
import re
import sys
import tarfile

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "scripts"))
import release  # noqa: E402


PACKAGE = "lantern_client_offline"
ROOT_FILES = {
    "pubspec.yaml", "README.md", "CHANGELOG.md", "LICENSE",
    "analysis_options.yaml",
}


def hosted_parent_state(pubspec, lockfile, parent_version, candidate, sha256):
    """Read-only readiness check; source overrides never establish hosted proof."""
    if runtime_dependencies(pubspec.read_text()) != {"lantern_client": f"^{parent_version}"}:
        raise ValueError("offline hosted dependency must match the paired parent version")
    files = release.candidate_files(candidate, sha256)
    metadata = release.version_metadata(parent_version)
    if metadata is None:
        return "pending", f"parent {parent_version} is not published; publish and verify it first"
    # Even a stale lock must not hide a failed or mismatched parent archive.
    release.compare_published(parent_version, metadata, files)
    lock = lockfile.read_text() if lockfile.exists() else ""
    block = re.search(r"(?ms)^  lantern_client:\n(.*?)(?=^  \S|^\S|\Z)", lock)
    if not block:
        return "pending", "missing hosted parent lock; refresh the offline lock after parent verification"
    block = block[1]
    if not re.search(r"^    source: hosted$", block, re.M) or not re.search(
        r'^      url: ["\']?https://pub.dev["\']?$', block, re.M
    ) or not re.search(r'^      name: lantern_client$', block, re.M):
        raise ValueError("offline parent lock must use pub.dev hosted source without overrides")
    version = re.search(r'^    version: ["\']?(\d+\.\d+\.\d+)["\']?$', block, re.M)
    if not version or version[1] != parent_version:
        return "pending", "stale hosted parent lock; refresh the offline lock after parent verification"
    checksum = re.search(r'^      sha256: ["\']?([0-9a-f]{64})["\']?$', block, re.M)
    if not checksum or checksum[1] != metadata["archive_sha256"]:
        raise ValueError("hosted parent lock checksum differs from verified pub.dev archive")
    return "ready", f"parent {parent_version} archive bytes and hosted lock verified; offline archive still needs qualification"


def runtime_dependencies(pubspec):
    section = None
    dependencies = {}
    for line in pubspec.splitlines():
        if match := re.match(r"^([a-z_]+):(?:\s.*)?$", line):
            section = match.group(1)
        elif section == "dependencies" and (match := re.match(
            r"^  ([^\s:]+):\s*(\S.*)?$", line
        )):
            dependencies[match.group(1)] = match.group(2) or ""
    return dependencies


def validate(archive_path, version, parent_version):
    files = release.archive_files(Path(archive_path).read_bytes())
    missing = ROOT_FILES - files.keys()
    if missing:
        raise ValueError(f"offline archive is missing required files: {sorted(missing)}")
    unexpected = sorted(
        path for path in files
        if path not in ROOT_FILES and not (path.startswith("lib/") and path.endswith(".dart"))
    )
    if unexpected:
        raise ValueError(f"offline archive has repository-only files: {unexpected}")
    if not any(path.endswith(".dart") for path in files if path.startswith("lib/")):
        raise ValueError("offline archive contains no Dart library")
    with tarfile.open(archive_path, mode="r:gz") as archive:
        pubspec = archive.extractfile("pubspec.yaml").read().decode("utf-8")
    if not re.search(rf"^name: {PACKAGE}$", pubspec, re.MULTILINE):
        raise ValueError("offline archive has the wrong package name")
    if not re.search(rf"^version: {re.escape(version)}$", pubspec, re.MULTILINE):
        raise ValueError("offline archive version does not match the tag")
    if re.search(r"^(publish_to|dependency_overrides):", pubspec, re.MULTILINE):
        raise ValueError("offline archive has a publication block or local override")
    if re.search(r"^\s+(path|git):", pubspec, re.MULTILINE):
        raise ValueError("offline archive has a non-hosted dependency")
    dependencies = runtime_dependencies(pubspec)
    if dependencies != {"lantern_client": f"^{parent_version}"}:
        raise ValueError(f"offline runtime dependencies are not storage-neutral: {dependencies}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--archive", type=Path)
    parser.add_argument("--version")
    parser.add_argument("--parent-version", required=True)
    parser.add_argument("--check-parent", action="store_true")
    parser.add_argument("--parent-candidate", type=Path)
    parser.add_argument("--parent-sha256")
    parser.add_argument("--pubspec", type=Path)
    parser.add_argument("--lockfile", type=Path)
    parser.add_argument("--output", type=Path)
    parser.add_argument("--summary", type=Path)
    parser.add_argument("--require-hosted", action="store_true")
    args = parser.parse_args()
    for value in (args.parent_version, *([args.version] if args.version else [])):
        if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", value):
            parser.error("versions must have exact X.Y.Z form")
    if args.check_parent:
        if not all((args.parent_candidate, args.parent_sha256, args.pubspec, args.lockfile)):
            parser.error("parent readiness requires candidate, checksum, pubspec and lockfile")
        state, reason = hosted_parent_state(args.pubspec, args.lockfile, args.parent_version,
                                            args.parent_candidate, args.parent_sha256)
        message = f"Offline hosted qualification {state}: {reason}."
        print(message)
        if args.summary:
            with args.summary.open("a") as output:
                output.write(message + "\n")
        if args.output:
            with args.output.open("a") as output:
                output.write(f"parent_ready={str(state == 'ready').lower()}\n")
        if state != "ready" and args.require_hosted:
            raise ValueError(message)
        return
    if not args.archive or not args.version:
        parser.error("archive validation requires --archive and --version")
    validate(args.archive, args.version, args.parent_version)
    print(f"{PACKAGE} {args.version} archive contract passed")


if __name__ == "__main__":
    main()
