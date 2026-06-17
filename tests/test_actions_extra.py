"""Additional tests for filemaid.actions edge cases."""
import subprocess
from pathlib import Path

import pytest

from filemaid.llm import Decision
from filemaid.state import init_db
from filemaid.actions import apply, _Filesystem


class RecordingFilesystem(_Filesystem):
    def __init__(self):
        self.moved = []
        self.tags = []
        self.trashed = []

    def move(self, src, dest):
        self.moved.append((src, dest))
        # Actually move so path.exists checks work
        dest.parent.mkdir(parents=True, exist_ok=True)
        src.rename(dest)

    def set_tags(self, path, tags):
        self.tags.append((path, tags))

    def trash(self, path):
        self.trashed.append(path)


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
        "tags": True,
        "safe_delete_patterns": ["~/Downloads/*.tmp"],
    }


def test_apply_sets_tags(config, db, tmp_path):
    fs = RecordingFilesystem()
    src = tmp_path / "Desktop" / "img.png"
    src.parent.mkdir(parents=True)
    src.write_text("image")

    decision = Decision(category="Images", tags=["image", "desktop"], action="move", reason="png")
    apply(decision, src, config, db, fs=fs)

    assert len(fs.tags) == 1
    assert fs.tags[0][1] == ["image", "desktop"]


def test_apply_trashes_allowed_delete(config, db, tmp_path):
    fs = RecordingFilesystem()
    home = tmp_path / "home"
    home.mkdir()
    mp = pytest.MonkeyPatch()
    mp.setenv("HOME", str(home))
    try:
        src = home / "Downloads" / "junk.tmp"
        src.parent.mkdir(parents=True)
        src.write_text("junk")
        config["allowed_dirs"] = [str(home / "Downloads"), str(config["review_dir"])]

        decision = Decision(category="Unknown", tags=[], action="delete", reason="temp")
        result = apply(decision, src, config, db, fs=fs)

        assert result == "trash"
        assert len(fs.trashed) == 1
    finally:
        mp.undo()


def test_apply_trash_failure_forces_review(config, db, tmp_path, monkeypatch):
    class FailingFs(RecordingFilesystem):
        def trash(self, path):
            raise RuntimeError("finder down")

    fs = FailingFs()
    home = tmp_path / "home"
    home.mkdir()
    monkeypatch.setenv("HOME", str(home))
    src = home / "Downloads" / "junk.tmp"
    src.parent.mkdir(parents=True)
    src.write_text("junk")
    config["allowed_dirs"] = [str(home / "Downloads"), str(config["review_dir"])]

    decision = Decision(category="Unknown", tags=[], action="delete", reason="temp")
    result = apply(decision, src, config, db, fs=fs)

    assert "review" in result


def test_apply_move_failure_returns_error(config, db, tmp_path, monkeypatch):
    class FailingFs(RecordingFilesystem):
        def move(self, src, dest):
            raise RuntimeError("disk full")

    fs = FailingFs()
    src = tmp_path / "Desktop" / "file.txt"
    src.parent.mkdir(parents=True)
    src.write_text("data")

    decision = Decision(category="Documents", tags=[], action="move", reason="txt")
    result = apply(decision, src, config, db, fs=fs)

    assert "move failed" in result


def test_apply_unique_dest_collision(config, db, tmp_path):
    fs = RecordingFilesystem()
    src1 = tmp_path / "Desktop" / "file.txt"
    src1.parent.mkdir(parents=True)
    src1.write_text("one")
    src2 = tmp_path / "Desktop" / "file2.txt"
    src2.write_text("two")

    dest_dir = Path(config["categories"]["Documents"])
    dest_dir.mkdir(parents=True)
    (dest_dir / "file.txt").write_text("existing")

    decision = Decision(category="Documents", tags=[], action="move", reason="txt")
    result1 = apply(decision, src1, config, db, fs=fs)
    result2 = apply(decision, src2, config, db, fs=fs)

    assert "file-" in result1 or result1.endswith("file2.txt")
    assert result2.endswith("file2.txt")


def test_apply_destination_outside_allowed_redirects(config, db, tmp_path):
    fs = RecordingFilesystem()
    src = tmp_path / "Desktop" / "file.txt"
    src.parent.mkdir(parents=True)
    src.write_text("data")

    decision = Decision(category="Images", tags=[], action="move", destination="/tmp/outside", reason="hack")
    result = apply(decision, src, config, db, fs=fs)

    assert Path(result).is_relative_to(Path(config["review_dir"]))
    assert "destination outside allowed dirs" in db.execute("SELECT reason FROM history").fetchone()[0]


def test_apply_source_not_allowed(config, db, tmp_path):
    fs = RecordingFilesystem()
    src = tmp_path / "Secret" / "file.txt"
    src.parent.mkdir(parents=True)
    src.write_text("data")

    decision = Decision(category="Documents", tags=[], action="move", reason="txt")
    result = apply(decision, src, config, db, fs=fs)

    assert "skipped (not allowed)" in result
