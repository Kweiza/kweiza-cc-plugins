---
name: fd-setup
description: Set up this machine. Measure the state, decide server vs client, then install and start only what is missing. "셋업" · "설치" · "처음 켰다" · "서버가 안 뜬다" · use on a machine where the plugin was just switched on.
---

# Setup

`fd setup` makes the call. This skill only **asks · gets approval · runs · confirms**.

## 1. Measure first

```
fd setup
```

It gives the role (server/client) · the address and **where it came from** · reachability ·
what is missing · what to do. Do not invent — what is not printed here, this tool does not know.

## 2. If it is already done, ask and stop

`할 일 없음` ("nothing to do") means setup is in place. **Ask only whether to change the
configuration; otherwise do nothing.** Overwriting a working config is this skill's most
common accident.

## 3. Otherwise, ask for the role

- **Server** — this machine runs the coordination server. Other sessions attach here.
- **Client** — attach to a server already running. **Ask for the address too**
  (`http://<host>:7420`).

If `fd setup` printed `Windows 는 지원하지 않는다` (Windows is unsupported), **stop there.**
Point to WSL and nothing more; never talk as if setup succeeded — neither hooks nor MCP come up.

## 4. Show the commands, get approval, then run

Put the plan's commands **on screen verbatim**, get approval, then run them. For anything
marked `[관리자 권한]` (admin rights), say so as well. The user must never be walked through
what goes onto their own machine, and why, without knowing it.

Do not invent commands the plan does not have. Not knowing the distro is no reason to take
shots at package managers.

## 5. Save and confirm

```
fd setup --url <url> [--token <token>]
fd doctor
```

After saving, you must relay **"Claude Code 를 다시 시작해야 훅·MCP 가 이 값을 본다"**
(restart Claude Code so hooks and MCP see this value). The MCP server reads the environment
once at startup and that is all — without this sentence, the user hits "I set it up and it
does not work".

## What it does not do

Installing without approval · commands not in the plan · pretending success on Windows ·
writing config files directly (`fd setup` owns the format) · starting another server when
one is already running.
