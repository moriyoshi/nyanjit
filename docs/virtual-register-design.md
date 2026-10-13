# Virtual values and native lowering

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

In IR a variable is mutable storage: because a scope is a single-entry region with a lexical lifetime, a variable can have one home for that region, with `Get` and `Set` lowering to reads and writes of it. The optimized tier promotes most variables to SSA values before lowering (`compile/var_promotion.go`): `Set` defines a value, `Get` reads the reaching one, and merges become internal block parameters. It keeps a home for variables where promotion measured worse (loop-carried state under the per-block allocator, FP state crossing blocks, a `Get` feeding a `CallGo` target) and falls back to homes when a promoted program hits a backend limit. The baseline tier keeps every variable in a home, cached in registers. A capture is lowered as an immutable variable of the enclosing scope. `ir.Program` records each variable's type and owning scope, and `Validate` rejects a use outside that scope.

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

Selection receives target capabilities before assigning physical registers. It picks immediate instructions, folded addresses, scaled arithmetic, and narrow aliases where legal. A target may use amd64 `LEA`, arm64 shifted `ADD` or `MADD`, or a riscv64 shift/multiply sequence for a suitable expression. On amd64 and 386, native-width `+1` and `-1` may select `INC` and `DEC`; their carry-flag behavior is tracked separately from `ADD` and `SUB`. The 386 assembler and call trampoline are implemented. The riscv64 backend emits integer instructions and supports the same high-level Builder operations within its documented limits.

Scalar value-count shifts remain semantic IR through the Builder. Selection uses a target register-count operation when available, and otherwise expands a portable barrel network. PPC64 emits native doubleword/word shifts with a full unsigned count clamp, preventing oversized counts from wrapping through the hardware count mask. Rotations use native register-count rotate instructions; narrow values repeat their pattern into a word before rotating. Counts saturate for shifts and reduce modulo the semantic width for rotations, independently of count width. Composite 128-bit values retain their portable decomposition.

Wide integer arithmetic retains scalar high-product and carry/borrow operations in IR. Targets can select native high products and carry chains, or expand them into portable arithmetic. PPC64 fuses single-use carry extraction into the high-word operation and reuses XER only when its operand provenance still matches; shared carries remain explicit values. This gives eligible 128-bit additions/subtractions two arithmetic instructions (`addc / adde` or `subfc / subfe`), and uses `mulhdu` for wide products. Scalar carry inputs consume their low bit.

Register allocation works on selected virtual values. It propagates fixed call argument registers backward through eligible producers, reuses a dead input's register when the target permits, and schedules spills or reloads when pressure or clobbers require them. The encoder receives only allocated instructions. Neither the encoder nor the emitter implicitly saves all clobbered registers.

Callee-preserved registers follow the volatile ones in allocation order, so a function uses one only after the volatile registers are taken, and then saves and restores it once in its prologue and epilogue. Before a C entry takes its first such register, the allocator evicts a value that already has a slot, which costs at most a reload, rather than pay for the save. A 386 frame saves EBX, ESI and EDI on entry; a frameless 386 entry saves the ones it uses in its workspace. A value that stays live across a call moves to a free preserved register before the call instead of being stored before it and reloaded after it; it is spilled only when no preserved register is free.

Variables and captures are scheduled by scope rather than by block:

- **Fixed location per region.** Within one scope's own blocks, a variable's location, either a register or its slot, is the same at every label. Edges inside the scope need no copies for variables, and loop-carried state needs no back-edge parallel copies.
- **Changes only at scope boundaries and calls.** A location may change only at `Enter`, at `Leave`, and around calls. At `Enter`, outer variables that the inner scope never uses are spilled to their slots and reloaded at the matching `Leave`, but only when the inner scope's total register demand exceeds the registers available. Otherwise they stay in their registers across the inner scope.
- **Innermost scope first.** Under register pressure, variables of the innermost scope take priority. An outer variable that the inner scope reads may stay in memory for that region, where each `Get` becomes a load.
- **Live for the whole scope.** A variable is live from its declaration to its scope's `Leave`, not merely until its last use in a block. A call that clobbers a variable's register either finds the variable in a callee-saved register or saves and restores it through the ordinary call-effect spill schedule.
- **Reserved slot.** Every variable has a slot reserved for its scope, so a spill never allocates storage.

Backends see only ordinary register and memory moves.

`Unit.Listing` visits the emitted physical instructions with a text assembler. `Unit.Template` visits the same instruction stream with a byte assembler. The emitter owns instruction order, flag facts, and PC accounting; architecture-specific assemblers own textual syntax or byte encoding. `Listing` uses concrete runtime gateways and matches the template's physical layout. `ListingSymbolic` uses symbolic gateway names and can have different offsets for managed C calls; it is the listing available for those programs across targets.

`compile.TargetSpec.Architecture` selects a Linux assembly target independently of the executing host. Policies, register allocation, ABI layouts, and text assemblers are available for amd64, arm64, 386, riscv64, loong64, and ppc64le in every build; big-endian ppc64 is not supported. An empty architecture keeps the host platform and CPU defaults. Explicit amd64 listings use a baseline without FMA3 unless `CPU.Extensions` includes `"fma"`. Explicit PPC64 listings select POWER8, POWER9 or POWER10 through `CPU.Level`; zero selects POWER8. Native emission checks the kernel CPU level, and POWER9/10 can select integer `MADDLD` for single-use products. Text emission does not produce runtime unwind metadata, and managed C gateways are represented by symbols belonging to the target process. Native byte emission and callable compilation require the host architecture. Builder arity limits and native-width values still follow the host that constructs the IR.

## Flags and branches

An arithmetic instruction reports which of sign, zero, and carry it changed. The emitter records the value and operands that established each surviving flag. A following predicate may reuse those facts when they still describe its comparison; otherwise lowering emits a comparison. This permits a decrement followed by a zero branch without a redundant test. On x86, `INC` and `DEC` preserve carry, so a carry or borrow predicate for their new result needs a comparison. Branch compaction and range relaxation run before final assembly.

PPC64 reports independent CR0 and XER effects through `machine.StreamOptions.FlagFacts`. Logical operations preserve carry; immediate arithmetic right shifts invalidate carry while preserving CR0; register-count shift guards overwrite CR0, while arithmetic forms also overwrite carry. Register-count rotations preserve CR0 and XER; comparisons preserve XER. Identity and widening copies preserve flag aliases only at proven widths. High-word carry operations expose sign/zero facts without inventing binary carry facts. Proven zero aliases survive sign extension, and truncating extensions do not invent aliases. Carry testing copies XER into volatile CR7, preserving CR0 for composed predicates. POWER8 uses `mfxer / srdi / mtcrf`; POWER9/10 use `mcrxrx` and can select CA32.

With `PreferCompactLoops`, a PPC64 layout pass selects CTR for eligible natural countdown loops. It examines counter uses, dedicated-counter effects and individual flag liveness across the physical CFG. External entries initialize CTR, the backedge bypasses initialization, and fallthrough restores the original GPR count to zero. Calls, overlapping CTR loops, side entries, early exits and observed counters keep their original form. Limited-range `bdnz` branches relax from a flag-preserving `bdz / b` expansion.

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
