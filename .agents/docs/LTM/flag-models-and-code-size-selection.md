# Flag Models and Code Size Selection

## Summary

Flag reuse and dead-flag rewrites are correct only when every writer, reader and selected encoding is modeled. Encoding-size changes must also update baseline sizing and be assessed per case, not just by aggregate savings.

## Key Facts

- Calls and syscalls kill flag facts even when the source IR has no explicit flag result.
- A MOVZX mask writes no flags; facts must describe the unmasked arithmetic result.
- 386 overflow liveness tracks OF separately from S/Z/C to preserve ADD feeding JO.
- Baseline handwritten sizes must match encoded operand-dependent sizes.
- Size-only public streams cannot be assumed to contain exact flag metadata.

## Details

386's old FlagEffect omitted CallC, CallGo and Syscall. An INC zero fact survived a callee that overwrote flags, so compare elimination reused stale flags after the call. Neither the conformance corpus nor snapshots arranged a reader across that missing writer. `i386.FlagFacts` brings the precise model into the stream.

amd64/386 dead-flag LEA replaces add/sub/inc/dec into a different destination when flags are dead. Initial amd64 corpus savings were 3.32%, with nothing larger. Golden replay accepts intended differences by re-lowering with test-only KeepFlagWrites and requiring exact old output. The initial +2.7% compile-cost claim was invalid because only the baseline rebuilt uncached targets per case. Shared targets and alternating runs gave +6.7%; copying/re-measuring was three quarters of it. Approved Stream.ReplaceAt and ModifiedFlagsAt reduced elision overhead to about +5% and improved ppc64le lowering 21.5%.

F20 showed amd64 ElideDeadFlags incorrectly trusting size-only/legacy stream flags: add; jb became LEA for stackvm while compile's exact-fact streams were unaffected. Kills now intersect stream flags with amd64's every-CPU writes (FlagFactsWithCPU including BMI2), and candidate facts include the target's own model. Legacy ModifiedFlags/ModifiedFlagsAt remain conservative. FuseLoops uses per-flag liveness and every-CPU kills; ChangesFlags is safe without metadata. Worklist liveness and a 64-instruction straight-line lookahead avoid quadratic raw-stream passes.

386 encoding work produced these historical corpus changes:

| Change | Optimized | Baseline |
|---|---|---|
| Dead-flag LEA | 138 entries, -5.2 KB; sha256-scalar -4.2% | unused |
| Short immediates | -1.2% bytes | -5.3% bytes |
| MOVZX masks / live-flag EAX `25 id` | -107.5 KB (-1.68%) | -120 KB (-1.70%) |
| Redundant double masks | -53.7 KB, 1,140 entries | not recorded |
| Byte-register preference | direct -28.7 KB; online -6.4 KB | -13.9 KB |

Short immediates include `83 /n ib`, EAX forms and frame adjustments. `i386ImmediateSize` replaced fixed sizes in roughly a dozen baseline sites. OF uses a private bit so ADD feeding JO cannot become LEA. Decode-plus-execution regressions ensure masked forms do not invent zero/sign facts from MOVZX. Eight exported NoFlags visitor methods were approved after their addition; this historical exception does not waive prior-signature approval rules.

BenchmarkZZW8 pre/post-w8 used six alternating rounds, core 5, GOMAXPROCS=1: optimized 386 +4.7%, baseline +3.2% compile geomean, amd64/arm64 within +/-1.3%; code -1.1%/-0.6% on performance workloads. Subsequent profiling recovered much larger preexisting costs; see [compile investigations](compile-cost-investigations.md).

Eligible x*(1+2^k) uses scaled forms on amd64/arm64/386. Across the reviewed amend series, 848 optimized entries changed and total code fell 61 KB. Three amd64 float-abi/control-64 entries grew four bytes (2-byte jrcxz -> 6-byte jbe). There is no isolated multiply benchmark, so do not attribute the series' timing gains to this rewrite.

ARM narrow subtract-immediate selection also matters: sign-extended 16-bit negative literals could turn SUB into 32-bit ADDS with carry unrelated to the narrow subtraction. Selection uses register SUB for negative narrow literals, and flag reporting drops the invalid carry. ARM AND facts must likewise match the actual width used to encode a 32-bit mask.

## Files

- `backend/target/i386/`, `backend/target/amd64/dead_flags.go`, `backend/target/amd64/loops.go`: facts, readers and rewrites.
- `backend/machine/zero_loops.go`: zero-loop worklist liveness.
- `compile/baseline_*target.go`, `compile/*target.go`: sizes and selected forms.

## Test Coverage

`TestI386FlagsAcrossCalls` arranges the missing writer/reader relationship. `TestElideDeadFlagsIgnoresInexactStreams`, `TestFuseLoopsKeepsFlagsReadPastPartialWriters`, `TestFlagLivenessIsLinear`, `TestFuseZeroLoopsLivenessIsLinear` and `BenchmarkElideDeadFlagsStaircase` cover public streams and scaling. Baseline sizing encodes each form at -129, -128, 127, 128, zero and large values. Narrow ARM subtraction scans 65,790 nonzero 8/16-bit immediates. Require a positive JO dependency check for OF-sensitive LEA changes.

## Pitfalls

A snapshot can prove no output changed without establishing the old output was correct. Tests must force a consumer after the suspected writer. Aggregate code shrinkage hides growing functions; inspect individual entries with growth.py. Outstanding duplicate narrowings and 386 byte-register occupancy work are in TODO.md.

## Sources

Consolidated from these headings in `.agents/docs/JOURNAL.md`:

- 2026-10-14 — Backend parity with PPC64, and CPU profiles
- 2026-10-14 — Scoped Vars, and a green matrix that proved nothing
- 2026-10-25 — Second audit: the Medium and Low findings, and how the audit ran
- 2026-10-25 — Dead-flag LEA on amd64, and a benchmark that inflated its own baseline
- 2026-10-31 — 386 flag facts, and a miscompile the old flag model allowed
- 2026-11-01 — 386 code size, one encoding at a time
- 2026-11-01 — Scaled multiplication and a shared stack VM
- 2026-11-04 — Reviewing Codex's amends
