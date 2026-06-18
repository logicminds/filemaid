"""cargo cache cleanup."""
import os
import shutil
import subprocess
from pathlib import Path

from filemaid.cleaners._size import dir_size, humanize


def _cargo_cache_dir() -> Path:
    return Path(os.environ.get("CARGO_HOME", Path.home() / ".cargo")) / "registry" / "cache"


def can_run() -> bool:
    return shutil.which("cargo") is not None and shutil.which("cargo-cache") is not None


def run(dry_run: bool, config: dict) -> str:
    cache_dir = _cargo_cache_dir()
    before = dir_size(cache_dir) if cache_dir.exists() else 0
    if dry_run:
        return f"cargo: would autoclean cache (~{humanize(before)} to remove)"
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
        return f"cargo: cache autocleaned, saved {humanize(saved)}\n{output}"
    except Exception as exc:
        return f"cargo: failed - {exc}"
