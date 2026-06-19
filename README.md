# filemaid

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
- **Standard library only** — no third-party Python dependencies.

## Requirements

- macOS 13+ (uses `launchctl`, `xattr`, `mdimport`, `osascript`)
- Python 3.12+ (recommended: install the latest with `brew install python@3.14`, or use `python@3.13` / `python@3.12`)
- [Ollama](https://ollama.com/) running locally with a vision-capable model (default: `filemaid-gemma4-26b`)

`install.sh` will detect your Python version and refuse to install if it is older than 3.12.

## Quick Start

```zsh
git clone https://github.com/logicminds/filemaid.git ~/Projects/filemaid
cd ~/Projects/filemaid
./install.sh
```

By default the installer asks whether to enable the scheduled scan agent. To skip it and run `filemaid scan` manually, use:

```zsh
./install.sh --no-scan
```

- `~/.local/bin/filemaid` — command-line wrapper
- `~/.config/filemaid/config.json` — user configuration
- `~/.local/share/filemaid/` — logs and SQLite database
- `~/.filemaid/review/` — quarantine folder
- `~/Library/LaunchAgents/biz.logicminds.filemaid.*.plist` — background agents
- Custom Ollama models (`filemaid-gemma4-26b`, `filemaid-gemma4-12b`, `filemaid-metadata`) — created automatically if Ollama is installed

## Usage

```zsh
# Process files manually
filemaid process ~/Desktop/Screenshot*.png ~/Downloads/receipt.pdf

# Scan watch directories
filemaid scan

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

# Tail logs
filemaid logs --tail 50

# Show resolved configuration
filemaid config
```

## Shortcuts Setup

For instant per-file processing, add a Shortcuts folder automation:

1. Open **Shortcuts → Automations → Personal Automation → + → Folder**.
2. Select `Desktop`, choose **Run immediately**.
3. Add **Run Shell Script**:
   - Shell: `/bin/zsh`
   - Pass input: **As arguments**
   - Command:
     ```zsh
     export PATH="/usr/local/bin:/opt/homebrew/bin:$PATH"
     "$HOME/.local/bin/filemaid" process "$@"
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
| `safe_delete_patterns` | `fnmatch` patterns for files allowed to be deleted. |
| `age_rules` | Patterns + age that force review, e.g. old `.dmg` installers. |

## Custom Ollama Models

filemaid ships with three Ollama Modelfiles in the `modelfiles/` directory. They bundle a system prompt, low temperature, and output constraints so the model returns the JSON shape filemaid expects.

| Model | Base | Use case |
|-------|------|----------|
| `filemaid-gemma4-26b` | `gemma4:26b-a4b-it-qat` | Default high-quality vision model for images + text |
| `filemaid-gemma4-12b` | `gemma4:12b-it-qat` | Faster fallback with vision |
| `filemaid-metadata` | `qwen2.5:7b` | Non-vision/text-only model; classifies from filename and metadata only |

`./install.sh` creates all three models automatically if Ollama is installed. To create or recreate them manually:

```zsh
cd ~/Projects/filemaid
ollama create -f modelfiles/Modelfile.filemaid-gemma4-26b filemaid-gemma4-26b
ollama create -f modelfiles/Modelfile.filemaid-gemma4-12b filemaid-gemma4-12b
ollama create -f modelfiles/Modelfile.filemaid-metadata filemaid-metadata
```

Switch models by editing `~/.config/filemaid/config.json`:

```json
{
  "model": "filemaid-gemma4-12b"
}
```

Use `filemaid-metadata` when running on a machine without a vision-capable model or when you only want filename/text classification.

## Architecture

```
filemaid process <paths>
  -> load_config()          filemaid/config.py
  -> init_db()              filemaid/state.py
  -> process_paths()
       -> classify_file()   filemaid/llm.py  → Decision
       -> apply()           filemaid/actions.py
       -> record()          filemaid/state.py
```

- `filemaid/llm.py` — Ollama classifier and `Decision` value object.
- `filemaid/actions.py` — Applies decisions: whitelist, moves, tags, trash, review.
- `filemaid/state.py` — SQLite history and duplicate detection.
- `filemaid/config.py` — Config loading with defaults.
- `filemaid/cleaners/` — Plugin registry for dev-artifact cleanup.
- `filemaid/__main__.py` — CLI entry point.

## Scheduling

| Agent | Schedule | Logs |
|-------|----------|------|
| `biz.logicminds.filemaid.scan` | Every 15 minutes (optional) | `~/.local/share/filemaid/scan.log` |
| `biz.logicminds.filemaid.cleanup` | 06:00, 12:00, 18:00, 23:00 | `~/.local/share/filemaid/cleanup.log` |

The scan agent is optional. If you choose not to install it, run `filemaid scan` manually or use the Shortcuts folder automations below.

## Uninstall

```zsh
cd ~/Projects/filemaid
./uninstall.sh
```

This removes the LaunchAgents and the `~/.local/bin/filemaid` wrapper. It does not remove your config, logs, database, or review queue.

## Troubleshooting

### Scan agent cannot read Desktop/Downloads

The background scan agent may need Full Disk Access for the Python interpreter selected by `install.sh` (for example, `/opt/homebrew/bin/python3.13`):

1. **System Settings → Privacy & Security → Full Disk Access**
2. Click **+**, press `Cmd+Shift+G`, and enter the path shown when you ran `./install.sh` (e.g. `/opt/homebrew/bin/python3.14`).

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

## License

MIT
