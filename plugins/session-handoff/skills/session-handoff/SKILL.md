---
name: session-handoff
description: "Use when wrapping up a work session, switching to a new conversation, or before a context reset — ensures progress, decisions, and next steps are preserved across sessions"
user-invocable: true
argument-hint: "[optional: notes about what to focus on next]"
---

# Session Handoff

Wrap up the current session and write a durable handoff so the next session — yours, another model, or another agent — starts immediately productive. The handoff is saved to a **file** so it survives `/clear` and context resets regardless of what's still in the chat. Resume it later with `/session-resume`.

## When to Use

- Ending a work session
- About to `/clear` or start a new conversation
- Handing off to a different model or agent
- Before a long break where context will be lost

## Process

Perform ALL of the following steps in order.

First, capture the timestamp and repo state you'll reuse below:

```bash
date '+%Y-%m-%d %H:%M'                    # → STAMP (e.g. 2026-06-19 14:30)
git rev-parse --is-inside-work-tree 2>/dev/null   # "true" if this is a git repo
```

If **not** a git repo, skip all git commands below and note "(non-git session)" wherever git state is requested.

### Step 1: Summarize This Session

Review the full conversation and produce a structured summary:

**What was accomplished:**
- List each major task completed (with file paths and commit SHAs where relevant)
- Note key decisions made and why

**What changed (git repos):**
- `git log --oneline @{u}..HEAD` — commits not yet pushed (falls back: `git log --oneline -15` if no upstream)
- `git diff --stat @{u}..HEAD` — committed file changes since upstream
- `git status --short` and `git diff --stat` — **uncommitted** work (easy to miss; capture it)
- Note any config/settings changes made outside the repo

**What's unfinished or blocked:**
- Tasks started but not completed
- Known issues discovered but not fixed
- Questions that need answers before proceeding

### Step 2: Write the Handoff File

This is the durable artifact `/session-resume` reads. Write it even if you also save a memory.

Path: `.claude/handoffs/<YYYY-MM-DD-HHMM>-<topic-slug>.md` (create the dir if needed; `<topic-slug>` is 2–4 kebab-case words). Use this exact structure so resume can parse it:

~~~markdown
# Session Handoff — <STAMP>

**Branch:** <branch> · **Last commit:** <sha> <subject>
**Repo:** <repo path, or "(non-git session)">

## Summary
<Step 1: what was accomplished + key decisions>

## Unfinished / Blocked
<Step 1: in-progress, known issues, open questions>

## Next Session — Priorities
1. <most important task — specific file paths + context>
2. <second task>

## Starter Prompt
```
<the Step 5 starter prompt — ready to paste>
```
~~~

### Step 3: Save to Memory (if it qualifies)

Save a **project memory** only if this session produced context future sessions need that is NOT derivable from git log, the handoff file, or reading the code. If nothing qualifies, skip.

Write the file to your project memory directory (the path given in your memory instructions, e.g. `~/.claude/projects/<project-slug>/memory/`) using the format that matches the **existing** memory files there:

```markdown
---
name: session-handoff-<YYYY-MM-DD>-<topic-slug>
description: <one-line summary of what was accomplished and what's next>
type: project
---

<Key decisions, context, or state the next session needs>

**Why:** <what drove these decisions>
**Next steps:** <what should happen next>
```

**CRITICAL — then add a one-line pointer to `MEMORY.md` in that same directory:**
```
- [Session handoff <YYYY-MM-DD> — <topic>](session-handoff-<YYYY-MM-DD>-<topic-slug>.md) — <hook>
```
`MEMORY.md` is the index loaded into future sessions. **A memory file with no `MEMORY.md` entry will never load** — skipping this defeats the purpose. Match the `name:` (kebab-case slug) and `type:` placement to the other files already in the directory rather than this template if they differ.

### Step 4: Update Project Docs (if needed)

Update only if there's a real change — don't touch docs just to touch them:
- `CLAUDE.md` — new build commands, changed conventions, new project structure
- `.claude/rules/` — new or updated rules

### Step 5: Write Starter Prompt

Write a **copy-pasteable prompt** for the next session (also embedded in the handoff file from Step 2). It must:
- Give full context without requiring the previous conversation
- Reference specific files, branches, and tasks
- Be actionable — the new session starts working immediately
- Include the current git branch and last commit SHA (or "(non-git session)")

Keep it under 500 words. Show it to the user in a fenced block, and tell them they can instead just run `/session-resume` next session to reload it from the file.

### Step 6: Final Checklist

- [ ] Handoff file written to `.claude/handoffs/` (report its path)
- [ ] All work committed, or uncommitted work is intentional and noted (`git status --short`)
- [ ] Pushed, or unpushed commits are intentional and noted (`git log --oneline @{u}..HEAD`)
- [ ] No sensitive data in uncommitted files or the handoff file
- [ ] Memory saved **and indexed in MEMORY.md** (or confirmed nothing to save)
- [ ] Starter prompt written

Report checklist results to the user.

## Notes

- If `$ARGUMENTS` provided, treat as guidance for the next session's focus (weave into Priorities and the starter prompt).
- The handoff file and starter prompt must work even without memory access or prior context.
- Pair skill: **`/session-resume`** loads a saved handoff to start the next session.
