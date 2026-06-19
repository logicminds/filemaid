# Repository Guidelines

## Project Overview

`filemaid` is a local, macOS-only file organizer. It classifies files dropped on `~/Desktop` or in `~/Downloads` using a local Ollama LLM, moves them into categorized archive folders, applies Finder tags, and quarantines uncertain items for review. It also runs scheduled cleanup of stale development artifacts (Docker images, npm/cargo/pip/brew caches, Xcode DerivedData) and stale review-queue items.

Two triggers are supported:

- **macOS Shortcuts folder automations** — instant per-file processing, no Full Disk Access required.
- **`biz.logicminds.filemaid.scan` LaunchAgent** — optional periodic scan every 15 minutes. The installer asks whether to enable it; disable with `./install.sh --no-scan`.
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
| `filemaid/cleaners/` | Plugin modules for dev-artifact and review-queue cleanup; registered in `__init__.py`. |
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
- **Python 3.12+ syntax.** `pyproject.toml` requires `requires-python = ">=3.12"`; `install.sh` checks for Python 3.12+ before installing.
- **Config-driven behavior.** Most rules live in `~/.config/filemaid/config.json` and are merged with `DEFAULTS` in `filemaid/config.py`. All paths containing `~` are expanded.
- **Whitelist safety.** `allowed_dirs` gates both source and destination paths; `allowed_cleaners` gates which cleaners may run.
- **Fail-safe classification.** Any LLM error, parse failure, timeout, or ambiguous result becomes `category="Unknown"`, `action="review"`.
- **Delete safety.** `action="delete"` is only honored if the file matches `safe_delete_patterns` or is a duplicate; otherwise it is coerced to `"review"`.
- **Pattern matching.** `safe_delete_patterns` and `age_rules` use `fnmatch.fnmatchcase` against the path relative to `~`.
- **Cleaner plugin contract.** Each cleaner module exposes:
  ```python
  def can_run() -> bool
  def run(dry_run: bool, config: dict) -> CleanupResult
  ```
  where `CleanupResult` lives in `filemaid/cleaners/_result.py` and carries `name`, `status`, `saved` (bytes), `saved_human`, `detail`, and `command`.
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
| `filemaid/cleaners/{docker,npm,cargo,pip,brew,xcode,review}.py` | Individual dev-artifact cleaners (plus `review.py` for review-queue cleanup). |
| `config.json` | Default user-facing configuration (copied to `~/.config/filemaid/config.json` on install). |
| `pyproject.toml` | Setuptools packaging metadata; console script `filemaid = filemaid.__main__:main`. |
| `install.sh` / `uninstall.sh` | Agent install/remove scripts. |
| `biz.logicminds.filemaid.scan.plist` | LaunchAgent for periodic scan. |
| `biz.logicminds.filemaid.cleanup.plist` | LaunchAgent for scheduled cleanups. |
| `local://mac-file-automation-plan.md` | Full implementation plan. **Note:** the plan text still references `com.opselite.*`; the actual plists/scripts use `biz.logicminds.*`. |

## Runtime/Tooling Preferences

- **Runtime:** Python ≥ 3.12; Python 3.14 is recommended. Install via Homebrew (`brew install python@3.14`, or `python@3.13` / `python@3.12`). No third-party Python packages are required.
- **Platform:** macOS only (uses `launchctl`, `osascript`, `xattr`, `mdimport`, Finder tags).
- **External dependency:** A running Ollama server at `http://localhost:11434` with the model configured in `~/.config/filemaid/config.json` (default `gemma4:26b-a4b-it-qat`).
- **Shell scripts:** `install.sh` and `uninstall.sh` are `zsh` scripts.
- **LaunchAgent management:** Uses `launchctl bootstrap gui/$(id -u)` / `launchctl bootout gui/$(id -u)`.
- **Path assumptions:** The wrapper is installed to `~/.local/bin/filemaid`; runtime data goes to `~/.local/share/filemaid/`; config to `~/.config/filemaid/`; review queue to `~/.filemaid/review/`.
- **TCC note:** The scan LaunchAgent may be denied read access to `~/Desktop`/`~/Downloads` until the Python interpreter selected by `install.sh` (e.g. `/opt/homebrew/bin/python3.14`) is granted Full Disk Access in System Settings → Privacy & Security → Full Disk Access. The Shortcuts folder-automation path does not need this.

