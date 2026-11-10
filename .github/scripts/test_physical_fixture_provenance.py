"""Local provenance cannot turn mixed, changed or incomplete fixtures into proof."""

from contextlib import redirect_stderr, redirect_stdout
import copy
import io
import json
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch

import physical_fixture_provenance as proof


class PhysicalFixtureProvenanceTest(unittest.TestCase):
    def setUp(self):
        self.temporary = tempfile.TemporaryDirectory()
        self.base = Path(self.temporary.name).resolve()
        self.evidence = self.base / "evidence"
        for directory in (self.evidence, self.evidence / "build", self.evidence / "private"):
            directory.mkdir(mode=0o700)
        self.head = "a" * 40
        self.now = 1_794_268_000.0
        self.config = proof.digest(b"server time.asia.apple.com\n")
        self.config_patch = patch.object(proof, "configured_source_digest", return_value=self.config)
        self.config_read = self.config_patch.start()
        self.addCleanup(self.config_patch.stop)
        self.addCleanup(self.temporary.cleanup)

    def write(self, path, raw):
        path.write_bytes(raw)
        path.chmod(0o600)

    def json(self, path, value):
        self.write(path, proof.encoded(value))

    def save(self):
        self.json(self.evidence / "receipt.json", self.receipt)

    def command(self, argv, log=b"completed\n", exit_code=0):
        index = len(self.receipt["commands"])
        name = f"{index:02d}.log"
        self.write(self.evidence / name, log)
        row = {"argv": argv, "exit": exit_code, "log": name, "sha256": proof.digest(log)}
        self.receipt["commands"].append(row)
        return row

    def setup_fixture(self, profile="physical", source="controlled-source"):
        controlled = source == "controlled-source"
        self.profile, self.source = profile, source
        mode = proof.expected_mode(source)
        origin = "https://fixture.example.invalid:24443"
        self.fixture = {"schema": 2 if controlled else 1,
                        "kind": ("controlled-physical-serving" if profile == "physical" else "controlled-native-serving") if controlled else (
                            "current-physical-serving" if profile == "physical" else "synthetic-native-serving"),
                        "endpoint": origin, "ca_file": "ca.pem", "serving_until": proof.stamp(self.now + 360)}
        if controlled:
            self.fixture.update(mode)
            self.fixture["serving_from"] = proof.stamp(self.now)
        if profile == "physical":
            self.fixture.update(receipt_retention_ms=21_600_000, receipt_max_entries=1024, receipt_max_bytes=32 << 20,
                                credentials={"observer_tokens": ["lnt_m1_privateA", "lnt_m1_privateB"],
                                             "writer_token": "lnt_m1_privateC", "expires_at": proof.stamp(self.now + 600)})
        else:
            self.fixture.update(prefix="orders:fixture:", accounts={
                key: {"access_token": "lnu1_private" + key, "expires_at": proof.stamp(self.now + 3600)} for key in ("a", "b")})
        self.json(self.evidence / "private" / "fixture.json", self.fixture)
        self.write(self.evidence / "private" / "ca.pem", b"fixture test CA\n")
        self.write(self.evidence / "private" / "ready", b"ready\n")
        self.receipt = {"schema": 1, "status": "RUNNING", "mode": "serving", "serving_profile": profile,
                        "head": self.head, "tree": "b" * 40, "source_sha256": "c" * 64,
                        "image": "sha256:" + "d" * 64, "development_changes": False, "started": self.now - 10,
                        "serving_lifetime_seconds": 360, "serving_origin_sha256": proof.digest(origin.encode()),
                        "commands": [], "binaries": {}, **mode}
        if not controlled:
            self.receipt["source_config_sha256"] = self.config
        for package, binary in (("./server/internal/security", "security.test"), ("./tests/integration", "integration.test")):
            raw = binary.encode()
            path = self.evidence / "build" / binary
            self.write(path, raw)
            path.chmod(0o755)
            self.receipt["binaries"][binary] = proof.digest(raw)
            self.command(["go", "test", "-c", *(["-tags=lantern_test_time"] if controlled else []), "-o", str(path), package])
        self.name = "lantern-native-login-012345abcdef"
        label = "lantern.task=2119-controlled-serving" if controlled else "lantern.task=1809-native-serving"
        self.command(["docker", "build", "--pull=false", "--network=none", "--label", label, "-t", self.name, str(self.evidence / "build")])
        marker = "physical" if profile == "physical" else "native"
        ready = "observer_credentials=2 writer_credentials=1" if profile == "physical" else "accounts=2"
        self.log = ((proof.driver.CONTROLLED_SOURCE_MARKER if controlled else proof.driver.NORMAL_SOURCE_MARKER) + "\n"
                    + "\n".join(f"current_constructor node={i} mode={mode} passed=true" for mode in ("fresh", "resume") for i in (1, 2, 3))
                    + f"\n{marker}_fixture_ready schema={2 if controlled else 1} {ready}\n").encode()
        self.write(self.evidence / "03.log", self.log)
        self.save()

    def prepare(self):
        return proof.prepare(self.evidence, self.head, self.profile, self.source, now=self.now + 1)

    def completed(self):
        controlled = self.source == "controlled-source"
        label = "lantern.task=2119-controlled-serving" if controlled else "lantern.task=1809-native-serving"
        args = ["docker", "run", "--rm", "--pull=never", "--name", self.name, "--label", label,
                "--network=bridge", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges",
                "--user", f"{os.getuid()}:{os.getgid()}", "--pids-limit=256", "--memory=2g", "--cpus=4",
                "--sysctl", "net.ipv4.ip_unprivileged_port_start=0", "--tmpfs", "/work:rw,nosuid,nodev,size=512m,mode=1777"]
        if not controlled:
            args += ["--mount", "type=bind,source=/etc/ntp.conf,target=/etc/ntp.conf,readonly"]
        args += ["--mount", f"type=bind,source={self.evidence / 'private'},target=/native-fixture",
                 "--publish", "127.0.0.1:18443:16443/tcp"]
        for host in proof.driver.HOSTS:
            args += ["--add-host", host + ":127.0.0.1"]
        environment = {"LANTERN_CURRENT_PUBLIC_GATE": "1", "LANTERN_NATIVE_LOGIN_GATE": "1",
                       "LANTERN_CURRENT_FIXTURE_EXPORTER": "/security.test", "TMPDIR": "/work", "LANTERN_NATIVE_FIXTURE_SERVE": "1",
                       "LANTERN_NATIVE_FIXTURE_ORIGIN": "<private-service-origin>",
                       "LANTERN_NATIVE_FIXTURE_PREFIX": self.fixture.get("prefix", "orders:"),
                       "LANTERN_NATIVE_FIXTURE_LISTEN_PORT": "16443", "LANTERN_NATIVE_FIXTURE_LIFETIME_SECONDS": "360",
                       "LANTERN_NATIVE_FIXTURE_EXPORT_DIR": "/native-fixture"}
        if self.profile == "physical":
            environment["LANTERN_NATIVE_FIXTURE_PROFILE"] = "physical"
        if controlled:
            environment["LANTERN_NATIVE_FIXTURE_TIME_SOURCE"] = "controlled-source"
        for key, value in environment.items():
            args += ["--env", key + "=" + value]
        args += [self.receipt["image"], "-test.v", "-test.run=^" + proof.driver.ROOT_TEST + "$", "-test.timeout=660s"]
        marker = "physical" if self.profile == "physical" else "native"
        case = proof.driver.ROOT_TEST + f"/{marker}_fixture_serving_export"
        stopped = " stopped_at=" + proof.stamp(self.now + 35) if controlled else ""
        log = self.log + f"{marker}_fixture_stopped reason=released{stopped}\n--- PASS: {case} (10.0s)\n--- PASS: {proof.driver.ROOT_TEST} (10.0s)\nPASS\n".encode()
        self.command(args, log)
        self.command(["docker", "rm", "--force", self.name], b"already removed\n", exit_code=1)
        self.command(["docker", "image", "rm", self.name])
        self.receipt.update(status="PASS", finished=self.now + 40, native_owner_constructors=copy.deepcopy(proof.OWNERS), cases=[case])
        (self.evidence / "private" / "ready").unlink()
        self.save()

    def finalize(self, binding):
        raw = proof.encoded(binding)
        return proof.finalize(self.evidence, raw, proof.digest(raw), now=self.now + 45)

    def test_four_modes_are_bound_through_completion_without_credentials(self):
        for profile in ("physical", "native"):
            for source in ("controlled-source", "configured-source"):
                with self.subTest(profile=profile, source=source):
                    # Separate evidence for each real lifecycle, never overwrite history.
                    self.evidence = self.base / (profile + "-" + source)
                    for path in (self.evidence, self.evidence / "build", self.evidence / "private"):
                        path.mkdir(mode=0o700)
                    self.setup_fixture(profile, source)
                    binding = self.prepare()
                    self.completed()
                    final = self.finalize(binding)
                    self.assertEqual(final["status"], "PASS")
                    self.assertEqual(final["fixture"]["source_kind"], source)
                    self.assertEqual(final["command_results"][4]["exit"], 1)
                    public = json.dumps(final) + json.dumps(binding)
                    self.assertNotIn("privateA", public)
                    self.assertNotIn("fixture.example", public)
                    self.assertNotIn(str(self.evidence), public)
                    self.assertIn("device/provider/UTC/configured-source qualification NOT_PROVEN", final["proof"])

    def test_controlled_never_reads_configured_source_input(self):
        self.setup_fixture()
        self.config_read.side_effect = AssertionError("must not read configured source")
        binding = self.prepare()
        self.completed()
        self.finalize(binding)
        self.config_read.assert_not_called()

    def test_configured_source_input_binding_is_read_only_and_exact(self):
        self.setup_fixture(source="configured-source")
        self.config_read.return_value = "e" * 64
        with self.assertRaises(ValueError):
            self.prepare()

    def test_expected_source_profile_and_clean_head_are_required(self):
        self.setup_fixture()
        for head, profile, source in (("e" * 40, "physical", self.source), (self.head, "native", self.source),
                                     (self.head, "physical", "configured-source")):
            with self.subTest(head=head, profile=profile, source=source), self.assertRaises(ValueError):
                proof.prepare(self.evidence, head, profile, source, now=self.now)
        self.receipt["development_changes"] = True
        self.save()
        with self.assertRaises(ValueError):
            self.prepare()

    def test_mixed_build_or_fixture_provenance_is_refused(self):
        self.setup_fixture()
        original_receipt = copy.deepcopy(self.receipt)
        original_fixture = copy.deepcopy(self.fixture)
        for field, value in (("build_kind", "normal"), ("build_tags", []), ("synthetic_utc", 1),
                             ("source_kind", "configured-source"), ("source_config_sha256", self.config)):
            with self.subTest(field=field):
                self.receipt = copy.deepcopy(original_receipt)
                self.receipt[field] = value
                self.save()
                with self.assertRaises(ValueError):
                    self.prepare()
        self.receipt = original_receipt
        self.save()
        for field, value in (("schema", 1), ("kind", "current-physical-serving"), ("build_tags", []), ("synthetic_utc", False)):
            with self.subTest(field=field):
                self.fixture = copy.deepcopy(original_fixture)
                self.fixture[field] = value
                self.json(self.evidence / "private" / "fixture.json", self.fixture)
                with self.assertRaises(ValueError):
                    self.prepare()

    def test_only_both_tagged_builds_qualify_controlled_source(self):
        self.setup_fixture()
        self.receipt["commands"][1]["argv"].remove("-tags=lantern_test_time")
        self.save()
        with self.assertRaises(ValueError):
            self.prepare()

    def test_ready_and_remaining_interval_are_required_before_phone_start(self):
        self.setup_fixture()
        with self.assertRaises(ValueError):
            proof.prepare(self.evidence, self.head, self.profile, self.source, now=self.now + 360)
        (self.evidence / "private" / "ready").unlink()
        with self.assertRaises(OSError):
            self.prepare()

    def test_constructor_order_and_source_observation_cannot_be_relabelled(self):
        self.setup_fixture()
        for log in (self.log.replace(b"node=2 mode=fresh", b"node=1 mode=fresh"),
                    self.log.replace(proof.driver.CONTROLLED_SOURCE_MARKER.encode(), proof.driver.NORMAL_SOURCE_MARKER.encode()),
                    self.log.replace(b"schema=2", b"schema=1"), self.log + b"--- SKIP: fixture\n",
                    self.log + b"production native constructor: unknown\n",
                    self.log + b"physical_fixture_ready schema=1 observer_credentials=2 writer_credentials=1\n",
                    self.log + b"native_fixture_ready schema=2 accounts=2\n"):
            with self.subTest(log=log):
                self.write(self.evidence / "03.log", log)
                with self.assertRaises(ValueError):
                    self.prepare()

    def test_private_file_modes_symlinks_and_duplicate_json_are_refused(self):
        self.setup_fixture()
        path = self.evidence / "receipt.json"
        raw = path.read_bytes()
        self.write(path, b'{"schema":1,"schema":1}')
        with self.assertRaises(ValueError):
            self.prepare()
        self.write(path, raw)
        path.chmod(0o644)
        with self.assertRaises(ValueError):
            self.prepare()
        path.unlink()
        saved = self.base / "saved-receipt.json"
        self.write(saved, raw)
        path.symlink_to(saved)
        with self.assertRaises(ValueError):
            self.prepare()

    def test_changed_artifact_descriptor_or_ca_breaks_original_binding(self):
        self.setup_fixture()
        binding = self.prepare()
        self.completed()
        for path in (self.evidence / "build" / "security.test", self.evidence / "private" / "fixture.json", self.evidence / "private" / "ca.pem"):
            with self.subTest(path=path.name):
                raw, mode = path.read_bytes(), path.stat().st_mode & 0o777
                path.write_bytes(raw + b" ")
                with self.assertRaises(ValueError):
                    self.finalize(binding)
                path.write_bytes(raw)
                path.chmod(mode)

    def test_failed_incomplete_or_still_ready_lifecycle_does_not_finalize(self):
        self.setup_fixture()
        binding = self.prepare()
        self.completed()
        original = copy.deepcopy(self.receipt)
        for field, value in (("status", "FAIL"), ("cases", []), ("native_owner_constructors", proof.OWNERS[:-1]), ("finished", None)):
            with self.subTest(field=field):
                self.receipt = copy.deepcopy(original)
                self.receipt[field] = value
                self.save()
                with self.assertRaises(ValueError):
                    self.finalize(binding)
        self.receipt = original
        self.save()
        self.write(self.evidence / "private" / "ready", b"ready\n")
        with self.assertRaises(ValueError):
            self.finalize(binding)

    def test_completed_log_must_extend_original_ready_observation(self):
        self.setup_fixture()
        binding = self.prepare()
        self.completed()
        row = self.receipt["commands"][3]
        path = self.evidence / row["log"]
        raw = b"replacement prefix\n" + path.read_bytes()
        self.write(path, raw)
        row["sha256"] = proof.digest(raw)
        self.save()
        with self.assertRaises(ValueError):
            self.finalize(binding)

    def test_cleanup_is_retained_but_unknown_failure_is_refused(self):
        self.setup_fixture()
        binding = self.prepare()
        self.completed()
        self.receipt["commands"][4]["exit"] = 2
        self.save()
        with self.assertRaises(ValueError):
            self.finalize(binding)

    def test_actual_stop_observation_is_required_before_device_summary(self):
        self.setup_fixture()
        binding = self.prepare()
        self.completed()
        row = self.receipt["commands"][3]
        path = self.evidence / row["log"]
        raw = path.read_bytes().replace((" stopped_at=" + proof.stamp(self.now + 35)).encode(), b"")
        self.write(path, raw)
        row["sha256"] = proof.digest(raw)
        self.save()
        with self.assertRaises((ValueError, RuntimeError)):
            self.finalize(binding)

    def test_controlled_run_cannot_import_external_source_mount_or_change_environment(self):
        self.setup_fixture()
        binding = self.prepare()
        self.completed()
        args = self.receipt["commands"][3]["argv"]
        args[3:3] = ["--mount", "type=bind,source=/etc/ntp.conf,target=/etc/ntp.conf,readonly"]
        self.save()
        with self.assertRaises(ValueError):
            self.finalize(binding)

    def test_binding_digest_cannot_be_replaced_silently(self):
        self.setup_fixture()
        binding = self.prepare()
        original_digest = proof.digest(proof.encoded(binding))
        self.completed()
        binding["fixture"]["source_sha256"] = "e" * 64
        with self.assertRaises(ValueError):
            proof.finalize(self.evidence, proof.encoded(binding), original_digest, now=self.now + 45)

    def test_verified_sidecar_summary_binds_device_interval_marker_and_record(self):
        self.setup_fixture()
        binding = self.prepare()
        self.json(self.evidence / "physical-fixture-binding.json", binding)
        self.completed()
        final = self.finalize(binding)
        path = self.evidence / "physical-fixture-provenance.json"
        self.json(path, final)
        summary = proof.public_summary(path, self.head, "physical", proof.stamp(self.now + 2), proof.stamp(self.now + 30), "1" * 64, "2" * 64)
        self.assertEqual(summary["sourceKind"], "controlled-source")
        proof.validate_summary(summary, self.head, "physical", phone_marker_sha256="1" * 64, record_payload_sha256="2" * 64)
        with self.assertRaises(ValueError):
            proof.public_summary(path, self.head, "physical", proof.stamp(self.now + 2), proof.stamp(self.now + 300), "1" * 64, "2" * 64)
        for change in ({"sourceCommit": "e" * 40}, {"deviceStartedAt": proof.stamp(self.now)},
                       {"deviceFinishedAt": proof.stamp(self.now + 360)}, {"syntheticUtc": False},
                       {"deviceFinishedAt": proof.stamp(self.now + 300)}, {"deviceFinishedAt": proof.stamp(self.now + 37)},
                       {"phoneMarkerSha256": "bad"}, {"unexpectedToken": "private"}):
            with self.subTest(change=change), self.assertRaises(ValueError):
                proof.validate_summary(summary | change, self.head, "physical")
        final["fixture"]["image"] = "sha256:" + "e" * 64
        self.json(path, final)
        with self.assertRaises(ValueError):
            proof.verify_final(path)

    def test_public_summary_requires_actual_retained_private_evidence(self):
        self.setup_fixture()
        binding = self.prepare()
        self.json(self.evidence / "physical-fixture-binding.json", binding)
        self.completed()
        final = self.finalize(binding)
        path = self.evidence / "physical-fixture-provenance.json"
        self.json(path, final)
        (self.evidence / "build" / "integration.test").unlink()
        with self.assertRaises(OSError):
            proof.public_summary(path, self.head, "physical", proof.stamp(self.now + 2), proof.stamp(self.now + 30), "1" * 64, "2" * 64)

    def test_new_outputs_never_overwrite_retained_evidence(self):
        destination = self.base / "binding.json"
        original = {"original": "retained"}
        proof.write_new(destination, original)
        self.assertEqual(destination.stat().st_mode & 0o777, 0o600)
        with self.assertRaises(FileExistsError):
            proof.write_new(destination, {"different": True})
        self.assertEqual(proof.decode(destination.read_bytes()), original)

    def test_cli_does_not_expose_private_failure_details(self):
        self.setup_fixture()
        self.receipt["error"] = "private credential should never print"
        self.save()
        stdout, stderr = io.StringIO(), io.StringIO()
        with redirect_stdout(stdout), redirect_stderr(stderr), self.assertRaises(SystemExit) as stopped:
            proof.main(["prepare", "--evidence", str(self.evidence), "--output", str(self.evidence / "physical-fixture-binding.json"),
                        "--expected-head", self.head, "--profile", "physical", "--source-kind", "controlled-source"])
        self.assertEqual(stopped.exception.code, 1)
        self.assertNotIn("private credential", stderr.getvalue())
        self.assertEqual(stdout.getvalue(), "")


if __name__ == "__main__":
    unittest.main()
