"""SQLite state for processed files and duplicates."""
import sqlite3
from pathlib import Path

SCHEMA = """
CREATE TABLE IF NOT EXISTS history (
    id INTEGER PRIMARY KEY,
    original_path TEXT NOT NULL,
    final_path TEXT,
    sha256 TEXT,
    category TEXT,
    tags TEXT,
    action TEXT,
    reason TEXT,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_sha256 ON history(sha256);
"""


def init_db(path):
    Path(path).parent.mkdir(parents=True, exist_ok=True)
    conn = sqlite3.connect(path)
    conn.row_factory = sqlite3.Row
    conn.executescript(SCHEMA)
    conn.commit()
    return conn


def record(conn, original, final, sha256, category, tags, action, reason):
    tags_str = ",".join(tags) if isinstance(tags, list) else str(tags)
    conn.execute(
        """
        INSERT INTO history (original_path, final_path, sha256, category, tags, action, reason)
        VALUES (?, ?, ?, ?, ?, ?, ?)
        """,
        (str(original), str(final), sha256, category, tags_str, action, reason),
    )
    conn.commit()


def find_by_hash(conn, sha256):
    cur = conn.execute(
        "SELECT * FROM history WHERE sha256 = ? ORDER BY created_at DESC LIMIT 1",
        (sha256,),
    )
    row = cur.fetchone()
    if row is None:
        return None
    return {key: row[key] for key in row.keys()}
