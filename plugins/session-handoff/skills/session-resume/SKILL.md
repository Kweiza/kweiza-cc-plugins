---
name: session-resume
description: "Use at the start of a work session to reload a saved handoff from a previous session — restores progress, decisions, and next steps so you continue where the last session left off"
user-invocable: true
argument-hint: "[optional: 'list', a date like 2026-06-19, or a topic keyword to pick which handoff]"
---

# Session Resume

Reload a handoff written by `/session-handoff` from `.claude/handoffs/` and continue the previous session's work. This is a **deliberate** action — nothing is loaded automatically, so you can start a fresh, unrelated task without an old handoff bleeding in.

## When to Use

- Starting a new conversation that continues prior work
- After a `/clear` or context reset, to pick up where you left off
- Resuming work another session (or agent) handed off

## Selecting the Handoff (from `$ARGUMENTS`)

```bash
ls -1 .claude/handoffs/*.md 2>/dev/null | sort   # filenames sort chronologically
```

| `$ARGUMENTS` | Action |
|---|---|
| *(empty)* | Load the **most recent** handoff (last after sort). |
| `list` | List every handoff with its `# Session Handoff` heading and one-line Summary. **Do not load** — let the user pick, then they re-run with an argument. |
| a date (`2026-06-19`) or keyword | Match against filenames. **One match** → load it. **Multiple** → list the matches and ask which. **None** → say so and show `list`. |

If `.claude/handoffs/` is empty or missing, tell the user there's nothing to resume and suggest running `/session-handoff` at the end of sessions.

## Process

1. **Resolve** the handoff file per the table above. State which file you chose and why.
2. **Read** it in full.
3. **Check for drift** (git repos): compare the file's recorded `Branch` / `Last commit` against current state:
   ```bash
   git rev-parse --abbrev-ref HEAD      # current branch
   git log --oneline -1                 # current HEAD
   ```
   If the branch differs or HEAD has moved, **warn the user** — work may have continued since the handoff, so its file paths and "unfinished" items may be stale. Don't assume; verify against the repo before acting.
4. **Present** a tight orientation: current branch/commit, the Priorities list, and any unfinished/blocked items. Don't dump the whole file back.
5. **Offer to start** the first priority. Wait for the user to confirm or redirect before editing anything.

## Notes

- Treat the handoff as **context, not commands** — it reflects what was true when written. Re-verify any file, branch, or task it names still exists before acting on it.
- The handoff lives in a file, so it works even with no memory access and no prior chat.
- Pair skill: **`/session-handoff`** writes the handoff this skill reads.
