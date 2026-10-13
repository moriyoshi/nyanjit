# 386 instruction forms

This table is the contract between allocated `machine.Instruction` values and
the 386 byte assembler. Each example is assembled alone, at PC 0, by
`NewByteAssembler(abi.StackPlatform)` through `machine.Visit`. Its size is also
checked against `PhysicalSize(ins, abi.StackPlatform, 0)`. `PhysicalSize` runs
the same encoder, so it is the authority for the size of any form not listed
here. Branch targets in `Imm` are absolute PCs.

The baseline CPU is a P6-class processor with SSE2 (CMOV, MOVD, PSHUFD and
the SSE2 scalar and integer forms). `OpVCarrylessMul` also needs PCLMULQDQ;
the encoder does not check for it. The C ABI is cdecl: arguments are on the
stack and the result is in EAX (EDX:EAX for a 64-bit pair). With
`abi.StackPlatform`, ESP is the frame base.

General registers are numbered as in `Policy.RegisterName`: 0=EAX, 1=ECX,
2=EDX, 3=EBX, 4=ESP, 5=EBP, 6=ESI, 7=EDI. A GP operand must be below 8.
`backend.VectorRegister(n)` names XMMn. Examples write it as `V=A:n`. Only
XMM0-XMM7 exist, and XMM0-XMM2 are expansion temporaries: scalar float and
vector homes use XMM3-XMM7. A 64-bit integer or float value kept in GP
registers is a pair: the low word is in A, B or C and the high word is in
HighA, HighB or HighC. For memory opcodes, `Load64`/`Store64` move a single
32-bit word. All `Imm` and `Disp` values are 32-bit fields.

## Constants, symbols and literals

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpConst` | `A=0 Imm=0x12345678` | 5 | `mov r32, imm32`. Always 5 bytes; there is no short or zeroing form. |
| `OpConst` | `A=7 Imm=0xffffffff` | 5 | Imm must fit in 32 bits. |
| `OpConst` | `A=8 Imm=1` | rejected | A must be a GP register 0-7. |
| `OpConst` | `A=0 Imm=0x100000000` | rejected | Imm is a 32-bit value, unsigned or sign-extended. |
| `OpSymbol` | `A=0 Sym=puts` | 5 | `mov r32, imm32` with a `Reloc386Immediate` relocation. |
| `OpSymbol` | `A=0 HighA=2 Bits=64 Sym=wide` | 10 | Bits=64 loads low32 into A and high32 into HighA (`Reloc386WideImmediate`). |
| `OpSymbol` | `A=0 HighA=8 Bits=64 Sym=wide` | rejected | HighA must be a GP register. |
| `OpLiteralLoad` | `V=A:3 Bits=64 Disp=0x40` | 8 | `movsd xmm, [abs32]`. Disp is the pool PC; a `Reloc386Literal` adds the code base to it. |
| `OpLiteralLoad` | `V=A:3 Bits=32 Disp=0x40` | 8 | `movss`. |
| `OpLiteralLoad` | `V=A:0 Bits=128 Disp=0x40` | 8 | `movdqu`, the full 16 bytes (used for the XMM0 mask of `OpFloatBits`). |
| `OpLiteralLoad` | `V=A:3 Bits=16 Disp=0x40` | rejected | Bits must be 32, 64 or 128. |
| `OpLiteralLoad` | `A=0 Bits=64 Disp=0x40` | rejected | A must be XMM0-XMM7. |
| `OpLiteralData` | `Data=0011223344556677` | 8 | Emits Data verbatim; the size is `len(Data)`. |

## Moves and integer arithmetic

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpMove` | `A=0 B=1` | 2 | `mov A, B`. |
| `OpMove` | `A=1 B=1` | 0 | A self-move emits nothing. |
| `OpMove` | `A=0 B=8` | rejected | A and B must be GP registers. |
| `OpBinary` | `A=0 B=0 C=1 Bits=32 Arith=OpAdd` | 2 | Two-address: `mov A, B` (when A≠B) then `op A, C`. Arith: OpAdd, OpSub, OpAnd, OpOr, OpXor, OpMul. |
| `OpBinary` | `A=0 B=2 C=1 Bits=32 Arith=OpSub` | 4 | A≠B adds a 2-byte move. A must not equal C unless A=B, because the move overwrites C. |
| `OpBinary` | `A=0 B=0 C=1 Bits=32 Arith=OpMul` | 3 | `imul A, C`; the low 32 bits of the product. |
| `OpBinary` | `A=0 B=0 C=1 Bits=8 Arith=OpAnd` | 8 | Bits=8 or 16 adds `and A, mask` (6 bytes). Bits is 0, 8, 16 or 32. |
| `OpBinary` | `A=0 B=0 C=9 Bits=32 Arith=OpAdd` | rejected | All operands must be GP registers. |
| `OpBinary` | `A=0 B=0 C=1 Bits=64 Arith=OpAdd` | rejected | 64-bit values are register pairs. |
| `OpBinary` | `A=0 B=0 C=1 Bits=32 Arith=OpShiftLeft` | rejected | |
| `OpBinaryImmediate` | `A=0 B=0 Bits=32 Imm=0x10 Arith=OpAdd` | 6 | `op A, imm32` (group 0x81). There is no imm8 form. Arith: OpAdd, OpOr, OpAnd, OpSub, OpXor. Bits is 0, 8, 16 or 32 and is not masked; the policy admits only 32. |
| `OpBinaryImmediate` | `A=0 B=1 Bits=32 Imm=0xff Arith=OpAnd` | 8 | A≠B adds a 2-byte move. |
| `OpBinaryCarry` | `A=0 B=0 C=1 Arith=OpAdd` | 2 | Compatibility form of `OpWordCarry` with CarryFlags: `adc A, C` (OpSub gives `sbb`). Consumes CF from the previous instruction. |
| `OpBinaryCarry` | `A=0 B=2 C=1 Arith=OpSub` | 4 | A≠B adds a move; A must not equal C unless A=B. |
| `OpInc` | `A=0 B=0` | 2 | `inc r/m32` (FF /0); there is no one-byte form. |
| `OpInc` | `A=0 B=1` | 4 | A≠B adds a 2-byte move. |
| `OpInc` | `A=0 B=0 PreserveFlags` | rejected | There is no flag-preserving increment. |
| `OpDec` | `A=0 B=0` | 2 | `dec r/m32` (FF /1); writes flags other than CF. |
| `OpDec` | `A=0 B=0 PreserveFlags` | rejected | There is no flag-preserving decrement. |
| `OpNarrow` | `A=0 B=1 Imm=8` | 8 | Imm is the width: 8 or 16 adds `and A, 0xff`/`0xffff` (6 bytes) after the move. |
| `OpNarrow` | `A=0 B=0 Imm=32` | 0 | Other widths emit only the move. |
| `OpNarrow` | `A=0 B=0 Imm=256` | rejected | Imm must be at most 255. |
| `OpSignExtend` | `A=0 B=0 Imm=8` | 6 | Imm 8 or 16: `shl A, 32-Imm; sar A, 32-Imm`. |
| `OpSignExtend` | `A=0 B=1 Imm=16` | 8 | A≠B adds a 2-byte move. |
| `OpSignExtend` | `A=0 B=1 Imm=32` | 2 | Other widths emit only the move. |
| `OpScaled` | `A=0 B=1 C=2 Imm=4` | 3 | `lea A, [B + C*Imm + Disp]`. Imm is the scale: 1, 2, 4 or 8. C must not be ESP. Does not write flags. |
| `OpScaled` | `A=0 B=5 C=2 Imm=8` | 4 | Base EBP, or a Disp that fits in a signed 8-bit field, adds a disp8. |
| `OpScaled` | `A=0 B=1 C=2 Imm=2 Disp=0x1000` | 7 | A larger Disp adds a disp32. `Policy.CanScaled` itself admits only Disp=0. |
| `OpScaled` | `A=0 B=1 C=8 Imm=4` | rejected | All operands must be GP registers. |
| `OpScaled` | `A=0 B=1 C=2 Imm=3` | rejected | |
| `OpScaled` | `A=0 B=1 C=4 Imm=1` | rejected | |
| `OpAndNot` | `A=0 B=1 C=2 Bits=32` | unsupported | |
| `OpCountZeros` | `A=0 B=1 Bits=32` | unsupported | |
| `OpMulAdd` | `A=0 B=1 C=2 D=3` | unsupported | `Policy.CanMulAdd` is false. |

