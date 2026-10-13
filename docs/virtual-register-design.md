# Values, control flow and native calls

This document describes the current nyanjit pipeline. The public Builder API creates target-independent integer, floating-point bit-pattern, vector and mask values. `Finish` validates its instruction stream once and returns an immutable `*ir.SealedProgram`; callers may compile it repeatedly, concurrently, and for several targets, and `Clone` returns an editable `*ir.Program`.

## Values, blocks, and scopes

A `Value` identifies a typed result, not a physical register or an addressing mode. Scalar integers have widths of 8, 16, 32, 64, or 128 bits; vectors and masks carry an `ir.Type` and may have a larger total width. Pure expressions can be composed without immediately adding instructions:

```go
address := base.Add(index.MulConst(8)).Add(b.Literal(16))
value := b.Load64(address)
```

The builder materializes a pure expression at its use site. Loads, stores, calls, and syscalls remain ordered operations. Selection may fold the address into a memory operand, use a scaled arithmetic instruction, or compute it in ordinary registers. An internal scaled IR form is an instruction-selection aid; `AddScaled` is not a public Builder operation.

The function begins in an implicit entry block. A label can be reserved before or after entry instructions. `Bind` starts another block; if the current block is still open and nonempty, it supplies an implicit edge to the new label. `Jump` ends the current block. `Branch(p, label)` continues at the label when `p` holds; otherwise execution continues after the call, in a new block. A two-way branch is `Branch(p, a)` followed by `Jump(c)`; the Builder records the pair as one two-way branch, so it lowers to one conditional branch. Labels take no parameters. A `Label` is an opaque handle tagged with its Builder: the zero Label is invalid, and the builder rejects a Label or a value from another builder.

`Enter` and `Leave` nest lexical scopes. A label belongs to the scope that is current when it is reserved, and an explicit jump or branch may target only a label of the current scope. When the entered scope contains a jump or branch, `Enter` terminates the preceding block with an implicit edge into the scope. Straight-line scopes stay in the same block. Every block of a scope is reachable only through its `Enter`, and the scope is left only at its `Leave`. Values listed at `Leave` are promoted to the parent scope.

A value is usable in every block that its defining block dominates (see [docs/ir.md](ir.md)), until the `Leave` of the scope that defines it, unless that `Leave` lists it. Values that differ by path, including loop state, merge only through scoped variables: `Var`, `Get`, and `Set` (below).

Block parameters remain an IR concept. Raw IR built with `ir.ProgramBuilder`, or written as an `ir.Program`, can give labels parameters that incoming edges supply, as SSA block parameters, and the compiler lowers them with parallel copies on each edge. Builder programs never contain them, with one internal exception: the in-bounds continuation of `IndexedAddress` and `Data.Index` receives the checked base and index on its single edge, so that a hardened bounds check can clamp the index it passes. Lowering also carries a value used in a dominated block through internal label parameters; `Finish` rejects a use that its definition does not dominate.

### Scoped variables

```go
func (b *Builder) Var(initial Value) Var // owned by the current scope; type from initial
func (b *Builder) Get(v Var) Value       // snapshot of v
func (b *Builder) Set(v Var, x Value)    // x must have v's type
```

A `Var` is declared with a required initial value, so every `Get` reads a defined value. Declarations belong to the owning scope’s entry sequence, before its first branch; this prevents a path from bypassing initialization without undefined-read analysis. `Get` and `Set` are valid anywhere inside the owning scope, including nested scopes and blocks bound after the declaration. They are rejected after the owning scope's `Leave`. A value returned by `Get` is an ordinary value, usable in the blocks it dominates; later `Set` calls do not change it.

The optimized tier may promote Vars to SSA values or retain homes; the baseline
tier keeps homes cached in registers. Both preserve the same snapshot and scope
semantics. A capture is an immutable variable of the enclosing scope. Raw IR
records variable types and owning scopes, and Validate rejects out-of-scope use.

A loop keeps its state in variables of a scope that encloses the loop's labels:

```go
sum := b.Var(b.Const(0))
p := b.Var(base)
k := b.Var(count)
loop, exit := b.NewLabel(), b.NewLabel()
b.Bind(loop)
b.Branch(b.IsZero(b.Get(k)), exit) // the false edge falls through to the body
b.Set(sum, b.Add(b.Get(sum), b.Load64(b.Get(p))))
b.Set(p, b.Add(b.Get(p), b.ConstNative(8)))
b.Set(k, b.Sub(b.Get(k), b.ConstNative(1)))
b.Jump(loop)
b.Bind(exit)
total := b.Get(sum)
```

