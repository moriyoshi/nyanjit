# ppc64 instruction forms

This table describes the `machine.Instruction` forms that the ppc64 backend
accepts. It covers little-endian POWER (ppc64le) with the C (ELFv2) profile
from `abi.NewPPC64Profile("ppc64le", abi.C)`, `abi.StackPlatform`, the POWER8
baseline and an unmanaged emitter (`NewEmitter`). POWER9 and later only change
a few expansions; the rows below show POWER8.

ppc64 does not implement `machine.Assembler`. `Emitter.Expand` turns each
machine instruction into concrete four-byte POWER instructions, and
`ByteAssembler.Visit` encodes them as little-endian words. `ByteAssembler.Data`
emits `OpLiteralData` directly. A form is rejected when either step fails.
Each example is expanded alone at PC 0, so a branch's `Imm` is an absolute
target PC and its displacement equals `Imm`. `Emitter.Size` is the authority
for forms that are not listed; it returns 0 for a form it cannot expand.

General registers r0..r31 are `backend.Register` 0..31 and are named by
`Policy.RegisterName` (`R3`). The floating-point operands of the float opcodes
are FPR/VSR n, written as `backend.VectorRegister(n)` (`V=A:n`). Most float
opcodes also accept a GP register that holds the IEEE bit pattern. Register
numbers above 31 in a GP role are rejected by the encoder. Several registers
are reserved:

- r0 is zeroed after calls and syscalls. As a base (the RA field), r0 reads as
  literal zero.
- r1 is the stack pointer and r2 is the TOC pointer.
- r11 and r12 are expansion temporaries, and r12 is also the call target.
- r13 is the thread pointer.
- r29 is the workspace and the spill base without a platform stack.
- f0..f2 (vs0..vs2) are temporaries for GP to FP transfers, and f0 also
  holds the result of a float to integer conversion.

The C allocator uses r3..r10.

