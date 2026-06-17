"""Configuration loader with path expansion and defaults."""
import json
import os

DEFAULTS = {
    "ollama_url": "http://localhost:11434",
    "model": "gemma4:26b-a4b-it-qat",
    "watch_dirs": ["~/Desktop", "~/Downloads"],
    "allowed_dirs": [
        "~/Desktop",
        "~/Downloads",
        "~/Documents/Archive",
        "~/.filemaid/review",
    ],
    "allowed_cleaners": ["docker", "npm", "cargo", "pip", "brew", "xcode"],
    "review_dir": "~/.filemaid/review",
    "log_path": "~/.local/share/filemaid/filemaid.log",
    "db_path": "~/.local/share/filemaid/filemaid.db",
    "tags": True,
    "min_age_hours": 0,
    "categories": {
        "Screenshots": "~/Documents/Archive/Screenshots",
        "Documents": "~/Documents/Archive/Documents",
        "Receipts": "~/Documents/Archive/Receipts",
        "Images": "~/Documents/Archive/Images",
        "Installers": "~/Documents/Archive/Installers",
        "Code": "~/Documents/Archive/Code",
        "Archives": "~/Documents/Archive/Archives",
        "Media": "~/Documents/Archive/Media",
        "Unknown": "~/.filemaid/review",
    },
    "safe_delete_patterns": [],
    "age_rules": [
        {"pattern": "~/Downloads/*.dmg", "days": 30, "action": "review"},
    ],
    "dev_cleanup": {
        "docker": {"enabled": True, "mode": "safe"},
        "npm": {"enabled": True, "mode": "safe"},
        "cargo": {"enabled": True, "mode": "safe"},
        "pip": {"enabled": True, "mode": "safe"},
        "brew": {"enabled": True, "mode": "safe"},
        "xcode": {"enabled": True, "mode": "safe"},
    },
}


def _expand(value):
    if isinstance(value, str):
        return os.path.expanduser(value)
    if isinstance(value, list):
        return [_expand(item) for item in value]
    if isinstance(value, dict):
        return {key: _expand(val) for key, val in value.items()}
    return value


def load_config(path=None):
    """Load config, expanding ~ and merging defaults."""
    path = path or os.path.expanduser("~/.config/filemaid/config.json")
    cfg = _expand(DEFAULTS.copy())
    if os.path.exists(path):
        with open(path, "r", encoding="utf-8") as f:
            user_cfg = json.load(f)
        for key, value in user_cfg.items():
            if isinstance(value, dict) and key in cfg and isinstance(cfg[key], dict):
                cfg[key].update(_expand(value))
            else:
                cfg[key] = _expand(value)
    if "Unknown" not in cfg["categories"]:
        cfg["categories"]["Unknown"] = cfg["review_dir"]
    return cfg
