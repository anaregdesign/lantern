#!/usr/bin/env python3
"""Read-only root release guards; receipts are written only below ignored dist.

The pinned Go version command emits runtime/debug.BuildInfo JSON. Under
-trimpath it intentionally omits -ldflags: CLI Main.Version and the frozen -X
recipe are separate from the native CLI's observed `version` output. Other
targets are inspected, never executed. No Server binary is executed.
Dependency records are format checked only; frozen dependency bytes and actual
compiler inputs require the separate fresh-cache resolver/recipe qualification.
"""

import argparse
import datetime
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import stat
import subprocess
import sys
import tarfile
import zipfile


MODULE = "github.com/anaregdesign/lantern"
TAG = re.compile(r"v(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\.(?:0|[1-9][0-9]*)\Z")
HEX40 = re.compile(r"[0-9a-f]{40}\Z")
TARGETS = tuple((kind, goos, arch)
                for kind, systems in (("lantern-cli", ("linux", "darwin", "windows")),
                                      ("lantern", ("linux", "darwin")))
                for goos in systems for arch in ("amd64", "arm64"))
MAX_BINARY = 256 * 1024 * 1024
MAX_DOCUMENT = 2 * 1024 * 1024


class Refused(Exception):
    pass


def require(condition, message):
    if not condition:
        raise Refused(message)


def command(args, repo):
    try:
        result = subprocess.run(args, cwd=repo, check=True, capture_output=True,
                                timeout=30)
    except (OSError, subprocess.SubprocessError) as error:
        raise Refused(f"command failed: {args[0]}") from error
    require(not result.stderr, f"unexpected diagnostic from {args[0]}")
    return result.stdout


def git(repo, *args):
    return command(["git", "-c", "core.fsmonitor=false", *args], repo)


def regular(path):
    require(stat.S_ISREG(path.lstat().st_mode), f"regular file required: {path.name}")
    return path


def sha256(path):
    with regular(path).open("rb") as stream:
        return stream_hash(stream, path.stat().st_size)


def stream_hash(stream, size):
    value, count = hashlib.sha256(), 0
    while chunk := stream.read(1024 * 1024):
        count += len(chunk)
        require(count <= size, "member exceeds declared size")
        value.update(chunk)
    require(count == size, "truncated member")
    return value.hexdigest()


def unique_object(pairs):
    result = {}
    for key, value in pairs:
        require(key not in result, "duplicate JSON field")
        result[key] = value
    return result


def reject_constant(_value):
    raise Refused("nonfinite JSON")


def json_read(data):
    try:
        return json.loads(data, object_pairs_hook=unique_object,
                          parse_constant=reject_constant)
    except (UnicodeDecodeError, json.JSONDecodeError) as error:
        raise Refused("invalid JSON") from error


def scalar_list(block, name):
    match = re.search(rf"(?m)^    {name}:\n((?:[ \t]*\n|      .*\n|    #.*\n)*)", block)
    require(match is not None, f"missing recipe {name}")
    result = []
    for line in match[1].splitlines():
        if not line.strip() or line.lstrip().startswith("#"):
            continue
        require(line.startswith("      - "), f"unsupported recipe {name}")
        result.append(line[len("      - "):])
    return result


def recipe(repo):
    """Intentionally accepts only this small, reviewed release build recipe."""
    text = regular(repo / ".goreleaser.yaml").read_text()
    match = re.search(r"(?ms)^builds:\n(.*?)^archives:", text)
    require(match is not None, "missing build recipe")
    blocks = re.split(r"(?m)(?=^  - id: )", match[1])
    require(not blocks[0].strip(), "unexpected build recipe prefix")
    blocks = blocks[1:]
    require(len(blocks) == 2, "expected exactly two builds")
    seen = set()
    for block in blocks:
        kind = block.splitlines()[0].removeprefix("  - id: ")
        require(kind in ("lantern", "lantern-cli") and kind not in seen,
                "unknown or duplicate build")
        seen.add(kind)
        keys = re.findall(r"(?m)^    ([a-z_]+):", block)
        require(len(keys) == len(set(keys)), "duplicate recipe property")
        require(set(keys) <= {"main", "binary", "env", "flags", "ldflags", "goos", "goarch", "hooks"},
                "unsupported build property")
        main = "./cli" if kind == "lantern-cli" else "./server/cmd"
        require(re.search(rf"(?m)^    main: {re.escape(main)}$", block), "incorrect recipe main")
        require(re.search(rf"(?m)^    binary: {kind}$", block), "incorrect recipe binary")
        require(scalar_list(block, "env") == ["CGO_ENABLED=0"], "incorrect recipe environment")
        require(scalar_list(block, "flags") == ["-trimpath"], "incorrect recipe flags")
        expected_flags = ["-s -w"]
        if kind == "lantern-cli":
            expected_flags.append("-X github.com/anaregdesign/lantern/cli/cmd.version={{ .Tag }}")
        require(scalar_list(block, "ldflags") == expected_flags, "incorrect exact CLI/linker recipe")
        require(scalar_list(block, "goarch") == ["amd64", "arm64"], "incorrect architecture matrix")
        systems = ["linux", "darwin", "windows"] if kind == "lantern-cli" else ["linux", "darwin"]
        require(scalar_list(block, "goos") == systems, "incorrect operating-system matrix")
    return sha256(repo / ".goreleaser.yaml")


