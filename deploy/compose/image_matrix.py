#!/usr/bin/env python3
"""Read-only image provenance preflight; never pulls/builds/starts containers.

Run on the exact selected checkout before explicit initialization and ordinary
startup. Local image IDs qualify source builds only; RepoDigests identify pulled
artifacts and need the independent publication/actual-stack qualification.
"""
import argparse
import json
from pathlib import Path
import re
import subprocess


def inspect_images(source, images, inspect):
    if not re.fullmatch(r"[0-9a-f]{40}", source):
        raise ValueError("exact source commit required")
    result = {}
    for service, reference in images.items():
        if not (re.fullmatch(r"sha256:[0-9a-f]{64}", reference) or
                re.fullmatch(r"[^\s@]+@sha256:[0-9a-f]{64}", reference)):
            raise ValueError("select immutable image IDs or registry digests")
        records = inspect(reference)
        if len(records) != 1:
            raise ValueError("exactly one image required")
        image = records[0]
        if not isinstance(image, dict):
            raise ValueError("invalid image metadata")
        config = image.get("Config") or {}
        if not isinstance(config, dict):
            raise ValueError("invalid image configuration")
        labels = config.get("Labels") or {}
        if not isinstance(labels, dict):
            raise ValueError("invalid image labels")
        if labels.get("org.opencontainers.image.revision") != source:
            raise ValueError("image belongs to a different or unknown source")
        image_id = image.get("Id", "")
        if not re.fullmatch(r"sha256:[0-9a-f]{64}", image_id):
            raise ValueError("immutable image identity missing")
        digests = image.get("RepoDigests") or []
        result[service] = {"image_id": image_id, "repo_digests": digests,
                           "kind": "registry-artifact" if digests else "local-source-build"}
    return {"schema": 1, "source": source, "images": result,
            "qualification": "identity-only; runtime and publication acceptance remain separate"}


def selected_images(rendered, mode):
    """Bind provenance to the actual default Compose services, not spare IDs."""
    services = rendered.get("services", {})
    names = ["lantern-0", "lantern-1", "lantern-2", "admin", "lantern-mcp",
             "metrics-0", "metrics-1", "metrics-2"]
    if mode == "oidc":
        names += ["membershippublisher", "edge"]
    elif mode != "off":
        raise ValueError("supported mode required")
    images = {}
    for name in names:
        service = services.get(name)
        if not isinstance(service, dict) or service.get("profiles") or service.get("build"):
            raise ValueError("complete immutable runtime service required")
        image = service.get("image")
        if not isinstance(image, str):
            raise ValueError("runtime image missing")
        images[name] = image
    if len({images[f"lantern-{i}"] for i in range(3)}) != 1 or len({images[f"metrics-{i}"] for i in range(3)}) != 1:
        raise ValueError("homogeneous Server and metrics helper images required")
    return images


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", required=True)
    parser.add_argument("--mode", choices=("off", "oidc"), required=True)
    parser.add_argument("--env-file", type=Path, required=True,
                        help="the same private env file selected for Compose startup")
    args = parser.parse_args()
    try:
        compose = Path(__file__).with_name("docker-compose.oidc.yml" if args.mode == "oidc" else "docker-compose.yml")
        # Rendering may contain private configuration; capture it in memory and
        # expose only selected image identities in the content-free result.
        rendered = json.loads(subprocess.check_output(["docker", "compose", "--env-file", str(args.env_file),
            "-f", str(compose), "config", "--format", "json"], stderr=subprocess.DEVNULL))
        images = selected_images(rendered, args.mode)
        result = inspect_images(args.source, images,
            lambda value: json.loads(subprocess.check_output(["docker", "image", "inspect", value], stderr=subprocess.DEVNULL)))
    except (ValueError, OSError, subprocess.SubprocessError, KeyError, TypeError):
        parser.exit(1, "image matrix refused: immutable same-source images must be available locally\n")
    print(json.dumps(result, indent=2, sort_keys=True))


if __name__ == "__main__":
    main()
