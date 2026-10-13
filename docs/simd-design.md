# Portable SIMD design

## 1. Purpose and scope

A program chooses a fixed vector shape. Compilation legalizes that shape for
its selected target and CPU features, without exposing register widths or
addressing modes in the Builder. Native execution specializes for the executing
machine; text emission can select a different architecture and explicit features.

Vectors contain untyped lane bit patterns. `Add` interprets each lane as a
wrapping integer; `FAdd` interprets it as IEEE 754. Existing scalar operations
and scalar 128-bit integers retain their semantics. A scalar 128-bit integer is
not a vector of two 64-bit lanes.

Every implemented portable operation has a scalar lowering on every existing
nyanjit backend. This does not create executable support for an OS/architecture
whose execution package is currently unsupported. The explicit exception is
hardware-fused `FMA`: until an exact software implementation exists, it returns
`ErrBackendLimit` on a target without suitable hardware fusion. `FMulAdd` has
no such requirement and may use separate multiplication and addition.

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

The shared shape definition lives in `abi.ValueType` to avoid an IR/ABI import cycle; `ir.Type`, `builder.Type` and `nyanjit.Type` alias it. Semantic constructors live in `ir` and the Builder facades.
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
representation. `mask.Value()` exposes its handle for label arguments;
`value.Mask()` reconstructs a mask after a typed label binding. The latter
returns an invalid mask for a non-mask Value. Operations validate ownership,
shape and scope when materializing the mask.

Vectors initially enter and leave functions through memory. `Param`, native
calls and `Return` retain their scalar ABI; a vector is not accepted as a scalar
argument just because its total width equals a scalar width.

## 3. Builder operations and control

Lane-wise operations use the scalar names:

| Group | Operations |
| --- | --- |
| Integer | `Add`, `Sub`, `Mul`, `And`, `Or`, `Xor`, immediate/value-count shifts and rotations |
| Additional integer | `AndNot`, `Neg`, `Abs`, `MinS/MinU`, `MaxS/MaxU`, `AddSatS/AddSatU`, `SubSatS/SubSatU`, `MulHighS/MulHighU` |
| Floating point | `FAdd`, `FSub`, `FMul`, `FDiv`, `FSqrt`, `FMA`, `FMulAdd`, `FMin/FMax`, relaxed minimum/maximum, `FAbs/FNeg` |
| Creation and lanes | `Splat(t, scalar)`, `ConstVector(t, bytes)`, `ExtractLane(v, i)`, `InsertLane(v, i, scalar)` |
| Rearrangement | `Shuffle(a, b, indices)`, `Swizzle(table, indices)` |
| Comparisons | `VCompare(x, condition, y)`, `FCompare`, `Blend(mask, x, y)` |
| Masks | `And`, `Or`, `Not`, `AnyTrue`, `AllTrue`, `MaskBits` |
| Conversions | `WidenS/WidenU`, `Narrow`, saturating narrow, integer/float conversions, `Bitcast` |
| Reductions | integer sum/min/max and `FReduceAdd` |
| Memory | `VLoad(t, address)`, `VStore(address, v)`, partial loads/stores; gather/scatter later |

Binary vector operands have identical Type. Value-count shifts accept a scalar
unsigned count applied to every lane, or a matching vector of per-lane counts.
Scalar shift-count behavior is unchanged. Scalar predicates (`Compare`,
`CompareImmediate`, `IsZero`, `IsNegative`) reject vector and mask operands;
use `VCompare` followed by `MaskBits` and a scalar predicate instead. Scalar
casts, bit extraction, concatenation, selects and memory-address operations
also reject vector/mask handles immediately. Public IR validation checks
operand kinds in predicate trees and instruction lane widths against Types. Constant shuffle indices select from
`a` followed by `b`; dynamic lookup uses `Swizzle`, whose out-of-range lanes are
zero. Integer sums wrap at the lane width.

There is one widening convention: `WidenU(v, newLaneBits)` and
`WidenS(v, newLaneBits)` widen the whole vector, keeping the lane count and
requiring `newLaneBits == 2*v.Type().LaneBits`. The total width therefore doubles.
Shapes exceeding 512 bits are rejected. There is no half selector. Narrowing
similarly keeps the lane count and halves the lane width.