def clean_index_flags(repo):
    # status, diff and Go VCS stamping all honor these index flags, so none of
    # them independently detects modifications hidden by assume-unchanged or
    # skip-worktree. Read the flags without refreshing or mutating the index.
    records = git(repo, "ls-files", "-v", "-z").split(b"\0")
    require(records[-1] == b"" and len(records) > 1, "invalid tracked-file inventory")
    paths = set()
    for record in records[:-1]:
        require(record.startswith(b"H ") and len(record) > 2,
                "hidden/nonordinary tracked-file index flags")
        require(record[2:] not in paths, "duplicate tracked-file index record")
        paths.add(record[2:])


def go_environment(repo):
    regular(repo / "go.work")
    expected = {"GOENV": "off", "GOTOOLCHAIN": "local", "GOFLAGS": "", "GOWORK": str(repo / "go.work")}
    # Refuse the raw selector inputs before invoking Go: an automatic toolchain
    # must not download itself while this guard is trying to inspect it.
    require(all(os.environ.get(key) == value for key, value in expected.items()),
            "release Go environment inputs differ")
    actual = json_read(command(["go", "env", "-json", *expected], repo))
    require(actual == expected, "effective Go environment differs")
    return actual


def source(repo, head, tag):
    require(HEX40.fullmatch(head) and TAG.fullmatch(tag), "full commit and root release tag required")
    require(git(repo, "rev-parse", "HEAD").decode().strip() == head, "HEAD differs")
    require(git(repo, "rev-parse", "--verify", f"refs/tags/{tag}^{{commit}}").decode().strip() == head,
            "release tag does not identify expected HEAD")
    tree = git(repo, "rev-parse", f"{head}^{{tree}}").decode().strip()
    require(HEX40.fullmatch(tree), "invalid source tree")
    clean_index_flags(repo)
    require(not git(repo, "status", "--porcelain=v1", "--untracked-files=all"), "source is dirty")
    require(not git(repo, "diff", "--binary", "HEAD", "--"), "source differs from HEAD")
    pin = re.findall(r"(?m)^go (\d+\.\d+\.\d+)$", regular(repo / "go.mod").read_text())
    require(len(pin) == 1, "exact root Go pin required")
    timestamp = git(repo, "show", "-s", "--format=%ct", head).decode().strip()
    require(timestamp.isdigit(), "invalid commit timestamp")
    vcs_time = datetime.datetime.fromtimestamp(int(timestamp), datetime.timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    result = {"head": head, "tree": tree, "tag": tag, "go_version": "go" + pin[0],
              "go_environment": go_environment(repo),
              "vcs_time": vcs_time, "recipe_sha256": recipe(repo),
              "verifier_sha256": sha256(Path(__file__).resolve()),
              "documents": {name: sha256(repo / name) for name in ("LICENSE", "README.md")}}
    require(not git(repo, "status", "--porcelain=v1", "--untracked-files=all"), "source changed during check")
    clean_index_flags(repo)
    return result


def target_path(repo, target):
    kind, goos, arch = target
    require(target in TARGETS, "unsupported target")
    variant = "v1" if arch == "amd64" else "v8.0"
    name = kind + (".exe" if goos == "windows" else "")
    return repo / "dist" / f"{kind}_{goos}_{arch}_{variant}" / name


def safe_dist_file(repo, path):
    path = Path(os.path.abspath(path))
    try:
        parts = path.relative_to(repo / "dist").parts
    except ValueError as error:
        raise Refused("artifact is outside dist") from error
    parent = repo
    for part in ("dist", *parts[:-1]):
        parent = parent / part
        require(stat.S_ISDIR(parent.lstat().st_mode), "symlink/non-directory artifact parent")
    return regular(path)


def buildinfo(repo, binary, target, expected):
    tool = command(["go", "version"], repo).decode().split()
    require(len(tool) == 4 and tool[:3] == ["go", "version", expected["go_version"]],
            "inspection Go toolchain differs from root pin")
    info = json_read(command(["go", "version", "-m", "-json", str(binary)], repo))
    require(isinstance(info, dict) and set(info) <= {"GoVersion", "Path", "Main", "Deps", "Settings"},
            "invalid BuildInfo object")
    kind, goos, arch = target
    main = info.get("Main")
    require(isinstance(main, dict) and set(main) <= {"Path", "Version", "Sum"}, "invalid Main module")
    require(all(isinstance(value, str) for value in main.values()), "invalid Main module fields")
    require(main.get("Path") == (MODULE if kind == "lantern-cli" else MODULE + "/server"),
            "wrong Main module")
    require(info.get("Path") == MODULE + ("/cli" if kind == "lantern-cli" else "/server/cmd"),
            "wrong binary package")
    require(info.get("GoVersion") == expected["go_version"], "wrong binary Go version")
    require(isinstance(main.get("Version"), str) and main["Version"] and "+dirty" not in main["Version"],
            "missing/dirty Main.Version")
    if kind == "lantern-cli":
        require(main["Version"] == expected["tag"], "CLI Main.Version differs from actual tag")
    dependencies = info.get("Deps")
    require(isinstance(dependencies, list) and dependencies, "missing dependency records")
    paths = set()
    for module in dependencies:
        require(isinstance(module, dict) and {"Path", "Version"} <= set(module)
                and set(module) <= {"Path", "Version", "Sum"}
                and all(isinstance(value, str) and value for value in module.values()),
                "invalid/replaced dependency record")
        require(module["Path"] not in paths, "duplicate dependency record")
        paths.add(module["Path"])
    rows = info.get("Settings")
    require(isinstance(rows, list), "missing build settings")
    settings = {}
    for row in rows:
        require(isinstance(row, dict) and set(row) == {"Key", "Value"}, "invalid build setting")
        key, value = row["Key"], row["Value"]
        require(isinstance(key, str) and isinstance(value, str) and key not in settings,
                "duplicate/invalid build setting")
        settings[key] = value
    required = {"-buildmode": "exe", "-compiler": "gc", "-trimpath": "true", "CGO_ENABLED": "0",
                "GOOS": goos, "GOARCH": arch, "vcs": "git", "vcs.revision": expected["head"],
                "vcs.time": expected["vcs_time"], "vcs.modified": "false",
                ("GOAMD64" if arch == "amd64" else "GOARM64"): ("v1" if arch == "amd64" else "v8.0")}
    require(settings == required, "build settings differ from clean pinned release recipe")
    return info


def native_target():
    arch = {"x86_64": "amd64", "AMD64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(platform.machine())
    goos = {"Linux": "linux", "Darwin": "darwin", "Windows": "windows"}.get(platform.system())
    return goos, arch


def native_cli(repo, binary, target, expected):
    if target[0] != "lantern-cli" or target[1:] != native_target():
        return {"status": "not_executed", "reason": "server_or_non_native_target"}
    result = command([str(binary), "version"], repo)
    require(result == (expected["tag"] + "\n").encode(), "native CLI version stdout differs")
    return {"status": "verified", "stdout": result.decode(), "command": [binary.name, "version"]}


def receipt_path(repo, target):
    return repo / "dist" / "release-integrity" / ("_".join(target) + ".json")


def write_receipt(repo, path, value):
    dist = repo / "dist"
    require(stat.S_ISDIR(dist.lstat().st_mode), "dist is not a real directory")
    directory = dist / "release-integrity"
    directory.mkdir(mode=0o700, exist_ok=True)
    require(stat.S_ISDIR(directory.lstat().st_mode), "receipt directory is not a real directory")
    require(path.parent == directory, "invalid receipt destination")
    data = (json.dumps(value, sort_keys=True, indent=2, allow_nan=False) + "\n").encode()
    try:
        fd = os.open(path, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    except FileExistsError as error:
        raise Refused("duplicate target/final receipt") from error
    with os.fdopen(fd, "wb") as output:
        output.write(data)
        output.flush()
        os.fsync(output.fileno())


def verify_binary(repo, target, binary, head, tag):
    require(target in TARGETS, "unsupported target")
    expected = source(repo, head, tag)
    require(not receipt_path(repo, target).exists() and not receipt_path(repo, target).is_symlink(),
            "duplicate target/final receipt")
    path = safe_dist_file(repo, binary)
    require(path == target_path(repo, target), "unexpected raw binary path")
    require(0 < path.stat().st_size <= MAX_BINARY, "invalid raw binary size")
    digest = sha256(path)
    info = buildinfo(repo, path, target, expected)
    native = native_cli(repo, path, target, expected)
    require(sha256(path) == digest, "binary changed during inspection")
    require(source(repo, head, tag) == expected, "source changed during binary inspection")
    value = {"schema": 1, "kind": target[0], "goos": target[1], "goarch": target[2],
             "binary": str(path.relative_to(repo)), "bytes": path.stat().st_size, "sha256": digest,
             "source": expected, "buildinfo": info, "native_cli": native,
             "cli_version_scope": "Main.Version and exact -X recipe; direct stdout only for native CLI"}
    write_receipt(repo, receipt_path(repo, target), value)
    return value


def archive_name(target, tag):
    kind, goos, arch = target
    extension = "zip" if goos == "windows" else "tar.gz"
    return f"{kind}_{tag[1:]}_{goos.title()}_{'x86_64' if arch == 'amd64' else arch}.{extension}"


def verify_archive(path, names):
    """Hash members without extracting any path or trusting archive metadata."""
    require(0 < path.stat().st_size <= MAX_BINARY * 2, "invalid archive size")
    found = {}

    def member(name, size, stream):
        require(name in names and name not in found, "unsafe/extra/duplicate archive member")
        require(0 < size <= names[name][1], "invalid archive member size")
        digest = stream_hash(stream, size)
        require(digest == names[name][0], "archive member differs from qualified source/binary")
        found[name] = digest

    if path.suffix == ".zip":
        with zipfile.ZipFile(path) as archive:
            for item in archive.infolist():
                mode = stat.S_IFMT(item.external_attr >> 16)
                require(not item.is_dir() and mode in (0, stat.S_IFREG) and not item.flag_bits & 1,
                        "nonregular/encrypted ZIP member")
                with archive.open(item) as stream:
                    member(item.filename, item.file_size, stream)
    else:
        with tarfile.open(path, "r:gz") as archive:
            for item in archive:
                require(item.isfile() and not item.issparse(), "nonregular/sparse TAR member")
                stream = archive.extractfile(item)
                require(stream is not None, "missing TAR member")
                with stream:
                    member(item.name, item.size, stream)
    require(set(found) == set(names), "missing archive member")
    return found


def verify_complete(repo, head, tag):
    expected = source(repo, head, tag)
    directory = repo / "dist" / "release-integrity"
    require(stat.S_ISDIR(directory.lstat().st_mode), "receipt directory is not a real directory")
    wanted_receipts = {receipt_path(repo, target).name for target in TARGETS}
    require({path.name for path in directory.iterdir()} == wanted_receipts, "missing/extra target receipt")
    wanted_archives = {archive_name(target, tag) for target in TARGETS}
    actual_archives = {path.name for path in (repo / "dist").iterdir()
                       if path.name.endswith((".tar.gz", ".zip"))}
    require(actual_archives == wanted_archives, "missing/extra release archive")
    checksum_path = safe_dist_file(repo, repo / "dist" / "checksums.txt")
    checksum_digest = sha256(checksum_path)
    checksums = {}
    for line in checksum_path.read_text().splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})  ([A-Za-z0-9_.-]+)", line)
        require(match is not None and match[2] not in checksums, "invalid/duplicate checksum entry")
        checksums[match[2]] = match[1]
    require(set(checksums) == wanted_archives, "checksum inventory differs")
    verified, native_count = [], 0
    fields = {"schema", "kind", "goos", "goarch", "binary", "bytes", "sha256", "source",
              "buildinfo", "native_cli", "cli_version_scope"}
    for target in TARGETS:
        receipt_file = safe_dist_file(repo, receipt_path(repo, target))
        receipt = json_read(receipt_file.read_bytes())
        require(isinstance(receipt, dict) and set(receipt) == fields
                and type(receipt["schema"]) is int and receipt["schema"] == 1,
                "invalid target receipt")
        require((receipt["kind"], receipt["goos"], receipt["goarch"]) == target and receipt["source"] == expected,
                "receipt target/source differs")
        raw = safe_dist_file(repo, target_path(repo, target))
        require(receipt["binary"] == str(raw.relative_to(repo)) and receipt["bytes"] == raw.stat().st_size
                and receipt["sha256"] == sha256(raw), "raw binary differs from post-hook receipt")
        require(receipt["buildinfo"] == buildinfo(repo, raw, target, expected), "raw BuildInfo changed")
        require(sha256(raw) == receipt["sha256"], "raw binary changed during inspection")
        native = receipt["native_cli"]
        if target[0] == "lantern-cli" and target[1:] == native_target():
            require(native == {"status": "verified", "stdout": tag + "\n", "command": [raw.name, "version"]},
                    "missing native CLI observation")
            native_count += 1
        else:
            require(native == {"status": "not_executed", "reason": "server_or_non_native_target"},
                    "unexpected native observation")
        require(receipt["cli_version_scope"] == "Main.Version and exact -X recipe; direct stdout only for native CLI",
                "incorrect CLI proof scope")
        filename = archive_name(target, tag)
        archive = safe_dist_file(repo, repo / "dist" / filename)
        digest = sha256(archive)
        require(digest == checksums[filename], "archive checksum differs")
        members = {raw.name: (receipt["sha256"], MAX_BINARY),
                   **{name: (digest, MAX_DOCUMENT) for name, digest in expected["documents"].items()}}
        verify_archive(archive, members)
        require(sha256(archive) == digest, "archive changed during inspection")
        verified.append({"target": list(target), "archive": filename, "sha256": digest,
                         "binary_sha256": receipt["sha256"], "post_receipt_sha256": sha256(receipt_file)})
    require(native_count == 1, "release verification requires one supported native CLI observation")
    require({path.name for path in directory.iterdir()} == wanted_receipts, "receipt inventory changed")
    require(checksums == {row["archive"]: sha256(repo / "dist" / row["archive"]) for row in verified},
            "archives changed before final receipt")
    require({path.name for path in (repo / "dist").iterdir() if path.name.endswith((".tar.gz", ".zip"))}
            == wanted_archives, "archive inventory changed")
    require(sha256(checksum_path) == checksum_digest, "checksum manifest changed")
    require(source(repo, head, tag) == expected, "source changed during complete inspection")
    result = {"schema": 1, "verdict": "pass", "source": expected, "targets": verified,
              "checksums_sha256": checksum_digest, "binary_signatures": "not provided",
              "dependency_scope": "BuildInfo format only; frozen dependency-byte qualification is separate",
              "scope": "clean raw metadata, post-hook/native observations, source and archive correspondence"}
    write_receipt(repo, directory / "complete.json", result)
    return result


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    subcommands = parser.add_subparsers(dest="mode", required=True)
    for mode in ("source", "binary", "complete"):
        subparser = subcommands.add_parser(mode)
        subparser.add_argument("--repo", type=Path, default=Path(__file__).resolve().parents[2])
        subparser.add_argument("--expected-head", required=True)
        subparser.add_argument("--expected-tag", required=True)
        if mode == "binary":
            subparser.add_argument("--kind", choices=("lantern", "lantern-cli"), required=True)
            subparser.add_argument("--binary", type=Path, required=True)
            subparser.add_argument("--goos", required=True)
            subparser.add_argument("--goarch", required=True)
    args = parser.parse_args(argv)
    repo = args.repo.resolve()
    if args.mode == "source":
        result = {"schema": 1, "verdict": "pass", "source": source(repo, args.expected_head, args.expected_tag)}
    elif args.mode == "binary":
        binary = args.binary if args.binary.is_absolute() else repo / args.binary
        result = verify_binary(repo, (args.kind, args.goos, args.goarch), binary, args.expected_head, args.expected_tag)
    else:
        result = verify_complete(repo, args.expected_head, args.expected_tag)
    print(json.dumps(result, sort_keys=True, allow_nan=False))
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except (Refused, OSError, UnicodeError, ValueError, tarfile.TarError, zipfile.BadZipFile) as error:
        print(f"root release integrity refused: {error}", file=sys.stderr)
        sys.exit(1)
