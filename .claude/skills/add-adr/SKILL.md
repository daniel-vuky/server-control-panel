---
name: add-adr
description: Create a new Architecture Decision Record (ADR) in the project's ADR directory. Interviews the user to understand the decision, proposes the full content for review, then creates the file via adr-tools CLI or directly from template.
argument-hint: "[brief description of the architectural decision to record]"
---

# Add ADR

Creates a new Architecture Decision Record by interviewing the user, proposing content for confirmation, then creating the file.

**ADR directory**: read from `.adr-dir` in the workspace root (defaults to `docs/decisions`).

---

## Workflow

### Step 1: Research the topic

Before asking anything, search the codebase to build context around the decision. Use the user's prompt as a starting point — extract component names, concepts, or patterns mentioned and search for relevant files, existing implementations, and related ADRs.

Look for:

- Components or modules directly involved in the decision
- Existing patterns or conventions that would be affected
- Any previous ADRs in the ADR directory that relate to the topic

Use this research to:

- Pre-fill answers you can confidently infer (so you don't ask the user for things already evident in the code)
- Make your questions more specific and informed

### Step 2: Understand the decision

Based on your research and the user's prompt, identify what's still missing. Batch all unanswered questions into a single question tool call — skip any already answered.

The questions to cover:

**Context** _(free text)_

> "What problem or situation is driving this decision?"

**Options considered** _(free text, skippable)_

> "Were multiple options considered? If so, briefly describe each with its pros and cons. (skip if none)"

**Decision** _(free text)_

> "What was decided, and why?"

**Consequences** _(free text)_

> "What becomes easier because of this? What becomes harder or what risks are introduced?"

**Related links** _(free text, skippable)_

> "Are there any related PRs, issues, or other ADRs to link? (skip if none)"

**Status** _(multiple choice, single-select)_ Options: `Accepted` (recommended), `Proposed`, `Deprecated`, `Superseded`

If the user's prompt combined with your research already covers everything, skip directly to Step 4.

---

### Writing guidance (apply when composing the draft)

- **Options in Context**: if options were provided in Q2, render them as `### Option A` / `### Option B` sub-sections inside the Context section, each with a brief pros/cons list — not as a separate top-level section.
- **Structured decisions**: if the decision has multiple rules or steps, use a numbered list or sub-headings inside the Decision section rather than a single prose paragraph.
- **Consequences**: split into **Easier:** and **Harder:** bullet groups when both apply.
- **Links**: weave related PR/issue/ADR links naturally into the relevant section (Context or Consequences) using Markdown links.

### Step 4: Derive the title

From the gathered information, produce a short imperative title (e.g. `Use React for UI components`, `Adopt ADRs for architecture decisions`). Confirm with the user if unsure.

### Step 5: Propose ADR content — do not create yet

Present the full draft to the user for review:

---

> **Title:** {title} **Status:** Proposed | Accepted
>
> ---
>
> ```markdown
> # {N}. {title}
>
> Date: {today's date YYYY-MM-DD}
>
> ## Status
>
> {Proposed | Accepted}
>
> ## Context
>
> {context gathered in Step 1}
>
> ## Decision
>
> {decision gathered in Step 1}
>
> ## Consequences
>
> {consequences gathered in Step 1}
> ```

---

Ask: _"Does this look right? Should I create this ADR?"_

**Do not create anything until the user confirms.**

### Step 6: Create the ADR file

1. List the ADR directory to find existing files and determine the next sequence number.
2. Slugify the title: lowercase, words separated by `-`, remove special characters.
3. Create `{NNNN}-{slug}.md` in the ADR directory with the confirmed content.

### Step 7: Confirm

Output: _"ADR created: [{adr-directory}/{filename}]({adr-directory}/{filename})"_
