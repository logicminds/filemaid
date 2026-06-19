# filemaid — Product Requirements Document

## 1. Purpose

filemaid is a local, AI-assisted file organizer for macOS. It classifies files that land on the user's Desktop or Downloads, moves them into a categorized archive, applies Finder tags, and quarantines uncertain items for review. It also runs scheduled cleanup of stale development artifacts and stale review-queue items.

This PRD describes the behavior of the existing Python implementation so it can be faithfully rebuilt in Go. It also notes Go-specific implementation considerations.

## 2. Target Platform & Environment

- **OS:** macOS 13+ (uses `launchctl`, `xattr`, `mdimport`, `osascript`).
- **Architecture:** Apple Silicon primary; Intel support desirable via Homebrew path fallbacks.
- **Runtime:** Single static Go binary; no Python runtime required after rewrite.
- **External dependency:** Local Ollama server at `http://localhost:11434` with a configured model.
- **Permissions:**
  - Shortcuts folder automations run in the user session and do **not** require Full Disk Access.
  - The launchd scan agent may require Full Disk Access for the binary until the user grants it.

## 3. Product Goals

1. **Automate inbox zero** for Desktop/Downloads by moving files into a user-defined archive.
2. **Classify privately** using only a local LLM; no cloud services.
3. **Never lose data:** uncertain items are quarantined for review; deletions are restricted to explicit safe patterns or known duplicates.
4. **Reclaim disk space** by cleaning stale developer caches and the review queue.
5. **Run unattended** via launchd agents or Shortcuts folder automations.

## 4. Functional Requirements

### 4.1 CLI

The binary exposes one top-level command with subcommands. All subcommands load and resolve configuration, open the SQLite database, and configure logging.

```
filemaid process <paths>...
filemaid scan [--dir <directory>]
filemaid cleanup [--dry-run] [--format table|json]
filemaid review [--open]
filemaid logs --tail <n>
filemaid config
```

| Command | Purpose |
|---------|---------|
| `process` | Classify and act on the supplied file paths. |
| `scan` | Scan `watch_dirs` (or a single `--dir`) and process every eligible file. |
| `cleanup` | Run enabled dev-artifact and review-queue cleaners. |
| `review` | List the review queue, or open it in Finder with `--open`. |
| `logs` | Print the last `n` lines of the application log. |
| `config` | Print the fully resolved configuration as JSON. |

### 4.2 File Eligibility

Files are eligible for processing only when **all** of the following are true:

- Path exists and is a regular file (not a directory or symlink target).
- Not hidden: name does not start with `.` and is not `.localized` or `.DS_Store`.
- Resides inside one of the directories in `allowed_dirs`.
- For `scan`, modification time is at least `min_age_hours` old.

If `allowed_dirs` is empty, the whitelist check is disabled.

### 4.3 Classification

#### 4.3.1 Decision Object

Every classification produces a `Decision` value:

| Field | Type | Description |
|-------|------|-------------|
| `category` | string | One of the configured category names, or `Unknown`. |
| `tags` | []string | 1–3 Finder tag suggestions. |
| `action` | string | `move`, `delete`, or `review`. Default: `review`. |
| `destination` | string | Optional explicit destination path; normally empty. |
| `reason` | string | Human-readable explanation. |

#### 4.3.2 Prompt Construction

For each file, the classifier builds a prompt containing:

- Absolute path, basename, lowercase extension, size in bytes, ISO modification time.
- The list of configured category names.
- For images (`png`, `jpg`, `jpeg`, `gif`, `webp`, `heic`): a note that the image is attached, and the image is sent as base64 to the LLM.
- For text files (`txt`, `md`, `csv`, `json`, `xml`, `yaml`, `yml`, `py`, `js`, `ts`, `jsx`, `tsx`, `html`, `css`, `sh`, `zsh`, `bash`, `swift`, `c`, `cpp`, `h`, `rs`, `go`, `java`, `kt`, `rb`, `php`, `pl`, `sql`): the first 2048 bytes.

The model must return a single JSON object with keys `category`, `tags`, `action`, `destination`, and `reason`. `destination` must be empty.

#### 4.3.3 LLM Transport & Fallback

