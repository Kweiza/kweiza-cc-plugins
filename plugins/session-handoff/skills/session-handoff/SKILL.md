---
name: session-handoff
description: "Use when wrapping up a work session, switching to a new conversation, or before a context reset — ensures progress, decisions, and next steps are preserved across sessions"
user-invocable: true
argument-hint: "[optional: notes about what to focus on next]"
---

# Session Handoff

Wrap up the current session and prepare a clean handoff for the next one. Nothing is lost, and the next session starts immediately productive.

## When to Use

- Ending a work session
- About to `/clear` or start a new conversation
- Handing off to a different model or agent
- Before a long break where context will be lost

## Process

Perform ALL of the following steps in order:

### Step 1: Summarize This Session

Review the full conversation and produce a structured summary:

**What was accomplished:**
- List each major task completed (with file paths and commit SHAs where relevant)
- Note key decisions made and why

**What changed:**
- Run `git log --oneline` to capture commits from this session
- Run `git diff --stat HEAD~N` (where N = number of session commits) for a file change overview
- Note any config/settings changes made outside the repo

**What's unfinished or blocked:**
- Tasks started but not completed
- Known issues discovered but not fixed
- Questions that need answers before proceeding

### Step 2: Save to Memory

Save a **project memory** if this session produced context that future sessions need and that is NOT derivable from git log or reading the code. If nothing qualifies, skip this step.

Format:
```markdown
---
name: Session handoff YYYY-MM-DD — [topic]
description: [one-line summary of what was accomplished and what's next]
type: project
---

[Key decisions, context, or state the next session needs]

**Why:** [what drove these decisions]
**Next steps:** [what should happen next]
```

### Step 3: Update Project Docs (if needed)

Check if any of the following need updating:
- `CLAUDE.md` — new build commands, changed conventions, new project structure
- `.claude/rules/` — new or updated rules

Only update if there's a real change. Don't touch docs just to touch them.

### Step 4: Plan Next Session

Based on what's unfinished and any `$ARGUMENTS` provided:

**Priority items for next session:**
1. [Most important task — with specific file paths and context]
2. [Second task]
3. [etc.]

**Context the next session needs:**
- Current branch and its state
- Any in-progress work to continue
- External dependencies or blockers

### Step 5: Write Starter Prompt

Write a **copy-pasteable prompt** the user can paste into the next session. It must:
- Give full context without requiring the previous conversation
- Reference specific files, branches, and tasks
- Be actionable — the new session starts working immediately

~~~
```
[Starter prompt — ready to paste]
```
~~~

Keep it under 500 words. Include the current git branch and last commit SHA.

### Step 6: Final Checklist

- [ ] All work is committed (`git status`)
- [ ] All work is pushed (`git log --oneline origin/main..HEAD`)
- [ ] No sensitive data in uncommitted files
- [ ] Memory saved (or confirmed nothing to save)
- [ ] Starter prompt written

Report checklist results to the user.

## Notes

- If `$ARGUMENTS` provided, treat as guidance for next session focus
- The starter prompt must work even without memory access or prior context
