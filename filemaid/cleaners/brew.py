"""Homebrew cleanup."""
import shutil
import subprocess
from pathlib import Path

from filemaid.cleaners._size import dir_size, humanize


def _brew_cache_dir() -> Path:
    try:
        result = subprocess.run(
            ["brew", "--cache"],
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
    return Path.home() / "Library" / "Caches" / "Homebrew"


def can_run() -> bool:
    return shutil.which("brew") is not None


def run(dry_run: bool, config: dict) -> str:
    mode = config.get("dev_cleanup", {}).get("brew", {}).get("mode", "safe")
    prune = "all" if mode == "aggressive" else "7"
    cache_dir = _brew_cache_dir()
    before = dir_size(cache_dir) if cache_dir.exists() else 0

    if dry_run:
        return f"brew: would cleanup --prune={prune} (~{humanize(before)} cache to consider)"

    try:
        result = subprocess.run(
            ["brew", "cleanup", f"--prune={prune}"],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            timeout=300,
        )
        after = dir_size(cache_dir) if cache_dir.exists() else 0
        saved = max(0, before - after)
        output = result.stdout.strip()
        return f"brew: cleanup --prune={prune}, saved {humanize(saved)}\n{output}"
    except Exception as exc:
        return f"brew: failed - {exc}"
