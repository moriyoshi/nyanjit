---
name: good-sleep
description: "Read JOURNAL.md and reorganize its contents into semantically separate long-term memory documents under .agents/docs/LTM/. Use when you want to consolidate accumulated journal entries into reusable, topic-organized reference files."
user-invocable: true
allowed-tools: Bash, Read, Write, Edit, Grep, Glob
---

# Good Sleep: Reorganize JOURNAL.md into Long-Term Memory

This skill reads `.agents/docs/JOURNAL.md`, identifies semantically related sections, and reorganizes them into topic-based documents under `.agents/docs/LTM/`. This mirrors how the brain consolidates episodic memories into structured long-term knowledge during sleep.

**Use this skill when:** JOURNAL.md has accumulated multiple entries and you want to distill them into organized, reusable reference documents.

## Overview

JOURNAL.md is an append-only chronological log. Over time it grows unwieldy. This skill extracts knowledge from it and organises it by **topic** (not by date) into separate files under `.agents/docs/LTM/`.

## Step 1: Read and Analyze JOURNAL.md

Read the full contents of `.agents/docs/JOURNAL.md`.

**First, identify and extract any to-do items** (tasks, action items, follow-ups, open issues, "TODO", "FIXME", "next steps", etc.) from all journal entries. These must NOT go into LTM — they go into the repository's `TODO.md` (see Step 3 below).

Identify semantically distinct topics. A "topic" is a cohesive area of knowledge that an agent would want to look up as a unit. Examples of good topic boundaries:

- A specific subsystem or module (e.g., "flag facts and dead-flag elision", "compile scratch retention", "baseline tier sizing")
- A target-specific encoding concern (e.g., "386 narrow masks and byte registers", "arm64 literal reach and relaxation")
- A category of fixes sharing a common mechanism (e.g., "flag writers missing from a flag model", "per-instruction assemblers in size queries")
- A performance investigation and its outcome (e.g., "packed physical program and store forwarding")

Do NOT split by date. Multiple journal entries about the same topic should be **merged** into a single LTM document.

## Step 2: Plan the LTM Documents

Before writing, list out the planned documents with:
- Filename (kebab-case, descriptive, e.g., `flag-facts-and-dead-flag-elision.md`)
- One-line summary of what knowledge it captures
- Which JOURNAL.md sections (by heading) feed into it

Present this plan to the user for confirmation before proceeding.

## Step 3: Extract To-Dos to TODO.md

Before writing LTM documents, collect all to-do items found in JOURNAL.md entries:

- Items explicitly marked as TODO, FIXME, or "next steps"
- Action items or follow-ups described as pending work
- Open issues or unresolved investigations flagged for future attention

Add them to the repository's `TODO.md` (at the repository root, not under `.agents/docs/`). It is the project's one to-do list, shared with humans, and has its own format: dated `##` sections (for example `## Compile-time and code-size follow-ups (2026-11-05)`), optional `###` subsections, and `- [ ]` items whose first line is a bold or plain one-sentence statement, followed by the evidence (files, measurements, which agent or review found it). Items that would add or change exported identifiers are marked *Needs API.*, because they need the user's approval first.

Append new items under a new dated section, or under an existing open section on the same subject. Do not duplicate items that are already listed, and do not edit checked items: they keep their original descriptions.

## Step 4: Create LTM Documents

Create the directory `.agents/docs/LTM/` if it doesn't exist.

For each planned document, create `.agents/docs/LTM/<filename>.md` with:

### Document Structure

```markdown
# <Topic Title>

## Summary

<2-3 sentence overview of the topic — what it is, why it matters>

## Key Facts

<Bulleted list of the most important facts an agent needs to know>

## Details

<Reorganized, deduplicated content from JOURNAL.md entries>
<Merge overlapping sections, remove redundant context>
<Keep tables, code snippets, and file path references intact>

## Files

<List of relevant source files with brief descriptions>

## Test Coverage

<Summary of test cases and how to run them, if applicable>

## Pitfalls

<Known gotchas, edge cases, and warnings>
```

### Writing Guidelines

- **Merge, don't copy-paste.** If multiple journal entries cover the same topic, synthesize them into a coherent narrative. Remove chronological framing ("On 2026-10-31 we discovered...") and write in a timeless reference style.
- **Preserve precision.** Keep exact file paths, type and function names, opcodes, encodings, benchmark numbers with their method (core, rounds, `GOMAXPROCS`), and test names. These are high-value reference material.
- **Keep tables.** Tables from JOURNAL.md (per-target results, encoding sizes, etc.) should be preserved or consolidated.
- Follow the documentation style rules in `AGENTS.md`: half-width parentheses and colons, no full-width punctuation.

## Step 5: Create an Index

Create `.agents/docs/LTM/INDEX.md` that lists all LTM documents with one-line descriptions:

```markdown
# Long-Term Memory Index

| Document | Summary |
|----------|---------|
| [flag-facts-and-dead-flag-elision.md](flag-facts-and-dead-flag-elision.md) | How each target describes the flags an instruction writes, and when a flag write may be elided |
| ... | ... |
```

Preserve the two tables the index already carries (synthesis documents and source topic documents). `good-sleep` writes into the **source topic documents** table; `deep-sleep` owns the synthesis table.

## Step 6: Trim JOURNAL.md

After creating LTM documents, **do NOT delete** existing JOURNAL.md content (per AGENTS.md: never edit existing sections). Instead, append a note at the end of JOURNAL.md:

```markdown
---

## LTM Consolidation Record

The following sections have been consolidated into long-term memory documents under `.agents/docs/LTM/`:

| Section | LTM Document |
|---------|-------------|
| <original heading> | <LTM filename> |
| ... | ... |

See `.agents/docs/LTM/INDEX.md` for the full index.
```

## Step 7: Update the AGENTS.md Reference (if needed)

`AGENTS.md` (`CLAUDE.md` is a symlink to it) lists `.agents/docs/LTM/INDEX.md` among the documents for coding agents. If that entry is missing, suggest restoring it so that agents know to consult LTM documents.

## Notes

- This skill is idempotent: running it again should detect already-consolidated sections and only process new JOURNAL.md entries added since the last consolidation.
- LTM documents are meant to be **edited and refined** over time, unlike JOURNAL.md which is append-only.
- If a JOURNAL.md section doesn't fit neatly into any topic, create a `miscellaneous.md` catch-all or ask the user.
