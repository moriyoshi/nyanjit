# Dual-tier compilation: baseline and optimized tiers behind one Builder

Status: implemented, 2026-10-29 (P0–P6). Section 13 lists where the implementation differs from this text.

## 1. Summary

A client writes a function once with the Builder and compiles it with either of two tiers:

- **TierOptimized**, today's pipeline: SSA IR, selection, whole-function register allocation.
- **TierBaseline**, a new tier that builds no SSA. It records compact operation records for the function, runs one liveness pass, and allocates and encodes in one forward pass. Its target is 40–60 ns per operation, against about 630 ns today and about 12 ns for sljit, with run time within 1.5× of the optimized tier.

Five decisions shape the design:

1. **Label parameters leave the Builder.** Values are usable wherever they dominate within their scope, as today. Values that differ by path merge through Vars. `Leave(exports...)` bounds every lifetime, so neither tier needs explicit edge arguments. Edges become `Jump(label)` and `Branch(p, label)`.
2. **The optimized tier promotes Vars to SSA**, so moving merge state from label parameters to Vars costs no code quality.
3. **The tier is a Target property**, using the field reserved for it, `compile.TargetSpec.Tier`. A Builder bound to a Target compiles at that Target's tier.
4. **The Builder hands each finished block to a `compile.Stream`** as an `ir.Segment`. The Stream records segments for the optimized tier, or converts them to its own compact form for the baseline tier. No import edge is added.
5. **The unbound path is unchanged.** `NewBuilder()` with `Finish()` still returns an `*ir.SealedProgram`. `compile.Lower` accepts both tiers.

## 2. Evidence

These measurements were taken on the arm64 host, pinned to a Cortex-X925 core. The scratchpad holds the details (`apidesign/notes-*.md`, `sjbench/`).

- **Compile cost today is about 630 ns per operation:**
  - Builder: about 60–140 ns.
  - `Finish`/Seal: about 60 ns per instruction.
  - `Lower`: 600–2800 ns per instruction.
  - For comparison, sljit spends 11.6 ns per operation. The optimized tier's code (with the Online allocator) runs within 1.15–1.25× of sljit's hand-allocated code.
- **A prototype of the baseline design** compiles chain1000 at 100 ns per operation through today's `machine.Instruction`/`Stream`/`Visit` path, and at 30 ns per operation calling `ByteAssembler` methods directly, bind included. Its code matches sljit's instruction for instruction. The one difference is that arm64 `Binary` always sets flags (§9).
- **Values in a 1270-program corpus:**
  - 88% have a single use.
  - 82% are consumed by the next instruction.
  - 0.7% are used in another block other than as an edge argument.
- **Liveness matters most for a naive register cache.** With no liveness, the cache does 6.3× the frame traffic of compiled code. Exact use counts cut that by 74%. True liveness with next-use eviction is best of all.
- **Prior art agrees:**
  - Every dual-tier system shares an instruction-level contract and leaves SSA construction to the optimizing tier: Cranelift `use_var`, MIR, QBE, libgccjit, Liftoff with TurboFan, Winch with Cranelift.
  - TPDE spent 37% of its compile time building SSA its single-pass back end did not need. TPDE's own structure, one analysis pass followed by one emission pass, is the model for this baseline.
- **Vars are already non-SSA.** `OpGet`/`OpSet` survive to lowering, and each Var gets one home per scope. Converting the outer-value workloads from label parameters to Vars costs +19–20% instructions in the optimized tier today (§6.1 removes that cost).

## 3. Goals and non-goals

**Goals.**
- One Builder vocabulary for both tiers. A generator function runs unchanged against either.
- Both tiers accept and reject the same programs with `ErrInvalidFunction` (§8).
- Baseline compile at 40–60 ns per operation. Small functions are bounded by bind cost until §10.2.
- No SSA construction in the baseline tier.
- No new import edges: `builder` → `ir`, `compile` → `ir` + backend. The backend module stays free of the IR.

**Non-goals.**
- Byte-identical output across tiers.
- Automatic tier-up. Tier-up means calling the generator again with a different Target (§7.3).
- Accepting every Func and every operation in the baseline from day one. Gaps return `ErrUnsupported` (§6.2.6).

