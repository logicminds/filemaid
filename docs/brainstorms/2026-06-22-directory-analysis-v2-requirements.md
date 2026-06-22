---
date: 2026-06-22
topic: directory-analysis-v2
---

# Directory-Aware Deep Classification & In-Place Renaming

## Problem Frame

The current `--include-dirs` feature treats directories as read-only units and stops at the top level. This surfaces folders for manual review but does not actually help the user clean them up. Users accumulate nested project folders, archives, and `.app` bundles in `~/Downloads` and `~/Desktop`. They need filemaid to descend into those directories, classify the files inside them, and rename them in place without moving them, while respecting project boundaries (e.g., Git repositories) and using directory context to make better decisions.

## Requirements

- **R1.** Extend `--include-dirs` to accept an optional integer depth: `--include-dirs=N` where `N >= 1` means "descend up to N directory levels from the scanned/provided path and process files found there." `--include-dirs` without a value defaults to `1` for backward compatibility with the existing directory-as-unit behavior.
- **R2.** When `--include-dirs=N` is set, classify and rename files in place inside directories up to depth `N`. Files are not moved out of their containing directories.
- **R3.** Continue to surface directory candidates themselves as read-only recommendations (`keep|review|trash|archive`) with `kind=directory`, so the user sees both the folder summary and the per-file actions.
- **R4.** Directories containing a `.git` directory are classified as Git repositories (or equivalent project type) at the directory level and are excluded from file-level in-place processing to avoid corrupting version-controlled projects. The directory still receives a recommendation.
- **R5.** Other project-marker directories (e.g., `node_modules`, `.venv`, `.terraform`) may also be excluded from file-level processing; the directory receives a recommendation summarizing the project type.
- **R6.** Directory context is used to improve file classification: files inside a known project type are categorized relative to the project (e.g., source code, assets, documentation, config) and are less likely to be moved.
- **R7.** Existing guardrails apply at every level: `allowed_dirs`, hidden-directory skipping, and `min_age_hours` (file and directory modification times).
- **R8.** `--dry-run` previews both directory recommendations and in-place file renames without modifying files.
- **R9.** Without `--include-dirs`, behavior remains exactly as today.

## Success Criteria

- `filemaid scan --include-dirs=2` on `~/Downloads` descends into immediate subdirectories and their subdirectories, classifying and renaming eligible files in place.
- Git repositories (folders containing `.git`) are surfaced as directory recommendations and skipped for file-level in-place processing.
- Output distinguishes directory rows (`dir:` / `Recommendation:`) from file rows (`Action:` / rename).
- In-place renamed files keep their parent directory unchanged.
- Existing file-only scans are unaffected when `--include-dirs` is omitted.

## Scope Boundaries

- **In scope:** Optional numeric `--include-dirs` depth; descending into directories up to that depth; in-place file classification and renaming; `.git` project detection and exclusion; directory context influencing file classification.
- **Out of scope:** Moving or deleting entire directories, extracting files from archives, applying Finder tags to directories, recursive classification beyond the configured depth, and processing inside Git submodules or worktrees differently from regular Git repos.
- **Out of scope:** A separate `inspect` command; the feature remains integrated into `scan` and `process`.

## Key Decisions

- **Depth is opt-in and bounded:** `--include-dirs=N` gives the user explicit control and prevents accidental full filesystem traversal.
- **In-place only for files:** Files inside descended directories are renamed in place but never moved, preserving project structure.
- **Git repos are protected:** Detecting `.git` at any level within a candidate directory avoids corrupting version-controlled projects while still providing a useful directory-level recommendation.
- **Directory-level candidates remain read-only:** Directories are still not moved, renamed, deleted, or tagged; only their files may be renamed in place.

## Dependencies / Assumptions

- Assumes the existing file classifier can be extended with directory-context hints without degrading file classification.
- Assumes the existing `processResult` output format can represent both directory recommendations and in-place file renames.
- Assumes in-place renaming can be implemented by setting destination equal to source directory while allowing name changes.

## Outstanding Questions

### Resolve Before Planning
- [Affects R1][User decision] Should `--include-dirs` with no value mean `1` (directory-as-unit) or switch to a default depth like `2`?
- [Affects R4][User decision] Beyond `.git`, which marker directories should block file-level in-place processing? (e.g., `node_modules`, `.venv`, `vendor`, `.terraform`, `build`)
- [Affects R6][User decision] How should directory context change file classification? Should it primarily avoid moving files, or should it also influence category/tag selection?

### Deferred to Planning
- [Affects R3][Technical] How do directory recommendations and file results interleave in output ordering?
- [Affects R4][Technical] Should Git repo detection stop traversal entirely for that subtree, or continue deeper for non-project files?
- [Affects R8][Technical] How does dry-run represent in-place renames in the destination/path fields?
- [Affects R2][Technical] Does in-place renaming bypass duplicate detection because the file stays in the same directory?

## Next Steps

→ `/ce:plan` for structured implementation planning
