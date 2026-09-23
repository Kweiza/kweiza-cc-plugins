**English** | [한국어](README.ko.md)

# Claude Code Plugins

Personal Claude Code plugins marketplace.

> **Korean is the source of truth in this repository.** Documentation is edited in the `.ko.md` file
> first and the English follows; commits, judgments and the design document are all Korean, so that
> direction matches reality. GitHub does not pick a README by language — it renders only `README.md` —
> so the link above does that job, and the filenames follow the de-facto ISO 639-1 convention.
>
> Note that flightdeck's **runtime output is Korean** (board, prescriptions, refusals). Its English
> guide quotes that output verbatim and explains each quote, rather than translating it into something
> you will not see on screen.

## Install

```
/plugin marketplace add kweiza/kweiza-cc-plugins
/plugin install grafik-bar@kweiza-cc-plugins
/plugin install session-handoff@kweiza-cc-plugins
/plugin install flightdeck@kweiza-cc-plugins
```

## Plugins

### grafik-bar

Graphical status line: login, workspace folder, git branch, model, reasoning effort, context window, 5h/7d rate limits with reset countdowns, and session stats (cost, lines changed, elapsed time) — with a responsive layout that measures each assembled segment and wraps to the terminal's actual width, counting CJK and emoji as the two columns they occupy.

**No setup command.** Just install the plugin — a `SessionStart` hook points your `~/.claude/settings.json` `statusLine` at the plugin's own script and keeps it current. Because it references the installed plugin directly, every plugin update applies automatically. The hook is idempotent and only touches the `statusLine` key (all other settings are preserved). Requires `jq`.

> Updating to newer versions is handled by Claude Code's marketplace plugin updates; the hook always tracks whichever version is installed.

### session-handoff

Session handoff — save progress to a durable file under `.claude/handoffs/`, plan the next session, and write a starter prompt. The handoff survives `/clear` and context resets because it lives in a file, not just the chat. Resume it in the next session with `/session-resume`.

| Skill | Description |
|-------|-------------|
| `/session-handoff` | Wrap up session, save context to a handoff file + memory, write next-session prompt |
| `/session-resume` | Reload a saved handoff to continue prior work — `list`, or pass a date/keyword to pick which one (default: most recent) |

### flightdeck

Coordination layer for parallel agent sessions (Claude Code and codex). One self-hosted server (Docker);
many sessions, across machines and repos, register with it, and who is alive, which paths they touch,
what they claimed and what has landed are all **derived from git and the database**. Its surface is a
queue with claims instead of locks, path overlap visible before the merge, a landing lane, and judgments
that travel with each item.

**Since 2026-09-24 it is developed in its own repository, [Kweiza/flightdeck](https://github.com/Kweiza/flightdeck).**
This marketplace's `flightdeck` entry points there, so `/plugin install flightdeck@kweiza-cc-plugins` above
keeps working and `/plugin update` fetches releases from that repository. The guide and the design of
record live there; history up to 0.38.3 stays in this repository.
