# Changelog

All notable changes to filemaid are documented in this file.

## Unreleased

### Added

- **LLM retry with exponential backoff** — transient Ollama errors (`context deadline exceeded`, connection failures, model loading, EOF) are now retried up to `llm_retry_attempts` times with `llm_retry_base_delay` exponential backoff. Configurable in `~/.config/filemaid/config.json`.
- **`filemaid review --retry`** — re-process review-queue items whose reason indicates a transient LLM failure. Successful retries move files to their proper archive folders; persistent failures remain in review with an updated reason.
- **`--rename` flag on `review --retry`** — `filemaid review --retry --rename=3` applies the same rename override semantics as `process` and `scan` during a retry run.
- **`llm_retry_attempts` and `llm_retry_base_delay` config options** — control retry behavior; defaults are 2 attempts and a 2-second base delay.
- **Directory-aware deep classification and in-place renaming** — `--depth` is now an optional integer depth flag. `scan --depth` and `process --depth` descend up to N levels, detect project markers, classify directories read-only, and include directory context in file prompts.
- **`project_markers` config option** — built-in markers (`.git`, `node_modules`, `.venv`, `vendor`, `.terraform`, `build`) stop recursion; user-provided markers are merged additively.
- **Bounded directory metadata gathering** — `internal/directory.Gather` produces a capped snapshot of immediate children, extension counts, and detected markers; `.app` bundles are treated as opaque directories.
- **Directory-specific LLM classifier** — new `DirectoryDecision` type and `ClassifyDirectory` method return `keep|review|trash|archive` recommendations; cached in a dedicated SQLite table keyed by content digest.
- **Directory context in file prompts** — files discovered inside descended directories include ancestor path, depth, and detected project marker in the classification prompt; cache keys incorporate context.
- **Updated output formatters** — table/human/JSON output distinguishes directory rows with `dir:`/`[dir]` markers and recommendations, and in-place renames are labeled with the new absolute path; summary counts split files and directories.
- **`--move` flag for `process` and `scan`** — moving files to their classified archive folder or review queue is now opt-in. Without `--move`, filemaid classifies, tags, and (optionally) renames files in place, including items the model marks for review. Use `--move` per run or set `move_files: true` in `config.json` to restore the previous relocate-by-default behavior.

### Changed

- `--depth` changed from a boolean flag (`--include-dirs`) to an optional integer (`--depth` = depth 1, `--depth=N` = depth N).
- **Default behavior no longer moves files** — pass `--move` or set `move_files: true` to relocate organized files or send review items to the review queue.
### Fixed

- **Transient LLM errors are no longer cached.** A timeout or unreachable Ollama previously wrote an "ollama error: ..." decision to the per-file cache, causing the same file to land in review forever on retry. Error decisions are now skipped so the next run can retry classification.
- **Default `request_timeout` increased to 5 minutes** and **default `process_workers` reduced to 1** to accommodate slower local vision models and avoid concurrent model loads timing out on typical Apple Silicon setups.

## [0.5.1]

### Added

* **Install script** — `install.sh` downloads the latest Apple Silicon release from GitHub, verifies its SHA-256 checksum, and installs it to `~/.local/bin/filemaid`. Unsupported operating systems and architectures are rejected with a clear error.
* **Named release asset** — GitHub releases now publish `filemaid-darwin-arm64` plus `filemaid-darwin-arm64.sha256` for verified installs.

### Changed

* **README quick start** — installation instructions now lead with the curl-to-bash install script and manual GitHub releases download; building from source and `go install` are listed as alternatives.

## [0.5.0]

### Added

- **Filemaid hub and Smart Folders** — `filemaid process` and `filemaid scan` now rebuild a `~/Documents/Filemaid` hub after each run. The hub contains:
  - One Smart Folder per configured category and per unique Finder tag.
  - Finder aliases named `Archive` and `Review`.
  - A pinned favorite in Finder's sidebar.
  - Regenerate manually with `filemaid smart-folders`.
  - Configure with `smart_folders` and `smart_folders_dir` in `~/.config/filemaid/config.json`.
- **Inline `--rename` threshold** — the `--rename` flag now accepts an optional quality level, so `--rename=3` overrides `rename_level` for a single run. Use `--rename` alone to use the configured level.
- **`--force` flag** — `filemaid process --force` and `filemaid scan --force` process files even if they look like duplicates or are similar to items already in history.
- **Human-readable default output** — `process` and `scan` now default to `--format human`. Use `--format table` or `--json` for other layouts.
- **Streaming progress** — `process` and `scan` stream human/table progress while files are classified and applied.
- **`history` command** — show recent processing runs from the SQLite database:
  - `filemaid history`
  - `filemaid history --last`
  - `filemaid history --run <run-id>`
- **Dual-model routing** — configure `image_model` and `text_model` separately in `config.json`. Image files route to `image_model`; everything else routes to `text_model`. Each falls back to `model` when unset.
- **Decision cache and duplicate skip** — already-seen file hashes reuse cached decisions, and duplicates that cannot be safely deleted skip LLM classification entirely.
- **Merged Finder tags and comments** — filemaid now merges new tags with existing tags and appends new reasons to existing Finder comments instead of overwriting them.

### Changed

- `process` and `scan` now default to `--format human` instead of `table`.
- The `--rename` and `--rename-level` flags have been combined into a single `--rename` flag with an optional value.
- Classification prompts now guide the model to prefer `move` over `review` for clear category matches.

## [0.4.0]

### Added

- `filemaid history` command for browsing processed-file history by run ID.
- LLM metrics and run IDs recorded to the SQLite database.
- Support for human-readable `request_timeout` values (e.g. `120s`, `2m`).
- Optional launchd agents: `setup --agents` installs scan/cleanup agents, `setup --agents --no-scan` installs only cleanup.
- `setup --shortcuts` prints macOS Shortcuts folder-automation steps.

### Changed

- Modernized Ollama client and deduplicated classification prompts.

## [0.3.0]

### Added

- Model validation before `process`/`scan` and setup disk-space checks.
- Interactive configuration interview during `filemaid setup`.
- Image subcategories (subject/scene for photos, app/context for screenshots).
- Perceptual image resize before sending to Ollama.
- Decision cache by file hash and parallel classification with a bounded worker pool.
- Untagged model aliases so config references without `:latest` resolve correctly.
- LLM acceptance test harness with real web test files.

### Changed

- Finder tags and comments are now written correctly via xattrs; the LLM reason is saved as a Finder comment.
- Setup creates models with the `latest` tag and no longer cleans up stale hash-tagged versions.

## [0.2.0]

### Added

- Table output and `--format`/`--json` flags for `process`, `scan`, and `cleanup`.
- Estimated storage space saved by dev cleaners.
- Pre-computed hash passed to `Apply`; graceful skip when a file disappears during processing.
- Model name matching regardless of `:latest` tag.

## [0.1.0]

### Added

- Initial Go rewrite of filemaid.
- Local Ollama classification, file moves, Finder tags, review queue, and dev-cache cleaners.
[0.5.0]: https://github.com/logicminds/filemaid/releases/tag/v0.5.0
[0.5.1]: https://github.com/logicminds/filemaid/releases/tag/v0.5.1
[0.4.0]: https://github.com/logicminds/filemaid/releases/tag/v0.4.0
[0.3.0]: https://github.com/logicminds/filemaid/releases/tag/v0.3.0
[0.2.0]: https://github.com/logicminds/filemaid/releases/tag/v0.2.0
[0.1.0]: https://github.com/logicminds/filemaid/releases/tag/v0.1.0
