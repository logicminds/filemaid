"""npm cache cleanup."""
import shutil
import subprocess


def can_run() -> bool:
    return shutil.which("npm") is not None


def run(dry_run: bool, config: dict) -> str:
    if dry_run:
        return "npm: would clean cache"
    try:
        result = subprocess.run(
            ["npm", "cache", "clean", "--force"],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            timeout=120,
        )
        return f"npm: cache cleaned\n{result.stdout.strip()}"
    except Exception as exc:
        return f"npm: failed - {exc}"