`ConstVector` takes exactly `Type.Bits()/8` bytes in canonical lane order:
lane zero first, each lane encoded little endian. This is an IR/serialization
format; native memory operations use the requested memory byte order within each
lane. Lane zero is always at the lowest memory address, including big-endian
PPC64. `VLoad`/`VStore` are unaligned accesses and touch exactly the vector's bytes.
Partial loads zero inactive lanes and never access their addresses; partial
stores write only active lanes. A runtime count is clamped to the lane count.

### Labels and scopes

```go
p := b.BindParamsTypes(label, Scalar(64), Vec(4, 32), Mask(4))
v := p[1]
m := p[2].Mask()
b.JumpArgs(next, p[0], v, m.Value())
```

`BindParamsTypes(label, types ...Type)` returns one Value per logical parameter.
Incoming edges must have the same number of parameters and the same complete
Types. Scalar 128-bit values still have two logical uint64 components internally.
IR records logical label types; legalization may expand vector/mask homes into
scalar lanes. It must rewrite every incoming edge and parameter consistently.
An equal byte count is insufficient for label matching.

Values remain block-local and follow `Enter`/`Leave`. Vector and mask exports
follow the same ownership rules as scalars. Phase 1 fully scalarizes every vector operation in a program that contains
labels, even if an operation lies outside the loop or branch. Native SIMD loops
and vector edge homes remain a subsequent allocation optimization.

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
| Strict floating minimum/maximum | Propagate NaN; minimum prefers −0 and maximum prefers +0. |
| NaN output | Any quiet NaN. Payload/sign bits need not match between targets. |
| Float-to-int | Round toward zero; saturate overflow; NaN becomes zero. |
| FMA | One rounding step; unavailable hardware fusion is an explicit backend limitation until exact software fusion is implemented. |
| FMulAdd | Either the fused or separately rounded result is permitted. |
| Floating arithmetic | Round to nearest, ties to even; retain subnormals. |
| Relaxed minimum/maximum | Permit either operand on NaN or mixed-sign zero cases; ordinary finite cases keep min/max semantics. |
| Relaxed conversion | Inputs outside the finite representable range have unspecified results. |

Strict vector floating lowering must establish the required floating environment
and restore the caller's environment if it changes it. Call effects invalidate
assumptions about that environment. Armv7 NEON float flushing does not meet the
strict contract: use a suitable scalar VFP/software path and report scalarization.
Phase 1 contains no floating vector execution; existing scalar FP behavior is
unchanged.

`FReduceAdd` has a fixed pairwise tree: adjacent lanes are added, then adjacent
partial sums, until one value remains. An unmatched final lane carries unchanged
to the next level. There is no artificial +0 leaf, which could change signed zero.
This order is deterministic and admits `faddp`/`haddps` patterns when their order
matches. It is not a sequential left fold. Integer reductions wrap at lane width.

## 5. Features and reports

```go
type Lowering uint8 // Native, Expanded, Split, Scalarized

type VectorInfo struct {
    PreferredBits int // 128 when SIMD is enabled, otherwise 0
    Features      []string
    Err           error // invalid explicit feature request
    Lowering      func(ir.Opcode, Type, ...ir.BranchCondition) Lowering
}
func compile.Vectors(options abi.CompileOptions) VectorInfo
```

`CompileOptions.Features == nil` selects detected native features, or the explicit
cross-target baseline. A non-nil empty slice requests no SIMD and forces scalar
lowering. Explicit feature names select a declared target feature set. Native
emission rejects instructions unsupported by the executing CPU; text emission
can describe a different machine. Phase 1 recognizes `sse2` and `neon`; later
feature sets include AVX/AVX2/AVX-512, SVE/SVE2, RVV, VSX, LSX/LASX and MSA.
Unknown or incompatible requested features are errors, not silently ignored.
`Vectors` exposes them in `VectorInfo.Err`; compilation returns the same error.
`PreferredBits` is zero for disabled SIMD and targets with scalar-only lowering.

For `OpVCompare`, supply the input vector Type and comparison condition:
`info.Lowering(ir.OpVCompare, Vec(16, 8), ir.BranchEqual)`. Phase 1 natively
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

The report also names actual vector/predicate register clobbers, spill storage
and required alignment. A feature query is an estimate; the final report reflects
legalization, allocation and emitted instructions. Strict operations rejected for
lack of fusion return an error rather than a successful lowering entry.

