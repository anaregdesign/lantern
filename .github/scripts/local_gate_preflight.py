"""Bounded, read-only local gate checks; never provision or select tools."""

import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import tempfile
import time


def exact_pin(root, file, pattern):
    values = re.findall(pattern, (root / file).read_text(), re.MULTILINE)
    if not values or len(set(values)) != 1:
        raise ValueError(f"missing or ambiguous tool pin in {file}")
    return values[0]


def command(argv, root, environment):
    # No toolchain auto acquisition, including rustup proxies. These overrides
    # apply only to inspection, never to the user's environment or gate steps.
    env = dict(environment, GOTOOLCHAIN="local", RUSTUP_AUTO_INSTALL="0")
    try:
        result = subprocess.run(argv, cwd=root, env=env, text=True,
                                capture_output=True, timeout=10, check=True)
    except (OSError, subprocess.SubprocessError) as error:
        raise ValueError(f"preflight inspection failed: {argv[0]} {argv[1:]}") from error
    return result.stdout.strip()


def executable(name, environment):
    path = shutil.which(name, path=environment.get("PATH", os.defpath))
    if not path:
        raise ValueError(f"preflight: {name} not found; select an existing repository-pinned tool on task-local PATH")
    return str(Path(path).absolute())


def require_version(name, path, actual, expected):
    if actual != expected:
        raise ValueError(f"preflight: {name} path={path} expected={expected} actual={actual}; "
                         "select an existing pinned executable on task-local PATH (including child GOTOOLCHAIN=local); "
                         "no tool was installed or host setting changed")


def scratch_environment(environment):
    # No compile/exec probe: statvfs exposes mount noexec on Linux. This checks
    # the effective compiler scratch, including an explicit GOTMPDIR override.
    path = Path(environment.get("GOTMPDIR") or environment.get("TMPDIR") or tempfile.gettempdir()).resolve()
    if not path.is_dir() or not os.access(path, os.W_OK | os.X_OK):
        raise ValueError(f"preflight: compiler scratch is unavailable: {path}")
    mount = os.statvfs(path)
    if mount.f_flag & getattr(os, "ST_NOEXEC", 0):
        raise ValueError(f"preflight: compiler scratch mount is noexec: {path}; select executable scratch "
                         "inside the owned execution environment; do not change host security settings")
    if mount.f_bavail == 0:
        raise ValueError(f"preflight: compiler scratch has no available blocks: {path}")
    return {"compiler_scratch": str(path), "mount_noexec_checked": hasattr(os, "ST_NOEXEC")}


