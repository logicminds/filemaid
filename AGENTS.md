# Repository Guidelines

## Project Overview

`filemaid` is a local, macOS-only file organizer. It classifies files dropped on `~/Desktop` or in `~/Downloads` using a local Ollama LLM, moves them into categorized archive folders, applies Finder tags, and quarantines uncertain items for review. It also runs scheduled cleanup of stale development artifacts (Docker images, npm/cargo/pip/brew caches, Xcode DerivedData).

Two triggers are supported:

- **macOS Shortcuts folder automations** — instant per-file processing, no Full Disk Access required.
- **`biz.logicminds.filemaid.scan` LaunchAgent** — periodic scan every 15 minutes.
- **`biz.logicminds.filemaid.cleanup` LaunchAgent** — cleanup at 06:00, 12:00, 18:00, and 23:00.

## Architecture & Data Flow

The codebase is a small, synchronous, procedural Python CLI. There is no async framework and no dependency injection; state flows through a `config` dict and a `sqlite3.Connection`.

```
filemaid process <paths>
  -> load_config()               (filemaid/config.py)
  -> init_db()                   (filemaid/state.py)
  -> process_paths()
       -> _compute_hash()        (filemaid/actions.py)
       -> _check_age_rule()      (filemaid/__main__.py)
       -> classify_file()        (filemaid/llm.py)  → Decision
       -> apply()                (filemaid/actions.py)
            -> move / tag / trash / review
       -> record()               (filemaid/state.py)

filemaid cleanup
  -> CLEANERS registry           (filemaid/cleaners/__init__.py)
       -> can_run() / run()      (per-cleaner modules)
```

The shared value object is `Decision` (`filemaid/llm.py`):

```python
@dataclass
class Decision:
    category: str
    tags: list[str]
    action: str        # "move" | "delete" | "review"
    destination: str
    reason: str
```

## Key Directories

| Directory | Purpose |
|-----------|---------|
| `filemaid/` | Core Python package: CLI, classifier, actions, state, config. |
| `filemaid/cleaners/` | Plugin modules for dev-artifact cleanup; registered in `__init__.py`. |
| Project root | Packaging (`pyproject.toml`), default config (`config.json`), LaunchAgent plists, install/uninstall scripts, and the implementation plan. |

## Development Commands

| Task | Command |
|------|---------|
| Syntax-check all Python files | `cd /Users/opselite/Projects/filemaid && for f in filemaid/*.py filemaid/cleaners/*.py; do python3 -m py_compile "$f"; done` |
| Install/reinstall agents and wrapper | `./install.sh` |
| Uninstall agents and wrapper | `./uninstall.sh` |
| Process files manually | `~/.local/bin/filemaid process ~/Desktop/foo.png ~/Downloads/bar.pdf` |
| Scan watch dirs | `~/.local/bin/filemaid scan` or `~/.local/bin/filemaid scan --dir ~/Downloads` |
| Dry-run cleaners | `~/.local/bin/filemaid cleanup --dry-run` |
| Run cleaners now | `~/.local/bin/filemaid cleanup` |
| View/approve review queue | `~/.local/bin/filemaid review` or `~/.local/bin/filemaid review --open` |
| Tail logs | `~/.local/bin/filemaid logs --tail 50` |
| Show resolved config | `~/.local/bin/filemaid config` |

There is no test/lint runner today (see Testing & QA).

## Code Conventions & Common Patterns

- **Python standard library only.** `pyproject.toml` declares `dependencies = []`.
- **Python 3.9+ syntax.** Use `from __future__ import annotations` where union types like `X | None` appear.
- **Config-driven behavior.** Most rules live in `~/.config/filemaid/config.json` and are merged with `DEFAULTS` in `filemaid/config.py`. All paths containing `~` are expanded.
- **Whitelist safety.** `allowed_dirs` gates both source and destination paths; `allowed_cleaners` gates which cleaners may run.
- **Fail-safe classification.** Any LLM error, parse failure, timeout, or ambiguous result becomes `category="Unknown"`, `action="review"`.
- **Delete safety.** `action="delete"` is only honored if the file matches `safe_delete_patterns` or is a duplicate; otherwise it is coerced to `"review"`.
- **Pattern matching.** `safe_delete_patterns` and `age_rules` use `fnmatch.fnmatchcase` against the path relative to `~`.
- **Cleaner plugin contract.** Each cleaner module exposes:
  ```python
  def can_run() -> bool
  def run(dry_run: bool, config: dict) -> str
  ```