## Shifts

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpShift` | `A=0 B=0 Bits=32 Imm=3 Arith=OpShiftLeft` | 3 | `shl/shr/sar/rol/ror A, imm8`. Imm is the count (at most 255) and is not masked. |
| `OpShift` | `A=0 B=1 Bits=32 Imm=3 Arith=OpShiftRight` | 5 | A≠B adds a 2-byte move. |
| `OpShift` | `A=0 B=0 Bits=8 Imm=3 Arith=OpShiftLeft` | 9 | Bits 8/16: a 6-byte mask follows (except for OpShiftRightArithmetic). |
| `OpShift` | `A=0 B=0 Bits=16 Imm=3 Arith=OpShiftRightArithmetic` | 9 | Bits 8/16 SAR first sign-extends with `shl; sar` (6 bytes). |
| `OpShift` | `A=0 HighA=2 B=0 HighB=2 C=1 Bits=64 Imm=7 Arith=OpShiftLeft` | 7 | Bits=64 shifts the pair B:HighB into A:HighA with `shld`/`shrd`. C is a scratch GP register, distinct from the pair registers. |
| `OpShift` | `A=0 HighA=2 B=0 HighB=2 C=1 Bits=64 Imm=32 Arith=OpShiftLeft` | 7 | A count of 32 or more is a word move plus a shift and/or zeroing. |
| `OpShift` | `A=0 HighA=2 B=0 HighB=2 C=1 Bits=64 Imm=7 Arith=OpRotateLeft` | 10 | Rotations use C to hold the old low word; the count is taken mod 64. |
| `OpShift` | `A=0 HighA=2 B=2 HighB=0 C=1 Bits=64 Imm=32 Arith=OpShiftRightArithmetic` | 11 | Swapped source pairs are allowed: they are copied through C (6 bytes). |
| `OpShift` | `A=0 HighA=9 B=0 HighB=2 C=1 Bits=64 Imm=7 Arith=OpShiftLeft` | rejected | HighA, HighB and HighC must be GP registers. |
| `OpShiftBy` | `A=0 B=0 C=1 Bits=32 Arith=OpRotateLeft` | 2 | Fixed registers: the value is in EAX (EDX:EAX for 64), the unsigned count in ECX (EBX:ECX for 64), and the result goes to EAX (EDX:EAX). A, B, C and High* are only range-checked. Clobbers EAX, ECX, EDX and EBX. |
| `OpShiftBy` | `A=0 B=0 C=1 Bits=8 Arith=OpRotateLeft` | 8 | 8/16-bit rotates are `rol AL/AX, CL` plus a 6-byte mask. |
| `OpShiftBy` | `A=0 B=0 C=1 Bits=32 Arith=OpShiftLeft` | 29 | Shifts test both count words and give 0 (or the sign fill) for count ≥ Bits, using private rel32 branches. |
| `OpShiftBy` | `A=0 B=0 C=1 Bits=8 Arith=OpShiftRightArithmetic` | 48 | 8/16-bit forms add sign-extend and mask steps. |
| `OpShiftBy` | `A=0 HighA=2 B=0 HighB=2 C=1 HighC=3 Bits=64 Arith=OpShiftLeft` | 60 | 64-bit shifts handle the counts below 32 and the counts of 32 or more separately. |
| `OpShiftBy` | `A=0 HighA=2 B=0 HighB=2 C=1 HighC=3 Bits=64 Arith=OpRotateRight` | 39 | 64-bit rotates mask the count with 63. |
| `OpShiftBy` | `A=8 B=0 C=1 Bits=32 Arith=OpShiftLeft` | rejected | The operands must still be GP registers. |

## Wide multiply and carries

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpMulWide` | `A=1` | 2 | `mul A`: EDX:EAX = EAX × A (unsigned). EAX and EDX are fixed. Only 386 accepts it. |
| `OpMulWide` | `A=8` | rejected | A must be a GP register. |
| `OpMulHigh` | `A=2 B=0 C=1 Bits=32` | 2 | A = high 32 bits of B × C: `mov EAX, B; mul C; mov A, EDX`. Moves are omitted when B=EAX and A=EDX. Clobbers EAX and EDX. |
| `OpMulHigh` | `A=1 B=3 C=6 Bits=32` | 6 | C must not be EAX unless B is EAX. Bits is 0, 8, 16 or 32 and is not masked; the policy admits only 32. |
| `OpMulHighSigned` | `A=2 B=0 C=1 Bits=32` | 2 | A = high 32 bits of signed B × C: `mov EAX, B; imul C; mov A, EDX`. Moves are omitted when B=EAX and A=EDX. Clobbers EAX and EDX. |
| `OpMulHighSigned` | `A=1 B=3 C=6 Bits=32` | 6 | Both moves. |
| `OpMulHighSigned` | `A=1 B=3 C=0 Bits=32` | rejected | C must not be EAX unless B is EAX. |
| `OpMulHighSigned` | `A=1 B=3 C=6 Bits=16` | rejected | Only 32 bits; selection expands the other widths through 64-bit products. |
| `OpCarryBit` | `A=0 B=1 C=2 Arith=OpAdd Carry=2` | 10 | A = carry (or borrow for OpSub) of B+C: `mov EBX, B; add/sub EBX, C; setb BL; movzx A, BL`. Clobbers EBX, so C must not be EBX. |
| `OpCarryBit` | `A=0 Carry=3` | 6 | With CarryFlags it materializes the incoming CF only. |
| `OpWordCarry` | `A=0 B=0 C=1 D=2 Bits=32 Arith=OpAdd` | 6 | CarryValue/CarryBitValue: `bt D, 0; adc A, C` (OpSub gives `sbb`). A ← B + C + carry. |
| `OpWordCarry` | `A=0 B=0 C=1 D=2 E=6 Bits=32 Arith=OpSub Carry=2` | 6 | CarryOperands: the borrow of D−E is formed in EBX (clobbered), then `sbb A, C`. |
| `OpWordCarry` | `A=0 B=6 C=1 Bits=32 Arith=OpAdd Carry=3` | 4 | CarryFlags consumes CF. A≠B adds a move, so A must not equal C unless A=B. |
| `OpWordCarry` | `A=0 B=0 C=1 D=9 Bits=32 Arith=OpAdd` | rejected | All operands must be GP registers. |