- Base URL: `ollama_url` (default `http://localhost:11434`).
- Model: `model` (default `filemaid-gemma4-26b`).
- Hidden config key `ollama_endpoint` controls strategy: `auto`, `chat`, or `generate` (default `auto`).

Strategies:

1. **`/api/chat`** with a `classify_file` tool schema that enforces the category enum and action enum.
2. **`/api/generate`** with a system prompt that insists on JSON-only output.

For each configured strategy, try the request **with images** (if the file is an image) and then **without images**. If any request succeeds and parses, use that decision. If all fail, return `category=Unknown`, `action=review` with a reason describing the last error or parse failure.

Temperature, max tokens, and context size are fixed to conservative values (temperature `0.2`, `num_predict=512`, `num_ctx=8192`).

### 4.4 Pre-classification Rules

#### 4.4.1 Age Rules

Before invoking the LLM, evaluate `age_rules`. Each rule has:

- `pattern` — `fnmatch` pattern matched against the path (home directory rendered as `~`) and the absolute path.
- `days` — minimum age in days.
- `action` — action to force (default `review`).

If a file matches and is old enough, return a synthetic `Decision` with `category=Unknown`, `tags=["age-rule"]`, the configured action, and a reason identifying the matched rule. This bypasses the LLM.

### 4.5 Duplicate Detection

Compute a SHA-256 hash of every processed file. Look up the most recent history row with the same hash whose `final_path` still exists on disk. If found, the file is a duplicate.

Duplicate handling:

- If the duplicate also matches `safe_delete_patterns`, force `action=delete`.
- Otherwise, if the action is not already `review`, coerce it to `review` and append `; duplicate detected` to the reason.

### 4.6 Action Engine

The action engine applies a `Decision` to a source file.

#### 4.6.1 Whitelist

Both source and destination must be inside `allowed_dirs`. The check uses normalized absolute paths: the path must equal an allowed directory or start with `allowed_dir + os.PathSeparator`.

If the source is not allowed, skip it. If the computed destination is not allowed, fall back to the review directory.

#### 4.6.2 Delete Safety

`delete` is honored only if **either**:

- The file matches a pattern in `safe_delete_patterns`, **or**
- The file is a detected duplicate.

Otherwise, coerce the action to `review` and rewrite the reason to note that delete was refused.

#### 4.6.3 Move / Review

Determine destination:

1. If `decision.destination` is non-empty, use it.
2. Else if `decision.category` exists in `config.categories`, use `config.categories[category] / basename`.
3. Else use `review_dir / YYYY-MM-DD / basename`.

If the action is `review`, force the destination to `review_dir / YYYY-MM-DD / basename`.

If a file already exists at the destination, generate a unique name by appending an ISO timestamp (with colons removed) to the stem.

Create parent directories, move the file, and (if `tags` is true) apply Finder tags.

#### 4.6.4 Delete Execution

When delete is approved, move the file to the macOS Trash using AppleScript:

```applescript
tell application "Finder" to delete POSIX file "<path>"
```

Record the final path as the literal string `trash`.

#### 4.6.5 Finder Tags

Tags are written by setting the extended attribute `com.apple.metadata:_kMDItemUserTags` to a binary plist encoding of the tag list, using `xattr -w -x`, then calling `mdimport <path>` to refresh Spotlight metadata. Failures are swallowed so they never abort a move.

#### 4.6.6 Recording

After every successful action, insert a row into the `history` table with:

- original_path
- final_path (or `"trash"`)
- sha256
- category
- tags (comma-joined string)
- action
- reason

### 4.7 State & Persistence

#### 4.7.1 SQLite Database

Path: `db_path` (default `~/.local/share/filemaid/filemaid.db`).

Schema:

```sql
CREATE TABLE IF NOT EXISTS history (
    id INTEGER PRIMARY KEY,
    original_path TEXT NOT NULL,
    final_path TEXT,
    sha256 TEXT,
    category TEXT,
    tags TEXT,
    action TEXT,
    reason TEXT,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX IF NOT EXISTS idx_sha256 ON history(sha256);
```

#### 4.7.2 Duplicate Lookup

Return the most recent row for a given SHA-256 ordered by `created_at DESC`.

### 4.8 Cleanup

Cleanup runs a fixed registry of cleaners in order:

1. `docker`
2. `npm`
3. `cargo`
4. `pip`
5. `brew`
6. `xcode`
7. `review`

