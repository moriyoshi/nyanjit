# arm64 instruction forms

This table is the arm64 (AArch64) contract for allocated `machine.Instruction`
values. Every example row is assembled by `backend/target/forms_test.go`.

The table assumes the following:

- The byte assembler is built with `NewByteAssembler(abi.StackPlatform, 0)`:
  the platform stack is in use and there are no Go argument homes. The link
  register is stored at 0(SP). Spill slot n is at 8*(n+1)(SP).
- The baseline CPU is ARMv8.0-A with FP and Advanced SIMD (NEON). The only
  optional extension encoded is PMULL, for `OpVCarrylessMul`; compilation
  selects it only for a CPU profile with `pmull`. Every instruction word is 4
  bytes, so all sizes are multiples of 4.
- Each example is assembled alone at PC 0. Branch and literal targets are
  absolute PCs: in `Imm` for branches, and in `Disp` for literal loads.
- The encoder is the same for the C and Go ABI flavors. Frames follow the
  AAPCS64 and Go layout: LR at 0(SP), and the caller's FP stored just below the
  incoming SP.
- `Bits` is the semantic width. A `Bits` value of 8, 16 or 32 selects the
  32-bit (W) register view, and 64 selects the X view. Where noted, `Bits=0`
  means the native 64-bit width.

GP registers are numbered 0..30 for X0..X30 (see `Policy.RegisterName`).
Encoders accept number 31, but it encodes SP or XZR depending on the
instruction field, so the allocator never assigns it. Registers 32 and above
are rejected wherever a GP operand is expected. X8 is the workspace register.
X16 and X17 are expansion temporaries (IP0/IP1), and X16 holds the target of
`OpCallC` and `OpCallGo`. X29 is the frame pointer and X30 the link register.

FP and vector registers are `backend.VectorRegister(n)`, which is 64+n, for
V0..V31 (written `V=A:n` in the examples). V16, V17 and V18 are expansion
temporaries for FP forms that take GP operands. V30 and V31 are vector
expansion temporaries. The allocatable vector homes are V0..V7 and V19..V29.

`PhysicalSize(ins, stack, goHomes)` is the authority for the size of any form
not listed here. It returns 0 for a form it rejects, and also for an empty move.

