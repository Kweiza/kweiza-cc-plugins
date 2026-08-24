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

Coordination layer for parallel Claude Code sessions. One self-hosted server (Docker); many sessions, across machines and repos, register with it.

Run ten sessions on one product and they have no way to talk to each other, so each one *guesses* what the others picked up. When the guess is wrong, a session takes over work another session is already doing. flightdeck removes the guess — who is alive, which paths they touch, what they claimed, what has landed are all **derived from git and the database**, never hand-copied.

- **A queue with claims, not locks.** `pick` claims an item; the item id becomes the branch name and the worktree path. Nothing is held exclusively except the one thing that must be.
- **Path overlap, before the merge.** A `PostToolUse` hook reports uncommitted footprints, so "we're both editing that file" surfaces while it is still cheap.
- **A landing lane.** A serialized queue in front of the merge — `fd land` exits non-zero unless it is your turn, so `fd land && <your merge command>` is a correct one-liner.
- **Judgments.** The one asset that cannot be derived: why you did it, what you rejected, what you deliberately did not do. They travel with the item to whoever picks it up next.
- **A read-only board** at `localhost:7420`, plus a `SessionStart` hook that injects the same board into every new session — including a banner when the server is unreachable, so no agent assumes coordination exists when it doesn't.

| Skill | Description |
|-------|-------------|
| `/fd-setup` | Set up this machine — measure state, decide server vs. client, install and start only what's missing |
| `/fd-pickup` | Start a session: board → recommendation → claim → read the judgments linked to that item |
| `/fd-handoff` | Wrap up: judgment + followups + close item + release resources, in one call and one transaction |
| `/fd-update` | Bring server, plugin, and DB up to date |

Eight MCP tools (`board` `pick` `note` `add` `finish` `alloc` `land` `label`) and a `fd` CLI expose the same operations to the agent and to you. Requires Docker (or Go) for the server.

Guide: [`plugins/flightdeck/README.md`](plugins/flightdeck/README.md) · design of record: [`plugins/flightdeck/DESIGN.md`](plugins/flightdeck/DESIGN.md)