## Testing & QA

- **No test infrastructure currently exists.** There are no `pytest`, `unittest`, `mypy`, `ruff`, `black`, CI, or coverage configs in `pyproject.toml` or elsewhere.
- **Manual QA workflow:**
  1. `./install.sh`
  2. Drop a test file on `~/Desktop` or run `filemaid process <path>`.
  3. Verify expected archive folder and Finder tags with `ls -R ~/Documents/Archive` and `mdls -name kMDItemUserTags <path>`.
  4. Run `filemaid cleanup --dry-run` and inspect output/log.
- **Adding tests:** A contributor would need to add a test framework (e.g., `pytest`) and likely mock Ollama and external tools like `xattr`/`osascript`.

<!-- BEGIN BEADS INTEGRATION v:1 profile:minimal hash:970c3bf2 -->
## Beads Issue Tracker

This project uses **bd (beads)** for issue tracking. Run `bd prime` to see full workflow context and commands.

### Quick Reference

```bash
bd ready              # Find available work
bd show <id>          # View issue details
bd update <id> --claim  # Claim work
bd close <id>         # Complete work
```

### Rules

- Use `bd` for ALL task tracking — do NOT use TodoWrite, TaskCreate, or markdown TODO lists
- Run `bd prime` for detailed command reference and session close protocol
- Use `bd remember` for persistent knowledge — do NOT use MEMORY.md files

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/SYNC_CONCEPTS.md for details and anti-patterns.

## Agent Context Profiles

The managed Beads block is task-tracking guidance, not permission to override repository, user, or orchestrator instructions.

- **Conservative (default)**: Use `bd` for task tracking. Do not run git commits, git pushes, or Dolt remote sync unless explicitly asked. At handoff, report changed files, validation, and suggested next commands.
- **Minimal**: Keep tool instruction files as pointers to `bd prime`; use the same conservative git policy unless active instructions say otherwise.
- **Team-maintainer**: Only when the repository explicitly opts in, agents may close beads, run quality gates, commit, and push as part of session close. A current "do not commit" or "do not push" instruction still wins.

## Session Completion

This protocol applies when ending a Beads implementation workflow. It is subordinate to explicit user, repository, and orchestrator instructions.

1. **File issues for remaining work** - Create beads for anything that needs follow-up
2. **Run quality gates** (if code changed) - Tests, linters, builds
3. **Update issue status** - Close finished work, update in-progress items
4. **Handle git/sync by active profile**:
   ```bash
   # Conservative/minimal/default: report status and proposed commands; wait for approval.
   git status

   # Team-maintainer opt-in only, unless current instructions forbid it:
   git pull --rebase
   bd dolt push
   git push
   git status
   ```
5. **Hand off** - Summarize changes, validation, issue status, and any blocked sync/commit/push step

**Critical rules:**
- Explicit user or orchestrator instructions override this Beads block.
- Do not commit or push without clear authority from the active profile or the current user request.
- If a required sync or push is blocked, stop and report the exact command and error.
<!-- END BEADS INTEGRATION -->

<!-- BEGIN BEADS CODEX SETUP: generated by bd setup codex -->
## Beads Issue Tracker

Use Beads (`bd`) for durable task tracking in repositories that include it. Use the `beads` skill at `.agents/skills/beads/SKILL.md` (project install) or `~/.agents/skills/beads/SKILL.md` (global install) for Beads workflow guidance, then use the `bd` CLI for issue operations.

### Quick Reference

```bash
bd ready                # Find available work
bd show <id>            # View issue details
bd update <id> --claim  # Claim work
bd close <id>           # Complete work
bd prime                # Refresh Beads context
```

### Rules

- Use `bd` for all task tracking; do not create markdown TODO lists.
- Run `bd prime` when Beads context is missing or stale. Codex 0.129.0+ can load Beads context automatically through native hooks; use `/hooks` to inspect or toggle them.
- Keep persistent project memory in Beads via `bd remember`; do not create ad hoc memory files.

**Architecture in one line:** issues live in a local Dolt DB; sync uses `refs/dolt/data` on your git remote; `.beads/issues.jsonl` is a passive export. See https://github.com/gastownhall/beads/blob/main/docs/SYNC_CONCEPTS.md for details and anti-patterns.
<!-- END BEADS CODEX SETUP -->
