#!/usr/bin/env python3
"""Verify a tagged Go SDK against the published pb module outside go.work."""

import argparse
import io
import json
import os
from pathlib import Path
import re
import shlex
import subprocess
import sys
import tarfile
import tempfile


SDK_MODULE = "github.com/anaregdesign/lantern/sdks/go"
PB_MODULE = "github.com/anaregdesign/lantern/pb"
SEMVER = r"v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)"
SDK_TAG = re.compile(rf"sdks/go/{SEMVER}\Z")
PB_VERSION = re.compile(rf"{SEMVER}\Z")


class ReleaseError(Exception):
    pass


def command(args, *, cwd, env=None, capture=False):
    try:
        result = subprocess.run(
            args, cwd=cwd, env=env, check=True,
            stdout=subprocess.PIPE if capture else None,
            stderr=subprocess.PIPE if capture else None,
        )
    except subprocess.CalledProcessError as error:
        detail = b"\n".join(
            output for output in (error.stderr, error.stdout) if output
        ).decode("utf-8", "replace").strip()
        raise ReleaseError(
            f"{shlex.join(args)} failed" + (f": {detail}" if detail else "")
        ) from error
    except OSError as error:
        raise ReleaseError(f"cannot run {args[0]}: {error}") from error
    return result.stdout if capture else b""


def git(repo, *args):
    return command(["git", *args], cwd=repo, capture=True)


def go(source, env, *args, capture=False):
    return command(["go", *args], cwd=source, env=env, capture=capture)


def go_json(source, env, *args):
    try:
        value = json.loads(go(source, env, *args, capture=True))
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise ReleaseError(f"go {' '.join(args)} returned invalid JSON") from error
    if not isinstance(value, dict):
        raise ReleaseError(f"go {' '.join(args)} returned a non-object JSON value")
    return value


def check_sdk_tag(repo, tag):
    if not SDK_TAG.fullmatch(tag):
        raise ReleaseError("SDK tag must be sdks/go/vX.Y.Z")
    try:
        tagged = git(repo, "rev-parse", "--verify", f"refs/tags/{tag}^{{commit}}")
    except ReleaseError as error:
        raise ReleaseError(f"SDK tag {tag} is missing") from error
    if tagged.strip() != git(repo, "rev-parse", "HEAD").strip():
        raise ReleaseError(f"checkout HEAD does not match SDK tag {tag}")


def archive_sdk(repo, tag, source):
    archive = git(repo, "archive", "--format=tar", f"refs/tags/{tag}:sdks/go")
    with tarfile.open(fileobj=io.BytesIO(archive), mode="r:") as contents:
        contents.extractall(source, filter="data")
    if not (source / "go.mod").is_file():
        raise ReleaseError(f"{tag} has no sdks/go/go.mod")


def required_pb_version(manifest):
    if (manifest.get("Module") or {}).get("Path") != SDK_MODULE:
        raise ReleaseError("SDK go.mod declares the wrong module path")
    requirements = [
        item for item in manifest.get("Require") or []
        if item.get("Path") == PB_MODULE
    ]
    if len(requirements) != 1 or requirements[0].get("Indirect"):
        raise ReleaseError("SDK go.mod must directly require exactly one pb version")
    version = requirements[0].get("Version", "")
    if not PB_VERSION.fullmatch(version):
        raise ReleaseError("SDK pb requirement must be a tagged vX.Y.Z, not a pseudo-version")
    return version


def remove_local_replacements(source, env):
    manifest = go_json(source, env, "mod", "edit", "-json")
    version = required_pb_version(manifest)
    replacements = manifest.get("Replace") or []
    if len(replacements) > 1:
        raise ReleaseError("SDK go.mod has unexpected replace directives")
    if replacements:
        replacement = replacements[0]
        old = replacement.get("Old") or {}
        new = replacement.get("New") or {}
        if (old.get("Path") != PB_MODULE or old.get("Version")
                or new.get("Path") != "../../pb" or new.get("Version")):
            raise ReleaseError("SDK go.mod has an unexpected replace directive")
        go(source, env, "mod", "edit", f"-dropreplace={PB_MODULE}")

    sanitized = go_json(source, env, "mod", "edit", "-json")
    if required_pb_version(sanitized) != version:
        raise ReleaseError("SDK pb requirement changed while removing local replacements")
    if sanitized.get("Replace"):
        raise ReleaseError("a go.mod replacement leaked into the isolated SDK")
    return version


def pb_go_files(repo, ref):
    names = git(repo, "ls-tree", "-r", "-z", "--name-only", f"{ref}:pb")
    files = {
        name.decode("utf-8") for name in names.split(b"\0") if name
        and (name == b"go.mod" or name.endswith(b".go"))
    }
    if "go.mod" not in files or not any(name.endswith(".go") for name in files):
        raise ReleaseError(f"{ref} has no usable pb Go module")
    return files


def check_pb_tag(repo, sdk_tag, version):
    pb_ref = f"refs/tags/pb/{version}"
    try:
        git(repo, "rev-parse", "--verify", f"{pb_ref}^{{commit}}")
    except ReleaseError as error:
        raise ReleaseError(f"pb/{version} tag is missing; publish pb before the SDK") from error
    tagged_files = pb_go_files(repo, pb_ref)
    sdk_ref = f"refs/tags/{sdk_tag}"
    sdk_files = pb_go_files(repo, sdk_ref)
    if tagged_files != sdk_files or any(
        git(repo, "show", f"{pb_ref}:pb/{name}")
        != git(repo, "show", f"{sdk_ref}:pb/{name}")
        for name in sorted(sdk_files)
    ):
        raise ReleaseError(
            f"pb/{version} is stale: its Go sources/go.mod differ from {sdk_tag}; "
            "tag the current pb module and update the SDK requirement"
        )


