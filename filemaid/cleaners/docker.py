"""Docker image/system cleanup."""
import shutil
import subprocess

from filemaid.cleaners._result import CleanupResult
from filemaid.cleaners._size import parse_size


def can_run() -> bool:
    return shutil.which("docker") is not None


def run(dry_run: bool, config: dict) -> CleanupResult:
    mode = config.get("dev_cleanup", {}).get("docker", {}).get("mode", "safe")
    if mode == "aggressive":
        cmd = ["docker", "system", "prune", "-af", "--volumes"]
    else:
        cmd = ["docker", "image", "prune", "-f"]
    command = " ".join(cmd)

    if dry_run:
        return CleanupResult(
            name="docker",
            status="dry-run",
            detail=f"would run {command}",
            command=command,
        )

    try:
        result = subprocess.run(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            timeout=300,
        )
        output = result.stdout.strip()
        reclaimed = "0 B"
        saved = 0
        for line in output.splitlines():
            if "Total reclaimed space:" in line:
                reclaimed = line.split(":", 1)[-1].strip()
                saved = parse_size(reclaimed) or 0
                break
        return CleanupResult(
            name="docker",
            status="ok",
            saved=saved,
            saved_human=reclaimed,
            detail=output or f"ran {command}",
            command=command,
        )
    except Exception as exc:
        return CleanupResult(
            name="docker",
            status="failed",
            detail=str(exc),
            command=command,
        )
