"""Tests for filemaid CLI entry points."""
import json
import sqlite3
from pathlib import Path

import pytest

from filemaid.__main__ import main, tail_logs, review_queue, run_cleanup


@pytest.fixture
def temp_env(tmp_path, monkeypatch):
    home = tmp_path / "home"
    home.mkdir()
    monkeypatch.setenv("HOME", str(home))
    project = tmp_path / "project"
    project.mkdir()
    monkeypatch.syspath_prepend(str(project.parent))
    return home


def test_main_config_command(temp_env, capsys, monkeypatch):
    home = temp_env
    config_dir = home / ".config" / "filemaid"
    config_dir.mkdir(parents=True)
    config = {
        "ollama_url": "http://localhost:11434",
        "model": "dummy",
        "watch_dirs": ["~/Desktop"],
        "allowed_dirs": ["~/Desktop", "~/.filemaid/review"],
        "allowed_cleaners": ["pip"],
        "review_dir": "~/.filemaid/review",
        "log_path": "~/.local/share/filemaid/filemaid.log",
        "db_path": "~/.local/share/filemaid/filemaid.db",
        "tags": True,
        "min_age_hours": 0,
        "categories": {"Unknown": "~/.filemaid/review"},
        "safe_delete_patterns": [],
        "age_rules": [],
        "dev_cleanup": {"pip": {"enabled": True, "mode": "safe"}},
    }
    config_dir.joinpath("config.json").write_text(json.dumps(config))

    monkeypatch.setattr("sys.argv", ["filemaid", "config"])
    main()
    captured = capsys.readouterr()
    out = json.loads(captured.out)
    assert out["model"] == "dummy"
    assert out["ollama_url"] == "http://localhost:11434"


def test_main_logs_command(temp_env, capsys, monkeypatch):
    home = temp_env
    config_dir = home / ".config" / "filemaid"
    config_dir.mkdir(parents=True)
    config = {
        "ollama_url": "http://localhost:11434",
        "model": "dummy",
        "watch_dirs": ["~/Desktop"],
        "allowed_dirs": ["~/Desktop", "~/.filemaid/review"],
        "allowed_cleaners": ["pip"],
        "review_dir": "~/.filemaid/review",
        "log_path": "~/.local/share/filemaid/filemaid.log",
        "db_path": "~/.local/share/filemaid/filemaid.db",
        "tags": True,
        "min_age_hours": 0,
        "categories": {"Unknown": "~/.filemaid/review"},
        "safe_delete_patterns": [],
        "age_rules": [],
        "dev_cleanup": {"pip": {"enabled": True, "mode": "safe"}},
    }
    config_dir.joinpath("config.json").write_text(json.dumps(config))
    log_file = home / ".local" / "share" / "filemaid" / "filemaid.log"
    log_file.parent.mkdir(parents=True)
    log_file.write_text("line1\nline2\nline3\n")

    monkeypatch.setattr("sys.argv", ["filemaid", "logs", "--tail", "2"])
    main()
    captured = capsys.readouterr()
    assert "line2" in captured.out
    assert "line3" in captured.out


def test_main_review_command_opens_finder(temp_env, monkeypatch):
    home = temp_env
    config_dir = home / ".config" / "filemaid"
    config_dir.mkdir(parents=True)
    config = {
        "ollama_url": "http://localhost:11434",
        "model": "dummy",
        "watch_dirs": ["~/Desktop"],
        "allowed_dirs": ["~/Desktop", "~/.filemaid/review"],
        "allowed_cleaners": ["pip"],
        "review_dir": "~/.filemaid/review",
        "log_path": "~/.local/share/filemaid/filemaid.log",
        "db_path": "~/.local/share/filemaid/filemaid.db",
        "tags": True,
        "min_age_hours": 0,
        "categories": {"Unknown": "~/.filemaid/review"},
        "safe_delete_patterns": [],
        "age_rules": [],
        "dev_cleanup": {"pip": {"enabled": True, "mode": "safe"}},
    }
    config_dir.joinpath("config.json").write_text(json.dumps(config))
    review_dir = home / ".filemaid" / "review"
    review_dir.mkdir(parents=True)

    calls = []
    monkeypatch.setattr("os.system", lambda cmd: calls.append(cmd))
    monkeypatch.setattr("sys.argv", ["filemaid", "review", "--open"])
    main()
    assert any(str(review_dir) in c for c in calls)


def test_tail_logs_missing_file(capsys, tmp_path):
    tail_logs(str(tmp_path / "missing.log"), 5)
    captured = capsys.readouterr()
    assert "log file not found" in captured.out


def test_review_queue_empty(capsys, tmp_path):
    review_queue(str(tmp_path / "empty_review"), open_finder=False)
    captured = capsys.readouterr()
    assert "review queue is empty" in captured.out


def test_run_cleanup_prints_result(tmp_path, capsys, monkeypatch):
    review_dir = tmp_path / "review"
    review_dir.mkdir()
    config = {
        "allowed_dirs": [str(tmp_path / "Desktop"), str(review_dir)],
        "review_dir": str(review_dir),
        "categories": {"Unknown": str(review_dir)},
        "tags": True,
        "safe_delete_patterns": [],
        "age_rules": [],
        "dev_cleanup": {"pip": {"enabled": True, "mode": "safe"}},
        "allowed_cleaners": ["pip"],
    }
    monkeypatch.setattr("filemaid.cleaners.pip.can_run", lambda: True)
    monkeypatch.setattr("filemaid.cleaners.pip.run", lambda dry_run, cfg: "pip: cleaned")
    run_cleanup(dry_run=False, config=config)
    captured = capsys.readouterr()
    assert "pip: cleaned" in captured.out
