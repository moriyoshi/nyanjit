---
name: distill-memories
description: Read `.agents/docs/JOURNAL.md` and `.agents/docs/LTM/` documents, find durable knowledge that belongs in the canonical docs, and update those documents with concise synthesized findings. Targets are `README.md`, consumer references under `docs/`, `.agents/docs/ARCHITECTURE.md`, the per-target `backend/target/*/FORMS.md`, and `.agents/docs/QUALITY_GATE.md`. Use when journal or LTM notes have accumulated user-facing, design or convention knowledge that should be promoted into the project's canonical documentation.
user-invocable: true
allowed-tools: Bash, Read, Write, Edit, Grep, Glob
---

# Distill Memories

## Overview

Promote durable facts from `.agents/docs/JOURNAL.md` and `.agents/docs/LTM/` into the project's canonical documentation, keeping these up to date:

a. `README.md` ... the human-facing description of NyanJIT: the API and its contracts, supported targets and tiers, limits, the Performance section and its comparison tables.
b. `docs/*.md` ... consumer references, one subject each: `ir.md` (the IR specification), `dual-tier-design.md` (tier selection and segment front ends), `sealed-programs.md`, `backend-parity.md`, `virtual-register-design.md`, `simd-design.md`, `optional-hardening-design.md`, `cpu-profiles.md`. Internal compiler design, implementation invariants, plans, historical acceptance and measurement evidence belong in `.agents/docs/ARCHITECTURE.md`; do not recreate the moved FP/scoped-variable reports or completion JSON files under `docs/`.
c. `backend/target/*/FORMS.md` ... the documented encoding forms and sizes per target. `TestDocumentedForms` assembles every row, so a row is a checked claim.
d. `.agents/docs/QUALITY_GATE.md` ... the checklist a change must pass before it is reported as done, and the implementation conventions. Create it on the first promotion if it does not exist yet.

## Sources: JOURNAL and LTM

- `.agents/docs/JOURNAL.md` is the append-only log of findings, decisions, benchmark results and review history. It holds the **freshest** durable facts, including changes not yet consolidated into LTM. The `good-sleep` / `reconcile-journal-ltm` skills consolidate JOURNAL entries into `.agents/docs/LTM/`; distill runs against whatever is present.
- `.agents/docs/LTM/` holds the consolidated, topic-organized reference material (`INDEX.md` is its table of contents).

Read both. When JOURNAL and LTM overlap, treat LTM as the settled synthesis and JOURNAL as the source of anything newer than the last consolidation. If JOURNAL records a change that the target docs do not yet reflect, that is exactly the gap this skill closes.

## Read in This Order

1. Read `README.md` (at least its table of contents and the sections the candidate facts touch) and `.agents/docs/QUALITY_GATE.md` if it exists.
2. Read `.agents/docs/JOURNAL.md` (at least the entries since the last consolidation record) and `.agents/docs/LTM/INDEX.md`.
3. Open only the relevant LTM documents, `docs/` consumer pages and `.agents/docs/ARCHITECTURE.md` sections for the gaps, stale sections, or missing detail you identified.

Do not bulk-load every LTM file or every design document unless the set is still small enough that selective reading costs more than reading them all.

The source of truth for any contract is the code. Verify a fact against it (exported identifiers, error values, limits, `FORMS.md` rows) before writing it into `README.md` or `docs/`; JOURNAL and LTM point you at what changed, not at the exact current name.

## Classify Findings Before Editing

For each candidate fact, decide whether it belongs in:

- `README.md`: what a user of the library observes. Exported API and its semantics, supported targets and tiers, what returns which error, documented limits, performance figures and how they were measured.
- `docs/<subject>.md`: consumer semantics and usage, for example what a sealed program guarantees, how to select a tier, or which target/profile supports an operation.
- `.agents/docs/ARCHITECTURE.md`: compiler implementation rationale and invariants, such as why baseline fuses a form, how home planning or literal layout works, and internal validation/measurement evidence. Preserve archived JSON snapshots as historical data rather than silently treating their statuses as current.
- `backend/target/*/FORMS.md`: a new or changed encoding form, with its size.
- `.agents/docs/QUALITY_GATE.md`: a check or convention that should apply to every future change (for example the snapshot identity check, the verification set for shared code, the rule that new exported API needs approval).
- More than one: a single change often lands in two places, for example a new opcode in `docs/ir.md` and in every `FORMS.md`. Update each in its own register.
- None of them: narrow bug history, one-off investigations, temporary workarounds, or details too fine-grained for canonical docs.

Prefer durable knowledge over incident history. Convert timelines into timeless guidance.

## Update Strategy

When updating the target docs:

- Synthesize; do not copy JOURNAL/LTM prose verbatim.
- Merge into existing sections when possible instead of appending new sections.
- Add a new section only when the information represents a stable topic that the current document truly lacks.
- Keep summaries compact. Core docs should stay easier to scan than the underlying notes.
- Preserve exact file paths, identifiers, opcodes and figures when they help precision.
- Performance figures in `README.md` come from one full jitbench run; do not update one cell from a different run (see the `nyanjit-jit-bench` skill).
- If multiple sources disagree, or JOURNAL and the current code disagree, call out the ambiguity or stop and ask the user before cementing one interpretation.

## Editing Heuristics

- Favor contracts and invariants over patch-level history.
- Favor current subsystem boundaries over implementation anecdotes.
- Omit test names unless they explain a guarantee or an important invariant.
- Omit details that are already covered well in the target document.
- Fix obvious typos or stale wording in the touched sections if doing so improves clarity.

## Validation

Before finishing:

1. Re-read the edited sections of every document you changed.
2. Check that each added fact is supported by a source note (JOURNAL/LTM) and verified against the code.
3. Check that internal implementation plans/history stay in ARCHITECTURE.md, consumer contracts remain in README/docs, and README-level summaries do not replace detail the references need.
4. If you changed a `FORMS.md`, run `go test -C backend ./target/... -run TestDocumentedForms`.
5. Keep the documentation style rules in `AGENTS.md`: half-width parentheses and colons, no full-width punctuation.
