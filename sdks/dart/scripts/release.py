#!/usr/bin/env python3
"""Read-only pub.dev state and archive checks for Lantern Dart releases."""

import argparse
import hashlib
import io
import json
from pathlib import Path, PurePosixPath
import re
import tarfile
import time
from urllib.error import HTTPError
from urllib.request import urlopen

PACKAGE = "lantern_client"
API = f"https://pub.dev/api/packages/{PACKAGE}"
MAX_ARCHIVE_BYTES = 128 * 1024 * 1024


def fetch(url, limit=MAX_ARCHIVE_BYTES):
    """Only a real HTTP 404 is absence; all other transport failures abort."""
    try:
        with urlopen(url, timeout=30) as response:
            if response.status != 200:
                raise ValueError(f"unexpected HTTP status: {response.status}")
            body = response.read(limit + 1)
            if len(body) > limit:
                raise ValueError("pub.dev response exceeds size limit")
            return body
    except HTTPError as error:
        with error:
            if error.code == 404:
                return None
            raise


def version_metadata(version, package=PACKAGE):
    body = fetch(f"https://pub.dev/api/packages/{package}/versions/{version}")
    if body is None:
        return None
    metadata = json.loads(body)
    if (
        metadata.get("version") != version
        or metadata.get("pubspec", {}).get("name") != package
        or metadata.get("pubspec", {}).get("version") != version
    ):
        raise ValueError("pub.dev metadata does not match the requested package/version")
    return metadata


def archive_files(body):
    """Compare package paths/bytes, ignoring tar ordering and timestamps."""
    files = {}
    seen = set()
    total = 0
    with tarfile.open(fileobj=io.BytesIO(body), mode="r:gz") as archive:
        for entry in archive:
            name = entry.name
            while name.startswith("./"):
                name = name[2:]
            path = PurePosixPath(name)
            if (
                not name
                or path.is_absolute()
                or ".." in path.parts
                or "\\" in name
                or "\x00" in name
                or str(path) != name.rstrip("/")
                or name in seen
            ):
                raise ValueError(f"unsafe or duplicate archive path: {entry.name}")
            seen.add(name)
            if entry.isdir():
                continue
            if not entry.isfile():
                raise ValueError(f"archive contains a link or special file: {name}")
            total += entry.size
            if total > MAX_ARCHIVE_BYTES:
                raise ValueError("unpacked archive exceeds size limit")
            with archive.extractfile(entry) as source:
                files[name] = hashlib.sha256(source.read()).hexdigest()
    if "pubspec.yaml" not in files:
        raise ValueError("archive contains no root pubspec.yaml")
    return files


def candidate_files(candidate, expected_sha256):
    body = candidate.read_bytes()
    if hashlib.sha256(body).hexdigest() != expected_sha256:
        raise ValueError("candidate archive checksum does not match preflight")
    return archive_files(body)


def compare_published(version, metadata, candidate, package=PACKAGE):
    archive_url = f"https://pub.dev/api/archives/{package}-{version}.tar.gz"
    if metadata.get("archive_url") != archive_url:
        raise ValueError("pub.dev returned an unexpected archive URL")
    checksum = metadata.get("archive_sha256", "")
    if not re.fullmatch(r"[0-9a-f]{64}", checksum):
        raise ValueError("pub.dev returned an invalid archive checksum")
    body = fetch(archive_url)
    if body is None:
        raise ValueError("pub.dev version exists but its archive returned 404")
    if hashlib.sha256(body).hexdigest() != checksum:
        raise ValueError("published archive checksum does not match pub.dev metadata")
    published = archive_files(body)
    if published != candidate:
        missing = sorted(candidate.keys() - published.keys())
        extra = sorted(published.keys() - candidate.keys())
        changed = sorted(name for name in candidate.keys() & published.keys()
                         if candidate[name] != published[name])
        raise ValueError(
            f"published archive differs from candidate: missing={missing}, "
            f"extra={extra}, changed={changed}"
        )


def preflight(version, candidate, package=PACKAGE):
    package_body = fetch(f"https://pub.dev/api/packages/{package}")
    if package_body is None:
        raise ValueError("pub.dev package returned 404; automated publication is blocked")
    if json.loads(package_body).get("name") != package:
        raise ValueError("pub.dev returned an unexpected package")
    metadata = version_metadata(version, package)
    if metadata is None:
        return True
    compare_published(version, metadata, candidate, package)
    return False


def verify(version, candidate, attempts=30, delay=10, package=PACKAGE):
    for attempt in range(attempts):
        metadata = version_metadata(version, package)
        if metadata is not None:
            compare_published(version, metadata, candidate, package)
            return
        if attempt + 1 < attempts:
            time.sleep(delay)
    raise ValueError("published version did not become visible on pub.dev")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("phase", choices=("preflight", "verify"))
    parser.add_argument("--version", required=True)
    parser.add_argument("--package", choices=(PACKAGE, "lantern_client_offline"), default=PACKAGE)
    parser.add_argument("--candidate", type=Path, required=True)
    parser.add_argument("--sha256", required=True)
    parser.add_argument("--output", type=Path)
    args = parser.parse_args()
    if not re.fullmatch(r"[0-9]+\.[0-9]+\.[0-9]+", args.version):
        parser.error("version must have exact X.Y.Z form")
    candidate = candidate_files(args.candidate, args.sha256)
    if args.phase == "preflight":
        required = preflight(args.version, candidate, args.package)
        if args.output:
            with args.output.open("a") as output:
                output.write(f"publish_required={str(required).lower()}\n")
    else:
        verify(args.version, candidate, package=args.package)
    print(f"pub.dev {args.phase} passed for {args.package} {args.version}")


if __name__ == "__main__":
    main()