Each cleaner is skipped if:

- Its short name is not in `allowed_cleaners`.
- `can_run()` returns false.
- It is disabled in `dev_cleanup.<name>.enabled` (cleaners default to enabled).

Results are printed as a table or JSON. Each result carries `name`, `status`, `saved` (bytes), `saved_human`, `detail`, and `command`.

#### 4.8.1 Cleaner Behavior

| Cleaner | `can_run()` | Safe mode | Aggressive mode |
|---------|-------------|-----------|-----------------|
| **docker** | `docker` binary exists | `docker image prune -f` | `docker system prune -af --volumes` |
| **npm** | `npm` binary exists | `npm cache clean --force` | same |
| **cargo** | `cargo` and `cargo-cache` binaries exist | `cargo cache --autoclean` | same |
| **pip** | always | `python3 -m pip cache purge` | same |
| **brew** | `brew` binary exists | `brew cleanup --prune=7` | `brew cleanup --prune=all` |
| **xcode** | `xcodebuild` exists or `~/Library/Developer/Xcode/DerivedData` exists | Remove DerivedData entries older than 30 days | Remove all DerivedData |
| **review** | always | Delete review-queue files older than `review_cleanup.max_age_days` | Delete all review-queue files |

For `npm`, `cargo`, `pip`, and `brew`, saved bytes are estimated as `max(0, before_size - after_size)` of the relevant cache directory. For `docker`, saved bytes are parsed from the command output (`Total reclaimed space:`). For `xcode` and `review`, saved bytes are the sum of deleted file sizes.

After a real review cleanup, remove any empty directories inside the review dir (excluding the review dir itself).

### 4.9 Scheduling & Triggers

#### 4.9.1 Shortcuts Folder Automation

Users can create a Personal Automation in Shortcuts for `Desktop` and `Downloads`:

- Shell: `/bin/zsh`
- Pass input: as arguments
- Command:
  ```zsh
  export PATH="/usr/local/bin:/opt/homebrew/bin:$PATH"
  "$HOME/.local/bin/filemaid" process "$@"
  ```

This is the recommended trigger because it avoids Full Disk Access.

#### 4.9.2 launchd Agents

- **Scan agent** `biz.logicminds.filemaid.scan`: runs `filemaid scan` every 15 minutes (`StartInterval=900`) and at load.
- **Cleanup agent** `biz.logicminds.filemaid.cleanup`: runs `filemaid cleanup` at 06:00, 12:00, 18:00, and 23:00, and at load.

Both agents redirect stdout/stderr to `~/.local/share/filemaid/{scan,cleanup}.log` and `.err`.

#### 4.9.3 Installation

- Detect a suitable Python interpreter for the existing version (≥ 3.12). For the Go rewrite, the install script should instead place the compiled Go binary at `~/.local/bin/filemaid`.
- Create directories: `~/.config/filemaid`, `~/.local/share/filemaid`, `~/.filemaid/review`, `~/.local/bin`, `~/Library/LaunchAgents`.
- Recommend an Ollama model by installed RAM: ≥ 24 GB → `filemaid-gemma4-26b`, ≥ 16 GB → `filemaid-gemma4-12b`, else `filemaid-metadata`.
- Run an interactive configuration interview to confirm or override watch directories, archive location, Finder tags, dev cleaners, review-queue retention, and safe-delete patterns. The `--no-interactive` flag skips this interview.
- Copy `config.json` to `~/.config/filemaid/config.json` if it does not exist, updating it with the selected model and any interview answers.
- Generate `biz.logicminds.filemaid.cleanup.plist` and optionally `biz.logicminds.filemaid.scan.plist` dynamically and write them to `~/Library/LaunchAgents`.
- Create custom Ollama models from `modelfiles/` if Ollama is installed.
- Bootstrap agents with `launchctl bootstrap gui/$(id -u)`.

#### 4.9.4 Uninstallation

- `launchctl bootout` both agents.
- Remove `~/Library/LaunchAgents/biz.logicminds.filemaid.*.plist` (these are generated dynamically by `filemaid setup`).
- Remove `~/.local/bin/filemaid` wrapper/binary.
- **Do not** remove config, logs, database, or review queue.

## 5. Configuration

### 5.1 Setup Interview

