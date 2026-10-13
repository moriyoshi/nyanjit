# Portable SIMD

The implemented API covers integer vectors, comparisons, `MaskBits`, lane
access, memory operations and carryless multiplication. Native 128-bit lowering
uses SSE2 on amd64/386, NEON on arm64 and optional LSX on loong64; other targets
use scalar lanes. Programs with control flow or Vars also use scalar lanes.
Scalar floating-point operations are separate from the integer vector API.

The [IR specification](ir.md#47-vectors-and-masks) lists the implemented vector opcodes. Floating vector lanes, general mask operations, reductions, scalable vectors and a native vector call ABI are not currently supported.

## 1. Purpose and scope

A program chooses a fixed vector shape. Compilation legalizes that shape for
its selected target and CPU features, without exposing register widths or
addressing modes in the Builder. Native execution specializes for the executing
machine; text emission can select a different architecture and explicit features.

Vectors contain untyped lane bit patterns. `Add` interprets each lane as a
wrapping integer. Existing scalar operations and scalar 128-bit integers retain
their semantics. A scalar 128-bit integer is
not a vector of two 64-bit lanes.

Every implemented integer vector operation has a scalar lowering on every
existing nyanjit backend. This does not create executable support for an
OS/architecture whose execution package is unsupported.

## 2. Types and values

```go
type Kind uint8 // InvalidKind, ScalarKind, VectorKind, MaskKind

type Type struct {
    Kind     Kind
    Lanes    uint16
    LaneBits uint8
}

func Scalar(bits uint8) Type
func Vec(lanes uint16, laneBits uint8) Type
func Mask(lanes uint16) Type
func (t Type) Valid() bool
func (t Type) Bits() uint16
func (v Value) Type() Type
func (v Value) Bits() uint16
```

The shared shape definition lives in `abi.ValueType` to avoid an IR/ABI import cycle; `ir.Type` aliases it. Its constructors (`ir.Scalar`, `ir.Vec`, `ir.Mask`) live in `ir`.
Scalars admit 8, 16, 32, 64 and 128 bits. Vectors have at least two lanes,
8/16/32/64 bits per lane, and at most 512 total bits. Masks have 2–64 logical
boolean lanes; `LaneBits` is 1 and their logical bit width is the lane count.
Constructors return an invalid Type for unsupported shapes. The zero Type and
zero Value are invalid. Validity tests use `Type().Valid()` and Builder scope
checks, never `Bits() == 0`.

`Bits()` always means total logical width, including 512-bit vectors. Existing
scalar width arguments such as `ParamBits` remain `uint8`; use `Type().LaneBits`
when an API specifically needs a scalar/lane width. `Type`, rather than total
width alone, determines whether operands match.

`VMask` wraps a mask-typed Value without promising an all-ones physical lane
representation. `mask.Value()` exposes its handle for Vars;
`value.Mask()` reconstructs a mask from a Var snapshot. The latter
returns an invalid mask for a non-mask Value. Operations validate ownership,
shape and scope when materializing the mask.

Vectors initially enter and leave functions through memory. `Param`, native
calls and `Return` retain their scalar ABI; a vector is not accepted as a scalar
argument just because its total width equals a scalar width.

## 3. Builder operations and control

Lane-wise operations use the scalar names:

| Group | Implemented operations |
|-------|------------------------|
| Integer | `Add`, `Sub`, `Mul`, `And`, `Or`, `Xor`, immediate/value-count shifts and rotations |
| Polynomial | `CarrylessMul(x, y, xLane, yLane)` on even-count 64-bit lane vectors |
| Creation and lanes | `Splat(t, scalar)`, `ConstVector(t, bytes)`, `ExtractLane(v, i)`, `InsertLane(v, i, scalar)` |
| Rearrangement | `ShuffleLanes(x, y, lanes)`, `BroadcastLane(v, i)` |
| Comparisons | `VCompare(x, condition, y)` |
| Masks | `MaskBits` |
| Widening | `WidenLanes`, `WidenLanesSigned` |
| Memory | `VLoad(t, address)`, `VStore(address, v)` |

Binary vector operands have identical Type. Value-count shifts accept a scalar
unsigned count applied to every lane, or a matching vector of per-lane counts.
Scalar shift-count behavior is unchanged. Scalar predicates (`Compare`,
`CompareImmediate`, `IsZero`, `IsNegative`) reject vector and mask operands;
use `VCompare` followed by `MaskBits` and a scalar predicate instead. Scalar
casts, bit extraction, concatenation, selects and memory-address operations
also reject vector/mask handles immediately. Public IR validation checks
operand kinds in predicate trees and instruction lane widths against Types. Constant shuffle indices select from
`a` followed by `b`. Integer sums wrap at the lane width.

There is one widening convention: `WidenLanes(v)` and `WidenLanesSigned(v)`
widen the whole vector, keeping the lane count and doubling the lane width. The
total width therefore doubles. Shapes exceeding 512 bits are rejected. The
Builder has no half selector; in the IR, `OpVExtend*` also accepts an input of
twice the lanes with `Imm` selecting its half, which native lowering uses for
each 128-bit chunk.

`ConstVector` takes exactly `Type.Bits()/8` bytes in canonical lane order:
lane zero first, each lane encoded little endian. This is an IR/serialization
format; native memory operations use the requested memory byte order within each
lane. Lane zero is always at the lowest memory address. `VLoad`/`VStore` are unaligned accesses and touch exactly the vector's bytes.

### Labels and scopes

Builder labels take no parameters. Vectors and masks cross blocks by dominance
or through Vars, like scalars:

```go
vv, mv := b.Var(v), b.Var(m.Value())
b.Bind(loop)
v, m = b.Get(vv), b.Get(mv).Mask()
// ...
b.Set(vv, next)
b.Jump(loop)
```

A Var has its value's complete Type, and `Set` requires the same Type. Raw IR
block parameters (`ir.ProgramBuilder.NewBlock`) carry complete Types too:
incoming edges must pass the same number of arguments with the same Types.
Scalar 128-bit values still have two logical uint64 components internally.
IR records logical label types, including the label parameters lowering adds
for cross-block values; legalization may expand vector/mask homes into scalar
lanes. It must rewrite every incoming edge and parameter consistently. An equal
byte count is insufficient for label matching.

Vector and mask values follow the same dominance rule as scalars (see docs/ir.md) and `Enter`/`Leave`. Vector and mask exports
follow the same ownership rules as scalars. Programs containing labels or Vars
use scalar legalization, including operations outside the loop or branch.

No separate flow builder or mandatory loop abstraction is introduced. Pure
literal expression trees, including splats of deferred literals, can be
rematerialized in each block. Expressions capturing a block-local load or
parameter cannot cross a label. The materialization cache must check current
scope/block validity rather than reusing an expired definition.

## 4. Portable semantics

| Operation | Rule |
| --- | --- |
| Integer arithmetic | Wrap independently modulo `2^LaneBits`; no cross-lane carry. |
| Oversized shift | Zero for logical shifts, sign fill for arithmetic right shift. Count is unsigned and its full width is examined. |
| Rotation | Count modulo the lane width. |
| Signed integer comparison | Two's-complement interpretation at lane width. |
| MaskBits | Bit `i` is lane `i`; scalar result has sufficient width, up to 64 bits. |

## 5. Features and reports

```go
type VectorInfo struct {
    PreferredBits int // 128 when SIMD is enabled, otherwise 0
    Features      []string
    Lowering      func(ir.Opcode, ir.Type, ...ir.BranchCondition) abi.VectorLowering
}
```

`compile.Target` exposes this through its `Vectors() compile.VectorInfo` method.
Lowering classes are `abi.Native`, `abi.Expanded`, `abi.Split` and `abi.Scalarized`.

`TargetSpec.Vectors` selects the policy. `compile.VectorsAuto()` (the zero value)
selects detected native features, or the explicit cross-target baseline.
`compile.VectorsOff()` requests no SIMD and forces scalar lowering.
`compile.VectorsRequire(names...)` selects a declared target feature set.
`compile.NewTarget` rejects an invalid request, so a Target's `VectorInfo` always
describes a usable policy. Native
emission rejects instructions unsupported by the executing CPU; text emission
can describe a different machine. `VectorsRequire` accepts `sse2` and
`pclmulqdq` on amd64/386, `neon` and `pmull` on arm64, and `lsx`/`lasx` on loong64.
LASX requests enable 128-bit LSX lowering. Wider AVX/LASX, SVE/SVE2, RVV, VSX
and MSA lowering remain planned.
`NewTarget` returns errors for unknown or incompatible requested features.
`PreferredBits` is zero for disabled SIMD and targets with scalar-only lowering.

For `OpVCompare`, supply the input vector Type and comparison condition:
`info.Lowering(ir.OpVCompare, Vec(16, 8), ir.BranchEqual)`. Native lowering
compares byte equality in one 128-bit vector. Other conditions scalarize. A query
without the condition, or with only a logical mask Type, conservatively reports
Scalarized because it cannot establish the input lane width. The final comparison
report keeps the logical result mask Type and records the actual lowering.

Each semantic vector operation has a report entry with original IR position,
operation, complete Type and lowering class. Class definitions:

- **Native:** a direct target vector operation, including required copies or
  load/store encoding details.
- **Expanded:** several vector/bit-manipulation instructions implement a semantic
  operation, a fix-up, or an emulation pattern. NEON byte `MaskBits` is Expanded.
- **Split:** the semantic vector is divided into smaller native vector chunks.
  Per-chunk expansion can be reported as detail without losing the Split class.
- **Scalarized:** lane arithmetic or control is performed by scalar operations.

The report also names actual register clobbers, spill storage and required
alignment. A feature query is an estimate; the final report reflects
legalization, allocation and emitted instructions.

## 6. Calls, memory and constants

Native vector values are preserved across C/Go calls and syscalls by spilling when needed. Preservation contributes to reported frame/workspace use. A larger Stack.BytesAvailable does not raise the managed-frame limit; vector-heavy functions may return ErrBackendLimit.

Loads/stores support unaligned user addresses. Constant pools honor the alignment required by their instructions; aligned constant operands impose no alignment on user buffers. Vector values have no native vector call ABI: use scalar entry/call contracts and memory for vector inputs/outputs.

## 7. Target support and examples

| Target | Current native integer SIMD |
|--------|-----------------------------|
| amd64 | SSE2, optional PCLMULQDQ |
| arm64 | NEON, optional PMULL |
| 386 | Required SSE2, optional PCLMULQDQ; wider shapes split |
| riscv64 | Scalar lanes |
| ppc64le | Scalar lanes |
| loong64 | Optional 128-bit LSX; wider shapes split; baseline profile uses scalar lanes |

On 386, scalar FP and SIMD compete for the same registers and spill under
pressure. All XMM registers are caller-saved for C and Go ABI0; live vectors
are preserved through memory. Literal references are rebased before each
executable mapping is sealed. `Template.Bytes` returns ErrCodeBaseRequired
when it needs a final base; use Template.Bind or Template.LinkAt. FMA and
VEX/AVX are unsupported on 386.

Examples using implemented operations:

1. **int32x4 add:** load two `Vec(4,32)` values, Add, then VStore. SSE2 uses
   unaligned loads and PADDD; NEON uses LDR Q, vector ADD and STR Q. Wider
   shapes can split into 128-bit chunks.
2. **byte equality mask:** use `MaskBits(VCompare(VLoad(Vec(16,8), p),
   BranchEqual, Splat(t, needle)))`. SSE2 uses PCMPEQB/PMOVMSKB; NEON uses
   CMEQ and expanded bit-weight extraction. Bit i always describes lane i.
3. **byte rearrangement:** constant ShuffleLanes/BroadcastLane operations
   choose supported native patterns or scalarize. Consult the final feature
   report for the selected shape and profile.
4. **vector live across a call:** a ContextGoManaged C entry reached from Go
   calls C through the runtime gateway, with live vector state preserved in
   memory. Use ContextForeign only when the entry runs in a foreign context;
   a raw C call on a goroutine stack is not an equivalent execution path.

## 8. SHA-256 application and packed shifts

The [SHA-256 example](../examples/sha256/README.md) uses `Vec(4,32)` to run four
independent compression streams. Builder construction unrolls the 64 rounds;
message iteration and transposition remain in Go. The generated function uses
native vector add/bitwise operations, immediate logical shifts and rotations
on SSE2/NEON, and scalarizes on other backends or with disabled SIMD features.

Immediate `ShiftLeft` and `ShiftRight` now have native 32-bit lane forms.
Counts at least 32 produce zero. `RotateLeft` and `RotateRight` use modulo-32
counts and an `Expanded` sequence. On amd64 this uses opposite shifts and OR,
with XMM0 as a temporary. On arm64 distinct source and destination registers use
`USHR` + `SLI` for right rotation, or `SHL` + `SRI` for left rotation: two
instructions with no temporary. An aliased arm64 destination uses opposite
shifts and OR through V30 to preserve the original input. Clobber reports follow
the emitted form; rotations by a multiple of 32 require no temporary. Wider
shapes split into 128-bit chunks; other lane widths and value-count operations
retain scalar lowering.
No new public operation or CPU feature spelling is required. The Go XMM15
zero-register reservation is unchanged.

## 9. Carryless multiplication

```go
shape := ir.Vec(2, 64)
x := b.VLoad(shape, xAddress)
y := b.VLoad(shape, yAddress)
product := b.CarrylessMul(x, y, 0, 1) // low lane of x times high lane of y
b.VStore(resultAddress, product)
```

`Value.CarrylessMul(other, xLane, yLane)` also composes a deferred expression.
The operands have identical vector Types with an even number of 64-bit lanes.
Each pair of lanes is one 128-bit chunk. Each selector is 0 or 1 within every
chunk, independently for each operand. The result has the same Type, with the
low product word followed by the high product word in each chunk. Bits represent
polynomial coefficients: addition is XOR, and multiplication has no carries.
There is no polynomial reduction and no ordinary integer multiplication.

| x selector | y selector | amd64 immediate | arm64 |
| --- | --- | --- | --- |
| 0 | 0 | `0x00` | `PMULL` |
| 1 | 0 | `0x01` | `EXT` of x into V31, then `PMULL` |
| 0 | 1 | `0x10` | `EXT` of y into V31, then `PMULL` |
| 1 | 1 | `0x11` | `PMULL2` |

The amd64 backend emits legacy SSE-encoded `PCLMULQDQ` with allocated XMM
registers, including high registers and last-use reuse of the first input.
Byte and text assemblers share the expansion and PC accounting. The operation
does not modify integer flags or add expansion-register clobbers. It does not
write XMM15. A 128-bit operation is `Native`; wider supported shapes split into
128-bit chunks and report `Split`. This does not multiply complete 256/512-bit
polynomials: each chunk produces its own independent 128-bit product.

On arm64 the `pmull` extension (FEAT_PMULL, detected from the process HWCAPs)
selects `PMULL`/`PMULL2` the same way; a mixed selector swaps the halves of one
input into the reserved V31 first. `VectorsRequire("pmull")` implies `neon` and
adds `pmull` to the required CPU profile, as `pclmulqdq` does on x86.

Native CPU discovery includes `pclmulqdq` independently of x86-64-v1/v2/v3/v4.
A baseline profile therefore does not imply the extension. With `VectorsAuto()`,
SIMD selection follows the resolved CPU profile. To request it explicitly, use
`CPU: abi.CPUProfile{Extensions: []string{"pclmulqdq"}}` or
`Vectors: compile.VectorsRequire("pclmulqdq")`; the latter also enables the SSE2 register file
and adds the extension to the required CPU profile. Native compilation rejects
an explicit request exceeding the executing CPU with `compile.ErrCPUUnsupported`.
For compatibility, `errors.Is(err, compile.ErrBackendLimit)` still matches CPU
mismatches; their diagnostic no longer says the function exceeds backend limits.
A cross-target listing
can request it independently of the host, for example:

```go
target, err := compile.NewTarget(compile.TargetSpec{
    Architecture: "amd64",
    CPU: abi.CPUProfile{Extensions: []string{"pclmulqdq"}},
})
unit, err := compile.Lower(target, program, compile.Func{ABI: abi.C})
text, err := unit.Listing()
```

Without this feature, with disabled SIMD, or in a labeled/Var program, legalization
expands each selected product to scalar shifts, ANDs and XORs. This fallback also
supports the other existing architectures and preserves the per-chunk low/high
lane layout. Operand ownership, vector Types,
selectors and raw IR operand widths are validated. ARM PMULL/PMULL2 are supported
with pmull; byte-polynomial multiplication and VEX/EVEX VPCLMULQDQ emission
remain separate native-lowering work.

## LoongArch LSX lowering

With VectorsAuto, a profile containing LSX enables native 128-bit integer forms; wider shapes split into 128-bit chunks. LASX implies LSX but no LASX instructions are emitted. Scalar FP and SIMD share registers, and live vectors spill in full across calls. Control-flow programs and mutable vector homes use scalar legalization.

Native forms include unaligned memory, constants, lane transfers, add/subtract, bitwise operations and integer multiplication at all four lane widths. Immediate shifts/rotations and variable scalar or per-lane counts preserve full-count saturation rules. Byte equality/mask extraction have native expansions; other comparisons and carryless products use scalar lanes.

Reports distinguish Native immediate operations, Expanded variable-count operations/byte mask extraction, Split wider shapes, and Scalarized unsupported/disabled operations. See [CPU profiles](cpu-profiles.md#loongarch-lsx).
