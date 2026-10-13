# The NyanJIT IR

This document specifies `github.com/moriyoshi/nyanjit/ir`: the program form that every front end, the `Builder` or a front end that emits IR directly, hands to `compile`. It covers program structure, value numbering, types, every opcode, blocks and control flow, the dominance rule, scopes and variables, ordering of effects, validation and backend limits.

The modules are unpublished and the exported API may still change. This document specifies the current IR. Where behaviour depends on the target architecture or CPU, this document says so; anything it does not specify is unspecified, even if one backend happens to behave consistently today.

Contents:

1. [Programs](#1-programs)
2. [Values and numbering](#2-values-and-numbering)
3. [Types](#3-types)
4. [Opcodes](#4-opcodes)
5. [Blocks and control flow](#5-blocks-and-control-flow)
6. [The dominance rule](#6-the-dominance-rule)
7. [Scopes and variables](#7-scopes-and-variables)
8. [Effects, ordering and snapshots](#8-effects-ordering-and-snapshots)
9. [Validation](#9-validation)
10. [Backend limits](#10-backend-limits)

## 1. Programs

A `Program` is a mutable, target-independent function:

| Field | Meaning |
|---|---|
| `Instructions` | The instruction sequence. An instruction's index is its value ID (§2). |
| `Arity` | The number of entry parameters, which must equal the number of `OpParam` instructions. |
| `Result` | Straight-line programs: the value returned. Control programs: `-1`, or any value-producing instruction (it is not what the function returns; `TermReturn` steps are). |
| `ResultParts` | Straight-line programs only: `[low, high]` 64-bit values of a 128-bit result, with `ResultParts[0] == Result`. Empty for a single-word result. |
| `Control` | Labeled control flow (§5), or nil for a straight-line program. |
| `Variables`, `ScopeExits` | Scoped mutable storage and nested scopes (§7). Both are optional. |
| `StackAreas` | Fixed-size stack areas that `OpStackAddress` addresses by index (§4.5). Optional. |
| `Finalizers` | Optional `backend.Finalizers` that release host resources the code reaches through compiled-in addresses, such as memory pinned by `nyanjit.ValueCell`. Every Code built from the program shares one count: the list runs once, last registered first, when the last of those Codes is closed, and binding afterwards returns `execution.ErrClosed`. `Clone` shares the list rather than copying it. Each `Segment` carries the Finalizers registered in it, and the function's list appends them in order. Not part of any wire format. |
| `ParameterTypes` | Optional host Go aggregate types for the entry parameters (`reflect.Type` of a struct, array, slice or pointer; nil entries are raw scalars). When present it has exactly `Arity` entries. It describes how an executable `Func` wrapper passes arguments, and is not part of any wire format. |

A straight-line program (`Control == nil`) is one block. It returns the scalar value `Result` (or the pair `ResultParts`).

**Sealing.** Compilers read programs through `ir.Input`:

- `ir.Seal(p)` validates a detached copy of `p` and returns an immutable `*SealedProgram`. Later edits to `p` have no effect on it.
- `ir.SealOwned(p)` validates `p` and takes ownership without copying. Don't use `p`, or any slice reachable from it, afterwards.
- `(*SealedProgram).Clone()` returns an independent, editable `*Program`.
- `SealedProgram` accessors (`Instructions`, `Variables`, `ScopeExits`, `Control`, `ResultParts`, `ParameterTypes`, `Finalizers`, `StackAreas`) return borrowed storage. Read it, never modify it.
- A `SealedProgram` is safe for concurrent use. A `*Program` passed directly to a compiler entry point is sealed afresh on each call.

**Analysis.** `(*SealedProgram).Analysis()` provides `ScopeAt`, `Scopes`, `ScopeIndex`, `ScopeBounds`, `Parent`, `Bounds`, `Locals`, `VarUses`, `VarSets`, `VariableUsed`, `Uses`, `LastUse`, `ParamIndex`, `EntryParamBits`, `HasLoop`, `IsFloating`, `FloatingVariables`, `LabelFloating` and `Dominates`. Some analyses are computed lazily and cached on first use. Positions are instruction indices, and position `len(Instructions)` is the end of the program. Slice and map results are borrowed. `ir.Analyze(p)` provides the same analyses without validating `p`; the program must already be valid and must not change while the analysis is in use.

`Dominates(a, b)` reports whether instruction `a` dominates instruction `b`:

- within one block, `a <= b`;
- for an unreachable `b` (§6), always true;
- otherwise, whether `a`'s block dominates `b`'s.

**Building programs.** A front end can fill in a `Program` directly, use the `nyanjit.Builder` (which scopes values and Vars for it), or use `ir.ProgramBuilder`, a thin appender for SSA-style front ends. The Builder gives labels no parameters: its programs merge values that differ by path through Vars, and use block parameters only internally (§5.2). `ProgramBuilder` keeps block parameters:

- `NewProgramBuilder()` starts a program with an empty entry block, `Entry()`.
- `Param(entry, type)` declares a scalar function parameter in the entry block.
- `NewBlock(paramTypes...)` creates a block with parameters, and `BlockParam(block, i)` returns the ID of parameter `i`.
- `Append(block, instruction)` adds an instruction to any open block and returns its ID. Operands are earlier IDs. Vars and scopes are not available.
- `Jump`, `Branch` (with a `PredicateExpr` over IDs), `Switch` and `Return` (one value, or a 128-bit low/high pair) terminate a block.
- `Seal()` drops the blocks unreachable from the entry, lays the rest out in reverse postorder so dominators come first, numbers the instructions in that order and seals the program. A `*ValidationError` from it carries the builder's ID in `Index`.

The builder checks operand IDs and edge argument counts as it goes; `Seal` returns the first error.

## 2. Values and numbering

- **Value IDs.** Every instruction occupies one index, and an instruction that produces a value is referred to by that index. `Opcode.ProducesValue` says which opcodes produce one. Every opcode does except `OpSet`, `OpScopeEnter`, `OpScopeLeave`, `OpStore8`, `OpStore16`, `OpStore32`, `OpStore64`, `OpVStore`, `OpAtomicStore`, `OpFence` and `OpPrefetch`.
- **Operands.** `A`, `B`, `C` and `Args` hold operand value IDs. Each operand must name a value-producing instruction with a lower index than the user. Where the user is in a different block from the definition, the dominance rule (§6) also applies.
- **Defined once.** Values are immutable. Mutable state lives in Vars (§7) or, in raw IR, is passed between blocks as label parameters (§5).
- **Unused fields.** An opcode's unused fields must be zero or empty. This covers `A`, `B`, `C`, `Imm`, `Args`, `Data`, `Symbol`, `Variable`, `Effect`, `Contract`, `Aligned` and a non-native `ByteOrder`. `EntryReg` must be 0, or -1 on `OpLabelParam`. The compiler assigns registers; source IR never names them.

## 3. Types

`ir.Type` (`abi.ValueType`) is `{Kind, Lanes, LaneBits}`:

| Kind | Valid shapes |
|---|---|
| `ScalarKind` | `Lanes == 1`, `LaneBits` of 8, 16, 32, 64 or 128 |
| `VectorKind` | `Lanes >= 2`, `LaneBits` of 8, 16, 32 or 64, and at most 512 bits in total |
| `MaskKind` | `2 <= Lanes <= 64`, `LaneBits == 1`: one boolean per lane, with a private physical representation |

`ir.Scalar(bits)`, `ir.Vec(lanes, bits)` and `ir.Mask(lanes)` construct them, and return the zero `Type` for an invalid shape.

**Instruction types.**
- **Scalars:** an instruction's type is `Type` if set; otherwise it is `Scalar(Bits)`, where `Bits == 0` means 64. A scalar instruction has `Bits` of 0, 8, 16, 32 or 64, and an explicit scalar `Type` must equal `Scalar(width)`. No instruction produces a 128-bit scalar.
- **Vectors and masks:** these need an explicit `Type`.
  - For a vector, `Bits` is the lane width.
  - `OpVCompare` is the exception: its `Type` is the mask, and its `Bits` is the lane width of its input vectors.
  - A mask label parameter has `Bits == 64`.
- **128-bit integers.** These exist only as pairs of 64-bit values, low word first:
  - `ResultParts` for a 128-bit return;
  - two `Initial` values and two `OpGet`/`OpSet` parts for a 128-bit Var;
  - two entry parameters.

**Integers and floats.** Scalars are untyped bit patterns. Integer operations treat them as unsigned or two's complement as each opcode says. Floating-point operations treat 32- and 64-bit scalars as IEEE 754 binary32 and binary64 bit patterns. There is no separate float type, and the register bank is chosen by lowering.

**Narrow values in registers.** Values narrower than the target register are kept zero-extended. Comparisons in branch predicates read that register image (§5.3).

## 4. Opcodes

The opcodes, branch conditions and byte orders are defined in package `op`, which the backend layers share. `ir` re-exports every name (`ir.OpAdd` is `op.OpAdd`), so IR code needs only `ir`.

### Conventions

- `w` is the instruction's width: `Bits`, or 64 when `Bits == 0`.
- "Same width" means the operand's width must equal `w`. Validation checks these widths.
- Arithmetic wraps modulo 2^w. There is no checked overflow. No operation traps, except a division with `DivisionUnchecked` semantics on the inputs it excludes (§4.2). Atomic operations require a naturally aligned address; a misaligned one is a validity rule broken, like an invalid address, not an operation that traps (§4.5).

### 4.1 Parameters and constants

| Opcode | Operands | Result | Semantics |
|---|---|---|---|
| `OpParam` | none | scalar `w` | The next entry parameter: the k-th `OpParam` in instruction order is parameter k. In a control program, every `OpParam` must be in the entry block's segment, and that step's `Arity` counts them. Vector parameters are invalid. A 128-bit parameter is two consecutive 64-bit parameters, low first. |
| `OpConst` | `Imm`, optional `Symbol` | scalar `w` | The constant `Imm`, which must fit in `w` bits. With `Symbol` set, the value is supplied at compile time from `compile.Func.Constants`, or when a template is linked. `ir.MathPi4Symbol` is provided by the compiler when unbound. |
| `OpLabelParam` | `Imm` = parameter index | the label parameter's type | Parameter `Imm` of the label that begins this block (§5.2). `EntryReg` must be 0 or -1. |

### 4.2 Integer arithmetic and bitwise operations

| Opcode | Operands | Semantics |
|---|---|---|
| `OpAdd`, `OpSub`, `OpMul` | `A`, `B` same width | `A+B`, `A−B` and the low `w` bits of `A×B`, modulo 2^w. |
| `OpAnd`, `OpOr`, `OpXor` | `A`, `B` same width | Bitwise. |
| `OpAddScaled` | `A`, `B` same width, `Imm` | `A + B×Imm` modulo 2^w. This is the canonical form of a multiply-and-add, so selection can use addressing modes. |
| `OpMulHigh` | `A`, `B` same width | The upper `w` bits of the full unsigned 2w-bit product. |
| `OpMulHighSigned` | `A`, `B` same width | The upper `w` bits of the full signed 2w-bit product. |
| `OpCarryAdd` | `A`, `B` same width | 1 if the unsigned `A+B` overflows `w` bits, else 0, at width `w`. |
| `OpBorrowSub` | `A`, `B` same width | 1 if `A < B` unsigned, else 0, at width `w`. |
| `OpAddWithCarry` | `A`, `B`, `C` same width | `A + B + (C & 1)` modulo 2^w. Only the low bit of `C` is used. |
| `OpSubWithBorrow` | `A`, `B`, `C` same width | `A − B − (C & 1)` modulo 2^w. |
| `OpLeadingZeros`, `OpTrailingZeros` | `A` same width, `w <= 64` | The number of zero bits above the highest set bit, or below the lowest set bit. A zero operand gives `w`. |
| `OpSelect` | `A` any scalar; `B`, `C` same width | `B` if `A != 0`, else `C`. All three operands are evaluated. |
| `OpDiv`, `OpDivSigned` | `A`, `B` same width, `Imm` = `DivisionSemantics` | `A÷B`, unsigned or signed, truncated toward zero. |
| `OpRem`, `OpRemSigned` | `A`, `B` same width, `Imm` = `DivisionSemantics` | The remainder of `A÷B`, unsigned or signed; a signed remainder has `A`'s sign. |
| `OpCompare` | `A`, `B` same width (`w <= 64`); `Imm` = `BranchCondition` | 1 if `A cond B`, else 0, at width `w`. Unsigned and equality conditions compare the `w`-bit patterns. Signed conditions treat them as `w`-bit two's complement, so the register-image caveat of predicates (§5.3) does not apply. Pure; it never traps. |

**Division semantics.** `Imm` says what a division does for a zero divisor and for the signed `MIN ÷ −1`, whose quotient does not fit:

| `DivisionSemantics` | `x ÷ 0` | `x % 0` | `MIN ÷ −1` | `MIN % −1` |
|---|---|---|---|---|
| `DivisionZero` (0, the Builder's default) | 0 | `x` | `MIN` | 0 |
| `DivisionAllOnes` (1) | all ones (`−1` signed) | `x` | `MIN` | 0 |
| `DivisionUnchecked` (2) | unspecified, may trap | unspecified, may trap | unspecified, may trap | unspecified, may trap |

`DivisionZero` gives arm64's results and `DivisionAllOnes` RISC-V's; other targets add a guard. `DivisionUnchecked` emits the bare instruction: the front end promises a nonzero divisor and no signed `MIN ÷ −1`, and x86 raises SIGFPE when the promise is broken. Division is defined at 8, 16, 32 and 64 bits; 128-bit values have none.

### 4.3 Shifts, rotations and width changes

| Opcode | Operands | Semantics |
|---|---|---|
| `OpShiftLeft`, `OpShiftRight` | `A` same width, `Imm < w` | Logical shift by `Imm`. Validation rejects `Imm >= w` for scalars. |
| `OpShiftRightArithmetic` | `A` same width, `Imm < w` | Shift right by `Imm`, filling with the sign bit (bit `w−1`). |
| `OpRotateLeft`, `OpRotateRight` | `A` same width, `Imm < w` | Rotate by `Imm`. |
| `OpShiftLeftBy`, `OpShiftRightBy` | `A` same width; `B` an unsigned scalar of at most 64 bits | Logical shift by `B`. Counts of `w` or more give 0. |
| `OpShiftRightArithmeticBy` | as above | Counts of `w` or more fill every bit with the sign bit. |
| `OpRotateLeftBy`, `OpRotateRightBy` | as above | Rotate by `B mod w`. |
| `OpCastUnsigned` | `A` any scalar of at most 64 bits | Truncate, or zero-extend, to `w` bits. |
| `OpSignExtend` | `A`; `Imm` of 8, 16 or 32, with `Imm <= w` and `Imm <=` width of `A` | Sign-extend the low `Imm` bits of `A` to `w` bits. The Builder emits `w == 64`. |
| `OpReverseBytes` | `A` same width, `w` of 16, 32 or 64 | `A` with its `w/8` bytes in reverse order. Pure. Targets without a byte-reverse instruction (riscv64 without Zbb) expand it to shifts and masks. |

The Builder folds immediate counts of `w` or more before emitting IR: zero for logical shifts, `w−1` for arithmetic shifts, and `count mod w` for rotations.

### 4.4 Floating point

All floating-point opcodes need `Bits` of 32 or 64 and operands of that width, except `OpFConvert` and the integer conversions. Results are IEEE 754 operations rounded in the target's current floating-point environment, which by default rounds to nearest, ties to even. The IR does not change that environment. Whether subnormals are flushed depends on the environment the host leaves in place.

| Opcode | Operands | Semantics |
|---|---|---|
| `OpFAdd`, `OpFSub`, `OpFMul`, `OpFDiv` | `A`, `B` | `A+B`, `A−B`, `A×B`, `A÷B`, each rounded once. |
| `OpFAdd` with `Contract` | `A`, `B`, at least one of them an `OpFMul` | Permits contracting a single-use `FMul` operand into one fused multiply-add when the target has FMA. The result can then differ from separate rounding, and depends on the CPU. |
| `OpFMA` | `A`, `B`, `C` | `A×B + C` with one rounding. It needs a hardware FMA: there is none on 386, and amd64 needs FMA3 in the CPU profile. Otherwise lowering returns `ErrBackendLimit`. |
| `OpFSqrt` | `A` | Square root. −0 gives −0, and a negative nonzero operand gives NaN. |
| `OpFConvert` | `A` of `96 − Bits` bits | Converts binary64 to binary32 (`Bits == 32`, rounding in the current mode) or binary32 to binary64 (`Bits == 64`, exact). |
| `OpFFromSigned`, `OpFFromUnsigned` | integer `A` of 32 or 64 bits | Converts the signed or unsigned integer to binary32 or binary64 (`Bits`), rounding in the current mode. |
| `OpFToSigned`, `OpFToUnsigned` | float `A` of 32 or 64 bits, `Imm` = `ConversionSemantics` | Truncates toward zero to a signed or unsigned integer of `Bits` (32 or 64). |
| `OpFCompare` | `A`, `B`; `Imm` = `FloatCondition` | 1 when the condition holds and 0 otherwise, at width `Bits`. |
| `OpFProductError` | `A`, `B`, `C`, `Bits == 64` | The exact residual `A×B − C`, where `C` must be `A×B` rounded to binary64, and `A` and `B` must be finite and scaled so that no intermediate of a Dekker split overflows or underflows. Validation checks only references and widths. Results outside this contract are unspecified and may differ by CPU profile: targets with FMA use it, and others use Dekker's algorithm. |

**Conversion semantics.** For `OpFToSigned` and `OpFToUnsigned`, `Imm` says what a NaN or a value outside the result's range gives. `ConversionSaturate` (0, the Builder's default) clamps to the range (0 to the maximum for unsigned results) and gives 0 for NaN, as Wasm's saturating truncation does. `ConversionNative` (1) gives what the target's conversion instruction gives: it never traps, but the result depends on the target.

**Comparisons.** `FloatCondition` names IEEE 754 relations in pairs, each the negation of the other (`c^1`): `FloatEqual` and `FloatNotEqual` (Go's `==` and `!=`), `FloatLess` and `FloatUnorderedGreaterEqual`, `FloatLessEqual` and `FloatUnorderedGreater`, `FloatGreater` and `FloatUnorderedLessEqual`, `FloatGreaterEqual` and `FloatUnorderedLess`, `FloatOrdered` and `FloatUnordered`, `FloatOrderedNotEqual` and `FloatUnorderedEqual`. An ordered relation is false when either operand is NaN, and an unordered one is then true. −0 equals +0. Whether a quiet NaN raises the invalid-operation flag depends on the target.

**NaNs.** A NaN result is a quiet NaN. Its payload and sign bit depend on the target, so front ends that NaN-box must canonicalize. Signed zeros follow IEEE 754.

**Status flags.** Floating-point status flags may change. `abi.Report.FPStatusModified()` says whether code can modify them.

**Target notes.**
- All six targets emit scalar floating point. 386 requires SSE2.
- RISC-V keeps binary32 values NaN-boxed in FP registers.
- POWER keeps binary32 values in double format in FP registers.

These representation differences are invisible at the IR level.

### 4.5 Memory

**Addresses.** An address is a scalar value of 32 or 64 bits (`A`) that holds a valid address for the target. On a 64-bit target, a 32-bit address is zero-extended.

**Byte order.**
- `ByteOrderNative` uses the target's order; all six targets are little-endian.
- `ByteOrderLittleEndian` and `ByteOrderBigEndian` fix the order explicitly. Big-endian is valid at every width; a big-endian `OpLoad64` or `OpStore64` is a little-endian access with its bytes reversed (`ldbrx`/`stdbrx` on ppc64le, `revb.d` on loong64).

**Ordering and validity.** Memory operations are ordered (§8). The program must supply valid addresses, and keep Go-owned memory alive for the whole access. Stores emit no write barrier, so they must not store Go pointers.

| Opcode | Operands | Semantics |
|---|---|---|
| `OpLoad8` | `A`; `Bits` of 8 or 64 | Loads a byte, zero-extended to `w`. |
| `OpLoad16`, `OpLoad32` | `A`; `Bits` = 16 or 32; `Aligned`; `ByteOrder` | Loads a zero-extended value of the access width. |
| `OpLoad64` | `A`; `Bits` = 64; `ByteOrder` | Loads 64 bits. |
| `OpStore8` | `A`, `B` any scalar | Stores the low byte of `B`. |
| `OpStore16`, `OpStore32` | `A`; `B` of the access width or 64 bits; `Aligned`; `ByteOrder` | Stores the low 16 or 32 bits of `B`. |
| `OpStore64` | `A`, `B` of 64 bits; `ByteOrder` | Stores 64 bits. |
| `OpPrefetch` | `A`; `Imm` = `PrefetchRead` (0) or `PrefetchWrite` (1); `Bits` = 0 | Hints that `A` will soon be read or written. It defines no value, never faults, even for an invalid address, and has no architectural effect. |

**Alignment.**
- `Aligned` promises natural alignment for a 16- or 32-bit access. Without it, any byte alignment is permitted. Lowering can expand an access into byte operations when the target cannot use a suitable native access.
- `OpLoad64`/`OpStore64` addresses should be 8-byte aligned. Behaviour on misaligned 64-bit accesses is target-dependent.

**Atomics.** Atomic operations access `Bits` (8, 16, 32 or 64) bits at address `A` with the `MemoryOrder` in `Imm`. Loads, read-modify-writes and compare-and-swap return the old value, zero-extended at width `Bits`.

| Opcode | Operands | Semantics |
|---|---|---|
| `OpAtomicLoad` | `A`; `Bits` | Returns the value at `A`. |
| `OpAtomicStore` | `A`; `B` of width `Bits` | Stores `B`. It produces no value. |
| `OpAtomicAdd`, `OpAtomicAnd`, `OpAtomicOr`, `OpAtomicXor` | `A`; `B` of width `Bits` | Replaces the value at `A` with value op `B` (modulo 2^Bits) and returns the old value. |
| `OpAtomicSwap` | `A`; `B` of width `Bits` | Stores `B` and returns the old value. |
| `OpAtomicCompareSwap` | `A`; `B`, `C` of width `Bits` | Stores `C` if the value at `A` equals `B`, and returns the old value in either case: the store happened exactly when the result equals `B`. |
| `OpFence` | `Imm` only; `Bits` and `Type` zero | Orders memory by `Imm` without accessing it. It produces no value. |

- **Ordering.** `MemoryOrderSequential` (0) is the only `MemoryOrder`: all atomic operations and fences of all threads have one total order consistent with each thread's program order. An atomic load acquires and an atomic store releases, as Go's `sync/atomic` does: plain loads and stores before a release are visible to a thread whose atomic load reads that store. Other values of `Imm` are invalid.
- **Widths and alignment.** Atomic operations additionally require natural alignment; a misaligned atomic address is invalid. Depending on the target it may raise SIGBUS (arm64, riscv64, ppc64le, loong64) or be non-atomic (x86 split locks, which also fault under Linux `split_lock_detect=fatal`, and 386 64-bit accesses). Validation rejects `Aligned`, which atomics never need, and every byte order but native. 128-bit atomics are not available.
- **Races.** A plain access that races with another access to the same bytes gives an unspecified value, byte by byte. It never faults on that account.
- **Lowering.** Every target encodes every width; there is no software fallback. Each atomic is one target expansion, so a load-reserved/store-conditional loop never contains a spill. Selection returns `ErrBackendLimit` for a form a target's `Policy.CanAtomic` rejects.

**Stack areas.** `Program.StackAreas` lists fixed-size storage that lives for one invocation of the function. A `Segment` declares its own areas in `Segment.StackAreas`, and their indices continue those of earlier segments.

| Opcode | Operands | Semantics |
|---|---|---|
| `OpStackAddress` | `Imm` = area index; `Bits` = 32 or 64, the target's pointer width | The address of `StackAreas[Imm]`. Pure: lowering may compute it again. |

- `StackArea{Bytes, Align}`: `Bytes` is 1 to 65,536 and `Align` is 1, 2, 4, 8 or 16. The areas of a function total at most 65,536 bytes.
- Each area is one instance per call, laid out statically. An address taken in a loop is the same on every iteration, and the area keeps its contents until the function returns.
- Every area is zeroed when the function is entered, before any instruction runs, so a program never reads earlier frames' data.
- With `abi.StackPlatform` the areas live in the frame; with `abi.StackNoUse` they live in the workspace that `Code.Call` supplies (`Report.WorkspaceBytes`). Either way their words count as spill words, and in the frame as frame bytes (§10).
- An address may be passed to a C callee that does not keep it, or returned; it dangles once the function returns. Stores emit no write barrier, so an area must not hold Go pointers. `nyanjit.StackData[T]` accepts only pointer-free `T` and exposes an area as a `Data` view, with bounds-checked `Index`.
- Lowering checks the target rules: `Bits` must be the pointer width, `Align` at most 16 (4 on 386, whose frames are 4-byte aligned), otherwise `ErrBackendLimit`. A `ContextGoManaged` function with areas cannot make calls (`ErrUnsupported`): a call can move the goroutine stack, and the frame has no pointer map that could fix the addresses.

### 4.6 Calls and system calls

| Opcode | Operands | Semantics |
|---|---|---|
| `OpCallC` | `Args[0]` = native code address; `Args[1:]` = arguments; optional `Effect`; `Imm` = `abi.FloatPassing` | Calls through the platform C ABI and returns the integer result register, or the FP result register for a float result. |
| `OpCallGo` | `Args[0]` = Go function code pointer; `Args[1:]`; optional `Effect`; `Imm` = `abi.FloatPassing` | Calls a pointer-free Go function with scalar arguments through the Go register ABI (ABI0 word slots on 386), and returns the result register. |
| `OpSyscall` | `Args[0]` = system call number; up to 6 more `Args` | Makes a raw Linux system call and returns its raw result, with errors as negative values. |

**Arguments.**
- Call arguments are scalars of at most 64 bits.
- For C calls, 8- and 16-bit values are zero-extended. 32-bit values are extended as the psABI says: loong64 and riscv64 sign-extend them, including unsigned ones.
- On 386, a 64-bit argument takes two stack words.
- Go calls on 386 can't take two adjacent arguments narrower than 32 bits, because ABI0 packs them.
- An `OpSyscall` on 386 is `INT 0x80`: it passes the low 32 bits of the number and of each argument (EAX, then EBX, ECX, EDX, ESI, EDI and EBP), and its 64-bit result is EAX sign-extended, so `-errno` reads as on the 64-bit targets.

**Results and stacks.** The Builder emits call results as 64-bit values (`Bits == 0`), or 32-bit ones for an `abi.FloatResult32` result; other results of narrower `Bits` are not specified. Calls need a platform stack (`abi.StackPlatform`).

**Float passing.** `Imm` is an `abi.FloatPassing` (`Builder.CallCWithFloats`, `CallGoWithFloats`): bit *i* marks `Args[1+i]` as a value that travels in a floating point register, and `FloatResult32` or `FloatResult64` marks a binary32 or binary64 result. Zero passes everything in integer registers. The IR stays untyped: a marked value is the IEEE bit pattern, and lowering converts at the boundary to the target's representation (riscv64 NaN-boxes binary32; ppc64le holds it in double format). `compile.Func.Floats` marks the generated function's own parameters and result the same way.
- Validation requires marks only for arguments the call passes, each 32 or 64 bits wide, at most one result width, and a call `Bits` equal to that width. `OpSyscall` keeps `Imm == 0`.
- Integer and float arguments take their own register sequences, except that a ppc64le C float argument skips its positional GPR. On 386 every argument stays in stack words; only a C result changes, arriving in x87 `ST(0)`.
- **386 x87:** `FLD` turns a signaling NaN into a quiet NaN, so a 386 C float result (of a call, or of a C entry) is the one exception to bit-exact float passing, in line with "NaN payloads are target-dependent".
- `FloatPassing` describes prototyped callees. For a variadic C callee, amd64 sets `AL` to the number of XMM argument registers used, so marked variadic `double`s are correct there. On riscv64 and loong64 a variadic `double` travels in integer registers: leave it unmarked. ppc64le passes variadic floats in both register files; leave them unmarked there too.
- At most 8 arguments may be marked, within the target's argument count (§10); no argument is passed on the stack except on 386.
- A Go-managed C call with float passing goes through a generated adapter, so it takes one argument fewer than an integer one.

**`Effect`.** An `abi.CallEffect` replaces the conservative effect the ABI would otherwise assume. It may narrow the clobbered registers and the memory effects, and it must include every transitive callee's effects.
- Validation requires a `FunctionCall` effect for the call's ABI, naming an architecture, with no number register, describing a result, and clobbering its result register (an FP one for a float result).
- Lowering rejects an effect that differs from the selected target's convention, including an `Effect.Floats` other than the call's `Imm`; `CallCWithEffect` and `CallGoWithEffect` take their float passing from it.
- `OpSyscall` takes no `Effect`.

**Argument limits.** Argument counts are limited per target (§10).

**Tail calls.** `TermTailCall` (§5.1) ends a block with a C call whose result is the function's result. Lowering accepts it only for a `Func` with the C ABI in `ContextForeign` or `ContextExternal`, and returns `ErrUnsupported` otherwise. Its arguments must fit in argument registers; on 386 the callee's argument words must not exceed the entry's own, which it reuses. The interpreter runs it as `OpCallC` followed by a return.

### 4.7 Vectors and masks

Every vector operation is valid on every target. Native lowering happens only in straight-line programs without Vars, on amd64 and 386 (SSE2), arm64 (NEON) and loong64 (LSX). It processes the vector in 128-bit chunks, so wider vectors are split.

Everything else is scalarized lane by lane with the same results: riscv64 and ppc64le, any vector in a control program or a program with Vars, and operations a target has no native form for. `abi.Report` lists how each vector operation was lowered.

**Lane order.** Lane 0 is at the lowest address. Within a lane, bytes follow the instruction's byte order.

| Opcode | Operands | Result | Semantics |
|---|---|---|---|
| `OpVLoad` | `A` address | vector `Type` | Loads `Type.Bits()/8` bytes from any alignment, in memory lane order. `ByteOrder` must be native: validation rejects other byte orders on vector memory operations. |
| `OpVStore` | `A` address; `B` of `Type` | none | Stores exactly the vector's bytes, at any alignment. |
| `OpVConst` | `Data` of `Type.Bits()/8` bytes | vector | Lane 0 first, each lane little-endian. |
| `OpVZero` | none | vector | All lanes zero. |
| `OpVSplat` | `A` = `Scalar(LaneBits)` | vector | `A` in every lane. |
| `OpVExtract` | `A` vector; `Imm < Lanes` | `Scalar(LaneBits)` | Lane `Imm`. |
| `OpVInsert` | `A` of `Type`; `B` = `Scalar(LaneBits)`; `Imm < Lanes` | `Type` | `A` with lane `Imm` replaced by `B`. |
| `OpVInsertZero` | as `OpVInsert` | `Type` | Same result as `OpVInsert`. The scalarizer emits it to build a vector up from `OpVZero`, but any vector `A` is valid. |
| `OpAdd`, `OpSub`, `OpMul`, `OpAnd`, `OpOr`, `OpXor` | `A`, `B` of `Type` | `Type` | Lane-wise, modulo 2^LaneBits. |
| `OpShiftLeft`, `OpShiftRight`, `OpShiftRightArithmetic`, `OpRotateLeft`, `OpRotateRight` | `A` of `Type`; `Imm` | `Type` | Lane-wise. Unlike the scalar forms, `Imm` may be `>= LaneBits`: logical shifts then give 0, arithmetic shifts fill with the sign bit, and rotations reduce the count modulo `LaneBits`. |
| `…By` shifts and rotations | `A` of `Type`; `B` a scalar of at most 64 bits, or a vector of `Type` | `Type` | Lane-wise, with the scalar `…By` semantics at `LaneBits`. A scalar `B` counts for every lane, and a vector `B` gives a per-lane count. |
| `OpVCompare` | `A`, `B` of the same vector type; `Imm` = `BranchCondition`; `Bits` = lane width | `Mask(Lanes)` | Lane-wise integer comparison at the lane width. Signed conditions treat each lane as two's complement. |
| `OpVMaskBits` | `A` mask | scalar of `Bits >= Lanes` (at most 64) | Bit i is lane i's boolean; higher bits are zero. |
| `OpVCarrylessMul` | `A`, `B` of `Type` with 64-bit lanes and an even lane count; `Imm` ≤ 3 | `Type` | For each 128-bit chunk, multiplies lane `Imm&1` of `A`'s chunk by lane `Imm>>1` of `B`'s chunk as polynomials over GF(2), without reduction. The chunk's result is the low word, then the high word. |
| `OpVShuffle` | `A`, `B` of `Type`; `Data` = one index byte per lane, each `< 2·Lanes` | `Type` | Lane i is lane `Data[i]` of the concatenation `A‖B`: indices from `Lanes` on select from `B`. A broadcast is a shuffle with every index equal (and `A` = `B`). |
| `OpVExtendUnsigned`, `OpVExtendSigned` | `A` = `Vec(L, w)` with `w` ≤ 32 and `Imm` 0; or `A` = `Vec(2L, w)` with `Imm` 0 or 1 | `Vec(L, 2w)`, at most 512 bits; `Bits` = `2w` | Each lane of `A` (or of its low half, `Imm` 0, or high half, `Imm` 1) zero- or sign-extended to `2w` bits. |

Native shuffles and extends work per 128-bit chunk too. A shuffle chunk whose lanes come from at most two input chunks is one target sequence; SSE2 has no general byte shuffle (no SSSE3 `PSHUFB` in the baseline), so 8- and 16-bit shuffles it cannot express with `PSHUFD`, `PSHUFLW`/`PSHUFHW` and `PUNPCK*` are built lane by lane. A widening extend is native when its input is at least 128 bits wide.

Only `OpVCompare` creates masks, and masks have no memory form. They can pass through label parameters and Vars, and `OpVMaskBits` turns one into a scalar.

### 4.8 Variables and scope markers

| Opcode | Operands | Semantics |
|---|---|---|
| `OpGet` | `Variable`; `Imm` = part (0, or 0/1 for a 128-bit Var); `Bits` = lane width of the Var's type | Reads a snapshot of the Var's current value (§8). |
| `OpSet` | `Variable`; `Imm` = part; `A` of the Var's type | Replaces the stored value. It produces no value. |
| `OpScopeEnter` | `Imm` = scope ID; `Args` = the scope's captured values | Enters a scope (§7). It produces no value, and its `Type` and `Bits` must be zero. |
| `OpScopeLeave` | `Imm` = scope ID | Leaves the innermost scope. It produces no value. |

These four opcodes need a scoped program, one with at least one Var or `ScopeExit`.

## 5. Blocks and control flow

### 5.1 Structure

`Control` describes labeled control flow:

| Field | Meaning |
|---|---|
| `Labels` | One entry per label, each `true`. |
| `LabelScopes` | The scope each label belongs to. |
| `Params` | Each label's parameter count. |
| `ParamBits` | Each label's parameter widths, of 8, 16, 32 or 64; `len(ParamBits[l]) == Params[l]`. |
| `ParamTypes` | Optional, but either empty or present for every label. Full parameter types, including vectors and masks. Each must agree with `ParamBits`, which holds 64 for a mask. |
| `Steps` | The program's layout, in order. |

Each `ControlStep` is either a label or a segment.

**A label step** (`IsLabel`) binds `Label` at the current position. Its other fields must be zero or empty, except `To` and `Other`, which may be `-1`.

**A segment step** covers instructions `[Start, End)` and scope exits `[LeaveStart, LeaveEnd)`, then ends with a terminator:
- Segments are contiguous: the first starts at 0, each starts where the previous ended, and together they cover every instruction and every `ScopeExit`.
- Every segment follows at least one label step, and several labels may precede the same segment. Such labels name one block, so they must declare the same parameters.
- Every label is bound exactly once.

**Terminators:**

| `Term` | Fields used | Meaning |
|---|---|---|
| `TermJump` | `To`, `Args` | Continue at label `To`, passing `Args`. |
| `TermBranch` | `Predicate`, `Values`, `Result`, `To`/`Args`, `Other`/`OtherArgs` | If the predicate holds, continue at `To` with `Args`; otherwise at `Other` with `OtherArgs`. |
| `TermReturn` | `Result`, optional `ResultParts` | Return the scalar value `Result`, or the 128-bit pair `ResultParts` (`[low, high]`, `ResultParts[0] == Result`). |
| `TermTailCall` | `Args` | Call the native address `Args[0]` with `Args[1:]` through the C ABI, as `OpCallC` does, and return the callee's integer result as the function's own; the callee reuses the caller's frame. `Args[0]` is a 32- or 64-bit scalar and each argument a scalar of at most 64 bits. `Result` is ignored. A program with a tail call returns scalars, never `ResultParts`. |
| `TermSwitch` | `Selector`, `Cases`, `To`, `Args` | Continue at `Cases[Selector]` when `Selector` is below `len(Cases)`, and at `To` otherwise; every edge passes `Args`. |

Fields a terminator doesn't use must be empty. Unused label fields must be 0 or -1. `Result` is ignored on jumps. Either every return of a program is 128-bit (`ResultParts`) or none is.

**Switch.** `TermSwitch` is a dense, bounded table, like Wasm's `br_table`; there are no label addresses or computed jumps. `Selector` is a step value (§6), a scalar of at most 64 bits, compared unsigned on its zero-extended register image, so a narrow selector needs no extension. `Cases` holds at least one label, and labels may repeat; an empty switch is a `TermJump`. `To` (the default) and every case receive `Args`, so their parameter types must be identical. `Other`, `OtherArgs`, `Predicate`, `Values` and `ResultParts` must be empty, and `Result` 0 or -1. Other terminators leave `Selector` 0 and `Cases` empty. Each case counts as one predicate node (§5.3). Lowering coalesces runs of equal targets, sends selectors at or above `len(Cases)` to the default with one comparison, and picks the case with a binary search on the run bounds. The optimized tier uses a jump table instead when there are at least 8 runs and they start at least 40% of the cases the selector's width can reach (on 386, for selectors of at most 32 bits). The table's index is clamped to `len(Cases)` without a branch, so even a mispredicted bounds check reads an entry of the table, and the last entry is the default. Hardened functions (`compile.Func.Harden`) and the baseline tier always use compare trees, so their code has no indirect jump.

**Fallthrough.** The IR has no implicit fallthrough: every edge names its label. Step order is the code layout, and the emitter drops a jump to the next block. The Builder's implicit edges, `Bind` after open code and the false edge of `Branch`, become explicit steps. A Builder `Branch(p, a)` followed directly by `Jump(c)` becomes one `TermBranch` step with `To == a` and `Other == c`.

### 5.2 Blocks and label parameters

**Blocks.** A block is a segment together with the label steps that precede it. The entry block is the first segment, and the first step's label is the entry label. Edges come from `TermJump` (`To`), `TermBranch` (`To` and `Other`) and `TermSwitch` (`To` and each of `Cases`); `TermReturn` and `TermTailCall` have none.

**Label parameters** are SSA block parameters. A label with `Params[l] > 0` receives, along every incoming edge, exactly `Params[l]` arguments. Each argument's type must equal the parameter's type: `ParamTypes[l][i]`, or `Scalar(ParamBits[l][i])`. Inside the block, `OpLabelParam` with `Imm == i` reads parameter `i`. Its type must equal the parameter's type, and for a mask parameter `Bits` is 64. Phis map directly onto label parameters.

Label parameters belong to raw IR (`ProgramBuilder`, or a `Program` filled in directly) and to lowering, which adds them to carry cross-block values (§6). The Builder's labels take no parameters; its only parameterized label is the private in-bounds continuation of `IndexedAddress` and `Data.Index`, which receives the checked base and index on its one edge so that a hardened bounds check can clamp the index.

Two rules apply:
- The entry label takes no parameters.
- A block with parameters needs at least one incoming edge, to any of its labels.

**Scope boundaries.** When the program has Vars, every edge's target label must belong to the scope active at the end of the jumping segment (`LabelScopes[l] == ScopeAt(step.End)`), so a branch can't cross a scope boundary. Programs without Vars don't check `LabelScopes`.

### 5.3 Predicates

`Predicate` is a `*PredicateExpr` tree, or a DAG, since subtrees may be shared:

| Kind | Meaning |
|---|---|
| `PredicateCompare` | `Value <Condition> Other` when `WithValue`, else `Value <Condition> Immediate`. |
| `PredicateAnd`, `PredicateOr` | Short-circuit conjunction or disjunction of `Left` and `Right`. |
| `PredicateNot` | Negation of `Left`. `Right` must be nil. |

`Condition` is a `BranchCondition`:

| Kind of comparison | Conditions |
|---|---|
| Equality | `BranchEqual`, `BranchNotEqual` |
| Signed | `BranchLess`, `BranchGreaterEqual`, `BranchLessEqual`, `BranchGreater` |
| Unsigned | `BranchBelow`, `BranchAboveEqual`, `BranchBelowEqual`, `BranchAbove` |

**Operands.** Comparison operands must be scalars of at most 64 bits, and `Other` must have the same type as `Value`. Every operand of a comparison (`Value`, `Other`, `Anchor`) must be listed in the step's `Values`. `HasAnchor`/`Anchor` names the arithmetic value whose flags the comparison mirrors (for example, the carry of an `OpAdd`), so a target may reuse those flags. It doesn't change the result. `Builder.Carry` anchors an unsigned `Below`/`AboveEqual` comparison; `Builder.Overflow` anchors `IsNegative((a^s)&(b^s))` for a sum `s = a+b`, `IsNegative((a^b)&(a^s))` for a difference and `MulHighSigned(a,b) != p>>(w−1)` for a product `p` (narrower than 64 bits, `0 − (p >> (w−1))` with a logical shift). A target reuses the anchor's overflow flag (`machine.BranchOverflow`) only after checking that shape against the anchor's operands.

**Comparison width.** Comparisons read the operands' register image: narrow values zero-extended, compared at the target's register width (64 bits, or 32 on 386, where 64-bit values are compared as pairs). A signed condition on an operand narrower than 64 bits therefore depends on the target. Sign-extend such operands to 64 bits first (`OpSignExtend` with `Bits == 0`), as `Builder.Compare` does. Unsigned and equality conditions on zero-extended narrow values agree everywhere.

**Immediates.** `Immediate` is used as its 64-bit two's-complement pattern. Keep immediates within the operand's range: for equality and unsigned conditions on an operand narrower than 64 bits, validation rejects an immediate outside `[0, 2^w−1]`; the Builder turns unsigned immediates above 4095 on narrow operands into constants. Some targets can't compare an immediate outside ±4095 when the operand is in a register home, and return `ErrBackendLimit`. Use a constant operand (`WithValue`) to avoid that.

**Values.** `Builder.PredicateValue(p, w)` turns a predicate into a 0/1 value of `w` bits: each comparison node becomes an `OpCompare` at its operands' width, `And`/`Or` become `OpAnd`/`OpOr` and `Not` an `OpXor` with 1. Both sides of a short circuit are evaluated, which is unobservable because comparison leaves are pure (§8). `Builder.CompareValue` emits one `OpCompare`. Selection fuses an `OpSelect` whose condition is a single-use `OpCompare` into the comparison and a flag-reading select (`CMOVcc`, `CSEL`, `isel`) on the targets that have one.

**Size limit.** A predicate's expanded size counts each use of a shared subtree. It is limited to `MaxPredicateNodes` (16,384), both per predicate and summed over all of a program's predicates. Each case of a `TermSwitch` counts as one node, in the switch and in the program's sum. A larger program fails validation with `ErrPredicateLimit`, and compiling it, or building it with a Builder, returns a `compile.LimitError` of kind `LimitPredicateNodes`. Cycles are invalid. Predicate walks use explicit worklists, so depth is unlimited.

## 6. The dominance rule

Values cross blocks by dominance.

- **Same block.** A value may be used in the block that defines it, after its definition.
- **Other blocks.** A value may be used in any other block that its defining block dominates, provided the value's index is lower than the use's position.
  - A block D dominates a block U when every path from the entry to U passes through D.
  - Because definitions precede uses in instruction order, a front end lays blocks out so that dominators come first. Reverse postorder does this.
- **Unreachable blocks.** Blocks unreachable from the entry are dominated by every block, so they may use any earlier value.
- **Where it applies.** The rule covers every value reference:
  - instruction operands;
  - call and scope `Args`;
  - Var `Initial` values;
  - a step's `Values`, `Args`, `OtherArgs`, `Result`, `ResultParts` and `Selector`;
  - predicate operands;
  - scope `Exports`.
- **Merges.** A value defined in one arm of a branch doesn't dominate the merge block. Keep it in a Var, or, in raw IR, pass it as a label argument.
- **Allowed shapes.** Irreducible control flow (loops with several entries) is supported, and so are critical edges: a `TermBranch` whose edges both pass arguments. Block parameters and dominance-scoped values mix freely: a label parameter is itself a value defined in its block.
- **Lowering.** Lowering passes cross-block values through internal label parameters: it computes where each such value is live and appends a parameter for it to every block that needs it. Programs without cross-block uses lower exactly as they did before this rule. In an unreachable block, references to values from other blocks, and the arguments it would pass along its edges, become zero values of the right type; that code never runs. `Unit.RegisterScopes` still reports source value IDs.

`Analysis.Dominates(a, b)` answers the same question for any two instructions (§1).

**Scope expiry.** The Builder makes values defined inside a scope expire at its `Leave` unless they are exported, and rejects later uses. `ir` validation does not enforce expiry. It checks only that `Hidden` and `Exports` entries are earlier values, and that `Exports` follow the dominance rule at the leaving segment. `Hidden` is descriptive metadata that lowering doesn't use.

## 7. Scopes and variables

### 7.1 Scopes

A `ScopeExit{ID, Parent, Enter, Position, Hidden, Exports}` describes one nested scope. Scope 0 is the implicit root.

| Field | Rule |
|---|---|
| `ID` | `> 0` and unique. |
| `Parent` | 0 or an existing scope with a lower `ID`. A child lies within its parent: `parent.Enter <= Enter` and `Position <= parent.Position`. |
| `Enter` | The index of the scope's `OpScopeEnter`, whose `Imm` is `ID`. |
| `Position` | One past the scope's `OpScopeLeave`, whose `Imm` is `ID`. `Enter < Position <= len(Instructions)`. Exits are listed in nondecreasing `Position`. |
| `Hidden` | Values that cease to be visible at the exit. Metadata only; see §6. |
| `Exports` | Values that stay usable after the exit. Lowering materializes them at the leave boundary. |

**Nesting.** `OpScopeEnter`/`OpScopeLeave` pairs must nest properly in instruction order: an enter's scope has the current innermost scope as its parent, a leave closes the innermost scope, and every scope is closed by the end. Leaving a scope belongs to the segment whose `[LeaveStart, LeaveEnd)` covers its `ScopeExit`.

**Active scope.** The scope active at position p (`Analysis.ScopeAt`) is the innermost scope with `Enter < p < Position`.

### 7.2 Variables

A `Variable{Type, Scope, Region, Declaration, Initial, Immutable}` is mutable storage for its region's lifetime. Its semantics are those of one home: loops need no merge copies. How a tier stores it is the compiler's choice; the optimized tier promotes most variables to SSA values, and the baseline tier keeps them in homes.

- **`Type`.** Any valid type. A 128-bit scalar Var has two 64-bit parts.
- **`Region`.** The scope whose lifetime the Var has. `Declaration` lies inside it.
- **`Initial`.** Required: one value per part, each of the part's type, defined before `Declaration`.
- **Mutable Vars** (`Immutable == false`):
  - `Scope == Region`;
  - the instructions at `Declaration + n` are `OpSet{Variable: v, A: Initial[n], Imm: n}`, executed in the scope `Scope`;
  - the declaration must come before the region's first terminating step. That is the entry-sequence rule: declare Vars before the scope's first `Jump`, `Branch` or `Return`. A jump immediately after the scope's `OpScopeEnter` doesn't count.
- **Immutable Vars** are the captures of a scope:
  - `Region != 0`;
  - `Scope` is the region's parent;
  - `Declaration == Region.Enter`;
  - `Set` is invalid;
  - the `Args` of the region's `OpScopeEnter` are the concatenated `Initial` values of the region's immutable Vars, in Var order. The Builder creates captures when a nested scope uses a value from an earlier block.
- **`OpGet` and `OpSet`** are valid only while the Var's region is entered, at or after `Declaration`, and inside the region's bounds. `Bits` equals the part's lane width, and `Type` is the part's type.

## 8. Effects, ordering and snapshots

**Ordered instructions** execute in instruction order relative to each other. Lowering never removes them, even when their result is unused. They are:

- `OpGet` and `OpSet`;
- `OpScopeEnter` and `OpScopeLeave`;
- every load and store, including `OpVLoad` and `OpVStore`;
- the atomic operations and `OpFence`, even when their result is unused;
- `OpPrefetch`;
- `OpSyscall`, `OpCallC` and `OpCallGo`, and the call of a `TermTailCall`, which follows every ordered instruction of its block.

Loads are ordered too, so a load after a store to the same address sees the store.

**Every other instruction is pure.** A pure instruction is evaluated at some point after its operands and before its first use. Pure instructions whose results are unused are not evaluated. Selection may also:

- duplicate or rematerialize them, for example by reloading constants in later blocks;
- fold them into users: constants into immediates, `OpAddScaled` into addressing modes, and literal comparison operands into predicate immediates;
- contract `FMul`+`FAdd` when `Contract` permits.

For defined inputs, these transformations are unobservable except for floating-point status flags and for a callee that changes the floating-point environment. `DivisionUnchecked` requires the caller to exclude its exceptional inputs (§4.2); those inputs may trap. Pure arithmetic is not ordered with respect to calls.

**`CallEffect` memory flags** (`MayReadMemory`, `MayWriteMemory`) describe the callee, for reports and register planning. They don't relax ordering: calls stay ordered with all memory operations.

**Snapshots.** `OpGet` returns a snapshot. It is an ordinary value, and a later `OpSet` does not change it.

## 9. Validation

`Program.Validate`, and through it `Seal` and `SealOwned`, checks everything in this document that is independent of the target:

- field usage;
- widths and types;
- operand references and the dominance rule;
- control structure;
- label parameters and edge arguments;
- predicates;
- scopes and Vars;
- call effects;
- stack areas: each area's size and alignment, their total, and each `OpStackAddress` index and width.
- atomic memory orders, and the address and operand widths of atomics.

**Errors.**
- An invalid program returns an `*ir.ValidationError{Index, Op, Rule}`, which wraps `ErrInvalidProgram`:
  - `Index` is the offending instruction, or -1 when the rule concerns a control step, label, scope or Var;
  - `Op` is that instruction's opcode;
  - `Rule` describes the broken rule. Its wording is not part of the API.
- Predicates above the node budget return an error wrapping `ErrPredicateLimit` instead.

**After validation.** Compiler entry points accept only validated programs. Target-specific rules are checked when lowering, which can return `ErrBackendLimit`, a `compile.LimitError`, `ErrUnsupported` or `ErrCPUUnsupported`. These rules include:

- an `Effect` that must match the target convention, and `compile.Func.Floats` that must mark 32- or 64-bit parameters and, for a float result, returns of that width;
- FMA availability;
- call and syscall argument limits;
- immediate ranges;
- stack area alignment and address width, and the Go-managed rule on calls (§4.5).

## 10. Backend limits

Lowering reports numeric limits as `*compile.LimitError{Kind, Needed, Limit, Arch, Detail}`, which wraps `ErrBackendLimit`. `Needed` may be a lower bound, and 0 means unknown, for `Limit` as well.

| Kind | What it counts | What a front end can do |
|---|---|---|
| `LimitSpillWords` | Spill storage in words, including the homes reserved for Vars and parameters and the stack areas. The limit is 4,096 words. | Split the function. Shorten live ranges, for example by ending scopes or recomputing values. Use fewer or smaller Vars. |
| `LimitFrameBytes` | A stack frame's bytes, for example a Go-managed frame above the managed-frame limit (640 bytes, 240 on 386), stack areas included. | Reduce spills, outgoing call space and stack areas, or use another context. |
| `LimitPredicateNodes` | A branch predicate's expanded nodes, and a switch's cases (`MaxPredicateNodes`, 16,384 per predicate or switch and per program). | Split the condition across several branches, or compute part of it as a value. Split a large switch into a switch on the high part of the selector and switches on the low part. |
| `LimitBranchRange` | A branch or literal-pool displacement beyond the instruction's reach (arm64 literals; control branches on some targets). | Split the function into smaller ones. |
| `LimitArguments` | Entry parameters, raw-IR label parameters, or an outbound call's arguments. | Pass extra values through memory, or split the call. |

The argument counts are:

- **Entry parameters:** at most `abi.MaxArgumentsFor(arch)`, which is 6 on amd64 and 8 elsewhere.
- **C calls:** the same limit as entry parameters, one fewer for a Go-managed C call with float passing. A tail call has the same limit and, on 386, at most as many argument words as the entry's parameters.
- **Float arguments:** at most 8 marked arguments per call or entry, within the counts above.
- **Go calls:** `abi.MaxGoCallArgumentsFor(arch)`, which is 7 on amd64, 9 on 386, arm64 and riscv64, 12 on ppc64le, and 16 on loong64.
- **System calls:** 6 arguments.
- **Label parameters:** up to 4,096 words.
- **Cross-block values:** the label parameters and edge arguments that lowering adds to carry them (§6) may total at most 8 × the instruction count + 65,536. A value live through many blocks costs one parameter per block and one argument per edge into it. Past the budget, keep long-lived state in a Var, or pass fewer values through long chains of blocks.

Limits without a number return `ErrBackendLimit` itself, for example an operation a target can't lower.
