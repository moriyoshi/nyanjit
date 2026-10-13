# loong64 instruction forms

This file lists the `machine.Instruction` forms that the loong64 (LA64) byte
assembler accepts. `backend/target/forms_test.go` assembles every example row.

The table assumes the following:

- The byte assembler is built with `NewByteAssembler(abi.StackPlatform, 0)`.
  This means there are no Go argument homes, and spill slot `n` is at `(n+1)*8(r3)`.
- The CPU is baseline LA64 with LSX. LSX provides the 128-bit vector forms.
- Each example is assembled alone at PC 0. A branch target is an absolute PC in
  `Imm`, and it must be a multiple of 4.
- The byte assembler takes no ABI flavor. The C-ABI rules (call target in r20,
  link register r1) are fixed in the encoder.
- LA64 has no condition flags. `OpCompareImmediate` and `OpCompareRegister`
  emit no compare instruction. They record the operand pair. A later `OpJumpIf`
  in the same assembler uses that pair in `beq`/`bne`/`blt`/`bge`/`bltu`/`bgeu`.
  The pair is r0, r0 when there was no compare.
  `PreserveFlags` has no flag-neutral variant and is rejected.

GP registers are numbered r0..r31 as plain `Register(n)`. FP and vector
registers f0..f31 / vr0..vr31 are `backend.VectorRegister(n)`. Examples write
them as `V=A:n,B:m`. `Policy.RegisterName` prints them as `R<n>` and `F<n>`.
OpSaveCFloat and OpRestoreCFloat are the only exceptions. They take the raw FP
ordinal 0..31 in `A`.

The encoders use r12, r19 and r20 as scratch registers. r12 holds byte-swapped
store values and the float-to-integer NaN bound. r19 and r20 hold address,
constant and shift temporaries. r20 is also the comparison immediate and the
call target. f0..f2 / vr0..vr2 are FP/vector expansion temporaries. r18 is
`Policy.ScratchRegister`, r23 is the workspace register, and r3 is SP.

For forms not listed here, `PhysicalSize` is the authority on size.

## Constants and moves

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpConst` | `A=4 Imm=3` | 4 | `addi.d` for signed 12-bit values |
| `OpConst` | `A=4 SImm=-1` | 4 | Signed 12-bit, so a single `addi.d` |
| `OpConst` | `A=4 Imm=0xfff` | 4 | `ori` for 0..4095 |
| `OpConst` | `A=4 Imm=0x10000000000000` | 4 | Low 52 bits zero: one `lu52i.d` |
| `OpConst` | `A=4 Imm=0x12345` | 8 | `lu12i.w` + `ori` |
| `OpConst` | `A=4 Imm=0x80000000` | 8 | `lu12i.w` + `lu32i.d` to clear the sign extension |
| `OpConst` | `A=4 Imm=0x123456789abcdef0` | 16 | Full `lu12i.w`/`ori`/`lu32i.d`/`lu52i.d` |
| `OpConst` | `A=32 Imm=3` | rejected | A must be a GP register r0..r31 |
| `OpSymbol` | `A=4 Sym=foo` | 16 | Always the four-word sequence with a `RelocLoong64Constant` relocation |
| `OpSymbol` | `V=A:8 Sym=foo` | rejected | GP destination only |
| `OpMove` | `A=4 B=5` | 4 | `or A, B, r0` |
| `OpMove` | `A=4 B=4` | 0 | Self-move emits nothing |
| `OpMove` | `A=4 V=B:8` | rejected | GP only; use OpFloatMove or OpVectorMove |

## Integer arithmetic

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpBinary` | `A=4 B=5 C=6 Bits=64 Arith=OpAdd` | 4 | A = B op C as a full 64-bit op; Arith is OpAdd, OpSub, OpMul, OpAnd, OpOr or OpXor; any aliasing allowed |
| `OpBinary` | `A=4 B=5 C=6 Bits=32 Arith=OpMul` | 4 | Bits is 0, 8, 16, 32 or 64; the operation is still 64-bit (`mul.d`) |
| `OpBinary` | `A=4 B=5 C=6 Bits=64 Arith=OpShiftLeft` | rejected | Shifts use OpShift or OpShiftBy |
| `OpBinary` | `A=4 B=5 C=6 Bits=12 Arith=OpAdd` | rejected | Invalid width |
| `OpBinary` | `A=4 B=5 C=6 Bits=64 Arith=OpAdd PreserveFlags` | unsupported | No flags to preserve, so no flag-neutral variant |
| `OpBinaryImmediate` | `A=4 B=5 Bits=64 Imm=2047 Arith=OpAdd` | 4 | OpAdd: signed Imm in -2048..2047 (`addi.d`) |
| `OpBinaryImmediate` | `A=4 B=5 Bits=64 SImm=-2048 Arith=OpAdd` | 4 | |
| `OpBinaryImmediate` | `A=4 B=5 Bits=64 Imm=2048 Arith=OpSub` | 4 | OpSub negates Imm: -2047..2048 |
| `OpBinaryImmediate` | `A=4 B=5 Bits=32 Imm=4095 Arith=OpAnd` | 4 | OpAnd/OpOr/OpXor: unsigned Imm 0..4095 (`andi`/`ori`/`xori`) |
| `OpBinaryImmediate` | `A=4 B=5 Bits=64 Imm=2048 Arith=OpAdd` | rejected | Out of signed 12-bit range; no expansion |
| `OpBinaryImmediate` | `A=4 B=5 Bits=64 Imm=4096 Arith=OpOr` | rejected | Out of 12-bit unsigned range |
| `OpBinaryImmediate` | `A=4 B=5 Bits=64 Imm=3 Arith=OpMul` | rejected | No multiply-immediate |
| `OpBinaryImmediate` | `A=4 B=5 Bits=64 Imm=3 Arith=OpAdd PreserveFlags` | unsupported | No flag-neutral variant |
| `OpBinaryCarry` | `A=4 B=5 C=6 D=7 Arith=OpAdd` | unsupported | Canonicalized to 32-bit OpWordCarry with CarryFlags; only 64-bit word carry is implemented |
| `OpInc` | `A=4 B=5` | 4 | `addi.d A, B, 1` |
| `OpInc` | `A=4 B=4 PreserveFlags` | rejected | No IncrementNoFlags form |
| `OpDec` | `A=4 B=4` | 4 | `addi.d A, B, -1` |
| `OpDec` | `A=4 B=4 PreserveFlags` | rejected | No DecrementNoFlags form |
| `OpMulAdd` | `A=4 B=5 C=6 D=7` | 8 | A = B + C*D through r19 (`mul.d` + `add.d`) |
| `OpMulHigh` | `A=4 B=5 C=6 Bits=64` | 4 | High half of unsigned B*C (`mulh.du`) |
| `OpMulHigh` | `A=4 B=5 C=6 Bits=32` | 8 | Bits < 64: `mul.d` + `srli.d` by Bits; inputs must be zero-extended to Bits |
| `OpMulHigh` | `A=4 B=5 C=6 Bits=12` | rejected | Bits is 8, 16, 32 or 64 |
| `OpMulHighSigned` | `A=4 B=5 C=6 Bits=64` | 4 | High half of signed B*C (`mulh.d`) |
| `OpMulHighSigned` | `A=4 B=5 C=6 Bits=32` | 8 | `mulh.w`, then `bstrpick.d` zero-extends the high word |
| `OpMulHighSigned` | `A=4 B=5 C=6 Bits=16` | 16 | Bits 8/16: `slli.d` of B and C by 64-Bits into r19 and r20, `mulh.d`, then `srli.d` by 64-Bits; inputs must be zero-extended to Bits |
| `OpMulHighSigned` | `A=4 B=5 C=6 Bits=12` | rejected | Bits is 8, 16, 32 or 64 |
| `OpMulWide` | `A=4` | rejected | 386 only: `machine.Visit` returns `ErrUnsupportedOperation`. Use `OpMulHigh`. |
| `OpCarryBit` | `A=4 B=5 C=6 Bits=64 Arith=OpAdd` | 8 | A = carry out of B+C (`add.d` r20 + `sltu`) |
| `OpCarryBit` | `A=4 B=5 C=6 Bits=64 Arith=OpSub` | 4 | A = borrow of B-C (`sltu`) |
| `OpCarryBit` | `A=4 B=5 C=6 Bits=32 Arith=OpAdd` | rejected | Bits must be 64 |
| `OpCarryBit` | `A=4 B=5 C=6 Bits=64 Arith=OpAdd Carry=4` | rejected | Unknown carry mode |
| `OpWordCarry` | `A=4 B=5 C=6 D=7 Bits=64 Arith=OpAdd` | 12 | CarryValue/CarryBitValue/CarryFlags: carry-in is bit 0 of D (`andi` r19); A = B op C op carry |
| `OpWordCarry` | `A=4 B=5 C=6 D=7 E=8 Bits=64 Carry=2 Arith=OpAdd` | 16 | CarryOperands: the carry of D+E is computed in r19 first |
| `OpWordCarry` | `A=4 B=5 C=6 D=7 E=8 Bits=64 Carry=2 Arith=OpSub` | 12 | CarryOperands borrow of D-E |
| `OpWordCarry` | `A=4 B=5 C=6 D=7 Bits=64 Arith=OpMul` | rejected | Arith is OpAdd or OpSub |
| `OpWordCarry` | `A=4 B=5 C=6 D=7 Bits=64 Carry=4 Arith=OpAdd` | rejected | Unknown carry mode |
| `OpAndNot` | `A=4 B=5 C=6 Bits=64` | 4 | A = B &^ C (`andn`); Bits is 8, 16, 32 or 64 |
| `OpAndNot` | `A=4 B=5 C=6 Bits=0` | rejected | Bits required |
| `OpCountZeros` | `A=4 B=5 Bits=64 Arith=OpLeadingZeros` | 4 | `clz.d` |
| `OpCountZeros` | `A=4 B=5 Bits=32 Arith=OpTrailingZeros` | 4 | `ctz.w` |
| `OpCountZeros` | `A=4 B=5 Bits=8 Arith=OpLeadingZeros` | 8 | Narrow CLZ: `clz.w` then subtract 32-Bits; input must be zero-extended |
| `OpCountZeros` | `A=4 B=5 Bits=8 Arith=OpTrailingZeros` | 8 | Narrow CTZ ORs in bit Bits through r19 (`ori` for Bits=8) |
| `OpCountZeros` | `A=4 B=5 Bits=16 Arith=OpTrailingZeros` | 12 | Bits=16 needs a `lu12i.w` constant |
| `OpCountZeros` | `A=4 B=5 Bits=64 Arith=OpAdd` | rejected | Arith is OpLeadingZeros or OpTrailingZeros |
| `OpDivide` | `A=4 B=5 C=6 Bits=64 Imm=2 Arith=OpDivSigned` | 4 | DivisionUnchecked (Imm=2): bare `div.d`/`div.du`/`mod.d`/`mod.du` |
| `OpDivide` | `A=4 B=5 C=6 Bits=32 Imm=2 Arith=OpRem` | 4 | Unsigned inputs arrive zero-extended, so the 64-bit forms serve every Bits |
| `OpDivide` | `A=4 B=5 C=6 Bits=64 Imm=0 Arith=OpDiv` | 8 | DivisionZero (Imm=0): quotient in r19, `maskeqz` gives x/0 = 0 |
| `OpDivide` | `A=4 B=5 C=6 Bits=16 Imm=1 Arith=OpDiv` | 20 | DivisionAllOnes (Imm=1): ORs in -(C == 0) through r20, then masks to Bits |
| `OpDivide` | `A=4 B=5 C=6 Bits=64 Imm=0 Arith=OpRem` | 16 | Checked remainder: `maskeqz`/`masknez`/`or` give x%0 = x |
| `OpDivide` | `A=4 B=5 C=6 Bits=64 Imm=0 Arith=OpDivSigned` | 24 | Signed 64-bit: r20 = C+1 selects -B for C == -1 (MIN/-1 = MIN) |
| `OpDivide` | `A=4 B=5 C=6 Bits=64 Imm=1 Arith=OpDivSigned` | 32 | |
| `OpDivide` | `A=4 B=5 C=6 Bits=64 Imm=1 Arith=OpRemSigned` | 24 | x % -1 = 0, x % 0 = x |
| `OpDivide` | `A=4 B=5 C=6 Bits=8 Imm=0 Arith=OpDivSigned` | 20 | Narrow signed: sign-extend B, C into r19, r20, use `div.d` (no overflow), mask to Bits |
| `OpDivide` | `A=4 B=5 C=6 Bits=32 Imm=1 Arith=OpRemSigned` | 28 | |
| `OpDivide` | `A=6 B=6 C=6 Bits=64 Imm=0 Arith=OpRemSigned` | 24 | A, B and C may alias: inputs are read before A is written |
| `OpDivide` | `A=4 B=5 C=6 Bits=12 Imm=0 Arith=OpDiv` | rejected | Bits is 8, 16, 32 or 64 |
| `OpDivide` | `A=4 B=5 C=6 Bits=64 Imm=3 Arith=OpDiv` | rejected | Imm is an op.DivisionSemantics |
| `OpDivide` | `A=4 B=5 C=6 Bits=64 Imm=0 Arith=OpMul` | rejected | Arith is OpDiv, OpDivSigned, OpRem or OpRemSigned |

