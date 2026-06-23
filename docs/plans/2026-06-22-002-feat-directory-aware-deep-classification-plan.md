---
title: feat: Directory-aware deep classification and in-place renaming
type: feat
status: active
date: 2026-06-23
origin: beads/filemaid-97x
---

# Directory-aware deep classification and in-place renaming

## Overview

Extend the `--depth` flag added in `filemaid-6ne` from a boolean
"immediate subdirectories" switch into an optional integer depth flag that
triggers deep, read-only directory analysis and, for regular files found inside
scanned directories, in-place renaming. Directories are still never moved,
renamed, deleted, or modified; files may be renamed within their source
directory when the LLM suggests a better name and the user has enabled rename.

This plan decomposes the epic `filemaid-97x` into the sub-issues below. Each
section states the contract that downstream work can depend on.

## Sub-issue decomposition

| Issue | What it does | Files touched |
|-------|--------------|---------------|
| `filemaid-uf2` | Add `project_markers` config option with additive merge. | `internal/config/config.go`, `config.json` |
| `filemaid-81r` | Bounded directory metadata gathering helper. | new `internal/directory/metadata.go` |
| `filemaid-7li` | Convert `--depth` from bool to optional integer depth flag. | `internal/cli/scan.go`, `internal/cli/process.go` |
| `filemaid-jyi` | Add directory-specific classifier prompt and tool schema. | `internal/llm/llm.go` |
| `filemaid-xc2` | Add `DirectoryDecision` type and result plumbing. | `internal/llm/llm.go`, `internal/cli/process.go` |
| `filemaid-8mg` | Recursive directory enumeration with project-marker detection. | `internal/cli/scan.go` |
| `filemaid-283` | Pass directory context into file classification prompts. | `internal/llm/llm.go` |
| `filemaid-ppu` | Support in-place file renaming in `actions.Apply`. | `internal/actions/actions.go` |
| `filemaid-kag` | Update output formatters for directory and in-place rename rows. | `internal/cli/process.go` |
| `filemaid-gma` | Unit and integration tests. | `*_test.go` |

## Contracts

### 1. Config — `project_markers`

`internal/config/config.go`:

```go
type Config struct {
    // ... existing fields ...
    ProjectMarkers []string `json:"project_markers"`
}
```

`Defaults()` returns `[]string{".git", "node_modules", ".venv", "vendor", ".terraform", "build"}`.
User-provided markers are **additive**: the merge layer unions user values with
defaults and normalizes each marker by trimming whitespace and removing leading
`./` and trailing path separators.

### 2. Directory metadata — `internal/directory`

New package `internal/directory`:

```go
package directory

type Metadata struct {
    Path        string
    Base        string
    Size        int64
    ChildCount  int
    Mtime       time.Time
    Extensions  map[string]int // sorted by name for stable prompts
    Markers     []string       // detected project markers from cfg.ProjectMarkers
    IsAppBundle bool
}

// Gather builds a bounded snapshot of dir. It walks only the immediate
// children. It returns an error only for fundamental failures; permission
// errors on individual children are recorded in the result and do not abort.
func Gather(dir string, cfg *config.Config) (*Metadata, error)
```

Bounds (configurable in `Config` with defaults):

* `MaxDirSampleEntries` default 50
* `MaxDirSampleBytes`   default 2048

The helper reports a `.app` bundle as a directory (`IsAppBundle == true`) and
stops enumerating its contents; it is treated as an opaque directory candidate.

### 3. `--depth` flag

In both `scan` and `process`:

```go
var scanDepth int
processCmd.Flags().IntVar(&processDepth, "depth", 0, "descend into directories N levels (0 = file-only)")
processCmd.Flags().Lookup("depth").NoOptDefVal = "1"
```

* `--depth` bare → depth 1
* `--depth=3` → depth 3
* depth <= 0 is treated as disabled (current file-only behavior)
* A negative value supplied on the CLI is rejected

Package-level variables `scanDepth` and `processDepth` of type `int` replace the
current `bool`.

### 4. Directory classifier

`internal/llm/llm.go` adds:

