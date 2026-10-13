# Scopes and Register Allocation

## Summary

Scopes express lexical live-out boundaries, while allocation manages deferred expressions, fixed operands and physical register groups. The public allocator protocol supports other front ends without requiring compile to abandon its faster direct path.

## Key Facts

- A deferred value escaping a scope must materialize if its inputs do not survive.
- Call effects bound an unknown callee's clobbers; they differ from the generated body's report.
- Address-exposed homes must reload after calls that can mutate them.
- Pair ownership, pinning, eviction and spill scheduling must account for every word.
- Historical direct Var lowering used fixed homes; the optimized dual-tier design later promotes Vars to SSA.

## Details

`AddScaled` can fold into memory addressing, form an address or materialize at its use. Fixed-register hints and parallel moves avoid unconditional call staging; the early `elfwrite` listings became frameless 28-byte functions on amd64 and arm64 (instruction observations, not timing results).

Vars require initial values in their scope's entry sequence before any branch. The original implementation deliberately avoided SSA: Get borrowed a home, Set updated it, captures were immutable outer Vars, and scope lookahead reserved room for ordinary temporaries. Slots were reserved without stores; preserved registers became available under pressure; argument-less jumps dropped bookkeeping results. The later optimized tier uses Braun promotion, while Vars remain the Builder's path-dependent-value interface.

Add/subtract of zero lowers as a copy. Parameter lookup is fallible: `ParamRegister` returns `(Register, bool)` rather than indexing an ABI table unconditionally. amd64 parameters seven and eight with a scoped Var previously panicked against a six-register table; the RISC-V Go bank also initially listed only nine of its sixteen registers. Test both register-backed and stack-backed ABI parameter boundaries.

Historical whole-function instruction counts:

| Workload | amd64 | arm64 | riscv64 | ppc64le |
|---|---|---|---|---|
| sum | 29 -> 10 | 30 -> 10 | 28 -> 10 | 33 -> 11 |
| pressure4 | 64 -> 35 | 54 -> 27 | 53 -> 28 | 70 -> 29 |
| idctvar | 453 -> 361 | 376 -> 289 | 506 -> 398 | 616 -> 514 |

386 sum fell 52 -> 42; the four 64-bit loops became frameless. Fixed storage includes every declared Var word, even register homes and disjoint sibling scopes. A 4,200-deep arm64 case needed 4,202 words against a 4,095-word limit, motivating required/reserved/available diagnostics.

The whole-function allocator uses linear scan with splitting and block parameters as phis, without SSA conversion. It replays results through `Mover`, rather than exposing a plan. Loop-entry pressure trimming survived evaluation; next-use weighting merely moved reloads to back edges and was removed.

The unified protocol names residency as `regalloc.Residency`, uses destination-first `Mover` operations (Move, Spill, Reload, LoadIncoming, Rematerialize), value-type `Pins`, and `Program`/`Function` interfaces with `FunctionBuilder`. `Allocator` provides Allocate, Entry, Before, Operand, After, Edge, Slots, Report and Finish. `Batch` and stateful `Online` implement it; `Verify` checks walks by abstract interpretation. Constraints include scope trees, `Operand.Memory`, `Operand.Prefer`, `Function.Clobbers` and an entry-slot location in `abi.Frame`.

The experimental compile port behind `NYANJIT_INTERNAL_ONLINE_ALLOCATION=1` progressed through straight-line, calls/homes/386 and whole control programs. Its +5..+14% cost exceeded the +5% gate, and the user stopped it. Online description/walking cost about 40 ns per position against a needed 20. This is a recorded stopping decision, not authorization to resume the port.

The balance study measured allocation at about 4.5% of compile CPU and no loop spills on 64-bit targets. Policy changes offered callee-saved registers last and moved live-across-call values into free preserved registers (2,000 -> 2 call spills). Adding EBX/ESI/EDI initially failed ABI checks; later entry saves and clean eviction made it viable. Creation-order scheduling improved chain (170 -> 2 spills) but damaged pressure (0 -> about 4,000); the remaining investigation is in TODO.md.

## Files

- `backend/regalloc/`: residency, online/batch allocation and verifier.
- `compile/allocation_online.go`, `compile/variable_codegen.go`: compile adapters and homes.
- [Scope architecture](../ARCHITECTURE.md#scopes-and-variable-homes), `docs/dual-tier-design.md`: implementation of homes and public tier usage.
- `examples/stackvm/`: shared bytecode/interpreter/workloads for backend online, batch and IR paths; unsigned division returns zero on zero divisor.

## Test Coverage

Six-target 2,000-case sweeps accompanied the protocol port. `irconform` caught a 386 pair-edge bug omitted by its sweep; listing diffs/QEMU caught a RISC exit returning register 0. `abicheck` caught unsaved preserved-register use. Wide-slot tests are described in [validation](validation-and-encoder-contracts.md).

## Pitfalls

A cache keyed by a `HomePlan` pointer is invalid when storage is reused across lowerings. `allocateAndEmitMode` only inlined callees costing 20 or less in the measured Go build; crossing that boundary cost about 5%. Check compiler diagnostics with `-gcflags=-m=2` rather than assuming an accessor is free.

## Sources

Consolidated from these headings in `.agents/docs/JOURNAL.md`:

- 2026-10-08 — Deferred values, scopes and call effects
- 2026-10-10 — Widths, register groups and package boundaries
- 2026-10-14 — Scoped Vars, and a green matrix that proved nothing
- 2026-10-17 to 2026-10-18 — Review rounds 6-10: scratch retention, poison builds, and validation that lowering trusts
- 2026-10-24 — A whole-function allocator beside the online one, not instead of it
- 2026-10-26 — Allocations down 42–99%, and a frozen corpus that had stopped meaning anything
- 2026-10-27 — A register allocator protocol, and porting compile onto it
- 2026-11-01 — Scaled multiplication and a shared stack VM