## 4. The Builder dialect

These rules apply to every Builder, bound or not. Raw IR (`ir.ProgramBuilder`, `ir.Program`) keeps block parameters and is unaffected.

### 4.1 Blocks and labels

- The function starts in an implicit entry block.
- `Bind(label)` starts a block. If the current block is open and nonempty, `Bind` ends it with an implicit edge to the label. This is unchanged.
- `Jump(label)` ends the current block.
- `Branch(p, label)` continues at the label when `p` holds. Otherwise execution continues after the call, in a new block that is bound implicitly. This matches today's one-target form.
- A two-way branch is `Branch(p, a)` followed by `Jump(c)`. Both tiers emit this as one conditional branch when the layout allows.
- Labels have no parameters.
- A label belongs to the scope that was current at `NewLabel`. Edges may target only labels of the current scope. Both rules are unchanged.

### 4.2 Values

A Value is usable from its definition in every block its defining block dominates, until the `Leave` of its scope, unless that `Leave` exports it. This is today's rule. `Finish` and `Lower` report a use that is not dominated (§5.6).

### 4.3 Vars

- `Var(initial)` declares mutable state owned by the current scope. It must still be called in the scope's entry sequence.
- `Set(v, x)` writes the Var.
- `Get(v)` returns a snapshot that later `Set`s do not change. The snapshot is an ordinary Value and follows §4.2.

All of this is unchanged. Vars are now the only way to merge values that differ by path:

```go
x := b.Var(b.Const(0))
yes, done := b.NewLabel(), b.NewLabel()
b.Branch(cond, yes)
b.Set(x, b.Mul(a, k))
b.Jump(done)
b.Bind(yes)
b.Set(x, b.Add(a, k))
b.Bind(done) // implicit edge from the open block
b.Return(b.Get(x))
```

A loop carries its state the same way:

```go
i, acc := b.Var(b.Const(0)), b.Var(b.Const(0))
loop, done := b.NewLabel(), b.NewLabel()
b.Bind(loop)
b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, n), done)
b.Set(acc, b.Add(b.Get(acc), f(b.Get(i))))
b.Set(i, b.Add(b.Get(i), b.Const(1)))
b.Jump(loop)
b.Bind(done)
b.Return(b.Get(acc))
```

### 4.4 Scopes

`Enter()` and `Leave(exports...)` are unchanged.
- Control flow enters a scope only at `Enter` and leaves only at `Leave`, because edges are scope-local and `Return` is root-only.
- Values the scope defines and does not export die at `Leave`. This is the lifetime bound the baseline's liveness pass relies on.
- Outer values used inside a nested scope are still captured implicitly. The Builder records captures as immutable Vars, as today (§5.4 covers how segments carry them).

### 4.5 Predicates and deferred expressions

- `Compare`, `CompareImmediate`, `IsZero`, `IsNegative` and `Carry` work as today. So do `And`, `Or` and `Not`, and deferred expressions.
- One correction: a materialized deferred expression is reused only within the block that materialized it. It is materialized again elsewhere. This fixes a bug where an expression shared by two branch arms produced a program that `Finish` rejects.

## 5. API changes (exact signatures)

### 5.1 Package `nyanjit` (root)

**Removed:**

```go
func (b *Builder) JumpArgs(label Label, args ...Value)
func (b *Builder) BranchArgs(predicate Predicate, ifTrue Label, trueArgs []Value, ifFalse Label, falseArgs []Value)
func (b *Builder) BindParams(label Label, count int) []Value
func (b *Builder) BindParamsBits(label Label, widths []uint8) []Value
func (b *Builder) BindParamsTypes(label Label, types ...ir.Type) []Value
```

**Changed:**

```go
// Branch continues at target when predicate holds. Otherwise execution
// continues after the call. For a two-way branch, follow it with Jump.
func (b *Builder) Branch(predicate Predicate, target Label) // was: (predicate, ifTrue Label, ifFalse ...Label)
```

