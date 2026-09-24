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
    parser.add_argument("--archive", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--parent-version", required=True)
    args = parser.parse_args()
    for value in (args.version, args.parent_version):
        if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", value):
            parser.error("versions must have exact X.Y.Z form")
    validate(args.archive, args.version, args.parent_version)
    print(f"{PACKAGE} {args.version} archive contract passed")


if __name__ == "__main__":
    main()
