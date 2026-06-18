"""cargo cache cleanup."""
import os
import shutil
import subprocess
from pathlib import Path

from filemaid.cleaners._result import CleanupResult
from filemaid.cleaners._size import dir_size, humanize


def _cargo_cache_dir() -> Path:
    return Path(os.environ.get("CARGO_HOME", Path.home() / ".cargo")) / "registry" / "cache"


def can_run() -> bool:
    return shutil.which("cargo") is not None and shutil.which("cargo-cache") is not None


def run(dry_run: bool, config: dict) -> CleanupResult:
    cache_dir = _cargo_cache_dir()
    before = dir_size(cache_dir) if cache_dir.exists() else 0
    if dry_run:
        return CleanupResult(
            name="cargo",
            status="dry-run",
            saved=before,
            saved_human=humanize(before),
            detail="would autoclean cache",
        )
    try:
        result = subprocess.run(
            ["cargo", "cache", "--autoclean"],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            timeout=300,
        )
        after = dir_size(cache_dir) if cache_dir.exists() else 0
        saved = max(0, before - after)
        output = result.stdout.strip()
        return CleanupResult(
            name="cargo",
            status="ok",
            saved=saved,
            saved_human=humanize(saved),
            detail=f"cache autocleaned\n{output}" if output else "cache autocleaned",
        )
    except Exception as exc:
        return CleanupResult(
            name="cargo",
            status="failed",
            detail=str(exc),
        )
