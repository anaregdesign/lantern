"""Quickstart example mirroring sdks/go/example/main.go.

Run a Lantern server first (e.g. ``go run ./server/cmd``) and then::

    uv run examples/quickstart.py
"""

from __future__ import annotations

import datetime as dt

from lantern_client import EdgeInput, Lantern, Optimization, VertexInput


def main() -> None:
    with Lantern.connect("127.0.0.1:6380") as c:
        # Single writes with mixed types.
        c.put_vertex("user:alice", "Alice")
        c.put_vertex("user:bob", "Bob")
        c.put_vertex("session:42", b"\xde\xad\xbe\xef", ttl_seconds=60)
        c.put_vertex("last-seen:alice", dt.datetime.now(dt.timezone.utc))

        # Batch write with auto-chunking.
        c.put_vertices([VertexInput(key=f"item:{i}", value=i) for i in range(100)])

        # Edges: add is additive (accumulates weight), put overwrites.
        c.add_edge("user:alice", "user:bob", 1.0)
        c.add_edge("user:alice", "user:bob", 0.5)  # weight is now 1.5
        c.put_edges(
            [EdgeInput(tail=f"item:{i}", head=f"item:{i + 1}", weight=1.0) for i in range(99)]
        )

        # Reads.
        alice = c.get_vertex("user:alice")
        print(f"alice = {alice.value!r} (kind={alice.kind.name})")

        # Subgraph traversal.
        g = c.illuminate(
            "user:alice",
            step=2,
            k=5,
            optimization=Optimization.SHORTEST_PATH_TREE,
        )
        edge_count = sum(len(e) for e in g.edges.values())
        print(f"illuminated {len(g.vertices)} vertices, {edge_count} edges")

        # Prefix scan.
        n = c.count_vertices_by_prefix("item:")
        print(f"{n} items under prefix item:")


if __name__ == "__main__":
    main()