## Width changes and shifts

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpNarrow` | `A=4 B=5 Imm=32` | 4 | Zero-extend the low Imm bits (`bstrpick.d`); Imm is 1..64 |
| `OpNarrow` | `A=4 B=5 Imm=64` | 4 | Imm=64 is a move (0 bytes when A == B) |
| `OpNarrow` | `A=4 B=5 Imm=0` | rejected | |
| `OpNarrow` | `A=4 B=5 Imm=65` | rejected | |
| `OpSignExtend` | `A=4 B=5 Imm=8` | 4 | 8/16: `ext.w.b`/`ext.w.h`; 32: `slli.w 0` |
| `OpSignExtend` | `A=4 B=5 Imm=32` | 4 | |
| `OpSignExtend` | `A=4 B=5 Imm=12` | 8 | Other widths 1..63: `slli.d` + `srai.d` |
| `OpSignExtend` | `A=4 B=4 Imm=64` | 0 | Imm=64 is a move |
| `OpSignExtend` | `A=4 B=5 Imm=0` | rejected | Imm is 1..64 |
| `OpShift` | `A=4 B=5 Bits=64 Imm=3 Arith=OpShiftLeft` | 4 | Imm < Bits; Bits is 8, 16, 32 or 64; Left/Right are 64-bit ops and the result is not narrowed |
| `OpShift` | `A=4 B=5 Bits=32 Imm=3 Arith=OpShiftRight` | 4 | The input must already be zero-extended to Bits |
| `OpShift` | `A=4 B=5 Bits=32 Imm=3 Arith=OpShiftRightArithmetic` | 8 | Bits < 64 sign-extends into r19 first |
| `OpShift` | `A=4 B=5 Bits=64 Imm=3 Arith=OpShiftRightArithmetic` | 4 | |
| `OpShift` | `A=4 B=5 Bits=32 Imm=8 Arith=OpRotateLeft` | 4 | Bits 32/64: one `rotri.w`/`rotri.d` (rotate left becomes rotate right by Bits-Imm) |
| `OpShift` | `A=4 B=5 Bits=16 Imm=3 Arith=OpRotateRight` | 12 | Bits 8/16: two shifts through r19/r20 plus `or` |
| `OpShift` | `A=4 B=5 Bits=16 Imm=0 Arith=OpRotateLeft` | 4 | Zero rotate is a move |
| `OpShift` | `A=4 B=5 Bits=32 Imm=32 Arith=OpShiftLeft` | rejected | Imm must be < Bits |
| `OpShift` | `A=4 B=5 Bits=0 Imm=3 Arith=OpShiftLeft` | rejected | Bits required |
| `OpShiftBy` | `A=4 B=5 C=6 Bits=64 Arith=OpShiftLeft` | 12 | Counts >= Bits yield 0 (`sltui`/`sll.d`/`maskeqz` through r19/r20) |
| `OpShiftBy` | `A=4 B=5 C=6 Bits=32 Arith=OpShiftRight` | 16 | Bits < 64 adds a final `bstrpick.d` narrow |
| `OpShiftBy` | `A=4 B=5 C=6 Bits=64 Arith=OpShiftRightArithmetic` | 16 | Counts >= Bits saturate to Bits-1 |
| `OpShiftBy` | `A=4 B=5 C=6 Bits=32 Arith=OpShiftRightArithmetic` | 24 | Bits < 64 also sign-extends into r20 and narrows the result |
| `OpShiftBy` | `A=4 B=5 C=6 Bits=64 Arith=OpRotateRight` | 4 | `rotr.d`; the count is taken modulo Bits |
| `OpShiftBy` | `A=4 B=5 C=6 Bits=64 Arith=OpRotateLeft` | 8 | Count negated in r20 |
| `OpShiftBy` | `A=4 B=5 C=6 Bits=32 Arith=OpRotateRight` | 8 | `rotr.w` + narrow |
| `OpShiftBy` | `A=4 B=5 C=6 Bits=16 Arith=OpRotateRight` | 20 | Bits 8/16 replicate the value to 32 bits in r19 first |
| `OpShiftBy` | `A=4 B=5 C=6 Bits=8 Arith=OpRotateLeft` | 32 | |
| `OpShiftBy` | `A=4 B=5 C=6 Bits=12 Arith=OpShiftLeft` | rejected | Bits is 8, 16, 32 or 64 |
| `OpScaled` | `A=4 B=5 C=6 Imm=1` | 4 | A = B + C*Imm + Disp; Imm=1 is `add.d` |
| `OpScaled` | `A=4 B=5 C=6 Imm=8` | 4 | Imm 2/4/8/16: `alsl.d` |
| `OpScaled` | `A=4 B=5 C=6 Imm=32` | 8 | Other powers of two: `slli.d` r19 + `add.d` |
| `OpScaled` | `A=4 B=5 C=6 Imm=3` | 12 | Non-power-of-two: constant in r19 + `mul.d` + `add.d` |
| `OpScaled` | `A=4 B=5 C=6 Imm=8 Disp=16` | 8 | Signed 12-bit Disp adds one `addi.d` |
| `OpScaled` | `A=4 B=5 C=6 Imm=8 Disp=0x1000` | 12 | Larger Disp: constant in r20 + `add.d` |
| `OpScaled` | `A=4 B=5 C=40 Imm=8` | rejected | GP registers only |

## Selection and comparison

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpSelect` | `A=4 B=5 C=6 D=7` | 12 | A = B != 0 ? C : D (`maskeqz`/`masknez` through r19/r20 + `or`) |
| `OpSelect` | `A=4 B=5 C=6 D=32` | rejected | GP registers only |
| `OpSelectFlags` | `A=4 C=5 D=6 Cond=Equal` | unsupported | There are no flags; use OpSelect |
| `OpCompareImmediate` | `A=4 Imm=0` | 0 | Zero compares against r0; nothing is emitted |
| `OpCompareImmediate` | `A=4 Imm=100` | 4 | A nonzero Imm is materialized in r20 like OpConst |
| `OpCompareImmediate` | `A=4 SImm=-1 Cmp=Signed` | 4 | Cmp is Equality, Signed or Unsigned; signedness comes from the branch condition |
| `OpCompareImmediate` | `A=4 Imm=0x12345678 Bits=64` | 8 | Bits is 0 or 64 |
| `OpCompareImmediate` | `A=4 Imm=1 Bits=32` | rejected | No 32-bit compare |
| `OpCompareRegister` | `A=4 B=5 Cmp=Unsigned` | 0 | Records the pair (A, B) only |
| `OpCompareRegister` | `A=4 B=5 Bits=32` | rejected | Bits is 0 or 64 |
| `OpCompareRegister` | `A=4 B=40` | rejected | GP registers only |
| `OpCompareSet` | `A=4 B=5 C=6 Bits=64 Cond=Less` | 4 | A = 1 when the Bits-wide integers in B and C satisfy Cond, else 0: `slt` (`sltu` for unsigned conditions), with B and C swapped for Greater, LessEqual, Above and BelowEqual. A may alias B or C. |
| `OpCompareSet` | `A=4 B=5 C=6 Bits=64 Cond=GreaterEqual` | 8 | GreaterEqual, LessEqual, AboveEqual and BelowEqual negate with `xori A, A, 1`. |
| `OpCompareSet` | `A=4 B=5 C=6 Bits=16 Cond=Equal` | 8 | `xor A, B, C` then `sltui A, A, 1` (NotEqual: `sltu A, r0, A`). Equality and unsigned conditions compare zero-extended narrow operands directly. |
| `OpCompareSet` | `A=4 B=5 C=6 Bits=32 Cond=Less` | 12 | Signed 8-, 16- and 32-bit conditions first shift B and C to the top of r19 and r20 (`slli.d` by 64-Bits). Writes r19 and r20. |
| `OpCompareSet` | `A=4 B=5 C=6 Bits=8 Cond=LessEqual` | 16 | |
| `OpCompareSet` | `A=4 B=5 C=19 Bits=8 Cond=Less` | rejected | C must not be r19 when the operands are shifted: r19 is written first. |
| `OpCompareSet` | `A=4 B=5 C=6 Bits=12 Cond=Less` | rejected | Bits must be 8, 16, 32 or 64. |
| `OpCompareSet` | `A=4 B=5 C=6 Bits=64 Cond=Sign` | rejected | Cond must be Equal through Above. |
| `OpReverseBytes` | `A=4 B=5 Bits=64` | 4 | A = B with its bytes reversed: `revb.d`. |
| `OpReverseBytes` | `A=4 B=5 Bits=32` | 4 | `revb.2w` reverses each word, so a zero-extended value stays zero-extended. |
| `OpReverseBytes` | `A=4 B=5 Bits=16` | 4 | `revb.2h`. |
| `OpReverseBytes` | `A=4 B=5 Bits=8` | rejected | Bits must be 16, 32 or 64. |
| `OpSaveCondition` | `A=4` | unsupported | |
| `OpRestoreCondition` | `A=4` | unsupported | |