def isolated_go_env(temp):
    env = os.environ.copy()
    env.update({
        "GOWORK": "off",
        "GOFLAGS": "",
        "GOTOOLCHAIN": "local",
        "GOMODCACHE": str(temp / "gomodcache"),
        "GOPROXY": "https://proxy.golang.org",
        "GOSUMDB": "sum.golang.org",
        "GOPRIVATE": "",
        "GONOPROXY": "",
        "GONOSUMDB": "",
    })
    return env


def download_pb(checksum_dir, env, version):
    if any((checksum_dir / name).exists() for name in ("go.mod", "go.sum")):
        raise ReleaseError("pb checksum verification must not reuse a module's go.sum")
    try:
        module = go_json(checksum_dir, env, "mod", "download", "-json", f"{PB_MODULE}@{version}")
    except ReleaseError as error:
        raise ReleaseError(f"published pb/{version} cannot be resolved: {error}") from error
    if module.get("Error"):
        raise ReleaseError(f"published pb/{version} cannot be resolved: {module['Error']}")
    if module.get("Path") != PB_MODULE or module.get("Version") != version:
        raise ReleaseError(f"published pb/{version} resolved to the wrong module or version")
    if not all(str(module.get(field, "")).startswith("h1:") for field in ("Sum", "GoModSum")):
        raise ReleaseError(f"published pb/{version} has no verified module checksums")
    directory = Path(module.get("Dir") or "")
    if not directory.is_absolute() or not directory.is_dir():
        raise ReleaseError(f"published pb/{version} has no downloaded module directory")
    if not directory.resolve().is_relative_to(Path(env["GOMODCACHE"]).resolve()):
        raise ReleaseError(f"published pb/{version} was not downloaded to the isolated cache")
    return directory, module["Sum"], module["GoModSum"]


def check_published_pb_source(repo, sdk_tag, directory):
    expected = pb_go_files(repo, f"refs/tags/{sdk_tag}")
    published = {
        file.relative_to(directory).as_posix()
        for file in directory.rglob("*.go")
    }
    if (directory / "go.mod").is_file():
        published.add("go.mod")
    if expected != published:
        raise ReleaseError(
            "published pb tag has different Go files from the SDK tag "
            f"(missing: {sorted(expected - published)}, extra: {sorted(published - expected)})"
        )
    for name in sorted(expected):
        if git(repo, "show", f"refs/tags/{sdk_tag}:pb/{name}") != (directory / name).read_bytes():
            raise ReleaseError(f"published pb tag differs from the SDK tag at pb/{name}")


def add_verified_pb_checksums(source, env, version, pb_sum, pb_mod_sum):
    go_mod = source / "go.mod"
    original_go_mod = go_mod.read_bytes()
    go(source, env, "mod", "download", f"{PB_MODULE}@{version}")
    if go_mod.read_bytes() != original_go_mod:
        raise ReleaseError("SDK go.mod changed while recording published pb checksums")
    try:
        lines = (source / "go.sum").read_text().splitlines()
    except (OSError, UnicodeDecodeError) as error:
        raise ReleaseError("isolated SDK has no valid go.sum after pb download") from error
    expected = {version: pb_sum, version + "/go.mod": pb_mod_sum}
    found = set()
    for line in lines:
        parts = line.split()
        if len(parts) == 3 and parts[0] == PB_MODULE and parts[1] in expected:
            if parts[2] != expected[parts[1]]:
                raise ReleaseError("SDK go.sum conflicts with published pb checksums")
            found.add(parts[1])
    if found != expected.keys():
        raise ReleaseError("isolated SDK is missing published pb checksums")


def check_selected_pb(source, env, version):
    selected = go_json(source, env, "list", "-mod=readonly", "-m", "-json", PB_MODULE)
    if (selected.get("Path") != PB_MODULE or selected.get("Version") != version
            or selected.get("Replace")):
        raise ReleaseError(f"isolated SDK selected a different or replaced pb module: {selected}")


def verify_release(repo, tag):
    repo = repo.resolve()
    check_sdk_tag(repo, tag)
    with tempfile.TemporaryDirectory(prefix="lantern-go-sdk-release-") as temp_path:
        temp = Path(temp_path)
        source = temp / "sdks-go"
        source.mkdir()
        if repo in source.resolve().parents:
            raise ReleaseError("SDK must be extracted outside the repository")
        env = isolated_go_env(temp)
        archive_sdk(repo, tag, source)
        version = remove_local_replacements(source, env)
        check_pb_tag(repo, tag, version)
        # The tagged SDK's go.sum must not satisfy the public checksum check.
        checksum_dir = temp / "pb-checksum"
        checksum_dir.mkdir()
        published, pb_sum, pb_mod_sum = download_pb(checksum_dir, env, version)
        check_published_pb_source(repo, tag, published)
        add_verified_pb_checksums(source, env, version, pb_sum, pb_mod_sum)
        check_selected_pb(source, env, version)
        go(source, env, "build", "-mod=readonly", "./...")
        go(source, env, "test", "-mod=readonly", "./...")
    print(f"Verified {tag} in isolation against published pb/{version}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--tag", required=True)
    parser.add_argument("--repo", type=Path, default=Path.cwd())
    args = parser.parse_args()
    try:
        verify_release(args.repo, args.tag)
    except ReleaseError as error:
        print(f"::error::{error}", file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
