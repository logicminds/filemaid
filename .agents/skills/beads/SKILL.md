---
name: beads
description: Use when working in a repository that uses bd or Beads for durable project task tracking, issue dependencies, blocker management, multi-session handoff, or shared work memory. Trigger when the user asks to find ready work, claim or close tasks, create follow-up work, inspect blockers, recover project context, or choose between local planning and persistent project tracking.
---

# Beads

Use Beads as the shared project task system. Local plans, scratch files, and personal memories are useful, but they are not the durable source of truth for project work.

## First Step

Run:

```bash
bd prime
```

If that prints nothing, check whether the repository has an active Beads workspace:

```bash
bd where
```

## Preferred Route

Use the `bd` CLI when shell access is available. It is the most compact and direct Beads interface.

## Core CLI Workflow

1. Find work:

```bash
bd ready
bd list --status=open
bd list --status=in_progress
```

2. Create a branch for the logical group of work before claiming or editing code:

```bash
git checkout -b <group-name>
# examples: feature/foo, bug/fix-bar, perf/reduce-allocation
```

3. Inspect before editing:

```bash
bd show <id>
```

4. Claim work atomically:

```bash
bd update <id> --claim
```

5. Create durable follow-up work when implementation reveals new tasks:

```bash
bd create "Short title" --description="Why this exists and what needs to be done" --type=task --priority=2
```

6. Commit the code changes for the task before closing it:

```bash
git add .
git commit -m "<type>: <summary> (<id>)"
```

7. Validate and test the committed changes:

```bash
# run the project's quality gates (tests, lints, builds)
go test ./...
go vet ./...
```

8. Simplify and refine the changed code before opening a pull request:

```bash
# Run the ce-simplify-code skill against the branch diff before pushing.
# The skill will review changed code for reuse, quality, and efficiency,
# apply safe fixes, and run tests/lints to verify behavior is preserved.
```

9. Push the branch and open a pull request once the work is complete:

```bash
git push -u origin <branch-name>
gh pr create --title="<summary>" --body="Closes <id>"
# or use the repository's PR workflow if gh is unavailable
```

10. Close completed work:

```bash
bd close <id> --reason="Completed"
```

## Branch & Commit Rules

- **Always create a branch to submit work.** Never commit directly to the default branch.
- A branch groups logically related work (a feature, bug fix, performance fix, refactor, etc.).
- Multiple bead tasks may share a branch when they belong to the same logical group.
- **Commit code before closing a bead task.** Do not run `bd close` until the task's changes are committed.
- If you discover additional work while on a branch, create new bead tasks for it and either complete them on the same branch or spin off a new branch if the new work is logically separate.
- If you are on the default branch and about to claim or edit code, stop and create a branch first.

## What Belongs In Beads

Use Beads for:

- shared project tasks
- blockers and dependencies
- discovered follow-up work
- work that must survive thread reset, compaction, or handoff
- status that another person or agent should be able to resume

Use agent-local planning tools only for the current turn's execution checklist. Do not treat them as shared project state.

## Rules

- Do not create markdown TODO files as the source of truth when Beads is available.
- Do not use `bd edit`; it opens an interactive editor. Use `bd update` flags instead.
- Prefer `--json` when parsing `bd` output programmatically.
- If hooks are installed, `bd prime` may already be injected. Run it manually when context is missing.
- Do not auto-close or mutate tasks unless the work is actually complete.
- **Always create a git branch before doing bead work.** Never commit directly to the default branch.
- **Commit code changes before closing a bead task.** Do not run `bd close` until the changes for that task are committed.
- **Validate and test code before closing a bead task.** Run tests, lints, and builds for the committed changes before running `bd close`.
- **Push the branch and open a pull request when the work is complete.** Do not leave finished work unmerged on the local branch.
- **Simplify and refine changed code before pushing.** Run `skill:ce-simplify-code` on the branch diff before opening the pull request.