These variables and labels use the root scope. `Enter` and `Leave` are optional for this loop; use a nested scope when values need to expire or enclosing values need to be captured across its blocks.

## Selection, allocation, and assembly

The lowering path is:

```text
Builder → IR snapshot → instruction selection → register allocation and spill scheduling
        → physical emitter (instruction order and PC) → target byte or text assembler
```

Lowering may fold addresses, choose immediates and reuse arithmetic flags.
Register allocation schedules the moves and spills required by operand
constraints and call clobbers. Reports describe actual frame/workspace use and
register effects; selecting an entry ABI does not mean every register is saved.

`Unit.Listing` visits the emitted physical instructions with a text assembler. `Unit.Template` visits the same instruction stream with a byte assembler. The emitter owns instruction order, flag facts, and PC accounting; architecture-specific assemblers own textual syntax or byte encoding. `Listing` uses concrete runtime gateways and matches the template's physical layout. `ListingSymbolic` uses symbolic gateway names and can have different offsets for managed C calls; it is the listing available for those programs across targets.

`compile.TargetSpec.Architecture` selects a Linux assembly target independently of the executing host. Policies, register allocation, ABI layouts, and text assemblers are available for amd64, arm64, 386, riscv64, loong64, and ppc64le in every build; big-endian ppc64 is not supported. An empty architecture keeps the host platform and CPU defaults. Explicit amd64 listings use a baseline without FMA3 unless `CPU.Extensions` includes `"fma"`. Explicit PPC64 listings select POWER8, POWER9 or POWER10 through `CPU.Level`; zero selects POWER8. Native emission checks the kernel CPU level, and POWER9/10 can select integer `MADDLD` for single-use products. Text emission does not produce runtime unwind metadata, and managed C gateways are represented by symbols belonging to the target process. Native byte emission and callable compilation require the host architecture. Builder arity limits and native-width values still follow the host that constructs the IR.

## Flags and branches

An arithmetic instruction reports which of sign, zero, and carry it changed. The emitter records the value and operands that established each surviving flag. A following predicate may reuse those facts when they still describe its comparison; otherwise lowering emits a comparison. This permits a decrement followed by a zero branch without a redundant test. On x86, `INC` and `DEC` preserve carry, so a carry or borrow predicate for their new result needs a comparison. Branch compaction and range relaxation run before final assembly.

Compact-loop selection is optional and preserves observable counter and flag
behavior. Instruction choices differ by target and CPU profile; inspect Listing
and Report when a particular encoding or clobber set matters.

## ABI and calls

`compile.Func.ABI` chooses the generated function's entry convention. Each `CallC` or `CallGo` operation chooses its callee convention independently. `compile.PreflightCall` reports argument and result locations and conservative register effects before lowering; `Unit.Report` reports the emitted body's actual register, memory, workspace, and frame effects.

`CallSpec.Architecture` permits ABI queries for another Linux architecture. Lowering uses the selected target's call effects and argument limits; an explicit callee effect must match that architecture.

`compile.ContextNative` prepares a leaf function. `ContextForeign` supports outbound C calls from a foreign context. `ContextGoManaged` registers a pointer-free generated frame with the supported Go runtime and allows outbound Go calls from a Go-managed context. A C ABI entry can use this managed path when it originates in a Go runtime callback trampoline. A foreign thread cannot directly cross into Go through generated code. Managed calls currently support Linux/amd64, Linux/arm64, Linux/386, Linux/riscv64, Linux/ppc64le, and Linux/loong64 on the supported Go versions; Go pointer targets must remain heap-resident and reachable throughout execution. Pass pointers through the original `execution.Words` frame to retain their roots; `Code.Func` retains typed aggregate arguments through its wrapper.

### 386 boundary widths