def check(root, environment, plan):
    started = time.monotonic()
    result = {"tools": [], "environment": scratch_environment(environment)}
    names = {step.name for step in plan}
    # Go is also launched by harness contracts and SDK/browser children.
    needs_go = any(name.startswith(("go-", "server-", "harness-", "rust-", "admin-playwright"))
                   or name.endswith("-generate") or "wire" in name for name in names)
    if needs_go:
        pin = exact_pin(root, "go.mod", r"^go (\d+\.\d+\.\d+)$")
        path = executable("go", environment)
        version = command([path, "version"], root, environment)
        match = re.fullmatch(r"go version go(\S+) \S+/\S+", version)
        require_version("go (local child)", path, match[1] if match else version, pin)
        settings = json.loads(command([path, "env", "-json", "GOVERSION", "GOROOT", "GOTOOLCHAIN", "GOENV", "GOTMPDIR"], root, environment))
        require_version("effective Go", path, settings.get("GOVERSION"), "go" + pin)
        if settings.get("GOTMPDIR"):
            result["environment"] = scratch_environment(dict(environment, GOTMPDIR=settings["GOTMPDIR"]))
        work_pin = exact_pin(root, "go.work", r"^go (\d+\.\d+\.\d+)$")
        require_version("workspace Go pin", str(root / "go.work"), work_pin, pin)
        # Reject an automatic or explicitly different parent selection rather
        # than probing it and accidentally downloading a toolchain.
        selection = environment.get("GOTOOLCHAIN")
        if not selection:
            selection = "auto"
            config = settings.get("GOENV")
            if config and config != "off" and Path(config).is_file():
                values = re.findall(r"^GOTOOLCHAIN=(.*)$", Path(config).read_text(), re.MULTILINE)
                if values:
                    selection = values[-1]
        for manifest in (root / "go.mod", root / "go.work"):
            directives = re.findall(r"^toolchain (\S+)$", manifest.read_text(), re.MULTILINE)
            if any(value not in {"default", "go" + pin} for value in directives):
                raise ValueError("preflight: manifest toolchain differs from source pin")
        if selection not in {"local", "auto", "path", "go" + pin, "go" + pin + "+auto", "go" + pin + "+path"}:
            raise ValueError("preflight: parent GOTOOLCHAIN differs from source pin; use task-local GOTOOLCHAIN=local")
        result["tools"].append({"name": "go", "path": path, "resolved_path": str(Path(path).resolve()),
                                "version": pin, "goroot": settings["GOROOT"]})
    if "go-lint" in names:
        pin = exact_pin(root, ".github/workflows/go.yml", r"^\s+version: v(2\.\d+\.\d+)\s*$")
        path = executable("golangci-lint", environment)
        output = command([path, "version"], root, environment)
        match = re.search(r"\bversion (\d+\.\d+\.\d+)\b", output)
        require_version("golangci-lint", path, match[1] if match else output, pin)
        result["tools"].append({"name": "golangci-lint", "path": path, "version": pin})
    if any(step.command[0] == "bun" for step in plan):
        pin = exact_pin(root, ".github/workflows/node-sdk.yml", r"^\s+bun-version: (\d+\.\d+\.\d+)\s*$")
        path = executable("bun", environment)
        actual = command([path, "--version"], root, environment)
        require_version("bun", path, actual, pin)
        result["tools"].append({"name": "bun", "path": path, "version": actual})
    if "node-generate" in names:
        # Node requires PATH buf and pins its version in its workflow. Root's
        # go-run commands and Dart's fallback carry their own explicit versions;
        # do not execute/download either fallback merely to inspect it.
        pin = exact_pin(root, "Makefile", r"^BUF_VERSION := v(\d+\.\d+\.\d+)$")
        path = executable("buf", environment)
        actual = command([path, "--version"], root, environment)
        require_version("buf (Node generation)", path, actual, pin)
        result["tools"].append({"name": "buf", "path": path, "version": pin})
    if any(step.command[0] == "cargo" for step in plan):
        # Native SDK4 owns the exact current-toolchain pin. Ordinary Rust gates
        # otherwise retain their existing MSRV/stable policy, not a new pin.
        for tool in ("cargo", "rustc", "cargo-clippy", "rustfmt"):
            path = executable(tool, environment)
            version = command([path, "--version"], root / "sdks/rust", environment)
            if "current-functional-wire" in names and tool in {"cargo", "rustc"}:
                pin = exact_pin(root, ".github/workflows/current-sdk4.yml", r"^\s+toolchain: '(\d+\.\d+\.\d+)'\s*$")
                match = re.match(re.escape(tool) + r" (\d+\.\d+\.\d+)\b", version)
                require_version(tool, path, match[1] if match else version, pin)
            result["tools"].append({"name": tool, "path": path, "version": version})
        if "rust-audit" in names:
            pin = exact_pin(root, ".github/workflows/rust-release.yml", r"^\s+cargo install --locked --version (\d+\.\d+\.\d+) cargo-audit\s*$")
            path = executable("cargo-audit", environment)
            actual = command([path, "--version"], root / "sdks/rust", environment)
            require_version("cargo-audit", path, actual.removeprefix("cargo-audit "), pin)
            result["tools"].append({"name": "cargo-audit", "path": path, "version": pin})
    if any(step.command[0] in {"dart", "flutter"} for step in plan):
        pin = exact_pin(root, ".github/workflows/current-sdk4.yml", r"^\s+sdk: (\d+\.\d+\.\d+)\s*$")
        selected = Path(executable("dart", environment)).resolve()
        # Flutter's shell launcher can bootstrap/download even for --version.
        # Inspect its already installed SDK executable directly instead.
        cached = selected.parent / "cache/dart-sdk/bin/dart"
        dart = cached if cached.is_file() else selected
        with dart.open("rb") as stream:
            magic = stream.read(4)
        if magic not in {b"\xcf\xfa\xed\xfe", b"\xfe\xed\xfa\xcf", b"\x7fELF", b"\xca\xfe\xba\xbe"}:
            raise ValueError("preflight: Dart launcher is not an installed native SDK; select its existing binary on task-local PATH")
        version = command([str(dart), "--version"], root, environment)
        match = re.search(r"Dart SDK version: (\d+\.\d+\.\d+)", version)
        require_version("dart", str(selected), match[1] if match else version, pin)
        result["tools"].append({"name": "dart", "path": str(selected), "effective_path": str(dart), "version": pin})
        if any(step.command[0] == "flutter" for step in plan):
            flutter = Path(executable("flutter", environment)).resolve()
            metadata = flutter.parent / "cache/flutter.version.json"
            pin = exact_pin(root, ".github/workflows/dart-sdk.yml", r"^\s+flutter-version: (\d+\.\d+\.\d+)\s*$")
            if not metadata.is_file():
                raise ValueError("preflight: installed Flutter version metadata missing; no bootstrap attempted")
            version = json.loads(metadata.read_text())
            require_version("Flutter cached framework", str(flutter), version.get("frameworkVersion"), pin)
            revision = command([executable("git", environment), "rev-parse", "HEAD"], flutter.parent.parent, environment)
            require_version("Flutter framework revision", str(flutter), revision, version.get("frameworkRevision"))
            result["tools"].append({"name": "flutter", "path": str(flutter), "version": pin,
                                    "framework_revision": revision, "inspection": "installed metadata and source HEAD"})
    # Check direct tool availability before any dependency or harness work.
    # Do not invoke Flutter startup or rustup installation during inspection.
    for name in sorted({step.command[0] for step in plan} - {"go", "bun"}):
        executable(name, environment)
    result["seconds"] = round(time.monotonic() - started, 6)
    return result