## Memory

All integer memory forms take GP operands. Byte loads zero-extend.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpLoad64` | `A=4 B=5` | 4 | A = [B] (`ld.d`) |
| `OpLoad64` | `A=4 B=5 BigEndian` | 8 | `ld.d` + `revb.d` |
| `OpStore64` | `A=5 B=4` | 4 | [A] = B (`st.d`) |
| `OpStore64` | `A=5 B=4 BigEndian` | 8 | Swapped value in r12, then `st.d` |
| `OpLoad8` | `A=4 B=5` | 4 | `ld.bu`; byte order is ignored |
| `OpLoad8` | `A=4 B=5 BigEndian` | 4 | |
| `OpStore8` | `A=5 B=4` | 4 | `st.b` |
| `OpLoad16` | `A=4 B=5` | 4 | `ld.hu`, no displacement; natural alignment expected |
| `OpLoad16` | `A=4 B=5 BigEndian` | rejected | Narrow memory is native byte order only |
| `OpStore16` | `A=5 B=4` | 4 | `st.h`; A is the address and B the value |
| `OpLoad32` | `A=4 B=5` | 4 | `ld.wu` |
| `OpStore32` | `A=5 B=4` | 4 | `st.w` |
| `OpStore32` | `A=5 B=4 BigEndian` | rejected | |
| `OpLoadOffset` | `A=4 B=5 Disp=2040` | 4 | A = [B + Disp]; signed 12-bit Disp is folded |
| `OpLoadOffset` | `A=4 B=5 Disp=0xfffffffffffff800` | 4 | Disp -2048 |
| `OpLoadOffset` | `A=4 B=5 Disp=4096` | 12 | Other Disp: constant in r19 (r20 when B is r19) + `add.d` + `ld.d` |
| `OpLoadOffset` | `A=4 B=5 Disp=0x12345678` | 16 | |
| `OpLoadOffset` | `A=4 B=5 Disp=8 BigEndian` | 8 | Adds `revb.d` |
| `OpStoreOffset` | `A=5 B=4 Disp=8` | 4 | [A + Disp] = B |
| `OpStoreOffset` | `A=5 B=4 Disp=4096` | 12 | |
| `OpLoadAddress` | `A=4 B=5 C=6 Imm=1` | 4 | A = [B + C*Imm + Disp]; Imm=1 with Disp=0 is `ldx.d` |
| `OpLoadAddress` | `A=4 B=5 C=6 Imm=8` | 8 | A power-of-two scale is shifted into r19 first |
| `OpLoadAddress` | `A=4 B=5 C=6 Imm=3` | 12 | Non-power-of-two: constant + `mul.d` into r19 |
| `OpLoadAddress` | `A=4 B=5 C=6 Imm=0` | 12 | Imm=0 is a zero scale: A = [B + Disp] |
| `OpLoadAddress` | `A=4 B=5 C=6 Imm=8 Disp=16` | 8 | Disp != 0: address formed in r19 (as OpScaled), then `ld.d` with Disp |
| `OpLoadAddress` | `A=4 B=5 C=6 Imm=8 BigEndian` | 12 | Adds `revb.d` |
| `OpStoreAddress` | `A=5 B=6 C=4 Imm=1` | 4 | [A + B*Imm + Disp] = C (`stx.d`) |
| `OpStoreAddress` | `A=5 B=6 C=4 Imm=8 Disp=16` | 8 | |
| `OpStoreAddress` | `A=5 B=6 C=4 Imm=1 BigEndian` | 8 | Swapped value in r12 |
| `OpStoreAddress` | `A=5 B=6 C=40 Imm=1` | rejected | GP registers only |

## Calls, stack frame and spills

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpSyscall` | — | 4 | `syscall 0` |
| `OpCallC` | — | 4 | `jirl r1, r20, 0`: the callee address must be in r20 |
| `OpCallGo` | — | 4 | Same encoding as OpCallC |
| `OpReturn` | — | 4 | `jirl r0, r1, 0` |
| `OpSaveLink` | `Slot=0` | 0 | Under StackPlatform, r1 is saved by OpFrameEnter; nothing is emitted |
| `OpSaveLink` | `Slot=0x7fffffffffffffff` | rejected | Slot bounded so the offset arithmetic cannot overflow |
| `OpRestoreLink` | `Slot=0` | 0 | |
| `OpFrameEnter` | `Imm=0` | 4 | `st.d r1, -n(r3)`, then `addi.d r3, r3, -n`: r1 is stored before SP moves, and SP moves once |
| `OpFrameEnter` | `Imm=32` | 8 | |
| `OpFrameEnter` | `Imm=4096` | 16 | One word per 2032 bytes plus one: over 2048 bytes, r20 steps to the frame, then `st.d r1, -2048(r20)` and `addi.d r3, r20, -2048`; leading NOPs pad |
| `OpFrameEnter` | `Imm=0x100000` | 2072 | 1 MiB maximum |
| `OpFrameEnter` | `Imm=0x100001` | rejected | |
| `OpFrameEnter` | `Imm=32 CallArea=16` | rejected | Outgoing call areas are not supported; fold them into Imm |
| `OpFrameLeave` | `Imm=32` | 8 | `ld.d r1, 0(r3)`, then SP steps |
| `OpFrameLeave` | `Imm=4096` | 16 | |
| `OpLoadIncoming` | `A=4 Imm=8` | 4 | A = [r3 + Imm] |
| `OpLoadIncoming` | `A=4 Imm=4096` | 12 | Large offsets are built in r19 |
| `OpSpill` | `A=4 Slot=0` | 4 | `st.d A, (Slot+1)*8(r3)` |
| `OpSpill` | `A=4 Slot=255` | 12 | Offset >= 2048: address built in r19 |
| `OpSpill` | `V=A:8 Slot=0` | rejected | GP only; use OpFloatSpill or OpVectorSpill |
| `OpReload` | `A=4 Slot=0` | 4 | `ld.d` from the slot |
| `OpReload` | `A=4 Slot=1000` | 16 | |
| `OpSaveCFloat` | `A=24 Slot=0` | 4 | `fst.d fA` to the slot; A is the raw FP ordinal 0..31, not a VectorRegister |
| `OpSaveCFloat` | `V=A:24 Slot=0` | rejected | |
| `OpRestoreCFloat` | `A=24 Slot=0` | 4 | `fld.d fA` |
| `OpRestoreCFloat` | `A=24 Slot=255` | 12 | |
| `OpSaveCVector` | `V=A:24 Slot=0` | unsupported | Only the low 64 bits of f24..f31 are C callee-saved |
| `OpRestoreCVector` | `V=A:24 Slot=0` | unsupported | |