```go
type DirectoryDecision struct {
    Recommendation string `json:"recommendation"` // keep|review|trash|archive
    Reason         string `json:"reason"`
    Category       string `json:"category,omitempty"`
    Tags           []string `json:"tags,omitempty"`
}

// DirectoryClassifier classifies a directory from bounded metadata.
type DirectoryClassifier interface {
    ClassifyDirectory(ctx context.Context, meta *directory.Metadata, cfg *config.Config) (DirectoryDecision, Metrics, error)
}
```

`llm.Client` implements `DirectoryClassifier`. The directory prompt is separate
from the file prompt and returns a tool schema with only `recommendation`,
`reason`, `category`, and `tags`. The file classifier is unchanged.

Directory decisions are cached in a new `directory_decisions` SQLite table keyed
by a stable digest of `path + sorted child names + total bytes + mtime`.

### 5. Directory context for files

When a file is processed as part of a descended directory scan, the classifier
prompt includes:

```
Directory context:
- ancestor: ~/Downloads/project-folder
- depth: 2
- project marker: .git
```

This is passed via an optional `DirectoryContext` value in the classifier call.
The cache key for file decisions incorporates a digest of the directory context
so that the same file in different contexts can receive different decisions.

### 6. Recursive enumeration

`defaultScanGetCandidates` is replaced by `scanGetCandidates(root string, depth int) ([]string, []string, error)`.

For depth > 0:

1. List immediate children of `root`.
2. Apply guardrails at every level: `allowed_dirs`, hidden skip, `min_age_hours`.
3. For each child directory:
   * Gather metadata.
   * If it contains a project marker, emit the directory as a read-only
     recommendation and **do not recurse** into it.
   * If depth > 1 and no marker, recurse and collect files + subdirectories.
4. Return two slices: file candidates and directory candidates. Directory
candidates preserve their full paths.

### 7. In-place renaming

`actions.Apply` already accepts a `Decision` with `Action == "move"` and a
`Destination == ""`. When the destination directory resolves to the source file's
own directory, `Apply` performs a same-directory rename via `fs.Move(src, dest)`
using the existing `uniqueNameWithCounter` collision handling. No new action
value is introduced.

The LLM is instructed in the directory-context prompt that files inside
project-marker directories should suggest a `new_name` and set `destination` to
empty to trigger in-place rename.

### 8. Result plumbing

`processResult` gains:

```go
type processResult struct {
    // ... existing fields ...
    Kind            string `json:"kind,omitempty"`            // "file" or "directory"
    Recommendation  string `json:"recommendation,omitempty"`  // for directories
    Depth           int    `json:"depth,omitempty"`
}
```

Files default to `Kind: "file"`. Directory rows use `Recommendation` instead of
`Action`.

### 9. Output formatters

* Table header changes from `File` to `Item`.
* Directory rows show `dir:` prefix (or 📁 glyph) and `Recommendation:` in the
  action column.
* In-place rename rows show the new absolute path in the `Result` column.
* Summary splits counts: `3 files, 2 directories`.

## Acceptance criteria

- [ ] `--depth` bare is depth 1; `--depth=N` descends N levels.
- [ ] Project markers are additive with defaults and stop recursion.
- [ ] Directory metadata gathering is bounded and treats `.app` bundles as directories.
- [ ] Directory classifier returns `keep|review|trash|archive` and is cached.
- [ ] Directory candidates never modify files or directories.
- [ ] Files inside descended directories can be renamed in place.
- [ ] Directory context appears in file prompts for descended files.
- [ ] Output distinguishes directory rows and in-place rename rows in all formats.
- [ ] Existing file-only behavior is unchanged when `--depth` is absent.
- [ ] All new code has unit tests; end-to-end behavior has integration tests.

## Test plan

See `filemaid-gma`. Coverage targets:

* bare flag = depth 1
* depth-2 enumeration
* project marker stops recursion
* user-configurable `project_markers`
* hidden and `allowed_dirs` guardrails at depth
* `.app` bundle treated as directory
* dry-run preview for directory analysis
* duplicate handling inside same directory
* name collision counters for in-place rename
* unchanged behavior without `--depth`

## Risks

* Oversized prompts despite bounds. Mitigation: hard caps on entries and bytes.
* LLM hallucination for opaque directories. Mitigation: default to `review`.
* Concurrent applies in the same directory. Mitigation: `dirLockMap` already
  serializes by destination directory; in-place renames use the source directory.
