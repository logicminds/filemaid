---
status: complete
priority: p2
issue_id: "002"
tags: [code-review, cli, bug, testing, go]
dependencies: []
---

# `processCmd.RunE` panics when called with a nil `*cobra.Command`

## Problem Statement

After the recent context-propagation change, `processCmd.RunE` calls `processPaths(cmd.Context(), args)`. If `cmd` is `nil` (as happens in `internal/cli/process_test.go:526`), `cmd.Context()` causes a nil pointer dereference panic. `scan.go` already guards against this with `if cmd != nil { ctx = cmd.Context() }`, but `process.go` does not, making the process command less robust and breaking an existing test.

## Findings

- `internal/cli/process.go:118`: `results, err := processPaths(cmd.Context(), args)`.
- `internal/cli/scan.go:39-42`: uses a safe fallback to `context.Background()` when `cmd` is nil.
- Test failure:
  ```
  panic: runtime error: invalid memory address or nil pointer dereference
  github.com/spf13/cobra.(*Command).Context(...)
  github.com/logicminds/filemaid/internal/cli.init.func5(.../process.go:118)
  github.com/logicminds/filemaid/internal/cli.TestProcessCommandSucceedsWhenValidationPasses(.../process_test.go:526)
  ```
- `go test ./internal/cli/...` fails on this test; all other packages pass.

## Proposed Solutions

### Option 1: Add nil guard in `processCmd.RunE` (recommended)

**Approach:** Mirror `scan.go`: initialize `ctx := context.Background()` and override with `cmd.Context()` only when `cmd != nil`.

**Pros:**
- Consistent with existing `scan.go` pattern.
- Fixes the panic without changing test code.
- Minimal diff.

**Cons:**
- None significant.

**Effort:** 5 minutes

**Risk:** Low

---

### Option 2: Update the test to supply a command

**Approach:** Change `processCmd.RunE(nil, []string{src})` to construct a `cobra.Command` with a context.

**Pros:**
- Keeps production code assuming a non-nil cmd.

**Cons:**
- Leaves the command vulnerable to panic if anyone else calls `RunE` directly.
- Requires changing test code when a simpler guard exists.

**Effort:** 15 minutes

**Risk:** Low

## Recommended Action

Implement Option 1: add the same nil-safe context initialization used in `scan.go` to `processCmd.RunE`.

## Technical Details

**Affected files:**
- `internal/cli/process.go` - `processCmd.RunE` context handling.

**Related components:**
- `internal/cli/scan.go` - already uses the safe pattern.
- `internal/cli/process_test.go` - `TestProcessCommandSucceedsWhenValidationPasses`.

## Resources

- Test failure log from `go test ./internal/cli/...`.
- Comparable safe code: `internal/cli/scan.go:39-42`.

- [x] `go test ./internal/cli/...` passes.
- [x] `processCmd.RunE(nil, args)` no longer panics and falls back to `context.Background()`.
- [x] Behavior when run normally through Cobra is unchanged.

## Work Log

### 2026-06-20 - Code Review Discovery

**By:** Claude Code

**Actions:**
- Ran `go test ./...` after external edits fixed the build.
- Observed panic in `TestProcessCommandSucceedsWhenValidationPasses`.
- Compared `process.go` and `scan.go` context handling; identified missing nil guard.

**Learnings:**
- `scan.go` already has the correct pattern; consistency within `internal/cli` would prevent this class of test panic.

### 2026-06-20 - Resolved During Review

**By:** External edit (user / automated fix)

**Actions:**
- `internal/cli/process.go:118-121` now mirrors `scan.go` with `ctx := context.Background()` and a nil guard before using `cmd.Context()`.
- Verified `go test ./...` passes (10 packages ok).

**Learnings:**
- The fix was applied concurrently with the review; no further action needed.