## Constants and moves

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpConst` | `A=3 Imm=42` | 4 | Signed 16-bit value: `li`. |
| `OpConst` | `A=3 SImm=-32768` | 4 | Signed 16-bit value: `li`. |
| `OpConst` | `A=3 Imm=0x12345678` | 8 | Signed 32-bit value: `lis; ori`. |
| `OpConst` | `A=3 SImm=-2147483648` | 8 | Signed 32-bit value: `lis; ori`. |
| `OpConst` | `A=3 Imm=0x80000000` | 20 | Any other value: `lis; ori; sldi 32; oris; ori`. |
| `OpConst` | `A=3 Imm=0x123456789` | 20 | Any other value: five instructions. |
| `OpConst` | `Imm=1 V=A:1` | rejected | A must be a GP register. |
| `OpConst` | `A=3 Imm=1 BigEndian` | rejected | Only memory opcodes accept a non-native byte order. |
| `OpSymbol` | `A=3 Sym=foo` | 20 | Always the fixed five-instruction form; the first `lis` carries a `RelocPPC64LittleEndian` relocation. |
| `OpSymbol` | `A=3` | rejected | A symbol name is required. |
| `OpMove` | `A=3 B=4` | 4 | `mr` (`or A,B,B`). |
| `OpMove` | `A=3 B=3` | 0 | A move to itself is elided. |
| `OpMove` | `A=3 V=B:1` | rejected | GP registers only; use `OpFloatMove` for FPRs. |

## Integer arithmetic

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpBinary` | `A=3 B=4 C=5 Bits=64 Arith=OpAdd` | 4 | `addc.`: records CR0 and sets XER.CA. A, B and C may alias. |
| `OpBinary` | `A=3 B=4 C=5 Bits=32 Arith=OpAdd` | 4 | `add.` on POWER8 (POWER9 uses `addc.` for 32 bits too). Bits only selects the add form. |
| `OpBinary` | `A=3 B=4 C=5 Bits=64 Arith=OpSub` | 4 | `subfc.` computes B-C. |
| `OpBinary` | `A=3 B=4 C=5 Bits=64 Arith=OpSub PreserveFlags` | 4 | PreserveFlags selects non-record, non-carry `add`/`subf`/`mulld`/`and`/`or`/`xor`. |
| `OpBinary` | `A=3 B=4 C=5 Bits=64 Arith=OpMul` | 4 | `mulld.` gives the low 64 bits. |
| `OpBinary` | `A=3 B=3 C=4 Bits=64 Arith=OpXor` | 4 | Accepts OpAdd, OpSub, OpMul, OpAnd, OpOr and OpXor. |
| `OpBinary` | `A=3 B=4 C=5 Bits=64 Arith=OpShiftLeft` | rejected | Other arithmetic is not accepted. |
| `OpBinary` | `A=3 B=4 C=5 Bits=64 Arith=OpAdd V=A:1` | rejected | GP registers only. |
| `OpBinaryImmediate` | `A=3 B=4 Imm=100 Arith=OpAdd` | 4 | `addic.`: signed 16-bit Imm (-32768..32767); sets CR0 and CA. Bits is not checked. |
| `OpBinaryImmediate` | `A=3 B=4 Imm=32768 Arith=OpSub` | 4 | OpSub negates Imm, so it accepts -32767..32768. |
| `OpBinaryImmediate` | `A=3 B=4 Imm=32768 Arith=OpAdd` | rejected | Imm is outside the signed 16-bit field. |
| `OpBinaryImmediate` | `A=3 B=4 Imm=1 Arith=OpAdd PreserveFlags` | 4 | `addi` when B is not r0. With B=r0, `addic` without record is used (still sets CA). |
| `OpBinaryImmediate` | `A=3 B=4 Imm=0xffff Arith=OpAnd` | 4 | `andi.`: unsigned 16-bit Imm. It always records CR0, even with PreserveFlags. |
| `OpBinaryImmediate` | `A=3 B=4 Imm=0xffff Arith=OpOr` | 4 | `ori`/`xori`: unsigned 16-bit Imm and no flags. |
| `OpBinaryImmediate` | `A=3 B=4 Imm=0x10000 Arith=OpXor` | rejected | Imm is outside the unsigned 16-bit field. |
| `OpBinaryImmediate` | `A=3 B=4 Imm=2 Arith=OpMul` | rejected | Only OpAdd, OpSub, OpAnd, OpOr and OpXor. |
| `OpBinaryCarry` | — | unsupported | Not implemented; use `OpWordCarry`. |
| `OpInc` | `A=3 B=3` | 4 | `addic. A,B,1` sets CR0 and CA. A and B may differ. |
| `OpInc` | `A=3 B=3 PreserveFlags` | 4 | `addi` when B is not r0. |
| `OpDec` | `A=3 B=3` | 4 | `addic. A,B,-1`. |
| `OpDec` | `A=3 B=0 PreserveFlags` | 4 | B=r0 cannot use `addi` (RA=0 is zero), so `addic` without record is used. |
| `OpMulAdd` | `A=3 B=4 C=5 D=6` | 8 | A = B + C*D. POWER8: `mulld r11,C,D; add A,B,r11` (POWER9: one `maddld`). |
| `OpMulAdd` | `A=3 B=4 C=5 V=D:6` | rejected | GP registers only. |
| `OpMulWide` | — | unsupported | Not implemented; use `OpMulHigh`. |
| `OpMulHigh` | `A=3 B=4 C=5 Bits=64` | 4 | `mulhdu`: high 64 bits of unsigned B*C. Bits=0 means 64. No flags. |
| `OpMulHigh` | `A=3 B=4 C=5 Bits=32` | 8 | `mulhwu; clrldi`: the high word, zero-extended. |
| `OpMulHigh` | `A=3 B=4 C=5 Bits=16` | 12 | 8 and 16 bits: `mulld; srdi; clrldi`. Inputs must be zero-extended. |
| `OpMulHigh` | `A=3 B=4 C=5 Bits=12` | rejected | Bits must be 8, 16, 32 or 64. |
| `OpMulHighSigned` | `A=3 B=4 C=5 Bits=64` | 4 | `mulhd`: high 64 bits of signed B*C. No flags. |
| `OpMulHighSigned` | `A=3 B=4 C=5 Bits=32` | 8 | `mulhw; clrldi`: the high word, zero-extended. |
| `OpMulHighSigned` | `A=3 B=4 C=5 Bits=16` | 16 | 8 and 16 bits: `extsb`/`extsh` of B and C into r11 and r12, `mulld`, then `rldicl` by 64-Bits keeping the low Bits. Inputs must be zero-extended. |
| `OpMulHighSigned` | `A=3 B=4 C=5 Bits=12` | rejected | Bits must be 8, 16, 32 or 64. |
| `OpDivide` | `A=3 B=4 C=5 Bits=64 Arith=OpDiv Imm=2` | 4 | DivisionUnchecked: bare `divdu` (OpDivSigned: `divd`). No flags. Bits must be 8, 16, 32 or 64; Imm is the `op.DivisionSemantics`. A, B and C may alias. |
| `OpDivide` | `A=3 B=4 C=5 Bits=64 Arith=OpDiv Imm=0` | 12 | DivisionZero: `cmpdi C,0; divdu r11,B,C; isel A,0,r11,eq`. The guard compare writes CR0; temporaries are r11 and r12. |
| `OpDivide` | `A=3 B=4 C=5 Bits=64 Arith=OpDiv Imm=1` | 16 | DivisionAllOnes: `li r12,-1` feeds the isel. |
| `OpDivide` | `A=3 B=4 C=5 Bits=32 Arith=OpDiv Imm=1` | 20 | Unsigned narrow operands are zero-extended, so 64-bit `divdu` serves; only the all-ones result is masked back to Bits. |
| `OpDivide` | `A=3 B=4 C=5 Bits=64 Arith=OpDivSigned Imm=0` | 24 | A second guard `cmpdi C,-1; neg r12,B; isel` gives -B, so MIN / -1 = MIN. |
| `OpDivide` | `A=3 B=4 C=5 Bits=32 Arith=OpDivSigned Imm=2` | 8 | Unchecked signed 32 bits: `divw; clrldi`. |
| `OpDivide` | `A=3 B=4 C=5 Bits=32 Arith=OpDivSigned Imm=0` | 24 | Checked signed narrow: `cmpdi C,0; extsw r11,B; extsw r12,C; divd; isel; clrldi`. Narrow MIN / -1 cannot overflow 64 bits, so only the zero divisor is guarded. |
| `OpDivide` | `A=3 B=4 C=5 Bits=8 Arith=OpDivSigned Imm=2` | 16 | 8 and 16 bits sign-extend with `extsb`/`extsh` and mask the quotient. |
| `OpDivide` | `A=3 B=4 C=5 Bits=64 Arith=OpRem Imm=0` | 12 | POWER8: `divdu r11,B,C; mulld r11,r11,C; subf A,r11,B`, which gives x % 0 = x unguarded, for every semantics. POWER9: `cmpdi C,0; modud r11,B,C; isel A,B,r11,eq` (unchecked: one `modud`). |
| `OpDivide` | `A=3 B=4 C=5 Bits=64 Arith=OpRemSigned Imm=1` | 20 | `cmpdi C,-1`, the `divd` formula, then `isel` gives MIN % -1 = 0. POWER9 uses `modsd` with both guards (20 bytes). |
| `OpDivide` | `A=3 B=4 C=5 Bits=64 Arith=OpRemSigned Imm=2` | 12 | Unchecked: the formula only (POWER9: one `modsd`). |
| `OpDivide` | `A=3 B=4 C=5 Bits=16 Arith=OpRemSigned Imm=0` | 24 | `extsh; extsh; divd; mulld; subf; clrldi` (POWER9: `cmpdi; extsh; extsh; modsd; isel; clrldi`). |
| `OpDivide` | `A=3 B=4 C=5 Bits=12 Arith=OpDiv` | rejected | Bits must be 8, 16, 32 or 64. |
| `OpDivide` | `A=3 B=4 C=5 Bits=64 Arith=OpMul` | rejected | Arith must be OpDiv, OpDivSigned, OpRem or OpRemSigned. |
| `OpDivide` | `A=3 B=4 C=5 Bits=64 Arith=OpDiv Imm=3` | rejected | Imm is not a `op.DivisionSemantics`. |
| `OpDivide` | `A=3 C=5 Bits=64 Arith=OpDiv V=B:4` | rejected | GP registers only. |
| `OpDivide` | `A=3 B=4 C=12 Bits=64 Arith=OpDiv` | rejected | r11 and r12 are the expansion temporaries. |
| `OpCarryBit` | `A=3 B=4 C=5 Arith=OpAdd` | 12 | A = carry of B+C (0 or 1): `addc r12,B,C; li r11,0; addze A,r11`. Bits must be 0 or 64. |
| `OpCarryBit` | `A=3 B=4 C=5 Arith=OpSub` | 16 | A = borrow of B-C: `subfc; li; subfze; neg`. |
| `OpCarryBit` | `A=3 Arith=OpAdd Carry=3` | 8 | CarryFlags reads the incoming XER.CA, with no compare. |
| `OpCarryBit` | `A=3 B=4 C=5 Bits=32 Arith=OpAdd` | rejected | 64 bits only. |
| `OpWordCarry` | `A=3 B=4 C=5 Arith=OpAdd Carry=3` | 4 | A = B+C+CA: `adde.` (OpSub: `subfe.`). CarryFlags uses the current XER.CA. |
| `OpWordCarry` | `A=3 B=4 C=5 D=6 E=7 Arith=OpAdd Carry=2` | 8 | CarryOperands: carry from D+E (or the borrow of D-E) via `addc r11,D,E`. |
| `OpWordCarry` | `A=3 B=4 C=5 D=6 Arith=OpAdd Carry=1` | 8 | CarryBitValue: D holds 0/1; `addic r11,D,-1` sets CA. |
| `OpWordCarry` | `A=3 B=4 C=5 D=6 Arith=OpSub Carry=0` | 12 | CarryValue: uses bit 0 of D (`clrldi`, then `subfic r11,r11,0` for OpSub). |
| `OpWordCarry` | `A=3 B=4 C=5 Bits=32 Arith=OpAdd Carry=3` | rejected | Bits must be 0 or 64. |
| `OpWordCarry` | `A=3 B=4 C=5 Arith=OpMul Carry=3` | rejected | Arith must be OpAdd or OpSub. |
| `OpWordCarry` | `A=3 B=4 C=5 Arith=OpAdd Carry=4` | rejected | Unknown CarryMode. |
| `OpAndNot` | — | unsupported | Not implemented. |
| `OpCountZeros` | — | unsupported | Not implemented. |

