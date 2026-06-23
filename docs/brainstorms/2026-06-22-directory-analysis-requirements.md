---
date: 2026-06-22
topic: directory-analysis
---

# Directory Analysis & Recommendations

## Problem Frame

`filemaid` currently ignores directories. Subdirectories in `~/Downloads` (project folders, extracted archives, `.app` bundles, etc.) are skipped because `scan` and `process` only handle regular files. Users accumulate these folders without a quick way to understand what they contain, whether they are still needed, or whether they can be safely removed. The goal is to classify the *directory as a unit* and recommend an action, without disturbing its internal files.

## Requirements

- **R1.** Add an `--include-dirs` flag to `filemaid scan` and `filemaid process` that opts into directory analysis. Without the flag, behavior remains exactly as today.
- **R2.** When `--include-dirs` is set, the command considers immediate subdirectories of the scanned/provided paths as candidates, in addition to regular files.
- **R3.** For each directory candidate, filemaid gathers lightweight metadata about its contents (e.g. file tree, total size, modification time, top-level file types) and sends it to the LLM for classification.
- **R4.** Directory classification produces a recommendation from a constrained set: `keep`, `review`, `trash`, or `archive`. The default safe fallback is `review`.
- **R5.** Directory analysis is read-only by default: it must not move, rename, delete, or modify files inside the directory, and must not move the directory itself. Output is a recommendation and rationale.
- **R6.** Directory candidates respect existing guardrails: `allowed_dirs`, hidden-directory skipping, and `min_age_hours` (using the directory's modification time).
- **R7.** Results for directories appear alongside file results in the existing output formats (table, human, json), clearly marked as directories so the user can distinguish them from file actions.
- **R8.** If a directory has already been analyzed and recorded in history, prefer the cached decision unless `--force` is used.

## Success Criteria

- Running `filemaid scan --include-dirs` on `~/Downloads` surfaces every immediate subdirectory and prints a recommendation for each.
- The user can tell from the output which rows are directories and what the LLM concluded they contain.
- No files inside analyzed directories are moved, renamed, or deleted.
- `--include-dirs` is off by default, so existing automation and scans are unaffected.

## Scope Boundaries

- **In scope:** Immediate subdirectories of watch dirs / explicitly provided paths; `.app` bundles treated as directories.
- **Out of scope:** Recursive descent into nested subdirectories, moving or deleting directories, archiving directory contents by extracting files, and applying Finder tags to directories.
- **Out of scope:** A separate `inspect` command; the feature is integrated into existing `scan` and `process`.

## Key Decisions

- **Opt-in flag (`--include-dirs`)**: Keeps existing scans predictable and avoids surprising users who rely on the current file-only behavior.
- **Read-only recommendations only**: Directories are more complex than files and carry higher risk of accidental data loss. The first version should inform, not act.
- **Recommendations limited to four values**: `keep`, `review`, `trash`, `archive`. This maps cleanly to filemaid's existing mental model while staying simple.
- **Reuse existing classifier pipeline**: Directory metadata is sent through the same Ollama-based classifier used for files, minimizing new infrastructure.

## Dependencies / Assumptions

- Assumes the current LLM prompt can be extended with a directory-specific instruction without degrading file classification.
- Assumes the existing `processResult` output format can accommodate a new `kind=directory` indicator without breaking consumers.

## Outstanding Questions

### Deferred to Planning
- [Affects R3][Technical] What exact metadata should be captured for a directory (depth of listing, sample file names, size thresholds, binary vs text detection)?
- [Affects R7][Technical] Should directory results use the same `processResult` type with an added `Kind` field, or a parallel result type?
- [Affects R8][Technical] Can the existing decision cache store directory decisions, or does it need a separate keying scheme?

## Next Steps

→ `/ce:plan` for structured implementation planning