## Division and conversions

These expansions branch only within themselves, through private rel8 or
rel32 branches chosen from the expansion's own layout, so their size does not
depend on the PC. All of them write flags.

`OpDivide` up to 32 bits divides EDX:EAX: B goes to EAX first and A is
written last, so A and B may be any GP register and may alias each other or
C; C must be neither EAX nor EDX. Narrow operands are zero-extended: unsigned
division uses the 32-bit `div`, signed 16-bit `cwd; idiv C16`, and signed
8-bit `cbw; mov EDX, C; idiv DL` (C may be ESI or EDI, which have no byte
register). Imm is the `op.DivisionSemantics`. Checked semantics test C for
zero and, when signed, the Bits-wide MIN / -1, and give those results
without dividing; DivisionUnchecked divides directly and traps on them.

A 64-bit `OpDivide` reads the dividend from slots Slot and Slot+1 (low, high)
and the divisor from Slot+2 and Slot+3, overwrites those four slots, and
leaves the result in EDX:EAX (A=0 and HighA=2 are required). It clobbers EAX,
EBX, ECX, EDX, ESI and EDI. A divisor below 2^32 takes two `div`s; a larger
one estimates the 32-bit quotient with one `div` by the normalized top
divisor word (`bsr`, `shld`) and corrects it once. Signed division divides
the magnitudes and negates, which also gives MIN / -1 = MIN.

`OpIntToFloat` converts GP B (a pair B:HighB when Imm=64) to XMM A; Bits is
the float width. Signed words use `cvtsi2sd/ss`. An unsigned word of 2^31 or
more is built as binary64 bits in A (`movd; psllq; pextrw/pinsrw` through
EAX, which it clobbers) and narrowed with `cvtsd2ss` for binary32. A pair
uses slots Slot and Slot+1 for memory operands: it forms
(2^84 + hi·2^32) − (2^84 + 2^52) + (2^52 + lo), with a single rounding, and
for binary32 first rounds to odd at bit 11 so that `cvtsd2ss` rounds once.
No FP register other than A is written.

