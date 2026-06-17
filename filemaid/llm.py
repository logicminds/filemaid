"""Ollama-based file classifier using tool calls for structured output."""
import base64
import json
import urllib.request
from dataclasses import dataclass, field
from datetime import datetime
from pathlib import Path

IMAGE_EXTS = {".png", ".jpg", ".jpeg", ".gif", ".webp", ".heic"}
TEXT_EXTS = {
    ".txt",
    ".md",
    ".csv",
    ".json",
    ".xml",
    ".yaml",
    ".yml",
    ".py",
    ".js",
    ".ts",
    ".jsx",
    ".tsx",
    ".html",
    ".css",
    ".sh",
    ".zsh",
    ".bash",
    ".swift",
    ".c",
    ".cpp",
    ".h",
    ".rs",
    ".go",
    ".java",
    ".kt",
    ".rb",
    ".php",
    ".pl",
    ".sql",
}


@dataclass
class Decision:
    category: str = "Unknown"
    tags: list[str] = field(default_factory=list)
    action: str = "review"
    destination: str = ""
    reason: str = ""


def _read_text_snippet(path: Path, limit: int = 2048) -> str:
    try:
        with open(path, "r", encoding="utf-8", errors="ignore") as f:
            return f.read(limit)
    except Exception:
        return ""


def _build_message(path: Path, config: dict):
    stat = path.stat()
    categories = list(config["categories"].keys())
    mtime = datetime.fromtimestamp(stat.st_mtime).isoformat()
    ext = path.suffix.lower()

    images = []
    extras = []
    if ext in IMAGE_EXTS:
        extras.append("The image is attached; use its content to classify.")
        with open(path, "rb") as f:
            images.append(base64.b64encode(f.read()).decode("utf-8"))
    elif ext in TEXT_EXTS:
        snippet = _read_text_snippet(path)
        extras.append(f"First 2048 bytes:\n{snippet}")

    content = (
        f"Classify this file.\n"
        f"- path: {path}\n"
        f"- name: {path.name}\n"
        f"- extension: {ext}\n"
        f"- size: {stat.st_size} bytes\n"
        f"- modified: {mtime}\n" + "\n".join(extras)
    )
    return content, images, categories


def _tool_schema(categories: list[str]) -> dict:
    return {
        "type": "function",
        "function": {
            "name": "classify_file",
            "description": "Classify a file and decide what to do with it",
            "parameters": {
                "type": "object",
                "properties": {
                    "category": {
                        "type": "string",
                        "enum": categories,
                    },
                    "tags": {
                        "type": "array",
                        "items": {"type": "string"},
                    },
                    "action": {
                        "type": "string",
                        "enum": ["move", "delete", "review"],
                    },
                    "reason": {"type": "string"},
                },
                "required": ["category", "tags", "action", "reason"],
            },
        },
    }


def classify_file(path: Path, config: dict) -> Decision:
    content, images, categories = _build_message(path, config)
    body = {
        "model": config["model"],
        "messages": [
            {
                "role": "system",
                "content": (
                    "You classify files for a macOS file manager. "
                    "Use the classify_file tool. "
                    "If the category is clear, action should be move. "
                    "Use delete only for obvious trash, installers, or duplicates. "
                    "Use review only when ambiguous, sensitive, or unclassifiable."
                ),
            },
            {
                "role": "user",
                "content": content,
                "images": images,
            },
        ],
        "tools": [_tool_schema(categories)],
        "stream": False,
        "options": {"temperature": 0.2, "num_predict": 512},
    }

    data = None
    for attempt in range(2):
        try:
            req = urllib.request.Request(
                f"{config['ollama_url']}/api/chat",
                data=json.dumps(body).encode("utf-8"),
                headers={"Content-Type": "application/json"},
                method="POST",
            )
            with urllib.request.urlopen(req, timeout=120) as resp:
                data = json.loads(resp.read().decode("utf-8"))
        except Exception as exc:
            if attempt == 0:
                import time
                time.sleep(0.5)
                continue
            return Decision(
                category="Unknown",
                action="review",
                reason=f"ollama error: {exc}",
            )
        tool_calls = data.get("message", {}).get("tool_calls", [])
        if tool_calls:
            break
        if attempt == 0:
            import time
            time.sleep(0.5)

    if not data:
        return Decision(
            category="Unknown",
            action="review",
            reason="ollama returned no response after retry",
        )

    tool_calls = data.get("message", {}).get("tool_calls", [])
    if not tool_calls:
        raw = data.get("message", {}).get("content", "")
        return Decision(
            category="Unknown",
            action="review",
            reason=f"no tool call; raw={raw[:200]}",
        )

    args = tool_calls[0].get("function", {}).get("arguments", {})
    if isinstance(args, str):
        try:
            args = json.loads(args)
        except Exception as exc:
            return Decision(
                category="Unknown",
                action="review",
                reason=f"tool arguments parse error: {exc}",
            )

    category = args.get("category", "Unknown")
    if category not in categories:
        category = "Unknown"

    action = args.get("action", "review")
    if action not in ("move", "delete", "review"):
        action = "review"

    return Decision(
        category=category,
        tags=args.get("tags", []) or [],
        action=action,
        destination="",
        reason=args.get("reason", ""),
    )
