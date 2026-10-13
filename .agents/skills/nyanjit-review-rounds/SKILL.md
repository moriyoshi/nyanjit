---
name: nyanjit-review-rounds
description: Reviewer side of NyanJIT's implement → review → follow-up loop, where another agent (usually Codex in a tmux pane) makes compiler, performance or API changes and you verify them independently and write the next handoff. Use when asked to review or verify a round of work another agent finished on NyanJIT, to write or send a follow-up handoff, or to run the differential, mutation, concurrency, performance, retention or QEMU checks on a NyanJIT change.
---

# NyanJIT review rounds

NyanJIT is a pure-Go JIT compiler backend (amd64, arm64, 386, riscv64, ppc64le,
loong64). Most compiler, performance and API work is done in rounds:

1. An implementer agent (normally Codex, running in a tmux pane) works
   through a handoff file.
2. It reports done, with a report under `.agents-workspace/tmp/<task>/`.
3. You verify independently. Do not trust the report's numbers.
4. You write the next handoff and send it once the user approves.

This skill holds the method used in follow-up rounds 1–10 (2026-10-12 to
2026-10-18). The bug classes those rounds found are in
`references/lessons.md`. Read it before reviewing: most rounds turned up
another instance of an earlier class.

## Ground rules (user decisions; do not relitigate)

- **Implementer limits:** the implementer must **not commit**, and must not
  change host state. That means no packages, host binfmt, systemd,
  `go env -w` or `docker run --privileged`. Repeat both in every handoff.
- **Real OS images:** never bake real Oracle Solaris (or other proprietary
  OS) images into container images.
- **Unpublished API:** delete old APIs outright. No deprecation shims and no
  migration layers.
- **No `internal/` packages.** Helpers must be designed well enough to be
  public, or be unexported in the package that uses them. `abi`, `machine`,
  `regalloc` and `target/*` stay public; construct their structs with keyed
  fields (field order is not API). Measurement and checking tools live under
  `instrument/` (`workload`, `abicheck`), which production code must not
  import (`instrument/imports_test.go`).
- **One ownership/lifetime contract per public API:** borrowed vs owned,
  stable vs "until next X". It must not depend on where a value came from.
  Public functions must not return mutable views of shared or cached state.
- **Generated code:** listings must stay byte-identical unless the round
  intends a codegen change. "Faster" never excuses a listing change.
- **Compiler shape:** `compile.Target` (from `compile.NewTarget`) is the
  shared, reentrant object; lowering takes it (`compile.Lower(target, p, f)`,
  `Emitter.Emit(target, f)`), never the other way round. Its state may only
  skip work, never change output. No SSA conversion, but keep the front end
  SSA-friendly.
- **API consolidation:** the plan (`api-consolidation.md`, target README) is
  done phase by phase, and only on the user's explicit go-ahead, which
  they gave on 2026-10-20. Phase 1 (TargetSpec/Target/Func/Context/Unit;
  old entry points, options and aliases deleted) was done that day; phase 2
  (sealed accessors, `ir.Analysis`) and phase 3 (`machine.Layout`,
  `instrument/`, layering docs) on 2026-10-21, phase 4 (`compile.LimitError`,
  `TargetSpec.Tier`, error docs, `execution.Arena`) on 2026-10-22. The root
  Builder converts the builder package's limit errors to `LimitError`,
  because builder cannot import compile. Phase 4b (IR as a target) made
  values usable in every block their definition dominates: `ir` validates
  dominance (`Analysis.Dominates`), `compile` threads cross-block values
  through internal label parameters before lowering (programs without such
  uses must keep byte-identical listings), and the Builder keeps its
  scope-entry captures. It also added `ir.ValidationError`,
  `ir.ProgramBuilder`, `docs/ir.md`, `instrument/irinterp` and
  `instrument/irconform`. Benchmarks and the scaling
  gate iterate `workload.All()`; add a shape there, not in a test table.
- **Approval:** a user's "hand it off" covers one round only. Ask again for
  the next round.

## The round loop

### 1. When the implementer says it's done

- Read its report (`.agents-workspace/tmp/<task>/report.txt`) and the pane's
  last lines.
