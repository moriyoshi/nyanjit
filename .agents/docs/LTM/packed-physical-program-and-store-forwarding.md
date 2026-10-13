# Packed Physical Program and Store Forwarding

## Summary

The optimized Unit stores a pointer-free physical program with side tables, reducing durable output size. The successful representation decodes in place and in batches to avoid store-to-load forwarding stalls introduced by narrow-field stores followed by wide struct copies.

## Key Facts

- Packed instructions are 64 bytes versus 136-byte located instructions.
- Data, Symbol, Table and CallAreaBytes use side tables; PC and Slot retain full width.
- Unwind generation reads frame, return and last instructions rather than decoding the whole stream.
- Whole-instruction passes decode batches of 16 on the stack; narrow queries read packed fields directly.
- The located output is owned by the Unit and cannot be returned to compile scratch.

## Details

The initial compact change lowered memory per optimized compile 10-26% but slowed compile time 1.8% on ppc64le through 9.2% on 386. A three-way benchmark (before / small fixes only / everything) isolated this: the loong64 assembler improvement of 11.6% would otherwise have hidden its compact regression.

386 many-vars put `(*physicalCode).at` at 15% of CPU. Its consumers included FP clobber reporting, per-instruction spill scanning and implicit-write reports; some wrapped each instruction in a one-element slice. Narrow stores built a struct that was then returned/copied as a 136-byte value using wide loads, defeating store forwarding. Both packInsn and at, and their callee argument copies, repeated the pattern.

The rework changed movement rather than pass order:

- pack writes fields directly to the reserved packed slot.
- decode writes into caller-provided instruction storage.
- Passes needing whole instructions decode 16 per stack batch.
- The FP report constructs only the seven FloatWrites cache-key fields.
- Spill scanning reads op, bits and slot directly.

Every pass still observes instructions at the same stage. Against the tree without compact storage, optimized compile improved:

| Target | Change |
|---|---|
| 386 | -5.9% |
| amd64 | -3.0% |
| arm64 | -2.4% |
| riscv64 | -1.5% |
| ppc64le | -1.3% |
| loong64 | -0.5% |

386 many-vars fell 10.1 -> 7.4 ms. The work-summary comparison reports memory down 18-38% against 537ae6f; that baseline differs from the initial compact-only 10-26% measurement. The period used BenchmarkZZW8/jitbench on one Cortex-X925 core, with core-5/GOMAXPROCS=1 alternating comparisons where specified; the journal does not give a separate round count for every compact comparison.

A separate vector pool regression arose because sync.Pool outputs were capped at 4 MiB, while vector workloads exceeded the cap. An exact-size result copy added cost without any reuse (+7-20% both tiers). Unpoolable outputs now transfer directly. The loong64 pooled-size-query test also had the wrong bound: large constants still box listing arguments, so the actual guarantee is fewer allocations than a fresh assembler, not less than one per query.

## Files

- `compile/physical_code.go`, `compile/physical_code_test.go`: representation and decoding.
- `compile/report_fp.go`, `compile/report_special.go`, `compile/report_writes.go`: reports and field access.
- `compile/vector_legalize.go`: capped output buffer transfer.

## Test Coverage

Representation tests and deterministic six-target/two-tier snapshots cover decoding and identical listings/reports. `TestFloatWriteKeyCoversInputs` pins narrow FP query inputs. Native gates, poison/retention/race checks and the QEMU matrix passed after the initially unverified work. Performance required the separate three-way benchmark; equivalent output did not reveal regressions.

## Pitfalls

Do not infer that fewer fields or bytes necessarily means faster execution. Inspect generated loads/stores and copies between representations. The proposed 56-byte form using 32-bit inline PC/Slot with wide side entries remains a TODO, not the implemented format. Control fragments still use located bodies.

## Sources

Consolidated from these headings in `.agents/docs/JOURNAL.md`:

- 2026-11-05 — The located copy is the Unit's output, and what could be removed instead
- 2026-11-05 — Unverified fixes, and the benchmark that caught two regressions
- 2026-11-05 — Store forwarding, not field count, made the compact form slow
- 2026-11-05 — Work summary: 386 parity and compile cost, 2026-10-30 to 2026-11-05