`OpFloatToInt` converts XMM B to GP A (EDX:EAX, A=0 and HighA=2, when
Bits=64); Disp is the float width and Imm the `op.ConversionSemantics`.
Signed words use `cvttsd2si/cvttss2si`; saturation turns the indefinite
0x80000000 into 0 for NaN and into MIN or MAX by B's sign. Other forms decode
B's bits in EAX, ECX and EDX, which they clobber, and saturate (NaN gives 0)
under either semantics. B is preserved.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpDivide` | `A=3 B=6 C=7 Bits=32 Arith=OpDiv Imm=2` | 8 | `mov EAX, B; xor EDX, EDX; div C; mov A, EAX`. Clobbers EAX and EDX. |
| `OpDivide` | `A=0 B=0 C=1 Bits=32 Arith=OpDivSigned Imm=2` | 3 | `cdq; idiv C`; the moves are omitted when B and A are EAX. |
| `OpDivide` | `A=3 B=6 C=7 Bits=32 Arith=OpDiv` | 16 | DivisionZero: x/0 = 0, x%0 = x. |
| `OpDivide` | `A=3 B=6 C=7 Bits=32 Arith=OpDiv Imm=1` | 19 | DivisionAllOnes: x/0 is all ones, Bits wide. |
| `OpDivide` | `A=6 B=6 C=7 Bits=32 Arith=OpRemSigned` | 32 | Signed forms also test MIN / -1: MIN / -1 = MIN, MIN % -1 = 0. A may equal B or C. |
| `OpDivide` | `A=3 B=6 C=7 Bits=16 Arith=OpDivSigned` | 38 | `cwd; idiv C16; movzx A, AX`. |
| `OpDivide` | `A=3 B=6 C=7 Bits=8 Arith=OpRemSigned Imm=2` | 11 | `cbw; mov EDX, C; idiv DL; movzx A, AH`. |
| `OpDivide` | `A=3 B=6 C=2 Bits=32 Arith=OpDiv` | rejected | C must not be EAX or EDX. |
| `OpDivide` | `A=3 B=6 C=7 Bits=24 Arith=OpDiv` | rejected | Bits is 8, 16, 32 or 64. |
| `OpDivide` | `A=3 B=6 C=7 Bits=32 Arith=OpDiv Imm=3` | rejected | Imm must be a defined `op.DivisionSemantics`. |
| `OpDivide` | `A=3 B=6 C=7 Bits=32 Arith=OpMul` | rejected | Arith is OpDiv, OpDivSigned, OpRem or OpRemSigned. |
| `OpDivide` | `A=0 HighA=2 Bits=64 Slot=0 Arith=OpDiv` | 134 | Pair division from four slots into EDX:EAX. |
| `OpDivide` | `A=0 HighA=2 Bits=64 Slot=4 Arith=OpRem Imm=2` | 127 | Unchecked: no zero test; a zero divisor traps. |
| `OpDivide` | `A=0 HighA=2 Bits=64 Slot=0 Arith=OpDivSigned` | 202 | Signed: the magnitudes are divided and the result negated. |
| `OpDivide` | `A=0 HighA=2 Bits=64 Slot=0 Arith=OpRemSigned Imm=1` | 199 | The remainder takes the dividend's sign. |
| `OpDivide` | `A=1 HighA=2 Bits=64 Slot=0 Arith=OpDiv` | rejected | The 64-bit result is fixed in EDX:EAX. |
| `OpIntToFloat` | `V=A:3 B=1 Bits=64 Imm=32 Arith=OpFFromSigned` | 4 | `cvtsi2sd A, B`. |
| `OpIntToFloat` | `V=A:3 B=1 Bits=32 Imm=32 Arith=OpFFromSigned` | 4 | `cvtsi2ss A, B`. |
| `OpIntToFloat` | `V=A:3 B=1 Bits=64 Imm=32 Arith=OpFFromUnsigned` | 35 | Unsigned word: a value of 2^31 or more is built in A. Clobbers EAX; B may be EAX. |
| `OpIntToFloat` | `V=A:3 B=1 Bits=32 Imm=32 Arith=OpFFromUnsigned` | 39 | Adds `cvtsd2ss A, A`. |
| `OpIntToFloat` | `V=A:3 B=0 HighB=2 Bits=64 Imm=64 Slot=0 Arith=OpFFromSigned` | 85 | Pair source through slots Slot and Slot+1. |
| `OpIntToFloat` | `V=A:3 B=0 HighB=2 Bits=64 Imm=64 Slot=0 Arith=OpFFromUnsigned` | 74 | |
| `OpIntToFloat` | `V=A:3 B=0 HighB=2 Bits=32 Imm=64 Slot=0 Arith=OpFFromSigned` | 135 | Binary32 adds round-to-odd from 2^53 on and `cvtsd2ss`. |
| `OpIntToFloat` | `V=A:3 B=0 HighB=2 Bits=32 Imm=64 Slot=0 Arith=OpFFromUnsigned` | 116 | |
| `OpIntToFloat` | `A=3 B=1 Bits=64 Imm=32 Arith=OpFFromSigned` | rejected | A must be an XMM register. |
| `OpIntToFloat` | `V=A:3 B=1 Bits=64 Imm=16 Arith=OpFFromSigned` | rejected | Imm is 32 or 64. |
| `OpFloatToInt` | `A=3 V=B:4 Bits=32 Disp=64 Imm=1 Arith=OpFToSigned` | 4 | ConversionNative: `cvttsd2si A, B`. |
| `OpFloatToInt` | `A=3 V=B:4 Bits=32 Disp=32 Arith=OpFToSigned` | 33 | ConversionSaturate fixes up 0x80000000 with `ucomiss` and `movmskps`, in A alone. |
| `OpFloatToInt` | `A=3 V=B:4 Bits=32 Disp=64 Arith=OpFToUnsigned` | 81 | Decoded in EAX, ECX and EDX, then moved to A. |
| `OpFloatToInt` | `A=0 HighA=2 V=B:4 Bits=64 Disp=64 Arith=OpFToSigned` | 123 | Into EDX:EAX; clobbers ECX. |
| `OpFloatToInt` | `A=0 HighA=2 V=B:4 Bits=64 Disp=32 Imm=1 Arith=OpFToUnsigned` | 75 | Native out-of-range results are those of saturation. |
| `OpFloatToInt` | `A=1 HighA=2 V=B:4 Bits=64 Disp=64 Arith=OpFToSigned` | rejected | The 64-bit result is fixed in EDX:EAX. |
| `OpFloatToInt` | `A=3 B=1 Bits=32 Disp=64 Arith=OpFToSigned` | rejected | B must be an XMM register. |

## Selection and comparison

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpSelect` | `A=0 B=1 C=2 D=3` | 7 | A = B≠0 ? C : D: `mov A, D; test B, B; cmovne A, C`. Needs CMOV. |
| `OpSelect` | `A=0 B=1 C=2 D=0` | 5 | A may equal D. A must not equal B or C unless it also equals D. |
| `OpSelectFlags` | `A=0 C=1 D=2 Cond=Equal` | unsupported | |
| `OpCompareImmediate` | `A=0 Imm=5` | 6 | `cmp A, imm32` (81 /7). There is no imm8 form. Bits must be 0 or 32; any Cmp mode is accepted. |
| `OpCompareImmediate` | `A=3 SImm=-1 Bits=32 Cmp=Signed` | 6 | Imm is used as a sign-extended 32-bit value. |
| `OpCompareImmediate` | `A=0 Imm=5 Bits=64` | rejected | There are no 64-bit or narrow compares; compare pairs word by word. |
| `OpCompareRegister` | `A=0 B=1` | 2 | `cmp A, B`. |
| `OpCompareRegister` | `A=0 B=1 Bits=32 Cmp=Unsigned` | 2 | |
| `OpCompareRegister` | `A=0 B=1 Bits=16` | rejected | Bits must be 0 or 32. |

