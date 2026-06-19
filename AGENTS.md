# Repository Guidelines

## Project Overview

`filemaid` is a local, macOS-only file organizer. It classifies files dropped on `~/Desktop` or in `~/Downloads` using a local Ollama LLM, moves them into categorized archive folders, applies Finder tags, and quarantines uncertain items for review. It also runs scheduled cleanup of stale development artifacts (Docker images, npm/cargo/pip/brew caches, Xcode DerivedData) and stale review-queue items.

Two triggers are supported:

- **macOS Shortcuts folder automations** — instant per-file processing, no Full Disk Access required.
- **`biz.logicminds.filemaid.scan` LaunchAgent** — optional periodic scan every 15 minutes. Disable during setup with `filemaid setup --no-scan`.
- **`biz.logicminds.filemaid.cleanup` LaunchAgent** — cleanup at 06:00, 12:00, 18:00, and 23:00.

## Architecture & Data Flow

The codebase is a small, synchronous, procedural Go CLI. There is no async framework; state flows through typed `config.Config` and a `state.Repo` backed by SQLite.

```
filemaid process <paths>
  -> internal/config.Load()      load config + defaults
  -> internal/state.Open()       open SQLite history
  -> internal/cli.process
       -> internal/actions.ComputeHash()
       -> internal/cli._checkAgeRule()
       -> internal/llm.Classify()  → Decision
       -> internal/actions.Apply()
            -> move / tag / trash / review
       -> internal/state.Record()

filemaid cleanup
  -> internal/cleaners.Registry()
       -> CanRun() / Run()        (per-cleaner modules)
```

The shared value object is `llm.Decision` (`internal/llm/llm.go`):

```go
type Decision struct {
    Category    string   `json:"category"`
    Tags        []string `json:"tags"`
    Action      string   `json:"action"`      // "move" | "delete" | "review"
    Destination string   `json:"destination"`
    Reason      string   `json:"reason"`
}
```

## Key Directories

| Directory | Purpose |
|-----------|---------|
| `cmd/filemaid/` | CLI entry point (`main.go`). |
| `internal/cli/` | Cobra root command and subcommands. |
| `internal/llm/` | Ollama classifier and `Decision` value object. |
| `internal/actions/` | Applies decisions: whitelist, duplicates, moves, tags, trash, review. |
| `internal/state/` | SQLite history and duplicate detection. |
| `internal/config/` | Config loading with defaults and `~` expansion. |
| `internal/cleaners/` | Plugin registry for dev-artifact and review-queue cleanup. |
| `internal/setup/` | Installation and uninstallation of binary, config, and launchd agents. |
| `internal/log/` | `log/slog` setup. |
| Project root | `go.mod`, `Makefile`, default config (`config.json`), Ollama Modelfiles (`modelfiles/`), and the implementation plan. |

## Development Commands

| Task | Command |
|------|---------|
| Build binary | `go build -o bin/filemaid ./cmd/filemaid` or `make build` |
| Run tests | `go test ./...` or `make test` |
| Run tests with coverage | `make coverage` |
| Format code | `go fmt ./...` or `make fmt` |
| Run linter | `go vet ./...` or `make lint` |
| Install/reinstall agents and wrapper | `filemaid setup` |
| Uninstall agents and wrapper | `filemaid uninstall` |
| Process files manually | `~/.local/bin/filemaid process ~/Desktop/foo.png ~/Downloads/bar.pdf` |
| Scan watch dirs | `~/.local/bin/filemaid scan` or `~/.local/bin/filemaid scan --dir ~/Downloads` |
| Dry-run cleaners | `~/.local/bin/filemaid cleanup --dry-run` |
| Run cleaners now | `~/.local/bin/filemaid cleanup` |
| View/approve review queue | `~/.local/bin/filemaid review` or `~/.local/bin/filemaid review --open` |
| Tail logs | `~/.local/bin/filemaid logs --tail 50` |
| Show resolved config | `~/.local/bin/filemaid config` |

## Code Conventions & Common Patterns

- **Go standard library plus Cobra only.** `go.mod` declares only `github.com/spf13/cobra` as a direct dependency.
- **Go 1.23+.** `go.mod` requires `go 1.23`.
- **Config-driven behavior.** Most rules live in `~/.config/filemaid/config.json` and are merged with defaults in `internal/config`. All paths containing `~` are expanded.
- **Whitelist safety.** `allowed_dirs` gates both source and destination paths; `allowed_cleaners` gates which cleaners may run.
- **Fail-safe classification.** Any LLM error, parse failure, timeout, or ambiguous result becomes `category="Unknown"`, `action="review"`.
- **Delete safety.** `action="delete"` is only honored if the file matches `safe_delete_patterns` or is a duplicate; otherwise it is coerced to `"review"`.
- **Pattern matching.** `safe_delete_patterns` and `age_rules` use `filepath.Match` against the path relative to `~`.
- **Cleaner plugin contract.** Each cleaner module exposes:
  ```go
  func CanRun() bool
  func Run(dryRun bool, cfg *config.Config) CleanupResult
  ```
  where `CleanupResult` lives in `internal/cleaners/result.go` and carries `Name`, `Status`, `Saved` (bytes), `SavedHuman`, `Detail`, and `Command`.