## Extension and shifts

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpNarrow` | `A=3 B=4 Imm=32` | 4 | Imm is the width (8, 16 or 32): `clrldi A,B,64-Imm`. |
| `OpNarrow` | `A=3 B=4 Imm=64` | 4 | Width 64 is a move. |
| `OpNarrow` | `A=3 B=3 Imm=64` | 0 | Width 64 into the same register is elided. |
| `OpNarrow` | `A=3 B=4 Imm=12` | rejected | The width must be 8, 16, 32 or 64. |
| `OpNarrow` | `A=3 B=4 Imm=32 Flags=3` | 4 | A FlagRequest selects the record form, `clrldi.`, which sets CR0 from the result. |
| `OpSignExtend` | `A=3 B=4 Imm=8` | 4 | `extsb`/`extsh`/`extsw` for Imm 8/16/32. |
| `OpSignExtend` | `A=3 B=4 Imm=64` | 4 | Width 64 is a move (0 bytes when A=B). |
| `OpSignExtend` | `A=3 B=4 Imm=24` | rejected | The width must be 8, 16, 32 or 64. |
| `OpSignExtend` | `A=3 B=4 Imm=8 Flags=3` | 4 | A FlagRequest selects the record form, `extsb.`. |
| `OpSignExtend` | `A=3 B=3 Imm=64 Flags=3` | rejected | An in-place 64-bit extension emits nothing to record flags on. |
| `OpShift` | `A=3 B=4 Imm=3 Bits=64 Arith=OpShiftLeft` | 4 | Immediate count Imm < Bits (Bits=0 means 64): `sldi`. |
| `OpShift` | `A=3 B=4 Imm=5 Bits=32 Arith=OpShiftRight` | 4 | `srdi` on the full register; the input must be zero-extended. |
| `OpShift` | `A=3 B=4 Imm=3 Bits=64 Arith=OpShiftRightArithmetic` | 4 | `sradi` sets XER.CA. |
| `OpShift` | `A=3 B=4 Imm=3 Bits=32 Arith=OpShiftRightArithmetic` | 8 | Narrow widths sign-extend into r11 first. |
| `OpShift` | `A=3 B=4 Imm=0 Bits=16 Arith=OpShiftRightArithmetic` | 4 | A zero count on a narrow arithmetic shift is a sign extension. |
| `OpShift` | `A=3 B=3 Imm=0 Bits=64 Arith=OpShiftLeft` | 0 | A zero count into the same register is elided. |
| `OpShift` | `A=3 B=4 Imm=7 Bits=64 Arith=OpRotateLeft` | 4 | `rotldi`. |
| `OpShift` | `A=3 B=4 Imm=7 Bits=32 Arith=OpRotateRight` | 4 | `rotlwi` by 32-Imm. |
| `OpShift` | `A=3 B=4 Imm=3 Bits=8 Arith=OpRotateRight` | 16 | 8/16-bit rotates combine r11/r12 and then zero-extend. |
| `OpShift` | `A=3 B=4 Imm=32 Bits=32 Arith=OpShiftLeft` | rejected | The count must be below the width. |
| `OpShift` | `A=3 B=4 Imm=1 Bits=12 Arith=OpShiftLeft` | rejected | Bits must be 8, 16, 32 or 64. |
| `OpShift` | `A=3 B=4 Imm=1 Bits=64 Arith=OpAdd` | rejected | Shift and rotate opcodes only. |
| `OpShiftBy` | `A=3 B=4 C=5 Bits=64 Arith=OpShiftLeft` | 16 | Register count C, clamped at the width through r11 (`li; cmpldi; isel; sld`). Writes CR0. C must not be r0 (the `isel` RA field). A may alias B or C. |
| `OpShiftBy` | `A=3 B=4 C=5 Bits=32 Arith=OpShiftRight` | 20 | Widths below 64 zero-extend the result (`clrldi`). |
| `OpShiftBy` | `A=3 B=4 C=5 Bits=16 Arith=OpShiftRightArithmetic` | 24 | 8/16-bit arithmetic shifts sign-extend into r12 first. |
| `OpShiftBy` | `A=3 B=4 C=5 Bits=64 Arith=OpRotateLeft` | 4 | `rotld` (count modulo 64). |
| `OpShiftBy` | `A=3 B=4 C=5 Bits=64 Arith=OpRotateRight` | 8 | `neg r11,C; rotld`. |
| `OpShiftBy` | `A=3 B=4 C=5 Bits=32 Arith=OpRotateLeft` | 8 | `rotlw; clrldi`. |
| `OpShiftBy` | `A=3 B=4 C=5 Bits=8 Arith=OpRotateLeft` | 20 | 8/16 bits replicate the value into r12 with `rlwimi`. |
| `OpShiftBy` | `A=3 B=4 C=5 Bits=12 Arith=OpShiftLeft` | rejected | Bits must be 8, 16, 32 or 64 (0 means 64). |
| `OpShiftBy` | `A=3 B=4 C=5 Bits=64 Arith=OpAdd` | rejected | Shift and rotate opcodes only. |
| `OpScaled` | `A=3 B=4 C=5 Imm=1` | 4 | A = B + C*Imm + Disp. With Imm=1 and Disp=0 it is one `add`. |
| `OpScaled` | `A=3 B=4 C=5 Imm=8` | 8 | A power-of-two scale uses `sldi r11`. |
| `OpScaled` | `A=3 B=4 C=5 Imm=3` | 12 | Other scales load the constant into r11 and use `mulld`. |
| `OpScaled` | `A=3 B=4 C=5 Imm=1 Disp=16` | 12 | A nonzero Disp adds the constant through r12. |
| `OpScaled` | `A=3 B=4 C=5 Imm=8 Disp=0x123456789` | 32 | The constant length follows `OpConst`. |

## Selection and comparison

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpSelect` | `A=3 B=4 C=5 D=6` | 8 | A = B != 0 ? C : D (`cmpldi B,0; isel`). Writes CR0. D must not be r0 (the `isel` RA field reads as zero). |
| `OpSelect` | `A=3 B=4 C=5 V=D:6` | rejected | GP registers only. |
| `OpSelectFlags` | `A=3 C=4 D=5 Cond=Below` | 4 | A = condition ? C : D, from CR0 (`isel`). B is ignored. |
| `OpSelectFlags` | `A=3 C=4 D=5 Cond=AboveEqual` | 4 | A false condition swaps C and D around the CR bit. |
| `OpSelectFlags` | `A=3 C=0 D=5 Cond=Below` | rejected | `isel` reads r0 as zero when the bit is set. |
| `OpSelectFlags` | `A=3 C=4 D=5 Cond=Carry` | unsupported | Carry conditions would first move XER into CR. |
| `OpCompareImmediate` | `A=3 Imm=7` | 4 | CompareEquality and CompareSigned use `cmpdi`, with a signed 16-bit Imm. Bits=0 means 64. |
| `OpCompareImmediate` | `A=3 SImm=-5 Bits=32 Cmp=Signed` | 4 | `cmpwi`. |
| `OpCompareImmediate` | `A=3 Imm=65535 Cmp=Unsigned` | 4 | `cmpldi`, with an unsigned 16-bit Imm. |
| `OpCompareImmediate` | `A=3 Imm=0x12345 Cmp=Signed` | 12 | A wider Imm is loaded into r12 (`OpConst` length) and compared with `cmpd`/`cmpld`. |
| `OpCompareImmediate` | `A=3 SImm=-1 Cmp=Unsigned` | 8 | The r12 constant here is a single `li`. |
| `OpCompareImmediate` | `A=3 Imm=0x123456789 Cmp=Unsigned` | 24 | Five-instruction constant plus `cmpld`. |
| `OpCompareImmediate` | `A=3 Imm=1 Bits=16` | rejected | Bits must be 0, 32 or 64. |
| `OpCompareRegister` | `A=3 B=4 Cmp=Signed` | 4 | `cmpd A,B` (CompareUnsigned: `cmpld`), writing CR0. |
| `OpCompareRegister` | `A=3 B=4 Bits=32 Cmp=Unsigned` | 4 | `cmplw`. |
| `OpCompareRegister` | `A=3 B=4 Bits=8` | rejected | Bits must be 0, 32 or 64. |
| `OpCompareSet` | `A=3 B=4 C=5 Bits=64 Cond=Less` | 12 | A = 1 when the Bits-wide integers in B and C satisfy Cond, else 0: `cmpd B,C` (`cmpld` for equality and unsigned conditions) into CR0, `mfcr A` and `rlwinm A,A,bit+1,31,31` of the CR0 bit. POWER8-compatible. A may alias B or C. Writes CR0. |
| `OpCompareSet` | `A=3 B=4 C=5 Bits=32 Cond=GreaterEqual` | 16 | `cmpw`; a condition that holds when the bit is clear (GreaterEqual, LessEqual, AboveEqual, BelowEqual, NotEqual) adds `xori A,A,1`. |
| `OpCompareSet` | `A=3 B=4 C=5 Bits=16 Cond=Equal` | 12 | Equality and unsigned conditions compare zero-extended narrow operands with `cmplw`. |
| `OpCompareSet` | `A=3 B=4 C=5 Bits=8 Cond=Greater` | 20 | Signed 8 and 16 bits: `extsb` (`extsh`) of B and C into r11 and r12, then `cmpd`. |
| `OpCompareSet` | `A=3 B=4 C=5 Bits=16 Cond=LessEqual` | 24 | |
| `OpCompareSet` | `A=3 B=4 C=5 Bits=12 Cond=Less` | rejected | Bits must be 8, 16, 32 or 64. |
| `OpCompareSet` | `A=3 B=4 C=5 Bits=64 Cond=Sign` | rejected | Cond must be Equal through Above. |
| `OpCompareSet` | `A=3 B=4 V=C:5 Bits=64 Cond=Less` | rejected | GP registers only. |
| `OpReverseBytes` | `A=3 B=4 Bits=32` | 12 | A = B with its bytes reversed, on POWER8 and later: `rlwinm A,B,8,0,31` then two `rlwimi A,B,24` inserting the first and third bytes. |
| `OpReverseBytes` | `A=3 B=3 Bits=32` | 16 | When A is B the word is reversed into r11 and moved. |
| `OpReverseBytes` | `A=3 B=4 Bits=16` | 8 | `rlwinm A,B,24,24,31; rlwimi A,B,8,16,23`. |
| `OpReverseBytes` | `A=3 B=4 Bits=64` | 36 | `srdi r12,B,32`, the low word reversed into r11 and the high word into A, `sldi r11,r11,32` and `or A,A,r11`. Writes r11 and r12. |
| `OpReverseBytes` | `A=3 B=11 Bits=64` | rejected | r11 and r12 are the expansion's temporaries. |
| `OpReverseBytes` | `A=3 B=4 Bits=8` | rejected | Bits must be 16, 32 or 64. |

