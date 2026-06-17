"""filemaid CLI entry point."""
from __future__ import annotations

import argparse
import fnmatch
import json
import logging
import os
import sys
import time
from datetime import datetime
from pathlib import Path

from filemaid.actions import apply, _compute_hash, _matches_patterns, _within_allowed
from filemaid.cleaners import CLEANERS
from filemaid.config import load_config
from filemaid.llm import classify_file, Decision
from filemaid.state import find_by_hash, init_db


def _setup_logging(log_path: str):
    Path(log_path).parent.mkdir(parents=True, exist_ok=True)
    handlers = [logging.StreamHandler(sys.stderr)]
    from logging.handlers import RotatingFileHandler

    handlers.append(RotatingFileHandler(log_path, maxBytes=5 * 1024 * 1024, backupCount=1))
    logging.basicConfig(
        level=logging.INFO,
        format="%(asctime)s %(levelname)s %(message)s",
        handlers=handlers,
    )


def _is_hidden(path: Path) -> bool:
    return path.name.startswith(".") or path.name in (".localized", ".DS_Store")


def _check_age_rule(path: Path, config: dict) -> Decision | None:
    stat = path.stat()
    age_days = (time.time() - stat.st_mtime) / 86400
    for rule in config.get("age_rules", []):
        if _matches_patterns(path, [rule["pattern"]]):
            if age_days >= rule["days"]:
                return Decision(
                    category="Unknown",
                    tags=["age-rule"],
                    action=rule.get("action", "review"),
                    reason=f"age rule {rule['pattern']} > {rule['days']} days",
                )
    return None


def process_paths(
    paths: list[str],
    config: dict,
    db,
    *,
    classifier=classify_file,
    applier=apply,
):
    logger = logging.getLogger("filemaid")
    allowed_dirs = config.get("allowed_dirs", [])

    for raw in paths:
        src = Path(raw).resolve()
        if not src.exists():
            logger.warning("path does not exist: %s", raw)
            continue
        if not src.is_file():
            logger.warning("not a file: %s", src)
            continue
        if _is_hidden(src):
            logger.info("skipping hidden file: %s", src)
            continue
        if allowed_dirs and not _within_allowed(src, allowed_dirs):
            logger.warning("skipping file outside allowed dirs: %s", src)
            continue

        file_hash = _compute_hash(src)
        dup = find_by_hash(db, file_hash)
        is_duplicate = dup is not None and Path(dup["final_path"]).exists()

        decision = _check_age_rule(src, config)
        if decision is None:
            decision = classifier(src, config)

        if is_duplicate:
            if _matches_patterns(src, config.get("safe_delete_patterns", [])):
                decision.action = "delete"
                decision.reason += "; duplicate matches safe delete pattern"
            elif decision.action != "review":
                decision.action = "review"
                decision.reason += "; duplicate detected"

        result = applier(decision, src, config, db, is_duplicate=is_duplicate)
        logger.info("processed %s -> %s (category=%s action=%s reason=%s)", src, result, decision.category, decision.action, decision.reason)


def scan_dir(directory: str, config: dict, db, *, iterdir=None):
    logger = logging.getLogger("filemaid")
    allowed_dirs = config.get("allowed_dirs", [])
    root = Path(directory).expanduser().resolve()
    if allowed_dirs and not _within_allowed(root, allowed_dirs):
        logger.error("scan directory not allowed: %s", root)
        return

    iterdir_fn = iterdir if iterdir is not None else root.iterdir
    try:
        items = list(iterdir_fn())
    except PermissionError as exc:
        logger.error("permission denied scanning %s: %s", root, exc)
        return

    min_age = config.get("min_age_hours", 0)
    cutoff = time.time() - (min_age * 3600)

    files = []
    for item in items:
        if not item.is_file():
            continue
        if _is_hidden(item):
            continue
        if item.stat().st_mtime > cutoff:
            continue
        if allowed_dirs and not _within_allowed(item, allowed_dirs):
            continue
        files.append(item)

    if files:
        process_paths([str(f) for f in files], config, db)
    else:
        logger.info("no files to scan in %s", root)


def run_cleanup(dry_run: bool, config: dict):
    logger = logging.getLogger("filemaid")
    allowed = set(config.get("allowed_cleaners", []))
    dev_cfg = config.get("dev_cleanup", {})

    for cleaner in CLEANERS:
        name = cleaner.__name__.split(".")[-1]
        if name not in allowed:
            logger.info("%s: not in allowed_cleaners whitelist", name)
            continue
        if not cleaner.can_run():
            logger.info("%s: skipped (not installed)", name)
            continue
        enabled = dev_cfg.get(name, {}).get("enabled", True)
        if not enabled:
            logger.info("%s: disabled in config", name)
            continue
        result = cleaner.run(dry_run, config)
        logger.info(result)
        print(result)


def review_queue(review_dir: str, open_finder: bool):
    path = Path(review_dir)
    if open_finder:
        os.system(f'open "{path}"')
        return
    if not path.exists():
        print("review queue is empty")
        return
    for item in sorted(path.rglob("*")):
        if item.is_file():
            rel = item.relative_to(path)
            print(f"{rel}: {item.stat().st_size} bytes")


def tail_logs(log_path: str, n: int):
    try:
        with open(log_path, "r", encoding="utf-8", errors="ignore") as f:
            lines = f.readlines()
    except FileNotFoundError:
        print("log file not found")
        return
    for line in lines[-n:]:
        print(line, end="")


def main():
    parser = argparse.ArgumentParser(prog="filemaid")
    sub = parser.add_subparsers(dest="command", required=True)

    p_process = sub.add_parser("process", help="classify and apply decisions to files")
    p_process.add_argument("paths", nargs="+", help="files to process")

    p_scan = sub.add_parser("scan", help="scan a directory for stale files")
    p_scan.add_argument("--dir", help="directory to scan (default: watch_dirs)")

    p_cleanup = sub.add_parser("cleanup", help="run dev artifact cleaners")
    p_cleanup.add_argument("--dry-run", action="store_true", help="do not actually clean")

    p_review = sub.add_parser("review", help="list or open review queue")
    p_review.add_argument("--open", action="store_true", help="open review queue in Finder")

    p_logs = sub.add_parser("logs", help="tail log file")
    p_logs.add_argument("--tail", type=int, default=20, help="number of lines to show")

    sub.add_parser("config", help="print resolved config")

    args = parser.parse_args()

    config = load_config()
    _setup_logging(config["log_path"])
    db = init_db(config["db_path"])

    try:
        if args.command == "process":
            process_paths(args.paths, config, db)
        elif args.command == "scan":
            if args.dir:
                scan_dir(args.dir, config, db)
            else:
                for d in config.get("watch_dirs", []):
                    scan_dir(d, config, db)
        elif args.command == "cleanup":
            run_cleanup(args.dry_run, config)
        elif args.command == "review":
            review_queue(config["review_dir"], args.open)
        elif args.command == "logs":
            tail_logs(config["log_path"], args.tail)
        elif args.command == "config":
            print(json.dumps(config, indent=2, default=str))
    finally:
        db.close()


if __name__ == "__main__":
    main()
