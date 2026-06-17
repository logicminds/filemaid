"""Tests for filemaid.config."""
import json
import os
from pathlib import Path

import pytest

from filemaid.config import load_config


@pytest.fixture
def temp_home(tmp_path, monkeypatch):
    home = tmp_path / "home"
    home.mkdir()
    monkeypatch.setenv("HOME", str(home))
    yield home


def test_load_config_uses_defaults(temp_home):
    config = load_config()
    assert config["ollama_url"] == "http://localhost:11434"
    assert config["model"] == "filemaid-gemma4-26b"
    assert config["tags"] is True
    assert config["min_age_hours"] == 0
    assert Path(config["review_dir"]).name == "review"


def test_load_config_expands_tilde(temp_home):
    config = load_config()
    assert str(config["watch_dirs"][0]) == str(temp_home / "Desktop")
    assert str(config["review_dir"]) == str(temp_home / ".filemaid" / "review")


def test_load_config_merges_user_values(temp_home):
    cfg_dir = temp_home / ".config" / "filemaid"
    cfg_dir.mkdir(parents=True)
    cfg_path = cfg_dir / "config.json"
    cfg_path.write_text(json.dumps({"model": "tiny-model", "tags": False}))

    config = load_config(str(cfg_path))
    assert config["model"] == "tiny-model"
    assert config["tags"] is False
    # Defaults still present
    assert config["ollama_url"] == "http://localhost:11434"


def test_load_config_unknown_category_defaults_to_review_dir(temp_home):
    cfg_dir = temp_home / ".config" / "filemaid"
    cfg_dir.mkdir(parents=True)
    cfg_path = cfg_dir / "config.json"
    cfg_path.write_text(
        json.dumps(
            {
                "categories": {
                    "Screenshots": "~/Pictures",
                }
            }
        )
    )
    config = load_config(str(cfg_path))
    assert "Unknown" in config["categories"]
    assert config["categories"]["Unknown"] == str(temp_home / ".filemaid" / "review")


def test_load_config_missing_file_uses_defaults(temp_home):
    config = load_config(str(temp_home / "nonexistent.json"))
    assert config["model"] == "filemaid-gemma4-26b"
