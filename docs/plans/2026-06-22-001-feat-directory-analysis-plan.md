---
title: feat: Add opt-in directory analysis to scan and process
type: feat
status: active
date: 2026-06-22
origin: docs/brainstorms/2026-06-22-directory-analysis-requirements.md
---

# Add opt-in directory analysis to `scan` and `process`

## Overview

Add an `--include-dirs` flag to `filemaid scan` and `filemaid process`. When enabled, immediate subdirectories of the scanned or explicitly provided paths become analysis candidates. The tool gathers bounded metadata about each directory, asks the configured Ollama model for a read-only recommendation (`keep`, `review`, `trash`, or `archive`), and prints the result alongside normal file output. Directories are never moved, renamed, deleted, or modified.

This feature is intentionally read-only in its first version because directories carry higher data-loss risk than individual files.

## Problem Statement / Motivation

`filemaid` currently ignores directories. Subdirectories in `~/Downloads` — project folders, extracted archives, `.app` bundles — accumulate without a quick way to understand what they contain or whether they are still needed. Users want to classify the *folder as a unit* without disturbing its internal files.

The existing pipeline assumes every candidate is a regular file: it computes SHA-256 hashes, looks up decisions by hash, applies `move`/`delete`/`review` actions via `actions.Apply`, records history, and writes Finder tags. None of these steps map cleanly to a read-only directory recommendation.

## Proposed Solution

1. **Opt-in flag**: Add `--include-dirs` to `scan` and `process`. Default behavior remains file-only.
2. **Directory candidate enumeration**: In `scan`, `defaultScanGetFiles` collects immediate subdirectories in addition to files when the flag is set. In `process`, any directory passed as an argument is accepted as a candidate.
3. **Metadata gathering**: A new helper produces a bounded snapshot of each directory: path, base name, total size, child count, directory mtime, sorted top-level extension counts, and markers for special entries (`.git`, `node_modules`, `.venv`, `.DS_Store`). No recursion beyond immediate children; caps on sampled filenames and total entries to avoid huge prompts.
4. **Directory-specific classification**: Add a new classifier path that sends directory metadata to the LLM with a directory prompt and tool schema. The schema returns a `recommendation` field (`keep|review|trash|archive`) separate from the file-oriented `action` field.
5. **Read-only result path**: Directory candidates bypass `actions.Apply`, history recording, and `processFS`. The result is a `processResult` with `kind: "directory"` and the recommendation in a dedicated field.
6. **Output**: Table header becomes `Item`; directories are prefixed with `dir:` or a distinct glyph. Human output prints `Recommendation:` instead of `Action:`. JSON gains an additive `kind` field. Summary reports item counts split by files and directories.
7. **Caching**: Directory recommendations are cached with a deterministic content-aware key (path + sorted child names + total bytes + mtime) so content changes invalidate the cache. `--force` re-analyzes.

## Technical Considerations

- **Classifier interface**: The current `llm.Classifier` interface is `Classify(ctx, path, fileHash, cfg)`. Directory classification needs a new method (e.g. `ClassifyDirectory(ctx, path, metadata, cfg)`) or an overload that accepts a `DirectoryMetadata` object. Keep the file classifier unchanged to avoid regressions.
- **Decision schema**: The existing `llm.Decision` struct is file-centric (`action` of `move|delete|review`, `destination`, `new_name`, `name_quality`). Directory analysis should return a separate `DirectoryDecision` struct (or extend `Decision` with a `Recommendation` field) to avoid overloading semantics.
- **Tool schema**: Add a second tool schema for directories with a `recommendation` enum and no `destination`/`new_name`/`name_quality` fields.
- **Cache**: `state.Repo` currently caches decisions by SHA256. Directory cache keys are not SHA256. Add a small separate table (e.g. `directory_decisions`) keyed by a prefixed stable key, or extend the existing `decisions` table with a `kind` column. Prefer a separate table to keep the file cache untouched.
- **Result type**: `processResult` needs a `Kind` field (`file` | `directory`). File rows default to `file` for backward compatibility. Add a `Recommendation` field for directory rows.
- **Dry-run**: Directory output must be identical in dry-run and normal mode because no filesystem changes occur.
- **Prompt size**: Cap metadata aggressively. A directory like `node_modules` or a photo library could otherwise generate a multi-megabyte prompt.

