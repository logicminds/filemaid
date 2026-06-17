"""Tests for filemaid.actions."""
import sqlite3
from pathlib import Path

import pytest

from filemaid.llm import Decision
from filemaid.state import init_db
from filemaid.actions import (
    _within_allowed,
    _compute_hash,
    _matches_patterns,
    _unique_dest,
    apply,
    _Filesystem,
)


@pytest.fixture
def fs(tmp_path):
    """Return a filesystem helper that records operations."""
    return _Filesystem()


@pytest.fixture
def db(tmp_path):
    conn = init_db(str(tmp_path / "filemaid.db"))
    yield conn
    conn.close()


@pytest.fixture
def config(tmp_path):
    review_dir = tmp_path / "review"
    review_dir.mkdir()
    categories = {
        "Images": str(tmp_path / "Images"),
        "Documents": str(tmp_path / "Documents"),
        "Unknown": str(review_dir),
    }
    return {
        "allowed_dirs": [str(tmp_path / "Desktop"), str(tmp_path / "Downloads"), str(tmp_path / "Images"), str(tmp_path / "Documents"), str(review_dir)],
        "review_dir": str(review_dir),
        "categories": categories,
        "tags": False,
        "safe_delete_patterns": ["~/Downloads/*.tmp"],
    }


def test_within_allowed_same_dir(config):
    path = Path(config["allowed_dirs"][0]) / "foo.txt"
    assert _within_allowed(path, config["allowed_dirs"]) is True


def test_within_allowed_outside(config, tmp_path):
    path = tmp_path / "elsewhere" / "foo.txt"
    assert _within_allowed(path, config["allowed_dirs"]) is False


def test_compute_hash(tmp_path):
    f = tmp_path / "data.bin"
    f.write_bytes(b"hello")
    h1 = _compute_hash(f)
    h2 = _compute_hash(f)
    assert len(h1) == 64
    assert h1 == h2


def test_matches_patterns_with_tilde(config, tmp_path):
    home = tmp_path / "home"
    home.mkdir()
    monkeypatch = pytest.MonkeyPatch()
    monkeypatch.setenv("HOME", str(home))
    try:
        path = home / "Downloads" / "old.tmp"
        assert _matches_patterns(path, config["safe_delete_patterns"]) is True
        assert _matches_patterns(home / "Desktop" / "old.tmp", config["safe_delete_patterns"]) is False
    finally:
        monkeypatch.undo()


def test_unique_dest_no_conflict(tmp_path):
    dest = tmp_path / "Images" / "foo.png"
    assert _unique_dest(dest) == dest


def test_unique_dest_with_conflict(tmp_path):
    dest = tmp_path / "Images" / "foo.png"
    dest.parent.mkdir(parents=True)
    dest.write_text("existing")
    unique = _unique_dest(dest)
    assert unique != dest
    assert unique.name.startswith("foo-")


def test_apply_moves_file_to_category(config, db, fs, tmp_path):
    src = tmp_path / "Desktop" / "img.png"
    src.parent.mkdir(parents=True)
    src.write_text("image")

    decision = Decision(category="Images", tags=[], action="move", reason="png")
    result = apply(decision, src, config, db, fs=fs)

    assert result.startswith(str(tmp_path / "Images"))
    assert (tmp_path / "Images" / "img.png").exists()
    assert not src.exists()


def test_apply_returns_review_for_disallowed_source(config, db, fs, tmp_path):
    src = tmp_path / " outside" / "img.png"
    src.parent.mkdir(parents=True)
    src.write_text("image")

    decision = Decision(category="Images", tags=[], action="move", reason="png")
    result = apply(decision, src, config, db, fs=fs)
    assert "skipped" in result


def test_apply_forces_review_on_unsafe_delete(config, db, fs, tmp_path):
    src = tmp_path / "Desktop" / "note.txt"
    src.parent.mkdir(parents=True)
    src.write_text("hi")

    decision = Decision(category="Documents", tags=[], action="delete", reason="delete it")
    result = apply(decision, src, config, db, fs=fs)

    # unsafe delete coerced to review; placed in dated review subdir
    import datetime
    review_subdir = Path(config["review_dir"]) / datetime.datetime.now().strftime("%Y-%m-%d")
    assert result.startswith(str(review_subdir))
    assert not src.exists()


def test_apply_allows_delete_for_safe_pattern(config, db, fs, tmp_path):
    home = tmp_path / "home"
    home.mkdir()
    mp = pytest.MonkeyPatch()
    mp.setenv("HOME", str(home))
    try:
        src = home / "Downloads" / "junk.tmp"
        src.parent.mkdir(parents=True)
        src.write_text("junk")

        # Rebuild config with expanded HOME
        config["allowed_dirs"] = [str(home / "Downloads"), str(config["review_dir"])]

        decision = Decision(category="Unknown", tags=[], action="delete", reason="temp file")
        result = apply(decision, src, config, db, fs=fs)
        assert result == "trash"
    finally:
        mp.undo()


def test_apply_redirects_outside_allowed_destination(config, db, fs, tmp_path):
    src = tmp_path / "Desktop" / "file.txt"
    src.parent.mkdir(parents=True)
    src.write_text("data")

    # Decision tries to move to a destination outside allowed dirs
    decision = Decision(category="Images", tags=[], action="move", destination="/tmp/evil", reason="hack")
    result = apply(decision, src, config, db, fs=fs)

    assert Path(result).is_relative_to(Path(config["review_dir"]))


def test_apply_records_history(config, db, fs, tmp_path):
    src = tmp_path / "Desktop" / "doc.txt"
    src.parent.mkdir(parents=True)
    src.write_text("document")

    decision = Decision(category="Documents", tags=[], action="move", reason="txt")
    apply(decision, src, config, db, fs=fs)

    cur = db.execute("SELECT original_path, final_path, action FROM history")
    rows = cur.fetchall()
    assert len(rows) == 1
    assert rows[0][2] == "move"


def test_apply_duplicate_detection(config, db, fs, tmp_path):
    src1 = tmp_path / "Desktop" / "a.txt"
    src1.parent.mkdir(parents=True)
    src1.write_text("same content")

    decision = Decision(category="Documents", tags=[], action="delete", reason="delete dup")
    apply(decision, src1, config, db, fs=fs)

    src2 = tmp_path / "Desktop" / "b.txt"
    src2.write_text("same content")

    result = apply(decision, src2, config, db, fs=fs)
    assert Path(result).is_relative_to(Path(config["review_dir"]))
    # src2 should not have been deleted (coerced to review)
    assert not (Path(config["categories"]["Documents"]) / "b.txt").exists()
