---
name: session-handoff
description: "End-of-session handoff — summarize progress, save to memory/docs, plan next session, write starter prompt"
user-invocable: true
argument-hint: "[optional: notes about what to focus on next]"
---

# Session Handoff

Wrap up the current session and prepare a clean handoff for the next one. This ensures nothing is lost between sessions and the next session starts with full context.

## Process

Perform ALL of the following steps in order:

### Step 1: Summarize This Session

Review the full conversation and produce a structured summary:

**What was accomplished:**
- List each major task completed (with file paths and commit SHAs where relevant)
- Note any decisions made and why

**What changed:**
- Run `git log --oneline` to capture commits from this session
- Run `git diff --stat HEAD~N` (where N = number of session commits) for a file change overview
- Note any config/settings changes made outside the repo

**What's unfinished or blocked:**
- Tasks that were started but not completed
- Known issues discovered but not fixed
- Questions that need answers before proceeding

### Step 2: Save to Memory

Save a **project memory** file summarizing this session's outcomes. Only save things that are:
- Non-obvious decisions or context that future sessions need
- NOT derivable from git log or reading the code

If nothing qualifies, skip this step. Don't save for the sake of saving.

Format for memory file:
```markdown
---
name: Session handoff YYYY-MM-DD — [topic]
description: [one-line summary of what was accomplished and what's next]
type: project
---

[Key decisions, context, or state that the next session needs to know]

**Next steps:** [what should happen next]
```

### Step 3: Update Project Docs (if needed)

Check if any of the following need updating based on this session's work:
- `CLAUDE.md` — new build commands, changed conventions, new project structure
- `.claude/rules/` — new rules learned or existing rules that need updating

Only update if there's a real change. Don't touch docs just to touch them.

### Step 4: Plan Next Session

Based on what's unfinished and any `$ARGUMENTS` provided, create a clear plan:

**Priority items for next session:**
1. [Most important task — with specific file paths and context]
2. [Second task]
3. [etc.]

**Context the next session needs:**
- Current branch and its state
- Any in-progress work that needs to be continued
- External dependencies or blockers

### Step 5: Write Starter Prompt

Write a **copy-pasteable prompt** that the user can use to start the next session. This prompt should:
- Give the new session full context without reading the entire previous conversation
- Reference specific files, branches, and tasks
- Be actionable — the new session should be able to start working immediately

Format the prompt inside a fenced code block so the user can easily copy it:

~~~
```
[The starter prompt goes here — ready to paste into a new Claude Code session]
```
~~~

### Step 6: Final Checklist

Before finishing, verify:
- [ ] All work is committed (run `git status`)
- [ ] All work is pushed to remote (run `git log --oneline origin/main..HEAD`)
- [ ] No sensitive data left in uncommitted files
- [ ] Memory saved (if applicable)
- [ ] Starter prompt written

Report the checklist results to the user.

## Notes

- If the user provides `$ARGUMENTS`, treat it as guidance for what the next session should focus on
- Keep the starter prompt under 500 words — concise but complete
- The starter prompt should work even if the next session uses a different model or has no memory access
- Always include the current git branch and last commit SHA in the handoff
