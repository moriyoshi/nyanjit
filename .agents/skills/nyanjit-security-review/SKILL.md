---
name: nyanjit-security-review
description: Security review of the NyanJIT working tree (pure-Go JIT used as infrastructure by other projects). Use when asked for a security review, vulnerability check, pre-release audit, or threat model of NyanJIT, or to re-check that earlier security fixes have not regressed. Covers execution/mmap and call paths, goregister runtime integration, all six backends' encoders, compiler stack safety, IR validation and DoS, consumer-facing Data views, repository hygiene, and a mapping of historical JIT-engine vulnerability classes.
---

# NyanJIT security review

NyanJIT compiles caller-built IR to native code and runs it in-process. It is
infrastructure: downstream projects will compile **end-user input**
(queries, expressions, bytecode) through it. Review from that point of view.
A bug matters when a downstream user, or an end user of a downstream product,
can reach it through the public API.

This skill encodes the method and the bug classes found in the first audit
(2026-10-18). That audit's record is in
`.agents-workspace/tmp/security-audit-20261018/` (`journal.md`,
`threat-model.md`, `poc/`) if the directory still exists; its findings are
tracked as F1–F13 and L1–L12 in `TODO.md`.

## Ground rules

- **Review the working tree**, including uncommitted and untracked files;
  that is what will be published. Skip `.agents-workspace/` and `e2e/.build/`.
- **Do not modify repository files during the review.** Write reproducers as
  separate Go modules outside the repository, using
  `scripts/new-poc-module.sh`. The public packages (`machine`, `regalloc`,
  `target/*`, `execution`, `compile`, `builder`) are enough for almost every
  reproducer.
- **If another agent is editing the tree** (for example Codex applying fixes),
  take a snapshot first with `scripts/snapshot.sh` and review and build
  against the snapshot. Say which tree each finding was checked against.
- **Cap resources.** The machine is shared, and an earlier session was lost
  while reproducers ran unbounded. For every heavy command:
  - `timeout 300` (120 for DoS reproducers and fuzzing);
  - `GOMAXPROCS=4 GOMEMLIMIT=4GiB`;
  - `ulimit -v 16000000`;
  - `go test -p 2`, fuzzing with `-fuzztime=60s -parallel=2`.
  Grow DoS inputs gradually: a measured superlinear curve is enough
  evidence; never jump to the size that exhausts memory.
- **Never run Go commands with `-mod=mod` inside the repository** (for example
  `go list -m all`): they rewrite `go.sum`. Run module queries in a PoC module.
- **Fuzzing must not leave files in the repo.** If a fuzz run writes under
  `testdata/fuzz/`, copy the crasher out and delete what it created.
- **Stay defensive.** Confirm a defect with the smallest reproducer that
  shows wrong output, a crash, or corruption of a canary. Do not develop it
  further into an exploit (no ROP chains, shellcode, or sandbox escapes).
  Builder-only checks that execute nothing are often enough.
- Host is linux/arm64: arm64 code runs natively; qemu-user binfmt usually
  runs 386, riscv64, loong64 and ppc64le (check `/proc/sys/fs/binfmt_misc`);
  amd64 is checked by decoding with `golang.org/x/arch/x86/x86asm`.
  Do not install or change binfmt, packages or other host configuration.

## Workflow

1. **Orient.** Read `README.md` and `TODO.md`, run `git status --short`, and
   run `scripts/hygiene.sh`. Note which earlier findings are marked fixed:
   each one gets a regression re-check in step 3.
2. **Split by surface.** Review the six surfaces in
   `references/checklists.md`. They are independent. If you can run parallel
   subagents, give each one surface, its checklist section, the ground rules
   above, and the report format below. Otherwise do them in order: 1, 5, 3, 2,
   6, 4. That order puts memory corruption reachable through the plain
   `compile` → `Bind` → `Call` path first.
3. **Re-check fixed findings.** For each F/L item marked done in `TODO.md`,
   confirm that its regression test exists and passes, and rerun the matching
   reproducer from the earlier audit if it is still around. A fixed item
   without a test that fails on the old behaviour counts as open.
4. **Threat-model pass.** Walk the classes in
   `references/jit-vulnerability-classes.md`. For each one, decide whether
   NyanJIT has the mechanism, what the analogue is, and whether a protection
   exists. Record "not applicable" with the structural reason; that is a
   result too.
5. **Verify before reporting.** Drop anything you cannot substantiate. For
   every confirmed finding, check the source yourself at the cited lines;
   do not forward a subagent's claim unread.
6. **Report and file.** Present findings in chat. Then, if the user asks,
   write the journal and TODO entries (see "Filing").

## Severity

Judge severity from a downstream user's point of view:

- **Critical:** silent memory corruption reachable through documented,
  ordinary use of the public API (example: F1, unbounded JIT frames
  overflowing goroutine stacks).
- **High:** miscompilation or memory corruption under realistic options;
  fatal, unrecoverable runtime crashes under common production settings
  such as profiling or tracing; small input causing exponential cost.
- **Medium:** needs malformed raw IR, unusual API sequences, or a documented
  contract the API makes easy to break; resource exhaustion that grows with
  long-running use.
- **Low / Info:** defence in depth, guarded today by upstream checks;
  documentation gaps.

A Go panic from a compiler entry point on bad input is at least Medium:
`compile` has no `recover`, so it crashes the embedding process.

## Report format

For each finding, most severe first:

- **Severity**, short title, and `file:line`.
- **What is wrong**, in a sentence or two.
- **Trigger:** concrete options and IR, or API sequence.
- **Evidence:** reproducer path and the key output line, or "from reading".
- **Fix direction.**

End with **checked and found sound** (with how it was checked) and
**not covered**. Both matter: they stop the next review from repeating work
or assuming coverage that never happened.

## Filing

When asked to record results:

- **Journal:** `.agents-workspace/tmp/security-audit-<YYYYMMDD>/journal.md`
  with purpose, method, interruptions, every finding in full, the sound
  areas, and not-covered. Copy reproducer sources (not binaries) into
  `poc/` next to it, with `go.mod` `replace` pointing at the repository.
  Note: `find -size -1M` matches only empty files; use `-size -1000k`.
- **TODO.md:** add items under "Security audit (<date>)" in three tiers
  (Blockers for publication / Should fix before publication / Hardening),
  continuing the existing F/L numbering. Each item names the location, the
  fix direction, the reproducer, and says a regression test is required.
  Read `TODO.md` again right before editing; other agents tick items off
  in it.
- **Handoff to another agent**, if asked: write `handoff.md` in the same
  directory with order of work, rules (no commits, no host changes, no
  deprecation shims since the project is unpublished, no new `internal/`
  packages, regression test per fix, resource caps), per-finding notes, and
  the report it must produce.
