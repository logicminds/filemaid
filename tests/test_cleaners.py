"""Tests for filemaid.cleaners."""
import shutil
from pathlib import Path

import pytest

from filemaid.cleaners import docker, npm, cargo, pip, brew, xcode
from filemaid.cleaners import xcode as xcode_module


@pytest.fixture
def base_config():
    return {
        "dev_cleanup": {
            "docker": {"enabled": True, "mode": "safe"},
            "npm": {"enabled": True, "mode": "safe"},
            "cargo": {"enabled": True, "mode": "safe"},
            "pip": {"enabled": True, "mode": "safe"},
            "brew": {"enabled": True, "mode": "safe"},
            "xcode": {"enabled": True, "mode": "safe"},
        }
    }


def test_docker_can_run_when_docker_present(monkeypatch):
    monkeypatch.setattr(shutil, "which", lambda cmd: "/usr/bin/docker" if cmd == "docker" else None)
    assert docker.can_run() is True


def test_docker_can_run_when_missing(monkeypatch):
    monkeypatch.setattr(shutil, "which", lambda cmd: None)
    assert docker.can_run() is False


def test_docker_safe_command(base_config, monkeypatch):
    monkeypatch.setattr(shutil, "which", lambda cmd: "/docker")
    result = docker.run(dry_run=True, config=base_config)
    assert "docker image prune" in result


def test_docker_aggressive_command(base_config, monkeypatch):
    monkeypatch.setattr(shutil, "which", lambda cmd: "/docker")
    base_config["dev_cleanup"]["docker"]["mode"] = "aggressive"
    result = docker.run(dry_run=True, config=base_config)
    assert "docker system prune" in result


def test_npm_can_run(monkeypatch):
    monkeypatch.setattr(shutil, "which", lambda cmd: "/npm" if cmd == "npm" else None)
    assert npm.can_run() is True


def test_npm_run_dry(base_config, monkeypatch):
    monkeypatch.setattr(shutil, "which", lambda cmd: "/npm" if cmd == "npm" else None)
    result = npm.run(dry_run=True, config=base_config)
    assert "would clean cache" in result


def test_cargo_can_run_requires_cargo_cache(monkeypatch):
    def fake_which(cmd):
        return "/cargo" if cmd == "cargo" else ("/cargo-cache" if cmd == "cargo-cache" else None)
    monkeypatch.setattr(shutil, "which", fake_which)
    assert cargo.can_run() is True


def test_cargo_can_run_missing_cargo_cache(monkeypatch):
    monkeypatch.setattr(shutil, "which", lambda cmd: "/cargo" if cmd == "cargo" else None)
    assert cargo.can_run() is False


def test_pip_can_run():
    assert pip.can_run() is True


def test_pip_run_dry(base_config, monkeypatch):
    result = pip.run(dry_run=True, config=base_config)
    assert "would purge cache" in result


def test_brew_can_run(monkeypatch):
    monkeypatch.setattr(shutil, "which", lambda cmd: "/brew" if cmd == "brew" else None)
    assert brew.can_run() is True


def test_brew_safe_prune(base_config, monkeypatch):
    monkeypatch.setattr(shutil, "which", lambda cmd: "/brew" if cmd == "brew" else None)
    result = brew.run(dry_run=True, config=base_config)
    assert "--prune=7" in result


def test_brew_aggressive_prune(base_config, monkeypatch):
    monkeypatch.setattr(shutil, "which", lambda cmd: "/brew" if cmd == "brew" else None)
    base_config["dev_cleanup"]["brew"]["mode"] = "aggressive"
    result = brew.run(dry_run=True, config=base_config)
    assert "--prune=all" in result


def test_xcode_can_run_when_xcodebuild_present(monkeypatch):
    monkeypatch.setattr(shutil, "which", lambda cmd: "/xcodebuild" if cmd == "xcodebuild" else None)
    assert xcode.can_run() is True


def test_xcode_can_run_when_deriveddata_exists(tmp_path, monkeypatch):
    monkeypatch.setattr(shutil, "which", lambda cmd: None)
    derived = tmp_path / "Library" / "Developer" / "Xcode" / "DerivedData"
    derived.mkdir(parents=True)
    monkeypatch.setattr(xcode_module, "DERIVED_DATA", derived)
    assert xcode.can_run() is True


def test_xcode_run_when_deriveddata_empty(base_config, tmp_path, monkeypatch):
    monkeypatch.setattr(shutil, "which", lambda cmd: None)
    derived = tmp_path / "Library" / "Developer" / "Xcode" / "DerivedData"
    derived.mkdir(parents=True)
    monkeypatch.setattr(xcode_module, "DERIVED_DATA", derived)
    result = xcode.run(dry_run=True, config=base_config)
    assert "DerivedData" in result


def test_xcode_run_when_deriveddata_missing(base_config, tmp_path, monkeypatch):
    derived = tmp_path / "Library" / "Developer" / "Xcode" / "DerivedData"
    monkeypatch.setattr(xcode_module, "DERIVED_DATA", derived)
    result = xcode.run(dry_run=True, config=base_config)
    assert "does not exist" in result
