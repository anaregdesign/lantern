---
description: 'Ambient memory policy for agents connected to the Lantern MCP server — recall before answering, capture durable facts after, with TTL-bucketed, namespaced keys.'
applyTo: '**'
---

> **Legacy profile only (#851).** These instructions drive the
> `remember_*` / `recall_*` decaying-memory verbs, which are served only
> when the MCP server runs with `LANTERN_MCP_PROFILE=memory`. The default
> `context` profile (multi-agent shared working context) carries its own
> session-open instructions and does not use this file.

# Ambient memory (Lantern MCP)

> **What this is.** A ready-to-use instruction profile that turns the Lantern
> MCP server's built-in nudge into an always-on capture+recall policy on the
> *client* side — because the host agent, not the server, ultimately decides
> when to call tools.
>
> **How to install it** is in [`README.md`](README.md#make-the-agent-use-lantern-automatically).
> In short: VS Code Copilot picks this file up automatically when it lives in
> `.github/instructions/`; for Claude Desktop or any other host, copy
> everything below the frontmatter into your system / project instructions.

You are connected to **Lantern**, an ambient graph memory exposed over MCP. It
is yours to use **proactively — you do not need to be asked** to remember or
recall. Treat it like your own long-term memory of this user and their work.

## Run this loop every turn

1. **Recall first.** Before answering anything that could depend on prior
   context — preferences, decisions, people, project facts — pull in what you
   already know:
   - `recall_fact` — look up an exact key you expect to exist.
   - `recall_related` — walk outward from a seed key to gather surrounding
     context and associations.
   - `list_under` — enumerate a namespace (e.g. `user.`, `project.`) when you
     are unsure of the exact key.
2. **Answer** using what you recalled. Prefer a remembered fact over
   re-asking the user something you were already told.
3. **Capture after.** Once the exchange settles, write what is newly durable:
   - `remember_fact` — a preference, decision, identity, or project fact.
   - `remember_relation` — how two things connect (additive: writing the same
     relation again strengthens it).

   Capturing is the **default, not the exception**.

## Two invariants govern every write

- **There is no "forever."** Each write picks a TTL bucket from
  `seconds`, `transient`, `turn`, `conversation`, `task`, `workday`, `day`,
  `week`, `sprint`, `month`, `quarter`, `durable`. Ask *"when will this stop
  being true?"* and *"how bad is it if it lingers past then?"* and pick the
  **shorter** bucket — writing again is cheap.
- **Recall does not refresh TTL.** Reading a fact does not extend its life. To
  keep a fact alive, **re-remember it when you reference it**.

## Namespace keys so the graph reads as a mind map

Use dotted, hierarchical keys. Reuse a key to update its value.

- `user.*` — stable facts about the person
  (`user.preferences.tone`, `user.identity.role`, `user.timezone`).
- `project.*` — the work at hand
  (`project.lantern.stack`, `project.lantern.deadline`).
- `session.*` — ephemeral working state
  (`session.current-task`, `session.open-question`).

Only call `forget` when a fact is now **wrong** or the user asks you to drop it
— routine staleness is handled by TTL decay, so you rarely need it.

## Suggested TTL defaults

A starting point; shorter is always safer, and the server's bucket durations
are configurable.

| Kind of fact | Bucket |
|---|---|
| Name, role, timezone, long-standing preferences | `durable` / `quarter` |
| Current project's stack, conventions, owners | `month` / `sprint` |
| What we're doing right now | `task` / `conversation` |
| A one-off detail for the next few replies | `turn` / `transient` |

## Worked example

> **User:** "I'm Hiro, I lead the Lantern project and I prefer terse answers."

1. `recall_related` from `user.identity` and `list_under` `user.` — nothing yet.
2. Answer briefly (honoring the stated preference).
3. Capture:
   - `remember_fact` `user.identity.name` = `Hiro` — bucket `durable`.
   - `remember_fact` `user.identity.role` = `lead, Lantern project` — `quarter`.
   - `remember_fact` `user.preferences.tone` = `terse` — `durable`.
   - `remember_relation` `user.identity.name` → `project.lantern` — `quarter`.

Next session, a `list_under` `user.` surfaces all of it before you answer — so
you stay terse and never re-ask who they are.
