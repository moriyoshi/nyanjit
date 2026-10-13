---
name: deep-sleep
description: "Consolidate related long-term memory documents under .agents/docs/LTM/ into broader synthesis documents and refresh the LTM index. Use when LTM has grown into overlapping topic files that would be easier to orient in as a smaller set of durable syntheses."
user-invocable: true
allowed-tools: Bash, Read, Write, Edit, Grep, Glob
---

# Deep Sleep: Consolidate Long-Term Memory Documents

This skill reads `.agents/docs/LTM/INDEX.md` and the relevant documents under `.agents/docs/LTM/`, then groups overlapping topics into broader synthesis documents while keeping the source documents intact for traceability.

Use this after `good-sleep` has already distilled chronological journal entries into topic-oriented LTM files and those files now need a second-stage consolidation.

## Goal

Turn a set of narrow LTM notes into a smaller set of durable synthesis documents. Source documents are preserved; the synthesis is an orientation layer on top of them, not a replacement.

## Step 1: Inspect the Existing LTM Set

Read `.agents/docs/LTM/INDEX.md` first, then open only the LTM documents needed to understand the candidate clusters. Do not bulk-load every file unless the LTM set is still small enough that doing so is cheaper than selective reading.

Look for:

- Multiple documents about the same package or subsystem (`compile`, `backend/machine`, `backend/regalloc`, a target under `backend/target/`, `builder`, `ir`, `backend/execution`)
- Repeated pitfalls, file references, or test guidance across documents
- One overview document plus several implementation-detail documents that should be summarized together
- A performance narrative spread across several investigations that would read better as one

Natural cluster boundaries in this project tend to follow the architecture rather than the calendar:

- **Targets and encoders** — per-target forms and `FORMS.md`, size agreement between size functions and encoders, flag facts, implicit writes, short encodings
- **Optimized tier** — selection, Var promotion, register allocation, emission, layout and branch relaxation, literal pools
- **Baseline tier** — the one-pass engines, their hand-written sizes, what they fuse and what they leave to the optimized tier
- **IR, Builder and sealed programs** — `ir.Program`/`Segment`, `Data` views, cells and `backend.Finalizers`, validation
- **Execution and the Go runtime** — templates, binding, pinning, managed calls, unwind information, GC interactions
- **Compile-time cost and scratch memory** — workspace leases, retention budgets, demand histories, the packed physical program
- **Verification and measurement** — listing and report identity checks, `irconform`, varfuzz, the e2e matrix under QEMU, poison builds, alternating before/after benchmarks, jitbench

## Step 2: Propose Consolidation Clusters

Before writing, present a plan to the user with:

- The synthesis documents you propose to create
- The source LTM documents that feed each synthesis document
- Any source documents that should remain standalone because they are already cohesive

Cluster by durable topic, not by date and not by arbitrary file-count balancing.

## Step 3: Write Synthesis Documents

Create new synthesis documents under `.agents/docs/LTM/` using descriptive kebab-case filenames such as:

- `targets-and-encoders-synthesis.md`
- `compile-time-and-scratch-memory-synthesis.md`
- `verification-and-measurement-synthesis.md`

Keep the original source LTM documents. Do not delete or overwrite them unless the user explicitly asks for replacement.

Use this structure:

```markdown
# <Synthesis Title>

## Summary

<2-4 sentence overview of the merged topic and why it matters>

## Included Documents

| Document | Focus |
|----------|-------|
| [source-a.md](./source-a.md) | <short note> |
| ... | ... |

## Stable Knowledge

<Bulleted list of the durable facts, constraints, and design decisions>

## Operational Guidance

<How an agent should approach work in this area>

## Files

<Important file paths and why they matter>

## Tests

<Which test layer covers this area, and the command to run it>

## Pitfalls

<Failure modes, tricky assumptions, and gotchas>
```

## Step 4: Synthesize, Do Not Merely Concatenate

When merging documents:

- Deduplicate repeated explanations
- Convert chronological narratives into timeless guidance
- Preserve exact file paths, constant names, function names, benchmark numbers, and test names when they are useful
- Keep contradictions visible; if two source docs disagree, call that out explicitly instead of silently choosing one

Prefer compact synthesis over exhaustive restatement. The new document should help future agents orient quickly and then drill into the source docs only when needed.

## Step 5: Watch for Material That Belongs in the Canonical Docs

Some durable knowledge outgrows LTM. While consolidating, flag (do not write) anything that belongs in:

- `README.md` — a user-visible contract: API behaviour, supported targets and tiers, limits, performance figures
- `docs/*.md` — a design document's subject (`docs/ir.md`, `docs/dual-tier-design.md`, `docs/sealed-programs.md`, `docs/backend-parity.md` and the others): a stable structural fact, an invariant, or a rationale that constrains future changes
- `backend/target/*/FORMS.md` — a documented encoding form or its size
- `.agents/docs/QUALITY_GATE.md` — a convention or check that should apply to every future change

Promotion into those three documents is the `distill-memories` skill's job, not this one's. List the candidates at the end of your run so the user can invoke it. Keeping the two skills separate is what stops a consolidation pass from quietly rewriting the canonical docs.

## Step 6: Refresh the Index

Update `.agents/docs/LTM/INDEX.md` so it clearly distinguishes:

- Synthesis documents (with the source documents each consolidates)
- Source topic documents

Both tables already exist in the index. Fill them; do not restructure the file.

## Step 7: Record the Consolidation

Append a short note to `.agents/docs/JOURNAL.md` describing:

- Which synthesis documents were created
- Which source LTM documents they consolidate
- Any documents intentionally left standalone
- Any candidates flagged for `distill-memories`

Do not edit or delete existing journal sections. Only append.

## Guardrails

- Do not delete source LTM documents without explicit user approval
- Do not collapse unrelated topics just to reduce file count
- Prefer a few high-value synthesis documents over a full rewrite of the entire LTM tree
- Preserve the documentation style rules in `AGENTS.md`: half-width parentheses and colons, no full-width punctuation
- Do not edit `README.md`, `docs/`, `FORMS.md` or `QUALITY_GATE.md` from this skill

## Notes

- This skill complements `good-sleep`; it does not replace it
- Re-running this skill should extend or refresh synthesis documents when new source LTM files appear
- If the current LTM set is already small and non-overlapping, say so and avoid forced consolidation
