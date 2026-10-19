"""Keep private developer examples parseable, aligned and placeholder-only."""

import hashlib
import json
from pathlib import Path
import plistlib
import re
import shlex
import subprocess
import unittest
from urllib.parse import urldefrag, urlsplit


ROOT = Path(__file__).resolve().parents[2]
EXAMPLES = ROOT / "deploy/examples/auth"
EXAMPLE_FILES = (
    "server-off.env.example", "server.env.example", "oauth-clients.env.example",
    "google-issuer.change.json.example", "apple-issuer.change.json.example",
    "flutter-android.json.example", "flutter-ios.json.example",
    "google-ios-url-types.plist.example",
)


def assignments(name):
    result = {}
    for item in shlex.split((EXAMPLES / name).read_text(), comments=True):
        key, separator, value = item.partition("=")
        if not separator or not re.fullmatch(r"[A-Z][A-Z0-9_]*", key) or key in result:
            raise ValueError("examples require unique literal shell assignments")
        result[key] = value
    return result


class PrivateAuthExamplesTest(unittest.TestCase):
    def test_formats_and_placeholder_values(self):
        constants = {
            "6380", "off", "oidc", "resume", "RS256", "Editor",
            "https://accounts.google.com", "https://oauth2.googleapis.com",
            "https://appleid.apple.com", "NATIVE_CLIENT_KIND_GOOGLE_ID_TOKEN",
            "NATIVE_CLIENT_KIND_APPLE_NATIVE", "NATIVE_CLIENT_KIND_APPLE_ANDROID_WEB",
        }

        def check(value):
            if isinstance(value, dict):
                for child in value.values():
                    check(child)
            elif isinstance(value, list):
                for child in value:
                    check(child)
            elif isinstance(value, str):
                origin = urlsplit(value)
                self.assertTrue(
                    value in constants
                    or value.startswith(("REPLACE_", "replace_", "invalid.replace.",
                                         "/REPLACE_PRIVATE_DIRECTORY/",
                                         "com.googleusercontent.apps.REPLACE_"))
                    or re.fullmatch(r"replace-[a-z-]+\.invalid", value)
                    or (origin.scheme == "https" and origin.hostname is not None
                        and re.fullmatch(r"replace-[a-z-]+\.invalid", origin.hostname)),
                    "example contains a non-placeholder selection",
                )

        for name in EXAMPLE_FILES:
            path = EXAMPLES / name
            with self.subTest(file=path.name):
                if ".json." in path.name:
                    value = json.loads(path.read_text())
                    self.assertEqual(path.read_text(), json.dumps(value, indent=2) + "\n")
                elif ".plist." in path.name:
                    value = plistlib.loads(path.read_bytes())
                else:
                    self.assertIn(".env.", path.name)
                    value = assignments(path.name)
                    if "LANTERN_OIDC_SECRET_BINDINGS" in value:
                        value["LANTERN_OIDC_SECRET_BINDINGS"] = json.loads(
                            value["LANTERN_OIDC_SECRET_BINDINGS"])
                check(value)

    def test_runtime_provider_and_platform_alignment(self):
        server = assignments("server.env.example")
        operator = assignments("oauth-clients.env.example")
        self.assertEqual(set(server), {
            "LANTERN_PORT", "LANTERN_TLS_CERT_FILE", "LANTERN_TLS_KEY_FILE",
            "LANTERN_AUTH_MODE", "LANTERN_SECURITY_CONFIG_FILE", "LANTERN_SECURITY_STORE_MODE",
            "LANTERN_OIDC_BROWSER_ORIGIN", "LANTERN_OIDC_SECRET_BINDINGS",
        })
        self.assertEqual(assignments("server-off.env.example"), {
            "LANTERN_PORT": "6380", "LANTERN_AUTH_MODE": "off",
        })
        for key in ["ANDROID_SIGNING_CERT_SHA1", "ANDROID_SIGNING_CERT_SHA256",
                    "APPLE_TEAM_ID", "APPLE_KEY_ID", "APPLE_SIGNING_KEY_FILE",
                    "APPLE_WEBSITE_DOMAIN", "GOOGLE_OAUTH_CLIENT_JSON_FILE"]:
            self.assertIn(key, operator)
        for key, value in operator.items():
            if key.endswith("_FILE"):
                self.assertTrue(value.startswith("/REPLACE_PRIVATE_DIRECTORY/"))
        bindings = json.loads(server["LANTERN_OIDC_SECRET_BINDINGS"])
        google = json.loads((EXAMPLES / "google-issuer.change.json.example").read_text())
        apple = json.loads((EXAMPLES / "apple-issuer.change.json.example").read_text())
        self.assertEqual(set(google), {"putIssuer"})
        self.assertEqual(set(apple), {"putIssuer"})
        google, apple = google["putIssuer"], apple["putIssuer"]
        self.assertFalse(google["enabled"])
        self.assertFalse(apple["enabled"])
        self.assertFalse(google["humanSubjectNamespaceQualified"])
        self.assertEqual(server["LANTERN_AUTH_MODE"], "oidc")
        self.assertEqual(server["LANTERN_SECURITY_STORE_MODE"], "resume")
        self.assertEqual(google["clientId"], operator["GOOGLE_WEB_CLIENT_ID"])
        self.assertEqual(google["apiAudience"], operator["API_AUDIENCE"])
        self.assertEqual(server["LANTERN_OIDC_BROWSER_ORIGIN"], operator["ADMIN_HTTPS_ORIGIN"])
        self.assertEqual(google["redirectUri"], operator["GOOGLE_WEB_REDIRECT_URI"])
        callback = hashlib.sha256(google["issuer"].encode()).hexdigest()
        self.assertEqual(google["redirectUri"],
                         server["LANTERN_OIDC_BROWSER_ORIGIN"] + "/auth/callback/" + callback)
        native_google, = google["nativeClients"]
        self.assertEqual(native_google["clientId"], google["clientId"])
        self.assertEqual(native_google["presenters"], sorted([
            operator["GOOGLE_ANDROID_CLIENT_ID"], operator["GOOGLE_IOS_CLIENT_ID"]]))
        android, ios = apple["nativeClients"]
        self.assertEqual(android["clientId"], operator["APPLE_SERVICES_ID"])
        self.assertEqual(android["androidPackage"], operator["ANDROID_PACKAGE"])
        self.assertEqual(android["redirectUri"], operator["APPLE_ANDROID_REDIRECT_URI"])
        self.assertEqual(android["redirectUri"],
                         android["serviceOrigin"] + "/auth/native/apple/callback/" + android["id"])
        self.assertEqual(ios["clientId"], operator["APPLE_APP_ID"])
        self.assertEqual(ios["clientId"], operator["IOS_BUNDLE_ID"])
        self.assertNotIn("redirectUri", ios)
        self.assertNotIn("clientId", apple)

        for issuer, profile, secret_file in [
            (google, google, "GOOGLE_WEB_CLIENT_SECRET_FILE"),
            (apple, android, "APPLE_ANDROID_CLIENT_SECRET_FILE"),
            (apple, ios, "APPLE_IOS_CLIENT_SECRET_FILE"),
        ]:
            binding = bindings[profile["secretRef"]]
            self.assertEqual(set(binding), {"issuer", "client_id", "token_origin", "path"})
            self.assertEqual(binding["issuer"], issuer["issuer"])
            self.assertEqual(binding["client_id"], profile["clientId"])
            self.assertEqual(binding["path"], operator[secret_file])
        for profile in [native_google, android, ios]:
            self.assertEqual(profile["serviceOrigin"], operator["SERVICE_HTTPS_ORIGIN"])

        for platform, profile in [("android", android), ("ios", ios)]:
            mobile = json.loads((EXAMPLES / f"flutter-{platform}.json.example").read_text())
            expected = {"LANTERN_ENDPOINT", "LANTERN_NATIVE_LOGIN",
                        "LANTERN_GOOGLE_PROFILE", "LANTERN_APPLE_PROFILE"}
            if platform == "ios":
                expected.add("LANTERN_GOOGLE_IOS_CLIENT_ID")
                self.assertEqual(mobile["LANTERN_GOOGLE_IOS_CLIENT_ID"],
                                 operator["GOOGLE_IOS_CLIENT_ID"])
            self.assertEqual(set(mobile), expected)
            self.assertIs(mobile["LANTERN_NATIVE_LOGIN"], True)
            self.assertEqual(mobile["LANTERN_ENDPOINT"], profile["serviceOrigin"])
            self.assertEqual(mobile["LANTERN_GOOGLE_PROFILE"], native_google["id"])
            self.assertEqual(mobile["LANTERN_APPLE_PROFILE"], profile["id"])
        plist = plistlib.loads((EXAMPLES / "google-ios-url-types.plist.example").read_bytes())
        self.assertEqual(set(plist), {"CFBundleURLTypes"})
        self.assertEqual(plist["CFBundleURLTypes"][0]["CFBundleURLSchemes"],
                         [operator["GOOGLE_IOS_REVERSED_CLIENT_ID"]])

    def test_scoped_private_ignores_preserve_examples_and_fixtures(self):
        ignored = {
            "deploy/examples/auth/private/node.json",
            "deploy/examples/auth/private/tls/server.pem",
            "deploy/examples/auth/server.local.env",
            "deploy/examples/auth/flutter-ios.local.json",
            "deploy/examples/auth/client_secret_REPLACE.json",
            "deploy/examples/auth/AuthKey_REPLACE.p8",
            "sdks/dart/example/android/app/google-services.json",
            "sdks/dart/example/ios/Runner/GoogleService-Info.plist",
            "sdks/dart/example/ios/Runner/AuthKey_REPLACE.p8",
        }
        visible = {"deploy/examples/auth/" + name for name in (*EXAMPLE_FILES, "README.md")} | {
            "sdks/dart/offline/test/fixtures/v3_snapshot_auth_pause.json",
            "sdks/dart/example/test/fixtures/google-services.json",
            "sdks/dart/example/ios/Runner/Info.plist",
            "sdks/dart/example/ios/Runner/NativeLogin.entitlements",
            "sdks/dart/example/android/app/build.gradle.kts",
        }
        result = subprocess.run(
            ["git", "check-ignore", "--no-index", "--stdin"], cwd=ROOT,
            input="\n".join(sorted(ignored | visible)) + "\n",
            text=True, capture_output=True, check=True,
        )
        self.assertEqual(set(result.stdout.splitlines()), ignored)

    def test_navigation_and_local_links(self):
        for path in ["README.md", "docs/oidc-operations.md", "docs/google-oidc-setup.md",
                     "sdks/dart/example/README.md"]:
            self.assertIn("deploy/examples/auth/README.md", (ROOT / path).read_text())
        for link in re.findall(r"\]\(([^)]+)\)", (EXAMPLES / "README.md").read_text()):
            target, _ = urldefrag(link)
            self.assertTrue((EXAMPLES / target).is_file(), "broken local example link")


if __name__ == "__main__":
    unittest.main()
