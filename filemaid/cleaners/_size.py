"""Disk-size helpers for dev cleaners."""
from __future__ import annotations

import os
import re
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


def parse_size(text: str) -> int | None:
    """Parse a human-readable size string (e.g. '1.2 GB', '0B') into bytes."""
    text = text.strip().replace(" ", "").upper()
    if not text:
        return None
    match = re.match(r"^([0-9]*\.?[0-9]+)\s*([A-Z]*)$", text)
    if not match:
        return None
    value = float(match.group(1))
    unit = match.group(2) or "B"
    units = {
        "B": 1,
        "KB": 1024,
        "MB": 1024 ** 2,
        "GB": 1024 ** 3,
        "TB": 1024 ** 4,
        "PB": 1024 ** 5,
    }
    factor = units.get(unit, 1)
    return int(value * factor)
