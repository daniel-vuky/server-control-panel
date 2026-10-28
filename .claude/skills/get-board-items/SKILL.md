---
name: get-board-items
description: Fetch items from a GitHub project board. Use when asked about sprint items, backlog status, upcoming work, issue assignments, or to list available sprints. Supports filtering by sprint (current, next, previous, specific) and status.
argument-hint: "[sprint filter, e.g. next, current, Sprint 5 - 2026Q1]"
---

# Get Board Items from GitHub Project Board

Retrieves issues from a GitHub organization or user project board via MCP tools.

## Setup

1. Load tools with `tool_search_tool_regex` pattern `mcp_gh-projects`.
2. If MCP tools are available, prefer them. If **no tools are returned**, fall back to `gh project` CLI commands (verify with `gh project --help`).
3. If neither is available, ask the user to start the `gh-projects` MCP server or install the [GitHub CLI](https://cli.github.com/) with the `gh-projects` extension.

## Target project

Determine the target project from (in priority order):

1. Explicit user specification (e.g. "get items from project #42 in my-org")
2. Infer the organization from the workspace's Git remote origin, then discover projects (see below)
3. Ask the user for the organization/user, owner type (`org` or `user`), and project number

### Discovering projects

If the project number is unknown, list available projects:

- **MCP:** call `mcp_gh-projects_projects_list` with `method: list_projects`, `owner`, and `owner_type`. Use the `query` parameter to filter by title (e.g. `query: "my-project"`).
- **CLI:** run `gh project list --owner {org-or-user}`

The response contains an array of projects. Each project has a `number` (this is the `project_number` used in all subsequent calls) and a `title`. Present relevant results to the user with the `askUserQuestion` tool, e.g.:

```example
#12 — Sprint Board
#45 — Roadmap
#78 — Feature Requests
```

(These are placeholder examples — use the actual project titles and numbers from the API response.)

Once the user picks a project, use its `number` as `project_number` for all further calls.

All calls to `mcp_gh-projects_projects_list` require these parameters:

| Parameter        | Value                     |
| ---------------- | ------------------------- |
| `owner`          | Organization or user name |
| `owner_type`     | `org` or `user`           |
| `project_number` | The project number        |

## Fetching sprint issues

1. Call with `method: list_project_items`, `per_page: 100`, and a `query` filter (see below).
2. Optionally filter results by repository if the user specifies one: `item.content.repository.full_name === "{owner}/{repo}"`.
3. Extract the `Status` field from `item.fields`: find the field with `name === "Status"`. Because Status is a `single_select` field, the value is a nested object — read `value.name.raw` (not `value.raw`).
4. Present as a table: issue number (linked), title, status, assignees, labels.

### Discovering field IDs

If you don't know the field IDs for the project, call with `method: list_project_fields` first and inspect the returned fields for their IDs, names, and types. Use the discovered IDs in subsequent queries.

Common field types and value paths:

| Field type      | Value path        |
| --------------- | ----------------- |
| `single_select` | `value.name.raw`  |
| `iteration`     | `value.title.raw` |
| `text`          | `value.raw`       |
| `date`          | `value.raw`       |

### Query filter syntax

| Goal            | `query` value                          |
| --------------- | -------------------------------------- |
| Next sprint     | `sprint:@next`                         |
| Current sprint  | `sprint:@current`                      |
| Previous sprint | `sprint:@previous`                     |
| Specific sprint | `sprint:"Sprint 4 - 2026Q1"`           |
| By status       | `status:"In progress"`                 |
| Combined        | `sprint:@current status:"In progress"` |

## Listing available sprints

Call with `method: list_project_fields`. In the response, find the field with `name: "Sprint"` (or the iteration field name used by the project). Its `configuration` contains:

- `iterations` — active/upcoming sprints
- `completed_iterations` — past sprints

Each iteration has `title` (object with `raw`/`html` keys — use `raw`), `start_date`, and `duration`. Use `start_date` to resolve current/next sprint relative to today.

## Example output

| #   | Issue                                                  | Title               | Status      | Assignees | Labels   |
| --- | ------------------------------------------------------ | ------------------- | ----------- | --------- | -------- |
| 1   | [#1234](https://github.com/{owner}/{repo}/issues/1234) | Example issue title | In progress | username  | QA, a11y |
