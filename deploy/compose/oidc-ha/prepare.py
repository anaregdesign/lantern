#!/usr/bin/env python3
"""Create NEW private placeholder inputs and stable data identities, never trust/state.

This performs no network, provider, Docker or credential/access operation. It
refuses an existing directory; use supplied JSON unchanged on every lifecycle.
"""
from __future__ import annotations

import argparse
import json
import os
from pathlib import Path
import secrets


def write_private(path: Path, data: str) -> None:
    with path.open("x", encoding="utf-8") as out:
        os.chmod(path, 0o600)
        out.write(data)


def prepare(directory: Path) -> None:
    if not directory.is_absolute() or directory != directory.resolve(strict=False):
        raise ValueError("select a clean absolute new private directory")
    if any(c in str(directory) for c in "\n\r\x00'$\\"):
        raise ValueError("select a private path representable as a literal Compose env value")
    if directory.exists() or directory.is_symlink():
        raise ValueError("private directory exists; retain it and never regenerate identities")
    if os.getuid() == 0 or os.getgid() == 0:
        raise ValueError("use a dedicated non-root owner for the example")
    directory.mkdir(mode=0o700)
    # These are mount PARENTS. The create-only generator alone creates the
    # final /material/identity and /operator/custody leaves, never this helper.
    names = ["material", "operator", "config", "tls", "tls/trust", "tls/edge", "tls/metrics", "tls/metrics/client"]
    names += [f"config/lantern-{i}" for i in range(3)]
    names += [f"tls/metrics/lantern-{i}" for i in range(3)]
    for name in names:
        (directory / name).mkdir(mode=0o700)
    shared_epoch = secrets.token_hex(16)
    templates = Path(__file__).with_name("templates")
    for i in range(3):
        config = json.loads((templates / f"lantern-{i}.json.example").read_text())
        write_private(directory / f"config/lantern-{i}/startup.json", json.dumps(config, indent=2) + "\n")
        write_private(directory / f"config/lantern-{i}/receipt.env", f"LANTERN_NODE_ID={secrets.token_hex(16)}\nLANTERN_RECEIPT_EPOCH={shared_epoch}\n")
    write_private(directory / "mcp.env", "# Inject an explicitly enrolled, scoped API JWT accepted by this Server.\nLANTERN_TOKEN=REPLACE_ACCEPTED_SCOPED_API_JWT\n")
    write_private(directory / "compose.env", f"LANTERN_HA_PRIVATE_DIRECTORY='{directory}'\nLANTERN_RUN_UID={os.getuid()}\nLANTERN_RUN_GID={os.getgid()}\nLANTERN_EDGE_PORT=8443\nLANTERN_MCP_PORT=6390\nLANTERN_PROMETHEUS_PORT=9091\n")
    for name in ["mcp-role.change.json.example", "operations-role.change.json.example", "service-principal.changes.json.example"]:
        write_private(directory / name, (templates / name).read_text())
    write_private(directory / "TLS-REQUIRED.txt", """Replace placeholders privately before any container starts.
edge/server.pem + server-key.pem: serverAuth leaf for localhost AND edge; ca.pem pins node public TLS CA.
trust/ca.pem: same node public TLS CA, public verification material only.
Node public TLS certificate/key PEM go in that node's supplied startup.json; SAN must include lantern-N.
metrics/lantern-N/server.pem + server-key.pem: separate serverAuth-only leaf for lantern-N, ca.pem metrics CA.
metrics/client/client.pem + client-key.pem: separate clientAuth leaf for Prometheus, ca.pem metrics CA.
Metrics CA signer stays with the operator, never in any relay/Server/Prometheus serving mount.
No private CA/TLS/provider credential is generated, fetched or installed by this preparation action.
""")


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--directory", type=Path, required=True)
    args = parser.parse_args()
    try:
        prepare(args.directory)
    except (OSError, ValueError, json.JSONDecodeError):
        parser.exit(1, "preparation refused: new private inputs unavailable; preserve partial output\n")
    print("private placeholder inputs created; complete TLS/provider/grants before use")


if __name__ == "__main__":
    main()
