"""Recognize dependency and workflow changes from checked-in source on both sides."""

import json
from pathlib import PurePosixPath
import re
import subprocess
import tomllib


VERSION = rb"v[0-9]+\.[0-9]+\.[0-9]+(?:[+~-][.\w+-]+)?"
REQUIRE = re.compile(rb"(\t[\w./~+-]+ )(" + VERSION + rb")( // indirect)?")
SUM = re.compile(rb"([\w./~+-]+) (" + VERSION + rb")(/go.mod)? h1:[A-Za-z0-9+/]{43}=")
SETUP = {".github/workflows/dart-sdk.yml", ".github/workflows/current-sdk4.yml"}
HOST_JOBS = {".github/workflows/dart-sdk.yml": "changes", ".github/workflows/commitlint.yml": "pr-title"}
PIN = re.compile(rb"^( +(?:- )?uses: dart-lang/setup-dart@)[0-9a-f]{40}(?: # v[0-9]+(?:\.[0-9]+){2})?$", re.M)
DART = ("sdks/dart", "sdks/dart/offline", "sdks/dart/offline_sqlite", "sdks/dart/example")
JS = ("admin", "sdks/node")
LOCKS = {*(f"{p}/pubspec.lock" for p in DART), *(f"{p}/bun.lock" for p in JS),
         "sdks/dart/offline/tool/paired_source.pubspec.lock", "sdks/rust/Cargo.lock"}


def requirements(raw):
    """A normalized go.mod permits version replacements only inside require blocks."""
    result, normalized, block = {}, [], False
    for line in raw.splitlines(keepends=True):
        raw = line.removesuffix(b"\n")
        single = raw.startswith(b"require ") and raw != b"require ("
        entry = b"\t" + raw.removeprefix(b"require ") if single else raw
        match = REQUIRE.fullmatch(entry) if block or single else None
        if not match and (single or block and raw.strip() not in (b"", b")") and not raw.lstrip().startswith(b"//")):
            raise ValueError("unsupported Go requirement")
        if match:
            name = match[1].strip().decode()
            if name in result:
                raise ValueError("duplicate Go requirement")
            result[name] = match[2].decode()
            line = line.replace(match[1].strip() + b" " + match[2], match[1].strip() + b" <version>", 1)
        normalized.append(line)
        if line.strip() == b"require (":
            block = True
        elif line.strip() == b")":
            block = False
    return result, normalized


def sum_delta(old, new):
    def entries(raw):
        lines = raw.splitlines()
        values = {}
        for line in lines:
            match = SUM.fullmatch(line)
            if not match or match.groups() in values:
                raise ValueError("ambiguous Go checksum")
            values[match.groups()] = line
        return values
    a, b = entries(old), entries(new)
    if any(a[key] != b[key] for key in a.keys() & b.keys()):
        raise ValueError("checksum changed for an existing Go module version")
    return {(name.decode(), version.decode()) for name, version, _ in a.keys() ^ b.keys()}


def go_dependencies(root, head, changes, blob, modules):
    manifests = {str(PurePosixPath(module) / "go.mod"): module for module in modules}
    locks = {str(PurePosixPath(module) / "go.sum") for module in modules} | {"go.work.sum"}
    changed = changes.keys() & (manifests.keys() | locks)
    if not changed or not (changed & manifests.keys()) or any(changes[p] != "M" for p in changed):
        return {}, set()
    owners, updated = set(), set()
    for path in changed & manifests.keys():
        module = manifests[path]
        old, shape = requirements(blob(0, path))
        new, new_shape = requirements(blob(1, path))
        if shape != new_shape:
            return {}, set()
        delta = {name for name in new if new[name] != old[name]}
        if not delta or any(name.startswith("github.com/anaregdesign/lantern/") for name in delta):
            return {}, set()
        owners.add(module)
        updated.update((name, v) for name in delta for v in (old[name], new[name]))
    identities = {name for name, _ in updated}
    owners.update(str(PurePosixPath(path).parent) for path in changed & locks if path != "go.work.sum")
    for path in changed & locks:
        delta = sum_delta(blob(0, path), blob(1, path))
        if delta <= updated:
            continue
        # An isolated CLI bump must identify every sum. For a module update,
        # tidy may also change transitive sums outside the explicit require list.
        # The owning module validates the exact graph with tidy -diff in CI.
        if owners == {"."}:
            return {}, set()
        identities.update(name for name, _ in delta)
        if path == "go.work.sum":
            # Workspace-only transitive records are shared by all six modules.
            owners.update(modules)
    names = identities
    # Go workspace minimum-version selection can also raise an unchanged
    # module's direct/indirect requirements. Include all such consumers.
    for path, module in manifests.items():
        if names.intersection(requirements(blob(1, path))[0]):
            owners.add(module)
    if owners == {"."}:
        # Distinguish CLI dependencies from the root's code generation,
        # SDK wire fixtures and release tools. Unproven root-only uses are broad.
        for name in names:
            pattern = '"'+re.escape(name)+ '(/[^"]*)?"'
            matches = subprocess.run(["git", "grep", "-l", "-E", pattern, head, "--", "*.go"],
                                     cwd=root, capture_output=True, text=True)
            files = [line.partition(":")[2] for line in matches.stdout.splitlines()]
            if matches.returncode != 0 or not files or any(not p.startswith("cli/") for p in files):
                return {}, set()
        kind = "cli-dependency"
    else:
        kind = "go-dependency"
    return dict.fromkeys(changed, kind), owners


