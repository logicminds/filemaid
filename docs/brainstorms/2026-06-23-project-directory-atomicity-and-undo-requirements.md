---
date: 2026-06-23
topic: project-directory-atomicity-and-undo
---

# Project Directory Atomicity & Undo

## Summary

filemaid should recognize cohesive project directories — folders whose files belong together, such as LaTeX/markdown source trees — as atomic units rather than processing each file independently. Recognition will combine marker-file detection with an LLM inspection of bounded directory evidence. Recognized project directories are moved as a whole, defaulting to the review queue. A new `undo` command will reverse filemaid moves and renames by reading the history database and restoring items to their original paths and names.

## Problem Frame

A recent `filemaid process` run treated `~/Downloads/Kimi_Agent_Marc's Radar-revised/` as a collection of individual files. The folder contained markdown sections (`marcsradar_sec00.md` through `sec08.md`), cross-reference files (`marcsradar_ref.md`), review files, rendered images, and generated Word documents that only make sense when kept together. filemaid moved many of those files into `~/.filemaid/review/` and the archive, breaking the working directory. The current directory-analysis feature (`--depth`) is read-only and built around dev project markers; it does not protect non-dev project folders, and there is no way to reverse a mistaken move.

## Key Decisions

- **Marker-first recognition, LLM fallback.** Directories containing known project markers (e.g. `.git`, `node_modules`, `.venv`, user-configured markers) are treated as projects immediately. When markers are absent or weak, filemaid asks the LLM to judge a bounded directory listing.
- **Atomic unit behavior.** Once a directory is recognized as a project, none of its contents are moved, renamed, or deleted individually. The entire directory is handled as one item.
- **Default to review.** Recognized project directories are moved to the review queue by default. Auto-archiving into a category is opt-in via configuration.
- **Undo is a first-class command.** The history table already records original paths and names, so undo is implemented as a stateful restore command rather than a separate journal or snapshot system.

## Requirements

### Project directory recognition

- R1. filemaid must detect project directories by marker files before invoking the LLM. Markers are drawn from the existing `project_markers` config list.
- R2. When no strong marker is present, filemaid must gather bounded directory evidence and pass it to the directory classifier for a project-vs-junk decision.
- R3. Directory evidence must include file names, extensions, sizes, modification times, and representative text snippets from text files inside the directory.
- R4. The directory classifier must return a decision that distinguishes cohesive project directories from random collections of files.
- R5. Recognition must apply to the directory being scanned, not recurse into nested subdirectories looking for inner projects.

### Project directory action

- R6. A directory recognized as a cohesive project must be treated as an atomic unit. No files inside it may be moved, renamed, deleted, or tagged individually.
- R7. Recognized project directories must be moved as a whole to the review queue by default.
- R8. When the user enables auto-project-archive, recognized project directories may be moved as a whole to a configured category destination.
- R9. Project directory moves must be recorded in history with the directory's original path and final path, and with action `move`.
- R10. Process and scan output must clearly distinguish project-directory rows from file rows, showing the directory path and its disposition.

### Undo / restore

- R11. filemaid must provide an `undo` command that restores moved or renamed items to their original paths.
- R12. The `undo` command must restore the original filename when `original_name` differs from `new_name`.
- R13. The `undo` command must support three modes: `--last` (most recent run), `--run <id>` (specific run), and a per-path target.
- R14. Undo must refuse to overwrite an existing file or directory at the original path unless `--force` is provided.
- R15. Undo must verify that the item still exists at `final_path` before attempting restoration.
- R16. Undo actions must be recorded in history so the restored state is itself reversible.

### Compatibility and safety

- R17. Existing file-only behavior must remain unchanged when no project directory is detected.
- R18. Project directory handling must respect existing guardrails: `allowed_dirs`, hidden-file skip, and `min_age_hours` applied to the directory's modification time.
- R19. Undo must only restore items whose original path is inside `allowed_dirs`, to prevent accidental restores outside the managed area.

## Key Flows

- F1. Recognize a project directory during `process` or `scan`
  - **Trigger:** `process` or `scan` encounters a directory candidate.
  - **Steps:**
    1. Check for project markers in the directory's immediate children.
    2. If a marker is found, classify the directory as a project.
    3. If no marker is found, gather bounded directory evidence and invoke the directory classifier.
    4. If the classifier returns `project`, treat the directory as an atomic unit.
  - **Outcome:** The directory is queued for whole-directory action; no individual files inside it are processed separately.

- F2. Undo a move or rename
  - **Trigger:** User runs `filemaid undo --last`, `filemaid undo --run <id>`, or `filemaid undo <path>`.
  - **Steps:**
    1. Resolve the target history rows based on the selected mode.
    2. For each row, verify the item exists at `final_path` and the original path is allowed.
    3. Compute the restore destination as `original_path` (which already encodes the original name).
    4. If the destination exists and `--force` is not set, skip and report the collision.
    5. Move the item from `final_path` to the restore destination.
    6. Record a new history row documenting the undo.
  - **Outcome:** The item is returned to its original location with its original name.

## Acceptance Examples

- AE1. **Markdown/LaTeX project folder stays intact.** Given a directory `~/Downloads/thesis/` containing `thesis.tex`, `thesis.bib`, `figures/`, and `sections/chapter1.md`, when `filemaid scan --depth` runs, then the entire `thesis/` directory is moved to `~/.filemaid/review/YYYY-MM-DD/thesis/` and no individual file is moved elsewhere.
- AE2. **Undo restores a renamed file.** Given a history row where `original_path` is `~/Downloads/IMG_1234.png`, `final_path` is `~/Documents/Archive/Images/Wedding_Table.png`, `original_name` is `IMG_1234.png`, and `new_name` is `Wedding_Table.png`, when the user runs `filemaid undo --run <id>`, then `Wedding_Table.png` is moved back to `~/Downloads/IMG_1234.png`.
- AE3. **Undo collision without force.** Given a history row where `original_path` is `~/Downloads/report.pdf` and a different file already exists at `~/Downloads/report.pdf`, when the user runs `filemaid undo --last` without `--force`, then the undo is skipped for that row and the user is warned.

## Scope Boundaries

- **In scope:** Recognizing cohesive project directories; moving them as atomic units; adding an undo command that reverses moves and renames using history.
- **Deferred for later:** Auto-creating project marker files for the user; recursive detection of nested project directories; archiving project directories by extracting their contents.
- **Outside this product's identity:** Cloud-based history snapshots, versioning, or a generalized filesystem time-machine. filemaid remains a local, history-backed organizer.

## Dependencies / Assumptions

- The history schema already records `original_path`, `final_path`, `original_name`, and `new_name` (see `internal/state/state.go`). `original_path` is the full pre-move path including the original filename.
- Directory-aware scanning is partially implemented via `--depth`, `project_markers`, `internal/directory.Gather`, `DirectoryDecision`, and `ClassifyDirectory` (see `internal/cli/scan.go`, `internal/llm/llm.go`).
- The action engine enforces `allowed_dirs`, safe-delete patterns, and duplicate rules through `internal/actions/actions.go`.
- A local Ollama server is available when LLM-based directory classification is needed.

## Outstanding Questions

### Resolve Before Planning

None.

### Deferred to Planning

- [Affects R3, R4][Technical] What exact directory evidence and prompt text produce the most reliable project-vs-junk classification?
- [Affects R8][Technical] What config key enables auto-project-archive, and which category should receive project directories?
- [Affects R14][Technical] What is the exact collision behavior for undo — skip, prompt, or generate a unique name — and how does `--force` interact with directories?
- [Affects R11][Technical] Should `undo` support `--dry-run` to preview restores without moving files?