## Branches

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpJump` | `Imm=0x100` | 4 | `b`: ±128 MiB, 4-byte aligned |
| `OpJump` | `Imm=0x7fffffc` | 4 | |
| `OpJump` | `Imm=0x8000000` | rejected | Out of `b` range without Long |
| `OpJump` | `Imm=0x8000000 Long` | 8 | `pcaddu12i` r19 + `jirl`: ±2 GiB |
| `OpJump` | `Imm=0x102` | rejected | Misaligned target |
| `OpJumpIf` | `Imm=0x100 Cond=Less` | 4 | One `beq`/`bne`/`blt`/`bge`/`bltu`/`bgeu` on the last compared pair: ±128 KiB |
| `OpJumpIf` | `Imm=0x1fffc Cond=Above` | 4 | Greater, LessEqual, Above and BelowEqual swap the operands |
| `OpJumpIf` | `Imm=0x20000 Cond=Equal` | rejected | Out of 18-bit range without Long |
| `OpJumpIf` | `Imm=0x20000 Cond=Equal Long` | 8 | Inverted branch over `b` (±128 MiB) |
| `OpJumpIf` | `Imm=0x100 Cond=NotEqual Long Far` | 12 | Inverted branch over `pcaddu12i` + `jirl` |
| `OpJumpIf` | `Imm=0x100 Cond=Equal Far` | 4 | Far without Long is ignored |
| `OpJumpIf` | `Imm=0x100 Cond=Sign` | rejected | Only the ten integer conditions Equal..Above |
| `OpJumpIf` | `Imm=0x100 Cond=Carry` | rejected | |
| `OpBranchZero` | `A=4 Bits=64 Imm=0x100 Cond=Equal` | 4 | `beqz`/`bnez`: ±4 MiB; the whole 64-bit register is tested |
| `OpBranchZero` | `A=4 Bits=32 Imm=0x3ffffc Cond=NotEqual` | 4 | Bits is 0, 8, 16, 32 or 64 but does not change the test |
| `OpBranchZero` | `A=4 Bits=64 Imm=0x400000 Cond=Equal` | rejected | Out of 23-bit range without Long |
| `OpBranchZero` | `A=4 Bits=64 Imm=0x400000 Cond=Equal Long` | 8 | |
| `OpBranchZero` | `A=4 Bits=64 Imm=0x100 Cond=Equal Long Far` | 12 | |
| `OpBranchZero` | `A=4 Bits=64 Imm=0x100 Cond=Less` | rejected | Cond is Equal or NotEqual |
| `OpBranchBit` | `A=4 Bits=64 Disp=3 Imm=0x100 Cond=Equal` | unsupported | Test the bit with OpBinaryImmediate OpAnd, then use OpBranchZero |
| `OpLoop` | `Imm=0x100` | unsupported | No counter register; FuseLoops leaves loops as compare and branch |
| `OpSetCount` | `A=4` | unsupported | |
| `OpJumpTable` | `A=4 B=5 Table=64,128,192` | 52 | Bounded switch: `li r20, n` (n = len(Table)-1), a branchless clamp (`sltu b, a, r20`; `maskeqz r19, a, b`; `masknez b, r20, b`; `or b, b, r19`) so entry n is the default, `pcaddi r19, 5`; `alsl.d b, b, r19, 2`; `ld.w b, b, 0`; `add.d b, b, r19`; `jirl r0, b, 0`, then one 32-bit table-relative entry per target. Writes B, r19 and r20; A is preserved. Targets are 4-byte aligned. |
| `OpJumpTable` | `A=4 B=19 Table=64,128` | rejected | r19 and r20 are the sequence's temporaries; r0 is no operand. |
| `OpJumpTable` | `A=4 B=5 Table=64,130` | rejected | Targets must be 4-byte aligned. |
| `OpJumpTable` | `A=4 B=5 Table=64` | rejected | The table needs a case and the default. |

## Scalar floating point

FP operands are `VectorRegister(n)` (f0..f31). Most FP forms also accept a GP
register for an operand that holds the raw bit pattern. A GP source is moved
into f0/f1/f2 (`movgr2fr`). A GP destination gets the result through
`movfr2gr`. A 32-bit GP result is zero-extended with `bstrpick.d`. Bits is 32
or 64.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpFloatMove` | `V=A:8,B:9 Bits=64` | 4 | `fmov.d` |
| `OpFloatMove` | `V=A:8,B:8 Bits=64` | 0 | |
| `OpFloatMove` | `V=A:8 B=4 Bits=64` | 4 | GP to FP: `movgr2fr.d` |
| `OpFloatMove` | `A=4 V=B:8 Bits=64` | 4 | FP to GP: `movfr2gr.d` |
| `OpFloatMove` | `A=4 V=B:8 Bits=32` | 8 | `movfr2gr.s` + zero-extension |
| `OpFloatMove` | `A=4 B=5 Bits=64` | rejected | At least one operand must be an FP register |
| `OpFloatMove` | `V=A:8,B:9 Bits=16` | rejected | Bits is 32 or 64 |
| `OpFloatBinary` | `V=A:8,B:9,C:10 Bits=64 Arith=OpFAdd` | 4 | Arith is OpFAdd, OpFSub, OpFMul or OpFDiv |
| `OpFloatBinary` | `V=A:8,B:9,C:10 Bits=32 Arith=OpFDiv` | 4 | |
| `OpFloatBinary` | `A=4 B=5 C=6 Bits=64 Arith=OpFMul` | 16 | Two GP inputs and a GP output |
| `OpFloatBinary` | `A=4 B=5 C=6 Bits=32 Arith=OpFSub` | 20 | 32-bit GP output adds the zero-extension |
| `OpFloatBinary` | `V=A:8,B:9,C:10 Bits=64 Arith=OpAdd` | rejected | |
| `OpFloatFMA` | `V=A:8,B:9,C:10,D:11 Bits=64` | 4 | A = B*C + D (`fmadd.d`) |
| `OpFloatFMA` | `A=4 B=5 C=6 D=7 Bits=32` | 24 | GP operands; uses f0..f2 |
| `OpFloatFMA` | `V=A:8,B:9,C:10,D:11 Bits=0` | rejected | |
| `OpFloatUnary` | `V=A:8,B:9 Bits=64 Arith=OpFSqrt` | 4 | `fsqrt` |
| `OpFloatUnary` | `V=A:8,B:9 Bits=32 Arith=OpFConvert` | 4 | Bits is the destination width: `fcvt.s.d` |
| `OpFloatUnary` | `V=A:8 B=5 Bits=64 Arith=OpFConvert` | 8 | `fcvt.d.s` from a 32-bit GP pattern |
| `OpFloatUnary` | `V=A:8,B:9 Bits=64 Arith=OpFAdd` | rejected | Arith is OpFSqrt or OpFConvert |
| `OpFloatBits` | `V=A:8,B:9 Bits=64 Arith=OpXor` | 4 | OpXor is `fneg`, OpAnd is `fabs` |
| `OpFloatBits` | `A=4 B=5 Bits=32 Arith=OpAnd` | 16 | GP in and out |
| `OpFloatBits` | `V=A:8,B:9 Bits=64 Arith=OpOr` | rejected | |
| `OpIntToFloat` | `V=A:8 B=5 Bits=64 Imm=32 Arith=OpFFromSigned` | 8 | `movgr2fr.w` + `ffint.d.w` into A; Bits is the float width, Imm the integer width |
| `OpIntToFloat` | `V=A:8 B=5 Bits=32 Imm=64 Arith=OpFFromSigned` | 8 | `movgr2fr.d` + `ffint.s.l` |
| `OpIntToFloat` | `V=A:8 B=5 Bits=32 Imm=32 Arith=OpFFromUnsigned` | 8 | A zero-extended 32-bit source converts as signed 64-bit |
| `OpIntToFloat` | `V=A:8 B=5 Bits=64 Imm=64 Arith=OpFFromUnsigned` | 36 | No unsigned `ffint`: bit 63 set halves B with a sticky bit in r19/r20, converts, then `fadd` doubles; two internal `bge` |
| `OpIntToFloat` | `A=4 B=5 Bits=64 Imm=64 Arith=OpFFromSigned` | rejected | A is an FP register and B a GP register |
| `OpIntToFloat` | `V=A:8 B=5 Bits=64 Imm=16 Arith=OpFFromSigned` | rejected | Imm is 32 or 64 |
| `OpFloatToInt` | `A=4 V=B:9 Bits=64 Disp=64 Imm=1 Arith=OpFToSigned` | 8 | ConversionNative (Imm=1): `ftintrz.l.d` into f0 + `movfr2gr.d`; Disp is the float width. f0 is the only FP register written |
| `OpFloatToInt` | `A=4 V=B:9 Bits=32 Disp=32 Imm=1 Arith=OpFToUnsigned` | 12 | Unsigned 32-bit converts as signed 64-bit (`ftintrz.l.s`); a 32-bit result is zero-extended |
| `OpFloatToInt` | `A=4 V=B:9 Bits=64 Disp=64 Imm=1 Arith=OpFToUnsigned` | 32 | Unsigned 64-bit: at or above 2^63 the significand is shifted into place in the GP bank (r19, r20) |
| `OpFloatToInt` | `A=4 V=B:9 Bits=64 Disp=64 Imm=0 Arith=OpFToSigned` | 60 | ConversionSaturate (Imm=0): the exponent decoded in r19/r20 takes `ftintrz` only in range; outside it a GP clamp (r12 detects NaN, which gives 0) |
| `OpFloatToInt` | `A=4 V=B:9 Bits=32 Disp=64 Imm=0 Arith=OpFToSigned` | 64 | |
| `OpFloatToInt` | `A=4 V=B:9 Bits=32 Disp=32 Imm=0 Arith=OpFToUnsigned` | 60 | Negative values give 0 |
| `OpFloatToInt` | `A=4 V=B:9 Bits=64 Disp=32 Imm=0 Arith=OpFToUnsigned` | 72 | |
| `OpFloatToInt` | `A=4 B=5 Bits=64 Disp=64 Imm=0 Arith=OpFToSigned` | rejected | B is an FP register |
| `OpFloatToInt` | `A=4 V=B:9 Bits=64 Disp=64 Imm=2 Arith=OpFToSigned` | rejected | Imm is an op.ConversionSemantics |
| `OpFloatCompare` | `A=4 V=B:9,C:10 Bits=64 Imm=0` | 8 | `fcmp.ceq.d fcc0, f9, f10` + `movcf2gr r4, fcc0`: A = 1 when B and C satisfy the op.FloatCondition in Imm. Quiet (C*) conditions only; fcc0 is clobbered |
| `OpFloatCompare` | `A=4 V=B:9,C:10 Bits=32 Imm=6` | 8 | FloatGreater is `fcmp.clt.s fcc0, f10, f9`: Greater, GreaterEqual, UnorderedGreater and UnorderedGreaterEqual swap the operands |
| `OpFloatCompare` | `A=4 V=B:9,C:10 Bits=64 Imm=13` | 8 | FloatUnorderedEqual is `fcmp.cueq.d`; NotEqual `cune`, Ordered `cor`, Unordered `cun`, OrderedNotEqual `cne` |
| `OpFloatCompare` | `A=4 V=B:9,C:10 Bits=16 Imm=0` | rejected | Bits is 32 or 64 |
| `OpFloatCompare` | `A=4 V=B:9,C:10 Bits=64 Imm=14` | rejected | Imm is an op.FloatCondition |
| `OpFloatCompare` | `A=4 B=5 C=6 Bits=64 Imm=0` | rejected | B and C are FP registers |
| `OpFloatCompare` | `V=A:8,B:9,C:10 Bits=64 Imm=0` | rejected | A is a GP register |
| `OpFloatSpill` | `V=A:8 Slot=0 Bits=64` | 4 | `fst.d` to `(Slot+1)*8(r3)`; Bits is 0, 32 or 64 (0 means 64) |
| `OpFloatSpill` | `V=A:8 Slot=0 Bits=32` | 4 | `fst.s` |
| `OpFloatSpill` | `A=4 Slot=0 Bits=64` | rejected | FP register only |
| `OpFloatReload` | `V=A:8 Slot=255 Bits=64` | 12 | Offset >= 2048 builds the address in r19 |
| `OpFloatReload` | `V=A:8 Slot=0 Bits=128` | rejected | |
| `OpLiteralLoad` | `V=A:8 Bits=64 Disp=0x100` | 8 | Always `pcaddu12i` r19 + `fld.d`; Disp is the pool PC (±2 GiB) |
| `OpLiteralLoad` | `V=A:8 Bits=32 Disp=0x100` | 8 | `fld.s` |
| `OpLiteralLoad` | `V=A:8 Bits=128 Disp=0x100` | 8 | `vld` |
| `OpLiteralLoad` | `V=A:8 Bits=64 Disp=0x80000000` | rejected | Out of PCADDU12I reach |
| `OpLiteralLoad` | `A=4 Bits=64 Disp=0x100` | rejected | FP/vector destination only |
| `OpLiteralData` | `Data=0000803f` | 4 | Raw pool bytes, emitted as is |
| `OpLiteralData` | `Data=000000000000f03f0000000000000040` | 16 | |
| `OpLiteralData` | `Data=0000` | rejected | Length must be a multiple of 4 |