## Constants and moves

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpConst` | `A=0 Imm=0` | 4 | A is GP. One MOVZ/MOVN, plus one MOVK for each further 16-bit half. MOVN is chosen when more halves are 0xffff than zero. |
| `OpConst` | `A=0 Imm=0xffff` | 4 | One MOVZ. |
| `OpConst` | `A=0 Imm=0x12345` | 8 | MOVZ plus MOVK. |
| `OpConst` | `A=0 Imm=0xffffffffffff1234` | 4 | One MOVN. |
| `OpConst` | `A=0 Imm=0x123456789abcdef0` | 16 | Four halves: MOVZ plus three MOVK. |
| `OpConst` | `V=A:0 Imm=1` | rejected | A must be a GP register. |
| `OpSymbol` | `A=0 Sym=callee` | 16 | A is GP. Always MOVZ plus three MOVK, with a `RelocARM64WideMoves` relocation at the first word. |
| `OpSymbol` | `A=32 Sym=callee` | rejected | A must be a GP register (0..31). |
| `OpMove` | `A=0 B=1` | 4 | A = B, as ORR from XZR. A and B are GP. |
| `OpMove` | `A=3 B=3` | 0 | A move to itself emits nothing. |
| `OpMove` | `A=0 V=B:1` | rejected | GP only. Use `OpFloatMove` or `OpVectorMove` for the vector bank. |

## Integer arithmetic

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpBinary` | `A=0 B=1 C=2 Bits=64 Arith=OpAdd` | 4 | A = B op C. Arith is OpAdd, OpSub, OpMul, OpAnd, OpOr or OpXor. Bits is 0 (native 64), 8, 16, 32 or 64. OpAdd, OpSub and OpAnd use the flag-setting ADDS/SUBS/ANDS forms. Any aliasing is allowed. |
| `OpBinary` | `A=0 B=1 C=2 Bits=32 Arith=OpSub PreserveFlags` | 4 | Plain ADD/SUB/AND, leaving NZCV unchanged. Dead-flag elision selects it where no path reads the flags. OpMul, OpOr and OpXor never write flags. |
| `OpBinary` | `A=0 B=0 C=1 Bits=32 Arith=OpMul` | 4 | MADD with XZR. Widths below 64 use the W form. |
| `OpBinary` | `A=0 B=1 C=2 Arith=OpXor` | 4 | Bits=0 is the native 64-bit form. |
| `OpBinary` | `A=0 B=1 C=2 Bits=64 Arith=OpShiftLeft` | rejected | Only the six arithmetic and logical operations are accepted. |
| `OpBinary` | `A=0 B=1 C=2 Bits=24 Arith=OpAdd` | rejected | Bits must be 0, 8, 16, 32 or 64. |
| `OpBinaryImmediate` | `A=0 B=1 Imm=4095 Bits=64 Arith=OpAdd` | 4 | A = B op Imm. OpAdd and OpSub take an unsigned imm12, optionally shifted left by 12, and set flags (ADDS/SUBS) unless PreserveFlags. |
| `OpBinaryImmediate` | `A=0 B=1 Imm=0x7ff000 Bits=64 Arith=OpSub` | 4 | An imm12 shifted left by 12. |
| `OpBinaryImmediate` | `A=0 B=1 SImm=-16 Bits=32 Arith=OpAdd` | 4 | A negative immediate swaps ADD and SUB. The magnitude must still fit imm12 or imm12<<12. |
| `OpBinaryImmediate` | `A=0 B=1 Imm=4097 Bits=64 Arith=OpAdd` | rejected | Fits neither imm12 nor imm12<<12. |
| `OpBinaryImmediate` | `A=0 B=1 Imm=0xff00 Bits=64 Arith=OpAnd` | 4 | OpAnd (ANDS), OpOr and OpXor take an AArch64 logical bitmask immediate. Widths below 64 try the 32-bit pattern first. |
| `OpBinaryImmediate` | `A=0 B=1 Imm=0xff00 Bits=64 Arith=OpAnd PreserveFlags` | 4 | Plain ADD/SUB/AND, leaving NZCV unchanged. |
| `OpBinaryImmediate` | `A=31 B=1 Imm=1 Bits=64 Arith=OpAdd PreserveFlags` | rejected | Without the S bit register 31 is SP as the destination, so XZR keeps ADDS/SUBS/ANDS. |
| `OpBinaryImmediate` | `A=0 B=1 Imm=0x5555555555555555 Bits=64 Arith=OpOr` | 4 | A repeating 2-bit pattern is encodable. |
| `OpBinaryImmediate` | `A=0 B=1 Imm=0x1234 Bits=64 Arith=OpXor` | rejected | Not a rotated run of ones. Use a register `OpBinary`. |
| `OpBinaryImmediate` | `A=0 B=1 Imm=0 Bits=64 Arith=OpAnd` | rejected | 0 and all-ones are never logical immediates. |
| `OpBinaryImmediate` | `A=0 B=1 Imm=3 Bits=64 Arith=OpMul` | rejected | There is no multiply-immediate form. |
| `OpBinaryCarry` | `A=0 B=1 C=2 Arith=OpAdd` | 4 | Compatibility form. Visit rewrites it to `OpWordCarry` with Bits=32 and CarryFlags, which emits a single ADCS/SBCS W. Arith is OpAdd or OpSub. |
| `OpBinaryCarry` | `A=0 B=1 C=2 Arith=OpMul` | rejected | Arith must be OpAdd or OpSub. |
| `OpInc` | `A=0 B=0` | unsupported | Not implemented. Use `OpBinaryImmediate` OpAdd. |
| `OpDec` | `A=0 B=1` | 4 | A = B - 1 with SUBS, which writes NZCV. |
| `OpDec` | `A=0 B=1 PreserveFlags` | 4 | Plain SUB, leaving NZCV unchanged. |
| `OpDec` | `A=0 B=32` | rejected | GP registers only. |
| `OpNarrow` | `A=0 B=1 Imm=8` | 4 | A = zero-extended low Imm bits of B (UBFM). Imm is 1..64. |
| `OpNarrow` | `A=0 B=1 Imm=0` | rejected | Imm must be 1..64. |
| `OpSignExtend` | `A=0 B=1 Imm=32` | 4 | A = sign-extended low Imm bits of B (SBFM). Imm is 1..64. |
| `OpSignExtend` | `A=0 B=1 Imm=65` | rejected | Imm must be 1..64. |
| `OpScaled` | `A=0 B=1 C=2 Imm=8` | 4 | A = B + C*Imm (ADD with shifted register). Imm is a nonzero power of two. Disp must be 0. |
| `OpScaled` | `A=0 B=1 C=2 Imm=3` | rejected | The scale must be a power of two. |
| `OpScaled` | `A=0 B=1 C=2 Imm=8 Disp=8` | rejected | No displacement. |
| `OpMulAdd` | `A=0 B=1 C=2 D=3` | 4 | A = B + C*D (MADD, 64-bit). All operands are GP. |
| `OpMulHigh` | `A=0 B=1 C=2 Bits=64` | 4 | A = high Bits of unsigned B*C. Bits=64 is UMULH. |
| `OpMulHigh` | `A=0 B=1 C=2 Bits=32` | 8 | For 8, 16 and 32, MUL then LSR by Bits. The operands must already be zero-extended to Bits. |
| `OpMulHigh` | `A=0 B=1 C=2 Bits=24` | rejected | Bits must be 8, 16, 32 or 64. |
| `OpMulHighSigned` | `A=0 B=1 C=2 Bits=64` | 4 | A = high Bits of signed B*C. Bits=64 is SMULH. |
| `OpMulHighSigned` | `A=0 B=1 C=2 Bits=32` | 8 | SMULL of the W registers, then LSR X by 32. |
| `OpMulHighSigned` | `A=0 B=1 C=2 Bits=16` | 16 | For 8 and 16, SXTB/SXTH of B and C into W16 and W17, a 32-bit MUL, then UBFX of bits [Bits, 2*Bits). Clobbers X16 and X17. |
| `OpMulHighSigned` | `A=0 B=1 C=2 Bits=24` | rejected | Bits must be 8, 16, 32 or 64. |
| `OpMulWide` | `A=1` | rejected | 386 only: `machine.Visit` returns `ErrUnsupportedOperation`. Use `OpMulHigh`. |
| `OpCarryBit` | `A=0 B=1 C=2 Bits=64 Arith=OpAdd` | 8 | A = carry out of B+C (ADDS into X16, then CSET HS). For OpSub, A = borrow of B-C (SUBS, then CSET LO). Bits is 32 or 64. Clobbers X16. |
| `OpCarryBit` | `A=0 Bits=64 Arith=OpAdd Carry=3` | 4 | CarryFlags: CSET from the live NZCV only. |
| `OpCarryBit` | `A=0 B=1 C=2 Bits=16 Arith=OpAdd` | rejected | Bits must be 32 or 64. |
| `OpWordCarry` | `A=0 B=1 C=2 Bits=64 Arith=OpAdd Carry=3` | 4 | CarryFlags: A = B + C + C-flag (ADCS). For OpSub, SBCS uses the AArch64 no-borrow convention. |
| `OpWordCarry` | `A=0 B=1 C=2 D=3 E=4 Bits=64 Arith=OpAdd Carry=2` | 8 | CarryOperands: first ADDS/SUBS X16, D, E recomputes the carry from the low words. Clobbers X16. |
| `OpWordCarry` | `A=0 B=1 C=2 D=3 Bits=32 Arith=OpSub Carry=0` | 12 | CarryValue or CarryBitValue (Carry=0 or 1): AND X16, D, #1, then a CMP that sets C, then SBCS/ADCS. Clobbers X16. |
| `OpWordCarry` | `A=0 B=1 C=2 Bits=64 Arith=OpAdd Carry=4` | rejected | CarryMode must be 0..3. |
| `OpAndNot` | `A=0 B=1 C=2 Bits=64` | unsupported | Not implemented by the byte assembler (no BIC form). |
| `OpDivide` | `A=0 B=1 C=2 Bits=64 Arith=OpDiv` | 4 | A = B / C (UDIV; OpDivSigned is SDIV). Arith is OpDiv, OpDivSigned, OpRem or OpRemSigned. Bits is 8, 16, 32 or 64, and 32 or less uses the W form. Imm is the `op.DivisionSemantics`; DivisionZero (0) and DivisionUnchecked (2) are the bare instructions, which give x/0 = 0 and MIN/-1 = MIN. Any aliasing is allowed. |
| `OpDivide` | `A=1 B=1 C=2 Bits=32 Arith=OpRem` | 8 | A = B - (B/C)*C: UDIV into X16, then MSUB, which gives x%0 = x and MIN%-1 = 0. Clobbers X16. |
| `OpDivide` | `A=0 B=1 C=2 Bits=64 Arith=OpDivSigned Imm=1` | 12 | DivisionAllOnes: CMP C, #0 before the division, then CSINV A, A, ZR, NE writes all ones for a zero divisor. Writes NZCV. Remainders need no fixup. |
| `OpDivide` | `A=0 B=1 C=2 Bits=16 Arith=OpDiv Imm=1` | 16 | Below 32, a final UXTB/UXTH trims the all-ones quotient to Bits. |
| `OpDivide` | `A=0 B=1 C=0 Bits=8 Arith=OpRemSigned` | 20 | Narrow signed: SXTB/SXTH B and C into W16 and W17, 32-bit SDIV and MSUB into A, then UXTB/UXTH. The operands must be zero-extended. Clobbers X16 and X17. |
| `OpDivide` | `A=0 B=1 C=2 Bits=8 Arith=OpDivSigned Imm=1` | 24 | Narrow signed all-ones quotient: CMP, two sign extensions, SDIV, CSINV and UXTB. |
| `OpDivide` | `A=0 B=1 C=2 Bits=64 Arith=OpDiv Imm=3` | rejected | Imm must be a defined `op.DivisionSemantics`. |
| `OpDivide` | `A=0 B=1 C=2 Bits=24 Arith=OpDiv` | rejected | Bits must be 8, 16, 32 or 64. |
| `OpDivide` | `A=0 B=1 C=2 Bits=64 Arith=OpMul` | rejected | Arith must be a division or remainder. |
| `OpCountZeros` | `A=0 B=1 Bits=64 Arith=OpLeadingZeros` | 4 | CLZ. Bits=32 uses the W form. |
| `OpCountZeros` | `A=0 B=1 Bits=16 Arith=OpLeadingZeros` | 8 | Below 32: CLZ W, then SUB #(32-Bits). The input must be zero-extended. |
| `OpCountZeros` | `A=0 B=1 Bits=64 Arith=OpTrailingZeros` | 8 | RBIT then CLZ. |
| `OpCountZeros` | `A=0 B=1 Bits=8 Arith=OpTrailingZeros` | 20 | Below 32: RBIT, CLZ, CMP, MOV W16 and CSEL clamp the count to Bits. Clobbers X16 and writes NZCV. |
| `OpCountZeros` | `A=0 B=1 Bits=64 Arith=OpAdd` | rejected | Arith must be OpLeadingZeros or OpTrailingZeros. |

