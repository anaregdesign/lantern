"""Error types raised by the Lantern Python SDK.

The SDK maps gRPC status codes to typed Python exceptions so callers can
branch on category without parsing status messages:

* :class:`NotFoundError` ← ``codes.NOT_FOUND``
* :class:`InvalidArgumentError` ← ``codes.INVALID_ARGUMENT``
* :class:`ResourceExhaustedError` ← ``codes.RESOURCE_EXHAUSTED``

All three subclass :class:`LanternError` for catch-all handling, and they
preserve the underlying :class:`grpc.RpcError` via ``self.cause`` so callers
can inspect the full gRPC status detail when needed.

:class:`BatchError` is raised by batch helpers (``put_vertices``, ``add_edges``,
``put_edges``, ``delete_vertices``, ``delete_edges``) on partial-write failure;
its ``written`` field reports how many entries from the input were already
committed before the failing chunk, mirroring the Go SDK's
``BatchError.Written`` so callers can resume with ``inputs[err.written:]``.
"""

from __future__ import annotations

import grpc


class LanternError(Exception):
    """Base class for typed errors raised by the SDK."""

    def __init__(self, message: str, *, cause: BaseException | None = None) -> None:
        super().__init__(message)
        self.cause = cause

    def __str__(self) -> str:  # pragma: no cover - trivial
        base = super().__str__()
        if self.cause is not None and not isinstance(self.cause, LanternError):
            return f"{base} ({type(self.cause).__name__}: {self.cause})"
        return base


class NotFoundError(LanternError):
    """Raised by ``get_vertex`` / ``get_edge`` when the key does not exist.

    A present vertex whose value is the explicit nil tombstone is NOT a
    NotFoundError — it is returned as a :class:`~lantern_client.Vertex` whose
    ``kind`` is :attr:`~lantern_client.VertexKind.NIL`.
    """


class InvalidArgumentError(LanternError):
    """Raised on caller-fixable input errors (empty key, oversized batch, ...).

    The server's ValidationInterceptor surfaces these as ``INVALID_ARGUMENT``;
    they are not retried.
    """


class ResourceExhaustedError(LanternError):
    """Raised on rate-limit and server-side back-pressure failures.

    Callers should back off before retrying.
    """


class OverflowError(LanternError):
    """Raised when a value cannot be represented in the requested Python type.

    For example, reading a ``Uint64`` vertex above ``2**63 - 1`` as a signed
    ``int`` via the strict accessor.
    """


class BatchError(LanternError):
    """Raised by batch helpers on partial-write failure.

    ``written`` is the number of inputs from the original sequence that were
    already committed by chunks 0..N-1 before chunk N failed. Resume safely
    with ``inputs[err.written:]``. For idempotent operations (``put_vertices``,
    ``put_edges``, ``delete_vertices``, ``delete_edges``) a full retry from
    index 0 is also safe; for ``add_edges`` it is NOT (the additive prefix
    would be double-counted).
    """

    def __init__(self, written: int, cause: BaseException) -> None:
        super().__init__(
            f"batch write failed after {written} items committed: {cause}",
            cause=cause,
        )
        self.written = written


def _wrap_rpc_error(err: grpc.RpcError) -> LanternError:
    """Translate a gRPC error into the matching typed LanternError subclass."""
    code = err.code() if hasattr(err, "code") else None
    msg = err.details() if hasattr(err, "details") else str(err)
    if code == grpc.StatusCode.NOT_FOUND:
        return NotFoundError(msg or "not found", cause=err)
    if code == grpc.StatusCode.INVALID_ARGUMENT:
        return InvalidArgumentError(msg or "invalid argument", cause=err)
    if code == grpc.StatusCode.RESOURCE_EXHAUSTED:
        return ResourceExhaustedError(msg or "resource exhausted", cause=err)
    return LanternError(f"gRPC {code}: {msg}" if code else str(err), cause=err)
