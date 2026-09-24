"""Exercise the archived README's interim Connect HTTP+JSON path.

This standalone documentation fixture uses only the Python standard library.
It is not part of the archived SDK or a maintained Python client package.
"""

from __future__ import annotations

import argparse
import json
import uuid
from urllib.error import HTTPError
from urllib.request import Request, urlopen


def call(endpoint: str, method: str, payload: dict) -> tuple[int, dict]:
    request = Request(
        f"{endpoint.rstrip('/')}/graph.v1.LanternService/{method}",
        data=json.dumps(payload).encode("utf-8"),
        headers={
            "Content-Type": "application/json",
            "Connect-Protocol-Version": "1",
        },
        method="POST",
    )
    try:
        response = urlopen(request, timeout=5)
    except HTTPError as error:
        response = error
    with response:
        return response.status, json.load(response)


def require_ok(endpoint: str, method: str, payload: dict) -> dict:
    status, body = call(endpoint, method, payload)
    if status != 200:
        raise RuntimeError(f"{method} returned HTTP {status}")
    return body


def run(endpoint: str) -> None:
    key = f"python-archive-smoke-{uuid.uuid4().hex}"
    value = "archive-smoke"
    try:
        put = require_ok(
            endpoint, "PutVertex", {"vertex": {"key": key, "string": value}}
        )
        if put.get("outcome") != "PUT_OUTCOME_APPLIED_AND_LIVE":
            raise RuntimeError("PutVertex did not report a live application")

        got = require_ok(endpoint, "GetVertex", {"key": key})
        if got.get("vertex") != {"key": key, "string": value}:
            raise RuntimeError("GetVertex did not return the exact string Vertex")

        graph = require_ok(
            endpoint,
            "Illuminate",
            {"seed": key, "bfs": {"step": 1, "fanOut": 1}},
        ).get("graph", {})
        if not any(vertex.get("key") == key for vertex in graph.get("vertices", [])):
            raise RuntimeError("Illuminate omitted its seed Vertex")

        missing_status, missing_body = call(
            endpoint, "GetVertex", {"key": f"{key}-missing"}
        )
        if missing_status != 404 or missing_body.get("code") != "not_found":
            raise RuntimeError("missing GetVertex did not return Connect not_found")
    finally:
        require_ok(endpoint, "DeleteVertex", {"key": key})

    print("CONNECT_JSON_SMOKE_PASS")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--endpoint", default="http://127.0.0.1:6380")
    run(parser.parse_args().endpoint)
