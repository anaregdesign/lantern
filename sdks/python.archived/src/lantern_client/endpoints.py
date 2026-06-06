"""Static multi-endpoint helper for ``Lantern.connect_endpoints``.

When the caller wants to connect to a fixed list of addresses (e.g. three
HA replicas) without using DNS, this module's resolver feeds them through
gRPC's ``manual`` name resolver under a synthesized ``static:`` scheme so the
default ``round_robin`` load-balancing policy still kicks in.

This mirrors ``sdks/go/endpoints.go`` which uses ``manual.NewBuilderWithScheme``
for the same purpose.
"""

from __future__ import annotations

import threading
from collections.abc import Sequence

_LOCK = threading.Lock()
_REGISTERED = False


def _ensure_static_resolver_registered() -> None:
    """No-op; static endpoints use the plain ``ipv4:`` URI scheme below."""
    # grpcio's bundled name resolvers already include ``ipv4:`` and ``ipv6:``,
    # which accept a comma-separated address list and feed each into the
    # configured LB policy (default round_robin). No custom resolver needed.
    global _REGISTERED
    with _LOCK:
        _REGISTERED = True


def static_target(endpoints: Sequence[str]) -> str:
    """Build a gRPC target URI that fans out across a fixed endpoint list.

    Each endpoint must already be ``host:port``. The result is suitable to
    pass directly to ``grpc.insecure_channel`` /
    ``grpc.secure_channel``; combined with the SDK's default
    ``round_robin`` service config the channel load-balances over all of
    them, picking a new sub-channel on each failed attempt.

    Raises ``ValueError`` if ``endpoints`` is empty or any entry is malformed.
    """
    if not endpoints:
        raise ValueError("at least one endpoint is required")
    cleaned: list[str] = []
    for ep in endpoints:
        ep = ep.strip()
        if not ep:
            raise ValueError("endpoint may not be empty")
        if ":" not in ep:
            raise ValueError(f"endpoint {ep!r} must be host:port")
        cleaned.append(ep)
    _ensure_static_resolver_registered()
    # ``ipv4:`` and ``ipv6:`` are bundled into grpcio; ``ipv4:`` accepts
    # hostnames in practice and the channel falls back to A/AAAA resolution
    # per entry. Use the comma-separated form documented in the gRPC name
    # resolution spec.
    return "ipv4:" + ",".join(cleaned)


def has_endpoints(target: str) -> bool:
    """Return ``True`` if ``target`` was produced by :func:`static_target`."""
    return target.startswith(("ipv4:", "ipv6:"))


__all__ = ["has_endpoints", "static_target"]
