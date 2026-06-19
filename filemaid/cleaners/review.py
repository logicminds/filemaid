"""Review queue cleanup."""
from __future__ import annotations

import time
from pathlib import Path

from filemaid.cleaners._result import CleanupResult
from filemaid.cleaners._size import humanize


def can_run() -> bool:
    return True


def _review_dir(config: dict) -> Path:
    return Path(config.get("review_dir", "~/.filemaid/review")).expanduser()


def run(dry_run: bool, config: dict) -> CleanupResult:
    review_dir = _review_dir(config)
    if not review_dir.exists():
        return CleanupResult(
            name="review",
            status="skipped",
            detail="review queue does not exist",
        )

    review_cfg = config.get("review_cleanup", {})
    if not review_cfg.get("enabled", True):
        return CleanupResult(
            name="review",
            status="disabled",
            detail="disabled in config",
        )

    mode = review_cfg.get("mode", "safe")
    max_age_days = review_cfg.get("max_age_days", 30)
    cutoff = (
        time.time() - (max_age_days * 24 * 60 * 60)
        if mode != "aggressive"
        else time.time()
    )

    removed_bytes = 0
    removed_files = 0
    skipped = 0

    for item in sorted(review_dir.rglob("*")):
        if not item.is_file():
            continue
        try:
            mtime = item.stat().st_mtime
        except OSError:
            skipped += 1
            continue
        if mtime < cutoff:
            try:
                size = item.stat().st_size
            except OSError:
                skipped += 1
                continue
            if dry_run:
                removed_bytes += size
                removed_files += 1
            else:
                try:
                    item.unlink()
                    removed_bytes += size
                    removed_files += 1
                except OSError:
                    skipped += 1

    if not dry_run:
        for item in sorted(review_dir.rglob("*"), reverse=True):
            if item.is_dir() and item != review_dir and not any(item.iterdir()):
                try:
                    item.rmdir()
                except OSError:
                    pass

    status = "dry-run" if dry_run else "ok"
    if removed_files == 0:
        detail = "no stale review items"
    else:
        mode_detail = (
            "aggressive mode" if mode == "aggressive" else f"older than {max_age_days} days"
        )
        detail = f"removed {removed_files} review item(s) ({mode_detail})"

    return CleanupResult(
        name="review",
        status=status,
        saved=removed_bytes,
        saved_human=humanize(removed_bytes),
        detail=detail,
    )
