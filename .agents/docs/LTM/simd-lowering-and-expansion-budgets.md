# SIMD Lowering and Expansion Budgets

## Summary

Portable vector shapes are legalized into native SIMD, split shapes or scalar fallbacks per operation. Register sharing, full-width spills and bounded expansion are essential even when the selected scalar operation looks small.

## Key Facts

- Logical vector widths use `uint16`; spill homes are aligned for the whole vector.
- Native vectors spill across C/Go calls and syscalls.
- FP and SIMD share a bank: 386 homes use XMM3-XMM7 with XMM0-XMM2 scratch.
- LoongArch emits 128-bit LSX; recognized LASX is not emitted.
- Legalization is capped at 64 instructions per source IR instruction plus 131,072.

## Details

Phase 1 supplied 128-bit SSE2/NEON integer operations, comparisons/equality masks, `MaskBits`, lane access and immutable constants, plus splitting and per-operation scalar fallback on other targets. Review explicitly covered Go's XMM15 zero register, extreme lanes (zero, all ones, INT_MIN and `0x80 * 0x80`), overlapping buffers and reloads under pressure.

Wider pressure exposed 386's three-GPR workspace limit. Paired binary operations use complete spill homes and process one word at a time while retaining low-word carry across reloads. Native SSE2 spills use MOVDQU for 16-byte vectors. SHA-256 uses `Vec(4,32)` for four compression streams; 386 instruction counts fell 48,001 scalar -> 6,777 SSE2, with no timing implied.

Native 32-bit lane shifts/rotations and optional PCLMULQDQ carryless multiplication extended phase 1. ARM PMULL/PMULL2 avoids the roughly 500-instruction scalar product fallback: same-half selectors use PMULL/PMULL2; mixed selectors use EXT into V31 plus PMULL. `VectorsRequire("pmull")` implies NEON, and HWCAP discovers the extension.

F19 demonstrated why source IR limits are insufficient: 16,000 scalar-fallback `CarrylessMul` operations took 16.4 s and allocated 20 GB; an audit run reported 8.7 GB RSS. The legalization cap returns `ErrBackendLimit`. These are different resource measurements, not interchangeable numbers.

386 native `OpVMaskBits` results wider than 32 bits originally failed allocation; PMOVMSKB now produces 32 bits and zero-extends. ARM `OpVMaskBits` implicitly writes X16 and must report that write. LoongArch LP64D preserves only low 64-bit FP halves, so vr24-vr31 are unsuitable for keeping vectors live across calls.

The vector legalizer's 4 MiB pool cap matters for performance: above-cap outputs must transfer directly to the program instead of paying an exact-size copy for a buffer that cannot return to the pool. See [packed program and pooling](packed-physical-program-and-store-forwarding.md). Remaining capacity-planning work belongs in TODO.md.

## Files

- `docs/simd-design.md`: shapes, lowering, SHA-256 and acceptance evidence.
- `compile/vector_legalize.go`: fallback expansion, budget and output ownership.
- `backend/target/arm64/`, `backend/target/i386/`, `backend/target/loong64/`: native encodings.

## Test Coverage

Phase 1 acceptance recorded 330 matrix checks: 85 amd64, 85 arm64, 77 386 and 83 riscv64 across Go 1.26.9/1.27.2. `TestSIMDImmediateShiftDifferential` batches counts 0-255 with SIMD enabled/disabled. `TestCarrylessMulExpansionIsBudgeted`, `TestPMULLCarrylessMulWords`, `TestCarrylessMulDifferential` and `TestVectorMaskBitsWideResult` cover the budget and later fixes.

## Pitfalls

Vector storage may occupy more words than its register representation. F21 showed that single-slot reuse and fixed two-slot edge temporaries can place wide vectors outside a frame. Full-width storage and callee preservation must be checked independently of scalar register ownership.

## Sources

Consolidated from these headings in `.agents/docs/JOURNAL.md`:

- 2026-10-14 — Portable SIMD, phase 1
- 2026-10-15 to 2026-10-16 — 386 floating point (P0-P4) and native SSE2 SIMD (P5)
- 2026-10-16 — LoongArch port (L0-L4), and a GC failure traced to the emulator build
- 2026-10-25 — Second audit: the Medium and Low findings, and how the audit ran
- 2026-10-30 — Eleven sljit-gap items in one wave, and what seven parallel merges taught
- 2026-11-05 — Unverified fixes, and the benchmark that caught two regressions