## Memory

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpLoad64` | `A=3 B=4` | 4 | `ld A,0(B)`. B must not be r0 (base r0 reads as zero). |
| `OpLoad64` | `A=3 B=4 BigEndian` | 4 | `ldbrx A,0,B`. |
| `OpStore64` | `A=4 B=3` | 4 | A is the address and B the value: `std B,0(A)`. |
| `OpStore64` | `A=4 B=3 BigEndian` | 4 | `stdbrx`. |
| `OpLoad8` | `A=3 B=4` | 4 | `lbz` zero-extends. |
| `OpLoad8` | `A=3 B=4 BigEndian` | 4 | Byte order has no effect on byte accesses. |
| `OpStore8` | `A=4 B=3` | 4 | `stb B,0(A)`. |
| `OpLoad16` | `A=3 B=4` | 4 | `lhz` zero-extends. |
| `OpLoad16` | `A=3 B=4 BigEndian` | rejected | Narrow accesses are native-order only. |
| `OpStore16` | `A=4 B=3` | 4 | `sth B,0(A)`. |
| `OpLoad32` | `A=3 B=4` | 4 | `lwz` zero-extends. |
| `OpStore32` | `A=4 B=3` | 4 | `stw B,0(A)`. |
| `OpStore32` | `A=4 B=3 BigEndian` | rejected | Narrow accesses are native-order only. |
| `OpLoadOffset` | `A=3 B=4 Disp=32760` | 4 | `ld A,Disp(B)` when Disp is a multiple of 4 in -32768..32764. |
| `OpLoadOffset` | `A=3 B=4 Disp=0xfffffffffffffff8` | 4 | Negative displacements (here -8) use the same D form. |
| `OpLoadOffset` | `A=3 B=4 Disp=2` | 8 | Other displacements go through r11 (r12 when B is r11) and `ldx`. |
| `OpLoadOffset` | `A=3 B=4 Disp=32768` | 12 | The displacement constant follows `OpConst` lengths. |
| `OpLoadOffset` | `A=3 B=4 Disp=0x123456789` | 24 | Five-instruction displacement plus `ldx`. |
| `OpLoadOffset` | `A=3 B=4 Disp=8 BigEndian` | 8 | Byte-reversed accesses are indexed only: constant plus `ldbrx`. |
| `OpStoreOffset` | `A=4 B=3 Disp=16` | 4 | A is the base and B the value: `std B,Disp(A)`. |
| `OpStoreOffset` | `A=4 B=3 Disp=0x10000` | 12 | The scratch is r11, or r12 when A or B is r11. |
| `OpStoreOffset` | `A=11 B=12 Disp=0x10000` | rejected | A large displacement needs a scratch distinct from both r11 and r12. |
| `OpLoadAddress` | `A=3 B=4 C=5 Imm=1` | 4 | `ldx A,B,C`. Imm (the scale) must be 1 and Disp 0. B must not be r0. |
| `OpLoadAddress` | `A=3 B=4 C=5 Imm=1 BigEndian` | 4 | `ldbrx`. |
| `OpLoadAddress` | `A=3 B=4 C=5 Imm=8` | rejected | Scaled indexes are not folded. |
| `OpLoadAddress` | `A=3 B=4 C=5 Imm=1 Disp=8` | rejected | Displacements are not folded. |
| `OpStoreAddress` | `A=4 B=5 C=3 Imm=1` | 4 | A is the base, B the index and C the value: `stdx C,A,B`. Imm must be 1 and Disp 0. |
| `OpStoreAddress` | `A=4 B=5 C=3 Imm=1 BigEndian` | 4 | `stdbrx`. |
| `OpStoreAddress` | `A=4 B=5 C=3` | rejected | Imm must be 1. |
| `OpStoreAddress` | `A=4 B=5 C=3 Imm=1 Disp=8` | rejected | Displacements are not folded. |
| `OpLoadIncoming` | `A=3 Imm=160` | 4 | Loads an incoming stack argument from Imm(r1), with the same displacement rules as `OpLoadOffset`. |
| `OpLoadIncoming` | `A=3 Imm=0x10000` | 12 | A large offset uses r11 and `ldx`. |

## Spill slots

Spill slot `Slot` is at 128 + 8*Slot from r1 (`Emitter.SpillOffset`).

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpSpill` | `A=3 Slot=0` | 4 | `std A,128(r1)`. Slot must be non-negative. |
| `OpSpill` | `A=3 Slot=4079` | 4 | The largest slot with a D-form offset (32760). |
| `OpSpill` | `A=3 Slot=4080` | 12 | Larger offsets use r11 (r12 when A is r11) and `stdx`. |
| `OpSpill` | `A=3 Slot=0xffffffffffffffff` | rejected | Negative slot (-1). |
| `OpReload` | `A=3 Slot=2` | 4 | `ld A,144(r1)`. |
| `OpReload` | `A=3 Slot=4080` | 12 | Constant offset plus `ldx`. |
| `OpFloatSpill` | `Slot=0 V=A:3` | 4 | `stfd fN,offset(r1)`. A must be an FPR (`VectorRegister`). |
| `OpFloatSpill` | `Slot=4080 V=A:3` | 16 | Offsets above 32767 compute the address into r11. |
| `OpFloatSpill` | `A=3 Slot=0` | rejected | A must be an FPR, not a GP register. |
| `OpFloatReload` | `Slot=1 V=A:3` | 4 | `lfd`. |
| `OpFloatReload` | `Slot=4080 V=A:3` | 16 | Large offsets go through r11. |
| `OpSaveLink` | `Slot=0` | 8 | `mflr r11; std r11,offset(r1)`. |
| `OpSaveLink` | `Slot=4080` | 16 | Large offsets use r12 as the address scratch. |
| `OpRestoreLink` | `Slot=0` | 8 | `ld r11; mtlr r11`. |
| `OpRestoreLink` | `Slot=4080` | 16 | Large offsets go through r11 and `ldx`. |
| `OpSaveCFloat` | `A=14 Slot=0` | 4 | `stfd`. A is the raw FPR number (0..31), not a `VectorRegister`. Requires StackPlatform. |
| `OpSaveCFloat` | `A=14 Slot=4080` | rejected | The offset must fit the 16-bit D field (32767). |
| `OpSaveCFloat` | `Slot=0 V=A:14` | rejected | A is a raw FPR number. |
| `OpRestoreCFloat` | `A=14 Slot=1` | 4 | `lfd`. |
| `OpSaveCVector` | `A=20 Slot=0` | 8 | `li r11,offset; stvx vA,r1,r11`. A is the raw VR number. |
| `OpSaveCVector` | `A=20 Slot=4080` | 12 | The offset constant follows `OpConst` lengths. |
| `OpSaveCVector` | `A=20 Slot=1` | rejected | The offset (128 + 8*Slot) must be 16-byte aligned, so Slot must be even. |
| `OpRestoreCVector` | `A=20 Slot=2` | 8 | `lvx`. |
| `OpSaveCondition` | `Slot=0` | 8 | `mfcr r11; std r11,offset(r1)`. |
| `OpSaveCondition` | `Slot=4080` | 16 | Large offsets use r12 and `stdx`. |
| `OpRestoreCondition` | `Slot=0` | 8 | `ld r11; mtcrf 0x38,r11` restores only CR2..CR4. |
| `OpRestoreCondition` | `Slot=4080` | 16 | Large offsets go through r11 and `ldx`. |