- Check `ls -t .agents-workspace/tmp/` for **other tasks the user gave the
  implementer in between**. Their changes sit in the same working tree. If
  there are any:
  - use the implementer's own `before/` snapshot as "pre" for this round;
  - check the interleaved changes separately: diff your previous snapshot
    against that `before/`. Their listing changes are expected, so run only
    a smaller differential on them and spot-check that the new code is
    equivalent.

### 2. Snapshot

Copy the working tree into your scratch dir and diff it:

```sh
git ls-files -co --exclude-standard | grep -v -e '^\.agents-workspace/' -e '^\.claude/' \
  | while read f; do [ -e "$f" ] && echo "$f"; done | tar -cf - -T - | tar -xf - -C <scratch>/revN/post
```

The `[ -e ]` filter skips tracked files deleted in the tree. Then:

```sh
diff -ruN rev(N-1)/post revN/post > revN/rN.diff
```

Keep every snapshot: later rounds compare against round 6/8 baselines.

### 3. Verify with three parallel agents

They are independent, so start them in one message. Give each:
- the before/after trees;
- the handoff file and the report path;
- a fixed CPU set;
- the ground rules above;
- "run long jobs detached with DONE markers; foreground sleep is blocked".

**a. Differential and fuzz** (CPUs 0–9): see `references/verification.md`
§1–5.
- Differential runs over all generators.
- A poison build of post.
- Native byte/report comparison.
- Concurrency determinism, including `-race`.
- IR mutation pre vs post, extended to whatever fields the round touched.

Start from `assets/harness/` + `assets/scripts/`.

**b. Fixes review** (CPUs 10–13):
- give a per-item verdict for the handoff;
- re-run every earlier reviewer repro against post;
- probe the design of any new public API against the policy above;
- run the gates (`go test`, poison, race, vet, 386 via qemu-i386 with a
  sysroot);
- sweep for scope creep, dead code and stale docs.

**c. Perf, retention and QEMU** (benchmarks on CPU 17 only; QEMU
`--cpuset-cpus` within 14–16,18–19): see `references/verification.md`
§6–8. Always compare against the previous round and against the round-6
baseline, and check memory retained across workloads, not just
time/allocs.

### 4. Judge

- Check cited lines yourself before forwarding a finding.
- Classify every difference as intended or unintended.
- A panic from any compiler entry point on validated IR is always a
  required fix.
- So is anything that validation accepts and lowering then rejects with
  `invalid function` / "no materializable source".

### 5. Write the handoff

Use `references/handoff-format.md`. Save it as
`<scratch>/handoff-followups<N>.md`.

Then tell the user, in a short report:
- what was verified clean (with counts);
- what the required items are;
- that the handoff is written but **not sent**.

Ask whether to send it unless the user already said "hand it off once the
review has finished" *for this round*.

### 6. Send it

See "Codex pane handling". Confirm it started working.

## Codex pane handling

- **Find the pane:**
  - Pane IDs move, so identify it each time: `tmux display -p -t <pane> '#{pane_current_path}'`
    must be the repo, and its footer must show the Codex prompt.
  - The project's auto-memory may record the last known ID.
- **Busy check:** busy means `tmux capture-pane -p -t <pane> | grep -c 'esc to interrupt'`
  is non-zero.
  - **Never** send Escape or `/status` to a busy Codex.
  - If it's busy, type the message and press **Tab** to queue it.
- **Send:** `tmux send-keys -t <pane> -l "<message>"`, then a separate
  `tmux send-keys -t <pane> Enter`.
  - Verify it started: wait until `esc to interrupt` appears.
  - The first Enter sometimes only leaves the text in the input box. If
    the message is still in the box after ~60 s, press Enter again.
- **Message shape:**
  - start with a one-line project description ("nyanjit is a JIT compiler
    backend library for Go.");
  - point at the handoff file by absolute path;
  - say "Do not commit, and do not modify host system state."
  - Use neutral compiler vocabulary.
- **Context compaction:** Codex may compact after a long handoff. It still
  proceeds; check the pane rather than resending.

## Reporting to the user

The user sees only your messages, not agent reports, and reads tersely:
- lead with the verdict;
- give the counts that back "clean";
- list the required items in one line each.

Name things by what they are, not by internal labels or file IDs. While
agents run, say they are running. Never predict their results.
