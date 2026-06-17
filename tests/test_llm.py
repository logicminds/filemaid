"""Tests for filemaid.llm."""
from pathlib import Path

import pytest

from filemaid.llm import (
    Decision,
    _extract_json,
    _parse_response,
    classify_file,
)


@pytest.fixture
def base_config(tmp_path):
    categories = {
        "Screenshots": str(tmp_path / "Screenshots"),
        "Documents": str(tmp_path / "Documents"),
        "Images": str(tmp_path / "Images"),
        "Unknown": str(tmp_path / "review"),
    }
    return {
        "ollama_url": "http://localhost:11434",
        "model": "dummy",
        "categories": categories,
    }


def test_decision_defaults():
    d = Decision()
    assert d.category == "Unknown"
    assert d.action == "review"
    assert d.tags == []
    assert d.destination == ""


def test_extract_json_plain():
    raw = '{"category": "Images", "action": "move"}'
    assert _extract_json(raw) == {"category": "Images", "action": "move"}


def test_extract_json_with_markdown_fence():
    raw = '```json\n{"category": "Images", "action": "move"}\n```'
    assert _extract_json(raw) == {"category": "Images", "action": "move"}


def test_extract_json_invalid_returns_none():
    assert _extract_json("not json") is None


def test_parse_response_generate_style(base_config):
    data = {"response": '{"category": "Images", "tags": ["a"], "action": "move", "reason": "x"}'}
    decision = _parse_response(data, set(base_config["categories"].keys()))
    assert decision.category == "Images"
    assert decision.action == "move"
    assert decision.tags == ["a"]


def test_parse_response_tool_call_style(base_config):
    data = {
        "message": {
            "tool_calls": [
                {
                    "function": {
                        "arguments": {
                            "category": "Documents",
                            "tags": ["doc"],
                            "action": "move",
                            "reason": "text file",
                        }
                    }
                }
            ]
        }
    }
    decision = _parse_response(data, set(base_config["categories"].keys()))
    assert decision.category == "Documents"
    assert decision.tags == ["doc"]


def test_parse_response_unknown_category_coerced(base_config):
    data = {"response": '{"category": "Banana", "tags": [], "action": "move", "reason": "x"}'}
    decision = _parse_response(data, set(base_config["categories"].keys()))
    assert decision.category == "Unknown"


def test_parse_response_invalid_action_coerced(base_config):
    data = {"response": '{"category": "Images", "tags": [], "action": "explode", "reason": "x"}'}
    decision = _parse_response(data, set(base_config["categories"].keys()))
    assert decision.action == "review"


def test_classify_file_uses_generate_endpoint(base_config, tmp_path):
    text_file = tmp_path / "note.txt"
    text_file.write_text("hello world")

    captured = {}

    def fake_post(url, body, timeout):
        captured["url"] = url
        captured["model"] = body["model"]
        return {"response": '{"category": "Documents", "tags": ["txt"], "action": "move", "reason": "text"}'}

    base_config["_http_post"] = fake_post
    base_config["ollama_endpoint"] = "generate"

    decision = classify_file(text_file, base_config)
    assert decision.category == "Documents"
    assert captured["url"].endswith("/api/generate")


def test_classify_file_uses_chat_endpoint(base_config, tmp_path):
    text_file = tmp_path / "note.txt"
    text_file.write_text("hello world")

    captured = {}

    def fake_post(url, body, timeout):
        captured["url"] = url
        return {
            "message": {
                "tool_calls": [
                    {"function": {"arguments": {"category": "Documents", "tags": ["txt"], "action": "move", "reason": "text"}}}
                ]
            }
        }

    base_config["_http_post"] = fake_post
    base_config["ollama_endpoint"] = "chat"

    decision = classify_file(text_file, base_config)
    assert decision.category == "Documents"
    assert captured["url"].endswith("/api/chat")


def test_classify_file_falls_back_on_error(base_config, tmp_path):
    text_file = tmp_path / "note.txt"
    text_file.write_text("hello")

    def fake_post(url, body, timeout):
        raise RuntimeError("connection refused")

    base_config["_http_post"] = fake_post
    decision = classify_file(text_file, base_config)
    assert decision.category == "Unknown"
    assert decision.action == "review"
    assert "connection refused" in decision.reason


def test_classify_file_retries_without_images_on_failure(base_config, tmp_path):
    img = tmp_path / "img.png"
    img.write_bytes(b"\x89PNG\r\n\x1a\nfake")

    calls = []

    def fake_post(url, body, timeout):
        calls.append(len(body.get("images", [])))
        if len(body.get("images", [])) > 0:
            raise RuntimeError("vision not supported")
        return {"response": '{"category": "Images", "tags": [], "action": "move", "reason": "metadata"}'}

    base_config["_http_post"] = fake_post
    base_config["ollama_endpoint"] = "generate"
    decision = classify_file(img, base_config)
    assert decision.category == "Images"
    assert 0 in calls


def test_classify_file_skips_hidden_files_no_special_handling(base_config, tmp_path):
    # classify_file itself does not filter hidden files; that happens in __main__
    text_file = tmp_path / ".hidden"
    text_file.write_text("secret")

    def fake_post(url, body, timeout):
        return {"response": '{"category": "Documents", "tags": [], "action": "move", "reason": "x"}'}

    base_config["_http_post"] = fake_post
    decision = classify_file(text_file, base_config)
    assert decision.category == "Documents"