## LSX vectors

Vector registers are `VectorRegister(0..31)` (vr0..vr31). They alias f0..f31.
Every `OpVector` form, including loads, stores, zeroing and mask extraction,
needs Bits (the lane width) to be 8, 16, 32 or 64. Every vector form operates on
all 128 bits, so `Vec` is not needed.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpVector` | `V=A:8 B=4 Disp=16 Bits=64 Arith=OpVLoad` | 4 | A = [B + Disp] (`vld`); signed 12-bit Disp is folded |
| `OpVector` | `V=A:8 B=4 Disp=4096 Bits=64 Arith=OpVLoad` | 12 | Larger Disp builds the address in r19 |
| `OpVector` | `V=A:8 B=4 C=5 Bits=64 Indexed Arith=OpVLoad` | 4 | `vldx`: unscaled GP index C |
| `OpVector` | `V=A:8 B=4 C=5 Disp=16 Bits=64 Indexed Arith=OpVLoad` | rejected | Indexed requires Disp=0 |
| `OpVector` | `A=4 V=B:8 Bits=64 Arith=OpVStore` | 4 | [A + Disp] = B (`vst`); Indexed gives `vstx` |
| `OpVector` | `V=A:8 B=4 Arith=OpVLoad` | rejected | Bits must be 8, 16, 32 or 64 even for whole-register forms |
| `OpVector` | `V=A:8 Bits=64 Arith=OpVZero` | 4 | `vxor.v` |
| `OpVector` | `V=A:8 B=4 Bits=32 Arith=OpVSplat` | 4 | `vreplgr2vr` from GP B |
| `OpVector` | `V=A:8 B=4 Arith=OpVSplat` | rejected | Bits required |
| `OpVector` | `A=4 V=B:8 Bits=32 Imm=3 Arith=OpVExtract` | 4 | Zero-extending `vpickve2gr`; Imm < 128/Bits |
| `OpVector` | `A=4 V=B:8 Bits=32 Imm=4 Arith=OpVExtract` | rejected | Lane out of range |
| `OpVector` | `V=A:8,B:8 C=4 Bits=16 Imm=7 Arith=OpVInsert` | 4 | `vinsgr2vr` from GP C |
| `OpVector` | `V=A:8,B:9 C=4 Bits=64 Imm=1 Arith=OpVInsertZero` | 8 | A != B copies B first |
| `OpVector` | `V=A:8,B:9,C:10 Bits=32 Arith=OpAdd` | 4 | Lane-wise OpAdd, OpSub or OpMul |
| `OpVector` | `V=A:8,B:9,C:10 Bits=64 Arith=OpMul` | 4 | |
| `OpVector` | `V=A:8,B:9,C:10 Bits=8 Arith=OpXor` | 4 | OpAnd/OpOr/OpXor (`.v` forms); Bits is still required |
| `OpVector` | `V=A:8,B:9,C:10 Arith=OpAnd` | rejected | |
| `OpVector` | `V=A:8,B:9,C:10 Bits=8 Arith=OpVCompare` | 4 | Only byte equality (`vseq.b`; Imm = BranchEqual = 0) |
| `OpVector` | `V=A:8,B:9,C:10 Bits=16 Arith=OpVCompare` | rejected | |
| `OpVector` | `A=4 V=B:8 Bits=8 Arith=OpVMaskBits` | 8 | Byte sign mask to GP (`vmskltz.b` vr0 + `vpickve2gr.wu`) |
| `OpVector` | `V=A:8,B:9 Bits=32 Imm=5 Arith=OpShiftLeft` | 4 | Immediate lane shift/rotate |
| `OpVector` | `V=A:8,B:9 Bits=32 Imm=40 Arith=OpShiftRight` | 4 | Count >= Bits: logical shifts zero the result; arithmetic shifts use Bits-1 |
| `OpVector` | `V=A:8,B:9 Bits=16 Imm=3 Arith=OpRotateLeft` | 4 | Rotate count is taken modulo Bits; left becomes `vrotri` |
| `OpVector` | `V=A:8,B:9,C:10 Bits=32 Arith=OpShiftLeftBy` | 20 | Per-lane vector counts; counts >= Bits are masked with vr1 |
| `OpVector` | `V=A:8,B:9 C=4 Bits=32 Arith=OpShiftRightBy` | 24 | Scalar GP count, splatted into vr0 |
| `OpVector` | `V=A:8,B:9,C:10 Bits=16 Arith=OpShiftRightArithmeticBy` | 32 | Saturates to the sign through vr2 |
| `OpVector` | `V=A:8,B:9,C:10 Bits=64 Arith=OpRotateRightBy` | 4 | `vrotr` |
| `OpVector` | `V=A:8,B:9 C=4 Bits=8 Arith=OpRotateLeftBy` | 12 | Splat + `vneg` + `vrotr` |
| `OpVector` | `V=A:8,B:9,C:10 Bits=64 Arith=OpVCarrylessMul` | rejected | No carry-less multiply in LSX |
| `OpVector` | `V=A:8,B:8,C:9 Bits=32 Imm=0x03020100 Arith=OpVShuffle` | 0 | Lane i of A is lane idx_i of B‖C: idx_i is byte i of Imm, and byte i-8 of Disp from lane 8 on. B's identity into A emits nothing |
| `OpVector` | `V=A:8,B:9,C:10 Bits=32 Imm=0x07060504 Arith=OpVShuffle` | 4 | C's identity: `vor.v` |
| `OpVector` | `V=A:8,B:9,C:10 Bits=8 Imm=0x0505050505050505 Disp=0x0505050505050505 Arith=OpVShuffle` | 4 | A broadcast is `vreplvei.b/h/w/d` of B or C |
| `OpVector` | `V=A:8,B:9,C:10 Bits=16 Imm=0x0909090909090909 Arith=OpVShuffle` | 4 | `vreplvei.h A, C, 1` |
| `OpVector` | `V=A:8,B:9,C:10 Bits=64 Imm=0 Arith=OpVShuffle` | 4 | `vreplvei.d` |
| `OpVector` | `V=A:8,B:9,C:10 Bits=16 Imm=0x0f070e060d050c04 Arith=OpVShuffle` | 4 | Interleaves (`vilvl`/`vilvh`) and even/odd picks (`vpickev`/`vpickod`) of B and C in either order, or of one input |
| `OpVector` | `V=A:8,B:9,C:10 Bits=32 Imm=0x06040200 Arith=OpVShuffle` | 4 | `vpickev.w A, C, B` |
| `OpVector` | `V=A:8,B:9,C:10 Bits=8 Imm=0x0f0d0b0907050301 Disp=0x1f1d1b1917151311 Arith=OpVShuffle` | 4 | `vpickod.b` |
| `OpVector` | `V=A:8,B:9,C:10 Bits=32 Imm=0x00010203 Arith=OpVShuffle` | 4 | A one-input permutation within groups of four lanes is `vshuf4i.b/h/w`; a pattern also matches at a finer lane width |
| `OpVector` | `V=A:8,B:9,C:10 Bits=8 Imm=0x0607040502030001 Disp=0x0e0f0c0d0a0b0809 Arith=OpVShuffle` | 4 | `vshuf4i.b A, B, 0xb1` |
| `OpVector` | `V=A:8,B:9,C:10 Bits=64 Imm=0x0300 Arith=OpVShuffle` | 8 | Any other 64-bit pattern: `vor.v A, B` and `vshuf4i.d A, C` |
| `OpVector` | `V=A:10,B:9,C:10 Bits=64 Imm=0x0300 Arith=OpVShuffle` | 4 | With A already B or C, only `vshuf4i.d` |
| `OpVector` | `V=A:8,B:9,C:10 Bits=8 Imm=0x18191a1b1c1d1e1f Disp=0x1011121314151617 Arith=OpVShuffle` | 44 | Any other pattern: the byte index table built in vr0 through r19 (`vinsgr2vr.d`), then `vshuf.b A, C, B, vr0` |
| `OpVector` | `V=A:8,B:9,C:10 Bits=8 Imm=0x0010011102120313 Disp=0x0010011102120313 Arith=OpVShuffle` | 24 | Equal table halves splat with `vreplgr2vr.d` |
| `OpVector` | `V=A:8,B:9,C:10 Bits=32 Imm=0x08 Arith=OpVShuffle` | rejected | Each index must be below twice the lane count |
| `OpVector` | `V=A:8,B:9,C:10 Bits=32 Imm=0x0100000000 Arith=OpVShuffle` | rejected | Index bytes beyond the last lane must be zero |
| `OpVector` | `V=A:8,B:9,C:10 Bits=64 Disp=1 Arith=OpVShuffle` | rejected | Disp holds lanes 8 to 15 only |
| `OpVector` | `V=A:8,B:9 Bits=16 Imm=0 Arith=OpVExtendSigned` | 4 | Bits is the result lane width. Imm 0 widens B's low half: `vsllwil.h.b A, B, 0` |
| `OpVector` | `V=A:8,B:9 Bits=32 Imm=0 Arith=OpVExtendSigned` | 4 | `vsllwil.w.h` |
| `OpVector` | `V=A:8,B:9 Bits=64 Imm=0 Arith=OpVExtendSigned` | 4 | `vsllwil.d.w` |
| `OpVector` | `V=A:8,B:9 Bits=16 Imm=1 Arith=OpVExtendSigned` | 4 | Imm 1 widens the high half: `vexth.h.b` |
| `OpVector` | `V=A:8,B:9 Bits=32 Imm=1 Arith=OpVExtendSigned` | 4 | `vexth.w.h` |
| `OpVector` | `V=A:8,B:8 Bits=64 Imm=1 Arith=OpVExtendSigned` | 4 | `vexth.d.w`; A may be B |
| `OpVector` | `V=A:8,B:9 Bits=16 Imm=0 Arith=OpVExtendUnsigned` | 4 | `vsllwil.hu.bu A, B, 0` |
| `OpVector` | `V=A:8,B:9 Bits=32 Imm=0 Arith=OpVExtendUnsigned` | 4 | `vsllwil.wu.hu` |
| `OpVector` | `V=A:8,B:9 Bits=64 Imm=0 Arith=OpVExtendUnsigned` | 4 | `vsllwil.du.wu` |
| `OpVector` | `V=A:8,B:9 Bits=16 Imm=1 Arith=OpVExtendUnsigned` | 4 | `vexth.hu.bu` |
| `OpVector` | `V=A:8,B:9 Bits=32 Imm=1 Arith=OpVExtendUnsigned` | 4 | `vexth.wu.hu` |
| `OpVector` | `V=A:8,B:9 Bits=64 Imm=1 Arith=OpVExtendUnsigned` | 4 | `vexth.du.wu` |
| `OpVector` | `V=A:8,B:9 Bits=8 Arith=OpVExtendUnsigned` | rejected | The result lanes are 16, 32 or 64 bits |
| `OpVector` | `V=A:8,B:9 Bits=32 Imm=2 Arith=OpVExtendSigned` | rejected | Imm selects the low (0) or high (1) half |
| `OpVector` | `V=A:32,B:9,C:10 Bits=32 Arith=OpAdd` | rejected | Vector numbers are 0..31 |
| `OpVectorMove` | `V=A:8,B:9` | 4 | `vor.v` |
| `OpVectorMove` | `V=A:8,B:8` | 0 | |
| `OpVectorMove` | `A=4 V=B:8` | rejected | Vector registers only |
| `OpVectorSpill` | `V=A:8 Slot=0` | 4 | `vst` to `(Slot+1)*8(r3)` (16 bytes; slots must be sized for it) |
| `OpVectorSpill` | `V=A:8 Slot=300` | 12 | |
| `OpVectorReload` | `V=A:8 Slot=0` | 4 | `vld` |
| `OpVectorReload` | `A=4 Slot=0` | rejected | |

## Barriers

`OpBarrier` reads and writes no registers or flags. `Barrier` names the
guarantee; `Policy.Barrier` reports whether the target's instruction rests on
the instruction set or also on host configuration.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpBarrier` | `Barrier=Speculation` | unsupported | LoongArch defines no speculation barrier. |
| `OpBarrier` | `Barrier=Memory` | 4 | `dbar 0` |
| `OpBarrier` | `Barrier=Instruction` | 4 | `ibar 0` |
| `OpBarrier` | — | rejected | Barrier must name a guarantee. |

