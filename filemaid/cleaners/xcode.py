"""Xcode DerivedData cleanup."""
import os
import shutil
import time
from pathlib import Path

DERIVED_DATA = Path.home() / "Library" / "Developer" / "Xcode" / "DerivedData"


def can_run() -> bool:
    return shutil.which("xcodebuild") is not None or DERIVED_DATA.exists()


def _remove(path: Path, dry_run: bool) -> int:
    if dry_run:
        total = 0
        for root, _, files in os.walk(path):
            for f in files:
                try:
                    total += os.path.getsize(Path(root) / f)
                except OSError:
                    pass
        return total
    total = 0
    for item in path.iterdir():
        try:
            if item.is_file():
                total += item.stat().st_size
                item.unlink()
            elif item.is_dir():
                total += _dir_size(item)
                shutil.rmtree(item)
        except OSError:
            pass
    return total


def _dir_size(path: Path) -> int:
    total = 0
    for root, _, files in os.walk(path):
        for f in files:
            try:
                total += os.path.getsize(Path(root) / f)
            except OSError:
                pass
    return total


def run(dry_run: bool, config: dict) -> str:
    if not DERIVED_DATA.exists():
        return "xcode: DerivedData does not exist"

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

    mb = removed / (1024 * 1024)
    prefix = "would " if dry_run else ""
    return f"xcode: {prefix}{action} (~{mb:.1f} MB)"