**Unchanged:** `Jump(label Label)`, `Bind(label Label)`, `NewLabel() Label`, `Return`, `Var`, `Get`, `Set`, `Enter`, `Leave`, `Valid`, `NewBuilder`, `Compile`.

**Added:**

```go
// NewBuilderFor creates a Builder bound to t and f. Lower compiles its
// function at t's tier: TierBaseline without building SSA, TierOptimized as
// Lower does for a sealed program. Both tiers accept the same programs. An
// error from compile.NewStream is recorded and returned by Lower.
func NewBuilderFor(t compile.Target, f compile.Func) *Builder

// Lower ends a Builder from NewBuilderFor and returns its Unit, or the first
// error recorded while building or lowering. Misuse wraps
// compile.ErrInvalidFunction; backend limits match compile.ErrBackendLimit,
// with a *compile.LimitError when the limit is a number; an operation or Func
// the baseline tier does not implement yet matches compile.ErrUnsupported. On
// a Builder from NewBuilder it returns an error wrapping
// compile.ErrInvalidFunction.
func (b *Builder) Lower() (*compile.Unit, error)
```

**Doc change to `Finish`:** "On a Builder from NewBuilderFor, Finish returns an error wrapping compile.ErrInvalidFunction; use Lower." This keeps generators tier-independent: a generator that calls `Finish` fails the same way on both tiers.

**Changed type** (decision D1, §11):

```go
// Label is a control-flow destination of one Builder. The zero Label is
// invalid; a Label of another Builder is rejected.
type Label = builder.Label // builder.Label becomes opaque (below)
```

### 5.2 Package `builder`

These mirror the root package, with error results.

**Removed:**

```go
func (b *Builder) JumpArgs(label Label, args ...Value) error
func (b *Builder) BranchArgs(predicate Predicate, ifTrue Label, trueArgs []Value, ifFalse Label, falseArgs []Value) error
func (b *Builder) BindParams(label Label, count int) ([]Value, error)
func (b *Builder) BindParamsBits(label Label, widths []uint8) ([]Value, error)
func (b *Builder) BindParamsTypes(label Label, types ...ir.Type) ([]Value, error)
```

**Changed:**

```go
func (b *Builder) Branch(predicate Predicate, target Label) error

// Label is a control-flow destination of one Builder. The zero Label is
// invalid. Labels carry their Builder's identity, so a Label of another
// Builder, or of an earlier run of a generator, is rejected.
type Label struct{ /* unexported: index and builder generation, no pointers */ }
```

Today `type Label = ir.Label` (an int). `ir.Label` stays as it is, for IR.

**Added:**

```go
// NewSegmentBuilder creates a Builder that hands each block to emit as an
// ir.Segment when the block ends, instead of recording a program for Finish.
// The rules for Values, Vars and scopes are those of every Builder. If emit
// returns an error, the method that ended the block returns it, later methods
// return ErrInvalidFunction, and End returns emit's error.
func NewSegmentBuilder(emit func(*ir.Segment) error) *Builder

// End finishes a Builder from NewSegmentBuilder. It reports open scopes,
// unbound labels and a missing return as Finish does, and otherwise returns
// emit's first error, if any. On a Builder from NewBuilder it returns
// ErrInvalidFunction, as Finish does on a segment Builder.
func (b *Builder) End() error
```

### 5.3 Package `ir`

**Added:**

```go
// Segment is one terminated block of a function, in layout order, as a
// streaming front end hands it to a consumer such as compile.Stream.
// Concatenating a function's segments gives a Program: Code makes up
// Instructions, Variables and ScopeExits append to those tables, and the label
// steps for Labels followed by Step make up Control.Steps. Every Program rule
// applies, including dominance (docs/ir.md §6); a consumer checks rules that
// span segments when the function ends.
//
// IDs are program-wide: Code[i] has ID Base+i, and Step's Start, End,
// LeaveStart and LeaveEnd are program coordinates. An immutable Variable that
// a later segment declares is a capture: it adds its Initial values to the
// Args of its scope's OpScopeEnter. A consumer must not retain a Segment or
// anything it refers to after it returns, because the front end reuses the
// storage.
type Segment struct {
	// Labels are bound at the start of the segment, in binding order. The
	// first segment is the function entry and binds none.
	Labels []Label
	// Base is the ID of Code[0].
	Base int
	// Code holds the segment's instructions. OpParam appears only in the
	// first segment.
	Code []Instruction
	// Variables declared in this segment. Their indices continue the
	// table of earlier segments.
	Variables []Variable
	// ScopeExits whose Leave lies in this segment, in order.
	ScopeExits []ScopeExit
	// Step ends the segment with TermJump, TermBranch or TermReturn. It is
	// never a label step.
	Step ControlStep
	// ParameterTypes is set on the first segment only, as for
	// Program.ParameterTypes.
	ParameterTypes []reflect.Type
}
```

