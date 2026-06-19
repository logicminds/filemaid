# Development Guide

This document covers how to set up the local development environment for `filemaid`, including how to install the shared Go skills used by this project.

## Prerequisites

- macOS (the app uses macOS-specific integrations: Finder tags, `osascript`, `launchctl`, etc.)
- Go 1.23+ (`brew install go`)
- A running Ollama server at `http://localhost:11434` with the model configured in `~/.config/filemaid/config.json`

## Building and Testing

```bash
# Build the binary
go build -o bin/filemaid ./cmd/filemaid

# Run tests
go test ./...

# Run tests with coverage (enforces 80% floor)
make coverage

# Format and lint
go fmt ./...
go vet ./...
```

## Installing Skills

This project uses the [samber/cc-skills-golang](https://github.com/samber/cc-skills-golang) skill pack to provide Go-specific guidance to the coding agent. Install the skills at the **user level** so they are available across all your projects and do not get committed into any single repository.

### User-level installation (recommended)

```bash
npx skills add https://github.com/samber/cc-skills-golang --all -g
```

The `-g` / `--global` flag installs skills into `~/.agents/skills/` and symlinks them into each supported agent's user-level skill directory (for Oh My Pi this is `~/.pi/agent/skills/`). Your agent discovers them from there.

The installer may also drop symlinks into the current project's agent directories (for example, `.claude/skills/`). These are only symlinks back to `~/.agents/skills/` and do not contain the skill content. This repository already ignores them in `.gitignore`:

```gitignore
.claude/skills/
.agents/skills/
skills-lock.json
```

If you accidentally installed them at the project level, remove the `./skills/` directory and reinstall globally:

```bash
rm -rf ./skills
npx skills add https://github.com/samber/cc-skills-golang --all -g
```

### Verify the installation

List the skills that Oh My Pi (the `pi` agent in the skills CLI) can see:

```bash
npx skills list --global --agent pi
```

You should see 43 entries such as `golang-cli`, `golang-testing`, `golang-error-handling`, etc.

You can also list the skill names as JSON:

```bash
npx skills list --global --agent pi --json | python3 -c "import sys, json; print('\n'.join(s['name'] for s in json.load(sys.stdin)))"
```

### Updating skills

Update the globally installed Go skills to their latest versions:

```bash
npx skills update -g
```

### Removing skills

Remove all globally installed Go skills:

```bash
npx skills remove --all -g
```

Remove a single skill (example):

```bash
npx skills remove golang-benchmark -g
```

## Project Layout and Conventions

See [`AGENTS.md`](AGENTS.md) for the full project overview, architecture, and code conventions.

Key points:

- The codebase is a synchronous, procedural Go CLI using Cobra.
- Direct dependencies are kept minimal; `go.mod` only declares `github.com/spf13/cobra`.
- All paths containing `~` are expanded by `internal/config`.
- Fail-safe classification: any LLM failure becomes `category="Unknown"`, `action="review"`.
- Delete safety: `action="delete"` is coerced to `"review"` unless the file matches `safe_delete_patterns` or is a duplicate.

## Useful Commands

| Task | Command |
|------|---------|
| Process files manually | `~/.local/bin/filemaid process <paths...>` |
| Scan watch directories | `~/.local/bin/filemaid scan` |
| Dry-run cleaners | `~/.local/bin/filemaid cleanup --dry-run` |
| Run cleaners | `~/.local/bin/filemaid cleanup` |
| View review queue | `~/.local/bin/filemaid review` |
| Show resolved config | `~/.local/bin/filemaid config` |
| Tail logs | `~/.local/bin/filemaid logs --tail 50` |
| Install/reinstall agents | `filemaid setup` |
| Uninstall agents | `filemaid uninstall` |
