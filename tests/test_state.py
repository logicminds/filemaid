"""Tests for filemaid.state."""
import sqlite3
from pathlib import Path

import pytest

from filemaid.state import init_db, record, find_by_hash


@pytest.fixture
def conn(tmp_path):
    db_path = tmp_path / "filemaid.db"
    return init_db(str(db_path))


def test_init_db_creates_tables(conn):
    cur = conn.execute("SELECT name FROM sqlite_master WHERE type='table'")
    tables = {row[0] for row in cur.fetchall()}
    assert "history" in tables


def test_init_db_creates_index(conn):
    cur = conn.execute("SELECT name FROM sqlite_master WHERE type='index'")
    indexes = {row[0] for row in cur.fetchall()}
    assert "idx_sha256" in indexes


def test_record_and_find(conn):
    record(
        conn,
        original="/Desktop/foo.png",
        final="/Archive/Images/foo.png",
        sha256="abc123",
        category="Images",
        tags=["image", "foo"],
        action="move",
        reason="looks like an image",
    )
    row = find_by_hash(conn, "abc123")
    assert row is not None
    assert row["sha256"] == "abc123"
    assert row["category"] == "Images"
    assert row["tags"] == "image,foo"
    assert row["action"] == "move"


def test_find_by_hash_returns_none_when_missing(conn):
    assert find_by_hash(conn, "nope") is None


def test_record_stores_empty_tags(conn):
    record(conn, "/a", "/b", "sha", "Unknown", [], "review", "uncertain")
    row = find_by_hash(conn, "sha")
    assert row["tags"] == ""
