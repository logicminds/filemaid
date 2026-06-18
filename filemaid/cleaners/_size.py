"""Disk-size helpers for dev cleaners."""
import os
from pathlib import Path


def dir_size(path: Path) -> int:
    """Return total byte size of a directory tree, skipping files we cannot stat."""
    total = 0
    for root, _, files in os.walk(path, followlinks=False):
        for f in files:
            try:
                total += os.path.getsize(Path(root) / f)
            except OSError:
                pass
    return total


def humanize(size: int) -> str:
    """Format bytes as human-readable units."""
    if size < 1024:
        return f"{size} B"
    for unit in ("KB", "MB", "GB", "TB"):
        size /= 1024
        if size < 1024:
            return f"{size:.1f} {unit}"
    return f"{size:.1f} PB"
