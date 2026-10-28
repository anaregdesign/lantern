"""Exercise source refusals and actual archive operations without building Go."""

import copy
import importlib.util
import io
import json
import os
from pathlib import Path
import stat
import subprocess
import tarfile
import tempfile
import unittest
from unittest.mock import patch
import warnings
import zipfile


SPEC = importlib.util.spec_from_file_location(
    "root_release_gate", Path(__file__).with_name("verify_root_release_binaries.py"))
gate = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(gate)


RECIPE = """version: 2
project_name: lantern
before:
  hooks:
    - go mod download
builds:
  - id: lantern-cli
    main: ./cli
    binary: lantern-cli
    env:
      - CGO_ENABLED=0
    flags:
      - -trimpath
    ldflags:
      - -s -w
      # The -X flag is deliberately bound to the tag.
      - -X github.com/anaregdesign/lantern/cli/cmd.version={{ .Tag }}
    goos:
      - linux
      - darwin
      - windows
    goarch:
      - amd64
      - arm64
    hooks:
      post:
        - cmd: python3 verifier binary
  - id: lantern
    main: ./server/cmd
    binary: lantern
    env:
      - CGO_ENABLED=0
    flags:
      - -trimpath
    ldflags:
      - -s -w
    goos:
      - linux
      - darwin
    goarch:
      - amd64
      - arm64
archives:
  - id: lantern
"""


