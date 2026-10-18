#!/usr/bin/env python3
"""Run native Rust wire coverage for the local Server's supported profile."""

import platform
import subprocess


# These root-provisioned cases have their own mandatory shared F/Q owner.
ROOT_FIXTURES = (
    "scoped_changes::tests::real_public_scoped_wire",
    "security::tests::real_public_current_wire",
)

# Windows SDK support does not grant a Windows native OIDC Server profile.
# Keep every ordinary OFF/TLS wire case, including explicit OFF+mTLS; the same
# seven authenticated cases remain compiled everywhere and run on Linux/macOS.
LOCAL_OIDC = (
    "crud::tests::real_wire_one_deadline_covers_all_authenticated_chunks",
    "scans::tests::real_wire_stream_is_lazy_page_bounded_and_cancels_on_drop",
    "transport::tests::real_wire_authenticated_ha_resumes_on_another_responder_and_backs_up",
    "transport::tests::real_wire_authenticated_https_and_search_details",
    "transport::tests::real_wire_authenticated_streams_have_independent_budgets_and_no_retry",
    "transport::tests::real_wire_mtls_and_bearer_rotation",
    "transport::tests::real_wire_public_cdc_and_receipt_status_cover_all_mutation_families",
)


def command(system):
    if system not in ("Linux", "Darwin", "Windows"):
        raise ValueError("unsupported native wire platform")
    result = ["cargo", "test", "--locked", "--all-features", "--", "--ignored", "--test-threads=1"]
    for name in ROOT_FIXTURES + (LOCAL_OIDC if system == "Windows" else ()):
        result.extend(("--skip", name))
    return result


if __name__ == "__main__":
    system = platform.system()
    print("native_wire_local_oidc=" + ("unsupported" if system == "Windows" else "required"), flush=True)
    raise SystemExit(subprocess.run(command(system), check=False).returncode)
