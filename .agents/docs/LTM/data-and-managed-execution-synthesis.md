# Data and Managed Execution Synthesis

## Summary

Safe use of Go data in generated code combines view validity, pointer roots, pin ownership, frame metadata and mapping lifetime. A checked index does not establish a whole-program memory-safety boundary, and correct argument registers do not establish runtime stack safety. This document connects the builder-facing Data contract to execution and final release.

## Included Documents

| Document | Focus |
|----------|-------|
| [data-views-cells-and-finalizers.md](data-views-cells-and-finalizers.md) | Transferable layouts, indexed borrows, narrow memory and program-shared cell pins |
| [managed-execution-and-stack-safety.md](managed-execution-and-stack-safety.md) | Runtime metadata, call checks, frame layout, rooting and Code lifetime |

## Stable Knowledge

- Transferable Data permits reading pointer words kept alive by Go's graph, never storing them. ValueParam copies are read-only; PointerParam targets and slice backing arrays are writable. Cells are read-only views of pinned original storage.
- `Data.Index(index, outOfBounds)` and IndexedAddress emit their failure branch. Indexed borrows and arithmetic aliases are block-local, cannot cross edges or enter Vars, and expire at calls/syscalls.
- `Code.Call`/CallPair are raw escape hatches. `execution.Words` makes pointer inputs escape and retains roots; `Code.Func(fptr any) error` validates signatures and nil aggregate roots. These responsibilities are complementary.
- Finalizer shares belong to the originating program across Units/Templates. Last-Code close runs the list last-first and closes future binding. Func exposure retains its share for process lifetime; Template.Bytes/LinkAt take no share.
- Runtime registration uses version-specific synthetic Go metadata. Bind caps goroutine frames at 640 bytes (240 on 386) and foreign frames at 4,096. The permanent managed-registration budget is 4,096.
- Per-entry stack caps do not bound generated-call chains. Callable CallGo targets must be fixed ordinary Go functions, excluding generated entries and known trampolines.

## Operational Guidance

Trace lifetime from builder input through sealed program, lowered Units, Templates, bound Codes and exposed function values. Cell finalizers identify shared slices by backing-array identity using weak pointers/cleanup; builder copies obtain independent pins. An unbound program needs GC cleanup to release Pinner state. Calling Finalizers.Finalize directly is not a general idempotence guarantee.

For Data changes, identify writability, alignment and invalidation separately. Cell slices capture length and pin nested backing arrays; Go must not mutate elements or replace nested slices during generated reads. Callbacks that replace or grow a parameter slice require a fresh Index. A program-order epoch alone missed loop-carried stale backing pointers, motivating the conservative block-local rule.

For frame changes, exercise unwinding after stack growth, tracing and profiling on a fresh goroutine. ARM managed entry uses gap-free callManaged/callPairManaged and reserves the caller-FP top word; unmanaged AAPCS64 entry retains its gap. amd64 also reserves the top word. 386 Func accepts one eight-byte integer result because its epilogue stores eight bytes. RISC-V/LoongArch save RA below SP before moving SP; the C-thread signal-window decision remains in TODO.md.

Close uses an atomic closed-bit/in-flight-count state, allowing callback close without an RWMutex deadlock. Mapping lifetime differs for Func exposure and managed registration; ordinary Close cannot reclaim those permanent resources. Applications serialize registration with plugin.Open. JIT loops without calls are not preemptible, so hosts must emit execution limits.

## Files

- `builder/data.go`, `builder/data_layout.go`, `builder/cell.go`: borrowing, transferable layouts and captured storage.
- `backend/finalizer.go`, `backend/execution/finalizer.go`, `compile/finalizers.go`: shared release ownership.
- `backend/execution/code.go`, `backend/execution/func.go`, `backend/execution/call_linux_arm64.s`: admission, exposure and trampolines.
- `backend/goregister/`, `compile/backend_*target.go`: runtime metadata and frame contracts.

## Tests

On a supported Linux execution host, focused checks from the repository root include:

```sh
taskset -c 5 timeout 180s go test ./builder -run '^TestCellUnpinsWhenLastCodeCloses$' -count=1
taskset -c 5 timeout 180s go test ./compile -run '^TestLowerSharesProgramFinalizers$' -count=1
```

Use `TestCellUnpinsUnusedProgram`, `TestFinalizersWaitForCallInProgress`, `TestFinalizersKeptByFuncExposure`, `TestIndexedViewLoopBackEdgeRejected` and `TestIndexedViewLoopReindexesAfterCallback` for ownership/invalidation. Architecture-specific stack checks include `TestSecurityFramePointerChainAfterStackGrowth` and `TestFuncResultWidthOn386`; use the E2E matrix on matching targets. Run race and GC/stack-growth checks for lifetime changes.

## Pitfalls

`//go:uintptrescapes` does not propagate through an interface call boundary. Raw uintptr construction can bypass escape/rooting guarantees. A main goroutine's large stack hides NOSPLIT exhaustion. Ordinary call loops missed frame-pointer bugs that tracing revealed. Old Cell/Unpin and generic execution.Func APIs are historical, not alternate supported paths. Use the standalone hardening document when changing bounds speculation defenses.
