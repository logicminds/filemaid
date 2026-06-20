---
status: pending
priority: p1
issue_id: "001"
tags: [code-review, config, bug, go]
dependencies: []
---

# Config `request_timeout` string cannot be loaded by Go's `time.Duration`

## Problem Statement

`config.json` now ships with `"request_timeout": "120s"`, and `internal/config.Config.RequestTimeout` is typed as `time.Duration`. Go's `encoding/json` cannot unmarshal a string like `"120s"` into `time.Duration`; it only accepts a number of nanoseconds. As a result, `config.LoadPath()` fails for the default user-facing config produced by `filemaid setup`, and any user who writes the natural `"120s"` value will break the application on startup.

This is a critical regression because the default configuration that setup installs is now unloadable.

## Findings

- `config.json` (and the embedded default under `internal/setup/assets/`) contains `"request_timeout": "120s"`.
- `internal/config/config.go:27` declares `RequestTimeout time.Duration`.
- Reproduction: create a config file with `"request_timeout": "120s"` and call `config.LoadPath(path)`; it returns `json: cannot unmarshal string into Go struct field Config.request_timeout of type time.Duration`.
- `go build ./...` currently passes, and `internal/config` tests pass, because the tests do not exercise the string form that users will actually have in `~/.config/filemaid/config.json`.

## Proposed Solutions

### Option 1: Custom JSON-unmarshalable duration type

**Approach:** Replace `time.Duration` with a project-specific `Duration` type (or use a library helper) that implements `UnmarshalJSON` to accept both `"120s"` strings and numeric nanoseconds, and `MarshalJSON` to emit a string or number.

**Pros:**
- Keeps user-facing config human-readable ("120s", "2m", etc.).
- Backward-compatible with any existing numeric configs.
- Standard pattern for Go config files.

**Cons:**
- Slight increase in config package code.
- Need to update any direct assignments to use the wrapper type.

**Effort:** 1-2 hours

**Risk:** Low

---

### Option 2: Change default config to numeric nanoseconds

**Approach:** Replace `"120s"` in `config.json` and embedded assets with `120000000000`.

**Pros:**
- No code changes required.
- Works immediately with `time.Duration`.

**Cons:**
- User-facing config becomes unreadable and error-prone.
- Users who naturally write `"120s"` will still break the app.
- Poor developer/user experience.

**Effort:** 15 minutes

**Risk:** Medium (poor UX, still fragile)

---

### Option 3: Keep `RequestTimeout` as `string` in Config

**Approach:** Change `Config.RequestTimeout` to `string` and parse it at use sites with `time.ParseDuration`.

**Pros:**
- Simplest unmarshaling.
- Human-readable config.

**Cons:**
- Loses typed duration benefits.
- Requires parsing in multiple places.
- Less idiomatic Go.

**Effort:** 1 hour

**Risk:** Medium

## Recommended Action

Implement Option 1: add a custom `Duration` type in `internal/config` that supports both string and numeric JSON forms, and add tests covering string, numeric, zero, and invalid values.

## Technical Details

**Affected files:**
- `internal/config/config.go` - `RequestTimeout` field type and `Defaults()`.
- `config.json` - shipped default value.
- `internal/setup/assets/config.json` - embedded default value (verify it matches).
- `internal/config/config_test.go` - add coverage.

**Related components:**
- `internal/llm/llm.go` - consumers of `cfg.RequestTimeout`.
- `internal/setup/setup.go` - copies the embedded config to `~/.config/filemaid/config.json`.

## Resources

- Reproduction command: `go run duration_check.go` (temporary file removed; recreate with `"request_timeout": "120s"` and `config.LoadPath`).
- Error: `json: cannot unmarshal string into Go struct field Config.request_timeout of type time.Duration`.
- Go docs: `time.Duration` JSON support is numeric-only.

## Acceptance Criteria

- [ ] A config file containing `"request_timeout": "120s"` loads successfully with `config.LoadPath()`.
- [ ] A config file containing `"request_timeout": 120000000000` still loads successfully.
- [ ] `go test ./internal/config/...` passes with new tests for string, numeric, zero, negative, and invalid timeout values.
- [ ] `filemaid setup` produces a working config that `filemaid config` can load.
- [ ] Embedded asset config matches the repo `config.json`.

## Work Log

### 2026-06-20 - Code Review Discovery

**By:** Claude Code

**Actions:**
- Reviewed current branch changes to `config.json` and `internal/config/config.go`.
- Ran `go run` reproduction with `"request_timeout": "120s"`; confirmed `LoadPath` returns an unmarshal error.
- Verified `go build` passes and existing config tests do not cover the string form.

**Learnings:**
- `time.Duration` JSON unmarshaling accepts only nanosecond integers, not `time.ParseDuration` strings.
- The shipped default config is the first thing users will have, so this is a default-broken scenario.