## Stack areas

`OpSlotAddress` writes to GP register A the address of spill slot Slot, as
`OpSpill` addresses it: (Slot+1)*8(r3), above the Go homes when there are
any, or Slot*8(r23) without a platform stack. A far offset is built in A and
added to the base, so no scratch register is written.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpSlotAddress` | `A=4 Slot=0` | 4 | `addi.d A, r3, (Slot+1)*8` |
| `OpSlotAddress` | `A=4 Slot=254` | 4 | largest slot in the 12-bit range |
| `OpSlotAddress` | `A=4 Slot=255` | 8 | `ori A, r0, 2048`, then `add.d A, r3, A` |
| `OpSlotAddress` | `A=4 Slot=1000` | 12 | wider offsets take `lu12i.w`/`ori` |
| `OpSlotAddress` | `A=3 Slot=0` | rejected | A must not be the stack pointer |
| `OpSlotAddress` | `A=0 Slot=0` | rejected | A must not be r0 |
| `OpSlotAddress` | `V=A:8 Slot=0` | rejected | GP only |

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
| `OpPrefetch` | `A=4 Imm=0` | 4 | `preld 0, r4, 0`: hint 0 loads into L1. |
| `OpPrefetch` | `A=4 Imm=1` | 4 | `preld 8, r4, 0`: hint 8 stores into L1. |
| `OpPrefetch` | `A=4 Imm=2` | rejected | Imm must be an `op.PrefetchKind`. |
| `OpPrefetch` | `V=A:1 Imm=0` | rejected | A must be a GP register. |


## Tail calls

`OpTailCall` ends a function with a C tail call. The arguments are in place
and the target is in the call-target register, as for `OpCallC`. It leaves
a frame of Imm bytes, as `OpFrameLeave` does (none when Imm is zero), and
jumps to the target, so the callee returns to this function's caller.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpTailCall` | `Imm=0` | 4 | `jirl r0, r20, 0`. |
| `OpTailCall` | `Imm=32` | 12 | `ld.d r1, r3, 0; addi.d r3, r3, 32; jirl r0, r20, 0`. |
| `OpTailCall` | `Imm=2097152` | rejected | Frame adjustments are bounded to 1 MiB. |

