---
name: work-board-item
description: Work an item from the server-control-panel GitHub Project board end to end — pick the next Ready issue (or a given issue number), move its card through In progress → In review, branch, implement, and open a PR that closes it. Also use to add an issue to the board or move a card to another Status column.
argument-hint: "[next | issue number | add <issue> | move <issue> <status>]"
---

# Work a Board Item

Drives cards on the GitHub Project board for this repo with the `gh` CLI. Reading the board in more detail (sprints, filters) is the `get-board-items` skill; creating issues is `create-issues`; editing issue fields is `update-issues`.

## Board constants

| Thing | Value |
| --- | --- |
| Owner / repo | `daniel-vuky` / `daniel-vuky/server-control-panel` |
| Project number | `2` |
| Project ID | `PVT_kwHOBMvx-c4BpIkp` |
| Status field ID | `PVTSSF_lAHOBMvx-c4BpIkpzhnliOs` |

Status option IDs:

| Status | Option ID | Meaning |
| --- | --- | --- |
| Backlog | `b3d9e600` | Idea, not specified yet. Do not pick up. |
| Ready | `b4e4d3e7` | Has goal + acceptance criteria. Claude may pick up. |
| In progress | `91c88127` | Branch exists, being worked on. |
| In review | `0c9c2df7` | PR open, waiting for human review. |
| Done | `2b023eb0` | Merged / closed. |

If a command fails with an unknown ID, the board was reconfigured: re-read with `gh project field-list 2 --owner daniel-vuky --format json` and update this table.

Requires a `gh` token with the `project` scope (`gh auth status` shows it; fix with `gh auth refresh -s project` as the `daniel-vuky` account).

## Helpers

Get (or create) the board item ID for an issue — `item-add` is idempotent and returns the existing item if the issue is already on the board:

```bash
gh project item-add 2 --owner daniel-vuky \
  --url https://github.com/daniel-vuky/server-control-panel/issues/<N> \
  --format json --jq .id
```

Move a card (`<OPTION_ID>` from the table above):

```bash
gh project item-edit --project-id PVT_kwHOBMvx-c4BpIkp \
  --field-id PVTSSF_lAHOBMvx-c4BpIkpzhnliOs \
  --id <ITEM_ID> --single-select-option-id <OPTION_ID>
```

List Ready items in board order:

```bash
gh project item-list 2 --owner daniel-vuky --format json --limit 200 \
  --jq '.items[] | select(.status == "Ready") | {number: .content.number, title: .content.title, item: .id}'
```

Board reads can lag a few seconds behind an `item-edit`; if a just-moved card is missing from a listing, wait briefly and list again before concluding anything.

## Modes

- `add <N>` — add issue N to the board, then move it to **Backlog** unless the user names a Status.
- `move <N> <status>` — get the item ID, move it, report the new Status.
- `next` (default) or `<N>` — the full workflow below.

## Full workflow

1. **Pick the issue.** With a number, use it. With `next`, take the first Ready item. If nothing is Ready, say so and stop; do not pull from Backlog without the user's OK.
2. **Read it.** `gh issue view <N> --comments`. If it has no clear goal or acceptance criteria, stop and ask the user (or offer to fill them in via `update-issues`) instead of guessing.
3. **Claim it.** Move the card to **In progress**. Create a branch from up-to-date `main`: `feat/<N>-<short-slug>` for features, `fix/<N>-<short-slug>` for bugs.
4. **Implement.** Follow `CLAUDE.md` and the relevant skills. Keep to the issue's scope; note anything out of scope as a follow-up instead of doing it.
5. **Verify** against every acceptance-criteria checkbox (tests, build, running the app). Do not claim done without evidence.
6. **Open the PR.** Commit, push, then `gh pr create` with a body that starts with `Closes #<N>` and maps each acceptance criterion to how it was verified. Ask the user before pushing if they have not already approved it in this session.
7. **Hand off.** Move the card to **In review** and report the PR link. Never merge the PR yourself — merging (which auto-closes the issue) is the user's call.

If work stops partway (blocked, user redirects), leave the card in **In progress** and add an issue comment summarizing where it stands and what is blocking.
