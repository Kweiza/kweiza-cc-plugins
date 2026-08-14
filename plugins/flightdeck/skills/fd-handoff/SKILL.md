---
name: fd-handoff
description: Wrap up work and hand it off. Judgment saved + followups registered + item closed + resources released, in one call. "핸드오프" · "마무리" · "세션 넘긴다" · use when the work is done.
---

# Wrapping up

The tool is `finish`, alone. **The four are one transaction**, so there is no order to keep —
you cannot end up with a judgment but an open item, or a closed item whose followups never landed.

## Do it in one call

```
finish(
  item_id: "<the item you claimed>",
  outcome: "done",          // or "dropped" — then close_reason is required
  title:   "<one line>",
  body:    "<the four below>",
  followups: [ { id, title, body, paths } ]   // new followups. **An item that already exists (only open ones I made after this claim): id only** — it links
)
```

Call it without `body` and it tells you **right there** what to write.

## The four for `body`

1. **Why you did it that way**
2. **What you rejected**
3. **What you left undone on purpose** — pushed out of scope · left as is because it was right
4. **What you checked but could not do** — investigations that came back "no problem" belong here too

Do not write what git log and diff already know (what you changed).
What goes here is **what is left nowhere else** — the reasons behind the judgment.

## Followups go in the same call

Passed as `followups`, judgment and followup are joined by a judgment link. Added separately later, that link is missing.
The next session's `pick` serves that judgment along with the item.

## Close the session last

`fd finish <item> --body … --close` — item and session together. To close without an item, `fd close`.
Left open it still does not sit on board ① (only cards holding a claim show). But overlap detection catches it all window.
**It refuses while a claim is still held** — a closed card's claim is invisible to everyone.
Reversible: the next prompt or tool call brings the card back. Closing is an observation, not a verdict.

## What to leave along the way

- `note(kind: "ask")` — what others must not touch. **The only channel carrying intent before a commit**
- `note(kind: "blocked")` — blocked. The reason is the body
- `note(kind: "decision")` — decisions expensive to undo, and the grounds

`note` reports **how many sessions will receive it**. 0 means nobody is watching right now.

## What not to do

- Hand-editing the dashboard — the screen is a view of the DB
- Writing landing sha or verification-passed lines — they are derived from job records