`filemaid setup` runs an interactive interview that asks the user to confirm or override:

- Ollama model (RAM-based recommendation).
- Watch directories (`watch_dirs`).
- Archive base directory (drives `categories`).
- Whether to apply Finder tags (`tags`).
- Which development cache cleaners to enable (`dev_cleanup`).
- Review-queue retention in days (`review_cleanup.max_age_days`).
- Safe-delete glob patterns (`safe_delete_patterns`).

Pressing `Enter` at any prompt accepts the default. The `--no-interactive` flag skips the interview and writes the shipped defaults, substituting only the selected model.

### 5.2 Resolution

1. Start with built-in defaults.
2. Load `~/.config/filemaid/config.json` if it exists.
3. Recursively expand `~` to the user's home directory.
4. For top-level keys whose default is a dict, merge the user's dict on top of defaults (shallow merge per key).
5. Ensure `categories["Unknown"]` exists and points to `review_dir`.

### 5.3 Schema

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `ollama_url` | string | `http://localhost:11434` | Ollama base URL. |
| `model` | string | `filemaid-gemma4-26b` | Model tag. |
| `watch_dirs` | []string | `["~/Desktop", "~/Downloads"]` | Directories scanned by `scan`. |
| `allowed_dirs` | []string | Desktop, Downloads, `~/Documents/Archive`, `~/.filemaid/review` | Whitelist for source and destination paths. |
| `allowed_cleaners` | []string | `docker`, `npm`, `cargo`, `pip`, `brew`, `xcode`, `review` | Cleaners permitted to run. |
| `review_dir` | string | `~/.filemaid/review` | Quarantine directory. |
| `log_path` | string | `~/.local/share/filemaid/filemaid.log` | Application log file. |
| `db_path` | string | `~/.local/share/filemaid/filemaid.db` | SQLite database file. |
| `tags` | bool | `true` | Whether to apply Finder tags. |
| `min_age_hours` | number | `0` | Minimum file age for `scan` eligibility. |
| `categories` | map<string,string> | See `config.json` | Category name → destination folder. |
| `safe_delete_patterns` | []string | `[]` | `fnmatch` patterns that may be deleted. |
| `age_rules` | []object | `[{pattern:"~/Downloads/*.dmg", days:30, action:"review"}]` | Age-based pre-classification rules. |
| `dev_cleanup` | map<string,object> | Per-cleaner `{enabled:true, mode:"safe"}` | Cleaner settings. |
| `review_cleanup` | object | `{enabled:true, mode:"safe", max_age_days:30}` | Review-queue cleanup settings. |

### 5.4 Custom Ollama Models

Three Modelfiles are shipped:

| Model | Base | Use case |
|-------|------|----------|
| `filemaid-gemma4-26b` | `gemma4:26b-a4b-it-qat` | Default high-quality vision model. |
| `filemaid-gemma4-12b` | `gemma4:12b-it-qat` | Faster vision fallback. |
| `filemaid-metadata` | `qwen2.5:7b` | Filename/text-only; ignores image content. |

All models set low temperature, JSON-only system prompts, and stop sequences for code fences.

## 6. Safety & Fail-safes

1. **Fail-safe classification:** any LLM error, timeout, parse failure, or ambiguous category becomes `Unknown` + `review`.
2. **Whitelist:** source and destination paths must be inside `allowed_dirs`.
3. **Delete gate:** `delete` is coerced to `review` unless the file matches `safe_delete_patterns` or is a duplicate.
4. **Duplicate review:** duplicates that are not safe-to-delete are sent to review.
5. **Trash fallback:** if Finder trash fails, fall back to review.
6. **Tag failures swallowed:** a tagging error must never abort a move.

## 7. Logging

- Log level: `INFO`.
- Format: `%(asctime)s %(levelname)s %(message)s`.
- Destinations: stderr and a rotating file (5 MB max, 1 backup).
- Each processed file is logged with source, result path, category, action, and reason.
- Cleanup prints a human table or JSON; all cleaner outcomes are also logged.

## 8. Go Implementation Notes

These notes are non-functional guidance for the rewrite.

### 8.1 CLI

Use a mature CLI library such as `cobra` or `urfave/cli`. Match the exact subcommand names, flags, and defaults above so existing Shortcuts and launchd plists continue to work.

### 8.2 Configuration

