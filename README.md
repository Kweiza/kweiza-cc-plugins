# Claude Code Plugins

Personal Claude Code plugins marketplace.

## Install

```
/plugin marketplace add kweiza/kweiza-cc-plugins
/plugin install grafik-bar@kweiza-cc-plugins
/plugin install session-handoff@kweiza-cc-plugins
```

## Plugins

### grafik-bar

Graphical status line: login, workspace folder, git branch, model, reasoning effort, context window, 5h/7d rate limits with reset countdowns, and session stats (cost, lines changed, elapsed time) — with a responsive layout that reflows for wide/medium/narrow terminals.

**No setup command.** Just install the plugin — a `SessionStart` hook points your `~/.claude/settings.json` `statusLine` at the plugin's own script and keeps it current. Because it references the installed plugin directly, every plugin update applies automatically. The hook is idempotent and only touches the `statusLine` key (all other settings are preserved). Requires `jq`.

> Updating to newer versions is handled by Claude Code's marketplace plugin updates; the hook always tracks whichever version is installed.

### session-handoff

End-of-session handoff — save progress, plan next session, write starter prompt.

| Skill | Description |
|-------|-------------|
| `/session-handoff` | Wrap up session, save context, write next-session prompt |
