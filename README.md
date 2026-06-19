# filemaid
[![Test](https://github.com/logicminds/filemaid/actions/workflows/test.yml/badge.svg)](https://github.com/logicminds/filemaid/actions/workflows/test.yml)


A local, AI-powered file organizer for macOS. It watches your `Desktop` and `Downloads`, classifies files with a local Ollama LLM, moves them into categorized archives, applies Finder tags, and quarantines uncertain items for review. It also cleans up stale development artifacts like Docker images, npm/cargo/pip caches, Homebrew packages, and Xcode DerivedData.

![filemaid image](./public/images/image.png)

> **⚠️ Experimental:** This project is under active development and may not work reliably in all environments. File movements, classifications, and cleanups can have side effects. Please review the code before running it on important data. Bug reports, issues, and pull requests are welcome to improve behavior.

> **Feedback:** If something breaks or behaves unexpectedly, [file an issue](https://github.com/logicminds/filemaid/issues) or [open a pull request](https://github.com/logicminds/filemaid/pulls).

## Features

- **Automatic classification** — files are classified by a local LLM using filename, extension, content snippets, and image analysis.
- **Move + tag** — files are moved into `~/Documents/Archive/<category>/` and tagged with Finder tags.
- **Review-before-delete** — anything uncertain goes to `~/.filemaid/review/`; deletions only happen for explicitly safe patterns or duplicates.
- **Dev cache cleanup** — scheduled cleanup for Docker, npm, cargo, pip, Homebrew, and Xcode.
- **Two triggers** — instant Shortcuts folder automations or periodic launchd agents.
- **Private & offline** — no cloud services; everything runs locally via Ollama.
- **Single Go binary** — one self-contained binary; only Cobra is used for the CLI.

## Requirements

- macOS 13+ (uses `launchctl`, `xattr`, `mdimport`, `osascript`)
- [Homebrew](https://brew.sh) — package manager for macOS. Install it with:
  ```zsh
  /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
  ```
- Go 1.23+ (recommended: install the latest with `brew install go`)
- [Ollama](https://ollama.com/) running locally with a vision-capable model (install with `brew install ollama`)

## Quick Start

Build locally and run setup:

```zsh
git clone https://github.com/logicminds/filemaid.git ~/Projects/filemaid
cd ~/Projects/filemaid
go build -o bin/filemaid ./cmd/filemaid
./bin/filemaid setup
```

`setup` checks for Ollama, detects your Mac's RAM, and recommends a model:

- 24 GB+ RAM → `filemaid-gemma4-26b`
- 16 GB+ RAM → `filemaid-gemma4-12b`
- less RAM → `filemaid-metadata`

Press `Enter` to accept the recommendation, or choose another model from the prompt.

To skip the interactive prompt, pass `--model`:

```zsh
./bin/filemaid setup --model filemaid-gemma4-12b
```

To skip installing the scheduled scan agent and run `filemaid scan` manually:

```zsh
./bin/filemaid setup --no-scan
```

Or install the latest release directly with `go install`:

```zsh
go install github.com/logicminds/filemaid/cmd/filemaid@latest
```

Make sure `$(go env GOPATH)/bin` is on your `PATH` to run the installed binary as `filemaid`.

After setup you will have:

- `~/.local/bin/filemaid` — installed command-line binary
- `~/.config/filemaid/config.json` — user configuration
- `~/.local/share/filemaid/` — logs and SQLite database
- `~/.filemaid/review/` — quarantine folder
- `~/Library/LaunchAgents/biz.logicminds.filemaid.*.plist` — background agents
- Custom Ollama models (`filemaid-gemma4-26b`, `filemaid-gemma4-12b`, `filemaid-metadata`) — created automatically if Ollama is installed

## Usage

```zsh
# Process files manually
./bin/filemaid process ~/Desktop/Screenshot*.png ~/Downloads/receipt.pdf

# Scan watch directories
./bin/filemaid scan

# Run cleaners in dry-run mode
./bin/filemaid cleanup --dry-run

# Run cleaners for real (table output is default)
./bin/filemaid cleanup

# Get cleaner results as JSON
./bin/filemaid cleanup --format json

# Run cleaners and see estimated space that would be freed
./bin/filemaid cleanup --dry-run --format table

# View the review queue
./bin/filemaid review

# Open the review queue in Finder
./bin/filemaid review --open

# Tail logs
./bin/filemaid logs --tail 50

# Show resolved configuration
./bin/filemaid config
```

If you installed via `go install`, use `filemaid` instead of `./bin/filemaid`.

## Shortcuts Setup

For instant per-file processing, add a Shortcuts folder automation:

1. Open **Shortcuts → Automations → Personal Automation → + → Folder**.
2. Select `Desktop`, choose **Run immediately**.
3. Add **Run Shell Script**:
   - Shell: `/bin/zsh`
   - Pass input: **As arguments**
   - Command:
     ```zsh
     export PATH="$(go env GOPATH)/bin:/usr/local/bin:/opt/homebrew/bin:$PATH"
     filemaid process "$@"
     ```
     If you are running from a local clone, use the binary path instead:
     ```zsh
     "$HOME/Projects/filemaid/bin/filemaid" process "$@"
     ```
4. Repeat for `Downloads`.

Shortcuts runs in your user session and does not require Full Disk Access.

## How Classification Works

When a file is processed, filemaid sends its name, extension, size, modification time, and (for supported images and text files) a content snippet to the local Ollama model. The model returns one of the categories listed in `config.json`, along with suggested Finder tags and an action (`move`, `delete`, or `review`).

- If the model returns a known category, the file is moved to the matching folder under `categories`.
- If the model is unsure, returns an unknown category, or the response cannot be parsed, the file is sent to `~/.filemaid/review/` instead.
- The model is only allowed to choose from categories you define. Adding a new category in `config.json` is enough for the model to classify files into it.

To add a new category, add it to the `categories` map in `~/.config/filemaid/config.json`:

```json
{
  "categories": {
    "Presentations": "~/Documents/Archive/Presentations"
  }
}
```

The next time filemaid runs, the model may classify matching files into `~/Documents/Archive/Presentations`.

## Configuration

Edit `~/.config/filemaid/config.json`:

```json
{
  "ollama_url": "http://localhost:11434",
  "model": "filemaid-gemma4-26b",
  "allowed_dirs": ["~/Desktop", "~/Downloads", "~/Documents/Archive", "~/.filemaid/review"],
  "allowed_cleaners": ["docker", "npm", "cargo", "pip", "brew", "xcode"],
  "categories": {
    "Screenshots": "~/Documents/Archive/Screenshots",
    "Documents": "~/Documents/Archive/Documents",
    "Receipts": "~/Documents/Archive/Receipts",
    "Images": "~/Documents/Archive/Images",
    "Installers": "~/Documents/Archive/Installers",
    "Code": "~/Documents/Archive/Code",
    "Archives": "~/Documents/Archive/Archives",
    "Media": "~/Documents/Archive/Media",
    "Unknown": "~/.filemaid/review"
  },
  "safe_delete_patterns": [],
  "age_rules": [
    {"pattern": "~/Downloads/*.dmg", "days": 30, "action": "review"}
  ]
}
```

| Key | Purpose |
|-----|---------|
| `model` | Ollama model tag. Non-vision models fall back to metadata-only image classification. |
| `allowed_dirs` | Files outside these directories are ignored. |
| `allowed_cleaners` | Which dev cleaners may run. |
| `categories` | Destination folders for each classification. |
| `safe_delete_patterns` | Glob patterns for files allowed to be deleted. |
| `age_rules` | Patterns + age that force review, e.g. old `.dmg` installers. |

## Custom Ollama Models

filemaid ships with three Ollama Modelfiles embedded in the binary. They bundle a system prompt, low temperature, and output constraints so the model returns the JSON shape filemaid expects.

| Model | Base | Use case |
|-------|------|----------|
| `filemaid-gemma4-26b` | `gemma4:26b-a4b-it-qat` | Default high-quality vision model for images + text |
| `filemaid-gemma4-12b` | `gemma4:12b-it-qat` | Faster fallback with vision |
| `filemaid-metadata` | `qwen2.5:7b` | Non-vision/text-only model; classifies from filename and metadata only |

`filemaid setup` creates all three models automatically if Ollama is installed. Switch models by editing `~/.config/filemaid/config.json`:

```json
{
  "model": "filemaid-gemma4-12b"
}
```

Use `filemaid-metadata` when running on a machine without a vision-capable model or when you only want filename/text classification.

## Architecture

```
filemaid process <paths>
  -> internal/config.Load()    load config + defaults
  -> internal/state.Open()     open SQLite history
  -> internal/cli process
       -> internal/llm.Classify()  → Decision
       -> internal/actions.Apply()
       -> internal/state.Record()
```

- `internal/cli/` — Cobra root command and subcommands (`process`, `scan`, `cleanup`, `review`, `logs`, `config`, `setup`, `uninstall`).
- `internal/llm/` — Ollama classifier and `Decision` value object.
- `internal/actions/` — Applies decisions: whitelist, moves, tags, trash, review.
- `internal/state/` — SQLite history and duplicate detection.
- `internal/config/` — Config loading with defaults and `~` expansion.
- `internal/cleaners/` — Plugin registry for dev-artifact cleanup.
- `internal/setup/` — Installation and uninstallation of binary, config, and launchd agents.
- `cmd/filemaid/main.go` — CLI entry point.

## Scheduling

| Agent | Schedule | Logs |
|-------|----------|------|
| `biz.logicminds.filemaid.scan` | Every 15 minutes (optional) | `~/.local/share/filemaid/scan.log` |
| `biz.logicminds.filemaid.cleanup` | 06:00, 12:00, 18:00, 23:00 | `~/.local/share/filemaid/cleanup.log` |

The scan agent is optional. If you choose not to install it, run `filemaid scan` manually or use the Shortcuts folder automations above.

## Uninstall

```zsh
filemaid uninstall
```

This removes the LaunchAgents and the `~/.local/bin/filemaid` binary. It does not remove your config, logs, database, or review queue.

## Troubleshooting

### Missing requirements

`filemaid setup` checks for Ollama before installing. If Ollama is missing, it prints:

```
Ollama is not installed or not on PATH.
Install Ollama with Homebrew:
  brew install ollama
Or download it from https://ollama.com/
```

If Ollama is installed but not running, start it with `ollama serve` and run setup again.

### Install Homebrew

If you do not have Homebrew yet:

```zsh
/bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
```

Then install the required tools:

```zsh
brew install go ollama
```

### Scan agent cannot read Desktop/Downloads

The background scan agent may need Full Disk Access for the `filemaid` binary:

1. **System Settings → Privacy & Security → Full Disk Access**
2. Click **+**, press `Cmd+Shift+G`, and enter the path to the binary (e.g. `~/.local/bin/filemaid`).

Or skip this entirely by using the Shortcuts folder automations above.

### Ollama not responding

Ensure Ollama is running:

```zsh
ollama list
```

If a custom model is missing, recreate it from the Modelfile:

```zsh
cd ~/Projects/filemaid
ollama create -f modelfiles/Modelfile.filemaid-gemma4-26b filemaid-gemma4-26b
```

To use the base model directly instead, pull it and update `model` in `~/.config/filemaid/config.json`:

```zsh
ollama pull gemma4:26b-a4b-it-qat
```

If Ollama is unreachable, files are sent to review instead of erroring.

## Development

Run the same checks the CI runs:

```zsh
make build   # go build -o bin/filemaid ./cmd/filemaid
make test    # go test ./...
make fmt     # go fmt ./...
make lint    # go vet ./...
make coverage # go test -coverprofile=coverage.out ./...
```

CI enforces:

- `go test ./...` passes on Go 1.23 and 1.24.
- Overall test coverage stays above 80%.
- `go vet ./...` and `gofmt -l .` are clean.
- JSON configs and generated launchd plists are valid.

The default `config.json` and all Ollama Modelfiles are embedded into the binary with `//go:embed`, so `go install github.com/logicminds/filemaid/cmd/filemaid@latest` produces a fully portable installer: `filemaid setup` writes the config and models at runtime.

## License

MIT
