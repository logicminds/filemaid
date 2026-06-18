"""Structured result object for dev cleaners."""
from __future__ import annotations

from dataclasses import dataclass


@dataclass
class CleanupResult:
    """Result returned by a cleaner run."""

    name: str
    status: str
    saved: int | None = None
    saved_human: str = ""
    detail: str = ""
    command: str = ""

    def to_dict(self) -> dict:
        return {
            "name": self.name,
            "status": self.status,
            "saved": self.saved,
            "saved_human": self.saved_human,
            "detail": self.detail,
            "command": self.command,
        }