## Shifts

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpShift` | `A=0 B=1 Imm=3 Bits=64 Arith=OpShiftLeft` | 4 | A = B shifted by the constant Imm (a UBFM/SBFM/EXTR alias). Arith is OpShiftLeft, OpShiftRight, OpShiftRightArithmetic, OpRotateLeft or OpRotateRight. Bits is 8, 16, 32 or 64. Imm < Bits. |
| `OpShift` | `A=0 B=0 Imm=31 Bits=32 Arith=OpShiftRightArithmetic` | 4 | Widths below 64 use the W form, except arithmetic right shifts of 8 and 16 bits, which use a 64-bit SBFM. |
| `OpShift` | `A=0 B=1 Imm=5 Bits=32 Arith=OpRotateRight` | 4 | ROR (EXTR). Bits 32 uses the W form. Bits 8, 16 and 64 use the 64-bit X form. |
| `OpShift` | `A=0 B=1 Imm=64 Bits=64 Arith=OpShiftLeft` | rejected | Imm must be less than Bits. |
| `OpShift` | `A=0 B=1 Imm=1 Bits=64 Arith=OpAdd` | rejected | Arith must be a shift or rotate. |
| `OpShiftBy` | `A=0 B=1 C=2 Bits=64 Arith=OpShiftLeft` | 12 | A = B shifted by register C, saturating: a count of Bits or more gives 0. Emits CMP, LSLV and CSEL. Writes NZCV. A may alias B or C. |
| `OpShiftBy` | `A=0 B=1 C=2 Bits=32 Arith=OpShiftRight` | 16 | Below 64, a trailing UBFM narrows the result to Bits. |
| `OpShiftBy` | `A=0 B=1 C=2 Bits=64 Arith=OpShiftRightArithmetic` | 16 | ASR X17 makes the sign fill for counts of Bits or more. Clobbers X17. |
| `OpShiftBy` | `A=0 B=1 C=2 Bits=32 Arith=OpShiftRightArithmetic` | 24 | Below 64, the value is first sign-extended into X16. Clobbers X16 and X17. |
| `OpShiftBy` | `A=0 B=1 C=2 Bits=64 Arith=OpRotateRight` | 4 | Rotates are taken modulo Bits. RORV. Flags are unchanged. |
| `OpShiftBy` | `A=0 B=1 C=2 Bits=64 Arith=OpRotateLeft` | 8 | NEG X17, then RORV. Clobbers X17. |
| `OpShiftBy` | `A=0 B=1 C=2 Bits=8 Arith=OpRotateLeft` | 24 | Below 32, the value is replicated into W16 (a MOV plus one ORR per doubling) and narrowed after the rotate. Clobbers X16 and X17. |
| `OpShiftBy` | `A=0 B=1 C=2 Bits=24 Arith=OpShiftLeft` | rejected | Bits must be 8, 16, 32 or 64. |

## Selects

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpSelect` | `A=0 B=1 C=2 D=3` | 8 | A = B != 0 ? C : D. Emits CMP B, #0 and CSEL NE. Writes NZCV. |
| `OpSelect` | `A=0 B=1 C=2 D=40` | rejected | All operands are GP. |
| `OpSelectFlags` | `A=0 C=2 D=3 Cond=Less` | 4 | A = condition ? C : D, from the live NZCV (CSEL). B is ignored. Cond is Equal through Above, Sign or NotSign. |
| `OpSelectFlags` | `A=0 C=2 D=3 Cond=Sign` | 4 | Sign and NotSign map to MI and PL. |
| `OpSelectFlags` | `A=0 C=2 D=3 Cond=Overflow` | 4 | Overflow and NotOverflow map to VS and VC. |
| `OpSelectFlags` | `A=0 C=2 D=3 Cond=Carry` | rejected | The Carry and Carry32 conditions are not accepted (condition < 12). |

## Memory

All plain memory forms use the native little-endian order. `BigEndian` is
rejected; `OpLoad8` and `OpStore8` ignore the byte order.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpLoad64` | `A=0 B=1` | 4 | A = [B] (LDR X). There is no displacement. |
| `OpLoad64` | `A=0 B=1 BigEndian` | rejected | Native order only. |
| `OpStore64` | `A=1 B=0` | 4 | [A] = B (STR X). |
| `OpLoad8` | `A=0 B=1` | 4 | A = zero-extended byte [B] (LDRB). |
| `OpStore8` | `A=1 B=0` | 4 | [A] = low byte of B (STRB). |
| `OpStore8` | `A=1 V=B:0` | rejected | GP registers only. |
| `OpLoadAddress` | `A=0 B=1 C=2 Imm=8` | 4 | A = [B + C*Imm] (LDR X, register offset). Imm is 1 or 8. Disp must be 0. |
| `OpLoadAddress` | `A=0 B=1 C=2 Imm=1` | 4 | An unscaled index. |
| `OpLoadAddress` | `A=0 B=1 C=2 Imm=4` | rejected | Only scales 1 and 8 exist for a 64-bit access. |
| `OpLoadAddress` | `A=0 B=1 C=2 Imm=8 Disp=8` | rejected | No displacement. |
| `OpStoreAddress` | `A=1 B=2 C=0 Imm=8` | 4 | [A + B*Imm] = C. Imm is 1 or 8. Disp must be 0. |
| `OpStoreAddress` | `A=1 B=2 C=0 Imm=2` | rejected | Only scales 1 and 8. |
| `OpStoreAddress` | `A=1 B=2 C=0 Imm=8 Disp=8` | rejected | No displacement form. |
| `OpLoadOffset` | `A=0 B=1 Disp=32760` | 4 | A = [B + Disp]. Disp is a multiple of 8 in 0..32760 (scaled LDR), or a signed value in -256..255 (LDUR). |
| `OpLoadOffset` | `A=0 B=1 Disp=0xffffffffffffff00` | 4 | Disp = -256, the most negative LDUR offset. |
| `OpLoadOffset` | `A=0 B=1 Disp=255` | 4 | An unaligned offset within -256..255 uses LDUR. |
| `OpLoadOffset` | `A=0 B=1 Disp=32768` | rejected | Beyond the scaled range. |
| `OpLoadOffset` | `A=0 B=1 Disp=260` | rejected | Not a multiple of 8, and outside -256..255. |
| `OpStoreOffset` | `A=1 B=0 Disp=8` | 4 | [A + Disp] = B. Same ranges as `OpLoadOffset`. |
| `OpStoreOffset` | `A=1 B=0 Disp=0xfffffffffffffefc` | rejected | Disp = -260 is below -256. |

## Narrow memory

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpLoad16` | `A=0 B=1` | 4 | A = zero-extended halfword [B] (LDRH). There is no displacement. The address must be naturally aligned. |
| `OpLoad16` | `A=0 B=1 BigEndian` | rejected | Native little-endian order only. |
| `OpStore16` | `A=1 B=0` | 4 | [A] = low 16 bits of B (STRH). |
| `OpLoad32` | `A=0 B=1` | 4 | A = zero-extended word [B] (LDR W). |
| `OpLoad32` | `V=A:0 B=1` | rejected | GP registers only. |
| `OpStore32` | `A=1 B=0` | 4 | [A] = low 32 bits of B (STR W). |

