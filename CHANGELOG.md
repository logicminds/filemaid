# Changelog

All notable changes to filemaid are documented in this file.

## Unreleased

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
[0.4.0]: https://github.com/logicminds/filemaid/releases/tag/v0.4.0
[0.3.0]: https://github.com/logicminds/filemaid/releases/tag/v0.3.0
[0.2.0]: https://github.com/logicminds/filemaid/releases/tag/v0.2.0
[0.1.0]: https://github.com/logicminds/filemaid/releases/tag/v0.1.0
