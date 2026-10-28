# server-control-panel

## Task board

Work is tracked on the GitHub Project board: https://github.com/users/daniel-vuky/projects/2 (project `2`, owner `daniel-vuky`).

Columns (the `Status` field): **Backlog → Ready → In progress → In review → Done**.

- Only pick up issues in **Ready**. Backlog items are not specified enough yet.
- One issue = one branch = one PR. Branches: `feat/<issue>-<slug>` or `fix/<issue>-<slug>`.
- PR bodies start with `Closes #<issue>` so merging closes the issue.
- Claude moves cards to In progress and In review; the user reviews and merges. Never merge PRs yourself.

Skills for the board:

- `work-board-item` — pick the next Ready issue and work it end to end, add an issue to the board, or move a card. Holds the project/field/option IDs.
- `get-board-items` — read and filter board items.
- `create-issues` — file new issues using `.github/ISSUE_TEMPLATE/` (feature, bug). After creating one, add it to the board with `work-board-item add <N>`.
- `update-issues` — edit issue fields or comment.

`gh` must be logged in as `daniel-vuky` with the `project` scope (`gh auth status`).