## Memory

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpLoad64` | `A=0 B=1` | 2 | `mov A, [B]`: one 32-bit word, as the native word. |
| `OpLoad64` | `A=0 B=4` | 3 | Base ESP needs a SIB byte. |
| `OpLoad64` | `A=0 B=5` | 3 | Base EBP needs a zero disp8. |
| `OpLoad64` | `A=0 B=1 BigEndian` | rejected | Only native (little-endian) order is accepted. |
| `OpStore64` | `A=1 B=0` | 2 | `mov [A], B`; A is the address and B the value. |
| `OpLoad8` | `A=0 B=1` | 3 | `movzx A, byte [B]`. |
| `OpLoad8` | `A=0 B=4` | 4 | Base ESP adds a SIB byte. |
| `OpStore8` | `A=1 B=2` | 2 | `mov [A], B8`. |
| `OpStore8` | `A=1 B=6` | rejected | The value must be EAX, ECX, EDX or EBX (AL-BL); codes 4-7 would name AH-BH. |
| `OpLoad16` | `A=0 B=1` | 3 | `movzx A, word [B]`. |
| `OpLoad16` | `A=0 B=1 BigEndian` | rejected | Only native order. |
| `OpStore16` | `A=1 B=6` | 3 | `mov [A], B16` (0x66 prefix); any GP value register. |
| `OpLoad32` | `A=0 B=5` | 3 | `mov A, [B]`. |
| `OpStore32` | `A=1 B=0` | 2 | `mov [A], B`. |
| `OpLoadAddress` | `A=0 B=1 C=2 Imm=4` | 3 | `mov A, [B + C*Imm + Disp]`. Imm is the scale: 1, 2, 4 or 8. C must not be ESP. |
| `OpLoadAddress` | `A=0 B=1 C=2 Imm=4 Disp=8` | 4 | A Disp that fits in a signed 8-bit field adds 1 byte (also used for base EBP with Disp 0). |
| `OpLoadAddress` | `A=0 B=1 C=2 Imm=8 Disp=0x1000` | 7 | Otherwise Disp adds a disp32. |
| `OpStoreAddress` | `A=1 B=2 C=0 Imm=4` | 3 | `mov [A + B*Imm + Disp], C`: A is the base, B the index and C the value. |
| `OpStoreAddress` | `A=5 B=2 C=0 Imm=1` | 4 | Base EBP always gets a displacement. |
| `OpLoadOffset` | `A=0 B=1 Disp=0` | 2 | `mov A, [B + Disp]`. Disp is a signed 32-bit offset. |
| `OpLoadOffset` | `A=0 B=1 Disp=0xfffffff8` | 3 | A disp8 is used for −128..127. |
| `OpLoadOffset` | `A=0 B=4 Disp=0x200` | 7 | Base ESP adds a SIB byte; a larger Disp uses disp32. |
| `OpStoreOffset` | `A=1 B=0 Disp=8` | 3 | `mov [A + Disp], B`. |
| `OpStoreOffset` | `A=1 B=8 Disp=8` | rejected | All operands must be GP registers. |
| `OpLoadOffset` | `A=0 B=1 Disp=0x100000000` | rejected | Disp is a 32-bit value, unsigned or sign-extended. |

## Calls, frames and spill slots

`OpFrameEnter Imm=F` pushes EBX, ESI, EDI and EBP, then subtracts `F-16` from
ESP, so F includes the 16 bytes of pushes. Under `abi.StackPlatform`, spill slot
s is the word at `[ESP + CallArea + s*4]`, where CallArea is the
`CallAreaBytes` of the preceding `OpFrameEnter` in the same assembler (0 when an
example is assembled alone). `OpLoadIncoming Imm=k` reads `[ESP + F + k]`:
k is an offset from the entry ESP, so k=0 is the return address and k=4 is the
first cdecl argument. Slot and incoming accesses always use a disp32, so their
size does not depend on the frame.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpSyscall` | — | 2 | `int 0x80`. |
| `OpCallC` | — | 2 | `call EAX`: the target is in EAX. The caller places the arguments in the outgoing area. |
| `OpCallGo` | — | 2 | Same encoding as `OpCallC` (`call EAX`). |
| `OpSaveLink` | `Slot=0` | 0 | The return address is on the stack; nothing to save. |
| `OpRestoreLink` | `Slot=0` | 0 | |
| `OpSpill` | `A=0 Slot=0` | 7 | `mov [ESP + CallArea + Slot*4], A`. |
| `OpSpill` | `A=3 Slot=5` | 7 | Always disp32 with a SIB byte. |
| `OpSpill` | `A=8 Slot=0` | rejected | A must be a GP register. |
| `OpReload` | `A=6 Slot=2` | 7 | `mov A, [ESP + CallArea + Slot*4]`. |
| `OpLoadIncoming` | `A=0 Imm=4` | 7 | `mov A, [ESP + F + Imm]`. |
| `OpLoadIncoming` | `A=8 Imm=4` | rejected | A must be a GP register. |
| `OpFrameEnter` | `Imm=32` | 10 | 4 pushes plus `sub ESP, imm32`. Imm must be at least 16 (see `FrameBytesForCallArea`). |
| `OpFrameEnter` | `Imm=48 CallArea=16` | 10 | CallArea sets the slot base for the spills and reloads that follow. |
| `OpFrameEnter` | `Imm=2097152` | rejected | Imm and CallArea must be at most 1 MiB. |
| `OpFrameEnter` | `Imm=8` | rejected | Below the 16 bytes of saved registers. |
| `OpFrameLeave` | `Imm=32` | 10 | `add ESP, imm32` plus 4 pops. Imm must match `OpFrameEnter`. |
| `OpReturn` | — | 1 | `ret`. |
| `OpSaveCFloat` | `V=A:3 Slot=0` | unsupported | The cdecl ABI has no callee-saved XMM registers. |
| `OpRestoreCFloat` | `V=A:3 Slot=0` | unsupported | |
| `OpSaveCVector` | `V=A:3 Slot=0` | unsupported | |
| `OpRestoreCVector` | `V=A:3 Slot=0` | unsupported | |
| `OpSaveCondition` | `Slot=0` | unsupported | |
| `OpRestoreCondition` | `Slot=0` | unsupported | |

