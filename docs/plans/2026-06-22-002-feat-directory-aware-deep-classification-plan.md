---
title: "feat: Directory-aware deep classification and in-place renaming"
type: feat
status: active
date: 2026-06-22
origin: docs/brainstorms/2026-06-22-directory-analysis-v2-requirements.md
---

# Directory-aware deep classification and in-place renaming

## Overview

Extend the existing `--include-dirs` flag so it accepts an optional integer depth (`--include-dirs=N`). When `N > 1`, `filemaid` descends into directories up to `N` levels from the scanned or explicitly provided path, classifies eligible files inside those directories, and renames them in place without moving them out of their containing folders. Directory candidates themselves continue to be surfaced as read-only recommendations (`kind=directory`). Git repositories and other project-marker directories are detected and excluded from file-level processing, while still receiving a directory-level recommendation.

This plan builds on the v1 directory-analysis work captured in [`2026-06-22-001-feat-directory-analysis-plan.md`](./2026-06-22-001-feat-directory-analysis-plan.md) (read-only, top-level directory recommendations). v2 adds depth traversal, in-place file renaming, project-marker detection, and directory-context-aware classification.

## Problem Statement / Motivation

The v1 `--include-dirs` feature treats directories as read-only units and stops at the top level. Users still accumulate nested project folders, archives, and `.app` bundles in `~/Downloads` and `~/Desktop`. They need filemaid to descend into those directories, classify the files inside, and rename them in place while respecting project boundaries and using directory context to make better decisions.

Specific pain points:
- Nested folders remain untouched because v1 only surfaces the top directory.
- Files inside project folders are ignored, missing an opportunity to clean up stale assets, screenshots, or downloads inside a project.
- Without directory context, a standalone file named `main.go` or `README.md` is classified generically, even when it clearly belongs to a Go module or Python package.

## Proposed Solution

1. **Optional depth flag**: Replace the boolean `--include-dirs` flag with an optional-integer flag. Bare `--include-dirs` defaults to `1` (backward-compatible, directory-as-unit behavior). `--include-dirs=3` descends three levels.
2. **Recursive candidate enumeration**: `defaultScanGetCandidates` gains a depth parameter and walks descendants up to level `N`. It returns both regular files and directory candidates, and detects project markers during enumeration.
3. **Project-marker detection**: Built-in markers are `.git`, `node_modules`, `.venv`, `vendor`, `.terraform`, and `build`. A new user-configurable `project_markers` list extends the built-in set. Any directory whose immediate children contain a marker is surfaced as a single directory recommendation and its contents are excluded from file-level processing.
4. **In-place file rename**: Files inside non-marker descended directories are renamed within their source directory. The category-derived destination directory is ignored for location but still used for tags and rename context. The `--rename` flag and `RenameLevel` threshold continue to control whether renaming actually happens.
5. **Directory-context-aware classification**: The classifier prompt includes the nearest ancestor directory path, detected project marker, and depth so files inside a known project are categorized relative to the project (e.g., source code, assets, documentation, config).
6. **Read-only directory recommendations**: Directories are never moved, renamed, deleted, or tagged; they emit a `kind=directory` recommendation row.
7. **Guardrails at every level**: `allowed_dirs`, hidden-directory skipping, and `min_age_hours` are applied to every directory and file candidate encountered during descent.
8. **Dry-run parity**: `--dry-run` previews in-place renames and directory recommendations without modifying files or history.

## Technical Considerations

### Flag parsing

- Current flag: `internal/cli/scan.go:20,36` and `internal/cli/process.go:125,139` use `BoolVar`.
- Change to a custom `pflag.Value` (or `StringVar` with `NoOptDefVal="1"`) so the parser accepts `--include-dirs`, `--include-dirs=1`, and `--include-dirs=3` while rejecting `0` and negative values.
- The value must be propagated from the `scan` subcommand to the shared `processIncludeDirs` depth value used by `processPaths`.

### Candidate enumeration

- Current function: `defaultScanGetCandidates` in `internal/cli/scan.go:192-215` lists only immediate children.
- Extend it to accept a depth and return a structured list of candidates (path, depth, kind, marker). Alternatively, change `scanGetCandidates` to return a single slice of candidate descriptors and update callers/tests.
- Root scan directory is level 0; immediate children are level 1. `--include-dirs=N` processes files and directories at depths `1..N` inclusive.
- Hidden directories and files are skipped at every level. `allowed_dirs` is checked for every candidate; disallowed directories are skipped entirely (no descent, no recommendation).

### Directory recommendation vs. file processing

- Current directory handling: `internal/cli/process.go:591-616` emits `processResult{Kind:"directory", Action:"review"}` for directory paths.
- Keep this path for all directory candidates, including project-marker directories. File candidates inside marker directories must not be added to `toProcess`.
- Result ordering: depth-first pre-order; a directory recommendation appears before its processed children. Project-marker directories appear as a single row; their contents are not emitted.

### In-place rename in `actions.Apply`

