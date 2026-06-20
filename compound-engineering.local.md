---
review_agents: [code-simplicity-reviewer, security-sentinel, performance-oracle, architecture-strategist]
plan_review_agents: [code-simplicity-reviewer]
---

# Review Context

`filemaid` is a local, macOS-only Go CLI for automated file organization. Code conventions:

- Go standard library plus Cobra only; `go.mod` declares only `github.com/spf13/cobra` as a direct dependency.
- Go 1.23+ required.
- Synchronous, procedural architecture; state flows through typed `config.Config` and a SQLite-backed `state.Repo`.
- macOS-specific integrations: `xattr`/`mdimport` for Finder tags, `osascript` for trash, `launchctl` for LaunchAgents.
- Fail-safe classification: any LLM error, parse failure, timeout, or ambiguous result becomes `category="Unknown"`, `action="review"`.
- Delete safety: `action="delete"` is only honored if the file matches `safe_delete_patterns` or is a duplicate; otherwise coerced to `"review"`.
- Whitelist safety: `allowed_dirs` gates source/destination paths; `allowed_cleaners` gates cleaners.
- Embedded default assets under `internal/setup/assets`; setup is self-contained from a single binary.
- Subprocess calls are fire-and-forget; failures logged but do not abort moves.

Pay special attention to: path traversal, unsafe subprocess usage, LLM prompt injection, data loss around file moves/trash, SQLite/schema correctness, launchd plist correctness, and coverage of edge cases in tests.
