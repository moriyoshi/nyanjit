# Dual-Tier Design and Baseline Correctness

## Summary

Both tiers share a scope-and-Var Builder dialect, while baseline favors fast compilation and optimized lowering promotes Vars for SSA-quality output. Baseline correctness includes entry labels, occupancy bookkeeping and exact target sizing, not merely operation coverage.

## Key Facts

- Builder Branch/Jump/Bind use no explicit label arguments; path-dependent values flow through Vars.
- Baseline records a compact whole-function form and computes liveness once (TPDE-like).
- Optimized lowering promotes Vars to SSA with Braun's algorithm.
- `compile.TierBaseline` is selected through TargetSpec.Tier.
- A loop targeting an entry label must find that label emitted after the prologue.

## Details

The initial explicit branch-exit argument proposal was rejected: scope exports already identify escaping values. The final design is in docs/dual-tier-design.md. Baseline landed on all six targets with ir.Segment, compile.Stream, NewBuilderFor/Lower and Arena.BindAll.

Historical tuning results:

| Measurement | Before | After |
|---|---|---|
| pressure runtime, baseline/optimized | 2.6x | 1.00x |
| fp runtime, baseline/optimized | 1.77x | 0.99x |
| chain1000 compile metric | 97 ns/op | 58 ns/op |

The journal records the chain metric as ns/op without additional normalization here; retain the original benchmark context before comparing it to whole-function microsecond tables. Baseline engine core was roughly TPDE-equivalent on loop/pressure, with about 1.5-2 us of per-function overhead from map literals, eager reports and Units, plus slower Builder construction. Four fixes each cut 10-20%. A compact-record hand-off redesign measured at most 10% and was not pursued.

Wave w6 added Func.Harden, ContextGoManaged on all six targets, vector/mask Vars, 386 syscall recognition (initially ErrBackendLimit), and L16-L25 hardening. The online switch's known failures were largely test assumptions about the direct walk, but describeOnline also omitted FixedClobbers in exhaustion checks.

Specific baseline bugs demonstrate missing test dimensions:

- A label bound before the first instruction and branched back to later was omitted after prologue emission, causing SIGBUS.
- A 386 register-home occupant record was cleared when occupant equaled written-back content; Var swaps lost values (4/2,000 sweep failures).
- C-ABI 386 StackNoUse leaves clobbered EBX/ESI/EDI; entry saves fixed 900 snapshot cases, all 386/1 C setups.
- Narrow constant SAR on 386 left the sign above width w, so register-width predicates disagreed with the narrow result. Ten irconform shift/branch cases included two failing cases before the fix.
- ARM VMaskBits failed to report implicit X16 writes.

386 syscalls at both tiers use int 0x80, truncate argument words to low 32 bits, use EBP for argument six and sign-extend the return to 64 bits. Other baseline targets initially treated Syscall like a body call and built an unnecessary leaf frame. Updating hasCall only for operations other than OpSyscall removed frames in 39 64-bit baseline snapshot entries. Online allocation can legitimately insert a move, so the regression asserts framing, not equal byte counts across allocation modes.

## Files

- `docs/dual-tier-design.md`: shared dialect and per-tier strategy.
- `compile/baseline_*target.go`, `compile/stream.go`: baseline recording, homes and prologues.
- `ir/`, `builder/`: segments, Vars and promoted control flow.
- `instrument/jitbench/`: whole build/compile comparisons.

## Test Coverage

Use both-tier irconform, scoped-Var sweeps (including swaps), entry-backedge execution, syscall frame assertions, ABI preserved-register checks and deterministic snapshots. Snapshot coverage spans six architectures, two tiers and three function setups. Relevant later fixes are also in [flag models](flag-models-and-code-size-selection.md) and [managed execution](managed-execution-and-stack-safety.md).

## Pitfalls

TPDE figures include IR construction plus compilation; compare them with NyanJIT build-plus-compile. The period's final README shows baseline compiling 3-9x faster than optimized, not a universal constant. Code rebound at recently used addresses ran 8-35x slower on the measured host, motivating one kernel per process.

## Sources

Consolidated from these headings in `.agents/docs/JOURNAL.md`:

- 2026-10-29 — Two tiers: one Builder dialect, a baseline engine on six targets
- 2026-10-30 — Baseline parity items, and an entry-block loop that crashed
- 2026-10-30 — Eleven sljit-gap items in one wave, and what seven parallel merges taught
- 2026-10-31 — 386 flag facts, and a miscompile the old flag model allowed
- 2026-11-05 — Work summary: 386 parity and compile cost, 2026-10-30 to 2026-11-05
