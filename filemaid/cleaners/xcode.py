"""Xcode DerivedData cleanup."""
import os
import shutil
import time
from pathlib import Path

from filemaid.cleaners._result import CleanupResult
from filemaid.cleaners._size import dir_size, humanize

DERIVED_DATA = Path.home() / "Library" / "Developer" / "Xcode" / "DerivedData"


def can_run() -> bool:
    return shutil.which("xcodebuild") is not None or DERIVED_DATA.exists()


def _remove(path: Path, dry_run: bool) -> int:
    if dry_run:
        return dir_size(path)
    total = 0
    for item in path.iterdir():
        try:
            if item.is_file():
                total += item.stat().st_size
                item.unlink()
            elif item.is_dir():
                total += dir_size(item)
                shutil.rmtree(item)
        except OSError:
            pass
    return total


def run(dry_run: bool, config: dict) -> CleanupResult:
    if not DERIVED_DATA.exists():
        return CleanupResult(
            name="xcode",
            status="skipped",
            detail="DerivedData does not exist",
        )

    mode = config.get("dev_cleanup", {}).get("xcode", {}).get("mode", "safe")
    cutoff = time.time() - (30 * 24 * 60 * 60)
    removed = 0

    if mode == "aggressive":
        removed = _remove(DERIVED_DATA, dry_run)
        action = "removed all DerivedData"
    else:
        for item in DERIVED_DATA.iterdir():
            try:
                mtime = item.stat().st_mtime
            except OSError:
                continue
            if mtime < cutoff:
                removed += _remove(item, dry_run)
        action = "removed DerivedData entries older than 30 days"

    status = "dry-run" if dry_run else "ok"
    return CleanupResult(
        name="xcode",
        status=status,
        saved=removed,
        saved_human=humanize(removed),
        detail=action,
    )