SVE compilation records assumed vector length in bytes. A configurable entry
check uses `rdvl` and traps if the current thread's length differs. Without the
check, the caller must ensure the assumed length on every invocation. NEON/SVE
register interchange is valid only when the assumed shape and vector length
permit it; it is never inferred merely from host CPU feature flags.

## 6. Allocation, calls, constants, and assembly

The pipeline remains Builder → typed IR → legalization/selection → register
allocator and spill scheduler → physical emitter owning PC → byte/text assembler.
Assemblers visit allocated instructions and choose neither virtual homes nor
control flow.

Introduce independent general-purpose, vector/FP and predicate register classes.
A vector virtual value carries class, size and alignment. Scalarized lanes use
the existing general-purpose allocator; native chunks use vector residency.
Allocation supports last-use reuse, complete-value eviction and reload, aligned
vector spill homes, and live values crossing calls. Splits are logical pieces of
one vector, not assumptions about consecutive physical register numbers.

Reserve scalar FP expansion temporaries until phase 2 moves scalar float
intermediates into the FP/vector class. Entry and exit ABI argument placement
remain separate from internal register class. Phase 1 does not change the scalar
float ABI or silently pass vectors through `Code.Call`. On amd64, allocation
uses XMM3–14. XMM0–2 are scalar FP/expansion temporaries, and XMM15 is reserved for Go
ABIInternal's zero-register contract. Generated vector and scalar FP expansions
do not write it, including before managed C bridges that enter Go. On arm64, allocation uses V0–7 and V19–29; V16–18 and
V30–31 remain expansion temporaries, and V8–15 are excluded because AAPCS64
preserves only their low halves.

CallEffect must describe preserved bit ranges rather than a single preserved
register boolean. AAPCS64 preserves only the low 64 bits of V8–V15; a 128-bit value
in those registers needs preservation. System V amd64 has no preserved XMM
registers; Windows x64 preserves the low 128 bits of XMM6–15 and needs matching
unwind records when the generated entry saves them. Wider AVX state must be spilled
before destructive calls; emit `vzeroupper` where required after using upper lanes.
Go calls and syscalls clobber all SVE predicate registers and Z bits above 128;
Go calls also obey the Go ABI's ordinary vector clobbers. RVV configuration and
predicate ownership are tracked resources.

Register preservation belongs to the spill scheduler. An emitter does not
blanket-save clobbered registers. Phase 1 can conservatively spill native vector
values at all call/syscall boundaries, retaining correct behavior while more
precise preserved ranges are introduced.

Managed amd64/arm64 frames are bounded at 640 bytes, including Go argument
homes, entry ABI preservation and aligned vector spill homes. This leaves room
for gateway/call linkage within Go's 800-byte nosplit reserve; metadata
registration applies the same bound. `StackBytesAvailable` must cover the
reported frame and cannot raise this managed limit. Call tests cover 1, 2, 8, 13
and 19 simultaneously live 128-bit vectors, both C/Go managed entry ABIs, foreign
C calls, stack growth and GC. The capacity is determined by the complete frame,
rather than a fixed number of vectors; larger programs may return
`ErrBackendLimit`. The arm64 large-frame prologue/epilogue and PCSP metadata
handle frames beyond the 240-byte short encoding.

Constant pools are at least 16-byte aligned, and 32-byte aligned for AVX constants
(64 where a later instruction requires it). Legacy SSE aligned memory operands
must not read arbitrary user memory. Unaligned user loads/stores use `movdqu` or
other unaligned instructions. Constant offsets, padding and branch displacement
accounting belong to the emitter; literal bytes must not be entered as code.
Scalar FP and native SIMD constants now share a read-only per-object literal
pool, deduplicated and aligned to 16 bytes after the final return. See
[FP performance](fp-performance.md) for placement and architecture load forms.

## 7. Target baseline and corrected examples

| Target | Planned native support | Phase 1 |
| --- | --- | --- |
| amd64 | SSE2 baseline, optional AVX/AVX2/AVX-512 | SSE2 integer operations and expanded byte masks |
| arm64 | NEON baseline, optional SVE/SVE2 | NEON integer operations and expanded byte masks |
| 386 | Optional SSE2 (usual Go baseline) | Scalar lanes, including paired 64-bit lanes |
| riscv64 | Optional RVV 1.0 | Scalar lanes |
| ppc64/ppc64le | POWER8 VSX; translate lane numbering | Scalar lanes |
| arm | Optional integer NEON; strict float scalar path | Scalar lanes when this backend exists |
| loong64 | Optional LSX/LASX | Scalar lanes when this backend exists |
| mips64 | Optional MSA | Scalar lanes when this backend exists |

