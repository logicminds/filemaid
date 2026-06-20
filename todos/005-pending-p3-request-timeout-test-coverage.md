---
status: pending
priority: p3
issue_id: "005"
tags: [code-review, testing, config, go]
dependencies: ["001"]
---

# Missing test coverage for configurable `RequestTimeout` edge cases

## Problem Statement

The new `RequestTimeout` configuration field is consumed in `internal/llm` for Ollama request timeouts, but there are no tests verifying its behavior at boundaries: zero value, negative value, very large value, or the string-vs-numeric JSON form. The `internal/config` tests pass with the default numeric value but do not exercise user overrides.

## Findings

- `internal/config/config.go:71` sets default `RequestTimeout: 120 * time.Second`.
- `internal/llm/llm.go:292-295`, `606-608`, `645-647`: each use site falls back to a hard-coded default when `timeout <= 0`.
- No unit tests assert that a custom timeout is actually passed through to the HTTP request, or that zero/negative values trigger the fallback.
- This is blocked/related to issue `001`: once the JSON format is fixed, tests should cover both string and numeric forms.

## Proposed Solutions

### Option 1: Add unit tests for timeout plumbing

**Approach:**
- In `internal/config/config_test.go`, test loading `request_timeout` as string and number.
- In `internal/llm/llm_test.go`, add a test fake transport that records the deadline/context timeout and assert it matches the configured value.

**Pros:**
- Prevents regressions in config parsing and timeout application.
- Documents expected fallback behavior.

**Cons:**
- Some effort to set up fake transport deadline inspection.

**Effort:** 1-2 hours

**Risk:** Low

---

### Option 2: Add integration-style test through `Validate`

**Approach:** Test `Validate` with a fake transport that asserts the request context deadline is approximately `cfg.RequestTimeout`.

**Pros:**
- Exercises end-to-end timeout configuration.

**Cons:**
- Less direct than unit tests.
- Flaky if timing-sensitive.

**Effort:** 1 hour

**Risk:** Medium

## Recommended Action

Implement Option 1 after resolving `001`: add config parsing tests for string/numeric/zero/negative forms, and add LLM unit tests verifying the configured timeout reaches the HTTP request context.

## Technical Details

**Affected files:**
- `internal/config/config_test.go`
- `internal/llm/llm_test.go`

**Related components:**
- `internal/llm/llm.go` - timeout consumers.

## Resources

- Related todo: `001-pending-p1-config-request-timeout-string.md`.

## Acceptance Criteria

- [ ] `config.LoadPath` tests cover string, numeric, zero, negative, and missing `request_timeout` values.
- [ ] LLM tests verify that a custom `RequestTimeout` sets the request context deadline.
- [ ] LLM tests verify zero/negative timeout falls back to the default.
- [ ] All tests pass.

## Work Log

### 2026-06-20 - Code Review Discovery

**By:** Claude Code

**Actions:**
- Reviewed `internal/llm/llm_test.go` and `internal/config/config_test.go` changes.
- Noticed no new tests for timeout behavior despite the new config field and use sites.

**Learnings:**
- Timeout fallbacks are duplicated in multiple use sites; centralizing or testing them would improve confidence.
