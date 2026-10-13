# Floating Point and Literal Layout

## Summary

FP lowering preserves semantic operations until selection, keeps results in FP registers and minimizes bit-preserving bank crossings. Literal encoding, relocation and branch layout are coupled correctness constraints.

## Key Facts

- `ir.OpFProductError(A,B,C)` computes FMA(A,B,-C) when permitted, otherwise separately rounded Dekker steps, under its documented domain.
- Cached bank copies are keyed by value ID and consuming width; hinted registers must match the physical bank.
- XMM15 is Go's zero register; ARM V8-V15 are excluded because partial preservation is insufficient.
- Scalar 4/8-byte literals became naturally aligned after initial 16-byte pool alignment.
- ARM literal promotion and branch promotion must reach a joint monotone fixed point.

## Details

The original FP interface used IEEE bit patterns and an integer ABI; native float passing later arrived as `abi.FloatPassing`. Deferred exact-product expansion lets one Program compile for fused and nonfused profiles. FP residency uses volatile registers in the bank shared with SIMD and crosses to GPRs at integer consumers, ABI edges and predicates. A ten-operation chain needs two bank transfers.

RISC-V sign-injection instructions decode as `fmv`, `fneg` and `fabs` in listings, preserving the semantic aliases when inspecting FP moves and unary operations.

Historical binary64 instruction counts before -> after FP residency:

| Target | Pow | Sin | Exp | Log | Floor |
|---|---|---|---|---|---|
| amd64 | 1214 -> 683 | 444 -> 328 | 172 -> 102 | 205 -> 120 | 45 -> 44 |
| arm64 | 1193 -> 429 | 412 -> 249 | 183 -> 87 | 211 -> 100 | 31 -> 31 |
| riscv64 | 1549 -> 650 | 585 -> 357 | 262 -> 134 | 309 -> 159 | 64 -> 64 |
| ppc64le | 1518 -> 821 | 540 -> 445 | 238 -> 171 | 291 -> 214 | 65 -> 65 |

Earlier math expansion reduced FSin 826 -> 444 / 704 -> 412 / 987 -> 585 and FFloor 85 -> 45 / 63 -> 31 / 108 -> 64 on amd64/arm64/riscv64. Budgets use measured per-width counts plus about 10%, with 256-byte workspace. ARM64 Trunc32 uses 28 for 25 instructions rather than a flat 20 allowance.

Pools deduplicate read-only literals after the final return and seal them RX. POWER8 acquires PC with six LR-preserving instructions; managed literal leaves require an unwindable saved LR. POWER8 XSCVDPSPN defines word 0 only (word 1 also on ISA 3.0C), so extraction inserts `xxsldwi` before MFVSRWZ. Binary32 Pow/Sin/Exp/Log/Floor counts became 834/453/180/222/73 from 833/452/179/221/72.

ARM literal LDR has a signed 19-bit word offset. Far loads use ADR/MOVZ/MOVK/ADD-SXTW/LDR at any mapping base; beyond 32-bit reach is `ErrBackendLimit`. Layout compacts once, then alternates literal growth and short-branch growth until stable. `machine.RelaxBranches` itself only shrinks branches. The regression puts the pool 16 bytes inside LDR reach before TBZ growth pushes it out.

386 P1 used SSE2 with GPR words/pairs; P2 supplied inline math; P3 gave FP Vars/label parameters XMM3-XMM7 homes (XMM0-XMM2 scratch). FP32 uses one 4-byte slot, FP64 two; cycles use XMM scratch rather than truncating GPR moves. P4 adds `Reloc386Literal`: `Template.Bind` patches a fresh mapping copy; raw `Bytes` returns `ErrCodeBaseRequired`.

| 386 workload | P2 | P3 | P4 |
|---|---|---|---|
| Ten-add FP64 chain | 114 instructions, 60 movd | 31, 4 | 31, 4 |
| FP64 Var recurrence | 115, 24 | 69, 8 | 61, 4 |
| IDCT idctvar/idctargs | not recorded | 1687/2070 | 1556/1943 |

Scalar literal packing later reduced FP code 24% on arm64 and 21% on amd64 with only 0-1% compile-time change near noise. No packed-SSE literal operand required 16-byte alignment in the reviewed tree. FP branch fusion consumes FCMP flags (arm64 suppresses CSET with WZR destination 31) or UCOMIS flags on amd64. ARM FP code fell 256 -> 240 bytes, runtime reportedly improved 3.3%, and the measured Cranelift gap narrowed 6.7% -> 3.2%. Chain kernels compiled 13-15% faster and generated about 20% smaller code; native arm64 chain1000 ran 20% faster and Collatz 8.7% faster in that round. These are separate historical comparisons, not multiplicative gains.

## Files

- [FP architecture](../ARCHITECTURE.md#floating-point-lowering), `compile/math_budget_test.go`: expansion design and budgets.
- `compile/literals.go`, `backend/target/arm64/`, `backend/target/i386/`: layout and encoding.
- [Historical FP phases](../ARCHITECTURE.md#386-fp-phases) and [386 validation record](../ARCHITECTURE.md#386-validation-record): 386 FP evidence.

## Test Coverage

`Test386FloatWorkspaceSweep` ran 12,000 executions without limits. All 56 baseline math digests matched amd64 over 145 inputs each. The mixed 3,000-case sweep improved from 29,461 checks/18,860 limits to 38,555/9,766; limits were paired scratch needs, not SSE2 failures. A 200,000-leaf ARM literal regression ran in two mappings. FP fusion checks include NaNs and hardening.

## Pitfalls

Do not hide `OpFProductError`'s domain behind Builder provenance: Programs are public. Counts are not timing results. One POWER base per function remains in TODO.md because it requires allocation, call survival and LR scheduling together.

## Sources

Consolidated from these headings in `.agents/docs/JOURNAL.md`:

- 2026-10-14 — Backend parity with PPC64, and CPU profiles
- 2026-10-14 — FP lowering: exact products, literal pools, FP residency
- 2026-10-15 to 2026-10-16 — 386 floating point (P0-P4) and native SSE2 SIMD (P5)
- 2026-11-01 — Optimized-tier takeover: FP branches and literal packing
- 2026-11-04 — Reviewing Codex's amends
