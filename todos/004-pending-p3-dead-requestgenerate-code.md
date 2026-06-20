---
status: pending
priority: p3
issue_id: "004"
tags: [code-review, cleanup, dead-code, go]
dependencies: []
---

# `requestGenerate` is dead code

## Problem Statement

`internal/llm/llm.go` contains `requestGenerate`, a helper for the Ollama `/api/generate` endpoint. After the recent refactor, no caller invokes it; `Validate` now calls `postJSON` directly with a typed `generateResponse`. Keeping the unused function adds maintenance surface and confuses readers about whether `/api/generate` is still a supported fallback path in `Classify`.

## Findings

- `internal/llm/llm.go:591-613`: `func (c *Client) requestGenerate(...)`.
- Search for call sites returns only the definition.
- `Validate` uses `postJSON(..., &resp)` directly.
- `Classify` only uses `requestChat` (with image retry); there is no generate fallback.

## Proposed Solutions

### Option 1: Remove `requestGenerate`

**Approach:** Delete the function and any tests that only exercise it.

**Pros:**
- Eliminates dead code.
- Reduces confusion.
- Slightly smaller binary.

**Cons:**
- If a future change wants to reintroduce a generate fallback, it must be recreated.

**Effort:** 10 minutes

**Risk:** Low

---

### Option 2: Use `requestGenerate` inside `Validate`

**Approach:** Refactor `Validate` to call `requestGenerate` instead of duplicating the request body construction.

**Pros:**
- Keeps the helper alive and useful.
- Centralizes generate-request logic.

**Cons:**
- Adds indirection.
- The body construction in `Validate` is intentionally minimal (tiny validation prompt), so the helper's generic options may not be ideal.

**Effort:** 15 minutes

**Risk:** Low

## Recommended Action

Implement Option 1: remove the dead `requestGenerate` function. If a generate fallback is needed later, it can be reintroduced cleanly.

## Technical Details

**Affected files:**
- `internal/llm/llm.go` - remove `requestGenerate`.
- `internal/llm/llm_test.go` - remove or update any tests targeting only `requestGenerate`.

## Resources

- Call-site search: `search pattern="requestGenerate" paths="internal/llm"` returned only the definition.

## Acceptance Criteria

- [ ] `requestGenerate` is removed.
- [ ] `go build ./...` and `go test ./internal/llm/...` pass.
- [ ] No other references to `requestGenerate` remain.

## Work Log

### 2026-06-20 - Code Review Discovery

**By:** Claude Code

**Actions:**
- Searched for all call sites of `requestGenerate`.
- Confirmed no production or test callers.
- Noted `Validate` now uses `postJSON` directly.

**Learnings:**
- The typed-response refactor made the old `map[string]any` generate helper obsolete.
