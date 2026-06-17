"""cargo cache cleanup."""
import shutil
import subprocess


def can_run() -> bool:
    return shutil.which("cargo") is not None and shutil.which("cargo-cache") is not None


def run(dry_run: bool, config: dict) -> str:
    if dry_run:
        return "cargo: would autoclean cache"
    try:
        result = subprocess.run(
            ["cargo", "cache", "--autoclean"],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            timeout=300,
        )
        return f"cargo: cache autocleaned\n{result.stdout.strip()}"
    except Exception as exc:
        return f"cargo: failed - {exc}"
