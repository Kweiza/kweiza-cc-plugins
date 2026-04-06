---
name: harness-add
description: "Add a stack preset to an existing Kweiza Harness project"
user-invocable: true
argument-hint: "<preset name, e.g.: nextjs, fastapi, docker>"
---

# Kweiza Harness — Add Preset

Add a stack preset to an existing project. Merges the preset's CLAUDE.md section, hooks, and rules into the current project configuration.

## Available Presets

`nextjs`, `vite`, `fastapi`, `express`, `figma-plugin`, `ai-agent`, `comfyui`, `fullstack-platform`, `docker`

## Instructions

If `$ARGUMENTS` is provided, run:

```bash
npx @kweiza/harness add $ARGUMENTS
```

If `$ARGUMENTS` is empty, ask the user which preset they want to add. Show them the available presets list above and then run the command with their choice.

Wait for the command to complete, then report what was added:
- CLAUDE.md section
- Hooks added to settings.json
- Rules file added
- Any scaffold files copied

## Notes

- This merges into existing config — it won't overwrite what's already there
- Hooks are deduplicated — no duplicate commands
- If the preset is already applied, it will be skipped
- Scaffold files that already exist are not overwritten