`Segment` is also valid for raw-IR front ends that use block parameters. `OpLabelParam` and edge arguments are allowed; Builders never produce them.

### 5.4 How the Builder produces segments

This section describes behavior only; none of it is API. Two pieces of today's retroactive work in `Finish` move to the point where a block is emitted:

- **Scope-entry split.** When the first jump or branch of an entered scope appears, the open block still holds the code before `Enter`. The Builder splits it at the `Enter` position before emitting. Values cross the split by dominance, so nothing is threaded.
- **Captures.** A capture found in a later block becomes an immutable Variable in that block's segment (see `ir.Segment`). The optimized tier's recorder patches the `OpScopeEnter` Args when the function ends. The baseline treats the capture as a dominated use of the outer value.

Segment mode also replaces the Builder's per-instruction storage (144-byte `ir.Instruction` records growing in `codeBlocks`, plus owner tables) with:
- one reused segment buffer;
- a 16-byte per-value record of type, scope and block, needed for type checks and dominance checks at `End`.

### 5.5 Package `compile`

**Changed:**

```go
// TargetSpec.Tier doc: "Tier selects how much work lowering does. The zero
// value is TierOptimized."

// Tier selects how much work lowering does.
type Tier uint8

const (
	// TierOptimized is the optimizing tier: SSA IR, selection, Var
	// promotion and whole-function register allocation. It is the zero
	// value.
	TierOptimized Tier = 0
	// TierBaseline compiles without building SSA. It records each
	// function in a compact form, computes liveness once and allocates and
	// encodes in one forward pass. It compiles about ten times faster than
	// TierOptimized, with slower and larger code. NewTarget returns an error
	// wrapping ErrUnsupported for an architecture without a baseline tier.
	TierBaseline Tier = 1
)
```

`NewTarget` accepts `TierBaseline`. Baseline and optimized targets are separate cached Targets, because `Tier` is already part of the cache key. `Lower(t, program, f)` with a baseline Target lowers a sealed program in the baseline tier, feeding the program's blocks to the same engine. `nyanjit.Compile` follows `spec.Tier`.

**Added:**

```go
// Stream lowers one function for a Target as its front end produces it, one
// ir.Segment at a time. A Builder from nyanjit.NewBuilderFor drives a Stream;
// raw-IR front ends may drive one directly.
//
// For a TierBaseline Target, Segment converts each segment to the baseline's
// compact form and keeps no ir.Instruction. For a TierOptimized Target,
// Segment records the segment and Unit lowers the whole program as Lower
// does; the same segments then give output identical to Lower's for the
// concatenated program.
//
// Segment and Unit check what they receive. Rule violations return an error
// wrapping ErrInvalidFunction, located by a *ir.ValidationError with a
// program-wide ID. Limits return ErrBackendLimit, or a *LimitError when the
// limit is a number. Gaps in the baseline tier return ErrUnsupported. After
// the first error, every later call returns it. A Stream is not safe for
// concurrent use.
type Stream struct{ /* unexported */ }

// NewStream starts lowering a function entered as f describes, for t. It
// returns Lower's errors for an invalid Target, Context or ABI, and
// ErrUnsupported when t is TierBaseline and f asks for what that tier does not
// implement yet (§6.2.6).
func NewStream(t Target, f Func) (*Stream, error)

// Segment consumes the function's next segment in layout order. It does not
// retain seg or anything it refers to after it returns.
func (s *Stream) Segment(seg *ir.Segment) error

// Unit ends the function and returns its Unit. It checks rules that span
// segments (dominance, unbound labels, a complete function) and returns an
// error wrapping ErrInvalidFunction for a violation. Unit consumes the
// Stream; a second call returns ErrInvalidFunction.
func (s *Stream) Unit() (*Unit, error)
```

