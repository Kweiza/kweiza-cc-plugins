---
name: harness-init
description: "Initialize a new project with Kweiza Harness standards and stack presets"
user-invocable: true
argument-hint: "[optional: project path, default: prompts interactively]"
---

# Kweiza Harness — Init

Set up a new project with company standards, stack-specific presets, and Claude Code harness configuration.

## What It Does

1. Prompts for project name, stack presets (Next.js, FastAPI, Docker, etc.), and git init
2. Generates `CLAUDE.md` with build/run commands merged from base + selected presets
3. Creates `.claude/settings.json` with pre-commit hooks (lint, format, typecheck)
4. Copies `.claude/rules/` with company standards + stack-specific rules (path-scoped)
5. Copies `.gitignore` and scaffold files
6. Optionally configures Kweiza plugins (grafik-bar status line)
7. Initializes git with initial commit

## Available Presets

| Preset | Stack |
|--------|-------|
| `nextjs` | Next.js (App Router, TypeScript) |
| `vite` | Vite + React (TypeScript) |
| `fastapi` | FastAPI (Python, uv) |
| `express` | Express (TypeScript, Prisma) |
| `figma-plugin` | Figma Plugin (TypeScript) |
| `ai-agent` | Background AI Agent (Python) |
| `comfyui` | ComfyUI Custom Nodes (Python) |
| `fullstack-platform` | Node-based AI Platform (TS + Python) |
| `docker` | Docker containerization |

## Instructions

Run the following command in the terminal:

```bash
npx @kweiza/harness init $ARGUMENTS
```

If `$ARGUMENTS` is empty, it will prompt interactively. If a path is provided (e.g., `my-app`), it uses that as the project directory.

Wait for the command to complete, then report the results to the user.

## After Init

Remind the user:
- Open the generated `CLAUDE.md` to verify the content matches their project
- Check `.claude/rules/` for the applied rules
- Pre-commit hooks are active — lint/format/typecheck will run on every commit
- Use `/harness-add` to add more presets later
- Use `/harness-update` to sync with latest standards
