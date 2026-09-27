"""Fail-closed crates.io release checks shared by tag preflight and verification."""

CRATE_NAME = "lantern-client"
REPOSITORY = "https://github.com/anaregdesign/lantern"


class FirstPublicationRequired(ValueError):
    """The crate does not exist yet, so its owner must publish it manually."""


def release_requires_publish(
    crate: dict | None,
    versions_page: dict | None,
    owners_page: dict | None,
    release: dict | None,
    *,
    version: str,
    checksum: str,
    owner: str,
    sha: str,
    run_id: str,
) -> bool:
    """Return whether OIDC must publish; verify an existing release otherwise."""
    if not owner or owner != owner.strip():
        raise ValueError("set LANTERN_RUST_CRATES_IO_OWNER to the exact crates.io owner login")
    if crate is None:
        if release is not None:
            raise ValueError("version exists but the crate lookup returned 404")
        raise FirstPublicationRequired(
            f"{CRATE_NAME} {version} is the owner-held first publication"
        )
    if not isinstance(crate, dict) or not isinstance(crate.get("crate"), dict):
        raise ValueError("invalid crates.io crate response")
    info = crate["crate"]
    if (info.get("id"), info.get("name"), info.get("repository")) != (
        CRATE_NAME,
        CRATE_NAME,
        REPOSITORY,
    ):
        raise ValueError("crate identity or repository does not match the tagged source")

    if not isinstance(owners_page, dict) or not isinstance(owners_page.get("users"), list):
        raise ValueError("invalid crates.io owners response")
    if not any(
        isinstance(entry, dict)
        and entry.get("login") == owner
        and entry.get("kind") in ("user", "team")
        for entry in owners_page["users"]
    ):
        raise ValueError(f"configured crates.io owner {owner!r} does not own {CRATE_NAME}")

    if not isinstance(versions_page, dict):
        raise ValueError("missing crates.io version history")
    versions = versions_page.get("versions")
    meta = versions_page.get("meta")
    if (
        not isinstance(versions, list)
        or not isinstance(meta, dict)
        or type(meta.get("total")) is not int
        or meta["total"] != len(versions)
        or not versions
        or "next_page" not in meta
        or meta["next_page"] is not None
    ):
        raise ValueError("incomplete crates.io version history; cannot determine first publication")

    by_version = {}
    for entry in versions:
        if (
            not isinstance(entry, dict)
            or entry.get("crate") != CRATE_NAME
            or not isinstance(entry.get("num"), str)
            or not entry["num"]
            or type(entry.get("yanked")) is not bool
            or not isinstance(entry.get("checksum"), str)
            or "trustpub_data" not in entry
            or entry["num"] in by_version
        ):
            raise ValueError("invalid or duplicate crates.io version history entry")
        by_version[entry["num"]] = entry

    entry = by_version.get(version)
    if release is None:
        if entry is not None:
            raise ValueError("version lookup disagrees with the complete version history")
        return True
    if not isinstance(release, dict) or not isinstance(release.get("version"), dict):
        raise ValueError("invalid crates.io version response")
    published = release["version"]
    if entry is None or (published.get("crate"), published.get("num")) != (
        CRATE_NAME,
        version,
    ):
        raise ValueError("published version is absent from the complete version history")
    if published.get("yanked") is not False or entry["yanked"] is not False:
        raise ValueError("published version is yanked")
    if published.get("checksum") != checksum or entry["checksum"] != checksum:
        raise ValueError("existing registry archive differs from the tagged candidate")
    if "trustpub_data" not in published or published["trustpub_data"] != entry["trustpub_data"]:
        raise ValueError("registry endpoints disagree about publication provenance")

    provenance = published["trustpub_data"]
    if len(versions) == 1:
        if provenance is not None:
            raise ValueError("first and only publication must be owner-held")
    elif (
        not isinstance(provenance, dict)
        or provenance.get("provider") != "github"
        or provenance.get("repository") != "anaregdesign/lantern"
        or provenance.get("sha") != sha
        or str(provenance.get("run_id")) != run_id
    ):
        raise ValueError("later publication lacks exact-tag GitHub OIDC provenance")
    return False
