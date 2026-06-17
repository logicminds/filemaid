"""Homebrew cleanup."""
import shutil
import subprocess


def can_run() -> bool:
    return shutil.which("brew") is not None


def run(dry_run: bool, config: dict) -> str:
    mode = config.get("dev_cleanup", {}).get("brew", {}).get("mode", "safe")
    prune = "all" if mode == "aggressive" else "7"
    if dry_run:
        return f"brew: would cleanup --prune={prune}"
    try:
        result = subprocess.run(
            ["brew", "cleanup", f"--prune={prune}"],
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            timeout=300,
        )
        return f"brew: cleanup --prune={prune}\n{result.stdout.strip()}"
    except Exception as exc:
        return f"brew: failed - {exc}"
