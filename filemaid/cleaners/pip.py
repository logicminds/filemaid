"""pip cache cleanup."""
import subprocess
from pathlib import Path

from filemaid.cleaners._size import dir_size, humanize


def _pip_cache_dir() -> Path:
    try:
        result = subprocess.run(
            ["python3", "-m", "pip", "cache", "dir"],
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
            timeout=30,
        )
        path = result.stdout.strip()
        if path:
            return Path(path).expanduser()
    except Exception:
        pass
    return Path.home() / "Library" / "Caches" / "pip"


def can_run() -> bool:
    return True


def run(dry_run: bool, config: dict) -> str:
    cache_dir = _pip_cache_dir()
    before = dir_size(cache_dir) if cache_dir.exists() else 0
    if dry_run:
        return f"pip: would purge cache (~{humanize(before)} to remove)"
    try:
        result = subprocess.run(
            ["python3", "-m", "pip", "cache", "purge"],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            timeout=120,
        )
        after = dir_size(cache_dir) if cache_dir.exists() else 0
        saved = max(0, before - after)
        output = result.stdout.strip()
        return f"pip: cache purged, saved {humanize(saved)}\n{output}"
    except Exception as exc:
        return f"pip: failed - {exc}"