**Behavior of `Unit` methods for a baseline Unit:**
- `Report`, `Listing` and `Template` keep their contracts.
- `ListingSymbolic` returns `Listing`'s result. The baseline makes no managed C calls whose targets would need symbolic names.
- `RegisterScopes` returns an error wrapping `ErrUnsupported`.

### 5.6 Errors

There are no new sentinels.

- **Per call:**
  - type and width errors;
  - scope liveness;
  - foreign or stale Labels;
  - edges to labels of another scope;
  - argument limits.
- **At `End`, `Stream.Unit`, `Finish` and `Lower`:**
  - a use not dominated by its definition;
  - unbound labels;
  - open scopes;
  - a missing return.

Both tiers report all of these identically. The baseline needs dominance only at function end, and computes it over its own block graph, which is cheap at this size.

### 5.7 Identifier changes

| Change | Identifier | Package | Reason |
|---|---|---|---|
| added | `NewBuilderFor` | nyanjit | bound Builder; composes Builder and Stream |
| added | `(*Builder).Lower` | nyanjit | output of a bound Builder |
| added | `NewSegmentBuilder` | builder | segment mode |
| added | `(*Builder).End` | builder | ends segment mode |
| added | `Segment` | ir | hand-off format in IR vocabulary; no new import edge |
| added | `TierBaseline` | compile | fills the reserved `TargetSpec.Tier` |
| added | `Stream`, `NewStream`, `(*Stream).Segment`, `(*Stream).Unit` | compile | engine state across the root/compile boundary; raw-IR streaming entry |
| changed | `Branch` | nyanjit, builder | one target; fallthrough continues |
| changed | `Label` | builder (root alias) | opaque, builder-tagged (D1) |
| removed | `JumpArgs`, `BranchArgs`, `BindParams`, `BindParamsBits`, `BindParamsTypes` | nyanjit, builder | label parameters leave the Builder |

## 6. Engines

### 6.1 TierOptimized

The pipeline is unchanged, with one new pass.

**Var promotion.** After sealing and before cross-block threading, compile turns each promotable Var into SSA values:
- `Set` defines a new value.
- `Get` returns the reaching value.
- Merges become label parameters created internally, as cross-block threading creates them today.

Scope-local labels make every label's predecessors known by its scope's `Leave`. That makes the construction a straight application of Braun et al. 2013 over each scope region. Vars the pass does not promote keep their home; promotion changes code quality, never semantics.

Promotion is what keeps the move from label parameters to Vars free. Its gate is that the quality corpus, with the parameter programs converted to Vars, matches the parameter versions within 2% in instruction count.

### 6.2 TierBaseline

The baseline lives in `compile`. It reuses the target adapters (fixed clobbers, implicit writes), the frame and literal finishing, and `Template`.

#### 6.2.1 Recording

`Stream.Segment` converts each instruction to a pointer-free 32-byte record with:
- opcode;
- width;
- up to three operand IDs;
- an immediate index;
- a bank hint.

Out-of-line data (call arguments, constant bytes, symbols) goes into side arenas, because the segment storage is reused. Per block, the Stream records:
- the record range;
- the terminator;
- successors;
- whether the block is a loop header (a label that is bound before a later jump to it);
- scope boundaries.

#### 6.2.2 Analysis

At `Unit`, the engine makes one pass over the blocks:
- **Reverse postorder and dominators**, used to check the dominance rule.
- **Loop membership.**
- **Liveness of cross-block values only**, about 0.7% of values, by a backward pass over blocks with each loop's live set extended to the loop's end. A value's lifetime ends at its scope's `Leave` unless exported.
- **Use counts per Var, weighted by loop depth.**

Liveness of values within a block comes from the allocator's per-block backward next-use pass.

#### 6.2.3 Allocation