- Use `os.UserHomeDir()` or `path/filepath` to expand `~`.
- Preserve nested merging for `categories`, `dev_cleanup`, and `review_cleanup`.
- Load config once at startup and pass a typed struct through the application.

### 8.3 LLM Client

- Use `net/http` with explicit request/response timeouts (120 s per request, 300 s for docker commands).
- Encode images with `encoding/base64`.
- Implement the same `auto`/`chat`/`generate` strategy and `with-images`/`without-images` retry loop.
- Use Go structs to parse Ollama responses; fallback to string scanning for JSON extraction from `/api/generate` output.
- Allow injection of an HTTP client interface for tests.

### 8.4 macOS Integration

- Finder tags: write a binary plist to `com.apple.metadata:_kMDItemUserTags` using `xattr -w -x`, then run `mdimport`. Use `howett.net/plist` or implement a minimal binary plist encoder.
- Trash: execute the same `osascript` one-liner.
- Path discovery: search Homebrew prefixes `/opt/homebrew/bin`, `/usr/local/bin`, plus `$PATH`.

### 8.5 State

- Use SQLite. `modernc.org/sqlite` is preferable for a static binary without CGO; `mattn/go-sqlite3` is an alternative if CGO is acceptable.
- Keep the same schema and index.
- Wrap DB operations in a small repository type so tests can substitute an in-memory store.

### 8.6 Cleaners

- Define an interface:
  ```go
  type Cleaner interface {
      Name() string
      CanRun() bool
      Run(dryRun bool, cfg Config) CleanupResult
  }
  ```
- Register cleaners in a fixed slice matching the current order.
- Preserve `allowed_cleaners`, `enabled`, and `mode` gating.

### 8.7 Concurrency

- The Python version processes files sequentially. The Go version may process files concurrently, but must:
  - Serialize SQLite writes or use a single writer goroutine.
  - Ensure duplicate detection sees the latest committed history rows before a file is acted upon.
  - Avoid concurrent `osascript`/`xattr`/`mdimport` calls on the same path.
- A worker pool of size `min(4, runtime.NumCPU())` is a reasonable starting point.

### 8.8 Testing Strategy

- Use interfaces for filesystem, subprocess, HTTP client, and clock.
- Provide `fakeFS`, `fakeRunner`, and `fakeOllama` test doubles; avoid mocking frameworks.
- Cover:
  - Whitelist acceptance/rejection.
  - Age-rule preemption.
  - Duplicate detection and delete coercion.
  - Delete-only-for-safe-patterns invariant.
  - Cleaner dry-run vs real run.
  - Config merging and `~` expansion.

### 8.9 Packaging

- Ship as a single `filemaid` binary plus the `modelfiles/` directory and plist files.
- `go install github.com/logicminds/filemaid/cmd/filemaid@latest` should be supported.
- The install script should be rewritten in `zsh` (or Go) to deploy the binary, config, and launchd agents.

## 9. Non-Goals

- Windows or Linux support in v1.
- Cloud LLM providers.
- GUI, web dashboard, or menubar app.
- Network sync or cloud backup.
- Full-text indexing beyond Finder tags.
- In-place renaming without moving.

## 10. Acceptance Criteria

1. `filemaid process <file>` classifies the file and moves it into the correct archive category or review directory.
2. Finder tags are applied to moved files when `tags` is true.
3. `filemaid scan` processes all eligible files in `watch_dirs` older than `min_age_hours`.
4. `filemaid cleanup --dry-run` reports the commands and estimated space for every enabled cleaner without modifying the system.
5. `filemaid cleanup` actually runs enabled cleaners and reports saved bytes.
6. A file matching an `age_rule` is forced to the configured action regardless of LLM output.
7. A duplicate file that is not safe-to-delete is sent to review.
8. A file requesting `delete` without a safe pattern or duplicate status is coerced to `review`.
9. Install script places the binary, config, plists, and Ollama models; uninstall removes only agents and the binary.
10. The review-queue cleaner removes stale files and reports counts accurately.

## 11. Open Questions for Stakeholder

None assumed. The PRD should be updated if any of the following change:
- Supported LLM backends (e.g., adding OpenAI-compatible local servers).
- New cleaners or cleanup policies.
- Cross-platform ambitions beyond macOS.