386 entry parameters and outgoing C/Go call arguments use their scalar widths:
`ParamNative`, `ParamBits(8)`, `ParamBits(16)` and `ParamBits(32)` occupy one
four-byte word; `ParamBits(64)` occupies two, low first. Offsets accumulate
without an eight-byte slot or eight-byte alignment per argument. Unused
parameters remain part of the entry signature. For example, native/64/native
parameters occupy entry-SP offsets 4, 8/12, and 16; the return address is at 0.
The Go entry's two-word result slot starts at offset 20 for this signature.
Outgoing call offsets start at 0 instead, so its Go result slot starts at 16.
Go ABI0 packs consecutive 8-/16-bit parameters on 386. `CallGo` currently
rejects those adjacent subword arguments with a backend-limit error; use
32-bit arguments and matching callee parameters for that shape. Isolated
subword arguments retain a word-sized slot. `Code.Call` supplies EBP workspace
for spilling Go entries as well as C entries. Managed Go entries and direct
`Code.Func` calls still require `WorkspaceBytes == 0`.

`abi.ClassifyEntry`, `Classify386CEntry` and `Classify386GoEntry` take a slice
of word counts. `abi.CallSpec.ArgumentWords` supplies the corresponding call
signature alongside `ArgumentCount`; nil defaults to one native word per
argument. Explicit two-word counts describe 64-bit arguments on 386. Compiler
lowering always supplies actual widths, including
scoped preflight and `EmissionReport.ArgumentLocations`. The runtime counts
these reported words for `Code.Call` arity and Go frame argument metadata.

Outgoing areas reserve the largest real argument byte count across call sites,
plus eight bytes for a Go result, separately from spill homes. C calls keep ESP
16-byte aligned before CALL. The physical frame instruction carries this area
size to the byte and text assemblers; spill offsets no longer depend on a
fixed 80-byte reserve. The 386 unwinder still records the same ten-byte
prologue/epilogue and the final frame size.

A managed C call uses a compiler-owned Go helper with 64-bit slots. Its first
slot encodes the 32-bit target and a mask of two-word C arguments. The helper
packs native words and uses `execution.CallForeignPair`, preserving EAX:EDX.
This adapter keeps the native-word execution API intact and uses the existing
runtime-aware C transition. Ordinary Go calls use their actual argument widths.
Use `execution.Words(uint32(a), uint64(b), uintptr(c))` to supply native/64/native arguments, and
`CallPair` plus `execution.Wide` for a full 64-bit result on 386. An output buffer uses
`code.Call(execution.Words(&output[0], uint64(value))...)` after checking that
it is nonempty. Use a single `Words` frame for all arguments to retain roots.

Call effects drive the spill scheduler. Live values in clobbered registers are moved or saved only when needed; an escaped memory home is treated as authoritative after a call that may write it. Fixed argument moves are resolved as parallel copies. The generated body's clobber report excludes writes in the caller-side `Code.Call` trampoline.

## Stack and workspace

`StackNoUse` does not adjust the stack pointer. Straight-line code may spill to caller-provided workspace; `Code.Call` supplies the workspace required by the emission report. Labeled control-flow programs can also use `StackNoUse` when their selected edges and values need no frame. If lowering needs a platform frame, it returns a stack requirement error. Scoped variable slots are taken from the frame under `StackPlatform` and from workspace under `StackNoUse`; a scope whose variables fit in neither registers nor workspace also returns the stack requirement error.

`StackPlatform` permits an aligned frame bounded by `Func.Stack.BytesAvailable`. The report separates permanent frame size from additional call-site stack use. On amd64, a frame is reserved below the entry stack pointer and released before return; its owned interval is `[rsp, rsp+N)` after subtraction. The caller's return address is above that interval. A stack policy never implies blanket register saves. `StackPointerOrdinary` remains unsupported by the current targets.

## Memory and verification

Native loads and stores require a valid address for the entire access. `LoadBits` and `StoreBits` use an explicit width; `Store8` accepts 8-, 16-, 32- or 64-bit scalar values and stores the low byte; `Store16` and `Store32` accept a value of their width or a 64-bit value and store its low bytes; `Store64` accepts only a 64-bit value. `Code.Call`/`CallPair` accept raw native words for every entry kind. `execution.Words` moves pointer targets to the heap and retains their roots; callers match aggregate types and layouts to the compiled signature. `Code.Func` checks the exact signature. The `cell` package supplies a value kept in a caller-provided memory home: `New` writes it, and every `Store` and `Load` goes through the home, so aliases and calls see the current value on every control-flow path.

`ir.Program.Validate` checks instruction references, scope exits, labeled control flow, and label parameters. A raw `*ir.Program` passed to the compiler is detached and validated again on every compilation, because its fields are public and mutable; an `*ir.SealedProgram` was validated once when it was sealed and is not validated again. The program object itself owns no executable memory.
