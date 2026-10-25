#!/bin/sh
# Explicit first-use action for THESE three new project volumes only.
# Never called by normal startup, and never repairs or clears an existing volume.
set -eu
case "$LANTERN_RUN_UID:$LANTERN_RUN_GID" in *[!0-9:]*|:*) exit 1;; esac
[ "$LANTERN_RUN_UID" -gt 0 ] && [ "$LANTERN_RUN_GID" -gt 0 ]
for state in /state-0 /state-1 /state-2; do
    [ -d "$state" ] && [ ! -L "$state" ]
    [ -z "$(find "$state" -mindepth 1 -maxdepth 1 -print -quit)" ] || {
        echo 'state preparation refused: retained or interrupted volume' >&2
        exit 1
    }
done
for state in /state-0 /state-1 /state-2; do
    chown "$LANTERN_RUN_UID:$LANTERN_RUN_GID" "$state"
    chmod 700 "$state"
    (umask 077; printf 'lantern-oidc-ha-owned-v1\n' > "$state/.example-owner")
    chown "$LANTERN_RUN_UID:$LANTERN_RUN_GID" "$state/.example-owner"
done