## Frames, calls and returns

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpFrameEnter` | `Imm=144` | 16 | Imm is the frame size: nonzero and a multiple of 16. Expands to `mflr r11; std r11,16(r1); stdu r1,-Imm(r1); std r2,24(r1)`. CallArea is ignored. Requires StackPlatform. |
| `OpFrameEnter` | `Imm=32768 CallArea=64` | 16 | The largest `stdu` frame. |
| `OpFrameEnter` | `Imm=32784` | 24 | Larger frames load -Imm into r11 and use `stdux`. |
| `OpFrameEnter` | `Imm=0x100000000` | 36 | -Imm takes the five-instruction constant form. |
| `OpFrameEnter` | `Imm=24` | rejected | Not 16-byte aligned. |
| `OpFrameEnter` | — | rejected | Imm must be nonzero. |
| `OpFrameLeave` | `Imm=144` | 16 | `ld r11,Imm+16(r1); ld r2,24(r1); mtlr r11; addi r1,r1,Imm`. Same Imm rules as `OpFrameEnter`. |
| `OpFrameLeave` | `Imm=32752` | 24 | The LR slot offset (Imm+16) exceeds the D field and goes through r11 and `ldx`. |
| `OpFrameLeave` | `Imm=32768` | 32 | The SP adjustment also needs an r11 constant and `add`. |
| `OpFrameLeave` | `Imm=8` | rejected | Not 16-byte aligned. |
| `OpReturn` | — | 4 | `blr`. |
| `OpCallC` | — | 20 | Calls the target in r12: `std r2,24(r1); mtctr r12; bctrl; ld r2,24(r1); xor r0,r0,r0`. Requires StackPlatform. |
| `OpCallGo` | — | 16 | `xor r0,r0,r0; mtctr r12; bctrl; ld r2,24(r1)`. Requires StackPlatform. |
| `OpSyscall` | — | 16 | `sc; bns+ 8; neg r3,r3; xor r0,r0,r0`: an error returns a negative errno in r3. |

## Branches and loops

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpJump` | `Imm=0x100` | 4 | `b`: the displacement must be a multiple of 4 within ±32 MiB. Long and Far are ignored. |
| `OpJump` | `SImm=-0x2000000` | 4 | The most negative reach. |
| `OpJump` | `Imm=0x2000000` | rejected | Beyond the +32 MiB - 4 reach. |
| `OpJump` | `Imm=6` | rejected | The displacement is not word-aligned. |
| `OpJumpIf` | `Cond=Equal Imm=0x100` | 4 | One `bc` on CR0. The displacement is within -32768..32764. Signed and unsigned conditions share CR0 bits, so the preceding compare decides signedness. |
| `OpJumpIf` | `Cond=Less Imm=0x7ffc` | 4 | The largest short forward reach. |
| `OpJumpIf` | `Cond=Greater Imm=0x8000` | rejected | Beyond `bc` reach without Long. |
| `OpJumpIf` | `Cond=NotEqual Imm=0x100000 Long` | 8 | Inverted `bc` over a `b`, with ±32 MiB reach. |
| `OpJumpIf` | `Cond=Carry Imm=0x100` | 16 | POWER8 copies XER.CA to CR7 (`mfxer r12; rldicl; mtcrf`) and then branches. POWER9 uses `mcrxrx`. |
| `OpJumpIf` | `Cond=NotCarry Imm=0x100000 Long` | 20 | Carry copy plus the Long form. |
| `OpJumpIf` | `Cond=Carry32 Imm=0x100` | rejected | CA32 conditions need POWER9 (`mcrxrx`). |
| `OpBranchZero` | — | unsupported | Not implemented; use a compare and `OpJumpIf`. |
| `OpBranchBit` | — | unsupported | Not implemented. |
| `OpSetCount` | `A=3` | 4 | `mtctr A`. |
| `OpLoop` | `Imm=0x40` | 4 | `bdnz`: decrements CTR and branches while it is nonzero. Bits must be 0 or 64. |
| `OpLoop` | `Imm=0x100000 Long` | 8 | `bdz +8; b target`. |
| `OpLoop` | `Imm=0x8000` | rejected | Beyond `bc` reach without Long. |
| `OpLoop` | `Imm=0x40 Bits=32` | rejected | CTR loops are 64 bits only. |
| `OpJumpTable` | `A=3 B=4 Table=64,128,192` | 72 | Bounded switch: `li b, n` (n = len(Table)-1; `lis; ori` and longer forms for larger counts), `cmpld a, b`, `isel b, a, b, lt` clamps the index so entry n is the default, `mflr r12; bcl 20, 31, $+4; mflr r11; mtlr r12` for the PC with LR kept, `addi r11, r11, 40`, `sldi b, b, 2`, `add b, r11, b`, `lwz b, 0(b)`, `extsw b, b`, `add r11, r11, b`, `mtctr r11`, `bctr`, then one 32-bit table-relative entry per target. Writes B, r11, r12, CR0 and CTR; A is preserved, and so is LR. |
| `OpJumpTable` | `A=0 B=4 Table=64,128` | rejected | r0 reads as zero in `isel` and as a base; r1, r2, r11, r12 and r13 have fixed roles. |
| `OpJumpTable` | `A=3 B=3 Table=64,128` | rejected | A and B must differ. |
| `OpJumpTable` | `A=3 B=4 Table=64` | rejected | The table needs a case and the default. |