- **Embedded assets.** The default `config.json` and `modelfiles/` are embedded into the binary under `internal/setup/assets`, so `filemaid setup` is self-contained and works from a single portable binary.
- **Subprocess calls are fire-and-forget.** External-tool failures are logged but do not abort moves.
- **macOS-specific integration.** Finder tags are written via `xattr` + `mdimport`; trash uses `osascript` "Finder delete".

## Important Files

| File | Role |
|------|------|
| `cmd/filemaid/main.go` | CLI entry point. |
| `internal/cli/root.go` | Cobra root command and persistent pre-run (config, logging, DB). |
| `internal/cli/process.go` | `process` subcommand orchestration. |
| `internal/llm/llm.go` | Ollama classifier, `Decision` struct, image/text prompt building. |
| `internal/actions/actions.go` | Applies decisions: whitelist, duplicates, moves, tags, trash, review. |
| `internal/actions/fs.go` | `FS` interface, macOS `OSFS`, and test `RecordingFS`. |
| `internal/state/state.go` | SQLite repository and schema. |
| `internal/config/config.go` | Typed config, defaults, merge logic, `~` expansion. |
| `internal/cleaners/registry.go` | `CLEANERS` registry. |
| `internal/cleaners/{docker,npm,cargo,pip,brew,xcode,review}.go` | Individual dev-artifact cleaners (plus `review.go` for review-queue cleanup). |
| `internal/setup/setup.go` | Self-install logic; generates launchd plists dynamically from embedded templates. |
| `internal/setup/uninstall.go` | Removes agents and binary; preserves config/logs/db/review queue. |
| `internal/setup/assets/` | Embedded default `config.json` and Ollama Modelfiles. |
| `config.json` | Default user-facing configuration (copied to `~/.config/filemaid/config.json` on install). |
| `Makefile` | Build, test, fmt, lint, and coverage targets. |
| `local://mac-file-automation-plan.md` | Full implementation plan. **Note:** the plan text still references `com.opselite.*`; the actual code uses `biz.logicminds.*`. |

## Runtime/Tooling Preferences

- **Runtime:** Go 1.23+ (recommended: install the latest with `brew install go`).
- **Platform:** macOS only (uses `launchctl`, `osascript`, `xattr`, `mdimport`, Finder tags).
- **External dependency:** A running Ollama server at `http://localhost:11434` with the model configured in `~/.config/filemaid/config.json` (default `filemaid-gemma4-26b`).
- **LaunchAgent management:** Uses `launchctl bootstrap gui/$(id -u)` / `launchctl bootout gui/$(id -u)`. Plists are generated at setup time and written to `~/Library/LaunchAgents/`.
- **Path assumptions:** The binary is installed to `~/.local/bin/filemaid`; runtime data goes to `~/.local/share/filemaid/`; config to `~/.config/filemaid/`; review queue to `~/.filemaid/review/`.
- **TCC note:** The background scan agent may be denied read access to `~/Desktop`/`~/Downloads` until the `filemaid` binary is granted Full Disk Access in System Settings → Privacy & Security → Full Disk Access. The Shortcuts folder-automation path does not need this.

## Testing & QA

- **Test runner:** `go test ./...`.
- **Coverage gate:** `make coverage` enforces an 80% overall coverage floor.
- **Lint/format:** `go vet ./...` and `gofmt -l .` must be clean.
- **Manual QA workflow:**
  1. `go build -o bin/filemaid ./cmd/filemaid && ./bin/filemaid setup`
  2. Drop a test file on `~/Desktop` or run `filemaid process <path>`.
  3. Verify expected archive folder and Finder tags with `ls -R ~/Documents/Archive` and `mdls -name kMDItemUserTags <path>`.
  4. Run `filemaid cleanup --dry-run` and inspect output/log.
- **Adding tests:** Add table-driven tests in the relevant `internal/<pkg>/*_test.go` file. Use the fake implementations in `internal/state/fake.go` and `internal/actions/fs.go` to avoid touching the real filesystem or Ollama.

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
- Use `bd remember` for persistent knowledge — do NOT create MEMORY.md files

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