The source examples are illustrative. Tests will establish actual PCs, registers
and instruction counts; no performance ratio or external implementation
attribution is claimed.

1. **int32x4 add:** `VStore(dst, Add(VLoad(Vec(4,32), a), VLoad(t,c)))`.
   SSE2 uses two unaligned loads and register `paddd`; NEON uses `ldr Q`, vector
   `add`, `str Q`. A 128-bit `vpaddd` form is AVX, not specifically AVX2.
   A wider shape can split into 128-bit chunks.
2. **SAXPY:** typed label arguments carry pointers/counts and the splat, or a
   literal splat is rematerialized. `FMulAdd` permits fusion. SVE tails require
   recorded VL; partial inactive lanes cannot fault. This belongs to later phases.
3. **byte equality mask:** `MaskBits(VCompare(VLoad(Vec(16,8),p), BranchEqual,
   Splat(t, needle)))`. SSE2 uses byte splat, `pcmpeqb`, `pmovmskb`; NEON uses
   `dup`, `cmeq` and an Expanded bit-weight extraction. The verified NEON reference
   vector yields 9249 and is retained as a test vector.
4. **sum of bytes:** `ReduceAdd(WidenU(v,16))` widens all 16 lanes. A pattern can
   use `uaddlv` or `psadbw`; the unfused path remains correct. No unmeasured length
   ratio is asserted.
5. **byte swapping:** constant shuffle recognition can choose `rev32`, an aligned
   constant-pool `pshufb`, or an SSE2 shift/word-shuffle expansion. Arbitrary
   unsupported shuffles scalarize.
6. **saturating conversion:** NEON `fcvtzs` maps NaN, 3e9, −3e9 and 1.9 to 0,
   INT32_MAX, INT32_MIN and 1. SSE2 conversion needs an overflow/NaN fix-up with
   aligned constants; RVV needs a NaN fix-up.
7. **strict minimum:** compare NaNs by class and distinguish signed zeros.
   NEON `fmin` and SSE2's expanded minimum may produce different quiet NaN payloads.
   Relaxed minimum admits the ISA's cheaper result in the documented exceptional cases.
8. **vector live across a call:** this example uses `CompileGo` with a C entry
   reached from Go, and `CallC` lowered through the runtime C gateway. Schematically:
   `load/add → scheduler vector spill → move target/args into gateway's Go ABI homes
   → call runtime C gateway (switch to system stack, perform C call, restore Go
   context) → scheduler reload → vector store → scalar return`. It is not a bare
   `call RAX`/`blr X16` on a goroutine stack. `CompileForeign` is the alternative
   only when the entry actually runs in a foreign context. Windows unwind/shadow
   space details belong to a future Windows execution port.

## 8. Validation

A typed scalar reference interpreter defines integer lane arithmetic, memory
ordering, masks and the shift/count rules. Differential tests compile identical
programs with native features and `Features: []string{}` and compare results
against that independent interpreter. Test unaligned addresses, overlapping
buffers, big-endian lanes, high-bit counts, wrapping arithmetic, live values,
register pressure, calls, scope exports and typed label transfers.

Floating tests compare any quiet NaN by class rather than payload bits; signed
zeros remain distinct. `FMulAdd` accepts either the exact fused result or the
separately rounded result. `FMA` accepts only fused rounding. Strict reductions
use the fixed tree as their oracle. Conversion reference vectors include the
verified arm64 cases above.

Text goldens use the same physical stream as byte emission. Independent
assembler vectors check SSE2/NEON encodings and PC sizes. Examples 1 and 3 become
phase 1 goldens on amd64 and arm64. The existing privileged E2E container runs
amd64, arm64, 386 and riscv64 at minimum, without changes to host binfmt state.
Later CPU matrices add SSE2-only/AVX2/AVX-512, different SVE lengths, RVV vlen
128/256/512, and LSX/LASX baselines. CPU model declarations and assumed VL are
recorded with results.

