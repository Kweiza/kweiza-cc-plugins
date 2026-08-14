---
name: fd-pickup
description: Start a session and claim one item from the queue. Order is board → recommend → claim → read the linked judgments. "작업 가져와" · "세션 시작" · "뭐 할까" · use when starting new work.
---

# Picking Up Work

Two tools, board and pick; the order is the whole thing.

## 1. Look at the others first

```
board
```

You get **claimed work** (only cards holding a claim) · activity badges · last signal and its age · paths · unacknowledged items.
There is no "dead" marker. Only age — the call is a human's.

## 2. Choose what to pick

```
pick
```

With no arguments it **only recommends**: the top pick + **why**, **why each member was bundled**, **every rejection reason**.
Nothing to bundle reads "단독" (solo) — if that is missing, the server did not emit this axis.

## 3. Pick it

```
pick(item_ids: ["<lead>", "<rest>", …])
```

**The first is the lead** — its id becomes the branch and worktree name. The rest ride in the same worktree.
Lead fails, all are refused; lead succeeds, the rest go as far as they can + **why each missed one failed**. Single: `pick(item_id: "<id>")`.
A claim returns the body · **the full linked judgments** (below) · the worktree setup commands.

If it turns out you cannot do it, put it down with `pick(leave: "why not")` (add `item_id` to leave only that one).
The item returns **alive as `open`**, so its id, history, and `after` survive — **do not paper over it with `finish(dropped)`.**
That closes it: the id changes, the history is severed, and zero work enters the queue balance as a completion.

A re-claim request is **a re-print of the context**, not a refusal (the return path after context loss).
Carry the `큐 열림 N건` (queue open N) line **verbatim** — carry it even when it says it is absent ("이 응답에 없다"), and if it
is missing entirely (steal refused · offline · old fd) do not invent a number.

## 4. Read the linked judgments before you plan

The judgment section **is no substitute for the body** — the body is "what", the judgments are **"why it was done that way"**:
what was considered and why it was not done, which evidence turned out false, where something was **left unfixed on purpose**.
Skip them and you redo the investigation, or **read a deliberately left seam as a defect and go fix it.**

## 5. Coordinate when an overlap comes back

`pick` **does not filter path overlaps; it reports them.** If a session overlaps, announce what you will touch with
`note(kind: "ask")` before you start.

## What this does not do

Register the session (a hook does it — there is no `session` argument) · write branch, HEAD, or sha (the server reads them from git) · take a lock (there are none) · choose the bundle (the server decides; `item_ids` is for overriding only) · steal someone's claim (`steal_reason` is refused — that is a human's surface).
