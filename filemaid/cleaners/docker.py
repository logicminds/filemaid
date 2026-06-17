"""Docker image/system cleanup."""
import shutil
import subprocess


def can_run() -> bool:
    return shutil.which("docker") is not None


def run(dry_run: bool, config: dict) -> str:
    mode = config.get("dev_cleanup", {}).get("docker", {}).get("mode", "safe")
    if mode == "aggressive":
        cmd = ["docker", "system", "prune", "-af", "--volumes"]
    else:
        cmd = ["docker", "image", "prune", "-f"]

    if dry_run:
        return f"docker: would run {' '.join(cmd)}"

    try:
        result = subprocess.run(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            timeout=300,
        )
        return f"docker: {' '.join(cmd)}\n{result.stdout.strip()}"
    except Exception as exc:
        return f"docker: failed - {exc}"
