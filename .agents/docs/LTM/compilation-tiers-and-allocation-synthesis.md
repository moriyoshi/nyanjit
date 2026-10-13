# Compilation Tiers and Allocation Synthesis

## Summary

Both tiers consume one scope-and-Var Builder dialect, with different choices about compilation cost and promotion. Physical allocation, FP/SIMD bank ownership and branch/literal layout connect the front-end lifetime rules to correct machine code. This orientation keeps the direct compile path distinct from the reusable backend allocator protocol.

## Included Documents

| Document | Focus |
|----------|-------|
| [dual-tier-design-and-baseline-correctness.md](dual-tier-design-and-baseline-correctness.md) | Shared dialect, baseline liveness, optimized promotion and parity failures |
| [scopes-and-register-allocation.md](scopes-and-register-allocation.md) | Deferred materialization, homes, pairs, residency and allocator protocols |
| [floating-point-and-literal-layout.md](floating-point-and-literal-layout.md) | FP residency, width-sensitive bank copies, literal relocation and fixed-point layout |
| [simd-lowering-and-expansion-budgets.md](simd-lowering-and-expansion-budgets.md) | Shared FP/vector banks, full-width preservation and bounded legalization |

## Stable Knowledge

- Branch/Jump/Bind carry no explicit Builder label arguments; path-dependent values use Vars. Baseline records the function compactly and computes liveness once. Optimized lowering promotes Vars to SSA with Braun's algorithm.
- The source notes describe the original fixed-home, non-SSA Var implementation as well as its successor. These are different design stages, not conflicting current requirements. Read `docs/dual-tier-design.md` for the current tier contract.
- Deferred values materialize when an escaping expression's inputs will not survive. Calls use conservative ABI clobber bounds; address-exposed homes reload after effects.
- Physical aliases overlap. 386 register pairs require whole-group ownership even when an operation materializes only low words. Parameter register lookup is fallible across register/stack ABI boundaries.
- FP/SIMD share a bank. Bank-copy caches include consuming width; hints must match banks. Reserve Go's XMM15, exclude partially preserved ARM V8-V15, and spill live vectors in full across calls/syscalls.
- Literal growth and branch growth reach one joint monotone fixed point. ARM compacts once, then alternates promotions; `machine.RelaxBranches` itself only shrinks branches.
- Bound legalized expansion at 64 emitted instructions per source IR instruction plus 131,072 instructions of fixed slack (64n + 131,072), independent of the source IR length limit.

## Operational Guidance

Decide whether a change concerns Builder semantics, promotion, allocation policy, target selection or layout, then verify its boundary consumers. The public backend offers Residency, destination-first Mover, Online, Batch and an abstract verifier; it does not imply compile must route every instruction through that protocol. The experimental compile port exceeded its +5% gate at +5..+14% and was stopped by the user.

Preserve entry labels after prologue emission: a loop targeting the entry must not jump into or bypass the prologue. Check occupant bookkeeping with swaps, preserved GPR saves under C entry/StackNoUse, 386 narrow arithmetic before predicates and implicit X16 writes from ARM VMaskBits.

FP optimizations need bit-level oracles as well as listings. `OpFProductError` retains its documented domain; bank conversions preserve bits and use the consumer width. 386 literals require `Reloc386Literal` patched per mapping, and raw Bytes returns `ErrCodeBaseRequired`. ARM far literals use ADR/MOVZ/MOVK/ADD-SXTW/LDR; beyond 32-bit reach is a backend limit. Scalar literal packing and FP branch fusion have distinct code/runtime measurements in the source notes.

Treat allocation quality separately from algorithm selection. The balance study found allocation near 4.5% of compile CPU; moving live-across-call values into free preserved registers reduced call spills 2,000 -> 2. A creation-order schedule improved chain but caused about 4,000 pressure spills, so it was rejected. Remaining scheduling work is in TODO.md.

## Files

- `docs/dual-tier-design.md` and [compiler architecture](../ARCHITECTURE.md#compilation-tiers): public tier usage and internal home/promotion rationale.
- `backend/regalloc/`, `compile/allocation_online.go`, `compile/variable_codegen.go`: allocator interfaces, adapters and homes.
- `compile/baseline_*target.go`, `compile/literals.go`, `compile/vector_legalize.go`: baseline forms, literal layout and expansion.
- `examples/stackvm/`: shared online/batch/IR workloads and reference interpreter.

## Tests

Focused checks from the repository root:

```sh
taskset -c 5 timeout 180s go test ./compile -run '^TestBaselineEntryBlockLoop$' -count=1
taskset -c 5 timeout 180s go -C backend test ./regalloc -run '^TestBatchSlotsFitWideValues$' -count=1
```

Also use both-tier irconform, varfuzz with pair edges and Var swaps, ABI clobber checks and snapshot listings/reports. `TestSIMDImmediateShiftDifferential` covers all counts 0-255; `TestCarrylessMulExpansionIsBudgeted` guards fallback work. Architecture-specific FP residency and far-literal tests need matching execution or QEMU. A CPU that lacks hardened control flow can legitimately skip that configuration.

## Pitfalls

The original Var home counts do not describe the later SSA promotion pipeline. Shared mutable HomePlan storage invalidates pointer-keyed caches. Fixed two-word edge temporaries cannot hold wider vector storage. A Go host can be an incorrect conversion oracle; consult the standalone operation/oracle document. Do not compare baseline/optimized performance without naming target and whether IR construction is included.
