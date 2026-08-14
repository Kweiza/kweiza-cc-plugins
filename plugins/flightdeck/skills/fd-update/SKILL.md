---
name: fd-update
description: Bring the server, plugin, and DB up to date. "갱신" · "업데이트" · "최신인지 확인" · "새 버전 나왔다" · use when the board is stale or cannot read branches.
---

# Update

Staleness has three axes — **plugin · server · DB**. They go stale separately; fix one and the rest stay as they were.

## 1. Measure what is stale

```
fd status | head -1
fd doctor
claude plugin list
```

**The first line of `fd status` is the verdict.** It must read `파생 git@…` for the server to be reading git.
If it reads `파생 db@…` or `브랜치 ?(못 읽음)`, **that server's overlaps, branches, and footprints are all
untrustworthy** — it is a target to update, not a source to consult.

**Do not use `ok=true` from `/healthz` as a freshness verdict.** A server months old and a server that cannot
read git both return `ok` alike. It says only "it is up".

## 2. There is an order

**Push → marketplace → plugin → container.** The marketplace is a git remote, so if you bump the version in
`plugin.json` and **do not push, the update never arrives at all.** Skip the order and you reinstall the old
build and believe it is current.

## 3. Ask the new build before you change anything

```
fd selfcheck --db ~/.flightdeck/fd.db
```

It answers only **may this be restarted** — does that binary run, is the DB migration plan not a refusal.
**Ask this first when rolling back too.** If the DB version is higher than that build knows, a refusal comes back.
(If the build is old enough to print `모르는 명령: selfcheck`, that build's `serve` stops at startup for the same reason.)

## 4. Update

```
claude plugin marketplace update <marketplace>
claude plugin update flightdeck@<marketplace>

cd ~/.claude/plugins/cache/<marketplace>/flightdeck/<new version>
FD_TOKEN="$(cat ~/.flightdeck/token)" FD_UID=$(id -u) FD_GID=$(id -g) docker compose up -d --build
```

**Bring it up from the installed cache directory.** Bring it up from the dev repo and the installed build and
the running build diverge. The server applies DB migrations itself on open, and backs up before applying —
there is nothing separate to do.

## 5. Look at the evidence that it updated

Check **both** that `fd status | head -1` prints `파생 git@` with a just-now timestamp, and that
`claude plugin list` prints the new version. Look at only one and you mistake a half-update for current.

## 6. Always pass on what the human must do

> **Claude Code has to be restarted — not one window, but every open session.**
> An MCP server reads its config once at startup and that is that, so a window you did not relaunch keeps
> using the old token and old tools. Until then use the CLI instead of MCP
> (`fd pick` · `fd note` · `fd finish` work right now).

Leave this out and the user hits "I updated it and it does not work". The server is already the new build
while only the screen is the old one, so the cause is invisible.

## Not doing

Working around it with a manual build instead of bumping the version (that server keeps running stale) ·
calling it current from `healthz` alone · omitting the restart notice · **deleting an old plugin cache that
other sessions are referencing** (their hooks and MCP break in place).