## 9. Implementation phases and acceptance

1. **Groundwork:** public Type and `Bits() uint16`, full-type validation, typed
   IR and label parameters, vector register class/residency/spills, `VLoad`,
   `VStore`, `Splat`, constants and lane extraction/insertion; existing integer
   arithmetic/bitwise/shift/rotate names accept matching vectors. Scalar lowering
   is available on every existing backend. SSE2/NEON implement supported 128-bit
   operations; other operations scalarize, and wider shapes can split. Include
   equality masks and `MaskBits` needed for example 3. Report each original op's
   class and actual vector clobbers. Preserve scalar API semantics and all tests.
2. **Core:** general masks/blends, floating lanes and FP residency, additional
   integer operations, reductions, shuffles, conversions and strict/relaxed
   floating rules. General native vector label homes and preserved-bit call data.
3. **Widths and targets:** AVX2/LASX widths, PPC64 VSX with endian-aware lane
   numbering, RVV/LSX and armv7 integer NEON. Record scalable vector assumptions.
4. **Advanced:** AVX-512/SVE predicate allocation, checked scalable VL, partial
   and masked memory, gather/scatter and vector ABI entry/exit where supported.

Phase 1 is complete only when native and forced-scalar differential tests and
example goldens pass, scalar regressions pass, and the four required container
architectures execute the tests. No commit is part of this task.

### Phase 1 acceptance record (2026-10-14)

The implementation covers logical types and uint16 total widths, scalar/vector
register classes, aligned full-vector spill homes, 128-bit SSE2/NEON integer
operations, splitting and per-operation scalar fallback, equality/comparison
masks, lane access, immutable constants, typed label legalization and pure literal
splat rematerialization. Scalar FP now has independent residency in the shared FP/vector register file;
its expansion temporaries remain reserved for transfers and fixes.
Native vector values are scheduled to memory across C/Go calls and syscalls.

`go test ./...`, `go vet ./...` and the harness unit tests pass. The existing
privileged E2E image executed every package and smoke checks on both Go 1.26.9
and 1.27.2, using private container binfmt for foreign Linux architectures:

| Target | Pass | Skip | Fail |
| --- | ---: | ---: | ---: |
| linux/amd64 | 85 | 0 | 0 |
| linux/arm64 | 85 | 0 | 0 |
| linux/386 | 77 | 0 | 0 |
| linux/riscv64 | 83 | 0 | 0 |

The 330 matrix checks are package/build/smoke gates, not individual unit-test
counts. JUnit reports are cached as `simd-phase1-<arch>.xml`. Every required SIMD
oracle/golden/control/pressure test ran in both versions, and amd64/arm64 ran
native C/Go call-boundary tests. The 386 labeled-entry tests cover both C and Go
argument/result layouts. No host binfmt or packages were changed.

Phases 2–4 remain future work: floating lanes and reductions/conversions,
additional integer operations, wider native ISAs, native vector label homes,
scalable predicates and partial/gather memory operations.

### Phase 1 review corrections (2026-10-14)

Regression coverage now includes the Go XMM15 zero-register repro with C and Go
entry ABIs and Go/managed-C callees; malformed scalar predicates and vector IR
widths; feature-query/report agreement; and root error operation attribution.
Assembler goldens cover high registers, splat/extract/insert/insert-zero,
noncommutative arithmetic, REX byte aliases, stack/workspace addressing and
NEON MaskBits. Runtime differential tests force zero/all-ones/INT_MIN lanes
(including byte `0x80 * 0x80` overflow), randomized comparison operands, exact
in-place and partially overlapping buffers, and lane access after vector reload
under pressure. The wider pressure case also exposed the three-GPR workspace
limit on 386: paired binary operations now use complete spill homes and a
word-at-a-time schedule, preserving low-word carry across reloads. Inline scalar
math and its instruction budgets remain unchanged.

Review validation: `go test ./...` passes for Linux amd64, arm64, 386 and riscv64
on Go 1.26.9 and 1.27.2. Foreign architectures ran through isolated container
QEMU; arm64 ran natively. `go vet ./...`, E2E harness unit tests and
`git diff --check` also pass. No host binfmt or packages were changed.

Scalar FP residency and shared literal pools are implemented; portable floating
vector operations remain Phase 2 work. See [FP performance](fp-performance.md).
