# Architecture and API Decisions

## Summary

NyanJIT is a pure-Go native emission and execution toolkit with small architecture-specific assembly trampolines. Its backend components are public tools for other front ends; compiling arbitrary Go is outside the original scope.

## Key Facts

- No `internal/` packages and no new exported identifiers or changed exported signatures without user approval.
- The project is unpublished: delete replaced API outright, with no compatibility shims or deprecations.
- Replacing an entry point still requires generated-code identity and golden-output equivalence where behavior is intended to stay the same.
- Width belongs to virtual values; physical aliases and extension behavior belong to targets.
- `compile.Target` is shared and reentrant; it is not named `Compiler` because an emitter consumes it.
- Sealed-program accessors lend storage under a must-not-modify contract.

## Details

The pipeline separates Builder recording, program sealing, selection, allocation, physical emission and byte/text assembly. Early permanent register assignment limited temporaries to eight on arm64 and three on amd64; deferred expressions and register groups replaced that model. AL/AX/EAX/RAX overlap storage, and 386 64-bit values use ordered pairs rather than independent registers or a separate public `Value128` abstraction.

The first execution spike used writable mappings, instruction-cache flushing and an executable transition. That established native execution, not GC or stack safety. Runtime integration is a separate contract described in [managed execution](managed-execution-and-stack-safety.md).

The toolkit lives in the nested `backend/` module, with root `go.work`; `machine`, `regalloc`, `abi` and the targets are public. `machine.SelectedInstruction` belongs with the allocator's input. Gateways are private to `compile`, rather than a public `bridge` package. Targets provide `GoFrame(...) (goregister.Frame, error)` and documented encoding forms.

The final API consolidation chose:

- `compile.NewTarget(compile.TargetSpec{...})` returning the sealed `compile.Target` interface, consumed by public `Emitter`.
- Read-only `abi.Report` getters: `abi.ReportOf` transfers ownership and `abi.NewReport` copies. Historical `compile.CodeTemplate` and `execution.Template` separation was superseded by their merger.
- Zero-copy borrowed `SealedProgram` getters. A copy-out alternative cost 2-9% of Prepare; the user explicitly reconfirmed no deep copy. `ProgramBuilder.Seal` still clones instruction-owned Args, Data and Effect.
- `ContextExternal` for code bound outside the process; ordinary `Bind` returns `ErrExternalCallOnly`.
- A real page-granular `execution.Arena` and an argument-count `LimitError` kind.

Earlier policy briefly preserved field prefixes and migration compatibility. The subsequent unpublished-project instruction supersedes that policy, even though the already-completed prefix restoration was retained. Four hook-registry packages introduced during allocation work were removed: `internal/machinework` became `machine.Workspace`, `internal/cpuaccess` became `abi.ResolvedCPU`, template ownership used a normal copying constructor, and register names became private tables. Older helpers moved to `instrument/` or their consumers.

Public register sets carry their target's name table; raw public bitvectors with target-dependent bit meanings were rejected. `builder.Scalar`, `Vec` and `Mask` are functions, not mutable exported function variables. Clone/report entry points preserve nil versus empty slices.

## Files

- `AGENTS.md`: standing API policy.
- `backend/README.md`, `backend/abi/`, `backend/machine/`, `backend/regalloc/`: public toolkit contracts.
- `compile/target.go`, `docs/sealed-programs.md`: target construction and borrowed program storage.

## Test Coverage

`TestReportSliceShapesAreConsistent` covers 11 clone/report entry points. `TestProgramBuilderSealCopiesData` checks sealing ownership. `TestDenseAndUncachedCompactHomePlansMatch` compares dense, cached-compact and uncached-compact home plans on six targets; it caught a fallback still reading only `Regions`.

## Pitfalls

Historical entry points (`CompileForeign`, `CompileGo`, explicit block arguments and old report types) describe earlier trees. Use current README and Godoc for signatures. An extra code-byte copy was accepted over a public ownership constructor that allowed caller mutation of an immutable template. Specifically, `execution.NewOwnedTemplate` reduced tiny Prepare to 34 allocations but was deleted for that ownership violation; packed clones subsequently reached 25 allocations without the unsafe constructor.

## Sources

Consolidated from these headings in `.agents/docs/JOURNAL.md`:

- 2026-10-08 — First spike and the execution boundary
- 2026-10-10 — Widths, register groups and package boundaries
- 2026-10-16 — Standing API rules: everything public, nothing kept for compatibility
- 2026-10-17 — Two API policies in one night, and where internal/ went
- 2026-10-17 to 2026-10-18 — Review rounds 6-10: scratch retention, poison builds, and validation that lowering trusts
- 2026-10-20 — API consolidation, and where the user overrode the written plan
- 2026-10-26 — Allocations down 42–99%, and a frozen corpus that had stopped meaning anything
