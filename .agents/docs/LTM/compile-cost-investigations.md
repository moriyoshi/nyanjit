# Compile Cost Investigations

## Summary

Profiles repeatedly found per-instruction allocation, formatting and redundant copying ahead of allocator algorithm costs. Preserve the benchmark's target, tier, baseline and ownership constraints when reusing a performance claim.

## Key Facts

- The original reference is the [archived compile-time baseline](../ARCHITECTURE.md#compile-time-baseline), post round 6 and before allocation reductions.
- Size-only queries must avoid constructing listing text and temporary byte assemblers.
- Unit-owned physical programs are durable output needed by lazy listing, encoding and unwind generation.
- Compare a target-specific regression relative to a sibling target when generic improvements moved both.
- Percentage improvements below use separate historical baselines and must not be added or multiplied.

## Details

The original baseline used Go 1.27.2 linux/arm64, CPU 5, GOMAXPROCS=1, source IR count before vector legalization:

```sh
taskset -c 5 env GOMAXPROCS=1 go test ./compile -run '^$' -bench '^BenchmarkPrepare$' -benchmem -benchtime=300ms -count=5 -cpu=1
```

Prepare/tiny was 8,504 ns/169 allocs, small 62,990 ns/618 allocs; loops 1.98 s/op and scopes 9.26 s/op (1,157,509 ns/source instruction). Fixed plans, scratch reuse, Builder slabs and geometric growth produced:

| Workload | Before | Final |
|---|---|---|
| Prepare/small | 62,990 ns, 618 allocs | 44,263 ns, 103 allocs |
| Prepare/calls | 43.0 ms, 1,082,486 allocs | 16.9 ms, 32,170 allocs |
| Prepare/chain | 21,298 allocs | 69 allocs |
| Build/small | 18,534 ns, 233 allocs | 8,846 ns, 67 allocs |

Step-3 B/op worsened on chain (1.91 -> 3.12 MB) and scopes (1.60 -> 2.82 MB). Baseline ran on CPU 5, step pairs on CPU 7; a contaminated CPU-5 repeat with 98.8 ms samples against about 6.3 ms was excluded. Raw logs were later deleted; source reconstruction agreed with published B/op and allocs.

Later scratch arenas (`carve`), reusable fragment bodies, static sizes, FloatWrites caching and pooled emitter constructors reduced heavy allocations 42-99%: fp 46,289 -> 331; loops 41,256 -> 4,713, with loops about 18-38% faster. The FloatWrites key covers exactly the seven fields the query consumes.

The optimized Cranelift investigation divided the profile (approximately malloc/GC 30%, allocation/emission 20%, IR hand-off 25%, machine passes 15%, selection 12%; approximate categories) into three byte-identical changes:

- Machine passes reused flags, compacted/relaxed in place and remembered Append demand: 5-16% faster.
- Allocation used temporary arenas/maps/bitsets and one lowerPass: 5-15% faster, B/op down 25%.
- Front end pooled segment recording, lazily built sealed analyses and used dense/slab tables: 7-10% faster, allocations down 27-37%, with no validation removed.

ARM optimized loop/pressure/collatz/fp went 39.7/141/74.4/91.7 -> 30.8/103/54.7/65.8 us; amd64 cross improved 26-34%. The later full README comparison put loop kernels 1.6-2.3x slower than Cranelift speed, tiny 1.4x faster and chain1000 1.7x faster. The earlier report said 1.4x for chain1000; use the later run's baseline rather than treating this as a contradiction.

The reviewed amend series also reordered struct fields for GC scanning, fixed quadratic `scopeEntrySplits` processing and used stack buffers in `cross_block`. Those allocation/layout changes accompanied FP fusion, literal alignment and scaled multiplication; the journal does not isolate their individual speedups.

386 recovery found `i386.FlagFacts` setting up an 848-byte frame on every instruction. A mechanically checked early-return table improved 3.2% (amd64's analogous fix 2.0%). Fresh assembler/code allocation in PhysicalSize was 27-30% of optimized 386 CPU; pooling cut 13.8%. Float/paired-shift/word-carry/vector plans allocated bytes and Sprintf text even during sizes or byte emission, costing 25-40% of baseline 386 CPU. The recovery yielded baseline -45.4%, optimized -20.3%, amd64 optimized -2.0%, with identical code. Most cost predated w8's +4.7%/+3.2% regression.

The located copy from finishRegisters (about 16 MB/op for 386 pressure) becomes `optimizedUnit.physical`; lazy consumers need it. Leasing it as scratch was rejected. Instead:

- Skip unconditional loong64/riscv64 C-call bridge copies when there is no mCallC (19% of optimized bytes in that profile); when copied, positions map identically.
- Lease one uint64 table for verifySelectedProgram instead of ten separate allocations, keeping every check/error.
- Precheck ARM literal relaxation with its exact reach condition.
- Reserve the literal-pool slot so finishLiterals need not copy at listing time.

Those changes improved optimized riscv64 15.3% (54% B/op), loong64 11.2% and arm64 4.5%. Remaining uncommon formatting/size queries and API proposals are in TODO.md.

## Files

- [Compile-time baseline](../ARCHITECTURE.md#compile-time-baseline), `instrument/jitbench/`, `README.md`: reference methods and comparison tables.
- `compile/physical_code.go`, `compile/compiler.go`, `compile/literals.go`, `compile/report_fp.go`: physical ownership and reporting.
- `backend/target/i386/`, `backend/target/amd64/`, `backend/target/loong64/`: sizing plans and assembler reuse.
- `compile/perf_sign_alias_test.go`: sign-alias workload outside the scaling gate.

## Test Coverage

`TestFloatWriteKeyCoversInputs` uses reflection to fail when FloatWrites starts consuming another field. Use snapshot/listing equality for no-output-change work, retention/poison gates for ownership, and matched pre/post BenchmarkZZW8 or jitbench runs for costs. A 64k single block can exceed retention and regrow even with linear algorithms; the sign-alias benchmark records that effect rather than treating it as a slope failure.

## Pitfalls

One register-name-comparison closure, not the suspected RegisterSet iterator, consumed 7.42% of Prepare/calls; fixing it beat round 6 by 7-12%. A 32% allocated-byte share for cross-block copying is not a predicted 32% speedup. Profiling finds the largest cost, which can be older than the change that motivated investigation.

## Sources

Consolidated from these headings in `.agents/docs/JOURNAL.md`:

- 2026-10-16 — Compile allocations, round 6 and steps 1-3, and the baseline they were measured from
- 2026-10-17 to 2026-10-18 — Review rounds 6-10: scratch retention, poison builds, and validation that lowering trusts
- 2026-10-23 to 2026-10-24 — Writing FORMS.md found encoder bugs compile never hits; a QEMU hang that was not ours
- 2026-10-26 — Allocations down 42–99%, and a frozen corpus that had stopped meaning anything
- 2026-10-27 — A register allocator protocol, and porting compile onto it
- 2026-11-01 — Optimized-tier compile time against Cranelift, and two corrections from the user
- 2026-11-01 — Optimized-tier takeover: FP branches and literal packing
- 2026-11-04 — Reviewing Codex's amends
- 2026-11-04 — 386 compile-cost recovery: most of the cost was older than w8
- 2026-11-05 — The located copy is the Unit's output, and what could be removed instead
- 2026-11-05 — Work summary: 386 parity and compile cost, 2026-10-30 to 2026-11-05