## Floating point

Float opcodes take Bits 32 or 64. A GP operand holds the IEEE bit pattern.
A 32-bit GP input is widened with `mtvsrd; xxsldwi; xscvspdpn`. A 32-bit GP
result is packed with `xscvdpspn; xxsldwi; mfvsrwz`. GP inputs are staged in
f0/f1/f2 and computed results in f0, so FPR operands should not be f0..f2 when
GP operands are mixed in.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpFloatMove` | `Bits=64 V=A:3,B:4` | 4 | `fmr`. |
| `OpFloatMove` | `Bits=64 V=A:3,B:3` | 0 | A move to itself is elided. |
| `OpFloatMove` | `A=3 Bits=64 V=B:4` | 4 | FPR to GP: `mfvsrd`. |
| `OpFloatMove` | `A=3 Bits=32 V=B:4` | 12 | FPR to GP binary32 bits. |
| `OpFloatMove` | `B=3 Bits=64 V=A:4` | 4 | GP to FPR: `mtvsrd`. |
| `OpFloatMove` | `B=3 Bits=32 V=A:4` | 12 | GP binary32 bits to an FPR (as a double). |
| `OpFloatMove` | `A=3 B=4 Bits=64` | rejected | At least one side must be an FPR. |
| `OpFloatMove` | `Bits=16 V=A:3,B:4` | rejected | Bits must be 32 or 64. |
| `OpFloatBits` | `Bits=64 Arith=OpXor V=A:3,B:4` | 4 | Sign operations: OpAnd gives `fabs` and OpXor gives `fneg`. |
| `OpFloatBits` | `A=3 B=4 Bits=64 Arith=OpAnd` | 12 | GP in and out: `mtvsrd; fabs; mfvsrd`. |
| `OpFloatBits` | `A=3 B=4 Bits=32 Arith=OpXor` | 28 | 32-bit GP operands add the conversions. |
| `OpFloatBits` | `A=3 B=4 Bits=8 Arith=OpAnd` | rejected | Bits must be 32 or 64. |
| `OpFloatBits` | `V=A:3,B:4 Bits=64 Arith=OpOr` | rejected | Arith must be OpAnd or OpXor. |
| `OpFloatUnary` | `Bits=64 Arith=OpFSqrt V=A:3,B:4` | 4 | `fsqrt` (Bits=32: `fsqrts`). Writes FPSCR. |
| `OpFloatUnary` | `A=3 B=4 Bits=64 Arith=OpFSqrt` | 12 | GP operands go through f0. |
| `OpFloatUnary` | `A=3 B=4 Bits=32 Arith=OpFSqrt` | 28 | 32-bit GP operands add the conversions. |
| `OpFloatUnary` | `Bits=32 Arith=OpFConvert V=A:3,B:4` | 4 | binary64 to binary32: `frsp`. |
| `OpFloatUnary` | `A=3 B=4 Bits=32 Arith=OpFConvert` | 20 | 64-bit GP input and 32-bit GP result. |
| `OpFloatUnary` | `A=3 B=4 Bits=64 Arith=OpFConvert` | 16 | binary32 to binary64: the GP input conversion widens, and `mfvsrd` stores the result. |
| `OpFloatUnary` | `Bits=64 Arith=OpFConvert V=A:3,B:3` | 0 | An FPR already holds the double, so a widening into the same register is elided. |
| `OpFloatUnary` | `A=3 B=4 Arith=OpFSqrt` | rejected | Bits must be 32 or 64. |
| `OpFloatUnary` | `V=A:3,B:4 Bits=64 Arith=OpFAdd` | rejected | Arith must be OpFSqrt or OpFConvert. |
| `OpFloatBinary` | `Bits=64 Arith=OpFAdd V=A:3,B:4,C:5` | 4 | A = B op C with `fadd`/`fsub`/`fmul`/`fdiv` (single-precision forms for Bits=32). |
| `OpFloatBinary` | `A=3 B=4 C=5 Bits=64 Arith=OpFMul` | 16 | GP operands: B goes through f0, C through f1, and the result through f0. A may alias B or C. |
| `OpFloatBinary` | `A=3 B=4 C=5 Bits=32 Arith=OpFDiv` | 40 | 32-bit GP operands. |
| `OpFloatBinary` | `A=3 B=4 C=5 Bits=64 Arith=OpFSqrt` | rejected | Only OpFAdd, OpFSub, OpFMul and OpFDiv. |
| `OpFloatBinary` | `A=3 B=4 C=5 Bits=16 Arith=OpFAdd` | rejected | Bits must be 32 or 64. |
| `OpFloatFMA` | `Bits=64 V=A:3,B:4,C:5,D:6` | 4 | A = B*C + D with one rounding (`fmadd`/`fmadds`). |
| `OpFloatFMA` | `A=3 B=4 C=5 D=6 Bits=64` | 20 | GP operands go through f0, f1 and f2. |
| `OpFloatFMA` | `A=3 B=4 C=5 D=6 Bits=32` | 52 | 32-bit GP operands. |
| `OpFloatFMA` | `A=3 B=4 C=5 D=6` | rejected | Bits must be 32 or 64. |
| `OpIntToFloat` | `B=4 Bits=64 Imm=64 Arith=OpFFromSigned V=A:3` | 8 | `mtvsrd fA,B; fcfid fA,fA`, rounding in the FPSCR mode. Bits is the float width (32: `fcfids`), Imm the integer width (32 or 64). A must be an FPR and B a GP register; no other FPR is written. Writes FPSCR. |
| `OpIntToFloat` | `B=4 Bits=32 Imm=64 Arith=OpFFromUnsigned V=A:3` | 8 | `mtvsrd; fcfidus` (Bits=64: `fcfidu`). |
| `OpIntToFloat` | `B=4 Bits=64 Imm=32 Arith=OpFFromSigned V=A:3` | 8 | A 32-bit source moves with `mtvsrwa` (unsigned: `mtvsrwz`, then the exact signed `fcfid`). |
| `OpIntToFloat` | `A=3 B=4 Bits=64 Imm=64 Arith=OpFFromSigned` | rejected | A must be an FPR. |
| `OpIntToFloat` | `B=4 Bits=64 Imm=16 Arith=OpFFromSigned V=A:3` | rejected | Imm must be 32 or 64. |
| `OpFloatToInt` | `A=3 Bits=64 Disp=64 Imm=1 Arith=OpFToSigned V=B:4` | 8 | ConversionNative: `fctidz f0,fB; mfvsrd A,f0`. The conversion goes through the f0 temporary. Bits is the integer width (32: `fctiwz` and `mfvsrwz`, zero-extended); Disp is the float width, 32 or 64. Writes FPSCR. |
| `OpFloatToInt` | `A=3 Bits=32 Disp=64 Imm=0 Arith=OpFToSigned V=B:4` | 16 | ConversionSaturate: the conversions clamp, but signed NaN gives MIN, so `fcmpu cr0,fB,fB` before the conversion and `isel A,0,A,so` after it make it 0. Writes CR0. |
| `OpFloatToInt` | `A=3 Bits=64 Disp=32 Imm=0 Arith=OpFToUnsigned V=B:4` | 8 | `fctiduz` (32 bits: `fctiwuz`) already clamps and gives 0 for NaN, so both semantics share this form. |
| `OpFloatToInt` | `A=3 B=4 Bits=64 Disp=64 Arith=OpFToSigned` | rejected | B must be an FPR. |
| `OpFloatToInt` | `A=3 Bits=16 Disp=64 Arith=OpFToSigned V=B:4` | rejected | Bits must be 32 or 64. |
| `OpFloatToInt` | `A=3 Bits=64 Disp=16 Arith=OpFToSigned V=B:4` | rejected | Disp must be 32 or 64. |
| `OpFloatCompare` | `A=3 V=B:4,C:5 Bits=64 Imm=0` | 12 | A = 1 when FPRs B and C satisfy the `op.FloatCondition` in Imm, else 0: `fcmpu cr0,fB,fC; mfcr A; rlwinm A,A,s,31,31`. fcmpu sets exactly one of CR0 LT, GT, EQ and FU (bits 0-3), and the rotation s = bit+1 moves the wanted bit to bit 63. FPRs hold binary32 values in double format, so Bits=32 is the same. FloatEqual, FloatLess, FloatGreater and FloatUnordered are one bit (EQ, LT, GT, FU). Writes CR0 and FPSCR. |
| `OpFloatCompare` | `A=3 V=B:4,C:5 Bits=32 Imm=4` | 16 | Two-bit conditions first OR the bits into the first one: `cror lt,lt,eq` for FloatLessEqual, `cror gt,gt,eq` for FloatGreaterEqual and `cror lt,lt,gt` for FloatOrderedNotEqual. |
| `OpFloatCompare` | `A=3 V=B:4,C:5 Bits=64 Imm=1` | 16 | Each negation (odd Imm, and FloatOrdered, the negation of FU) uses `crnor` of the same bits: `crnor eq,eq,eq` for FloatNotEqual, `crnor lt,lt,eq` for FloatUnorderedGreater, `crnor lt,lt,gt` for FloatUnorderedEqual. A NaN operand sets only FU, so ordered conditions give 0 and unordered ones 1. |
| `OpFloatCompare` | `A=3 B=4 C=5 Bits=64 Imm=0` | rejected | B and C must be FPRs. |
| `OpFloatCompare` | `A=3 V=B:4,C:5 Bits=16 Imm=0` | rejected | Bits must be 32 or 64. |
| `OpFloatCompare` | `A=3 V=B:4,C:5 Bits=64 Imm=14` | rejected | Imm must be an `op.FloatCondition` (0-13). |
| `OpLiteralLoad` | `Bits=64 Disp=0x100 V=A:3` | 24 | Loads from the pool PC in Disp, PC-relative: `mflr r12; bcl 20,31,+4; mflr r11; addis r11,r11,hi; lfd fA,lo(r11); mtlr r12`. Bits=32 uses `lfs`; other Bits use `lfd`. |
| `OpLiteralLoad` | `Bits=32 Disp=0x100 V=A:3` | 24 | `lfs`. |
| `OpLiteralLoad` | `A=3 Bits=64 Disp=0x100` | rejected | A must be an FPR. |
| `OpLiteralLoad` | `Bits=64 Disp=0x80000000 V=A:3` | rejected | The pool offset must be within `addis` reach (about ±2 GiB). |
| `OpLiteralData` | `Data=000000000000f03f` | 8 | Pool bytes are emitted unchanged. |
| `OpLiteralData` | `Data=0000803f` | 4 | The length must be a multiple of 4. |
| `OpLiteralData` | `Data=000000` | rejected | The length is not a multiple of 4. |

## Vectors

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpVector` | — | unsupported | No vector operations on ppc64. |
| `OpVector` | `V=A:1,B:2,C:3 Bits=8 Imm=0x0706050403020100 Disp=0x08090a0b0c0d0e0f Arith=OpVShuffle` | unsupported | Shuffles and broadcasts are scalarized. |
| `OpVector` | `V=A:1,B:2 Bits=16 Arith=OpVExtendUnsigned` | unsupported | Widening extends are scalarized. |
| `OpVector` | `V=A:1,B:2 Bits=64 Imm=1 Arith=OpVExtendSigned` | unsupported | |
| `OpVectorMove` | — | unsupported | Not implemented. |
| `OpVectorSpill` | — | unsupported | Not implemented. |
| `OpVectorReload` | — | unsupported | Not implemented. |