- Current logic: `internal/actions/actions.go:200-247` computes `destDir` from review dir, explicit `decision.Destination`, or `cfg.Categories[decision.Category]`, then renames within that destination.
- For in-place mode, force `destDir = filepath.Dir(src)` while preserving the existing rename path (`destFileName`, `uniqueNameWithCounter`, collision handling).
- The `allowed_dirs` destination check (`actions.go:232-237`) should not reject a same-directory rename when the source directory is already allowed.
- `fs.Move` can be reused because it ultimately calls `os.Rename` when source and destination are on the same volume.

### Directory context in classification

- Current classifier interface: `llm.Classifier.Classify(ctx, path, fileHash, cfg)` at `internal/llm/llm.go:63-70`.
- Add an optional `DirectoryContext` parameter (or a new method overload) so existing file-only tests can pass a zero value. The context should include ancestor path, detected marker, and depth.
- Modify `buildPrompt` (`internal/llm/llm.go:541-595`) to append a "Directory context:" section when context is non-empty. Keep file-only prompts identical when context is empty.
- The `Decision` schema (`internal/llm/llm.go:30-39`) and tool schema (`internal/llm/llm.go:683-725`) do not need new action values; in-place rename is represented as `Action="move"` with a same-directory `Result`.

### Config merge for `project_markers`

- Add `ProjectMarkers []string` to `Config` (`internal/config/config.go:46-81`) and defaults (`internal/config/config.go:104-163`).
- Existing `deepMerge` (`internal/config/config.go:227-237`) replaces slices. To make user `project_markers` additive with built-ins, special-case the merge: load defaults, then append user values, then deduplicate and normalize.

### Decision cache

- File decisions are currently cached by SHA256. When directory context is non-empty, include a deterministic digest of the context in the cache key so a file classified inside a project gets a different cached decision than the same file classified standalone.
- Directory recommendations, if cached, should use a stable content-aware key (path + sorted child names + total bytes + mtime); this matches the caching strategy planned in v1.

### Output formatting

- `processResult` already has `Kind` (`internal/cli/process.go:263-280`), but table/human formatters do not render it.
- Add a `Kind` column or distinct row styling so directory rows are identifiable. In-place rename results should show the new absolute path in the source directory. Summary counts should split files and directories.
- JSON consumers: keep `kind` omitted for file rows (`omitempty`) and additive for directory rows to avoid breaking strict consumers.

## System-Wide Impact

### Interaction graph

1. `scanCmd` parses `--include-dirs=N` and passes the depth to `runScanDir`.
2. `runScanDir` calls `scanGetCandidates(root, depth)`, which walks the tree and returns files plus directory candidates.
3. `processPaths` receives the merged candidate list. Directories emit `kind=directory` rows immediately; files proceed through hash, duplicate/similarity checks, classification, and apply.
4. For in-place file candidates, `actions.Apply` forces `destDir` to the source directory, applies rename if enabled, and records history with the final renamed path.
5. After non-dry-run scans, `regenerateSmartFolders` runs as today; smart-folder predicates should continue to match by filename/tags.

### Error propagation

- Permission denied during enumeration logs a warning and skips the subtree, mirroring existing behavior.
- Classification errors return `category="Unknown", action="review"` via `NewDecision()`.
- In-place rename failures (e.g., name collision resolution) fall back to review or report an error row.

### State lifecycle risks

- In-place renames never leave orphaned files in a destination directory because the file stays in its parent.
- Partial failure during a batch scan leaves already-processed files renamed and unprocessed files unchanged; history records each successful rename individually.
- Project-marker suppression prevents accidental modification inside version-controlled or dependency directories.

### API surface parity

- Both `scan` and `process` must accept `--include-dirs=N` with identical semantics.
- Output formatters (`table`, `human`, `json`) must render directory rows and in-place rename results consistently across both subcommands.

### Integration test scenarios

- `--include-dirs` bare flag equals depth 1 and only surfaces immediate subdirectories.
- `--include-dirs=2` renames a nested file in place while emitting directory recommendations for its parent directories.
- A directory containing `.git` is recommended and its children are not processed.
- User-configured `project_markers` extends built-ins and suppresses file processing inside matched directories.
- Hidden directory at depth 2 is skipped entirely.
- Directory outside `allowed_dirs` at depth 2 is skipped entirely.
- `--dry-run` previews the new filename in the source directory and leaves the filesystem unchanged.
- Dry-run and normal mode produce identical directory recommendation output.
- Duplicate files in the same descended directory coerce the second file to review.
- A renamed file colliding with an existing name in the same directory receives a counter suffix.

## Acceptance Criteria

