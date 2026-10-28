---
name: update-issues
description: Update existing GitHub issues or add comments in any repository. Use when the user wants to modify an issue's title, body, state, labels, assignees, or add a comment to an existing issue.
argument-hint: "[issue number or search query]"
---

# Update Issues

Updates existing issues and adds comments via MCP tools.

## Setup

1. Load tools with `tool_search_tool_regex` pattern `mcp_github_(issue_write|issue_read|add_issue_comment|search_issues)`.
2. If MCP tools are available, prefer them. If **no tools are returned**, fall back to the `gh` CLI (verify with `gh --version`).
3. If neither is available, ask the user to start the GitHub MCP server or install the [GitHub CLI](https://cli.github.com/).

## Target repository

Determine the target repository from (in priority order):

1. Explicit user specification (e.g. "update issue #42 in org/repo")
2. The current workspace's Git remote origin
3. Ask the user if neither is available

## Workflow — updating an issue

### Step 1: Identify the issue

If the user provides an issue number, use it directly. Otherwise, call `mcp_github_search_issues` with relevant keywords scoped to `repo:{owner}/{repo}` and present matching issues as a table for the user to pick from:

| #    | Issue                                                  | Title         | State | Labels      | Updated    |
| ---- | ------------------------------------------------------ | ------------- | ----- | ----------- | ---------- |
| 5678 | [#5678](https://github.com/{owner}/{repo}/issues/5678) | Example issue | open  | bug, triage | 2026-02-15 |

### Step 2: Read the current issue

Call `mcp_github_issue_read` with `method: get` and the `issue_number` to fetch the current state.

### Step 3: Determine changes

Ask or infer what the user wants to change: title, body, state, labels, assignees.

### Step 4: Present changes for review

**MANDATORY** — Show a before/after comparison of the fields being changed. Ask: _"Should I apply these changes?"_

Only proceed to Step 5 after the user explicitly confirms.

### Step 5: Apply the update

Call `mcp_github_issue_write` with:

| Parameter        | Value                             |
| ---------------- | --------------------------------- |
| `method`         | `update`                          |
| `owner`          | Target owner                      |
| `repo`           | Target repo                       |
| `issue_number`   | The issue number                  |
| (changed fields) | Only the fields that are modified |

After update, output: _"Issue updated: [#NUMBER](https://github.com/{owner}/{repo}/issues/NUMBER) — {title}"_

## Workflow — adding a comment

If the user wants to add a comment rather than update the issue fields:

### Step 1: Identify the issue

Same as above — use the provided issue number or search to find it.

### Step 2: Compose the comment

Draft the comment body in Markdown.

### Step 3: Present for review

**MANDATORY** — Show the comment to the user. Ask: _"Should I post this comment?"_

Only proceed after the user explicitly confirms.

### Step 4: Post the comment

Call `mcp_github_add_issue_comment` with `owner`, `repo`, `issue_number`, and `body`.

After posting, output: _"Comment added to [#NUMBER](https://github.com/{owner}/{repo}/issues/NUMBER)"_

## Important rules

- **Never apply an update or post a comment without explicit user confirmation.**
- When the user provides partial information, ask targeted follow-up questions to clarify which fields to change.