One forward pass in layout order:
- **Within a block:** next-use (Belady) eviction, clean values evicted first. Integer constants are rematerialized at use, and compares against small constants use compare-immediate forms. On arm64 and amd64, FP constants load from a deduplicated literal pool after the code, and an FP compare that only feeds a branch is fused into it. Fixed-register operations evict whatever holds their registers first.
- **Vars:** each gets a home slot. The heaviest Vars by weighted use count are pinned to registers: callee-saved ones always, and in a function without calls caller-saved ones first, keeping a few registers per bank free. The rest are cached in registers and written back when evicted. A Get reads the pinned register directly unless a later block needs the snapshot.
- **At a label:** the first arriving edge fixes the register state of the values live into the label and of the cached Vars. A loop header's state is fixed by its forward entry. Later edges reconcile with `regalloc.PlanEdgeTransfer` parallel moves. The taken edge's moves go into an out-of-line stub when it needs any.
- **Calls:** a value live across a call goes to a callee-saved register, or to its slot if none is free.
- **Banks:** FP or GP is chosen at the definition from the defining operation and the block's consumers.

#### 6.2.4 Emission

Records encode straight into the target's `ByteAssembler` through its public methods, with no `machine.Instruction`, `Stream` or `ResolveBranches` step. Sizes are known per record. Labels resolve from one pass over record sizes, falling back to long branch forms out of range. The prologue and epilogue come from the existing frame helpers once the frame size is known.

#### 6.2.5 Output

The engine produces the inputs of `execution.NewTemplate`:
- code bytes;
- relocations;
- `CodeOptions`;
- a Go frame;
- an `abi.Report` with stack and workspace bytes, FP status and clobbers.

It wraps them in a `Unit`.

#### 6.2.6 Coverage

Baseline coverage grows by phase (§10).

**Until a feature lands, `NewStream` or `Segment` returns `ErrUnsupported` for:**
- `Func.Harden`;
- `ContextGoManaged`;
- `Func.Constants` with symbols bound in code.

**Vectors** are scalarized at first.

**`PreferCompactLoops`** is a permission, so the baseline ignores it.

## 7. Guarantees

### 7.1 Acceptance

A program that one tier rejects with `ErrInvalidFunction`, the other rejects the same way, and a program one tier accepts, the other accepts.

Backend limits are tier-dependent: spill words, frame bytes, branch range, `StackPolicy.BytesAvailable`, and the vector budget. A baseline `*compile.LimitError` means the function should be compiled at `TierOptimized`.

### 7.2 Results

Both tiers compute the same results, except where results are already unspecified:
- NaN payload and sign bits;
- `ConversionNative` on NaN or out-of-range input;
- `DivisionUnchecked` with a zero divisor or MIN / −1;
- `OpFProductError` outside its contract;
- misaligned access;
- floating-point status flags;
- `Harden` output bytes.

The conformance corpus runs on both tiers as a differential test of results, never of bytes.

**Determinism:** for the same Builder calls, Func and Target spec, each tier's output is deterministic, as today.

### 7.3 Tier-up

Call the generator again with a Builder bound to a Target at the other tier. Labels from an earlier run are rejected, because Labels are builder-tagged.

One hazard is outside nyanjit's control: `foreign.RegisterCallback` registers a callback for the life of the process, so a generator that calls it should register once, outside the generator.

## 8. Performance targets

These are measured with `scratchpad/sjbench` on CPU 5.

| Kernel | Today | Baseline target | sljit |
|---|---|---|---|
| chain1000 compile | 2.52 ms (630 ns/op) | ≤ 240 µs (≤ 60 ns/op) | 46 µs |
| loop compile | 41 µs | ≤ 8 µs before §10.2, ≤ 2 µs after | 0.63 µs |
| loop run | 5.6 ms (Online 3.9 ms) | ≤ 1.5 × TierOptimized | 3.4 ms |
| pressure run | 7.7 ms (Online 1.6 ms) | ≤ 1.5 × TierOptimized | 1.3 ms |

## 9. Fixes independent of the tiers (phase P0)

None of these changes the API. Each should land on its own:

- **arm64 `Binary` always emits ADDS/SUBS/ANDS.** A flag-free form where flags are dead makes the loop kernel 35% faster (below sljit) and pressure 17% faster. This helps both tiers.
- **arm64 `BinaryImmediate` builds a map literal on every logical-immediate call** (67 ns). The constant plan is string-based (23 ns per constant). Use enum operations instead.
- **The root `do()` allocates an error on every void call**, through `limitError(nil)`. Allocate only on failure.
- **`builder.newExpression` escapes every deferred node to the heap**, plus a mutex per node.
- **The deferred-expression cache is reused across blocks** (§4.5).
- **`Var` errors carry the caller's function name instead of the operation's.**

## 10. Phases and gates

1. **P0, fixes (§9).** Gate: the QEMU matrix, sweeps, and a quality corpus that is identical or better.
2. **P1, Var promotion in TierOptimized.** No API change. Gate: Var versions of the parameter programs within 2% of the parameter versions, and the conformance and QEMU matrices.
3. **P2, the dialect change (§5.1–5.2).**
   - Remove label parameters from the Builder.
   - Make `Branch` one-target and `Label` opaque.
   - Migrate call sites to Vars: about 170 `BindParams*` and 190 `JumpArgs`/`BranchArgs` sites, mostly in tests.

   Gate: conformance, QEMU matrix, quality corpus no worse.
4. **P3, segment plumbing.** `ir.Segment`, `builder.NewSegmentBuilder`/`End`, `compile.Stream` (TierOptimized recording), and `nyanjit.NewBuilderFor`/`Lower`. Gate: `Stream` output at TierOptimized is byte-identical to `Finish` + `Lower` over the corpus.
5. **P4, the baseline engine on arm64 and amd64.** Integers, control flow, Vars, calls and floats. Gate: the conformance corpus on both tiers, the performance targets (§8), and a tier differential in the random-program sweeps.
6. **P5, the remaining targets.** riscv64, ppc64le, loong64 and 386 (register pairs), plus `Harden`, `ContextGoManaged`, and `compile.Lower` with baseline Targets. Native vectors come later.
7. **P6, fixed costs:** cheaper bind (D2) and `ByteAssembler.Reset` (D3).

### 10.1 Migration

Two rewrites cover nearly every call site:
- A `BindParams` loop header becomes Vars declared before the loop.
- A two-target `BranchArgs` becomes `Branch` followed by `Jump`, with the arguments going through Vars.

`cell.Cell` keeps working, because its home Value is a parameter that dominates every use. `IndexedAddress` and `Data.Index` stop using a private parameterized label internally and rely on dominance instead.

### 10.2 Fixed bind cost

Binding costs 5–8 µs per function: `mprotect` to RW and to RX, and `mprotect`/`madvise` when closing. For small functions that is more than the compile itself. Decision D2 chooses between two remedies.

## 11. Decisions

| # | Decision | Recommendation |
|---|---|---|
| D1 | Make `builder.Label` opaque and builder-tagged | Yes. It catches foreign and stale Labels, which tier-up by re-running generators makes likely. |
| D2 | Bind cost, choose one | (a) batched W^X: `func (a *Arena) BindAll(templates []*Template, bindings Symbols) ([]*Code, error)` binds several templates with one mprotect pair, keeping strict W^X. (b) dual mapping: `func NewArenaDualMapped(size int) (*Arena, error)` writes through an RW alias and executes through an RX alias, with no syscall per bind, but leaves a writable alias of code in the process. Recommendation: (a) first. |
| D3 | Add `func (e *ByteAssembler) Reset()` to the six target ByteAssemblers | Yes. It lets the baseline reuse encoders without about 90 KB of garbage per compile. Stack practice and Go homes stay as constructed. |
| D4 | Tier on `TargetSpec.Tier`, not on `Func` | `TargetSpec.Tier`: the field is reserved for it, it is part of the Target cache key, and `Vectors` already shows code-generation policy living on the Target. |
| D5 | `Finish` on a bound Builder errors in both tiers | Yes. It keeps generators tier-independent. |

## 12. Alternatives considered

