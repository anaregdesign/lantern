from datetime import datetime, timedelta, timezone
import hashlib
import io
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from unittest.mock import patch

import physical_receipt_attestation as attestation


COMMIT = "a" * 40
RUN_ID = "b" * 32
TARGET = "integration_test/future_receipt_test.dart"
SCENARIOS = frozenset({"receipt_applied", "receipt_replayed"})
FINAL_SCENARIOS = frozenset({
    "receipt_authenticated_trusted_tls",
    "receipt_untrusted_tls_rejected",
    "receipt_conditional_put_exact",
    "receipt_vertex_delete_exact",
    "receipt_edge_delete_exact",
    "receipt_contribution_add_after_delete",
    "receipt_nonfinite_derived_float32",
    "receipt_committed_response_loss",
    "receipt_real_sigkill_sqlite_reopen",
    "receipt_relaunch_status_first_no_resend",
    "receipt_radio_foreground_recovery",
    "android_doze_like_pause",
})


def utc(when):
    return when.isoformat().replace("+00:00", "Z")


class PhysicalReceiptAttestationTest(unittest.TestCase):
    def setUp(self):
        self.sandbox = tempfile.TemporaryDirectory()
        self.addCleanup(self.sandbox.cleanup)
        self.directory = Path(self.sandbox.name)
        self.now = datetime.now(timezone.utc)
        self.run_start = self.now - timedelta(minutes=3)
        self.started = self.run_start + timedelta(seconds=10)
        self.finished = self.run_start + timedelta(minutes=2)
        self.recorded = self.finished + timedelta(seconds=10)
        self.marker_path = self.directory / "marker.json"
        self.record_path = self.directory / "record.json"

    def fixture(self, platform="android"):
        self.platform = platform
        suffix = attestation.BUILD_SUFFIXES[platform]
        self.built_path = self.directory.joinpath(*suffix)
        self.built_path.parent.mkdir(parents=True, exist_ok=True)
        self.built_path.write_bytes(b"target-specific profile AOT bytes")
        digest = hashlib.sha256(self.built_path.read_bytes()).hexdigest()
        marker = {
            "schema": 1, "kind": attestation.MARKER_KIND, "contentFree": True,
            "testedCommit": COMMIT, "target": TARGET, "runId": RUN_ID,
            "platform": platform, "packageId": attestation.PACKAGE_IDS[platform],
            "installedBinarySha256": digest, "startedAt": utc(self.started),
            "finishedAt": utc(self.finished),
            "completedScenarios": ["receipt_replayed", "receipt_applied"],
            "status": "passed", "phase": "complete",
        }
        record = {
            "schema": 1, "kind": attestation.RECORD_KIND,
            "repository": attestation.REPOSITORY, "contentFree": True,
            "physicalDevice": True, "testedCommit": COMMIT, "runId": RUN_ID,
            "runStartedAt": utc(self.run_start), "recordedAt": utc(self.recorded),
            "platform": {"kind": f"physical-{platform}"},
            "application": {
                "packageId": attestation.PACKAGE_IDS[platform], "target": TARGET,
                "binarySha256": digest,
            },
            "scenarios": sorted(SCENARIOS), "result": "passed",
        }
        return marker, record

    def final_fixture(self, platform="android"):
        marker, record = self.fixture(platform)
        scenarios = (FINAL_SCENARIOS - {"android_doze_like_pause"}) | {
            "ios_local_network_privacy_denial_retry"
        } if platform == "ios" else FINAL_SCENARIOS
        marker["schema"] = 2
        marker["target"] = attestation.RECEIPT_TARGET
        marker["completedScenarios"] = sorted(scenarios)
        marker["restart"] = {
            "preparedAt": utc(self.started + timedelta(seconds=30)),
            "resumedAt": utc(self.started + timedelta(seconds=60)),
            "processChanged": True,
        }
        record["application"]["target"] = attestation.RECEIPT_TARGET
        record["scenarios"] = sorted(scenarios)
        record["network"] = {
            "transport": "Connect/HTTPS",
            "authenticated": True,
            "platformTrustedTls": True,
            "fault": "committed-response-socket-drop",
        }
        return marker, record, scenarios

    def validate(self, marker, record, **overrides):
        self.marker_path.write_text(json.dumps(marker))
        self.record_path.write_text(json.dumps(record))

        return self.validate_files(**overrides)

    def validate_files(self, **overrides):
        options = {
            "tested_commit": COMMIT,
            "target": TARGET,
            "platform": self.platform,
            "required_scenarios": SCENARIOS,
            "run_id": RUN_ID,
            "run_started_at": utc(self.run_start),
            "now": self.now,
        }
        options.update(overrides)
        return attestation.validate_receipt_attestation(
            self.marker_path, self.record_path, self.built_path, **options
        )

    def test_duplicate_marker_or_nested_record_fields_fail_closed(self):
        marker, record = self.fixture()
        self.validate(marker, record)
        self.marker_path.write_text(
            self.marker_path.read_text().replace(
                '"status": "passed"', '"status": "failed", "status": "passed"', 1
            )
        )
        with self.assertRaisesRegex(ValueError, "duplicate receipt JSON field"):
            self.validate_files()

        self.marker_path.write_text(json.dumps(marker))
        self.record_path.write_text(
            self.record_path.read_text().replace(
                '"application": {',
                '"application": {"binarySha256": "' + "f" * 64 + '", ',
                1,
            )
        )
        with self.assertRaisesRegex(ValueError, "duplicate receipt JSON field"):
            self.validate_files()

    def test_both_platforms_bind_the_installed_target_to_host_build_bytes(self):
        for platform in ("android", "ios"):
            with self.subTest(platform=platform):
                marker, record = self.fixture(platform)
                self.assertEqual(self.validate(marker, record), marker["installedBinarySha256"])

    def test_missing_files_or_json_string_are_not_attestation(self):
        marker, record = self.fixture()
        with self.assertRaisesRegex(ValueError, "missing on-device"):
            attestation.validate_receipt_attestation(
                self.marker_path, self.record_path, self.built_path,
                tested_commit=COMMIT, target=TARGET, platform="android",
                required_scenarios=SCENARIOS, run_id=RUN_ID,
                run_started_at=utc(self.run_start), now=self.now,
            )
        self.validate(marker, record)
        self.record_path.unlink()
        with self.assertRaisesRegex(ValueError, "missing receipt evidence"):
            attestation.validate_receipt_attestation(
                self.marker_path, self.record_path, self.built_path,
                tested_commit=COMMIT, target=TARGET, platform="android",
                required_scenarios=SCENARIOS, run_id=RUN_ID,
                run_started_at=utc(self.run_start), now=self.now,
            )
        self.record_path.write_text(json.dumps(record))
        self.marker_path.write_text('"passed"')
        with self.assertRaisesRegex(ValueError, "JSON object"):
            attestation.validate_receipt_attestation(
                self.marker_path, self.record_path, self.built_path,
                tested_commit=COMMIT, target=TARGET, platform="android",
                required_scenarios=SCENARIOS, run_id=RUN_ID,
                run_started_at=utc(self.run_start), now=self.now,
            )

    def test_stale_replayed_marker_and_impossible_capture_are_rejected(self):
        marker, record = self.fixture()
        marker["startedAt"] = utc(self.started - timedelta(hours=1))
        with self.assertRaisesRegex(ValueError, "stale"):
            self.validate(marker, record)
        marker, record = self.fixture()
        record["recordedAt"] = utc(self.finished + timedelta(hours=1))
        with self.assertRaisesRegex(ValueError, "freshly captured"):
            self.validate(marker, record)
        marker, record = self.fixture()
        marker["runId"] = "c" * 32
        with self.assertRaisesRegex(ValueError, "another run"):
            self.validate(marker, record)

    def test_entire_old_run_cannot_pass_capture_time_validation(self):
        marker, record = self.fixture()
        age = timedelta(days=7)
        marker["startedAt"] = utc(self.started - age)
        marker["finishedAt"] = utc(self.finished - age)
        record["runStartedAt"] = utc(self.run_start - age)
        record["recordedAt"] = utc(self.recorded - age)
        with self.assertRaisesRegex(ValueError, "stale for capture-time"):
            self.validate(marker, record, run_started_at=utc(self.run_start - age))

        marker, record = self.fixture()
        self.validate(marker, record, now=self.recorded + attestation.CAPTURE_LIMIT)
        with self.assertRaisesRegex(ValueError, "stale for capture-time"):
            self.validate_files(
                now=self.recorded + attestation.CAPTURE_LIMIT + timedelta(microseconds=1)
            )

    def test_oversized_marker_or_record_is_rejected_before_json_parsing(self):
        marker, record = self.fixture()
        self.validate(marker, record)
        self.record_path.write_bytes(b" " * (attestation.MAX_EVIDENCE_BYTES + 1))
        with self.assertRaisesRegex(ValueError, "receipt evidence record.*oversized"):
            self.validate_files()
        self.marker_path.write_bytes(b" " * (attestation.MAX_EVIDENCE_BYTES + 1))
        with self.assertRaisesRegex(ValueError, "on-device receipt marker.*oversized"):
            self.validate_files()

    def test_failed_or_running_marker_including_cleanup_failure_is_rejected(self):
        for status, phase, failure_type in (
            ("failed", "body", None),
            ("failed", "cleanup", None),
            ("failed", "cleanup", "cleanup"),
            ("running", "body", None),
        ):
            with self.subTest(status=status, phase=phase):
                marker, record = self.fixture()
                marker["status"] = status
                marker["phase"] = phase
                if failure_type:
                    marker["failureType"] = failure_type
                rejection = "unexpected fields" if failure_type else "did not pass after cleanup"
                with self.assertRaisesRegex(ValueError, rejection):
                    self.validate(marker, record)
        marker, record = self.fixture()
        record["result"] = "failed"
        with self.assertRaisesRegex(ValueError, "identity or result"):
            self.validate(marker, record)

    def test_wrong_commit_target_platform_scenarios_or_hash_fails(self):
        changes = (
            lambda m, r: m.update(testedCommit="c" * 40),
            lambda m, r: r.update(testedCommit="c" * 40),
            lambda m, r: m.update(target="integration_test/mobile_smoke_test.dart"),
            lambda m, r: r["application"].update(target="integration_test/mobile_smoke_test.dart"),
            lambda m, r: m.update(platform="ios"),
            lambda m, r: r.update(platform={"kind": "ios-simulator"}),
            lambda m, r: m["completedScenarios"].pop(),
            lambda m, r: r["scenarios"].pop(),
            lambda m, r: m.update(installedBinarySha256="f" * 64),
            lambda m, r: r["application"].update(binarySha256="f" * 64),
        )
        for change in changes:
            with self.subTest(change=change):
                marker, record = self.fixture()
                change(marker, record)
                with self.assertRaises(ValueError):
                    self.validate(marker, record, platform="android")

    def test_altered_host_binary_or_runner_executable_is_rejected(self):
        marker, record = self.fixture()
        self.built_path.write_bytes(b"another target")
        with self.assertRaisesRegex(ValueError, "hashes differ"):
            self.validate(marker, record)
        marker, record = self.fixture("ios")
        self.built_path = self.directory / "build/ios/iphoneos/Runner.app/Runner"
        self.built_path.write_bytes(b"target-specific profile AOT bytes")
        with self.assertRaisesRegex(ValueError, "profile target build artifact"):
            self.validate(marker, record, platform="ios")

    def test_probe_or_decorative_fields_cannot_qualify(self):
        marker, record = self.fixture()
        with self.assertRaisesRegex(ValueError, "dedicated receipt"):
            self.validate(
                marker, record, target="integration_test/receipt_binary_probe_test.dart"
            )
        marker["accessToken"] = "never allowed"
        with self.assertRaisesRegex(ValueError, "unexpected fields"):
            self.validate(marker, record)
        marker, record = self.fixture()
        marker = {
            "kind": "nonqualifying_installed_binary_probe",
            "platform": "android",
            "sha256": marker["installedBinarySha256"],
        }
        with self.assertRaisesRegex(ValueError, "missing or unexpected fields"):
            self.validate(marker, record, platform="android")

    def test_capture_requires_clean_exact_checkout_and_its_build_path(self):
        self.fixture()
        root = str(self.directory)
        expected_path = self.directory.joinpath(
            "sdks/dart/example", *attestation.BUILD_SUFFIXES["android"]
        )
        expected_path.parent.mkdir(parents=True)
        expected_path.write_bytes(self.built_path.read_bytes())
        target_file = self.directory / "sdks/dart/example" / TARGET
        target_file.parent.mkdir(parents=True, exist_ok=True)
        target_file.write_text("test target fixture")
        with patch.object(
            attestation.subprocess, "check_output",
            side_effect=[COMMIT, "", root, ""],
        ):
            attestation.assert_capture_checkout(COMMIT, expected_path, "android", TARGET)
        for responses in ((COMMIT, " M receipt.dart", root), ("c" * 40, "", root)):
            with self.subTest(responses=responses), patch.object(
                attestation.subprocess, "check_output", side_effect=responses,
            ), self.assertRaisesRegex(ValueError, "clean tested"):
                attestation.assert_capture_checkout(COMMIT, expected_path, "android", TARGET)
        with patch.object(
            attestation.subprocess, "check_output",
            side_effect=[COMMIT, "", root, ""],
        ), self.assertRaisesRegex(ValueError, "this checkout"):
            attestation.assert_capture_checkout(COMMIT, self.built_path, "android", TARGET)
        target_file.unlink()
        with patch.object(
            attestation.subprocess, "check_output",
            side_effect=[COMMIT, "", root],
        ), self.assertRaisesRegex(ValueError, "target is missing"):
            attestation.assert_capture_checkout(COMMIT, expected_path, "android", TARGET)

    def test_device_capture_requires_hardware_and_reads_android_app_container(self):
        marker = b'{"kind":"physical_receipt_attestation"}'
        with patch.object(
            attestation, "_bounded_command_stdout", side_effect=[b"0\n", marker],
        ) as capture:
            self.assertEqual(attestation.capture_device_marker("android"), marker)
            self.assertEqual(
                capture.call_args_list[0].args[0],
                ["adb", "-d", "shell", "getprop", "ro.kernel.qemu"],
            )
            self.assertIn("run-as", capture.call_args_list[1].args[0])
        with patch.object(
            attestation, "_bounded_command_stdout", return_value=b"1\n",
        ) as capture, self.assertRaisesRegex(ValueError, "physical hardware"):
            attestation.capture_device_marker("android")
        capture.assert_called_once()
        with patch.object(
            attestation, "_bounded_command_stdout",
            side_effect=[b"0\n", b"x" * (attestation.MAX_EVIDENCE_BYTES + 1)],
        ), self.assertRaisesRegex(ValueError, "oversized"):
            attestation.capture_device_marker("android")

    def test_device_stdout_is_bounded_and_timeout_reaps_the_child(self):
        command = [sys.executable, "-c", "import sys; print('ready')"]
        self.assertEqual(
            attestation._bounded_command_stdout(
                command, timeout=5, limit=attestation.MAX_EVIDENCE_BYTES,
                label="on-device receipt marker",
            ),
            b"ready\n",
        )
        oversized = [
            sys.executable, "-c",
            "import sys; sys.stdout.buffer.write(b'x' * 65537); sys.stdout.flush()",
        ]
        with self.assertRaisesRegex(ValueError, "oversized"):
            attestation._bounded_command_stdout(
                oversized, timeout=5, limit=attestation.MAX_EVIDENCE_BYTES,
                label="on-device receipt marker",
            )
        with self.assertRaises(subprocess.CalledProcessError):
            attestation._bounded_command_stdout(
                [sys.executable, "-c", "import sys; sys.exit(2)"],
                timeout=5, limit=attestation.MAX_DEVICE_PROBE_BYTES,
                label="Android device probe",
            )
        stalled = [sys.executable, "-c", "import time; time.sleep(3)"]
        with self.assertRaises(subprocess.TimeoutExpired):
            attestation._bounded_command_stdout(
                stalled, timeout=0.1, limit=attestation.MAX_DEVICE_PROBE_BYTES,
                label="Android device probe",
            )

    def test_ios_capture_copies_marker_from_app_container_only(self):
        marker = b'{"kind":"physical_receipt_attestation"}'

        def copy_from_device(command, **kwargs):
            self.assertEqual(command[:5], ["xcrun", "devicectl", "device", "copy", "from"])
            self.assertEqual(command[command.index("--device") + 1], "private-device")
            self.assertEqual(
                command[command.index("--source") + 1],
                "tmp/lantern-receipt-attestation.json",
            )
            Path(command[command.index("--destination") + 1]).write_bytes(marker)
            return subprocess.CompletedProcess(command, 0, stdout=b"")

        with patch.object(attestation.subprocess, "run", side_effect=copy_from_device):
            self.assertEqual(
                attestation.capture_device_marker("ios", "private-device"), marker
            )
        marker = b"x" * (attestation.MAX_EVIDENCE_BYTES + 1)
        with patch.object(
            attestation.subprocess, "run", side_effect=copy_from_device,
        ), self.assertRaisesRegex(ValueError, "oversized"):
            attestation.capture_device_marker("ios", "private-device")
        with self.assertRaisesRegex(ValueError, "private physical device ID"):
            attestation.capture_device_marker("ios")
        with patch.object(
            attestation.subprocess, "run",
            side_effect=subprocess.CalledProcessError(1, ["xcrun", "private-device"]),
        ), self.assertRaisesRegex(ValueError, "copy failed") as failure:
            attestation.capture_device_marker("ios", "private-device")
        self.assertNotIn("private-device", str(failure.exception))

    def test_capture_cli_rejects_decorative_host_marker(self):
        marker, record = self.fixture()
        self.marker_path.write_text(json.dumps(marker))
        self.record_path.write_text(json.dumps(record))
        arguments = [
            "physical_receipt_attestation.py",
            "--marker", str(self.marker_path),
            "--record", str(self.record_path),
            "--built-binary", str(self.built_path),
            "--tested-commit", COMMIT,
            "--target", TARGET,
            "--platform", "android",
            "--run-id", RUN_ID,
            "--run-started-at", utc(self.run_start),
            "--scenario", "receipt_applied",
            "--scenario", "receipt_replayed",
        ]
        with patch.object(sys, "argv", arguments), patch.object(
            attestation, "assert_capture_checkout"
        ), patch.object(attestation, "capture_device_marker", return_value=b"decorative"):
            with redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as rejected:
                attestation.main()
        self.assertEqual(rejected.exception.code, 2)

        with patch.object(sys, "argv", arguments), patch.object(
            attestation, "assert_capture_checkout"
        ), patch.object(
            attestation, "capture_device_marker",
            return_value=self.marker_path.read_bytes(),
        ), redirect_stdout(io.StringIO()):
            attestation.main()

        self.marker_path.write_bytes(b"x" * (attestation.MAX_EVIDENCE_BYTES + 1))
        with patch.object(sys, "argv", arguments), patch.object(
            attestation, "assert_capture_checkout",
        ), patch.object(attestation, "capture_device_marker") as capture:
            with redirect_stderr(io.StringIO()), self.assertRaises(SystemExit) as rejected:
                attestation.main()
            capture.assert_not_called()
        self.assertEqual(rejected.exception.code, 2)

    def test_physical_restart_pair_requires_exact_installed_bytes_on_both_phones(self):
        for platform in ("android", "ios"):
            with self.subTest(platform=platform):
                marker, record, scenarios = self.final_fixture(platform)
                digest = self.validate(
                    marker, record,
                    target=attestation.RECEIPT_TARGET,
                    required_scenarios=scenarios,
                    platform=platform,
                )
                self.assertEqual(digest, marker["installedBinarySha256"])
                self.assertEqual(
                    attestation.validate_archived_receipt_evidence(
                        self.marker_path, self.record_path, tested_commit=COMMIT,
                        platform=platform, required_scenarios=scenarios, now=self.now,
                    ),
                    digest,
                )

    def test_physical_release_rejects_decorative_or_foreign_restart(self):
        for change in (
            lambda m, r: m.update(schema=1),
            lambda m, r: m.pop("restart"),
            lambda m, r: m["restart"].update(processChanged=False),
            lambda m, r: m["restart"].update(resumedAt=m["restart"]["preparedAt"]),
            lambda m, r: m.update(status="running", phase="awaiting_sigkill"),
            lambda m, r: m["completedScenarios"].remove("receipt_real_sigkill_sqlite_reopen"),
            lambda m, r: r["scenarios"].remove("receipt_untrusted_tls_rejected"),
            lambda m, r: r["network"].update(platformTrustedTls=False),
            lambda m, r: r["network"].update(fault="synthetic-response"),
            lambda m, r: m.update(testedCommit="f" * 40),
            lambda m, r: r.update(testedCommit="f" * 40),
            lambda m, r: m.update(platform="ios"),
            lambda m, r: r.update(platform={"kind": "physical-ios"}),
            lambda m, r: m.update(target=TARGET),
            lambda m, r: r["application"].update(binarySha256="f" * 64),
            lambda m, r: m.update(runId="c" * 32),
            lambda m, r: r["application"].update(target=TARGET),
        ):
            with self.subTest(change=change):
                marker, record, scenarios = self.final_fixture()
                change(marker, record)
                with self.assertRaises(ValueError):
                    self.validate(
                        marker, record,
                        target=attestation.RECEIPT_TARGET,
                        required_scenarios=scenarios,
                    )

    def test_archive_rejects_marker_replay_or_duplicate_json_fields(self):
        marker, record, scenarios = self.final_fixture()
        self.validate(
            marker, record, target=attestation.RECEIPT_TARGET,
            required_scenarios=scenarios,
        )
        self.marker_path.write_text(
            self.marker_path.read_text().replace(
                '"processChanged": true',
                '"processChanged": false, "processChanged": true', 1,
            )
        )
        with self.assertRaisesRegex(ValueError, "duplicate receipt JSON field"):
            attestation.validate_archived_receipt_evidence(
                self.marker_path, self.record_path, tested_commit=COMMIT,
                platform="android", required_scenarios=scenarios, now=self.now,
            )
        self.marker_path.write_text(json.dumps(marker))
        record["network"]["topology"] = "https://private.example"
        with self.assertRaises(ValueError):
            self.validate(
                marker, record, target=attestation.RECEIPT_TARGET,
                required_scenarios=scenarios,
            )

    def test_archived_receipt_is_not_a_capture_time_bypass(self):
        marker, record, scenarios = self.final_fixture()
        age = timedelta(days=5)
        marker["startedAt"] = utc(self.started - age)
        marker["finishedAt"] = utc(self.finished - age)
        marker["restart"]["preparedAt"] = utc(self.started + timedelta(seconds=30) - age)
        marker["restart"]["resumedAt"] = utc(self.started + timedelta(seconds=60) - age)
        record["runStartedAt"] = utc(self.run_start - age)
        record["recordedAt"] = utc(self.recorded - age)
        self.marker_path.write_text(json.dumps(marker))
        self.record_path.write_text(json.dumps(record))
        self.assertEqual(
            attestation.validate_archived_receipt_evidence(
                self.marker_path, self.record_path, tested_commit=COMMIT,
                platform="android", required_scenarios=scenarios, now=self.now,
            ),
            marker["installedBinarySha256"],
        )
        with self.assertRaisesRegex(ValueError, "stale for capture-time"):
            self.validate_files(
                target=attestation.RECEIPT_TARGET,
                required_scenarios=scenarios,
                run_started_at=record["runStartedAt"],
            )


if __name__ == "__main__":
    unittest.main()