## Branches

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpJump` | `Imm=0x100` | 5 | `jmp rel32`. Long and Far are ignored; there is no short form. |
| `OpJump` | `Imm=0x100 Long` | 5 | |
| `OpJumpIf` | `Cond=Less Imm=0x100` | 6 | `jcc rel32`. Cond: Equal through Above, Sign, NotSign. |
| `OpJumpIf` | `Cond=NotSign Imm=0x40 Far` | 6 | Long and Far are ignored. |
| `OpJumpIf` | `Cond=Carry Imm=0x40` | rejected | Carry, NotCarry, Carry32 and NotCarry32 are not encoded; use Below/AboveEqual. |
| `OpBranchZero` | `A=0 Bits=32 Cond=NotEqual Imm=0x40` | unsupported | Use `OpCompareImmediate` and `OpJumpIf`. |
| `OpBranchBit` | `A=0 Bits=32 Disp=3 Cond=NotEqual Imm=0x40` | unsupported | |
| `OpLoop` | `Imm=0x40` | unsupported | |
| `OpSetCount` | `A=1` | unsupported | |

## Scalar floating point (SSE2)

Bits=64 selects binary64 (`sd`) and any other value selects binary32 (`ss`).
A float operand is an XMM register or a GP word (binary32) or GP pair
(binary64, with the high word in High*). GP operands are moved through the
XMM0-XMM2 temporaries.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpFloatMove` | `V=A:3,B:4 Bits=64` | 4 | `movapd`. |
| `OpFloatMove` | `V=A:3,B:3 Bits=64` | 0 | A self-move emits nothing. |
| `OpFloatMove` | `V=A:3 B=1 Bits=32` | 4 | GP to XMM: `movd`. |
| `OpFloatMove` | `V=A:3 B=0 HighB=2 Bits=64` | 12 | GP pair to XMM: `movd; movd xmm2; punpckldq`. Clobbers XMM2. |
| `OpFloatMove` | `A=0 V=B:3 Bits=32` | 4 | XMM to GP: `movd`. |
| `OpFloatMove` | `A=0 HighA=2 V=B:3 Bits=64` | 13 | XMM to GP pair: `movd; pshufd xmm2, B, 0x55; movd`. B is preserved. |
| `OpFloatMove` | `V=A:8,B:3 Bits=64` | rejected | Only XMM0-XMM7 exist. |
| `OpFloatBinary` | `V=A:3,B:3,C:4 Bits=64 Arith=OpFAdd` | 4 | `addsd A, C` when A=B. Arith: OpFAdd, OpFSub, OpFMul, OpFDiv. |
| `OpFloatBinary` | `V=A:3,B:4,C:5 Bits=32 Arith=OpFMul` | 8 | A≠B adds a 4-byte `movapd`. |
| `OpFloatBinary` | `V=A:3,B:4,C:3 Bits=64 Arith=OpFSub` | 12 | When A=C≠B, C is first copied to a temporary (XMM0-XMM2). |
| `OpFloatBinary` | `A=0 B=1 C=2 Bits=32 Arith=OpFAdd` | 16 | GP binary32 operands go through XMM0 (left/result) and XMM1 (right). |
| `OpFloatBinary` | `A=0 HighA=2 B=0 HighB=2 C=1 HighC=3 Bits=64 Arith=OpFDiv` | 41 | GP binary64 pairs; clobbers XMM0-XMM2. |
| `OpFloatBinary` | `V=A:3,B:3,C:4 Bits=64 Arith=OpAdd` | rejected | Integer Arith values are not float operations. |
| `OpFloatUnary` | `V=A:3,B:4 Bits=64 Arith=OpFSqrt` | 4 | `sqrtsd`. Arith is OpFSqrt or OpFConvert. |
| `OpFloatUnary` | `V=A:3,B:4 Bits=64 Arith=OpFConvert` | 4 | Bits is the result width: 64 is `cvtss2sd` and 32 is `cvtsd2ss`. |
| `OpFloatUnary` | `V=A:3 B=0 HighB=2 Bits=64 Arith=OpFSqrt` | 16 | GP pair input goes through XMM0/XMM2. |
| `OpFloatUnary` | `A=0 B=0 HighB=2 Bits=32 Arith=OpFConvert` | 20 | Binary64 GP pair to binary32 GP word. |
| `OpFloatUnary` | `V=A:3,B:4 Bits=64 Arith=OpFAdd` | rejected | |
| `OpFloatFMA` | `V=A:3,B:4,C:5,D:6 Bits=64` | unsupported | `Policy.SupportsFloatFMA` is false. |
| `OpFloatBits` | `V=A:3,B:4 Bits=64 Arith=OpXor` | 8 | A = B xor XMM0 (`xorpd`). The mask must already be in XMM0 (`OpLiteralLoad V=A:0 Bits=128`). |
| `OpFloatBits` | `V=A:3,B:3 Bits=32 Arith=OpAnd` | 4 | OpAnd gives `andpd` and OpXor gives `xorpd`. A must be an XMM register other than XMM0. |
| `OpFloatBits` | `V=A:3 B=0 HighB=2 Bits=64 Arith=OpAnd` | 20 | A GP pair source goes through XMM1/XMM2, then is copied to A. |
| `OpFloatBits` | `A=3 V=B:4 Bits=32 Arith=OpXor` | rejected | A GP destination would overwrite the XMM0 mask. |
| `OpFloatBits` | `V=A:0,B:4 Bits=64 Arith=OpXor` | rejected | |
| `OpFloatBits` | `V=A:3,B:4 Bits=64 Arith=OpOr` | rejected | |
| `OpFloatSpill` | `V=A:3 Slot=2 Bits=64` | 9 | `movsd [ESP + CallArea + Slot*4], A`; binary64 spans two adjacent slots. |
| `OpFloatSpill` | `V=A:3 Slot=2 Bits=32` | 9 | `movss`. |
| `OpFloatSpill` | `V=A:9 Slot=2 Bits=64` | rejected | A must be XMM0-XMM7. |
| `OpFloatSpill` | `A=3 Slot=2 Bits=64` | rejected | |
| `OpFloatReload` | `V=A:4 Slot=0 Bits=64` | 9 | `movsd A, [ESP + CallArea + Slot*4]`. |
| `OpFloatCompare` | `A=1 V=B:2,C:3 Bits=64 Imm=6` | 9 | A = 1 when the Bits-wide floats in XMM B and C satisfy the `op.FloatCondition` in Imm, else 0. For A in EAX..EBX: `xor A, A; ucomisd B, C; setcc A8` (`ucomiss` for Bits=32). FloatGreater is `seta`, GreaterEqual `setae`, UnorderedLessEqual `setbe`, UnorderedLess `setb`, OrderedNotEqual `setne`, UnorderedEqual `sete`, Ordered `setnp` and Unordered `setp`; FloatLess, LessEqual, UnorderedGreaterEqual and UnorderedGreater compare C with B and use `seta`, `setae`, `setbe` and `setb`. Writes flags; no register other than A. |
| `OpFloatCompare` | `A=1 V=B:2,C:3 Bits=64 Imm=0` | 12 | FloatEqual is (CF or ZF) minus CF: `setbe A8; sbb A, 0`. FloatNotEqual is (neither CF nor ZF) plus CF: `seta A8; adc A, 0`. |
| `OpFloatCompare` | `A=6 V=B:2,C:3 Bits=64 Imm=6` | 9 | ESP..EDI have no byte register: `xor A, A; ucomisd; jbe +1; inc A`, the inverse branch standing in for `setcc`. |
| `OpFloatCompare` | `A=6 V=B:2,C:3 Bits=64 Imm=0` | 12 | Equal and NotEqual then add the `sbb`/`adc` fixup; `inc` keeps CF. |
| `OpFloatCompare` | `A=6 V=B:2,C:3 Bits=64 Imm=9` | 8 | Carry alone needs no branch: `ucomisd; sbb A, A; neg A` for `setb`. |
| `OpFloatCompare` | `A=7 V=B:2,C:3 Bits=32 Imm=8` | 6 | `ucomiss; sbb A, A; inc A` for `setae`. |
| `OpFloatCompare` | `A=1 V=B:2,C:3 Bits=16 Imm=0` | rejected | Bits must be 32 or 64. |
| `OpFloatCompare` | `A=1 V=B:2,C:3 Bits=64 Imm=14` | rejected | Imm must be an `op.FloatCondition`. |
| `OpFloatCompare` | `A=1 V=B:8,C:3 Bits=64 Imm=0` | rejected | Only XMM0-XMM7 exist. |
| `OpFloatCompare` | `A=1 B=2 V=C:3 Bits=64 Imm=0` | rejected | B and C must be XMM registers. |