## Calls, frames and slots

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpSyscall` | — | 4 | SVC #0. `FlagEffect` treats it as writing NZCV. |
| `OpCallC` | — | 4 | BLR X16. The caller loads the target into X16. |
| `OpCallGo` | — | 4 | BLR X16, the same encoding as `OpCallC`. |
| `OpSaveLink` | `Slot=0` | 4 | STR X30, [SP]. On the platform stack, LR always goes to 0(SP) and Slot is ignored. |
| `OpRestoreLink` | `Slot=0` | 4 | LDR X30, [SP]. |
| `OpSpill` | `A=0 Slot=0` | 4 | STR A, [SP, #8*(Slot+1)]. A is GP. |
| `OpSpill` | `A=0 Slot=4094` | 4 | The largest slot: the scaled offset field holds 4095 words. |
| `OpSpill` | `A=0 Slot=4095` | rejected | Slot+1 exceeds the 12-bit scaled offset. There is no expansion. |
| `OpSpill` | `V=A:0 Slot=0` | rejected | GP only. Use `OpFloatSpill` or `OpVectorSpill` for the vector bank. |
| `OpReload` | `A=0 Slot=3` | 4 | LDR A, [SP, #8*(Slot+1)]. Slot is 0..4094. |
| `OpReload` | `A=0 Slot=4095` | rejected | Same limit as `OpSpill`. |
| `OpSaveCFloat` | `A=8 Slot=2` | 4 | STR D(A), [SP, #8*(Slot+1)]: saves the low 64 bits of an AAPCS64 callee-saved V8..V15. A is the plain FP ordinal 0..31, not `backend.VectorRegister`. Slot is 0..4094. |
| `OpSaveCFloat` | `V=A:8 Slot=2` | rejected | A must be the plain ordinal (0..31). |
| `OpRestoreCFloat` | `A=8 Slot=2` | 4 | LDR D(A), [SP, #8*(Slot+1)]. |
| `OpRestoreCFloat` | `A=8 Slot=4095` | rejected | Slot+1 exceeds the 12-bit scaled offset. |
| `OpSaveCVector` | `V=A:8 Slot=2` | unsupported | Not implemented for arm64. C only preserves the low halves (see `OpSaveCFloat`). |
| `OpRestoreCVector` | `V=A:8 Slot=2` | unsupported | Not implemented for arm64. |
| `OpSaveCondition` | `A=0 Slot=0` | unsupported | Not implemented for arm64. NZCV is not saved across calls. |
| `OpRestoreCondition` | `A=0 Slot=0` | unsupported | Not implemented for arm64. |
| `OpLoadIncoming` | `A=0 Imm=0` | unsupported | Not implemented for arm64. Incoming stack arguments are not read through this opcode. |
| `OpFrameEnter` | `Imm=0` | 12 | Imm is the frame size, 0..1 MiB, and must be 16-byte aligned (see `FrameBytes`). Up to 240: STR X30, [SP, #-Imm]!, then STUR X29, [SP, #-8] and SUB X29, SP, #8. |
| `OpFrameEnter` | `Imm=240` | 12 | The largest single pre-indexed form. |
| `OpFrameEnter` | `Imm=256` | 24 | Above 240: the frame is built in X17, using one SUB per 4080 bytes, before SP moves. Size is `AdjustSize(Imm)+20`. Clobbers X17. |
| `OpFrameEnter` | `Imm=1048576` | 1052 | The 1 MiB maximum (258 SUB steps). |
| `OpFrameEnter` | `Imm=1048577` | rejected | Above the 1 MiB frame bound. |
| `OpFrameEnter` | `Imm=40` | rejected | Not 16-byte aligned. |
| `OpFrameEnter` | `Imm=64 CallArea=16` | rejected | `CallAreaBytes` must be 0: arm64 has no separate outgoing-area form. |
| `OpFrameLeave` | `Imm=0` | 8 | Up to 240: LDUR X29, [SP, #-8], then LDR X30, [SP], #Imm. |
| `OpFrameLeave` | `Imm=256` | 12 | Above 240: LDUR X29, LDR X30, then one ADD SP per 4080 bytes. Size is `AdjustSize(Imm)+8`. |
| `OpFrameLeave` | `Imm=1048576` | 1040 | The 1 MiB maximum. |
| `OpFrameLeave` | `Imm=1048577` | rejected | Above the 1 MiB frame bound. |
| `OpFrameLeave` | `Imm=40` | rejected | Not 16-byte aligned. |
| `OpReturn` | — | 4 | RET (X30). |

## Compares and branches

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpCompareImmediate` | `A=0 Imm=4095` | 4 | CMP A, #Imm (SUBS XZR). This is a 64-bit compare. Bits is 0 or 64. Cmp may be Equality, Signed or Unsigned, because all three read the same NZCV. |
| `OpCompareImmediate` | `A=0 SImm=-4095 Cmp=Signed` | 4 | A negative immediate uses CMN. The immediate is in -4095..4095. |
| `OpCompareImmediate` | `A=0 Imm=4096` | rejected | Outside -4095..4095 (no shifted form). Use `OpCompareRegister`. |
| `OpCompareImmediate` | `A=0 Imm=1 Bits=32` | rejected | Only 64-bit compares. Operands must be normalized to 64 bits first. |
| `OpCompareRegister` | `A=0 B=1 Cmp=Unsigned` | 4 | CMP A, B (SUBS XZR, 64-bit). Bits is 0 or 64. |
| `OpCompareRegister` | `A=0 B=1 Bits=16` | rejected | Only 64-bit compares. |
| `OpCompareSet` | `A=0 B=1 C=2 Bits=64 Cond=Less` | 8 | A = 1 when the Bits-wide integers in B and C satisfy Cond, else 0 (zero-extended W): CMP Xb, Xc (Wb, Wc below 64 bits), then CSET Wa, cond. Equality and unsigned conditions compare zero-extended narrow operands at 32 bits. A may alias B or C. Writes NZCV. |
| `OpCompareSet` | `A=3 B=4 C=5 Bits=16 Cond=Below` | 8 | CMP W4, W5; CSET W3, LO. |
| `OpCompareSet` | `A=0 B=1 C=2 Bits=8 Cond=Greater` | 12 | Signed 8 and 16 bits: SXTB (SXTH) Wa, Wb, then CMP Wa, Wc, SXTB (SXTH), then CSET. No register other than A is written. |
| `OpCompareSet` | `A=2 B=1 C=2 Bits=16 Cond=Less` | 12 | When A is C, C is extended into A and compared with B, and the condition is swapped (GT). |
| `OpCompareSet` | `A=0 B=31 C=2 Bits=8 Cond=Less` | rejected | The signed narrow form takes X0–X30: register 31 is SP in an extended-register compare. |
| `OpCompareSet` | `A=0 B=1 C=2 Bits=12 Cond=Less` | rejected | Bits must be 8, 16, 32 or 64. |
| `OpCompareSet` | `A=0 B=1 C=2 Bits=64 Cond=Sign` | rejected | Cond must be Equal through Above. |
| `OpReverseBytes` | `A=0 B=1 Bits=64` | 4 | A = B with its bytes reversed: REV Xa, Xb. |
| `OpReverseBytes` | `A=0 B=1 Bits=32` | 4 | REV Wa, Wb. |
| `OpReverseBytes` | `A=3 B=3 Bits=16` | 4 | REV16 Wa, Wb swaps the bytes of each halfword, so a zero-extended value stays zero-extended. |
| `OpReverseBytes` | `A=0 B=1 Bits=8` | rejected | Bits must be 16, 32 or 64. |
| `OpJump` | `Imm=134217724` | 4 | B to the absolute PC in Imm. The target is 4-byte aligned and within ±128 MiB. `Long` and `Far` are ignored. |
| `OpJump` | `Imm=134217728` | rejected | Outside the B range. |
| `OpJump` | `Imm=6` | rejected | The target must be 4-byte aligned. |
| `OpJumpIf` | `Imm=1048572 Cond=Less` | 4 | B.cond within ±1 MiB. Cond is Equal through Above, Sign or NotSign. |
| `OpJumpIf` | `Imm=64 Cond=Overflow` | 4 | B.VS (NotOverflow: B.VC), the V flag of the last ADDS or SUBS. |
| `OpJumpIf` | `Imm=1048576 Cond=Less` | rejected | Outside the B.cond range. Layout must set `Long`. |
| `OpJumpIf` | `Imm=1048576 Cond=NotSign Long` | 8 | Long: the inverted B.cond skips a B (±128 MiB from the second word). Flags are preserved. |
| `OpJumpIf` | `Imm=8 Cond=Equal Far` | 4 | `Far` is ignored. Only `Long` selects the expanded form. |
| `OpJumpIf` | `Imm=8 Cond=Carry` | rejected | Carry and Carry32 are not encodable conditions here. |
| `OpBranchZero` | `A=0 Bits=64 Imm=1048572 Cond=Equal` | 4 | CBZ (Equal) or CBNZ (NotEqual) within ±1 MiB. Bits is 8, 16, 32 or 64. Below 64 the W form is used, so the value must be zero-extended. |
| `OpBranchZero` | `A=0 Bits=32 Imm=1048576 Cond=NotEqual Long` | 8 | Long: an inverted CBZ/CBNZ skips a B. |
| `OpBranchZero` | `A=0 Imm=8 Cond=Equal` | rejected | Bits must be 8, 16, 32 or 64. |
| `OpBranchZero` | `A=0 Bits=64 Imm=8 Cond=Less` | rejected | Cond must be Equal or NotEqual. |
| `OpBranchBit` | `A=0 Bits=64 Disp=63 Imm=32764 Cond=NotEqual` | 4 | TBNZ (NotEqual) or TBZ (Equal) on bit Disp, within ±32 KiB. Disp < Bits. Bits is 8, 16, 32 or 64. |
| `OpBranchBit` | `A=0 Bits=64 Disp=3 Imm=32768 Cond=Equal` | rejected | Outside the TBZ range. Layout must set `Long`. |
| `OpBranchBit` | `A=0 Bits=64 Disp=3 Imm=32768 Cond=Equal Long` | 8 | Long: an inverted TBZ/TBNZ skips a B. |
| `OpBranchBit` | `A=0 Bits=32 Disp=32 Imm=8 Cond=Equal` | rejected | The bit must be below Bits. |
| `OpLoop` | `Imm=0` | unsupported | arm64 has no dedicated loop counter. Loops use compares and branches. |
| `OpJumpTable` | `A=0 B=1 Table=64,128,192` | 40 | Bounded switch: MOVZ Xb, #n (n = len(Table)-1, MOVK for counts above 0xffff); CMP Xa, Xb; CSEL Xb, Xa, Xb, LO clamps the index, so entry n is the default; ADR X16, table; LDRSW X17, [X16, Xb, LSL 2]; ADD X16, X16, X17; BR X16; then one 32-bit table-relative entry per target. Writes NZCV, X16 and X17. A and B are distinct GP registers other than X16, X17 and register 31; A is preserved. Targets are 4-byte aligned. |
| `OpJumpTable` | `A=0 B=1 Table=64,130` | rejected | Targets must be 4-byte aligned. |
| `OpJumpTable` | `A=0 B=0 Table=64,128` | rejected | A and B must differ. |
| `OpJumpTable` | `A=16 B=1 Table=64,128` | rejected | X16 and X17 are the sequence's temporaries. |
| `OpJumpTable` | `A=0 B=1 Table=64` | rejected | The table needs a case and the default. |
| `OpSetCount` | `A=0` | unsupported | arm64 has no dedicated loop counter. |