- **A separate sljit-style register-level API.** It is fastest, but the client becomes the register allocator and nothing is written once.
- **Choosing the tier only at `Lower`, over a sealed program.** It needs no API change, but recording and sealing set a floor of about 150–200 ns per operation. It survives as the baseline path of `compile.Lower`.
- **Block-local or chain-local Values with explicit label arguments (earlier drafts).** These allow a purely streaming baseline, but add a second validity rule and edge-argument ceremony. The extra lookahead the chosen design needs costs about 5–10 ns per operation and gives better code, because liveness is exact.
- **Use-count or `Drop` hints, or a stack-machine front end.** Recording the function makes them unnecessary.
- **A per-operation sink interface.** Pointer and slice arguments escape through it (14–39 ns and an allocation per operation), and it freezes a wide protocol. Handing over one segment per block costs about 0.2 ns per operation.
- **`builder` importing `compile`, or the reverse.** Either creates import cycles in 16–22 in-package test files and couples the front end to every target.

## 13. Implementation notes

The phases landed as planned. These points differ from, or add to, the text above.

- **Indexed addresses** (§10.1). `IndexedAddress` and `Data.Index` still use a private label with a parameter internally. The optimized tier's hardened bounds clamp recognizes only that shape (the index as the success edge's second argument). Nothing about it is exported.
- **Scope-entry split** (§5.4). Both Builder modes now split a block at `Enter` when the block ends, and values cross the split by dominance. `Finish` therefore no longer loads pre-`Enter` values through capture Vars. Over the review generators, about a quarter of the varfuzz-shaped programs changed, mostly shrinking by 1–8% on 64-bit targets.
- **Var promotion** (§6.1) is selective. It keeps a home for:
  - loop-carried Vars under the per-block allocator;
  - FP state that crosses blocks;
  - a `Get` that feeds a `CallGo` target;
  - any program where promotion hits a backend limit (lowering retries with homes).

  `NYANJIT_INTERNAL_VAR_PROMOTION=0` turns the pass off, and `strict` makes promotion errors fatal. Both are internal switches.
- **arm64 flag-free forms** (§9). These five methods were added: `Policy.ElideDeadFlags`, `ByteAssembler.BinaryNoFlags`, `ByteAssembler.BinaryImmediateNoFlags`, `TextAssembler.BinaryNoFlags` and `TextAssembler.BinaryImmediateNoFlags`. They implement the optional interfaces `machine` already defines, which amd64 and ppc64 already implement.
- **Segments** (§5.3). The first segment may bind labels when `Bind` comes before any instruction, as `Finish` puts such a label on the entry block. The Builder's per-value record is 20 bytes, not 16: it also keeps a literal's immediate or an Add/Sub's operands, which `Carry` and address folding read across segments.
- **Baseline coverage** (§6.2.6).
  - On every architecture, `Func.Harden` and `ContextGoManaged` return `ErrUnsupported`, and vectors are scalarized.
  - On amd64, FMA without the `fma` feature returns `ErrBackendLimit`, as in the optimized tier.
  - On 386:
    - FMA and 128-bit results return `ErrBackendLimit`, as in the optimized tier;
    - system calls are a gap;
    - 64-bit values live in XMM registers rather than register pairs.

### Measured results (arm64, Cortex-X925, one kernel per process)

| Kernel | Compile before (optimized, end to end) | Baseline compile | Baseline run vs optimized run | sljit compile |
|---|---|---|---|---|
| chain1000 (4000 ops) | 2.6 ms (648 ns/op) | 233 µs (58 ns/op) | 1.3 µs vs 1.3 µs | 47 µs |
| loop | 39 µs | 5.2 µs | 2.6 ms vs 2.6–2.9 ms | 0.68 µs |
| pressure (16 Vars) | 138 µs | 12.9 µs | 1.29 ms vs 1.29 ms | 1.28 µs |
| collatz | 73 µs | 8.0 µs | 10.1 ms vs 9.8 ms | 0.80 µs |
| fp (Horner + FCompare) | 88 µs | 11.2 µs | 1.75 ms vs 1.77 ms | 1.0 µs |

Binding costs 5–8 µs per function with `Bind`, and 0.4–1.3 µs per function with `Arena.BindAll` in batches of 8–64.
