# filemaid
[![Test](https://github.com/logicminds/filemaid/actions/workflows/test.yml/badge.svg)](https://github.com/logicminds/filemaid/actions/workflows/test.yml)


A local, AI-powered file organizer for macOS. It watches your `Desktop` and `Downloads`, classifies files with a local Ollama LLM, moves them into categorized archives, applies Finder tags, and quarantines uncertain items for review. It also cleans up stale development artifacts like Docker images, npm/cargo/pip caches, Homebrew packages, and Xcode DerivedData.

![filemaid image](./public/images/image.png)

> **⚠️ Experimental:** This project is under active development and may not work reliably in all environments. File movements, classifications, and cleanups can have side effects. Please review the code before running it on important data. Bug reports, issues, and pull requests are welcome to improve behavior.

> **Feedback:** If something breaks or behaves unexpectedly, [file an issue](https://github.com/logicminds/filemaid/issues) or [open a pull request](https://github.com/logicminds/filemaid/pulls).
>
> **Changelog:** See [CHANGELOG.md](CHANGELOG.md) for a version-by-version summary of new features and breaking changes.

## Features

- **Automatic classification** — files are classified by a local LLM using filename, extension, content snippets, and image analysis.
- **Move + tag + comment** — files are moved into `~/Documents/Archive/<category>/`, tagged with Finder tags, and the classification reason is stored in the Finder comment.
- **Review-before-delete** — anything uncertain goes to `~/.filemaid/review/`; deletions only happen for explicitly safe patterns or duplicates.
- **Dev cache cleanup** — scheduled cleanup for Docker, npm, cargo, pip, Homebrew, and Xcode.
- **Smart rename** — optionally renames files based on LLM-suggested names when the suggested name quality meets your threshold, with duplicate and near-duplicate detection.
- **Smart Folders + Finder hub** — automatically builds a `~/Documents/Filemaid` hub with per-category Smart Folders, Archive/Review aliases, and a Finder sidebar pin.
- **Private & offline** — no cloud services; everything runs locally via Ollama.
- **Single Go binary** — one self-contained binary; only Cobra is used for the CLI.

## Requirements

- macOS 13+ on Apple Silicon (uses `launchctl`, `xattr`, `mdimport`, `osascript`)
- [Homebrew](https://brew.sh) — package manager for macOS. Install it with:
  ```zsh
  /bin/bash -c "$(curl -fsSL https://raw.githubusercontent.com/Homebrew/install/HEAD/install.sh)"
  ```
- [Ollama](https://ollama.com/) running locally with a vision-capable model (install with `brew install ollama`) 0.30.0+

## Quick Start

Install the latest release with the install script (no Go required):

```zsh
curl -fsSL https://raw.githubusercontent.com/logicminds/filemaid/main/install.sh | bash
```

The script downloads the Apple Silicon binary from the [GitHub releases page](https://github.com/logicminds/filemaid/releases), verifies its checksum, and installs it to `~/.local/bin/filemaid`. Only macOS on Apple Silicon (arm64) is supported; the script reports an error on any other platform.

To install to a different directory, set `INSTALL_DIR`:

```zsh
curl -fsSL https://raw.githubusercontent.com/logicminds/filemaid/main/install.sh | INSTALL_DIR=/usr/local/bin bash
```

Or download the release manually:

1. Go to the [latest release](https://github.com/logicminds/filemaid/releases/latest).
2. Download `filemaid-darwin-arm64` and `filemaid-darwin-arm64.sha256`.
3. Verify the checksum:
   ```zsh
   shasum -a 256 -c filemaid-darwin-arm64.sha256
   ```
4. Move the binary to a directory on your PATH, for example `~/.local/bin/filemaid`, and make it executable:
   ```zsh
   chmod +x ~/.local/bin/filemaid
   ```

After installing, run setup:

```zsh
filemaid setup
```

`setup` checks for Ollama, detects your Mac's RAM, recommends a model, and runs a short configuration interview:

* 24 GB+ RAM → `filemaid-gemma4-26b`
* 16 GB+ RAM → `filemaid-gemma4-12b`
* less RAM → `filemaid-metadata`

Press `Enter` to accept each recommendation or default, or type a custom value when prompted.

To skip the model prompt, pass `--model`:

```zsh
filemaid setup --model filemaid-gemma4-12b
```

To skip the configuration interview and use the shipped defaults, pass `--no-interactive`:

```zsh
filemaid setup --no-interactive
```

To install the background launchd agents (disabled by default):

```zsh
filemaid setup --agents
```

To skip the scheduled scan agent when using `--agents`:

```zsh
filemaid setup --agents --no-scan
```

To see the macOS Shortcuts folder-automation steps:

```zsh
filemaid setup --shortcuts
```

Or install the latest release directly with `go install` (requires Go):

```zsh
go install github.com/logicminds/filemaid/cmd/filemaid@latest
```

Make sure `$(go env GOPATH)/bin` is on your `PATH` to run the installed binary as `filemaid`.

Or build locally from source (requires Go):

```zsh
git clone https://github.com/logicminds/filemaid.git ~/Projects/filemaid
cd ~/Projects/filemaid
go build -o bin/filemaid ./cmd/filemaid
./bin/filemaid setup
```

After setup you will have:

- `~/.local/bin/filemaid` — installed command-line binary
- `~/.config/filemaid/config.json` — user configuration
- `~/.local/share/filemaid/` — logs and SQLite database
- `~/.filemaid/review/` — quarantine folder
- `~/Documents/Filemaid/` — Finder hub with Smart Folders, aliases, and sidebar pin (when `smart_folders` is enabled)
- `~/Library/LaunchAgents/biz.logicminds.filemaid.*.plist` — background agents (only when `setup --agents` is used)
- Custom Ollama models (`filemaid-gemma4-26b`, `filemaid-gemma4-12b`, `filemaid-metadata`) — created automatically if Ollama is installed

For instant per-file processing without background agents, use the Shortcuts folder automation. Run `filemaid setup --shortcuts` to see the steps.

## Usage

```zsh
# Process files manually (human-readable list output is default)
filemaid process ~/Desktop/Screenshot*.png ~/Downloads/receipt.pdf

# Process files as a table
filemaid process --format table ~/Desktop/Screenshot*.png

# Get process results as JSON
filemaid process --json ~/Desktop/Screenshot*.png

# Suppress JSON log lines on stderr
filemaid process --quiet ~/Desktop/Screenshot*.png ~/Downloads/receipt.pdf

# Scan watch directories
filemaid scan

# Scan a single directory
filemaid scan --dir ~/Downloads

# Process with smart rename enabled for this run (uses rename_level from config)
filemaid process --rename ~/Desktop/*.pdf

# Rename with an inline quality threshold (1=most aggressive, 5=most conservative)
filemaid process --rename=3 ~/Desktop/*.pdf

# Force processing even if a file looks like a duplicate or similar to history
filemaid process --force ~/Desktop/*.png

# Preview renames without moving files
filemaid process --dry-run ~/Desktop/*.png

# Scan with rename preview
filemaid scan --dry-run

# Run cleaners in dry-run mode
filemaid cleanup --dry-run

# Run cleaners for real (table output is default)
filemaid cleanup

# Get cleaner results as JSON
filemaid cleanup --format json

# Run cleaners and see estimated space that would be freed
filemaid cleanup --dry-run --format table

# View the review queue
filemaid review

# Open the review queue in Finder
filemaid review --open

# Approve or reject a review item by its relative path
filemaid review --approve "Screenshots/old-screenshot.png"
filemaid review --reject "Documents/unwanted-receipt.pdf"

# Show recent processing history
filemaid history

# Show the last run with a count summary
filemaid history --last

# Regenerate the Filemaid hub (Smart Folders, aliases, sidebar pin)
filemaid smart-folders

# Tail logs
filemaid logs --tail 50

# Show resolved configuration
filemaid config

# Install background launchd agents
filemaid setup --agents

# Output macOS Shortcuts folder-automation steps
filemaid setup --shortcuts
```

If you are running from a local clone, use `./bin/filemaid` instead of `filemaid`.

## Shortcuts Setup

Shortcuts folder automations are the recommended trigger: they run instantly when a file lands in a folder, do not require Full Disk Access, and avoid leaving a background agent running.

Run the following command to print the exact steps for your binary path:

```zsh
filemaid setup --shortcuts
```

For instant per-file processing, add a Shortcuts folder automation:

1. Open **Shortcuts → Automations → Personal Automation → + → Folder**.
2. Select `Desktop`, choose **Run immediately**.
3. Add **Run Shell Script**:
   - Shell: `/bin/zsh`
   - Pass input: **As arguments**
   - Command:
     ```zsh
     export PATH="$HOME/.local/bin:$(go env GOPATH)/bin:/usr/local/bin:/opt/homebrew/bin:$PATH"
     filemaid process "$@"
     ```
     If you are running from a local clone, use the binary path instead:
     ```zsh
     "$HOME/Projects/filemaid/bin/filemaid" process "$@"
     ```
4. Repeat for `Downloads`.

Shortcuts runs in your user session and does not require Full Disk Access.

## How Classification Works

When a file is processed, filemaid sends its name, extension, size, modification time, and (for supported images and text files) a content snippet to the local Ollama model. The model returns one of the categories listed in `config.json`, along with a concise subcategory for images, suggested Finder tags, and an action (`move`, `delete`, or `review`).

For photos, the subcategory describes the main subject or scene (for example `cat`, `dog`, `baby`, `wedding`, or `car`). For screenshots, it describes the app or context (for example `Safari`, `Terminal`, `Slack`, `browser`, or `lock-screen`). The subcategory is added as a Finder tag when `tags` is enabled.

The LLM's reason for the classification is written to the file's Finder comment when `comments` is enabled, so you can see why a file was organized the way it was from the Finder Get Info panel.

Set `subcategorize_images` to `false` to disable the extra image detail and only receive the top-level category.

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

## Filemaid Hub and Smart Folders

When `smart_folders` is enabled (the default), filemaid builds a Finder hub at `~/Documents/Filemaid` every time files are processed or scanned. The hub contains:

- **Smart Folders** — one `.savedSearch` per configured category and per unique Finder tag filemaid has ever applied. These are live Spotlight searches scoped to files tagged `filemaid`.
- **Archive alias** — a Finder alias pointing to the root of your categorized archive.
- **Review alias** — a Finder alias pointing to `~/.filemaid/review`.
- **Finder sidebar pin** — the hub folder is added to Finder's Favorites for quick access.

The hub is recreated automatically after each `process` and `scan` run. To regenerate it manually:

```zsh
filemaid smart-folders
```

Smart Folders rely on Spotlight indexing. If they appear empty, see the [FAQ](FAQ.md) for troubleshooting steps.

To disable the hub, set `smart_folders` to `false` in `~/.config/filemaid/config.json`. To change the hub location, edit `smart_folders_dir`.

## Configuration

`filemaid setup` runs an interactive interview that asks for watch directories, archive location, Finder tags, dev cleaners, review-queue retention, and safe-delete patterns. Press `Enter` at each prompt to accept the default. To skip the interview and use the shipped defaults, run:

```zsh
filemaid setup --no-interactive
```

The generated configuration is written to `~/.config/filemaid/config.json` and can be edited at any time.

### Full config example

```json
{
  "ollama_url": "http://localhost:11434",
  "model": "filemaid-gemma4-26b",
  "image_model": "filemaid-gemma4-26b",
  "text_model": "filemaid-metadata",
  "watch_dirs": ["~/Desktop", "~/Downloads"],
  "allowed_dirs": ["~/Desktop", "~/Downloads", "~/Documents/Archive", "~/.filemaid/review"],
  "allowed_cleaners": ["docker", "npm", "cargo", "pip", "brew", "xcode", "review"],
  "review_dir": "~/.filemaid/review",
  "log_path": "~/.local/share/filemaid/filemaid.log",
  "db_path": "~/.local/share/filemaid/filemaid.db",
  "tags": true,
  "comments": true,
  "subcategorize_images": true,
  "smart_folders": true,
  "smart_folders_dir": "~/Documents/Filemaid",
  "min_age_hours": 0,
  "request_timeout": "120s",
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
  ],
  "dev_cleanup": {
    "docker": {"enabled": true, "mode": "safe"},
    "npm":    {"enabled": true, "mode": "safe"},
    "cargo":  {"enabled": true, "mode": "safe"},
    "pip":    {"enabled": true, "mode": "safe"},
    "brew":   {"enabled": true, "mode": "safe"},
    "xcode":  {"enabled": true, "mode": "safe"}
  },
  "review_cleanup": {
    "enabled": true,
    "mode": "safe",
    "max_age_days": 30
  },
  "_rename_note": "Set rename=true to let the LLM suggest better filenames. rename_level (0-5) is the minimum quality threshold a suggested new name must meet before filemaid applies it. 1 = most aggressive (even weak suggestions), 2 = aggressive, 3 = moderate, 4 = conservative, 5 = most conservative (only excellent suggestions). 0 accepts any suggestion. Invalid characters are removed, extensions are preserved, and collisions get a counter suffix.",
  "rename": false,
  "rename_level": 2,
  "rename_max_length": 120,
  "rename_min_length": 20,
  "rename_invalid_chars": "<>:\"/\\\\|?*",
  "rename_image_similarity_threshold": 0.95,
  "rename_av_similarity_threshold": 0.90,
  "_ffmpeg_note": "rename_use_ffmpeg enables audio/video fingerprinting via ffmpeg. This improves duplicate/near-duplicate detection but can be slow for large media libraries.",
  "rename_use_ffmpeg": false,
  "external_tools": {
    "ffmpeg": "ffmpeg"
  },
  "process_workers": 4,
  "max_image_dimension": 1024
}
```

### Config keys

| Key | Purpose |
|-----|---------|
| `ollama_url` | URL of the local Ollama server. |
| `model` | Default Ollama model tag. Used for all files unless `image_model` or `text_model` is set. |
| `image_model` | Model used for image files. Falls back to `model` when empty. |
| `text_model` | Model used for non-image files. Falls back to `model` when empty. |
| `watch_dirs` | Directories scanned by `filemaid scan`. |
| `allowed_dirs` | Files outside these directories are ignored; also gates destination paths. |
| `allowed_cleaners` | Which dev cleaners may run. Must include `"review"` to enable review-queue cleanup. |
| `review_dir` | Quarantine folder for uncertain files. |
| `log_path` | Path to the main application log. |
| `db_path` | Path to the SQLite history database. |
| `tags` | Whether to apply Finder tags to organized files. |
| `comments` | Whether to write the classification reason as a Finder comment. |
| `subcategorize_images` | When `true`, images and screenshots receive a subject/app subcategory that is also added as a Finder tag. |
| `smart_folders` | When `true`, build the Filemaid hub with Smart Folders, aliases, and sidebar pin. |
| `smart_folders_dir` | Directory for the Filemaid hub. |
| `min_age_hours` | Minimum file age before processing (0 = process immediately). |
| `request_timeout` | Per-request timeout for Ollama calls (e.g. `120s`, `2m`). |
| `categories` | Destination folders for each classification. The model may only return categories defined here. |
| `safe_delete_patterns` | Glob patterns for files allowed to be deleted without review. |
| `age_rules` | Patterns + age that force a specific action, e.g. old `.dmg` installers become `review`. |
| `dev_cleanup` | Per-cleaner enable/disable and mode (`safe` is the only mode currently). |
| `review_cleanup` | Enable and set retention for the review-queue cleaner. Set `max_age_days` to `0` to disable. |
| `rename` | When `true`, the LLM may suggest better filenames. |
| `rename_level` | Minimum quality threshold (0-5) a suggested new name must meet. 1 = most aggressive, 5 = most conservative. |
| `rename_max_length` | Maximum length for a renamed file. |
| `rename_min_length` | Minimum length below which a rename is not applied. |
| `rename_invalid_chars` | Characters stripped from suggested names. |
| `rename_image_similarity_threshold` | Perceptual-hash similarity (0-1) above which images are sent to review. |
| `rename_av_similarity_threshold` | Audio/video similarity (0-1) above which files are sent to review. |
| `rename_use_ffmpeg` | Enable ffmpeg-based audio/video fingerprinting. Slow for large libraries; requires ffmpeg on PATH. |
| `external_tools` | Paths to optional tools (`ffmpeg`). |
| `process_workers` | Concurrency for classification/hashing/apply (minimum 1). |
| `max_image_dimension` | Largest dimension for image payloads sent to the vision model (minimum 64). |

Changes take effect the next time `filemaid process`, `filemaid scan`, or `filemaid cleanup` runs.

## Rename

When `rename` is enabled, filemaid asks the LLM to suggest a better filename and rate the quality of that suggestion from 1 (weak) to 5 (excellent). `rename_level` is the minimum quality threshold the suggestion must meet before it is applied; 1 is the most aggressive (renames even on weak suggestions) and 5 is the most conservative (only excellent suggestions). If the suggested name's rating is at least `rename_level`, the file is renamed while preserving its extension.

Enable renaming for a single run with `--rename`. With no value it uses `rename_level` from config; pass a number inline to override the threshold for that run:

```zsh
# Use config rename_level
filemaid process --rename ~/Desktop/*.pdf

# Override threshold for this run only
filemaid process --rename=3 ~/Desktop/*.pdf
```

Suggested names are sanitized: characters matching `rename_invalid_chars` are stripped, the length is clamped between `rename_min_length` and `rename_max_length`, and collisions are resolved with a counter suffix (e.g., `document-2.pdf`).

Duplicate or near-duplicate files are routed to the review queue instead of being renamed:

- Exact SHA-256 duplicates are always sent to review.
- Images are compared by perceptual hash; similarity above `rename_image_similarity_threshold` goes to review.
- Audio/video files are compared by ffmpeg-extracted signatures when `rename_use_ffmpeg` is `true` and ffmpeg is available; otherwise a structural fallback is used. Similarity above `rename_av_similarity_threshold` goes to review.

**Performance note:** enabling `rename_use_ffmpeg` can be slow for large media libraries because each audio/video file is decoded and fingerprinted. Only enable it if you need robust duplicate detection for media.

Use `--dry-run` to preview suggested names without moving or renaming files:

```zsh
filemaid process --rename --dry-run ~/Desktop/*.pdf
```

| Rename level | Typical behavior |
|--------------|------------------|
| 1 | Apply almost any suggested name. |
| 2 | Apply reasonable names (default). |
| 3 | Apply only clearly better names. |
| 4 | Apply only high-confidence names. |
| 5 | Apply only very high-confidence names. |

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
       -> internal/hub.Build()     → Smart Folders, aliases, sidebar pin
```

- `internal/cli/` — Cobra root command and subcommands (`process`, `scan`, `cleanup`, `review`, `history`, `logs`, `config`, `setup`, `smart-folders`, `uninstall`).
- `internal/llm/` — Ollama classifier and `Decision` value object.
- `internal/actions/` — Applies decisions: whitelist, moves, tags, trash, review.
- `internal/state/` — SQLite history, duplicate detection, and cached decisions.
- `internal/config/` — Config loading with defaults and `~` expansion.
- `internal/smartfolder/` — macOS `.savedSearch` (Smart Folder) generation.
- `internal/hub/` — Builds the Filemaid hub: Smart Folders, archive/review aliases, and Finder sidebar pin.
- `internal/cleaners/` — Plugin registry for dev-artifact cleanup.
- `internal/setup/` — Installation and uninstallation of binary, config, and launchd agents.
- `cmd/filemaid/main.go` — CLI entry point.

Recent performance improvements cache decisions by file hash and skip LLM classification for duplicates that cannot be safely deleted, so already-seen files are applied instantly.

## Scheduling

Background agents are **disabled by default**. To install them, run setup with `--agents`:

```zsh
filemaid setup --agents
```

| Agent | Schedule | Logs |
|-------|----------|------|
| `biz.logicminds.filemaid.scan` | Every 15 minutes (optional) | `~/.local/share/filemaid/scan.log` |
| `biz.logicminds.filemaid.cleanup` | 06:00, 12:00, 18:00, 23:00 | `~/.local/share/filemaid/cleanup.log` |

Use `--no-scan` with `--agents` to install only the cleanup agent. If you do not install agents, run `filemaid scan` and `filemaid cleanup` manually, or use the Shortcuts folder automations above for instant per-file processing.

## Uninstall

```zsh
filemaid uninstall
```

This removes the LaunchAgents and the `~/.local/bin/filemaid` binary. It does not remove your config, logs, database, review queue, or the Filemaid hub in `~/Documents/Filemaid`.

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

## Acceptance Test Checklist

Before releasing a rename-related change, run through the following:

- [ ] `go test ./...` passes and coverage stays above 80%.
- [ ] `go vet ./...` and `gofmt -l .` are clean.
- [ ] `filemaid process --rename` renames a file when the LLM's suggested name rating is >= `rename_level`.
- [ ] `filemaid process --rename` keeps the original name when the LLM's suggested name rating is < `rename_level`.
- [ ] `filemaid scan --dry-run` shows proposed names without moving files.
- [ ] Duplicate files (same SHA-256) are routed to review instead of renamed.
- [ ] Similar images above `rename_image_similarity_threshold` are routed to review.
- [ ] `rename_use_ffmpeg: true` works when ffmpeg is installed and warns/falls back when it is missing.
- [ ] Review queue (`filemaid review`) shows original and proposed names.
- [ ] `filemaid review --approve` moves a review item to its category.
- [ ] `filemaid review --reject` trashes a review item.
- [ ] Finder tags and Smart Folders still regenerate after rename operations.

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
