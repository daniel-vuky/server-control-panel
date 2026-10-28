---
name: create-issues
description: Create GitHub issues in any repository. Use when the user wants to file a bug report or request a feature. Supports bug and feature issue types with structured templates. Offers to check for duplicates before creating.
argument-hint: "[bug | feature, short description of the issue]"
---

# Create Issues

Creates issues via MCP tools. Follows the repository's issue templates.

## Setup

1. Load tools with `tool_search_tool_regex` pattern `mcp_github_(issue_write|search_issues|list_issue_types)`.
2. If MCP tools are available, prefer them. If **no tools are returned**, fall back to the `gh` CLI (verify with `gh --version`).
3. If neither is available, ask the user to start the GitHub MCP server or install the [GitHub CLI](https://cli.github.com/).

## Target repository

Determine the target repository from (in priority order):

1. Explicit user specification (e.g. "create an issue in org/repo")
2. The current workspace's Git remote origin
3. Ask the user if neither is available

## Workflow

Follow these steps **in order**. Never skip the user review.

### Step 1: Determine issue type and read template

Check the `.github/ISSUE_TEMPLATE/` directory for available templates. List all `.yml` files to discover which issue types the repository supports (e.g. bug, feature, question, etc.).

If templates exist:

- Use the `askUserQuestion` tool to present the available types as options (or infer from context which one applies).
- Read the matching template file to get the current field definitions.
- Parse the YAML `body` array to determine which fields exist, their labels, whether they are required (`validations.required`), and any dropdown options. Also note the `title` hint and `type` value from the template frontmatter.

If no template files exist, fall back to asking the user for a title and description.

Use this parsed information to ask the user for only the required and relevant fields. **Do not hardcode template fields in this skill** — always read them from the YAML files so changes to the templates are automatically picked up.

### Step 2: Check for existing issues (user verification)

Before creating a new issue, **ask the user** whether they want to check for existing issues that may already cover this topic.

If the user agrees, call `mcp_github_search_issues` with:

- `query`: relevant keywords from the issue title/description, scoped with `repo:{owner}/{repo}`

Also try broader/alternative keyword variations to avoid missing duplicates.

**If potentially relevant issues are found:**

- Present them to the user as a table with columns: number (linked), title, state, labels, updated date.
- Ask the user whether any of these match their intent.
- If the user confirms a match, suggest using the **update-issues** skill instead to modify that existing issue.
- If the user says none match, proceed to Step 3.

**If no relevant issues are found** (or the user skipped the check), proceed to Step 3.

### Step 3: Compose the issue body

Build the issue body in Markdown using the structure from the template file read in Step 1.

For each field in the template's `body` array (excluding `type: markdown` entries), create a Markdown section:

```markdown
### {field label}

{user-provided value}
```

Maintain the same field order as defined in the template YAML. For dropdown fields, use the value the user selected. For optional fields the user left empty, use "N/A".

If no template was found, compose a simple body with the user-provided description.

### Step 4: Present for user review

**MANDATORY** — Before submitting, present the full issue to the user in Markdown for review:

---

> **Title:** {title} **Type:** Bug | Feature **Labels:** {labels, if any} **Assignees:** {assignees, if any}
>
> ---
>
> {full issue body in Markdown}

---

Ask: _"Does this look correct? Should I submit this issue?"_

Only proceed to Step 5 after the user explicitly confirms.

### Step 5: Submit the issue

Call `mcp_github_issue_write` with:

| Parameter   | Value                             |
| ----------- | --------------------------------- |
| `method`    | `create`                          |
| `owner`     | Target owner                      |
| `repo`      | Target repo                       |
| `title`     | The issue title                   |
| `body`      | The composed Markdown body        |
| `type`      | `Bug` or `Feature`                |
| `labels`    | Relevant labels (if any provided) |
| `assignees` | Assignees (if any provided)       |

After creation, output: _"Issue created: [#NUMBER](https://github.com/{owner}/{repo}/issues/NUMBER) — {title}"_

## Important rules

- **Always ask the user to verify existing issues before creating a new one.**
- **Never submit an issue without explicit user confirmation.**
- When the user provides partial information, ask targeted follow-up questions for any required fields that are missing.
- Use the `type` parameter (`Bug` or `Feature`) when creating issues if the repository has issue types configured — call `mcp_github_list_issue_types` to verify.
