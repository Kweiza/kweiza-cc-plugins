---
name: harness-update
description: "Sync current project with latest Kweiza Harness standards"
user-invocable: true
---

# Kweiza Harness — Update

Regenerate CLAUDE.md, settings.json, and rules from the latest harness standards. Only updates harness-managed files — never touches scaffold files or your custom code.

## Instructions

Run the following command in the project root:

```bash
npx @kweiza/harness update
```

This is an interactive command that will:
1. Show current harness version and applied presets
2. Ask for confirmation before regenerating
3. Regenerate CLAUDE.md, .claude/settings.json, and .claude/rules/

Wait for the command to complete, then report what was updated.

## When to Use

- After the harness package has been updated (`npm update @kweiza/harness`)
- When company standards have changed and you want to pull the latest rules
- When you want to reset harness files to their canonical state