- **Subprocess calls are fire-and-forget.** `subprocess.run(..., check=False)` is used for Finder scripts and external tools; failures are logged but do not abort moves.
- **macOS-specific integration.** Finder tags are written via `xattr` + `mdimport`; trash uses `osascript` "Finder delete".

## Important Files

| File | Role |
|------|------|
| `filemaid/__main__.py` | CLI entry point and orchestration (`process`, `scan`, `cleanup`, `review`, `logs`, `config`). |
| `filemaid/llm.py` | Ollama classifier, `Decision` dataclass, image/text prompt building. |
| `filemaid/actions.py` | Applies decisions: whitelist, duplicates, moves, tags, trash, review quarantine. |
| `filemaid/state.py` | SQLite schema and helpers (`init_db`, `record`, `find_by_hash`). |
| `filemaid/config.py` | Default config, merge logic, `~` expansion. |
| `filemaid/cleaners/__init__.py` | `CLEANERS` registry. |
| `filemaid/cleaners/{docker,npm,cargo,pip,brew,xcode}.py` | Individual dev-artifact cleaners. |
| `config.json` | Default user-facing configuration (copied to `~/.config/filemaid/config.json` on install). |
| `pyproject.toml` | Setuptools packaging metadata; console script `filemaid = filemaid.__main__:main`. |
| `install.sh` / `uninstall.sh` | Agent install/remove scripts. |
| `biz.logicminds.filemaid.scan.plist` | LaunchAgent for periodic scan. |
| `biz.logicminds.filemaid.cleanup.plist` | LaunchAgent for scheduled cleanups. |
| `local://mac-file-automation-plan.md` | Full implementation plan. **Note:** the plan text still references `com.opselite.*`; the actual plists/scripts use `biz.logicminds.*`. |

## Runtime/Tooling Preferences

- **Runtime:** Python ≥ 3.9, no third-party Python packages required.
- **Platform:** macOS only (uses `launchctl`, `osascript`, `xattr`, `mdimport`, Finder tags).
- **External dependency:** A running Ollama server at `http://localhost:11434` with the model configured in `~/.config/filemaid/config.json` (default `gemma4:26b-a4b-it-qat`).
- **Shell scripts:** `install.sh` and `uninstall.sh` are `zsh` scripts.
- **LaunchAgent management:** Uses `launchctl bootstrap gui/$(id -u)` / `launchctl bootout gui/$(id -u)`.
- **Path assumptions:** The wrapper is installed to `~/.local/bin/filemaid`; runtime data goes to `~/.local/share/filemaid/`; config to `~/.config/filemaid/`; review queue to `~/.filemaid/review/`.
- **TCC note:** The scan LaunchAgent may be denied read access to `~/Desktop`/`~/Downloads` until `/usr/bin/python3` is granted Full Disk Access. The Shortcuts folder-automation path does not need this.

## Testing & QA

- **No test infrastructure currently exists.** There are no `pytest`, `unittest`, `mypy`, `ruff`, `black`, CI, or coverage configs in `pyproject.toml` or elsewhere.
- **Manual QA workflow:**
  1. `./install.sh`
  2. Drop a test file on `~/Desktop` or run `filemaid process <path>`.
  3. Verify expected archive folder and Finder tags with `ls -R ~/Documents/Archive` and `mdls -name kMDItemUserTags <path>`.
  4. Run `filemaid cleanup --dry-run` and inspect output/log.
- **Adding tests:** A contributor would need to add a test framework (e.g., `pytest`) and likely mock Ollama and external tools like `xattr`/`osascript`.
