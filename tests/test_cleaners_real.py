"""Tests for cleaner real (non-dry-run) execution paths."""
import shutil
import subprocess
from pathlib import Path

import pytest

from filemaid.cleaners import docker, npm, cargo, pip, brew, xcode


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


def test_docker_real_run_success(base_config, monkeypatch):
    monkeypatch.setattr(shutil, "which", lambda cmd: "/docker")

    class FakeResult:
        stdout = "Total reclaimed space: 0B\n"

    monkeypatch.setattr(subprocess, "run", lambda cmd, **kw: FakeResult())
    result = docker.run(dry_run=False, config=base_config)
    assert "docker image prune" in result.command


def test_docker_real_run_failure(base_config, monkeypatch):
    monkeypatch.setattr(shutil, "which", lambda cmd: "/docker")

    def fake_run(cmd, **kw):
        raise RuntimeError("docker daemon not running")

    monkeypatch.setattr(subprocess, "run", fake_run)
    result = docker.run(dry_run=False, config=base_config)
    assert result.status == "failed"


def test_npm_real_run_success(base_config, monkeypatch):
    monkeypatch.setattr(shutil, "which", lambda cmd: "/npm")

    class FakeResult:
        stdout = "npm cache cleaned\n"

    monkeypatch.setattr(subprocess, "run", lambda cmd, **kw: FakeResult())
    result = npm.run(dry_run=False, config=base_config)
    assert "cache cleaned" in result.detail


def test_npm_real_run_failure(base_config, monkeypatch):
    monkeypatch.setattr(shutil, "which", lambda cmd: "/npm")

    def fake_run(cmd, **kw):
        raise RuntimeError("npm error")

    monkeypatch.setattr(subprocess, "run", fake_run)
    result = npm.run(dry_run=False, config=base_config)
    assert result.status == "failed"


def test_brew_real_run_success(base_config, monkeypatch):
    monkeypatch.setattr(shutil, "which", lambda cmd: "/brew")

    class FakeResult:
        stdout = "Cleaned up\n"

    monkeypatch.setattr(subprocess, "run", lambda cmd, **kw: FakeResult())
    result = brew.run(dry_run=False, config=base_config)
    assert "cleanup --prune=7" in result.detail


def test_pip_real_run_success(base_config, monkeypatch):
    class FakeResult:
        stdout = "Cache purged\n"

    monkeypatch.setattr(subprocess, "run", lambda cmd, **kw: FakeResult())
    result = pip.run(dry_run=False, config=base_config)
    assert "cache purged" in result.detail


def test_cargo_real_run_success(base_config, monkeypatch):
    def fake_which(cmd):
        return "/cargo" if cmd == "cargo" else ("/cargo-cache" if cmd == "cargo-cache" else None)

    monkeypatch.setattr(shutil, "which", fake_which)

    class FakeResult:
        stdout = "cargo cache autocleaned\n"

    monkeypatch.setattr(subprocess, "run", lambda cmd, **kw: FakeResult())
    result = cargo.run(dry_run=False, config=base_config)
    assert "autocleaned" in result.detail


def test_xcode_real_run_safe(base_config, tmp_path, monkeypatch):
    derived = tmp_path / "Library" / "Developer" / "Xcode" / "DerivedData"
    derived.mkdir(parents=True)
    old_dir = derived / "old-project"
    old_dir.mkdir()
    # Make directory old
    import time
    old_time = time.time() - 40 * 86400
    import os
    os.utime(old_dir, (old_time, old_time))

    monkeypatch.setattr(shutil, "which", lambda cmd: None)
    from filemaid.cleaners import xcode as xcode_module
    monkeypatch.setattr(xcode_module, "DERIVED_DATA", derived)
    result = xcode.run(dry_run=False, config=base_config)
    assert "Removed" in result.detail or "removed" in result.detail