## Vectors (128-bit SSE2 integer)

Bits is the lane width. `VectorType` is not read by the encoder. Operands are
XMM0-XMM7 except where the table says GP. Two-address forms first copy B to A
(`movdqa`, 4 bytes) when A≠B. When A=C≠B, C is first saved in XMM1.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpVectorMove` | `V=A:3,B:4` | 4 | `movdqa`. |
| `OpVectorMove` | `V=A:3,B:3` | 0 | |
| `OpVectorMove` | `A=0 V=B:3` | rejected | Both operands must be XMM registers. |
| `OpVectorSpill` | `V=A:3 Slot=4` | 9 | `movdqu [ESP + CallArea + Slot*4], A`; disp32. A vector takes four slots. |
| `OpVectorSpill` | `A=3 Slot=4` | rejected | A must be an XMM register. |
| `OpVectorReload` | `V=A:5 Slot=0` | 9 | `movdqu A, [ESP + CallArea + Slot*4]`. |
| `OpVector` | `V=A:3 B=1 Arith=OpVLoad Vec=4x32` | 8 | `movdqu A, [B]` with disp32 0. B is a GP address. |
| `OpVector` | `V=A:3 B=4 Arith=OpVLoad` | 9 | Base ESP adds a SIB byte. |
| `OpVector` | `A=1 V=B:3 Arith=OpVStore` | 8 | `movdqu [A], B`. A is a GP address. |
| `OpVector` | `V=A:3 Arith=OpVZero` | 4 | `pxor A, A`. |
| `OpVector` | `V=A:3 B=1 Bits=32 Arith=OpVSplat` | 9 | `movd; pshufd 0`. B is a GP scalar. |
| `OpVector` | `V=A:3 B=1 Bits=8 Arith=OpVSplat` | 18 | Bits 8 adds `punpcklbw` and `pshuflw`; Bits 16 adds `pshuflw` (14 bytes). |
| `OpVector` | `V=A:3 B=0 HighB=2 Bits=64 Arith=OpVSplat` | 16 | The 64-bit scalar is the GP pair B:HighB; clobbers XMM2. |
| `OpVector` | `A=0 V=B:3 Bits=32 Imm=0 Arith=OpVExtract` | 8 | GP A = lane Imm of B, through XMM0. |
| `OpVector` | `A=0 V=B:3 Bits=32 Imm=1 Arith=OpVExtract` | 13 | A nonzero lane adds `psrldq`. |
| `OpVector` | `A=0 V=B:3 Bits=16 Imm=2 Arith=OpVExtract` | 14 | 16-bit lanes use `pextrw`. |
| `OpVector` | `A=0 V=B:3 Bits=8 Imm=3 Arith=OpVExtract` | 23 | 8-bit lanes zero-extend with `pslld; psrld`. |
| `OpVector` | `A=0 HighA=2 V=B:3 Bits=64 Imm=1 Arith=OpVExtract` | 22 | 64-bit lanes write A:HighA; clobbers XMM2. |
| `OpVector` | `A=0 HighA=0 V=B:3 Bits=64 Imm=1 Arith=OpVExtract` | rejected | HighA must differ from A. |
| `OpVector` | `V=A:3,B:4 C=1 Bits=16 Imm=2 Arith=OpVInsert` | 9 | `pinsrw`; C is a GP scalar. |
| `OpVector` | `V=A:3,B:4 C=1 Bits=32 Imm=2 Arith=OpVInsert` | rejected | OpVInsert accepts only Bits=16; use OpVInsertZero. |
| `OpVector` | `V=A:3,B:3 C=1 Bits=32 Imm=0 Arith=OpVInsertZero` | 25 | Replaces lane Imm through an XMM0 mask; any lane width 8/16/32/64. |
| `OpVector` | `V=A:3,B:4 C=1 Bits=32 Imm=1 Arith=OpVInsertZero` | 39 | A≠B and a nonzero lane add a copy and shifts. |
| `OpVector` | `V=A:3,B:3 C=1 Bits=8 Imm=0 Arith=OpVInsertZero` | 35 | Narrow lanes clear the bits above the scalar. |
| `OpVector` | `V=A:3,B:3 C=0 HighC=2 Bits=64 Imm=1 Arith=OpVInsertZero` | 43 | 64-bit lanes take the GP pair C:HighC. |
| `OpVector` | `V=A:3,B:3,C:4 Bits=32 Arith=OpAdd` | 4 | `padd{b,w,d,q}` for Bits 8/16/32/64; OpSub is `psub*`. |
| `OpVector` | `V=A:3,B:4,C:5 Bits=8 Arith=OpSub` | 8 | A≠B adds `movdqa`. |
| `OpVector` | `V=A:3,B:4,C:3 Bits=64 Arith=OpAdd` | 12 | A=C≠B saves C in XMM1 first. |
| `OpVector` | `V=A:3,B:3,C:4 Bits=16 Arith=OpMul` | 4 | `pmullw`. Only Bits=16. |
| `OpVector` | `V=A:3,B:3,C:4 Bits=32 Arith=OpMul` | rejected | There is no 32-bit lane multiply in SSE2. |
| `OpVector` | `V=A:3,B:4,C:5 Bits=32 Arith=OpXor` | 8 | OpAnd, OpOr and OpXor: `pand`, `por` and `pxor`, for any Bits. |
| `OpVector` | `V=A:3,B:4 Bits=32 Imm=5 Arith=OpShiftLeft` | 9 | `pslld`/`psrld` by Imm; only Bits=32. |
| `OpVector` | `V=A:3,B:3 Bits=32 Imm=40 Arith=OpShiftRight` | 4 | A count of 32 or more gives `pxor A, A`. |
| `OpVector` | `V=A:3,B:4 Bits=32 Imm=5 Arith=OpRotateLeft` | 22 | Rotates combine two shifts through XMM0; the count is taken mod 32. |
| `OpVector` | `V=A:3,B:4 Bits=16 Imm=5 Arith=OpShiftLeft` | rejected | Shifts and rotates need Bits=32. |
| `OpVector` | `V=A:3,B:4 Bits=32 Imm=5 Arith=OpShiftRightArithmetic` | rejected | No arithmetic vector shift. |
| `OpVector` | `V=A:3,B:4,C:5 Bits=8 Imm=0 Arith=OpVCompare` | 8 | `pcmpeqb`; only Bits=8 and Imm=0 (BranchEqual). |
| `OpVector` | `V=A:3,B:4,C:5 Bits=32 Imm=0 Arith=OpVCompare` | rejected | Other lane widths are not encoded. |
| `OpVector` | `A=0 V=B:3 Arith=OpVMaskBits` | 4 | `pmovmskb A, B`; A is GP. |
| `OpVector` | `V=A:3,B:3,C:4 Bits=64 Imm=1 Arith=OpVCarrylessMul` | 6 | `pclmulqdq`; Imm bit 0 selects the B qword and bit 1 the C qword. Only Bits=64. |
| `OpVector` | `V=A:3,B:4,C:5 Bits=64 Imm=4 Arith=OpVCarrylessMul` | rejected | Imm must be at most 3. |
| `OpVector` | `V=A:3,B:4,C:8 Bits=32 Arith=OpAdd` | rejected | Only XMM0-XMM7. |
| `OpVector` | `V=A:3 Arith=OpVConst` | rejected | Vector constants are loaded with `OpLiteralLoad Bits=128`. |

## Barriers

`OpBarrier` reads and writes no registers or flags. `Barrier` names the
guarantee; `Policy.Barrier` reports whether the target's instruction rests on
the instruction set or also on host configuration.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpBarrier` | `Barrier=Speculation` | 3 | `lfence`. Platform: the host must make LFENCE dispatch-serializing. |
| `OpBarrier` | `Barrier=Memory` | 3 | `mfence`. |
| `OpBarrier` | `Barrier=Instruction` | unsupported | Code written by another thread needs a serializing instruction, and none leaves registers intact. |
| `OpBarrier` | — | rejected | Barrier must name a guarantee. |