## Barriers

`OpBarrier` reads and writes no registers or flags. `Barrier` names the
guarantee; `Policy.Barrier` reports whether the target's instruction rests on
the instruction set or also on host configuration.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpBarrier` | `Barrier=Speculation` | 4 | `ori r31, r31, 0`, leaving r31 unchanged. Platform: a barrier only with updated firmware, otherwise a no-op. |
| `OpBarrier` | `Barrier=Memory` | 4 | `sync` (hwsync). |
| `OpBarrier` | `Barrier=Instruction` | 4 | `isync`. |
| `OpBarrier` | — | rejected | Barrier must name a guarantee. |

## Stack areas

`OpSlotAddress` writes to GP register A the address of spill slot Slot, as
`OpSpill` addresses it: 128+8*Slot(r1), or 8*Slot(r29) without a platform
stack. It writes only A.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpSlotAddress` | `A=3 Slot=0` | 4 | `addi A,r1,128`. |
| `OpSlotAddress` | `A=3 Slot=4079` | 4 | The largest offset in one `addi` (32760). |
| `OpSlotAddress` | `A=3 Slot=4080` | 8 | `addis A,r1,1` then `addi A,A,-32768`. |
| `OpSlotAddress` | `A=3 Slot=8176` | 4 | An offset of 65536 needs only `addis`. |
| `OpSlotAddress` | `A=0 Slot=0` | rejected | A must not be r0, which the second `addi` would read as zero. |
| `OpSlotAddress` | `A=1 Slot=0` | rejected | A must not be the stack pointer. |
| `OpSlotAddress` | `A=3 Slot=0xffffffffffffffff` | rejected | Negative slot (-1). |

## Float passing

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpX87Result` | `V=A:3 Bits=64 Slot=0` | unsupported | 386 only: `machine.Visit` returns `ErrUnsupportedOperation`. FP results already travel in an FP register. |

## Prefetch

`OpPrefetch` hints an access at the address in GP register A, by the
`op.PrefetchKind` in Imm. It never faults, writes no register, and has no
architectural effect.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpPrefetch` | `A=3 Imm=0` | 4 | `dcbt 0, r3` (TH = 0) for PrefetchRead. |
| `OpPrefetch` | `A=3 Imm=1` | 4 | `dcbtst 0, r3` for PrefetchWrite. |
| `OpPrefetch` | `A=3 Imm=2` | rejected | Imm must be an `op.PrefetchKind`. |
| `OpPrefetch` | `V=A:1 Imm=0` | rejected | A must be a GP register. |


