# Compile Scratch Retention and Growth

## Summary

Bounded scratch reuse reduces compile allocation without retaining one-off large workloads forever. Demand observation, hysteresis, poison leases and result ownership must be treated as separate parts of the design.

## Key Facts

- Historical limits: pointer-bearing buffers above 1 MiB drop individually, 4 MiB per buffer, 32 MiB per context; recurring-demand credit later permits selected larger pointer buffers within the entry budget.
- Observe final lengths before TrimPointers, Trim or Decay can discard them.
- Demand-aware growth doubles toward remembered demand and stops exactly there; do not jump directly to possibly stale demand.
- Poison builds detach old arrays and supply fresh poison-filled arrays of equal capacity while preserving history.
- Output retained by Units/Templates is owned result memory, not scratch.

## Details

The allocation work combined fixed plans, caller-owned buffers, immutable tables/call-effect caches, per-compile pools, Builder slabs, geometric growth and flat allocator use lists. A reusable public Compiler was unnecessary because ordinary functions already reuse bounded scratch.

Retention went through four designs:

| Policy | Failure/evidence | Resolution |
|---|---|---|
| No decay | Scopes(10000), 2,000 tiny compiles and 10 GCs left 28 MB/P (about 560 MB at 20 Ps); map sizes undercounted about 20% | First reduction reached 632 B |
| All-or-nothing drop | Big/tiny/tiny alternation +58% bytes, 2.7x mallocs; all-time peakWork prevented medium decay | Per-buffer histories |
| 1/16 decay with 2x band | At 1:10 big/tiny, threshold crossed every cycle: 6.8k vs 2.8k mallocs | Wider band |
| 4x band alone | Scopes(1000) -> Scopes(300) retained +2.70 MB indefinitely | After 32 consecutive below-half requests, switch to 2x |

The final r10 policy took tracked medium excess 2,765,256 B -> zero in `TestMediumCompileScratchReturnsToBaseline`; r11 unified it as `machine.BufferHistory`. Retention bounds became relative: max(4096, baseline/64). QEMU 386's 6,400/11,200-byte excesses were word-size effects under the former fixed bound.

Poisoning has two independent traps: missing buffers remain untested, while poisoning a reused old array lets stale holders see fresh data. Detach-and-zero then hid reuse bugs behind fresh make zeros. The correct lease supplies a fresh poisoned array with the same capacity and history.

Stream regrowth exposed lost demand history. 386 pressure estimated 16,006 instructions but emitted about 124k; trimming discarded the final length before Release observed it. Go's roughly 1.25x growth caused nine grows, about 70 MB allocated and 57 MB copied per lowering. `observeWork` precedes trimming; geometric growth targets remembered demand. Repeated large demand earns 16-lowering credit to retain over-1-MiB buffers within 32 MiB. A one-off big compile retains no more than before; retained buffers release after 16-24 small lowerings.

Keeping pointer buffers cost about 34 us/MiB per GC cycle versus 70-95 us/MiB for fresh allocation per lowering. Historical optimized compile improved 15.6% on 386 (55% less B/op), 5.6% amd64 and 4.7% arm64; baseline was unchanged. loong64 sign-aliases regressed 3-6% where retained 28 MB was scanned while 194 MB was allocated above budget. These results belong to that experiment, not a universal retention tradeoff.

Stream flag history also shrank: only previous zero facts and last opcode/immediate were needed, rather than full states. ARM Stream size fell 1,472 -> 1,208 B, checked over 174,800 flag states and 200 layouts. Alias lists append oldest-first and reverse only for public newest-first Flags results, avoiding O(n^2) copies: 8,192 aliases went 24,600 -> 73 allocations.

## Files

- `backend/machine/work.go`, `backend/machine/`: workspace history, streams and pointer trimming.
- `compile/`: per-compile scratch leasing and retained result transfer.
- [Compile-time baseline](../ARCHITECTURE.md#compile-time-baseline): original allocation measurements and all samples.

## Test Coverage

Use `TestMediumCompileScratchReturnsToBaseline`, `TestSealedProgramRetention`, `TestFlagAliasChainsAppend`, poison builds and alternating big/small workloads. Measure heap per held result directly: B/op and allocs/op missed a +51% many-vars retention regression. P=8 heap-only repeats varied by several MB; a claimed +38 KiB Scopes700/1000 steady excess was noise, and the same gate measured post 9 KB below pre.

## Pitfalls

History is useless if its measured length is discarded before observation. Capped pools cannot serve above-cap workloads. Stale Stream-handle reuse remains a TODO verification item; these history fixes alone do not establish that it is closed.

## Sources

Consolidated from these headings in `.agents/docs/JOURNAL.md`:

- 2026-10-16 — Compile allocations, round 6 and steps 1-3, and the baseline they were measured from
- 2026-10-17 to 2026-10-18 — Review rounds 6-10: scratch retention, poison builds, and validation that lowering trusts
- 2026-10-19 — Rounds 11-13: retries, report ownership, and measurement noise
- 2026-10-23 to 2026-10-24 — Writing FORMS.md found encoder bugs compile never hits; a QEMU hang that was not ours
- 2026-11-04 — Stream regrowth: the history was lost, not just the buffer