## System-Wide Impact

- **Interaction graph**: `--include-dirs` affects `scan` enumeration, `process` argument acceptance, classifier routing, result formatting, and summary counting. It does not touch `actions.Apply`, `state.Record`, or Finder tags for directory candidates.
- **Error propagation**: Directory metadata gathering failures (e.g. permission denied) should produce a `review` recommendation with the error in `Reason`, mirroring the file-level fail-safe behavior.
- **State lifecycle risks**: Because directories bypass `actions.Apply` and history recording, there is no risk of orphaned moves or partial trash operations. The main risk is an oversized or privacy-leaking prompt; metadata bounds mitigate this.
- **API surface parity**: Both `scan` and `process` must support `--include-dirs` with identical semantics. The `format` flag (`table|human|json`) must render directory rows consistently in both commands.
- **Integration test scenarios**: 
  - `--include-dirs` with a `.app` bundle treated as a directory.
  - `--dry-run` and normal mode produce identical directory output.
  - Directory with many children respects metadata caps.
  - `--force` ignores cached directory recommendation.
  - Hidden directories are skipped.
  - Directory outside `allowed_dirs` is skipped.

## Acceptance Criteria

- [ ] `filemaid scan --include-dirs` surfaces every immediate subdirectory of `~/Downloads` and prints a recommendation for each.
- [ ] `filemaid process --include-dirs ~/Downloads/project-folder` analyzes the directory and prints a recommendation without modifying the directory or its contents.
- [ ] Directory recommendations are limited to `keep`, `review`, `trash`, and `archive`; the default fallback is `review`.
- [ ] Directory results are clearly distinguishable from file results in table, human, and JSON output.
- [ ] No files inside analyzed directories are moved, renamed, deleted, or tagged.
- [ ] `--include-dirs` is off by default; existing file-only behavior is unchanged.
- [ ] Directory analysis respects `allowed_dirs`, hidden-directory skipping, and `min_age_hours`.
- [ ] Directory metadata collection is bounded: immediate children only, max entry count, max sampled filename bytes, and no recursion.
- [ ] Directory recommendations are cached unless `--force` is used; cache invalidates on content changes.
- [ ] `.app` bundles are treated as directories.
- [ ] Dry-run and normal mode produce identical directory output.
- [ ] Existing file classification prompt, tool schema, and `llm.Decision` behavior are unchanged.

## Success Metrics

- Running `filemaid scan --include-dirs` on `~/Downloads` lists every immediate subdirectory with a recommendation.
- Users can identify directory rows at a glance in all three output formats.
- No directory contents are modified during analysis.
- Existing test suite passes without modification when `--include-dirs` is not used.

## Dependencies & Risks

- **Dependencies**: Requires the configured Ollama model to understand a new directory prompt; no new external services.
- **Risks**:
  - LLM may hallucinate recommendations for opaque directories. Mitigation: default to `review` and require user confirmation before any future action.
  - Large directories could still produce large prompts despite caps. Mitigation: hard limits on sampled entries and bytes, plus truncation markers.
  - Reusing the existing `processResult` type could complicate output formatting. Mitigation: add `Kind` and `Recommendation` fields; keep file defaults.

## Sources & References

- **Origin document:** [docs/brainstorms/2026-06-22-directory-analysis-requirements.md](../brainstorms/2026-06-22-directory-analysis-requirements.md)
- **Relevant code:**
  - `internal/cli/scan.go` — scan enumeration and eligibility
  - `internal/cli/process.go` — process pipeline, `processResult`, output formatting
  - `internal/llm/llm.go` — classifier interface, prompt, tool schema, `buildDecision`
  - `internal/actions/actions.go` — `Apply`, `WithinAllowed`, `ComputeHash`
  - `internal/state/state.go` — decision cache keyed by SHA256
