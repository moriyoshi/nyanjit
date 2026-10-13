# NyanJIT backend toolkit

`github.com/moriyoshi/nyanjit/backend` holds NyanJIT's lower layers as a
separate Go module. A project with its own IR (a bytecode VM, a wasm or DSL
compiler, a regex engine) can use them without NyanJIT's IR, Builder or
`compile`.

The six Linux targets are amd64, arm64, 386, riscv64, ppc64le and loong64.
Encoders and listings for these targets are available on any host; executable
mappings and calls require a supported host. Big-endian ppc64 is unsupported.
The modules target Go 1.26; managed Go frames also support Go 1.27, as
documented in the [main README](../README.md#what-is-nyanjit).

| Package | Purpose |
| --- | --- |
| `backend` (this directory) | Shared vocabulary: physical `Register`s and named `RegisterSet`s, value register `Class`es, and the `ErrInvalidFunction` and `ErrBackendLimit` errors. |
| `op` | Operation vocabulary: opcodes, branch conditions, byte orders. |
| `abi` | Calling conventions, call effects, CPU profiles, stack policies, emission reports. |
| `target/amd64` … `target/riscv64` | Per-ISA policies, register files, sizes, assemblers (bytes and text) and Go frame descriptions. |
| `machine` | IR-neutral instruction stream: labels, layout, branch relaxation, literal pools, relocations, generic passes. |
| `regalloc` | Register allocation: the `Allocator` protocol with its `Batch` and `Online` implementations and `Verify`, the client-driven `Residency`, and their primitives. |
| `execution` | Executable memory, `Template`, `Bind`, `Code`, typed Go function views. |
| `goregister` | Go runtime registration of generated frames. |
| `cpuprofile` | Host ISA feature discovery. |

This module does not require the main module
(`github.com/moriyoshi/nyanjit`), and no package here, tests included, imports
`ir`, `builder`, `compile` or the root package. The main module checks both
with `instrument/imports_test.go`. Tests that need compiled IR live in the
main module (for example `compile/goregister_*_test.go`).

Construct exported structs with keyed fields; field order is not part of the
API.

## Writing a front end

[examples/stackvm](../examples/stackvm/main.go) compiles a stack bytecode with
this module alone. A front end does the following:

1. Picks a target and its `Policy`, which gives the register file, parameter
   and result registers, the scratch register and the branch range check.
2. Appends `machine.Instruction` values to a `machine.Stream` sized with the
   target's `PhysicalSize` (ppc64: `Emitter.Size`). Branch operands are label
   indices; `machine.RelaxBranches` and `machine.ResolveBranches` turn them into
   byte PCs.
3. Visits each located instruction with the target's `ByteAssembler` through
   `machine.Visit` (ppc64: expands it with `Emitter.Expand` and visits the
   result), or with a `TextAssembler` for a listing. This works for every
   target on any host.
4. Maps the bytes with `execution.NewCode` and calls them.

Each target's `FORMS.md` lists the `machine.Instruction` forms it accepts,
their operand constraints and encoded sizes; `target/forms_test.go` assembles
every row and checks the sizes. `regalloc.Residency` assigns registers to
the values of a linear instruction sequence as the front end walks it. The
front end describes the sequence as a `regalloc.Program`: its values and the
values each position reads, with live-out values read at a final position.
The Residency emits each move through a `Mover` the front end supplies: `Move`, `Spill`,
`Reload`, `LoadIncoming` and `Rematerialize`, destination first. Both work in
`regalloc.Group`s, the one or two registers holding a value, and a front end
keeps the registers an instruction already uses out of a choice with
`regalloc.Pins`.
`compile` and stackvm both use it; stackvm keeps its stack values in
registers and spills the one used furthest in the future.
`regalloc.Config` names its registers by identity, as `backend.RegisterSet`s.
A target's policy returns sets that carry its register names, `Derive` makes
another set with the same names, and the allocators resolve abi
register names through them. `Registers` lists the allocatable ones in
preference order (vector-file registers hold Vector and Float values), and
`Blocked`, `Scratch` and `Preserved` list subsets. Mark registers whose first
use costs the front end a save, such as a C entry's callee-saved registers, in
`Preserved`: until the Residency has chosen one, it evicts a value that needs
no spill instead.

`regalloc.Allocator` is the protocol for allocating a whole function. The
front end describes it as a `regalloc.Function`, an interface that
`regalloc.FunctionBuilder` implements from slices: values, the operands at
each position (fixed registers, two-address ties, early clobbers, preferred
registers), calls as `abi.CallEffect`s, the entry as an `abi.EntryLayout`,
and blocks whose parameters receive edge arguments. A value may be used in any block its
definition dominates. `Allocate` takes the function and the front end's
`Mover`; the front end then walks the function in layout order, calling
`Entry`, then `Before`, `Operand` and `After` around each position's
instruction, and `Edge` for each of a block's edges right after its last
position, so an allocator sees the registers as the block leaves them. The
edge's moves go at the end of a source with one successor, right after the
branch for a fallthrough, at the start of a destination with one
predecessor, or in a block the front end adds when `EdgeEmpty` is false; a
front end placing them away from the `Edge` call collects them from its
`Mover` and splices them in. `Report` fills an `abi.EmissionReport`'s clobbered
registers. A function can also reserve registers for itself with a tree of
`regalloc.Scope`s, such as homes for variables: each scope is a run of
positions that may start and end inside a block or span several, and the
innermost scope covering a position names the complete set reserved there.
The allocator keeps its values out of them, but for operands fixed to them,
vacating them where the set grows (before a position inside a block, or on
the edge into a block). A position may also overwrite registers besides its
definitions (`Function.Clobbers`, an expansion's scratch registers), and a
use may be read from memory (`Operand.Memory`), where `Allocator.Memory`
reports its frame slots, incoming stack words or constant. Any
implementation can stand in for another:

- `regalloc.Batch` allocates the whole function at once: it computes
  liveness, splits live ranges, keeps values a loop never reads out of
  registers on entry to it, and orders edge moves as parallel moves.
- `regalloc.Online` allocates as the front end walks, for speed, with no
  liveness analysis: a `Residency` within each block, and block parameters
  carried in registers. The first edge resolved into a block fixes where its
  parameters start, in their arguments' registers when it can, and each edge
  is a parallel move into that state. Parameters that do not fit in registers
  share frame slots across blocks. A value used in another block without
  being passed there has a home slot, so front ends that pass cross-block
  values as arguments get the best code.
- `regalloc.Verify` allocates a function with any `Allocator` and checks, over
  every path, that each use finds its value where `Operand` says.

stackvm compiles every program through one emitter with either allocator
(`-alloc online|batch`). Both allocators keep their storage from one
function to the next, as does a `FunctionBuilder` after `Reset`. stackvm's
`Compiler` reuses all three, so a warm compile allocates nothing in
regalloc.

**Go frames.** Generated code that calls Go functions must run in a frame the
Go unwinder can walk. Each target's `GoFrame` builds the `goregister.Frame`
for a laid-out function; set its name, argument bytes and `PointerFree`, and
pass it to `goregister.Register` (through `execution.NewCode`'s registrar).
Go calls must happen inside an open frame.

**C calls from Go-managed code.** `execution.CallForeign` performs the
runtime-aware Go-to-C transition (through purego). Generated code running in
a registered Go frame can call only ordinary Go functions with a fixed
signature, so give it a fixed-arity wrapper and call that:

```go
func callC2(target, a, b uintptr) uintptr {
	return execution.CallForeign(target, []uintptr{a, b})
}

address := reflect.ValueOf(callC2).Pointer()
```

Code running with the C ABI outside a Go frame calls C directly.

## Contracts

**Borrowed results.** Several results borrow the producer's storage. Read them, and copy anything you need afterwards:
- `Stream.Instructions()` is read-only and borrowed until the next stream mutation or layout pass.
- `ResolveBranches` returns read-only locations borrowed until the next `ResolveBranches`, `CompactBranches` or `RelaxBranches` call on that stream.
- Target byte assemblers return read-only `Bytes()` and `Relocations()` valid until their next mutation.
- `machine.SelectedInstruction.InputsInto(i, buf)` reuses a caller-owned buffer but may return the instruction's own read-only `Args`; `Inputs()` returns an independent slice.
- `regalloc.Residency.Uses` and `Locations` are borrowed until the next `Finish`, `Reset`, `Trim`, `TrimToBudget` or `Decay`, whichever comes first.
- A `regalloc.Batch`'s or `regalloc.Online`'s walk results (`Operand`, `Slots`, `Report`) are valid until its next `Allocate` or `Finish`, and a `Batch`'s also until its next `Trim`, `TrimToBudget` or `Decay`.

These stream contracts apply equally to `NewStream` and workspace streams. Poison builds (`-tags nyanjit_scratch_poison`) overwrite expired storage at layout and release boundaries and detach it before reuse.

**Streams.** `Replace` copies its input instructions, while `ReplaceKeepingFacts` takes ownership of its input slice and preserves flag facts. `ReplaceAt` rewrites one instruction in place as `ReplaceKeepingFacts` would, measuring only that instruction; passes that change a few instructions without inserting or removing any use it instead of copying the stream. `ModifiedFlagsAt` returns the flags an instruction overwrites as recorded when it was appended or replaced, so flag-liveness passes need not ask the target again. `Reserve`, `SizeAt` and `StreamOptions.ExactFlagFacts` expose the cached size and flag-fact handling. `machine.Layout`, from `Stream.Layout()`, is an immutable snapshot of instruction PCs: `PC(index)` returns an instruction's byte position, and `InstructionAt(pc)` maps a PC back to the first instruction there, for profilers, debuggers and crash symbolization.

**Reusable storage.** `machine.Workspace` owns reusable stream storage and is not safe for concurrent use. `Rewind(keepStreams)` keeps a prefix of streams and restarts the remaining leases; reset, rewind, release and storage-discarding trims also invalidate affected borrows. `RetainedBytes`, `Trim` and `TrimToBudget` count the same retained buffers and maps. `TrimToBudget(bytes)` discards the largest buffers first and reports the bytes freed and the largest remaining buffer; `TrimPointers` applies a separate GC-scanning budget to pointer-bearing buffers; `Decay` applies the recent-demand policy after each completed lease cycle. `regalloc.Residency` and `regalloc.Batch` have the same `RetainedBytes`, `Trim`, `TrimToBudget` and `Decay` methods, and `Finish` ends their cycle. `machine.BufferHistory` is the shared decay policy: call `Observe` during each cycle, then `FinishCycle` once at its end. The fourfold recent-demand band keeps a peak for about 21 small requests; after 32 smaller requests it narrows to twofold to release persistent slack.

**Registers and sizes.** Target policies' `Registers` and `PhysicalRegisters` methods and `machine.VectorRegisters` return immutable `backend.RegisterSet` values; the policies' sets carry the target's register names, so `Name(i)` gives each register's name and `Derive(registers...)` makes another set named the same way. Use `Len`, `At`, `Contains`, `Index` or `All` to inspect them and `AppendTo(dst)` for independently mutable elements; queries allocate nothing. Parameter-register policies expose `ParamRegister(index) (backend.Register, bool)`; false means no register at that ABI index. Standalone size helpers are `PhysicalSize(ins, stack, goHomes)`, with an explicit stack policy and count of Go argument homes (zero for unmanaged layouts). The amd64, arm64, riscv64 and loong64 byte and text constructors take `(stack, goHomes)` too, as do loong64 clobber queries. The 386 helper ignores `goHomes` because Go arguments are stack-passed, and ppc64 uses the configured `Emitter.Size(ins)` method instead. The amd64 and riscv64 assemblers take `WithCPU(ResolvedCPU)`, and their size helpers are `PhysicalSizeWithCPU(ins, stack, profile, goHomes)`; both helpers panic on a foreign-architecture profile while accepting the zero baseline, so validate requests with `CPUProfile.Resolved` first.

**Barriers.** `machine.OpBarrier` names a guarantee in `Instruction.Barrier`, not an instruction: `BarrierSpeculation` (no later instruction executes, even speculatively, before earlier branches resolve), `BarrierMemory` (full load/store ordering) or `BarrierInstruction` (later fetches observe code stores visible to this thread). It reads and writes no registers or flags. Each target `Policy.Barrier(kind)` reports `BarrierArchitectural`, `BarrierPlatform` (the guarantee also needs processor, firmware or OS configuration, such as x86 LFENCE or POWER `ori 31,31,0`) or `BarrierUnavailable`, and its encoder rejects unavailable kinds with `ErrUnsupportedOperation`. riscv64 and loong64 have no speculation barrier, and x86 has no register-neutral instruction barrier. Each target's `FORMS.md` lists the instructions.

**CPU profiles.** `abi.CPUProfile.Resolved` returns an immutable `abi.ResolvedCPU` with `Architecture`, `Level`, `Has` and `Extensions` iteration; `Profile` and `AppendExtensions` copy feature names into caller-owned slices. Its zero value has no validated architecture and satisfies no contract, and `Supports` rejects cross-architecture requests.

**Homes.** `machine.HomePlan.Regions` holds complete visible register and spill homes per region. `RegisterOverrides` is a compact form for compiler-private plans, which leave `Regions` nil. `HomePlan.Lookup(region, variable)` reads either form, deriving spill homes in compact plans from `Slots`, `VariableRegions`, `VariableBits` and `RegionBounds`. `NeedsFloating`, `RegisterReserved` and the metadata fields (`ReservedRegions`, `RegisterVariables`, `HasFloatingHomes`) avoid repeated scans.

**Templates and reports.** `execution.NewTemplate` and `NewTemplateWithResolver` copy their mutable inputs and keep the report view they are given; wrap a raw report with `abi.NewReport`, which copies it, or hand one over with `abi.ReportOf`. Report getters, snapshots and the `CPUProfile.Clone`, `EmissionReport.Clone` and `CallEffect.Clone` methods return independent copies of populated slices and preserve nil versus non-nil empty slices; none of them normalizes an empty slice to nil.

## Versioning

Both modules are unpublished, and their APIs may still change. Once
published, this module will be tagged `backend/vX.Y.Z` and versioned
independently of the main module.

## Development

The repository's `go.work` joins this module with the main module. Run the
tests with `go -C backend test ./...` from the repository root, and with
`GOWORK=off` to check that this module builds from its own `go.mod`.
`examples/stackvm` is its own module and requires only this one;
`instrument/imports_test.go` in the main module checks that.
Run it with `go -C examples/stackvm run .` from the repository root, or add
`-S -arch loong64` to print a cross-target listing.