def dependency_sections(path, raw):
    """Strip just dependency values, preserving runtime/toolchain/policy fields."""
    if path.endswith("package.json"):
        obj = json.loads(raw)
        if not isinstance(obj, dict):
            raise ValueError("invalid npm manifest")
        for section in ("dependencies", "devDependencies", "peerDependencies", "optionalDependencies"):
            for name, value in obj.get(section, {}).items():
                if isinstance(value, str) and re.fullmatch(r"[0-9~^=*<>|. xX+-]+", value):
                    obj[section][name] = "<version>"
        return obj
    if path.endswith("Cargo.toml"):
        obj = tomllib.loads(raw.decode())
        for section in ("dependencies", "dev-dependencies", "build-dependencies"):
            for name, value in obj.get(section, {}).items():
                if isinstance(value, str):
                    obj[section][name] = "<version>"
                elif "version" in value:
                    value["version"] = "<version>"
        return obj
    # pubspec allows tagged/path/git SDKs. Only single-line registry version
    # constraints in top-level dependency tables can take this narrow route.
    normalized, section = [], False
    for line in raw.splitlines(keepends=True):
        if line and line[0:1].isalnum():
            section = line.strip() in (b"dependencies:", b"dev_dependencies:")
        if section:
            line = re.sub(rb"^(  \w+: )[\x22\x27]?[=<>~^\d][\d\s.<>=^~]*[\x22\x27]?\n$",
                          rb"\1<version>\n", line)
        normalized.append(line)
    return normalized


def outside_host_steps(raw, job):
    """Use the YAML node position to compare everything outside one Linux job's steps byte-for-byte."""
    query = (f'.jobs["{job}"] as $job | $job.steps as $s | '
             '[$job."runs-on", ($s | kind), ($s | line), ($s | column), '
             '([... | select(kind == "alias" or anchor != "")] | length), '
             '([... | select(kind == "map") | keys | select(length != (unique | length))] | length)]')
    try:
        data = subprocess.check_output(["yq", "-o=json", query, "-"], input=raw, stderr=subprocess.DEVNULL)
        runner, kind, line, column, anchors, duplicates = json.loads(data)
    except (OSError, ValueError, subprocess.CalledProcessError):
        return None
    if (runner, kind, column, anchors, duplicates) != ("ubuntu-latest", "seq", 7, 0, 0):
        return None
    lines = raw.split(b"\n")
    start = line - 1
    if not (0 < start < len(lines) and lines[start].startswith(b"      - ")):
        return None
    # A block sequence ends at the next nonblank dedented line. Keep the
    # steps key, all job metadata and every other job in the comparison.
    end = next((i for i in range(start + 1, len(lines))
                if lines[i].strip() and not lines[i].startswith(b"      ")), len(lines))
    return lines[:start], lines[end:]


def classify(root, base, head, changes, modules):
    def blob(side, path):
        return subprocess.check_output(["git", "show", f"{(base,head)[side]}:{path}"], cwd=root)
    result, owners = go_dependencies(root, head, changes, blob, modules)
    for path in (SETUP | HOST_JOBS.keys()).intersection(changes):
        if changes[path] == "M":
            old, new = blob(0, path), blob(1, path)
            if path in SETUP and old != new and PIN.search(old) and PIN.sub(rb"\1<pin>", old) == PIN.sub(rb"\1<pin>", new):
                result[path] = "setup-dart"
            elif path in HOST_JOBS:
                before = outside_host_steps(old, HOST_JOBS[path])
                if before is not None and before == outside_host_steps(new, HOST_JOBS[path]):
                    result[path] = "ci-routing" if HOST_JOBS[path] == "changes" else "workflow-steps"
    manifests = {*(f"{p}/pubspec.yaml" for p in DART), *(f"{p}/package.json" for p in JS),
                 "sdks/rust/Cargo.toml"}
    for path in (LOCKS | manifests).intersection(changes):
        if changes[path] != "M":
            continue
        if path in LOCKS or dependency_sections(path, blob(0, path)) == dependency_sections(path, blob(1, path)):
            result[path] = "package-dependency"
    return result, owners
