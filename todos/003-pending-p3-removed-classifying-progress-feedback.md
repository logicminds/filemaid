---
status: pending
priority: p3
issue_id: "003"
tags: [code-review, cli, ux, go]
dependencies: []
---

# Interactive "Classifying..." progress feedback was removed

## Problem Statement

`internal/cli/process.go` previously printed `Classifying <path>...` to `stderr` before each LLM classification. This gave users immediate feedback during the expensive, potentially multi-second classification step. The recent context-propagation change removed this line without a documented replacement, leaving users with no per-file progress indication until the final table is rendered.

## Findings

- `internal/cli/process.go:261` (old line ~260): removed `fmt.Fprintf(os.Stderr, "Classifying %s...\n", collapseHome(item.src))`.
- The removal appears to be a side effect of the context refactor rather than an intentional UX decision.
- Classification can take seconds per file (especially images on large models); silent processing feels like a hang in interactive use.

## Proposed Solutions

### Option 1: Restore the stderr progress line

**Approach:** Add the `fmt.Fprintf(os.Stderr, ...)` line back before `classifier.Classify`.

**Pros:**
- Restores previous UX.
- Simple one-line change.
- stderr keeps progress separate from structured stdout output.

**Cons:**
- Adds chatter; could be noisy for non-interactive use or JSON output.

**Effort:** 5 minutes

**Risk:** Low

---

### Option 2: Make progress output conditional on interactive stderr

**Approach:** Only print progress when `os.Stderr` is a terminal (`isatty`) and the output format is not JSON.

**Pros:**
- Avoids noise in scripts/JSON mode.
- Preserves interactive feedback.

**Cons:**
- Slightly more code.
- Adds a terminal check dependency.

**Effort:** 30 minutes

**Risk:** Low

---

### Option 3: Document the removal as intentional

**Approach:** If the removal was deliberate, update CLI help/behavior docs and rely on the final results table.

**Pros:**
- No code change.

**Cons:**
- Users lose progress feedback.

**Effort:** 15 minutes

**Risk:** Low

## Recommended Action

Triage decision needed. If interactive UX is a priority, implement Option 1 or Option 2. Option 2 is cleaner long-term.

## Technical Details

**Affected files:**
- `internal/cli/process.go` - classification loop.

**Related components:**
- `internal/cli/format.go` - output formatting (relevant if gating on JSON mode).

## Resources

- Diff shows removal at `internal/cli/process.go` classification loop.

## Acceptance Criteria

- [ ] Interactive `filemaid process` provides per-file progress feedback, OR
- [ ] The intentional removal is documented and communicated.
- [ ] JSON/non-interactive output is not polluted by progress messages (if Option 2 chosen).

## Work Log

### 2026-06-20 - Code Review Discovery

**By:** Claude Code

**Actions:**
- Reviewed diff for `internal/cli/process.go`.
- Noticed removal of the `Classifying...` stderr line.
- Compared with scan/process command UX expectations.

**Learnings:**
- The removal seems incidental to the context refactor, not a deliberate feature change.