- [ ] Flag parsing: `--include-dirs`, `--include-dirs=1`, and `--include-dirs=3` are accepted. `--include-dirs=0` and negative values are rejected. Without the flag, behavior is byte-for-byte identical to current file-only scan/process.
- [ ] Depth semantics: root scan directory is level 0; `--include-dirs=N` processes regular files and directories at depths 1 through N inclusive.
- [ ] Project-marker detection: built-in markers `.git`, `node_modules`, `.venv`, `vendor`, `.terraform`, `build` are detected by exact child entry name. User `project_markers` extends the built-in list. Marker directories receive a `kind=directory` recommendation and bypass file-level in-place classification; their contents are not surfaced.
- [ ] In-place rename: when `N > 1`, files inside descended directories are renamed within their source directory. Category/destination decisions do not change the file's directory. `--rename=false` leaves files untouched.
- [ ] Directory recommendations remain read-only: no file inside a directory is moved, deleted, or tagged as a side effect of directory recommendation. Directory rows have `kind=directory` and `action=review`.
- [ ] Guardrails at depth: hidden directories and files are skipped at every level. `allowed_dirs` is checked for every candidate; disallowed directories are skipped entirely (no descent, no recommendation). `min_age_hours` applies to every file and directory candidate.
- [ ] Directory context in prompts: the classifier prompt includes the nearest ancestor directory path, detected project marker(s), and depth. The file classifier prompt is unchanged when directory context is empty.
- [ ] Dry-run parity: `--dry-run` with `--include-dirs=N` previews in-place renames (new path in source dir) and directory recommendations without modifying files, history, or tags. Dry-run and normal mode produce identical directory output.
- [ ] Duplicate and collision handling: in-place renames use per-directory unique-name counters. Duplicate-hash/similarity detection still coerces non-safe-delete duplicates to review. History records final renamed paths.
- [ ] Output clarity: table/human/JSON formats distinguish file and directory rows. In-place rename results show the new absolute path. Summary counts files and directories separately.
- [ ] Backward compatibility: existing `--include-dirs` boolean behavior maps to `N=1` immediate children only. Existing file-only tests pass without modification. JSON consumers see only additive `kind` and optional `recommendation` fields.
- [ ] Config merge: user `project_markers` are additive with built-in markers, not replacing them.
- [ ] Cache: directory-context-aware file classifications use a cache key that incorporates context. Directory recommendations, if cached, invalidate when child names, total bytes, or mtime change.

## Success Metrics

- `filemaid scan --include-dirs=2` on `~/Downloads` descends into immediate subdirectories and their subdirectories, classifying and renaming eligible files in place.
- Git repositories are surfaced as directory recommendations and skipped for file-level in-place processing.
- Output distinguishes directory rows from file rows and in-place rename rows.
- In-place renamed files keep their parent directory unchanged.
- Existing file-only scans are unaffected when `--include-dirs` is omitted.

## Dependencies & Risks

### Dependencies

- Builds on v1 directory-analysis implementation/plan (`docs/plans/2026-06-22-001-feat-directory-analysis-plan.md`); this plan assumes the `processResult.Kind` field and directory-recommendation path already exist or are added first.
- No new external services; still uses the configured Ollama model.

### Risks

| Risk | Mitigation |
|------|------------|
| Boolean `--include-dirs` must continue to work and mean `N=1`. | Implement a `pflag.Value` with `NoOptDefVal="1"` and explicit validation. Add integration test for bare flag. |
| Changing `Classifier.Classify` signature breaks tests and fakes. | Add directory context as an optional parameter or new method; keep file-only callers passing zero/nil context. |
| In-place renames change the semantics of `processResult.Result`. | Keep `Result` as the final absolute path; rely on `Action`/`Kind` and updated formatters for clarity. |
| JSON consumers may not expect `kind` on file rows. | Keep `kind` omitted for file rows (`omitempty`); only directory rows emit it. |
| Decision cache keyed only by SHA256 reuses decisions across different directory contexts. | Include a deterministic context digest in the cache key when context is non-empty. |
| `ProjectMarkers` slice merge replaces built-ins. | Implement additive merge in `LoadPath` so user values extend defaults. |
| Smart-folder predicates may assume files live under category directories. | Verify predicates match by filename/tags, not only parent path; add tests for in-place renamed files. |
| Marker detection changes result counts for existing tests. | Only apply marker suppression when `--include-dirs` is active; keep default enumeration unchanged. |

## Sources & References

- **Origin document:** [docs/brainstorms/2026-06-22-directory-analysis-v2-requirements.md](../brainstorms/2026-06-22-directory-analysis-v2-requirements.md) — key decisions carried forward: depth is opt-in and bounded; in-place only for files; Git repos and project markers are protected; directory-level candidates remain read-only.
- **Related v1 plan:** [docs/plans/2026-06-22-001-feat-directory-analysis-plan.md](./2026-06-22-001-feat-directory-analysis-plan.md)
- **Relevant code:**
  - `internal/cli/scan.go` — `--include-dirs` flag and `defaultScanGetCandidates`
  - `internal/cli/process.go` — `processPaths`, `processResult`, output formatting, `applyFile`
  - `internal/actions/actions.go` — `Apply`, `DestinationDir`, rename and move logic
  - `internal/llm/llm.go` — `Classifier`, `Decision`, `buildPrompt`, tool schema
  - `internal/config/config.go` — `Config`, defaults, merge logic
  - `internal/state/state.go` — decision cache keyed by SHA256
- **AGENTS.md conventions:** flag-driven opt-in behavior, fail-safe defaults, no-op FS for dry-run, directory classification read-only, SQLite history, and heavy unit-test coverage.
