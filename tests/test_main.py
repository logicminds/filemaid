"""Tests for filemaid.__main__."""
import logging
import sqlite3
from pathlib import Path

import pytest

from filemaid.__main__ import (
    _is_hidden,
    _check_age_rule,
    process_paths,
    scan_dir,
    run_cleanup,
    review_queue,
    tail_logs,
)
from filemaid.llm import Decision
from filemaid.state import init_db


@pytest.fixture
def db(tmp_path):
    conn = init_db(str(tmp_path / "filemaid.db"))
    yield conn
    conn.close()


@pytest.fixture
def config(tmp_path):
    review_dir = tmp_path / "review"
    review_dir.mkdir()
    return {
        "allowed_dirs": [str(tmp_path / "Desktop"), str(tmp_path / "Downloads"), str(tmp_path / "Images"), str(tmp_path / "Documents"), str(review_dir)],
        "review_dir": str(review_dir),
        "categories": {
            "Images": str(tmp_path / "Images"),
            "Documents": str(tmp_path / "Documents"),
            "Unknown": str(review_dir),
        },
        "tags": False,
        "safe_delete_patterns": [],
        "age_rules": [
            {"pattern": "~/Downloads/*.dmg", "days": 30, "action": "review"},
        ],
    }


def test_is_hidden():
    assert _is_hidden(Path(".DS_Store")) is True
    assert _is_hidden(Path(".localized")) is True
    assert _is_hidden(Path("file.txt")) is False


def test_check_age_rule_matches_old_file(config, tmp_path):
    home = tmp_path / "home"
    home.mkdir()
    mp = pytest.MonkeyPatch()
    mp.setenv("HOME", str(home))
    try:
        old = home / "Downloads" / "old.dmg"
        old.parent.mkdir(parents=True)
        old.write_text("installer")
        # Set mtime to 60 days ago
        import time as time_mod
        old.touch()
        now = time_mod.time()
        old.write_text("installer")

        class _Result:
            pass

        # Use os.utime to set old time
        import os
        os.utime(old, (now - 60 * 86400, now - 60 * 86400))

        decision = _check_age_rule(old, config)
        assert decision is not None
        assert decision.action == "review"
        assert "age rule" in decision.reason
    finally:
        mp.undo()


def test_check_age_rule_no_match_for_recent(config, tmp_path):
    home = tmp_path / "home"
    home.mkdir()
    mp = pytest.MonkeyPatch()
    mp.setenv("HOME", str(home))
    try:
        new = home / "Downloads" / "new.dmg"
        new.parent.mkdir(parents=True)
        new.write_text("installer")
        assert _check_age_rule(new, config) is None
    finally:
        mp.undo()


def test_process_paths_skips_nonexistent(config, db, tmp_path, caplog):
    caplog.set_level(logging.WARNING)
    process_paths([str(tmp_path / "nope.txt")], config, db)
    assert "path does not exist" in caplog.text


def test_process_paths_skips_hidden(config, db, tmp_path, caplog):
    caplog.set_level(logging.INFO)
    hidden = tmp_path / "Desktop" / ".secret"
    hidden.parent.mkdir(parents=True)
    hidden.write_text("secret")
    process_paths([str(hidden)], config, db)
    assert "skipping hidden file" in caplog.text


def test_process_paths_skips_outside_allowed(config, db, tmp_path, caplog):
    caplog.set_level(logging.WARNING)
    outside = tmp_path / "outside.txt"
    outside.write_text("outside")
    process_paths([str(outside)], config, db)
    assert "outside allowed dirs" in caplog.text


def test_process_paths_classifies_and_applies(config, db, tmp_path):
    src = tmp_path / "Desktop" / "note.txt"
    src.parent.mkdir(parents=True)
    src.write_text("hello")

    def fake_classifier(path, cfg):
        return Decision(category="Documents", tags=["txt"], action="move", reason="text")

    process_paths([str(src)], config, db, classifier=fake_classifier)

    cur = db.execute("SELECT * FROM history")
    assert len(cur.fetchall()) == 1
    assert (Path(config["categories"]["Documents"]) / "note.txt").exists()


def test_process_paths_duplicate_forces_review(config, db, tmp_path):
    src1 = tmp_path / "Desktop" / "a.txt"
    src1.parent.mkdir(parents=True)
    src1.write_text("same")

    src2 = tmp_path / "Desktop" / "b.txt"
    src2.write_text("same")

    def fake_classifier(path, cfg):
        return Decision(category="Documents", tags=[], action="move", reason="dup")

    process_paths([str(src1), str(src2)], config, db, classifier=fake_classifier)
    # Second file should have action coerced to review due to duplicate
    cur = db.execute("SELECT action FROM history ORDER BY id")
    actions = [row[0] for row in cur.fetchall()]
    assert actions[0] == "move"
    assert actions[1] == "review"


def test_scan_dir_processes_files(config, db, tmp_path):
    src = tmp_path / "Desktop" / "note.txt"
    src.parent.mkdir(parents=True)
    src.write_text("hello")

    def fake_iterdir():
        return [src]

    def fake_classifier(path, cfg):
        return Decision(category="Documents", tags=[], action="move", reason="text")

    def fake_applier(decision, src, cfg, db_conn, is_duplicate=False):
        return str(Path(cfg["categories"]["Documents"]) / src.name)

    scan_dir(str(tmp_path / "Desktop"), config, db, iterdir=fake_iterdir)

    cur = db.execute("SELECT * FROM history")
    assert len(cur.fetchall()) == 1


def test_scan_dir_rejects_not_allowed(config, db, tmp_path, caplog):
    caplog.set_level(logging.ERROR)
    scan_dir(str(tmp_path / "NotAllowed"), config, db)
    assert "not allowed" in caplog.text


def test_scan_dir_permission_error(config, db, tmp_path, caplog, monkeypatch):
    caplog.set_level(logging.ERROR)

    def fake_iterdir():
        raise PermissionError("denied")

    scan_dir(str(tmp_path / "Desktop"), config, db, iterdir=fake_iterdir)
    assert "permission denied" in caplog.text


def test_run_cleanup_skips_not_allowed(config, tmp_path, caplog, monkeypatch):
    caplog.set_level(logging.INFO)
    config["allowed_cleaners"] = ["pip"]

    # pip cleaner should run, others skipped
    run_cleanup(dry_run=True, config=config)
    assert "docker: not in allowed_cleaners whitelist" in caplog.text
    assert "pip: would purge cache" in caplog.text


def test_review_queue_lists_files(config, tmp_path, capsys):
    review_dir = Path(config["review_dir"])
    sub = review_dir / "2026-01-01"
    sub.mkdir(parents=True)
    (sub / "file.txt").write_text("hello")
    review_queue(str(review_dir), open_finder=False)
    captured = capsys.readouterr()
    assert "file.txt" in captured.out


def test_tail_logs(tmp_path, capsys):
    log = tmp_path / "filemaid.log"
    log.write_text("line1\nline2\nline3\n")
    tail_logs(str(log), 2)
    captured = capsys.readouterr()
    assert "line2" in captured.out
    assert "line3" in captured.out
    assert "line1" not in captured.out


def test_tail_logs_missing_file(tmp_path, capsys):
    tail_logs(str(tmp_path / "missing.log"), 5)
    captured = capsys.readouterr()
    assert "log file not found" in captured.out
