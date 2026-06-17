"""Apply classification decisions: move, tag, delete, review."""
import fnmatch
import hashlib
import os
import plistlib
import shutil
import subprocess
from datetime import datetime
from pathlib import Path

from filemaid.llm import Decision


def _normalize(path: Path) -> str:
    return os.path.normpath(os.path.abspath(str(path)))


def _within_allowed(path: Path, allowed_dirs: list[str]) -> bool:
    norm = _normalize(path)
    for allowed in allowed_dirs:
        a = _normalize(Path(allowed))
        if norm == a or norm.startswith(a + os.sep):
            return True
    return False


def _compute_hash(path: Path) -> str:
    h = hashlib.sha256()
    with open(path, "rb") as f:
        for chunk in iter(lambda: f.read(8192), b""):
            h.update(chunk)
    return h.hexdigest()


def _matches_patterns(path: Path, patterns: list[str]) -> bool:
    rel = str(path)
    home = os.path.expanduser("~")
    if rel.startswith(home):
        rel = "~" + rel[len(home):]
    for pat in patterns:
        if fnmatch.fnmatchcase(rel, pat) or fnmatch.fnmatchcase(str(path), pat):
            return True
    return False


def _set_tags(path: Path, tags: list[str]):
    if not tags:
        return
    try:
        plist = plistlib.dumps(tags, fmt=plistlib.FMT_BINARY)
        hexval = plist.hex()
        subprocess.run(
            ["xattr", "-w", "-x", "com.apple.metadata:_kMDItemUserTags", hexval, str(path)],
            check=False,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        subprocess.run(
            ["mdimport", str(path)],
            check=False,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
    except Exception:
        pass


def _trash(path: Path):
    script = f'tell application "Finder" to delete POSIX file "{str(path)}"'
    subprocess.run(
        ["osascript", "-e", script],
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )


def _unique_dest(dest: Path) -> Path:
    if not dest.exists():
        return dest
    stem = dest.stem
    suffix = dest.suffix
    timestamp = datetime.now().isoformat(timespec="seconds").replace(":", "")
    return dest.with_name(f"{stem}-{timestamp}{suffix}")


def apply(decision: Decision, src: Path, config: dict, db, is_duplicate: bool = False) -> str:
    allowed_dirs = config.get("allowed_dirs", [])
    review_dir = Path(config["review_dir"])

    if allowed_dirs and not _within_allowed(src, allowed_dirs):
        return f"skipped (not allowed): {src}"

    file_hash = _compute_hash(src)

    if decision.action == "delete":
        safe = _matches_patterns(src, config.get("safe_delete_patterns", [])) or is_duplicate
        if not safe:
            decision.action = "review"
            decision.reason = f"delete refused for safety; original reason: {decision.reason}"

    if decision.action == "delete":
        final_path = review_dir / datetime.now().strftime("%Y-%m-%d") / src.name
        try:
            _trash(src)
            from filemaid.state import record
            record(db, src, "trash", file_hash, decision.category, decision.tags, "delete", decision.reason)
            return "trash"
        except Exception as exc:
            decision.action = "review"
            decision.reason = f"trash failed: {exc}"

    if decision.action == "review":
        dest = review_dir / datetime.now().strftime("%Y-%m-%d") / src.name
    else:
        if decision.destination:
            dest = Path(decision.destination)
        elif decision.category in config["categories"]:
            dest = Path(config["categories"][decision.category]) / src.name
        else:
            dest = review_dir / datetime.now().strftime("%Y-%m-%d") / src.name

    dest = _unique_dest(dest)

    if allowed_dirs and not _within_allowed(dest, allowed_dirs):
        dest = review_dir / datetime.now().strftime("%Y-%m-%d") / src.name
        decision.action = "review"
        decision.reason += "; destination outside allowed dirs"

    try:
        dest.parent.mkdir(parents=True, exist_ok=True)
        shutil.move(str(src), str(dest))
    except Exception as exc:
        return f"move failed: {src} -> {dest}: {exc}"

    if config.get("tags", True):
        _set_tags(dest, decision.tags)

    from filemaid.state import record
    record(db, src, dest, file_hash, decision.category, decision.tags, decision.action, decision.reason)
    return str(dest)
