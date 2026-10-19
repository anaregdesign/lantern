"""Compare the actual npm archive with an independently rebuilt tag candidate."""

import argparse
import hashlib
import json
from pathlib import Path, PurePosixPath
import tarfile


def package_files(archive, version):
    files = {}
    with tarfile.open(archive, "r:gz") as contents:
        for member in contents:
            name = member.name.rstrip("/")
            path = PurePosixPath(name)
            if (str(path) != name or path.is_absolute() or ".." in path.parts
                    or not path.parts or path.parts[0] != "package"):
                raise ValueError(f"invalid npm archive path: {member.name}")
            if member.isdir():
                continue
            if not member.isfile() or name in files:
                raise ValueError(f"non-file or duplicate npm archive entry: {name}")
            files[name] = (contents.extractfile(member).read(), member.mode & 0o111)
    manifest = json.loads(files["package/package.json"][0])
    if manifest.get("name") != "lantern-sdk" or manifest.get("version") != version:
        raise ValueError("npm archive package identity/version mismatch")
    return files


def compare_archives(candidate, published, version):
    expected = package_files(candidate, version)
    actual = package_files(published, version)
    if expected.keys() != actual.keys():
        raise ValueError("npm archive file set differs: " + json.dumps({
            "missing": sorted(expected.keys() - actual.keys()),
            "extra": sorted(actual.keys() - expected.keys()),
        }))
    changed = [name for name in sorted(expected) if expected[name] != actual[name]]
    if changed:
        raise ValueError("npm archive file bytes/executable mode differ: " + ", ".join(changed))
    manifest = [{"path": name, "sha256": hashlib.sha256(data).hexdigest(),
                 "executable_mode": mode} for name, (data, mode) in sorted(actual.items())]
    return {"package": "lantern-sdk", "version": version, "verdict": "pass",
            "files": len(manifest), "file_manifest": manifest,
            "candidate_archive_sha256": hashlib.sha256(Path(candidate).read_bytes()).hexdigest(),
            "published_archive_sha256": hashlib.sha256(Path(published).read_bytes()).hexdigest()}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--candidate", type=Path, required=True)
    parser.add_argument("--published", type=Path, required=True)
    parser.add_argument("--version", required=True)
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    result = compare_archives(args.candidate, args.published, args.version)
    args.out.write_text(json.dumps(result, indent=2) + "\n")
    print(f"Verified {result['files']} npm archive files against the exact-tag candidate")