## Tail calls

`OpTailCall` ends a function with a C tail call. The arguments are in place
and the target is in the call-target register, as for `OpCallC`. It leaves
a frame of Imm bytes, as `OpFrameLeave` does (none when Imm is zero), and
jumps to the target, so the callee returns to this function's caller.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpTailCall` | `Imm=0` | 8 | `mtctr r12; bctr`: r12 holds the callee's global entry. The original caller restores its TOC after its indirect call returns. |
| `OpTailCall` | `Imm=32` | 24 | The frame is left as `OpFrameLeave` leaves it, restoring LR and r2, first. |
| `OpTailCall` | `Imm=24` | rejected | Frames are 16-byte multiples. |

## Atomics

`OpAtomic` performs the sequentially consistent atomic `Arith` (`OpAtomicLoad`
… `OpAtomicCompareSwap`) at width `Bits` on the naturally aligned address in
B. A receives the old value, zero-extended; C is the operand (the value of a
store, the expected value of a compare-and-swap) and D the replacement. `Imm`
is the `op.MemoryOrder`, of which only `MemoryOrderSequential` (0) exists. A
store reads no A. No operand may be r0, r1, r11 or r12, and A, where it is
written, differs from every input.

The mappings are the C++11 sequentially consistent ones. A load is `sync`,
the load, `cmpw`/`cmpd` of the loaded value with itself, a never-taken
`bne-` and `isync`. A store is `sync` and the store. A read-modify-write is
`sync`, then a loop of `l{b,h,w,d}arx r11, 0, B`, the operation into r12 and
`st{b,h,w,d}cx. r12, 0, B` with `bne-` back to the reservation, then `isync`
and `mr A, r11`; a swap stores C directly. A compare-and-swap compares r11
with C (`cmpw` up to 32 bits, `cmpd` for 64) and leaves the loop through
`bne-` to the `isync`. `lbarx` and `lharx` are ISA 2.06, so POWER8 has every
width, and the forms are the same on POWER9 and POWER10. Every form but the
store writes CR0.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpAtomic` | `A=3 B=4 Bits=8 Arith=OpAtomicLoad` | 20 | `sync; lbz; cmpw r3, r3; bne- .+4; isync`. |
| `OpAtomic` | `A=3 B=4 Bits=16 Arith=OpAtomicLoad` | 20 | `lhz`. |
| `OpAtomic` | `A=3 B=4 Bits=32 Arith=OpAtomicLoad` | 20 | `lwz`. |
| `OpAtomic` | `A=3 B=4 Bits=64 Arith=OpAtomicLoad` | 20 | `ld`, `cmpd`. |
| `OpAtomic` | `B=4 C=5 Bits=8 Arith=OpAtomicStore` | 8 | `sync; stb r5, 0(r4)`. |
| `OpAtomic` | `B=4 C=5 Bits=16 Arith=OpAtomicStore` | 8 | `sth`. |
| `OpAtomic` | `B=4 C=5 Bits=32 Arith=OpAtomicStore` | 8 | `stw`. |
| `OpAtomic` | `A=4 B=4 C=5 Bits=64 Arith=OpAtomicStore` | 8 | `std`. A is not read, so it may name an input. |
| `OpAtomic` | `A=3 B=4 C=5 Bits=8 Arith=OpAtomicAdd` | 28 | `sync; lbarx r11; add r12, r11, r5; stbcx. r12; bne-; isync; mr r3, r11`. |
| `OpAtomic` | `A=3 B=4 C=5 Bits=16 Arith=OpAtomicAdd` | 28 | `lharx`/`sthcx.`. |
| `OpAtomic` | `A=3 B=4 C=5 Bits=32 Arith=OpAtomicAdd` | 28 | `lwarx`/`stwcx.`. |
| `OpAtomic` | `A=3 B=4 C=5 Bits=64 Arith=OpAtomicAdd` | 28 | `ldarx`/`stdcx.`. |
| `OpAtomic` | `A=3 B=4 C=5 Bits=8 Arith=OpAtomicAnd` | 28 | `and r12, r11, r5`. |
| `OpAtomic` | `A=3 B=4 C=5 Bits=64 Arith=OpAtomicAnd` | 28 | |
| `OpAtomic` | `A=3 B=4 C=5 Bits=16 Arith=OpAtomicOr` | 28 | `or`. |
| `OpAtomic` | `A=3 B=4 C=5 Bits=32 Arith=OpAtomicOr` | 28 | |
| `OpAtomic` | `A=3 B=4 C=5 Bits=8 Arith=OpAtomicXor` | 28 | `xor`. |
| `OpAtomic` | `A=3 B=4 C=5 Bits=64 Arith=OpAtomicXor` | 28 | |
| `OpAtomic` | `A=3 B=4 C=5 Bits=8 Arith=OpAtomicSwap` | 24 | `sync; lbarx r11; stbcx. r5; bne-; isync; mr r3, r11`. |
| `OpAtomic` | `A=3 B=4 C=5 Bits=16 Arith=OpAtomicSwap` | 24 | |
| `OpAtomic` | `A=3 B=4 C=5 Bits=32 Arith=OpAtomicSwap` | 24 | |
| `OpAtomic` | `A=3 B=4 C=5 Bits=64 Arith=OpAtomicSwap` | 24 | |
| `OpAtomic` | `A=3 B=4 C=5 D=6 Bits=8 Arith=OpAtomicCompareSwap` | 32 | `sync; lbarx r11; cmpw r11, r5; bne- done; stbcx. r6; bne-; done: isync; mr r3, r11`. |
| `OpAtomic` | `A=3 B=4 C=5 D=6 Bits=16 Arith=OpAtomicCompareSwap` | 32 | |
| `OpAtomic` | `A=3 B=4 C=5 D=6 Bits=32 Arith=OpAtomicCompareSwap` | 32 | |
| `OpAtomic` | `A=3 B=4 C=5 D=6 Bits=64 Arith=OpAtomicCompareSwap` | 32 | `cmpd`. |
| `OpAtomic` | `A=3 B=4 C=5 Bits=12 Arith=OpAtomicAdd` | rejected | Bits must be 8, 16, 32 or 64. |
| `OpAtomic` | `A=3 B=4 C=5 Bits=32 Imm=1 Arith=OpAtomicAdd` | rejected | Imm must be a defined `MemoryOrder`. |
| `OpAtomic` | `A=3 B=4 C=5 Bits=32 Arith=OpAdd` | rejected | Arith must be an atomic operation. |
| `OpAtomic` | `A=3 V=B:4 Bits=64 Arith=OpAtomicLoad` | rejected | The address must be a GP register. |
| `OpAtomic` | `A=3 B=0 Bits=64 Arith=OpAtomicLoad` | rejected | r0 reads as zero in the address field. |
| `OpAtomic` | `A=4 B=4 C=5 Bits=32 Arith=OpAtomicAdd` | rejected | A must differ from the inputs. |
| `OpAtomic` | `A=6 B=4 C=5 D=6 Bits=32 Arith=OpAtomicCompareSwap` | rejected | A must differ from the replacement. |
| `OpAtomic` | `A=11 B=4 Bits=64 Arith=OpAtomicLoad` | rejected | r11 and r12 are the loop temporaries. |
| `OpAtomic` | `A=3 B=4 C=12 Bits=64 Arith=OpAtomicSwap` | rejected | |
