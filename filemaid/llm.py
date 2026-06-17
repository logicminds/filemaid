"""Ollama-based file classifier with injectable transport for tests."""
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


PROMPT_TEMPLATE = """You are a macOS file classifier. Pick exactly one category from: {categories}. Suggest 1-3 concise Finder tags. Decide the action.

Actions:
- move: the file clearly belongs to a category.
- delete: only obvious trash, installers, or duplicates.
- review: ambiguous, sensitive, or cannot classify.

File:
- path: {path}
- name: {name}
- extension: {ext}
- size: {size} bytes
- modified: {mtime}
{extra}

Return a single compact JSON object and nothing else. Leave destination empty.
{{"category": "...", "tags": ["..."], "action": "...", "destination": "", "reason": "..."}}"""


def _read_text_snippet(path: Path, limit: int = 2048) -> str:
    try:
        with open(path, "r", encoding="utf-8", errors="ignore") as f:
            return f.read(limit)
    except Exception:
        return ""


def _build_prompt(path: Path, config: dict) -> tuple[str, list[str]]:
    stat = path.stat()
    categories = ", ".join(config["categories"].keys())
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

    prompt = PROMPT_TEMPLATE.format(
        categories=categories,
        path=str(path),
        name=path.name,
        ext=ext,
        size=stat.st_size,
        mtime=mtime,
        extra="\n".join(extras),
    )
    return prompt, images


def _extract_json(raw: str):
    text = raw.strip()
    if text.startswith("```"):
        text = text.split("\n", 1)[1]
        if text.rstrip().endswith("```"):
            text = text.rstrip()[:-3].strip()
    start = text.find("{")
    end = text.rfind("}")
    if start == -1 or end == -1 or end <= start:
        return None
    try:
        return json.loads(text[start : end + 1])
    except Exception:
        return None


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


def _http_post(url: str, body: dict, timeout: float) -> dict:
    """Default HTTP transport. Swappable in tests via config['_http_post']."""
    req = urllib.request.Request(
        url,
        data=json.dumps(body).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    with urllib.request.urlopen(req, timeout=timeout) as resp:
        return json.loads(resp.read().decode("utf-8"))


def _request_generate(config: dict, prompt: str, images: list[str]) -> dict:
    body = {
        "model": config["model"],
        "system": (
            "You classify files for a macOS file manager. "
            "Output valid JSON only with keys category, tags, action, destination, reason. "
            "No markdown, no code fences, no extra text."
        ),
        "prompt": prompt,
        "images": images,
        "stream": False,
        "format": "json",
        "options": {"temperature": 0.2, "num_predict": 512},
    }
    transport = config.get("_http_post", _http_post)
    return transport(f"{config['ollama_url']}/api/generate", body, timeout=120)


def _request_chat_tool(config: dict, prompt: str, images: list[str], categories: list[str]) -> dict:
    body = {
        "model": config["model"],
        "messages": [
            {
                "role": "system",
                "content": (
                    "You classify files for a macOS file manager. Use the classify_file tool. "
                    "If the category is clear, action should be move. "
                    "Use delete only for obvious trash, installers, or duplicates. "
                    "Use review only when ambiguous, sensitive, or unclassifiable."
                ),
            },
            {
                "role": "user",
                "content": prompt,
                "images": images,
            },
        ],
        "tools": [_tool_schema(categories)],
        "stream": False,
        "options": {"temperature": 0.2, "num_predict": 512},
    }
    transport = config.get("_http_post", _http_post)
    return transport(f"{config['ollama_url']}/api/chat", body, timeout=120)


def _parse_response(data: dict, categories: set[str]) -> Decision | None:
    # Try tool-calling output first
    tool_calls = data.get("message", {}).get("tool_calls", [])
    if tool_calls:
        args = tool_calls[0].get("function", {}).get("arguments", {})
        if isinstance(args, str):
            try:
                args = json.loads(args)
            except Exception:
                return None
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

    # Fall back to generate-style JSON
    raw = data.get("response", "")
    parsed = _extract_json(raw)
    if parsed is None:
        return None

    category = parsed.get("category", "Unknown")
    if category not in categories:
        category = "Unknown"

    action = parsed.get("action", "review")
    if action not in ("move", "delete", "review"):
        action = "review"

    return Decision(
        category=category,
        tags=parsed.get("tags", []) or [],
        action=action,
        destination=parsed.get("destination", "") or "",
        reason=parsed.get("reason", ""),
    )


def classify_file(path: Path, config: dict) -> Decision:
    prompt, images = _build_prompt(path, config)
    categories = set(config["categories"].keys())
    endpoint = config.get("ollama_endpoint", "auto")

    last_error = ""

    def _try_generate(use_images: bool):
        local_prompt, local_images = _build_prompt(path, config)
        return _request_generate(config, local_prompt, local_images if use_images else [])

    def _try_chat(use_images: bool):
        local_prompt, local_images = _build_prompt(path, config)
        return _request_chat_tool(config, local_prompt, local_images if use_images else [], list(categories))

    strategies = []
    if endpoint in ("auto", "chat"):
        strategies.append(_try_chat)
    if endpoint in ("auto", "generate"):
        strategies.append(_try_generate)

    for strategy in strategies:
        for use_images in (True, False):
            if use_images and not images:
                continue
            try:
                data = strategy(use_images)
            except Exception as exc:
                last_error = str(exc)
                continue

            decision = _parse_response(data, categories)
            if decision is not None:
                return decision

    return Decision(
        category="Unknown",
        action="review",
        reason=f"ollama error: {last_error}" if last_error else "could not parse model response",
    )
