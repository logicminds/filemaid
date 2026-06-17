"""pip cache cleanup."""
import subprocess


def can_run() -> bool:
    return True


def run(dry_run: bool, config: dict) -> str:
    if dry_run:
        return "pip: would purge cache"
    try:
        result = subprocess.run(
            ["python3", "-m", "pip", "cache", "purge"],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            timeout=120,
        )
        return f"pip: cache purged\n{result.stdout.strip()}"
    except Exception as exc:
        return f"pip: failed - {exc}"
