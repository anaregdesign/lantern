import io
import json
from pathlib import Path
import re
import subprocess
import tarfile
import tempfile
import unittest
from unittest.mock import patch

import verify_go_sdk_release as gate


class GoSDKReleaseTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.repo = Path(self.temp.name) / "repository"
        self.repo.mkdir()
        self.pb_version = "v0.13.0"
        self.sdk_tag = "sdks/go/v0.14.0"
        self.pb = self.repo / "pb"
        self.pb_go = self.pb / "graph/v1/graph.pb.go"
        self.pb_go.parent.mkdir(parents=True)
        (self.pb / "go.mod").write_text(f"module {gate.PB_MODULE}\ngo 1.27.0\n")
        self.pb_go.write_text("package v1\n")
        self.sdk = self.repo / "sdks/go"
        self.sdk.mkdir(parents=True)
        self.sdk_mod = self.sdk / "go.mod"
        self.sdk_mod.write_text(
            f"module {gate.SDK_MODULE}\ngo 1.27.0\n"
            f"require {gate.PB_MODULE} {self.pb_version}\n"
            f"replace {gate.PB_MODULE} => ../../pb\n"
        )
        (self.sdk / "go.sum").write_text(
            f"{gate.PB_MODULE} {self.pb_version} h1:verified\n"
            f"{gate.PB_MODULE} {self.pb_version}/go.mod h1:verified\n"
        )
        (self.sdk / "client.go").write_text("package client\n")
        (self.sdk / "client_test.go").write_text("package client\n")
        self.git("init", "-q")
        self.commit()
        self.git("tag", f"pb/{self.pb_version}")
        self.git("tag", self.sdk_tag)
        self.calls = []
        self.keep_replace = False
        self.fail_download = False
        self.corrupt_download = None
        self.selected_version = self.pb_version
        self.selected_replace = False

    def git(self, *args, capture=False):
        result = subprocess.run(
            ["git", *args], cwd=self.repo, check=True,
            stdout=subprocess.PIPE if capture else subprocess.DEVNULL,
        )
        return result.stdout

    def commit(self):
        self.git("add", ".")
        self.git(
            "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
            "commit", "-qm", "fixture",
        )

    def go(self, source, env, *args, capture=False):
        self.calls.append((args, source, env.copy()))
        go_mod = source / "go.mod"
        text = go_mod.read_text() if go_mod.is_file() else ""
        if args == ("mod", "edit", "-json"):
            self.assertEqual((source / "client.go").read_text(), "package client\n")
            requirements = re.findall(
                rf"(?m)^require {re.escape(gate.PB_MODULE)} (\S+)$", text
            )
            replacements = re.finditer(
                r"(?m)^replace (\S+) => (\S+)(?: (\S+))?$", text
            )
            manifest = {
                "Module": {"Path": gate.SDK_MODULE},
                "Require": [
                    {"Path": gate.PB_MODULE, "Version": version}
                    for version in requirements
                ],
                "Replace": [
                    {
                        "Old": {"Path": replacement.group(1)},
                        "New": {
                            "Path": replacement.group(2),
                            "Version": replacement.group(3) or "",
                        },
                    }
                    for replacement in replacements
                ],
            }
            return json.dumps(manifest).encode()
        if args == ("mod", "edit", f"-dropreplace={gate.PB_MODULE}"):
            if not self.keep_replace:
                go_mod.write_text(
                    re.sub(rf"(?m)^replace {re.escape(gate.PB_MODULE)} => .+\n", "", text)
                )
            return b""
        if args == ("mod", "download", "-json", f"{gate.PB_MODULE}@{self.pb_version}"):
            self.assertFalse(go_mod.exists())
            self.assertFalse((source / "go.sum").exists())
            self.assertTrue((source.parent / "sdks-go" / "go.sum").is_file())
            if self.fail_download:
                raise gate.ReleaseError("proxy returned 404")
            directory = Path(env["GOMODCACHE"]) / f"pb@{self.pb_version}"
            directory.mkdir(parents=True)
            archive = self.git(
                "archive", "--format=tar", f"refs/tags/pb/{self.pb_version}:pb",
                capture=True,
            )
            with tarfile.open(fileobj=io.BytesIO(archive)) as contents:
                contents.extractall(directory, filter="data")
            if self.corrupt_download == "go":
                (directory / "graph/v1/graph.pb.go").write_text("package old\n")
            elif self.corrupt_download == "mod":
                (directory / "go.mod").write_text("module example.com/other\n")
            return json.dumps({
                "Path": gate.PB_MODULE,
                "Version": self.pb_version,
                "Dir": str(directory),
                "Sum": "h1:verified",
                "GoModSum": "h1:verified",
            }).encode()
        if args == ("mod", "download", f"{gate.PB_MODULE}@{self.pb_version}"):
            self.assertTrue(go_mod.is_file())
            self.assertNotIn("replace", text)
            sums = source / "go.sum"
            recorded = sums.read_text() if sums.is_file() else ""
            for suffix in ("", "/go.mod"):
                entry = f"{gate.PB_MODULE} {self.pb_version}{suffix} h1:verified\n"
                if entry not in recorded:
                    recorded += entry
            sums.write_text(recorded)
            return b""
        if args == ("list", "-mod=readonly", "-m", "-json", gate.PB_MODULE):
            selected = {"Path": gate.PB_MODULE, "Version": self.selected_version}
            if self.selected_replace:
                selected["Replace"] = {"Path": "../../pb"}
            return json.dumps(selected).encode()
        if args in (
            ("build", "-mod=readonly", "./..."),
            ("test", "-mod=readonly", "./..."),
        ):
            self.assertNotIn("replace", text)
            return b""
        self.fail(f"unexpected Go command: {args}")

    def verify(self, tag=None):
        with patch.object(gate, "go", side_effect=self.go):
            gate.verify_release(self.repo, tag or self.sdk_tag)

    def assert_no_build(self):
        self.assertFalse(any(args[0] in ("build", "test") for args, _, _ in self.calls))

    def test_failed_go_download_reports_json_error(self):
        failure = subprocess.CalledProcessError(
            1, ["go", "mod", "download"], output=b'{"Error":"proxy returned 404"}\n'
        )
        with patch.object(gate.subprocess, "run", side_effect=failure):
            with self.assertRaisesRegex(gate.ReleaseError, "proxy returned 404"):
                gate.command(["go", "mod", "download"], cwd=self.repo, capture=True)

    def test_tagged_archive_uses_published_pb_outside_workspace(self):
        original = self.sdk_mod.read_bytes()
        self.verify()
        self.assertEqual(self.sdk_mod.read_bytes(), original)
        self.assertEqual(
            [args[0] for args, _, _ in self.calls],
            ["mod", "mod", "mod", "mod", "mod", "list", "build", "test"],
        )
        self.assertEqual(
            [args for args, _, _ in self.calls if args[0] in ("build", "test")],
            [("build", "-mod=readonly", "./..."), ("test", "-mod=readonly", "./...")],
        )
        for _, source, env in self.calls:
            self.assertNotIn(self.repo, source.parents)
            self.assertEqual(env["GOWORK"], "off")
            self.assertEqual(env["GOPROXY"], "https://proxy.golang.org")
            self.assertEqual(env["GOSUMDB"], "sum.golang.org")
            self.assertEqual(env["GOPRIVATE"], "")
            self.assertEqual(env["GONOPROXY"], "")
            self.assertEqual(env["GONOSUMDB"], "")
            self.assertIn("lantern-go-sdk-release-", env["GOMODCACHE"])

    def test_pb_tag_need_not_be_the_sdk_commit(self):
        (self.pb / "README.md").write_text("Documentation after pb tag.\n")
        self.commit()
        sdk_tag = "sdks/go/v0.14.1"
        self.git("tag", sdk_tag)
        self.assertNotEqual(
            self.git("rev-parse", "pb/v0.13.0", capture=True),
            self.git("rev-parse", sdk_tag, capture=True),
        )
        self.verify(sdk_tag)

    def test_uncommitted_sdk_worktree_edits_are_not_archived(self):
        (self.sdk / "client.go").write_text("uncommitted bad code\n")
        self.verify()

    def test_stale_pb_tag_blocks_release(self):
        self.pb_go.write_text("package newer\n")
        self.commit()
        sdk_tag = "sdks/go/v0.14.1"
        self.git("tag", sdk_tag)
        with self.assertRaisesRegex(gate.ReleaseError, "stale|differs"):
            self.verify(sdk_tag)
        self.assert_no_build()

    def test_missing_pb_tag_blocks_release(self):
        self.sdk_mod.write_text(
            self.sdk_mod.read_text().replace(self.pb_version, "v0.14.0")
        )
        self.commit()
        sdk_tag = "sdks/go/v0.14.1"
        self.git("tag", sdk_tag)
        with self.assertRaisesRegex(gate.ReleaseError, "tag is missing"):
            self.verify(sdk_tag)
        self.assert_no_build()

    def test_missing_or_pseudo_pb_requirement_blocks_release(self):
        base = {"Module": {"Path": gate.SDK_MODULE}, "Require": []}
        for version in (None, "v0.0.0-20260926000000-abcdef123456", "../../pb"):
            with self.subTest(version=version), self.assertRaises(gate.ReleaseError):
                manifest = dict(base)
                if version is not None:
                    manifest["Require"] = [{
                        "Path": gate.PB_MODULE, "Version": version,
                    }]
                gate.required_pb_version(manifest)

    def test_unresolvable_published_pb_blocks_release(self):
        self.fail_download = True
        with self.assertRaisesRegex(gate.ReleaseError, "cannot be resolved.*404"):
            self.verify()
        self.assert_no_build()

    def test_checksum_probe_rejects_existing_go_sum(self):
        checksum_dir = Path(self.temp.name) / "checksum"
        checksum_dir.mkdir()
        (checksum_dir / "go.sum").write_text(f"{gate.PB_MODULE} {self.pb_version} h1:cached\n")
        with self.assertRaisesRegex(gate.ReleaseError, "must not reuse"):
            gate.download_pb(checksum_dir, gate.isolated_go_env(checksum_dir), self.pb_version)
        self.assertEqual(self.calls, [])

    def test_missing_pb_sums_are_added_only_to_temporary_sdk(self):
        (self.sdk / "go.sum").write_text("")
        self.commit()
        sdk_tag = "sdks/go/v0.14.1"
        self.git("tag", sdk_tag)
        self.verify(sdk_tag)
        self.assertEqual((self.sdk / "go.sum").read_text(), "")
        self.assertEqual(
            [args for args, _, _ in self.calls if args[:2] == ("mod", "download")],
            [
                ("mod", "download", "-json", f"{gate.PB_MODULE}@{self.pb_version}"),
                ("mod", "download", f"{gate.PB_MODULE}@{self.pb_version}"),
            ],
        )

    def test_conflicting_tagged_pb_sum_blocks_release(self):
        (self.sdk / "go.sum").write_text(f"{gate.PB_MODULE} {self.pb_version} h1:wrong\n")
        self.commit()
        sdk_tag = "sdks/go/v0.14.1"
        self.git("tag", sdk_tag)
        with self.assertRaisesRegex(gate.ReleaseError, "conflicts with published pb checksums"):
            self.verify(sdk_tag)
        self.assert_no_build()

    def test_changed_published_pb_source_blocks_release(self):
        self.corrupt_download = "go"
        with self.assertRaisesRegex(gate.ReleaseError, "differs.*graph.pb.go"):
            self.verify()
        self.assert_no_build()

    def test_changed_published_pb_manifest_blocks_release(self):
        self.corrupt_download = "mod"
        with self.assertRaisesRegex(gate.ReleaseError, "differs.*pb/go.mod"):
            self.verify()
        self.assert_no_build()

    def test_leaked_local_replace_blocks_release(self):
        self.keep_replace = True
        with self.assertRaisesRegex(gate.ReleaseError, "go.mod replacement leaked"):
            self.verify()
        self.assert_no_build()

    def test_non_pb_remote_replace_blocks_release(self):
        self.sdk_mod.write_text(
            self.sdk_mod.read_text()
            + "replace connectrpc.com/connect => example.com/connect v9.9.9\n"
        )
        self.commit()
        sdk_tag = "sdks/go/v0.14.1"
        self.git("tag", sdk_tag)
        with self.assertRaisesRegex(gate.ReleaseError, "unexpected replace directives"):
            self.verify(sdk_tag)
        self.assert_no_build()

    def test_noncanonical_pb_local_path_blocks_release(self):
        self.sdk_mod.write_text(
            self.sdk_mod.read_text().replace("../../pb", "../pb")
        )
        self.commit()
        sdk_tag = "sdks/go/v0.14.1"
        self.git("tag", sdk_tag)
        with self.assertRaisesRegex(gate.ReleaseError, "unexpected replace directive"):
            self.verify(sdk_tag)
        self.assert_no_build()

    def test_remote_pb_replace_blocks_release(self):
        self.sdk_mod.write_text(
            self.sdk_mod.read_text().replace(
                f"replace {gate.PB_MODULE} => ../../pb",
                f"replace {gate.PB_MODULE} => example.com/other/pb v0.13.0",
            )
        )
        self.commit()
        sdk_tag = "sdks/go/v0.14.1"
        self.git("tag", sdk_tag)
        with self.assertRaisesRegex(gate.ReleaseError, "unexpected replace directive"):
            self.verify(sdk_tag)
        self.assert_no_build()

    def test_selected_version_or_replace_blocks_release(self):
        for version, replaced in (("v0.12.0", False), (self.pb_version, True)):
            with self.subTest(version=version, replaced=replaced):
                self.calls.clear()
                self.selected_version = version
                self.selected_replace = replaced
                with self.assertRaisesRegex(gate.ReleaseError, "selected a different or replaced"):
                    self.verify()
                self.assert_no_build()


if __name__ == "__main__":
    unittest.main()
