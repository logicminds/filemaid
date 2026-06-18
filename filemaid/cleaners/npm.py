"""npm cache cleanup."""
import shutil
import subprocess
from pathlib import Path

from filemaid.cleaners._result import CleanupResult
from filemaid.cleaners._size import dir_size, humanize


def _npm_cache_dir() -> Path:
    try:
        result = subprocess.run(
            ["npm", "config", "get", "cache"],
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
            timeout=30,
        )
        path = result.stdout.strip()
        if path and path != "undefined":
            return Path(path).expanduser()
    except Exception:
        pass
    return Path.home() / ".npm"


def can_run() -> bool:
    return shutil.which("npm") is not None


def run(dry_run: bool, config: dict) -> CleanupResult:
    cache_dir = _npm_cache_dir()
    content_dir = cache_dir / "_cacache"
    before = dir_size(content_dir) if content_dir.exists() else 0
    if dry_run:
        return CleanupResult(
            name="npm",
            status="dry-run",
            saved=before,
            saved_human=humanize(before),
            detail="would clean cache",
        )
    try:
        result = subprocess.run(
            ["npm", "cache", "clean", "--force"],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            timeout=120,
        )
        after = dir_size(content_dir) if content_dir.exists() else 0
        saved = max(0, before - after)
        output = result.stdout.strip()
        return CleanupResult(
            name="npm",
            status="ok",
            saved=saved,
            saved_human=humanize(saved),
            detail=f"cache cleaned\n{output}" if output else "cache cleaned",
        )
    except Exception as exc:
        return CleanupResult(
            name="npm",
            status="failed",
            detail=str(exc),
        )