class ReleaseFixture(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.addCleanup(self.temporary.cleanup)
        self.repo = Path(self.temporary.name).resolve()
        self.tag = "v0.36.1"
        for name, data in {"go.mod": "module github.com/anaregdesign/lantern\n\ngo 9.8.7\n",
                           "go.work": "go 9.8.7\n\nuse .\n",
                           ".goreleaser.yaml": RECIPE, ".gitignore": "dist/\n",
                           "LICENSE": "fixture license\n", "README.md": "fixture readme\n",
                           "source.go": "package fixture\n"}.items():
            (self.repo / name).write_text(data)
        self.git("init", "-q")
        self.git("config", "user.name", "Release fixture")
        self.git("config", "user.email", "release-fixture@example.invalid")
        self.commit()
        self.git("tag", self.tag)
        self.real_command = gate.command
        self.infos = {}
        self.calls = []
        self.go_environment = {"GOENV": "off", "GOTOOLCHAIN": "local", "GOFLAGS": "",
                               "GOWORK": str(self.repo / "go.work")}
        self.environment = patch.dict(os.environ, self.go_environment)
        self.environment.start()
        self.addCleanup(self.environment.stop)
        self.go = patch.object(gate, "command", side_effect=self.command)
        self.go.start()
        self.addCleanup(self.go.stop)
        self.native = patch.object(gate, "native_target", return_value=("linux", "amd64"))
        self.native.start()
        self.addCleanup(self.native.stop)

    def git(self, *args):
        return subprocess.run(["git", "-c", "core.fsmonitor=false", "-c", "core.hooksPath=/dev/null", *args],
                              cwd=self.repo, check=True, capture_output=True).stdout

    def commit(self):
        self.git("add", ".")
        self.git("commit", "-qm", "release fixture")
        self.head = self.git("rev-parse", "HEAD").decode().strip()

    def command(self, args, repo):
        self.calls.append(args)
        if args == ["go", "env", "-json", "GOENV", "GOTOOLCHAIN", "GOFLAGS", "GOWORK"]:
            return json.dumps(self.go_environment).encode()
        if args == ["go", "version"]:
            return b"go version go9.8.7 linux/amd64\n"
        if args[:4] == ["go", "version", "-m", "-json"]:
            return json.dumps(self.infos[args[4]]).encode()
        return self.real_command(args, repo)

    def source(self):
        return gate.source(self.repo, self.head, self.tag)

    def binary(self, target=("lantern-cli", "linux", "amd64")):
        path = gate.target_path(self.repo, target)
        path.parent.mkdir(parents=True, exist_ok=True)
        if target == ("lantern-cli", "linux", "amd64"):
            path.write_text(f"#!/bin/sh\n[ \"$1\" = version ] || exit 7\nprintf '%s\\n' '{self.tag}'\n")
        else:
            path.write_bytes(("fixture " + " ".join(target)).encode())
        path.chmod(0o755)
        source = self.source()
        kind, goos, arch = target
        settings = {"-buildmode": "exe", "-compiler": "gc", "-trimpath": "true", "CGO_ENABLED": "0",
                    "GOOS": goos, "GOARCH": arch, "vcs": "git", "vcs.revision": self.head,
                    "vcs.time": source["vcs_time"], "vcs.modified": "false",
                    ("GOAMD64" if arch == "amd64" else "GOARM64"): ("v1" if arch == "amd64" else "v8.0")}
        self.infos[str(path)] = {
            "GoVersion": "go9.8.7", "Path": gate.MODULE + ("/cli" if kind == "lantern-cli" else "/server/cmd"),
            "Main": {"Path": gate.MODULE + ("" if kind == "lantern-cli" else "/server"),
                     "Version": self.tag if kind == "lantern-cli" else "v0.0.0-20261028000000-0123456789ab"},
            "Settings": [{"Key": key, "Value": value} for key, value in settings.items()],
            "Deps": [{"Path": "example.invalid/dependency", "Version": "v1.0.0", "Sum": "h1:fixture"}],
        }
        return path

    def qualify(self, target):
        path = self.binary(target)
        return gate.verify_binary(self.repo, target, path, self.head, self.tag)

    def archive(self, target, members=None):
        raw = gate.target_path(self.repo, target)
        if members is None:
            members = [(raw.name, raw.read_bytes()), ("LICENSE", (self.repo / "LICENSE").read_bytes()),
                       ("README.md", (self.repo / "README.md").read_bytes())]
        path = self.repo / "dist" / gate.archive_name(target, self.tag)
        if target[1] == "windows":
            with warnings.catch_warnings():
                warnings.filterwarnings("ignore", message="Duplicate name:", category=UserWarning)
                with zipfile.ZipFile(path, "w") as output:
                    for name, data in members:
                        output.writestr(name, data)
        else:
            with tarfile.open(path, "w:gz") as output:
                for name, data in members:
                    item = tarfile.TarInfo(name)
                    item.size = len(data)
                    output.addfile(item, io.BytesIO(data))
        return path

    def checksums(self):
        lines = [f"{gate.sha256(path)}  {path.name}\n" for path in sorted((self.repo / "dist").iterdir())
                 if path.name.endswith((".tar.gz", ".zip"))]
        (self.repo / "dist" / "checksums.txt").write_text("".join(lines))

    def cohort(self):
        for target in gate.TARGETS:
            self.qualify(target)
            self.archive(target)
        self.checksums()


class SourceTests(ReleaseFixture):
    def test_clean_source_uses_manifest_pin_and_does_not_create_dist(self):
        self.assertEqual(self.source()["go_version"], "go9.8.7")
        self.assertEqual(self.source()["go_environment"], self.go_environment)
        self.assertFalse((self.repo / "dist").exists())

    def test_hidden_index_changes_refuse_even_when_status_and_diff_are_clean(self):
        target = ("lantern", "linux", "amd64")
        binary = self.binary(target)
        path = self.repo / "source.go"
        original = path.read_bytes()
        for flag in ("assume-unchanged", "skip-worktree"):
            with self.subTest(flag=flag):
                self.git("update-index", "--" + flag, "source.go")
                path.write_text("package hidden\n")
                self.assertFalse(self.git("status", "--porcelain=v1", "--untracked-files=all"))
                self.assertFalse(self.git("diff", "--binary", "HEAD", "--"))
                with self.assertRaisesRegex(gate.Refused, "index flags"):
                    self.source()
                with self.assertRaisesRegex(gate.Refused, "index flags"):
                    gate.verify_binary(self.repo, target, binary, self.head, self.tag)
                self.assertFalse(gate.receipt_path(self.repo, target).exists())
                path.write_bytes(original)
                self.git("update-index", "--no-" + flag, "source.go")

    def test_effective_go_environment_refuses_external_source_selectors(self):
        changes = (("GOFLAGS", "-overlay=ignored-overlay.json"), ("GOFLAGS", "-modfile=ignored.mod"),
                   ("GOWORK", str(self.repo / "dist/alternate.work")), ("GOENV", "/outside/go/env"),
                   ("GOTOOLCHAIN", "auto"))
        for key, value in changes:
            with self.subTest(key=key, value=value):
                original = self.go_environment[key]
                self.go_environment[key] = value
                with self.assertRaisesRegex(gate.Refused, "effective Go environment"):
                    self.source()
                self.go_environment[key] = original

    def test_raw_go_environment_refuses_before_invoking_go(self):
        for key, value in (("GOENV", "/outside/config"), ("GOTOOLCHAIN", "auto"),
                           ("GOFLAGS", "-overlay=ignored.json"), ("GOWORK", "off")):
            with self.subTest(key=key), patch.dict(os.environ, {key: value}):
                self.calls.clear()
                with self.assertRaisesRegex(gate.Refused, "Go environment inputs"):
                    self.source()
                self.assertFalse(any(args[0] == "go" for args in self.calls))

    def test_all_modes_refuse_alternate_effective_workspace_without_receipts(self):
        target = ("lantern", "linux", "amd64")
        binary = self.binary(target)
        self.go_environment["GOWORK"] = str(self.repo / "dist/alternate.work")
        modes = (self.source,
                 lambda: gate.verify_binary(self.repo, target, binary, self.head, self.tag),
                 lambda: gate.verify_complete(self.repo, self.head, self.tag))
        for mode in modes:
            with self.subTest(mode=mode), self.assertRaisesRegex(gate.Refused, "effective Go environment"):
                mode()
            self.assertFalse((self.repo / "dist/release-integrity").exists())

    def test_refuses_download_checksum_change_and_staged_or_untracked_source(self):
        for name, staged in (("go.work.sum", False), ("source.go", False), ("source.go", True), ("new.go", False)):
            with self.subTest(name=name, staged=staged):
                old = self.git("status", "--porcelain")
                self.assertFalse(old)
                path = self.repo / name
                original = path.read_bytes() if path.exists() else None
                path.write_text("changed\n")
                if staged:
                    self.git("add", name)
                with self.assertRaisesRegex(gate.Refused, "dirty"):
                    self.source()
                if staged:
                    self.git("restore", "--staged", name)
                if original is None:
                    path.unlink()
                else:
                    path.write_bytes(original)

    def test_wrong_commit_or_tag_refuses(self):
        for head, tag in (("0" * 40, self.tag), (self.head, "../tag"), (self.head[:12], self.tag)):
            with self.subTest(head=head, tag=tag), self.assertRaises(gate.Refused):
                gate.source(self.repo, head, tag)
        self.git("tag", "-d", self.tag)
        with self.assertRaises(gate.Refused):
            self.source()

    def test_recipe_refuses_unbound_version_changed_matrix_or_flags(self):
        for change in (RECIPE.replace("={{ .Tag }}", "=arbitrary"),
                       RECIPE.replace("      - windows\n", ""),
                       RECIPE.replace("-trimpath", "-buildvcs=false"),
                       RECIPE.replace("    main: ./cli", "    skip: true\n    main: ./cli"),
                       RECIPE.replace("    binary: lantern-cli", "    binary: lantern-cli\n    binary: lantern-cli")):
            with self.subTest(change=change), self.assertRaises(gate.Refused):
                (self.repo / ".goreleaser.yaml").write_text(change)
                gate.recipe(self.repo)
        (self.repo / ".goreleaser.yaml").write_text(RECIPE)


class BinaryTests(ReleaseFixture):
    def test_native_cli_has_actual_stdout_and_no_ldflags_fallback(self):
        target = ("lantern-cli", "linux", "amd64")
        result = self.qualify(target)
        self.assertEqual(result["native_cli"]["stdout"], self.tag + "\n")
        self.assertNotIn("-ldflags", {row["Key"] for row in result["buildinfo"]["Settings"]})
        self.assertTrue(gate.receipt_path(self.repo, target).is_file())

    def test_wrong_missing_duplicate_or_unknown_metadata_refuses(self):
        target = ("lantern-cli", "darwin", "arm64")
        path = self.binary(target)
        good = copy.deepcopy(self.infos[str(path)])
        changes = [lambda info: info.update(GoVersion="go0.0.0"),
                   lambda info: info["Main"].update(Version="(devel)"),
                   lambda info: info["Main"].update(Version=self.tag + "+dirty"),
                   lambda info: info.update(Path=gate.MODULE + "/server/cmd"),
                   lambda info: info.update(Deps=None),
                   lambda info: info["Deps"].append(info["Deps"][0]),
                   lambda info: info["Settings"].pop(),
                   lambda info: info["Settings"].append(info["Settings"][0]),
                   lambda info: info["Settings"].append({"Key": "GOEXPERIMENT", "Value": "unknown"})]
        for key, value in (("vcs.modified", "true"), ("vcs.revision", "0" * 40), ("GOOS", "linux"),
                           ("GOARCH", "amd64"), ("CGO_ENABLED", "1"), ("-trimpath", "false")):
            changes.append(lambda info, key=key, value=value:
                           next(row for row in info["Settings"] if row["Key"] == key).update(Value=value))
        for change in changes:
            self.infos[str(path)] = copy.deepcopy(good)
            change(self.infos[str(path)])
            with self.subTest(change=change), self.assertRaises(gate.Refused):
                gate.verify_binary(self.repo, target, path, self.head, self.tag)
            self.assertFalse(gate.receipt_path(self.repo, target).exists())

    def test_never_executes_server_or_foreign_cli(self):
        for target in (("lantern", "linux", "amd64"), ("lantern-cli", "windows", "amd64")):
            path = self.binary(target)
            result = gate.verify_binary(self.repo, target, path, self.head, self.tag)
            self.assertEqual(result["native_cli"]["status"], "not_executed")
            self.assertFalse(any(args[0] == str(path) for args in self.calls))

    def test_wrong_native_stdout_refuses_without_receipt(self):
        target = ("lantern-cli", "linux", "amd64")
        path = self.binary(target)
        path.write_text("#!/bin/sh\nprintf 'wrong\\n'\n")
        with self.assertRaisesRegex(gate.Refused, "stdout"):
            gate.verify_binary(self.repo, target, path, self.head, self.tag)
        self.assertFalse(gate.receipt_path(self.repo, target).exists())

    def test_duplicate_target_and_symlink_raw_or_receipt_directory_refuse(self):
        target = ("lantern", "linux", "amd64")
        self.qualify(target)
        with self.assertRaisesRegex(gate.Refused, "duplicate"):
            gate.verify_binary(self.repo, target, gate.target_path(self.repo, target), self.head, self.tag)
        gate.receipt_path(self.repo, target).unlink()
        raw = gate.target_path(self.repo, target)
        real = raw.with_name("real")
        raw.rename(real)
        raw.symlink_to(real.name)
        with self.assertRaisesRegex(gate.Refused, "regular"):
            gate.verify_binary(self.repo, target, raw, self.head, self.tag)

    def test_receipt_directory_symlink_refuses_without_writing_elsewhere(self):
        target = ("lantern", "linux", "amd64")
        path = self.binary(target)
        destination = self.repo / "ignored-receipts"
        destination.mkdir()
        # Keep this test-owned destination out of Git without touching project policy.
        (self.repo / ".git/info/exclude").write_text("ignored-receipts/\n")
        (self.repo / "dist/release-integrity").symlink_to(destination, target_is_directory=True)
        with self.assertRaisesRegex(gate.Refused, "real directory"):
            gate.verify_binary(self.repo, target, path, self.head, self.tag)
        self.assertFalse(list(destination.iterdir()))

    def test_wrong_inspection_toolchain_and_malformed_json_fail_closed(self):
        target = ("lantern-cli", "windows", "amd64")
        path = self.binary(target)
        def wrong_tool(args, repo):
            return b"go version go0.0.0 linux/amd64\n"
        with patch.object(gate, "command", side_effect=wrong_tool), self.assertRaisesRegex(gate.Refused, "toolchain"):
            gate.buildinfo(self.repo, path, target, {"go_version": "go9.8.7"})
        def malformed_info(args, repo):
            return b"go version go9.8.7 linux/amd64\n" if args == ["go", "version"] else b"{} garbage"
        with patch.object(gate, "command", side_effect=malformed_info), self.assertRaisesRegex(gate.Refused, "JSON"):
            gate.buildinfo(self.repo, path, target, {"go_version": "go9.8.7"})

    def test_json_duplicate_fields_are_not_accepted(self):
        with self.assertRaisesRegex(gate.Refused, "duplicate"):
            gate.json_read(b'{"GoVersion":"go9.8.7","GoVersion":"go9.8.7"}')


class CompleteTests(ReleaseFixture):
    def test_complete_real_tar_zip_and_ten_receipts(self):
        self.cohort()
        result = gate.verify_complete(self.repo, self.head, self.tag)
        self.assertEqual(result["verdict"], "pass")
        self.assertEqual(len(result["targets"]), 10)
        self.assertEqual(result["binary_signatures"], "not provided")
        self.assertTrue((self.repo / "dist/release-integrity/complete.json").is_file())
        self.assertFalse(self.git("status", "--porcelain"))
        with self.assertRaises(gate.Refused):
            gate.verify_complete(self.repo, self.head, self.tag)

    def test_missing_extra_receipt_or_archive_and_checksum_drift_refuse(self):
        self.cohort()
        files = [gate.receipt_path(self.repo, gate.TARGETS[0]),
                 self.repo / "dist" / gate.archive_name(gate.TARGETS[0], self.tag)]
        for path in files:
            data = path.read_bytes()
            path.unlink()
            with self.subTest(path=path), self.assertRaises(gate.Refused):
                gate.verify_complete(self.repo, self.head, self.tag)
            path.write_bytes(data)
        extra = self.repo / "dist" / "unexpected.zip"
        extra.write_bytes(b"extra")
        with self.assertRaisesRegex(gate.Refused, "extra release archive"):
            gate.verify_complete(self.repo, self.head, self.tag)
        extra.unlink()
        sums = self.repo / "dist" / "checksums.txt"
        original = sums.read_text()
        for contents in (original + original.splitlines()[0] + "\n", original.replace(original[:64], "0" * 64, 1),
                         original + "0" * 64 + "  ../escape.zip\n"):
            sums.write_text(contents)
            with self.subTest(contents=contents), self.assertRaises(gate.Refused):
                gate.verify_complete(self.repo, self.head, self.tag)

    def test_changed_raw_binary_receipt_source_or_native_observation_refuse(self):
        self.cohort()
        path = gate.target_path(self.repo, gate.TARGETS[-1])
        data = path.read_bytes()
        path.write_bytes(data + b"changed")
        with self.assertRaisesRegex(gate.Refused, "raw binary differs"):
            gate.verify_complete(self.repo, self.head, self.tag)
        path.write_bytes(data)
        receipt_file = gate.receipt_path(self.repo, gate.TARGETS[0])
        original = json.loads(receipt_file.read_text())
        for change in (lambda receipt: receipt["source"].update(head="0" * 40),
                       lambda receipt: receipt.update(native_cli={"status": "not_executed"})):
            receipt = copy.deepcopy(original)
            change(receipt)
            receipt_file.write_text(json.dumps(receipt))
            with self.subTest(change=change), self.assertRaises(gate.Refused):
                gate.verify_complete(self.repo, self.head, self.tag)

    def test_tar_and_zip_reject_extra_duplicate_unsafe_missing_or_wrong_members(self):
        for target in (("lantern", "linux", "amd64"), ("lantern-cli", "windows", "amd64")):
            self.qualify(target)
            raw = gate.target_path(self.repo, target)
            good = [(raw.name, raw.read_bytes()), ("LICENSE", (self.repo / "LICENSE").read_bytes()),
                    ("README.md", (self.repo / "README.md").read_bytes())]
            names = {name: (gate.hashlib.sha256(data).hexdigest(), gate.MAX_BINARY) for name, data in good}
            for members in (good + [("extra", b"extra")], good + [good[0]],
                            [("../" + good[0][0], good[0][1]), *good[1:]], good[:-1],
                            [good[0], ("LICENSE", b"different"), good[2]]):
                with self.subTest(target=target, names=[row[0] for row in members]), self.assertRaises(gate.Refused):
                    gate.verify_archive(self.archive(target, members), names)

    def test_links_and_directories_are_never_extracted(self):
        target = ("lantern", "linux", "amd64")
        raw = self.binary(target)
        path = self.archive(target)
        for kind in (tarfile.SYMTYPE, tarfile.LNKTYPE, tarfile.DIRTYPE):
            with tarfile.open(path, "w:gz") as output:
                item = tarfile.TarInfo(raw.name)
                item.type, item.linkname = kind, "../../outside"
                output.addfile(item)
            with self.subTest(kind=kind), self.assertRaises(gate.Refused):
                gate.verify_archive(path, {raw.name: (gate.sha256(raw), gate.MAX_BINARY)})
        zip_path = path.with_suffix(".zip")
        with zipfile.ZipFile(zip_path, "w") as output:
            item = zipfile.ZipInfo(raw.name)
            item.create_system, item.external_attr = 3, (stat.S_IFLNK | 0o777) << 16
            output.writestr(item, "../../outside")
        with self.assertRaises(gate.Refused):
            gate.verify_archive(zip_path, {raw.name: (gate.sha256(raw), gate.MAX_BINARY)})
        self.assertFalse((self.repo.parent / "outside").exists())


if __name__ == "__main__":
    unittest.main()