## Atomics

`OpAtomic` performs the sequentially consistent atomic `Arith`
(`OpAtomicLoad` … `OpAtomicCompareSwap`) at width `Bits` on the naturally
aligned address in B. A receives the old value, zero-extended; C is the
operand (the expected value for compare-and-swap) and D the replacement. `Imm`
is the `op.MemoryOrder` (only 0, sequential). A store reads no A and a load
no C. A must differ from every input and must not be r0; `am*` requires it.
Loads and stores sit between two `dbar 0` barriers, and naturally aligned
bytes and halfwords use `ld.bu`/`ld.hu`/`st.b`/`st.h` directly. 32- and 64-bit
read-modify-writes are one `am<op>_db.{w,d} A, C, B`, which orders like a full
barrier. Compare-and-swap is an `ll`/`sc` loop between `dbar 0` barriers.
`ll.w` and `am*.w` sign-extend, so 32-bit results are zero-extended
(`bstrpick.d`), and the 32-bit compare-and-swap compares with a sign-extended
copy of C in r20. Byte and halfword read-modify-writes work on the aligned
word: And, Or and Xor with one `amand_db.w`/`amor_db.w`/`amxor_db.w` whose
other bytes are the identity; Add, Swap and compare-and-swap with a masked
`ll.w`/`sc.w` loop of at most six instructions, with setup and result
extraction outside it.

