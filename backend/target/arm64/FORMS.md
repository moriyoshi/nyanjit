# arm64 instruction forms

This table is the arm64 (AArch64) contract for allocated `machine.Instruction`
values. Every example row is assembled by `backend/target/forms_test.go`.

The table assumes the following:

- The byte assembler is built with `NewByteAssembler(abi.StackPlatform, 0)`:
  the platform stack is in use and there are no Go argument homes. The link
  register is stored at 0(SP). Spill slot n is at 8*(n+1)(SP).
- The baseline CPU is ARMv8.0-A with FP and Advanced SIMD (NEON). No optional
  extension is used. Every instruction word is 4 bytes, so all sizes are
  multiples of 4.
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
| `OpJump` | `Imm=134217724` | 4 | B to the absolute PC in Imm. The target is 4-byte aligned and within ±128 MiB. `Long` and `Far` are ignored. |
| `OpJump` | `Imm=134217728` | rejected | Outside the B range. |
| `OpJump` | `Imm=6` | rejected | The target must be 4-byte aligned. |
| `OpJumpIf` | `Imm=1048572 Cond=Less` | 4 | B.cond within ±1 MiB. Cond is Equal through Above, Sign or NotSign. |
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
| `OpVector` | `V=A:0,B:1,C:2 Bits=64 Arith=OpVCarrylessMul` | rejected | PMULL is not used on the baseline CPU. |
| `OpVector` | `A=0 B=1 C=2 Bits=32 Arith=OpAdd` | rejected | Lane operands must be vector registers. |
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
