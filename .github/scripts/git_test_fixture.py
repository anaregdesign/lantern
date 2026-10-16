"""Keep automatic Git housekeeping within temporary fixture command lifetimes."""

from pathlib import Path
import subprocess


def synchronous_git_maintenance(root: Path) -> None:
    # Waiting for `git commit` does not join detached automatic maintenance.
    # Keep housekeeping enabled, but make teardown wait for its writers (#1788).
    # The gc setting also covers Git versions predating maintenance.autoDetach.
    for key in ("maintenance.autoDetach", "gc.autoDetach"):
        subprocess.run(["git", "config", "--local", key, "false"], cwd=root, check=True)