## Floating point

FP operands A, B, C and D are each either a vector register (`V=A:n`, using
lane 0 as S or D) or a GP register holding the bit pattern. Each GP input adds
an FMOV into V16, V17 or V18. A GP destination adds an FMOV out of V16. Bits is
32 (single) or 64 (double).

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpFloatBinary` | `V=A:0,B:1,C:2 Bits=64 Arith=OpFAdd` | 4 | A = B op C. Arith is OpFAdd, OpFSub, OpFMul or OpFDiv. |
| `OpFloatBinary` | `A=0 B=1 C=2 Bits=32 Arith=OpFMul` | 16 | All GP: two FMOVs in, the operation, and one FMOV out. |
| `OpFloatBinary` | `V=A:0,B:1 C=2 Bits=64 Arith=OpFDiv` | 8 | One GP input. |
| `OpFloatBinary` | `V=A:0,B:1,C:2 Bits=64 Arith=OpFMA` | rejected | Fused multiply-add is `OpFloatFMA`. |
| `OpFloatBinary` | `V=A:0,B:1,C:2 Bits=16 Arith=OpFAdd` | rejected | Bits must be 32 or 64. |
| `OpFloatFMA` | `V=A:0,B:1,C:2,D:3 Bits=64` | 4 | A = B*C + D (FMADD, fused). |
| `OpFloatFMA` | `A=0 B=1 C=2 D=3 Bits=32` | 20 | All GP: three FMOVs in, FMADD, and one FMOV out. |
| `OpFloatFMA` | `V=A:0,B:1,C:2 D=40 Bits=64` | rejected | Each operand must be GP 0..31 or V0..V31. |
| `OpFloatUnary` | `V=A:0,B:1 Bits=64 Arith=OpFSqrt` | 4 | FSQRT. |
| `OpFloatUnary` | `V=A:0,B:1 Bits=32 Arith=OpFConvert` | 4 | FCVT. Bits is the destination width, and the source is the other width (64 to 32 here). |
| `OpFloatUnary` | `A=0 B=1 Bits=64 Arith=OpFConvert` | 12 | GP in and out: the source is moved as 32-bit, the result as 64-bit. |
| `OpFloatUnary` | `V=A:0,B:1 Bits=64 Arith=OpFAdd` | rejected | Arith must be OpFSqrt or OpFConvert. |
| `OpFloatBits` | `V=A:0,B:1 Bits=64 Arith=OpAnd` | 4 | OpAnd clears the sign bit (FABS). OpXor flips it (FNEG). |
| `OpFloatBits` | `A=0 B=1 Bits=32 Arith=OpXor` | 12 | GP in and out. |
| `OpFloatBits` | `V=A:0,B:1 Bits=64 Arith=OpOr` | rejected | Arith must be OpAnd or OpXor. |
| `OpIntToFloat` | `V=A:0 B=1 Bits=64 Imm=64 Arith=OpFFromSigned` | 4 | SCVTF Dd, Xn; OpFFromUnsigned is UCVTF. A is an FP register and B a GP register. Bits is the float width and Imm the integer width, 32 (a W source) or 64. Rounds in the FPCR mode. |
| `OpIntToFloat` | `V=A:0 B=1 Bits=32 Imm=32 Arith=OpFFromUnsigned` | 4 | UCVTF Sd, Wn. |
| `OpIntToFloat` | `A=0 B=1 Bits=64 Imm=64 Arith=OpFFromSigned` | rejected | A must be an FP register. |
| `OpIntToFloat` | `V=A:0 B=1 Bits=64 Imm=16 Arith=OpFFromSigned` | rejected | Imm must be 32 or 64. |
| `OpFloatToInt` | `A=0 V=B:1 Bits=64 Disp=32 Arith=OpFToSigned` | 4 | FCVTZS Xd, Sn; OpFToUnsigned is FCVTZU. A is GP and B an FP register. Bits is the integer width, 32 (zero-extended W) or 64, and Disp the float width. FCVTZ* truncate, saturate and give 0 for NaN, so ConversionSaturate (Imm=0) and ConversionNative (Imm=1) are the same instruction. |
| `OpFloatToInt` | `A=0 V=B:1 Bits=32 Disp=64 Arith=OpFToUnsigned Imm=1` | 4 | FCVTZU Wd, Dn. |
| `OpFloatToInt` | `A=0 B=1 Bits=64 Disp=64 Arith=OpFToSigned` | rejected | B must be an FP register. |
| `OpFloatToInt` | `A=0 V=B:1 Bits=64 Disp=64 Arith=OpFToSigned Imm=2` | rejected | Imm must be a defined `op.ConversionSemantics`. |
| `OpFloatCompare` | `A=0 V=B:1,C:2 Bits=64 Imm=0` | 8 | A = 1 when the Bits-wide floats in FP registers B and C satisfy the `op.FloatCondition` in Imm, else 0 (zero-extended W). FCMP Dn, Dm (Sn, Sm for Bits=32), then CSET Wd, cond. Writes NZCV. After FCMP, less is N, equal ZC, greater C and unordered CV, so the conditions in `op.FloatCondition` order are EQ, NE, MI, PL, LS, HI, GT, LE, GE, LT, VC and VS. |
| `OpFloatCompare` | `A=3 V=B:4,C:5 Bits=32 Imm=3` | 8 | FCMP S4, S5; CSET W3, PL (UnorderedGreaterEqual). |
| `OpFloatCompare` | `A=31 V=B:1,C:2 Bits=64 Imm=0` | 4 | FCMP only: WZR discards the boolean result. NZCV is unchanged by omitting CSET/CSINC. |
| `OpFloatCompare` | `A=0 V=B:1,C:2 Bits=64 Imm=12` | 12 | OrderedNotEqual: CSET Wd, MI, then CSINC Wd, Wd, WZR, LE, which gives 1 when greater. |
| `OpFloatCompare` | `A=0 V=B:1,C:2 Bits=32 Imm=13` | 12 | UnorderedEqual: CSET Wd, EQ, then CSINC Wd, Wd, WZR, VC, which gives 1 when unordered. |
| `OpFloatCompare` | `A=0 V=B:1,C:2 Bits=16 Imm=0` | rejected | Bits must be 32 or 64. |
| `OpFloatCompare` | `A=0 V=B:1,C:2 Bits=64 Imm=14` | rejected | Imm must be a defined `op.FloatCondition`. |
| `OpFloatCompare` | `A=0 B=1 V=C:2 Bits=64 Imm=0` | rejected | B and C must be FP registers. |
| `OpFloatMove` | `V=A:0,B:1 Bits=64` | 4 | FMOV between vector registers. At least one operand must be a vector register. Bits is 32 or 64. |
| `OpFloatMove` | `V=A:2 B=3 Bits=64` | 4 | GP to FP (FMOV Dd, Xn). |
| `OpFloatMove` | `A=3 V=B:2 Bits=32` | 4 | FP to GP (FMOV Wd, Sn). |
| `OpFloatMove` | `V=A:1,B:1 Bits=64` | 0 | A move to itself emits nothing. |
| `OpFloatMove` | `A=0 B=1 Bits=64` | rejected | GP to GP is `OpMove`. |
| `OpFloatSpill` | `V=A:0 Slot=0` | 4 | STR D(A), [SP, #8*(Slot+1)]. A is a vector register. Bits is 0, 32 or 64, and 64 bits are stored either way. |
| `OpFloatSpill` | `V=A:0 Slot=4095` | 12 | Offsets above 32760 are formed in X16: ADD X16, SP, #0, then ADD X16, #imm, LSL #12 steps. Clobbers X16. |
| `OpFloatSpill` | `V=A:0 Slot=20970494` | 48 | The largest slot: ten ADD LSL #12 steps. |
| `OpFloatSpill` | `V=A:0 Slot=20970495` | rejected | Beyond the expansion limit. |
| `OpFloatSpill` | `A=0 Slot=0` | rejected | A must be a vector register. |
| `OpFloatSpill` | `V=A:0 Slot=0 Bits=16` | rejected | Bits must be 0, 32 or 64. |
| `OpFloatReload` | `V=A:0 Slot=1 Bits=64` | 4 | LDR D(A), [SP, #8*(Slot+1)]. The same expansion and limits as `OpFloatSpill`. |
| `OpFloatReload` | `V=A:0 Slot=0 Bits=16` | rejected | Bits must be 0, 32 or 64. |

## Vectors

`OpVector` operations act on whole 128-bit NEON registers. Bits is the lane
width (8, 16, 32 or 64) where it matters. `VectorType` is not consulted by the
encoder.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpVector` | `V=A:0 B=1 Arith=OpVLoad` | 4 | LDR Q(A), [B]. B is GP. Any alignment. |
| `OpVector` | `V=A:0 B=1 C=2 Arith=OpVLoad Indexed` | rejected | No indexed or displaced vector memory forms. |
| `OpVector` | `V=A:0 B=1 Disp=16 Arith=OpVLoad` | rejected | Disp must be 0. |
| `OpVector` | `A=1 V=B:0 Arith=OpVStore` | 4 | STR Q(B), [A]. A is GP. |
| `OpVector` | `V=A:0 Arith=OpVZero` | 4 | MOVI A.16B, #0. |
| `OpVector` | `V=A:0 B=1 Bits=32 Arith=OpVSplat` | 4 | DUP A.<lanes>, B (GP). |
| `OpVector` | `V=A:0 B=1 Arith=OpVSplat` | rejected | Lane-wise operations need Bits of 8, 16, 32 or 64. |
| `OpVector` | `A=0 V=B:1 Bits=64 Imm=1 Arith=OpVExtract` | 4 | UMOV A (GP), B lane Imm. Imm < 128/Bits. |
| `OpVector` | `A=0 V=B:1 Bits=32 Imm=4 Arith=OpVExtract` | rejected | The lane index is out of range. |
| `OpVector` | `V=A:0,B:0 C=1 Bits=16 Imm=7 Arith=OpVInsert` | 4 | A = B with lane Imm replaced by GP C (INS). Imm < 128/Bits. |
| `OpVector` | `V=A:0,B:1 C=2 Bits=8 Imm=15 Arith=OpVInsertZero` | 8 | When A differs from B, a MOV A.16B, B.16B comes first. OpVInsertZero is encoded like OpVInsert. |
| `OpVector` | `V=A:0,B:1 Bits=32 Imm=3 Arith=OpShiftLeft` | 4 | A constant shift of 32-bit lanes (SHL/USHR). Arith is OpShiftLeft, OpShiftRight, OpRotateLeft or OpRotateRight. Bits must be 32. |
| `OpVector` | `V=A:0,B:1 Bits=32 Imm=32 Arith=OpShiftRight` | 4 | A shift count of 32 or more gives zero (MOVI). |
| `OpVector` | `V=A:0,B:0 Bits=32 Imm=0 Arith=OpShiftLeft` | 0 | A zero shift in place emits nothing. A zero shift into another register is one MOV. |
| `OpVector` | `V=A:0,B:1 Bits=32 Imm=8 Arith=OpRotateLeft` | 8 | A rotate into a distinct destination: a shift, then SRI/SLI. Counts are taken modulo 32. |
| `OpVector` | `V=A:0,B:0 Bits=32 Imm=8 Arith=OpRotateRight` | 12 | A rotate in place: the complementary half goes through V30, then ORR. Clobbers V30. |
| `OpVector` | `V=A:0,B:1 Bits=16 Imm=3 Arith=OpShiftLeft` | rejected | Vector constant shifts need Bits=32. |
| `OpVector` | `V=A:0,B:1 Bits=32 Imm=3 Arith=OpShiftRightArithmetic` | rejected | There is no arithmetic right shift form. |
| `OpVector` | `V=A:0,B:1,C:2 Bits=32 Arith=OpShiftLeftBy` | rejected | No variable vector shifts or rotates. |
| `OpVector` | `V=A:0,B:1,C:2 Bits=8 Imm=0 Arith=OpVCompare` | 4 | CMEQ 16B. Only Bits=8 with Imm = BranchEqual (0) is accepted. |
| `OpVector` | `V=A:0,B:1,C:2 Bits=32 Imm=0 Arith=OpVCompare` | rejected | Only byte-lane equality. |
| `OpVector` | `A=0 V=B:1 Arith=OpVMaskBits` | 40 | A (GP) = a 16-bit mask of the byte lanes' high bits. Builds the bit weights in X16 and works in V30/V31. Clobbers X16, V30 and V31. |
| `OpVector` | `V=A:0,B:1,C:2 Bits=32 Arith=OpAdd` | 4 | Lane-wise ADD, SUB or MUL. OpAnd, OpOr and OpXor ignore the lane width. |
| `OpVector` | `V=A:0,B:1,C:2 Bits=8 Arith=OpXor` | 4 | EOR 16B. |
| `OpVector` | `V=A:0,B:1,C:2 Bits=64 Arith=OpMul` | rejected | NEON has no 64-bit lane multiply. |
| `OpVector` | `V=A:0,B:1,C:2 Bits=64 Imm=0 Arith=OpVCarrylessMul` | 4 | PMULL A.1Q, B.1D, C.1D. Imm bit 0 selects the doubleword of B and bit 1 that of C. Encoded whatever the CPU profile, so it requires FEAT_PMULL (`pmull`). |
| `OpVector` | `V=A:0,B:1,C:2 Bits=64 Imm=3 Arith=OpVCarrylessMul` | 4 | PMULL2 A.1Q, B.2D, C.2D: both high doublewords. |
| `OpVector` | `V=A:0,B:1,C:2 Bits=64 Imm=1 Arith=OpVCarrylessMul` | 8 | A mixed selector first swaps the halves of one input (EXT V31.16B, #8), then uses PMULL. Clobbers V31. |
| `OpVector` | `V=A:0,B:1,C:2 Bits=32 Imm=0 Arith=OpVCarrylessMul` | rejected | Bits must be 64, and Imm must be 0–3. |
| `OpVector` | `A=0 B=1 C=2 Bits=32 Arith=OpAdd` | rejected | Lane operands must be vector registers. |
| `OpVector` | `V=A:3,B:4,C:5 Bits=32 Imm=0x03020100 Arith=OpVShuffle` | 4 | Shuffle: lane i of A is lane idx_i of B‖C, with idx_i in byte i of Imm, then of Disp from lane 8 (Bits=8). Index bytes past the last lane must be zero, and idx_i < 2·128/Bits. Every index set encodes. B's identity is MOV A.16B, B.16B. |
| `OpVector` | `V=A:4,B:4,C:5 Bits=32 Imm=0x03020100 Arith=OpVShuffle` | 0 | An identity in place emits nothing. |
| `OpVector` | `V=A:3,B:4,C:4 Bits=32 Imm=0x07060504 Arith=OpVShuffle` | 4 | When B is C, indices are taken modulo the lane count. |
| `OpVector` | `V=A:3,B:4,C:5 Bits=8 Imm=0x0505050505050505 Disp=0x0505050505050505 Arith=OpVShuffle` | 4 | A broadcast is DUP A.16B, B.B[5]. |
| `OpVector` | `V=A:3,B:4,C:5 Bits=16 Imm=0x0b0b0b0b0b0b0b0b Arith=OpVShuffle` | 4 | DUP A.8H, C.H[3]. |
| `OpVector` | `V=A:3,B:4,C:5 Bits=64 Imm=0x0303 Arith=OpVShuffle` | 4 | DUP A.2D, C.D[1]. |
| `OpVector` | `V=A:3,B:4,C:5 Bits=8 Imm=0x1303120211011000 Disp=0x1707160615051404 Arith=OpVShuffle` | 4 | ZIP1 A, B, C. ZIP1/ZIP2, UZP1/UZP2 and TRN1/TRN2 match with sources B,C, C,B, B,B or C,C. |
| `OpVector` | `V=A:3,B:4,C:5 Bits=32 Imm=0x01050004 Arith=OpVShuffle` | 4 | ZIP1 A, C, B. |
| `OpVector` | `V=A:3,B:4,C:5 Bits=64 Imm=0x0301 Arith=OpVShuffle` | 4 | ZIP2 A.2D. |
| `OpVector` | `V=A:3,B:4,C:5 Bits=32 Imm=0x06040200 Arith=OpVShuffle` | 4 | UZP1 A.4S. |
| `OpVector` | `V=A:3,B:4,C:5 Bits=16 Imm=0x0f0d0b0907050301 Arith=OpVShuffle` | 4 | UZP2 A.8H. |
| `OpVector` | `V=A:3,B:4,C:5 Bits=16 Imm=0x0e060c040a020800 Arith=OpVShuffle` | 4 | TRN1 A.8H. |
| `OpVector` | `V=A:3,B:4,C:5 Bits=32 Imm=0x07030501 Arith=OpVShuffle` | 4 | TRN2 A.4S. |
| `OpVector` | `V=A:3,B:4,C:5 Bits=8 Imm=0x0a09080706050403 Disp=0x1211100f0e0d0c0b Arith=OpVShuffle` | 4 | A rotation of B‖C is EXT A, B, C, #3 (bytes). |
| `OpVector` | `V=A:3,B:4,C:5 Bits=64 Imm=0x0003 Arith=OpVShuffle` | 4 | EXT A, C, B, #8: a rotation of C‖B. |
| `OpVector` | `V=A:3,B:4,C:5 Bits=8 Imm=0x08090a0b0c0d0e0f Disp=0x0001020304050607 Arith=OpVShuffle` | 44 | Any other single-source set builds the byte index table in V31 from X16 (MOVZ/MOVK and INS for each doubleword), then TBL A.16B, {B.16B}, V31.16B. Clobbers X16 and V31. |
| `OpVector` | `V=A:3,B:4,C:5 Bits=32 Imm=0x00010001 Arith=OpVShuffle` | 24 | Equal doublewords of the table use one DUP V31.2D, X16. |
| `OpVector` | `V=A:3,B:4,C:5 Bits=8 Imm=0x0f18011a030a1103 Disp=0x1c130500170e0712 Arith=OpVShuffle` | 56 | Two sources: TBL from B (C's table entries are out of range and give zero), MOVI V30.16B, #16, EOR V31 with V30, then TBX from C. Clobbers X16, V30 and V31. |
| `OpVector` | `V=A:5,B:4,C:5 Bits=8 Imm=0x0f18011a030a1103 Disp=0x1c130500170e0712 Arith=OpVShuffle` | 56 | When A is C, the table is built for C first and TBX fills B's lanes. |
| `OpVector` | `V=A:3,B:4,C:5 Bits=32 Imm=0x08020100 Arith=OpVShuffle` | rejected | An index is not below 2·128/Bits. |
| `OpVector` | `V=A:3,B:4,C:5 Bits=32 Imm=0x0100000003020100 Arith=OpVShuffle` | rejected | A byte past the last lane is nonzero. |
| `OpVector` | `V=A:3,B:4,C:5 Bits=64 Imm=0x0100 Disp=1 Arith=OpVShuffle` | rejected | Disp must be zero unless Bits=8. |
| `OpVector` | `V=A:3,B:30,C:5 Bits=32 Imm=0x03020100 Arith=OpVShuffle` | rejected | V30 and V31 are reserved for the table. |
| `OpVector` | `V=A:3,B:4 Bits=16 Imm=0 Arith=OpVExtendUnsigned` | 4 | UXTL A.8H, B.8B (USHLL #0): zero-extend the low half of B to Bits-wide lanes. Bits is the result width: 16, 32 or 64. |
| `OpVector` | `V=A:3,B:4 Bits=16 Imm=1 Arith=OpVExtendUnsigned` | 4 | Imm=1 widens the high half: UXTL2 A.8H, B.16B. |
| `OpVector` | `V=A:3,B:3 Bits=32 Imm=0 Arith=OpVExtendUnsigned` | 4 | UXTL A.4S, B.4H. A may be B. |
| `OpVector` | `V=A:3,B:4 Bits=64 Imm=1 Arith=OpVExtendUnsigned` | 4 | UXTL2 A.2D, B.4S. |
| `OpVector` | `V=A:3,B:4 Bits=16 Imm=0 Arith=OpVExtendSigned` | 4 | SXTL A.8H, B.8B (SSHLL #0). |
| `OpVector` | `V=A:3,B:4 Bits=32 Imm=1 Arith=OpVExtendSigned` | 4 | SXTL2 A.4S, B.8H. |
| `OpVector` | `V=A:3,B:4 Bits=64 Imm=0 Arith=OpVExtendSigned` | 4 | SXTL A.2D, B.2S. |
| `OpVector` | `V=A:3,B:4 Bits=8 Imm=0 Arith=OpVExtendSigned` | rejected | Bits is the result lane width: 16, 32 or 64. |
| `OpVector` | `V=A:3,B:4 Bits=32 Imm=2 Arith=OpVExtendUnsigned` | rejected | Imm selects the low (0) or high (1) half. |
| `OpVectorMove` | `V=A:0,B:1` | 4 | MOV A.16B, B.16B (the whole register). |
| `OpVectorMove` | `V=A:2,B:2` | 0 | A move to itself emits nothing. |
| `OpVectorMove` | `A=0 V=B:1` | rejected | Both operands must be vector registers. |
| `OpVectorSpill` | `V=A:0 Slot=1` | 4 | STR Q(A), [SP, #8*(Slot+1)]. The offset must be 16-byte aligned, so Slot is odd here, and Slot+1 is at most 8190. |
| `OpVectorSpill` | `V=A:0 Slot=0` | rejected | The offset 8 is not 16-byte aligned. |
| `OpVectorSpill` | `V=A:0 Slot=8191` | rejected | Beyond the scaled 12-bit offset (65520 bytes). |
| `OpVectorReload` | `V=A:3 Slot=8189` | 4 | LDR Q(A), [SP, #8*(Slot+1)]. The largest slot. |
| `OpVectorReload` | `A=3 Slot=1` | rejected | A must be a vector register. |

## Literals

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpLiteralLoad` | `V=A:0 Bits=64 Disp=8` | 4 | LDR D(A), literal. Disp is the absolute pool PC. The distance is 4-byte aligned and within ±1 MiB. Bits is 32 (S), 64 (D) or 128 (Q). A is a vector register. |
| `OpLiteralLoad` | `V=A:0 Bits=128 Disp=1048576` | rejected | Outside the literal range without `Long`. |
| `OpLiteralLoad` | `V=A:0 Bits=128 Disp=1048576 Long` | 20 | Long: ADR X16, then MOVZ/MOVK W17 and ADD X16, X16, W17, SXTW, then LDR [X16]. The distance must fit in a signed 32-bit value. Clobbers X16 and X17. |
| `OpLiteralLoad` | `V=A:0 Bits=64 Disp=6` | rejected | The distance must be a multiple of 4. |
| `OpLiteralLoad` | `A=0 Bits=64 Disp=8` | rejected | A must be a vector register. |
| `OpLiteralLoad` | `V=A:0 Bits=16 Disp=8` | rejected | Bits must be 32, 64 or 128. |
| `OpLiteralData` | `Data=0000803f` | 4 | Raw pool bytes, emitted as little-endian words. The length must be a multiple of 4. |
| `OpLiteralData` | `Data=000102030405060708090a0b0c0d0e0f` | 16 | A 128-bit constant. |
| `OpLiteralData` | `Data=001122` | rejected | The length is not a multiple of 4. |

## Barriers

`OpBarrier` reads and writes no registers or flags. `Barrier` names the
guarantee; `Policy.Barrier` reports whether the target's instruction rests on
the instruction set or also on host configuration.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpBarrier` | `Barrier=Speculation` | 8 | `DSB SY; ISB`. |
| `OpBarrier` | `Barrier=Memory` | 4 | `DMB ISH`. |
| `OpBarrier` | `Barrier=Instruction` | 4 | `ISB`. |
| `OpBarrier` | — | rejected | Barrier must name a guarantee. |

## Stack areas

`OpSlotAddress` writes to GP register A the address of spill slot Slot, as
`OpSpill` addresses it: 8*(Slot+1)(SP), above the Go homes when there are any,
or 8*Slot(X8) without a platform stack. It writes no flags.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpSlotAddress` | `A=0 Slot=0` | 4 | ADD X0, SP, #8. |
| `OpSlotAddress` | `A=3 Slot=510` | 4 | The largest single ADD: #4088. |
| `OpSlotAddress` | `A=3 Slot=511` | 4 | ADD X3, SP, #1, LSL #12: a low part of zero needs no second ADD. |
| `OpSlotAddress` | `A=3 Slot=600` | 8 | ADD X3, SP, #1, LSL #12, then ADD X3, X3, #712. |
| `OpSlotAddress` | `A=30 Slot=2097150` | 8 | The largest offset, 16777208 bytes. |
| `OpSlotAddress` | `A=0 Slot=2097151` | rejected | The offset exceeds the two-ADD range. |
| `OpSlotAddress` | `A=31 Slot=0` | rejected | Register 31 names SP in ADD; A must be X0-X30. |
| `OpSlotAddress` | `V=A:0 Slot=0` | rejected | A is a GP register. |

## Float passing

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpX87Result` | `V=A:3 Bits=64 Slot=0` | unsupported | 386 only: `machine.Visit` returns `ErrUnsupportedOperation`. FP results already travel in an FP register. |

## Prefetch

`OpPrefetch` hints an access at the address in GP register A, by the
`op.PrefetchKind` in Imm. It never faults, writes no register or flag, and has
no architectural effect.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpPrefetch` | `A=0 Imm=0` | 4 | `PRFM PLDL1KEEP, [X0]` for PrefetchRead. |
| `OpPrefetch` | `A=9 Imm=1` | 4 | `PRFM PSTL1KEEP, [X9]` for PrefetchWrite. |
| `OpPrefetch` | `A=31 Imm=0` | 4 | A base of 31 is SP, as for the other memory forms. |
| `OpPrefetch` | `A=0 Imm=2` | rejected | Imm must be an `op.PrefetchKind`. |
| `OpPrefetch` | `V=A:1 Imm=0` | rejected | A must be a GP register. |


## Tail calls

`OpTailCall` ends a function with a C tail call. The arguments are in place
and the target is in the call-target register, as for `OpCallC`. It leaves
a frame of Imm bytes, as `OpFrameLeave` does (none when Imm is zero), and
jumps to the target, so the callee returns to this function's caller.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpTailCall` | `Imm=0` | 4 | `BR X16`. LR stays the caller's return address. |
| `OpTailCall` | `Imm=32` | 12 | `LDUR X29, [SP, #-8]; LDR X30, [SP], #32; BR X16`: the frame restores FP and LR first. |
| `OpTailCall` | `Imm=4096` | 20 | A frame above 240 bytes reloads LR, then adds to SP in steps. |
| `OpTailCall` | `Imm=24` | rejected | Frames are 16-byte multiples. |

## Atomics

`OpAtomic` performs the sequentially consistent atomic `Arith` (`OpAtomicLoad`
… `OpAtomicCompareSwap`) at width `Bits` on the naturally aligned address in
B. A receives the old value, zero-extended; C is the operand (the value of a
store, the expected value of a compare-and-swap) and D the replacement. `Imm`
is the `op.MemoryOrder`, of which only `MemoryOrderSequential` (0) exists. A
store reads no A. B, C and D are GP registers 0..30 other than X16 and X17,
and A, where it is written, differs from every input.

These rows are the ARMv8.0 forms the exported assembler emits. Loads are
`LDAR{B,H}` and stores `STLR{B,H}`. A read-modify-write is an exclusive loop:
`LDAXR{B,H} X16, [B]`, the operation into X17, `STLXR{B,H} WA, X17, [B]`,
`CBNZ WA` back, then `MOV A, X16`; the status register is A, so the loop
writes only X16, X17 and A and has no memory access between the exclusive
pair. A compare-and-swap compares X16 with C and branches out with `B.NE`
before the store-exclusive; it is the only form that writes NZCV. The
FEAT_LSE forms (`LDADDAL`, `MVN X16, C` then `LDCLRAL`, `LDSETAL`, `LDEORAL`,
`SWPAL`, and `MOV A, C; CASAL A, D, [B]`, with B/H variants) are implemented
but have no exported switch yet, so these rows do not cover them.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpAtomic` | `A=3 B=4 Bits=8 Arith=OpAtomicLoad` | 4 | `LDARB W3, [X4]`; zero-extends. |
| `OpAtomic` | `A=3 B=4 Bits=16 Arith=OpAtomicLoad` | 4 | `LDARH`. |
| `OpAtomic` | `A=3 B=4 Bits=32 Arith=OpAtomicLoad` | 4 | `LDAR W3`. |
| `OpAtomic` | `A=3 B=4 Bits=64 Arith=OpAtomicLoad` | 4 | `LDAR X3`. |
| `OpAtomic` | `B=4 C=5 Bits=8 Arith=OpAtomicStore` | 4 | `STLRB W5, [X4]`. |
| `OpAtomic` | `B=4 C=5 Bits=16 Arith=OpAtomicStore` | 4 | `STLRH`. |
| `OpAtomic` | `B=4 C=5 Bits=32 Arith=OpAtomicStore` | 4 | `STLR W5`. |
| `OpAtomic` | `A=4 B=4 C=5 Bits=64 Arith=OpAtomicStore` | 4 | `STLR X5`. A is not read, so it may name an input. |
| `OpAtomic` | `A=3 B=4 C=5 Bits=8 Arith=OpAtomicAdd` | 20 | `LDAXRB W16; ADD W17, W16, W5; STLXRB W3, W17; CBNZ W3; MOV X3, X16`. |
| `OpAtomic` | `A=3 B=4 C=5 Bits=16 Arith=OpAtomicAdd` | 20 | Halfword exclusives. |
| `OpAtomic` | `A=3 B=4 C=5 Bits=32 Arith=OpAtomicAdd` | 20 | Word exclusives. |
| `OpAtomic` | `A=3 B=4 C=5 Bits=64 Arith=OpAtomicAdd` | 20 | `ADD X17, X16, X5`. |
| `OpAtomic` | `A=3 B=4 C=5 Bits=8 Arith=OpAtomicAnd` | 20 | `AND` in the loop. |
| `OpAtomic` | `A=3 B=4 C=5 Bits=64 Arith=OpAtomicAnd` | 20 | |
| `OpAtomic` | `A=3 B=4 C=5 Bits=16 Arith=OpAtomicOr` | 20 | `ORR` in the loop. |
| `OpAtomic` | `A=3 B=4 C=5 Bits=32 Arith=OpAtomicOr` | 20 | |
| `OpAtomic` | `A=3 B=4 C=5 Bits=8 Arith=OpAtomicXor` | 20 | `EOR` in the loop. |
| `OpAtomic` | `A=3 B=4 C=5 Bits=64 Arith=OpAtomicXor` | 20 | |
| `OpAtomic` | `A=3 B=4 C=5 Bits=8 Arith=OpAtomicSwap` | 16 | `LDAXRB W16; STLXRB W3, W5; CBNZ W3; MOV X3, X16`. |
| `OpAtomic` | `A=3 B=4 C=5 Bits=16 Arith=OpAtomicSwap` | 16 | |
| `OpAtomic` | `A=3 B=4 C=5 Bits=32 Arith=OpAtomicSwap` | 16 | |
| `OpAtomic` | `A=3 B=4 C=5 Bits=64 Arith=OpAtomicSwap` | 16 | |
| `OpAtomic` | `A=3 B=4 C=5 D=6 Bits=8 Arith=OpAtomicCompareSwap` | 24 | `LDAXRB W16; CMP W16, W5; B.NE done; STLXRB W3, W6; CBNZ W3; done: MOV X3, X16`. Writes NZCV. |
| `OpAtomic` | `A=3 B=4 C=5 D=6 Bits=16 Arith=OpAtomicCompareSwap` | 24 | |
| `OpAtomic` | `A=3 B=4 C=5 D=6 Bits=32 Arith=OpAtomicCompareSwap` | 24 | `CMP W16, W5`. |
| `OpAtomic` | `A=3 B=4 C=5 D=6 Bits=64 Arith=OpAtomicCompareSwap` | 24 | `CMP X16, X5`. |
| `OpAtomic` | `A=3 B=4 C=5 Bits=12 Arith=OpAtomicAdd` | rejected | Bits must be 8, 16, 32 or 64. |
| `OpAtomic` | `A=3 B=4 C=5 Bits=32 Imm=1 Arith=OpAtomicAdd` | rejected | Imm must be a defined `MemoryOrder`. |
| `OpAtomic` | `A=3 B=4 C=5 Bits=32 Arith=OpAdd` | rejected | Arith must be an atomic operation. |
| `OpAtomic` | `A=3 V=B:1 Bits=64 Arith=OpAtomicLoad` | rejected | The address must be a GP register. |
| `OpAtomic` | `A=5 B=4 C=5 Bits=32 Arith=OpAtomicAdd` | rejected | A must differ from the inputs. |
| `OpAtomic` | `A=6 B=4 C=5 D=6 Bits=32 Arith=OpAtomicCompareSwap` | rejected | A must differ from the replacement. |
| `OpAtomic` | `A=3 B=16 Bits=64 Arith=OpAtomicLoad` | rejected | X16 and X17 are the loop temporaries. |
| `OpAtomic` | `A=3 B=4 C=17 Bits=64 Arith=OpAtomicSwap` | rejected | |
| `OpAtomic` | `B=4 C=31 Bits=64 Arith=OpAtomicStore` | rejected | Register 31 is not an operand. |
