# Data Views, Cells and Finalizers

## Summary

`Data` exposes transferable Go layouts through checked builder operations while keeping pointer storage and lifetime explicit. Cells reuse the same view shape and pin captured memory until the last derived Code releases its program share.

## Key Facts

- ValueParam copies are read-only; PointerParam targets and slice backing arrays are writable.
- Generated code may read pointer words kept alive by Go's graph but never store pointer words, avoiding write barriers.
- Indexing emits its own failure branch; indexed borrows are block-local and expire at calls/syscalls.
- `ValueCell`/`SliceCell` replace historical Cell/NewCell/Unpin API outright.
- Finalizers run last-first when the final derived Code closes; later binding returns `ErrClosed`.

## Details

The aggregate design chose one `Data` type over `ValueParam[T]`, `PointerParam[T]` and `SliceParam[E]`, rather than separate StructParam/StructPointerParam abstractions. Generic constructors are package functions because Go has no generic methods. Raw pointers, strings and complex values are excluded from transferable layouts. Raw pointers cannot support boxed-copy addressing or bounded lengths. The compiler/IR/ABI did not change for the original view layer.

F11 found that returning an element plus an advisory bounds predicate allowed the element to load before any branch. `Data.Index(index, outOfBounds)` and `Builder.IndexedAddress(...)` emit the failure edge themselves. A linear epoch still allowed loop-carried elements to survive a reallocating callback (V2). The chosen fix is conservative block-local borrowing: indexed views/addresses and arithmetic aliases cannot cross edges or enter Vars. A new Index is required after a call or in another block, avoiding a Finish-time CFG data-flow analysis.

Callback replacement or growing append can invalidate an old slice backing array. Recheck lengths and reindex after effects. Raw addresses and raw Call retain caller-owned contracts; `Words` and `Func` provide the rooting/type checks described in [managed execution](managed-execution-and-stack-safety.md).

Native narrow memory has separate `OpLoad16`, `OpLoad32`, `OpStore16` and `OpStore32` opcodes. Memory width must not be confused with register width. Public loads allow arbitrary alignment and return Scalar(16)/Scalar(32), requiring explicit casts. Go-layout Data accesses alone set the `ir.Instruction.Aligned` promise. amd64/386/arm64 use native operations; riscv64/loong64/ppc64le use bytes unless known aligned, with no assumption of Zicclsm, UAL or alignment-interrupt emulation.

Historical uint32 field-read counts:

| Target | Before | After |
|---|---|---|
| amd64 | 21 | 2 |
| 386 | 104 | 20 |
| arm64 | 13 | 3 |
| other targets | 16-17 | 3 |

Narrow-memory allocation failure retries once from validated source with byte expansion: `ErrStackRequired`, `ErrBackendLimit` on 386, or an oversized managed frame. Successful first plans never retry. Offset arithmetic must widen before adding byte offsets; the first retry wrapped 32-bit addresses at 4 GiB on 64-bit targets. Partial 386 groups materialize only needed low words while preserving live high words and whole-group ownership. Discarded loads still perform their access.

The managed-frame retry intentionally changed nine historical listings, including ppc64le. The initial report omitted the POWER changes; retry verification must inspect every target rather than assuming the affected set is only 386.

The final cell API uses builder generic `ValueCell[T](b, v *T)` and `SliceCell[E](b, s []E)` returning Data plus error, with root wrappers. These views are read-only: ValueCell reads the original pointer-free value rather than copying it, and SliceCell captures the slice length and pins backing arrays including nested slices. Go must not mutate values/elements or replace nested slices while generated code reads them. An empty slice has no valid index or storage to pin. Historical caller-kept slices and explicit Unpin were superseded. `Finalizer` means the interface with `Finalize()`, not a Go GC finalizer. `Finalizers` is a named slice, finalized last-first once per entry during release; calling its method directly is not an idempotence guarantee.

All Templates/Units derived from one program share reference-counted finalizers. Execution identifies a list by its backing array, using a weak pointer to `&fs[0]` and `runtime.AddCleanup` on a registry. A GC token releases pins for a program never bound; otherwise `runtime.Pinner` can panic when collected pinned. Builder copies acquire independent pins through `detachCells`. Template.Bytes/LinkAt take no share, so their callers keep the program alive. Codes exposed through Func retain shares for process lifetime.

## Files

- `builder/data.go`, `builder/data_layout.go`, `builder/cell.go`: Data validity, layouts and cells.
- `backend/execution/finalizer.go`, `backend/finalizer.go`, `compile/finalizers.go`: registry, binding and shared finalizer types.
- `compile/emitter_control.go`, `docs/backend-parity.md`: narrow-memory retry.

## Test Coverage

`TestDataLayoutTransferabilityAgreement` compares builder/execution acceptance across 44 types; it caught cached errors naming the wrong recursive field. An independent narrow-memory decoder checked 17,664 encodings. Bounds regressions include `TestCheckedIndexRequiresFailureEdge`, `TestIndexedDataExpiresAtCall`, `TestIndexedAddressBranchesBeforeLoad`, `TestIndexedViewLoopBackEdgeRejected` and `TestIndexedViewLoopReindexesAfterCallback`. Cell/finalizer anchors include `TestCellUnpinsWhenLastCodeCloses`, `TestCellUnpinsUnusedProgram`, `TestFinalizersRunLastFirstOnce`, `TestFinalizersRunWhenLastCodeCloses`, `TestFinalizersWaitForCallInProgress`, `TestFinalizersKeptByFuncExposure`, `TestFinalizersBindAll`, `TestFinalizersConcurrentClose` and `TestLowerSharesProgramFinalizers`.

## Pitfalls

Do not infer the current lifetime contract from the historical generic Cell API. Finalizer lists passed by value can still share a backing array. A test of one Template alone does not establish sharing across multiple Units from the same program.

## Sources

Consolidated from these headings in `.agents/docs/JOURNAL.md`:

- 2026-10-18 — An either/or question hid the option the user wanted
- 2026-10-18 — First security audit: frames, encodings and the call boundary
- 2026-10-18 — Data views for Go values, and native 16/32-bit memory
- 2026-10-19 — Rounds 11-13: retries, report ownership, and measurement noise
- 2026-11-02 — Cell's changing lifetime contract
- 2026-11-04 — Cells take the shape of Data, and finalizers are refcounted by program
