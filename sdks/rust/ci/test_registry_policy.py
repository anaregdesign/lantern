"""Controlled registry responses for the Rust release decision."""

import copy
import unittest

from registry_policy import FirstPublicationRequired, release_requires_publish


NAME = "lantern-client"
SHA = "a" * 40
CHECKSUM = "b" * 64
OWNER = "example-owner"
RUN_ID = "1234"


def version_entry(version, provenance=None):
    return {
        "crate": NAME,
        "num": version,
        "checksum": CHECKSUM,
        "yanked": False,
        "trustpub_data": provenance,
    }


def registry(version, *, earlier=None, provenance=None):
    target = version_entry(version, provenance)
    versions = [target] if earlier is None else [target, version_entry(earlier)]
    return {
        "crate": {
            "crate": {
                "id": NAME,
                "name": NAME,
                "repository": "https://github.com/anaregdesign/lantern",
            }
        },
        "versions_page": {
            "versions": versions,
            "meta": {"total": len(versions), "next_page": None},
        },
        "owners_page": {"users": [{"login": OWNER, "kind": "user"}]},
        "release": {"version": copy.deepcopy(target)},
    }


def evaluate(response, version):
    return release_requires_publish(
        **response,
        version=version,
        checksum=CHECKSUM,
        owner=OWNER,
        sha=SHA,
        run_id=RUN_ID,
    )


class RegistryPolicyTests(unittest.TestCase):
    def test_unregistered_crate_requires_owner_held_first_publish(self):
        with self.assertRaisesRegex(FirstPublicationRequired, "owner-held first publication"):
            evaluate(
                {"crate": None, "versions_page": None, "owners_page": None, "release": None},
                "0.1.1",
            )

    def test_first_publish_accepts_owner_held_0_1_0_and_0_1_1(self):
        for version in ("0.1.0", "0.1.1"):
            with self.subTest(version=version):
                self.assertFalse(evaluate(registry(version), version))

    def test_later_version_requires_exact_oidc_provenance(self):
        provenance = {
            "provider": "github",
            "repository": "anaregdesign/lantern",
            "sha": SHA,
            "run_id": 1234,
        }
        self.assertFalse(
            evaluate(registry("0.1.2", earlier="0.1.1", provenance=provenance), "0.1.2")
        )
        response = registry("0.1.2", earlier="0.1.1")
        with self.assertRaisesRegex(ValueError, "later publication lacks exact-tag"):
            evaluate(response, "0.1.2")
        response = registry("0.1.2", earlier="0.1.1", provenance=provenance)
        response["release"]["version"]["trustpub_data"]["sha"] = "different"
        with self.assertRaisesRegex(ValueError, "disagree about publication provenance"):
            evaluate(response, "0.1.2")

    def test_missing_later_version_requires_oidc_publish(self):
        response = registry("0.1.1")
        response["release"] = None
        self.assertTrue(evaluate(response, "0.1.2"))

    def test_inconsistent_or_incomplete_history_fails_closed(self):
        response = registry("0.1.1")
        response["versions_page"]["meta"]["total"] = 2
        with self.assertRaisesRegex(ValueError, "incomplete crates.io version history"):
            evaluate(response, "0.1.1")
        response = registry("0.1.1")
        response["versions_page"]["meta"]["next_page"] = "/next"
        with self.assertRaisesRegex(ValueError, "incomplete crates.io version history"):
            evaluate(response, "0.1.1")
        response = registry("0.1.1")
        response["release"] = None
        with self.assertRaisesRegex(ValueError, "version lookup disagrees"):
            evaluate(response, "0.1.1")

    def test_yank_owner_checksum_and_identity_fail_closed(self):
        for field, value, message in (
            ("yanked", True, "yanked"),
            ("checksum", "wrong", "archive differs"),
        ):
            with self.subTest(field=field):
                response = registry("0.1.1")
                response["release"]["version"][field] = value
                with self.assertRaisesRegex(ValueError, message):
                    evaluate(response, "0.1.1")
        response = registry("0.1.1")
        response["owners_page"]["users"] = []
        with self.assertRaisesRegex(ValueError, "does not own"):
            evaluate(response, "0.1.1")
        response = registry("0.1.1")
        response["crate"]["crate"]["repository"] = "https://example.invalid"
        with self.assertRaisesRegex(ValueError, "identity or repository"):
            evaluate(response, "0.1.1")


if __name__ == "__main__":
    unittest.main()