Besides r12, r19 and r20, the masked loops write fixed allocatable
temporaries, which no operand may name: 8/16-bit Add and Swap use r13, and
8/16-bit compare-and-swap r13 and r14. `ImplicitWrites` reports every
register an expansion writes besides A from `Arith` and `Bits` alone.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpAtomic` | `A=8 B=9 Bits=8 Arith=OpAtomicLoad` | 12 | `dbar 0; ld.bu A, B, 0; dbar 0`. |
| `OpAtomic` | `A=8 B=9 Bits=16 Arith=OpAtomicLoad` | 12 | `ld.hu`. |
| `OpAtomic` | `A=8 B=9 Bits=32 Arith=OpAtomicLoad` | 12 | `ld.wu`. |
| `OpAtomic` | `A=8 B=9 Bits=64 Arith=OpAtomicLoad` | 12 | `ld.d`. |
| `OpAtomic` | `B=9 C=10 Bits=8 Arith=OpAtomicStore` | 12 | `dbar 0; st.b C, B, 0; dbar 0`. |
| `OpAtomic` | `B=9 C=10 Bits=16 Arith=OpAtomicStore` | 12 | `st.h`. |
| `OpAtomic` | `B=9 C=10 Bits=32 Arith=OpAtomicStore` | 12 | `st.w`. |
| `OpAtomic` | `B=9 C=10 Bits=64 Arith=OpAtomicStore` | 12 | `st.d`. |
| `OpAtomic` | `A=8 B=9 C=10 Bits=8 Arith=OpAtomicAdd` | 80 | Masked `ll.w`/`sc.w` loop. Writes r12, r13, r19, r20. |
| `OpAtomic` | `A=8 B=9 C=10 Bits=16 Arith=OpAtomicAdd` | 84 | As 8-bit; the halfword mask takes `lu12i.w`/`addi.w`. |
| `OpAtomic` | `A=8 B=9 C=10 Bits=32 Arith=OpAtomicAdd` | 8 | `amadd_db.w`, then `bstrpick.d A, A, 31, 0`. |
| `OpAtomic` | `A=8 B=9 C=10 Bits=64 Arith=OpAtomicAdd` | 4 | `amadd_db.d A, C, B`. |
| `OpAtomic` | `A=8 B=9 C=10 Bits=8 Arith=OpAtomicAnd` | 48 | `amand_db.w` on the aligned word with `C << shift \| ^(0xff << shift)`. Writes r12, r19, r20. |
| `OpAtomic` | `A=8 B=9 C=10 Bits=16 Arith=OpAtomicAnd` | 52 | As 8-bit. |
| `OpAtomic` | `A=8 B=9 C=10 Bits=32 Arith=OpAtomicAnd` | 8 | `amand_db.w`, then zero-extension. |
| `OpAtomic` | `A=8 B=9 C=10 Bits=64 Arith=OpAtomicAnd` | 4 | `amand_db.d`. |
| `OpAtomic` | `A=8 B=9 C=10 Bits=8 Arith=OpAtomicOr` | 32 | `amor_db.w` on the aligned word with `C << shift`. Writes r12, r19, r20. |
| `OpAtomic` | `A=8 B=9 C=10 Bits=16 Arith=OpAtomicOr` | 32 | As 8-bit. |
| `OpAtomic` | `A=8 B=9 C=10 Bits=32 Arith=OpAtomicOr` | 8 | `amor_db.w`. |
| `OpAtomic` | `A=8 B=9 C=10 Bits=64 Arith=OpAtomicOr` | 4 | `amor_db.d`. |
| `OpAtomic` | `A=8 B=9 C=10 Bits=8 Arith=OpAtomicXor` | 32 | `amxor_db.w` on the aligned word. Writes r12, r19, r20. |
| `OpAtomic` | `A=8 B=9 C=10 Bits=16 Arith=OpAtomicXor` | 32 | As 8-bit. |
| `OpAtomic` | `A=8 B=9 C=10 Bits=32 Arith=OpAtomicXor` | 8 | `amxor_db.w`. |
| `OpAtomic` | `A=8 B=9 C=10 Bits=64 Arith=OpAtomicXor` | 4 | `amxor_db.d`. |
| `OpAtomic` | `A=8 B=9 C=10 Bits=8 Arith=OpAtomicSwap` | 76 | Masked loop. Writes r12, r13, r19, r20. |
| `OpAtomic` | `A=8 B=9 C=10 Bits=16 Arith=OpAtomicSwap` | 80 | As 8-bit. |
| `OpAtomic` | `A=8 B=9 C=10 Bits=32 Arith=OpAtomicSwap` | 8 | `amswap_db.w`. |
| `OpAtomic` | `A=8 B=9 C=10 Bits=64 Arith=OpAtomicSwap` | 4 | `amswap_db.d`. |
| `OpAtomic` | `A=8 B=9 C=10 D=11 Bits=8 Arith=OpAtomicCompareSwap` | 84 | Masked loop comparing `old & mask` with `C << shift` and storing `old ^ ((C ^ D) << shift)`. Writes r12, r13, r14, r19, r20. |
| `OpAtomic` | `A=8 B=9 C=10 D=11 Bits=16 Arith=OpAtomicCompareSwap` | 88 | As 8-bit. |
| `OpAtomic` | `A=8 B=9 C=10 D=11 Bits=32 Arith=OpAtomicCompareSwap` | 36 | `addi.w r20, C, 0; dbar 0; ll.w r12; bne r12, r20; or r19, D, r0; sc.w r19; beqz r19; dbar 0; bstrpick.d A, r12, 31, 0`. |
| `OpAtomic` | `A=8 B=9 C=10 D=11 Bits=64 Arith=OpAtomicCompareSwap` | 32 | As 32-bit with `ll.d`/`sc.d`, comparing with C directly. Writes r12, r19. |
| `OpAtomic` | `A=8 B=9 C=10 Bits=12 Arith=OpAtomicAdd` | rejected | Bits must be 8, 16, 32 or 64. |
| `OpAtomic` | `A=8 B=9 C=10 Bits=32 Imm=1 Arith=OpAtomicAdd` | rejected | Imm must be a defined `op.MemoryOrder`. |
| `OpAtomic` | `A=8 B=9 C=10 Bits=32 Arith=OpAdd` | rejected | `Arith` must be an atomic operation. |
| `OpAtomic` | `V=B:1 A=8 C=10 Bits=32 Arith=OpAtomicAdd` | rejected | The address must be a GP register. |
| `OpAtomic` | `A=10 B=9 C=10 Bits=64 Arith=OpAtomicAdd` | rejected | A must differ from the inputs. |
| `OpAtomic` | `A=0 B=9 C=10 Bits=32 Arith=OpAtomicAdd` | rejected | A must not be r0. |
| `OpAtomic` | `A=8 B=12 C=10 Bits=32 Arith=OpAtomicAdd` | rejected | r12, r19 and r20 are scratch. |
| `OpAtomic` | `A=8 B=13 C=10 Bits=8 Arith=OpAtomicAdd` | rejected | An input of a masked loop cannot be one of its temporaries. |
| `OpAtomic` | `A=8 B=9 C=10 D=14 Bits=16 Arith=OpAtomicCompareSwap` | rejected | r14 is a temporary of the 8/16-bit compare-and-swap. |
| `OpAtomic` | `A=8 B=9 Bits=64 Arith=OpAtomicLoad BigEndian` | rejected | Atomics use native byte order. |
