# amd64 instruction forms

This file lists the `machine.Instruction` forms that the amd64 byte assembler
accepts. For each form it gives the operand constraints and the encoded size.
`backend/target/forms_test.go` assembles every example row.

The table makes these assumptions:

- The assembler is `NewByteAssembler(abi.StackPlatform, 0)`. Spill slots are
  addressed from RSP, and there are no Go argument homes.
- The CPU is the baseline x86-64 profile (SSE2), with no optional ISA
  extensions. With a `WithCPU` profile that has `bmi1`, `bmi2` or `lzcnt`,
  `OpShift` rotates, `OpShiftBy`, `OpMulHigh` and `OpCountZeros` use shorter
  forms. These rows do not cover those forms.
- Each example is assembled alone at PC 0. Branch targets and literal pool
  addresses are absolute PCs, given in `Imm` (branches) or `Disp` (literals).
- `OpCallC` follows the System V AMD64 C ABI. It sets AL to 0 for varargs and
  calls through R11. The byte assembler takes no ABI flavor. Argument and
  result registers come from `Policy`.

Registers are physical ordinals. `Policy.RegisterName` gives their names:
0=RAX, 1=RCX, 2=RDX, 3=RBX, 4=RSP, 5=RBP, 6=RSI, 7=RDI, and 8–15=R8–R15.
Vector and FP registers are `backend.VectorRegister(n)` for XMMn, n 0–15.
Examples write them as `V=A:n,B:m`. A GP operand of 16 or more, and a vector
number of 16 or more, is rejected. XMM0–XMM2 are expansion temporaries, and
allocator homes are XMM3–XMM14. RAX, RCX, RDX and R11 are implicit in some expansions,
as noted in the rows. A width of `Bits=0` means 64 bits wherever the
assembler accepts it. `PhysicalSize` is the authority for the size of any
form not listed here.

## Constants and moves

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpConst` | `A=0 Imm=3` | 5 | `MOV r32, imm32` for values up to 0xFFFFFFFF (zero-extends). |
| `OpConst` | `A=8 Imm=3` | 6 | R8–R15 add a REX prefix. |
| `OpConst` | `A=0 SImm=-1` | 7 | A negative value that fits a sign-extended imm32 uses `MOV r64, simm32`. |
| `OpConst` | `A=9 SImm=-2147483648` | 7 | Lowest sign-extended imm32. |
| `OpConst` | `A=0 Imm=0x123456789` | 10 | Any other value uses `MOV r64, imm64`. |
| `OpConst` | `A=16 Imm=0` | rejected | A must be a GP register (0–15). |
| `OpSymbol` | `A=0 Sym=memcpy` | 10 | `MOV r64, imm64` with a `RelocAMD64Immediate` relocation at the instruction start. Always 10 bytes. |
| `OpSymbol` | `A=12 Sym=memcpy` | 10 | Any GP destination. |
| `OpSymbol` | `A=16 Sym=memcpy` | rejected | A must be a GP register. |
| `OpMove` | `A=0 B=1` | 3 | 64-bit `MOV A, B`. Always has REX.W. |
| `OpMove` | `A=8 B=15` | 3 | Extended registers fit in the same REX byte. |
| `OpMove` | `A=3 B=3` | 0 | A self-move emits nothing. |
| `OpMove` | `V=A:3 B=0` | rejected | GP only. Use `OpFloatMove` or `OpVectorMove` for XMM registers. |

## Integer arithmetic

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpBinary` | `A=0 B=0 C=1 Bits=64 Arith=OpAdd` | 3 | Two-address form: `A = B op C`. Arith is OpAdd, OpSub, OpAnd, OpOr, OpXor or OpMul. |
| `OpBinary` | `A=0 B=7 C=6 Bits=64 Arith=OpAdd` | 6 | When A differs from B, a 3-byte `MOV A, B` comes first. A must not alias C unless A is B. |
| `OpBinary` | `A=0 B=0 C=1 Arith=OpSub` | 3 | Bits=0 is the 64-bit form. |
| `OpBinary` | `A=0 B=0 C=1 Bits=32 Arith=OpXor` | 3 | 32-bit form. The result is zero-extended. |
| `OpBinary` | `A=0 B=0 C=1 Bits=16 Arith=OpAnd` | 4 | The 16-bit form adds a 0x66 prefix. Bits above 16 keep their old value. |
| `OpBinary` | `A=0 B=0 C=1 Bits=8 Arith=OpOr` | 3 | 8-bit form, with a REX prefix so SPL–DIL are addressable. Bits above 8 keep their old value. |
| `OpBinary` | `A=0 B=0 C=1 Bits=64 Arith=OpMul` | 4 | `IMUL A, C` (0F AF). Bits 0, 8 and 64 use the 64-bit multiply. |
| `OpBinary` | `A=0 B=0 C=1 Bits=32 Arith=OpMul` | 4 | 32-bit IMUL. |
| `OpBinary` | `A=0 B=0 C=1 Bits=16 Arith=OpMul` | 5 | 16-bit IMUL with a 0x66 prefix. |
| `OpBinary` | `A=0 B=0 C=1 Bits=8 Arith=OpMul` | 4 | No 8-bit IMUL form exists, so this uses the 64-bit multiply. The low 8 bits are correct. |
| `OpBinary` | `A=0 B=0 C=1 Bits=12 Arith=OpAdd` | rejected | Bits must be 0, 8, 16, 32 or 64. |
| `OpBinary` | `A=0 B=0 C=1 Bits=64 Arith=OpShiftLeft` | rejected | Shifts use `OpShift` or `OpShiftBy`. |
| `OpBinary` | `A=0 B=7 C=6 Bits=64 Arith=OpAdd PreserveFlags` | 4 | `LEA A, [B + C]` leaves flags alone. Only OpAdd at 32 or 64 bits. RSP is never the index, and an RBP or R13 base is avoided when the other operand allows. |
| `OpBinary` | `A=0 B=5 C=13 Bits=32 Arith=OpAdd PreserveFlags` | 5 | 32-bit `LEA`, zero-extended. RBP and R13 together need a zero disp8. |
| `OpBinary` | `A=0 B=4 C=4 Bits=64 Arith=OpAdd PreserveFlags` | rejected | RSP cannot be both base and index. |
| `OpBinary` | `A=0 B=1 C=2 Bits=64 Arith=OpSub PreserveFlags` | rejected | No flag-preserving form for other operations. |
| `OpBinaryImmediate` | `A=0 B=0 Bits=64 Arith=OpAdd Imm=1` | 4 | `op A, simm8` (0x83) when the immediate fits a signed 8-bit value. Arith is OpAdd, OpSub, OpAnd, OpOr or OpXor. |
| `OpBinaryImmediate` | `A=0 B=1 Bits=64 Arith=OpAdd Imm=1` | 7 | When A differs from B, a 3-byte `MOV A, B` comes first. |
| `OpBinaryImmediate` | `A=0 B=0 Bits=64 Arith=OpAnd Imm=0x7fffffff` | 7 | `op A, imm32` (0x81). For 64 bits the immediate must fit a sign-extended imm32. |
| `OpBinaryImmediate` | `A=0 B=0 Bits=32 Arith=OpXor Imm=0xffffffff` | 7 | Below 64 bits, values up to 0xFFFFFFFF (or a sign-extended imm32) are accepted. The 32-bit result zero-extends. |
| `OpBinaryImmediate` | `A=0 B=0 Bits=8 Arith=OpSub SImm=-1` | 4 | Bits 8 and 16 use the 32-bit operation. Bits above the width are not normalized. |
| `OpBinaryImmediate` | `A=0 B=0 Bits=64 Arith=OpOr Imm=0x80000000` | rejected | A 64-bit immediate outside the signed imm32 range is rejected. |
| `OpBinaryImmediate` | `A=0 B=0 Arith=OpAdd Imm=1` | rejected | Bits must be 8, 16, 32 or 64. Bits=0 is not accepted here. |
| `OpBinaryImmediate` | `A=0 B=0 Bits=64 Arith=OpMul Imm=3` | rejected | OpMul has no immediate form. |
| `OpBinaryImmediate` | `A=0 B=1 Bits=64 Arith=OpAdd Imm=1 PreserveFlags` | 4 | `LEA A, [B + Imm]` leaves flags alone. OpAdd and OpSub only. |
| `OpBinaryImmediate` | `A=0 B=1 Bits=32 Arith=OpSub Imm=0x1000 PreserveFlags` | 6 | `LEA A32, [B - Imm]` with disp32. Bits 8, 16 and 32 use the 32-bit form, as the flag-writing form does. |
| `OpBinaryImmediate` | `A=0 B=1 Bits=64 Arith=OpSub SImm=-2147483648 PreserveFlags` | rejected | The negated immediate must fit a signed 32-bit displacement. |
| `OpInc` | `A=0 B=0` | 3 | 64-bit `INC A`. CF is preserved. |
| `OpInc` | `A=0 B=1` | 6 | When A differs from B, a 3-byte `MOV A, B` comes first. |
| `OpInc` | `A=16 B=16` | rejected | GP only. |
| `OpInc` | `A=0 B=1 PreserveFlags` | 4 | `LEA A, [B + 1]` leaves flags alone. |
| `OpInc` | `A=0 B=0 PreserveFlags` | 4 | Also valid when A is B, but one byte longer than `INC`. |
| `OpDec` | `A=8 B=8` | 3 | 64-bit `DEC A`. CF is preserved. |
| `OpDec` | `A=0 B=1` | 6 | When A differs from B, a 3-byte `MOV A, B` comes first. |
| `OpDec` | `A=1 B=12 PreserveFlags` | 5 | `LEA A, [B - 1]`. An RSP or R12 base needs a SIB byte. |
| `OpBinaryCarry` | `A=0 B=0 C=1` | unsupported | Canonicalized to 32-bit `OpWordCarry`, which amd64 rejects. Use 64-bit `OpWordCarry`. |
| `OpWordCarry` | `A=0 B=0 C=1 D=2 Bits=64 Arith=OpAdd Carry=0` | 8 | `A = B ± C ± carry`. Carry=0 or 1: `BT D, 0` loads the carry from bit 0 of D, then `ADC`/`SBB`. Bits must be 64. |
| `OpWordCarry` | `A=0 B=0 C=1 D=2 Bits=64 Arith=OpSub Carry=1` | 8 | `SBB` with the borrow taken from bit 0 of D. |
| `OpWordCarry` | `A=0 B=3 C=1 D=2 E=6 Bits=64 Arith=OpAdd Carry=2` | 12 | Carry=2 recomputes the carry from `D + E` (or `D - E`) in R11, then `MOV A, B` and `ADC`. R11 is clobbered. |
| `OpWordCarry` | `A=0 B=0 C=1 Bits=64 Arith=OpSub Carry=3` | 3 | Carry=3 uses CF from the preceding flag writer. A must not alias C unless A is B. |
| `OpWordCarry` | `A=0 B=0 C=1 Bits=32 Arith=OpAdd Carry=3` | rejected | Only Bits=64 is accepted. |
| `OpWordCarry` | `A=0 B=0 C=1 Bits=64 Arith=OpMul Carry=3` | rejected | Arith must be OpAdd or OpSub. |
| `OpCarryBit` | `A=0 B=1 C=2 Bits=64 Arith=OpAdd Carry=2` | 15 | A = carry out of `B + C` (or borrow of `B - C`), computed in R11, then `MOV A32, 0` and `SETB`. R11 is clobbered. |
| `OpCarryBit` | `A=8 B=11 C=2 Bits=64 Arith=OpAdd Carry=0` | 13 | Any mode other than 3 recomputes from B and C. The copy to R11 is skipped when B is R11. |
| `OpCarryBit` | `A=0 Bits=64 Arith=OpSub Carry=3` | 9 | Carry=3 materializes the current CF. |
| `OpCarryBit` | `A=0 B=1 C=2 Bits=32 Arith=OpAdd Carry=2` | rejected | Only Bits=64 is accepted. |
| `OpMulHigh` | `A=2 B=0 C=1 Bits=64` | 3 | 64-bit: `MUL C` with B in RAX, and A taken from RDX. RAX and RDX are clobbered. |
| `OpMulHigh` | `A=3 B=6 C=1 Bits=64` | 9 | Adds `MOV RAX, B` and `MOV A, RDX`. C must not be RAX unless B is RAX. |
| `OpMulHigh` | `A=0 B=0 C=1 Bits=32` | 8 | Bits 8, 16 and 32 use 64-bit `IMUL` and then `SHR A, Bits`. B and C must be zero-extended. A must not alias C unless A is B. |
| `OpMulHigh` | `A=3 B=6 C=7 Bits=16` | 11 | Narrow form with a leading `MOV A, B`. |
| `OpMulHigh` | `A=0 B=0 C=1 Bits=12` | rejected | Bits must be 8, 16, 32 or 64. |
| `OpMulHighSigned` | `A=2 B=0 C=1 Bits=64` | 3 | 64-bit: one-operand `IMUL C` with B in RAX, and A taken from RDX. RAX and RDX are clobbered. |
| `OpMulHighSigned` | `A=3 B=6 C=1 Bits=64` | 9 | Adds `MOV RAX, B` and `MOV A, RDX`. |
| `OpMulHighSigned` | `A=3 B=6 C=0 Bits=64` | rejected | C must not be RAX unless B is RAX. |
| `OpMulHighSigned` | `A=3 B=6 C=1 Bits=32` | 14 | Bits 8, 16 and 32: `MOVSX`/`MOVSXD` C into R11, then B into A, 64-bit `IMUL A, R11` and `SHR A, Bits`. Any operand may alias. Clobbers R11. |
| `OpMulHighSigned` | `A=3 B=6 C=1 Bits=16` | 20 | 8 and 16 bits end with `MOVZX` of the high half. |
| `OpMulHighSigned` | `A=3 B=11 C=1 Bits=32` | rejected | B must not be R11, which C's extension writes first. |
| `OpMulHighSigned` | `A=0 B=0 C=1 Bits=12` | rejected | Bits must be 8, 16, 32 or 64. |
| `OpMulWide` | `A=1` | rejected | 386 only: `machine.Visit` returns `ErrUnsupportedOperation`. Use `OpMulHigh`. |
| `OpMulAdd` | `A=0 B=1 C=2 D=3` | unsupported | No fused multiply-add for integers (`Policy.CanMulAdd` is false). |
| `OpAndNot` | `A=0 B=1 C=2 Bits=64` | unsupported | Not implemented. Lowering uses NOT and AND. |
| `OpCountZeros` | `A=0 B=1 Bits=64 Arith=OpTrailingZeros` | 14 | Baseline: `BSF`, `MOV R11D, Bits` and `CMOVZ A, R11`. A zero input yields Bits. R11 is clobbered. |
| `OpCountZeros` | `A=0 B=1 Bits=64 Arith=OpLeadingZeros` | 21 | Baseline: `BSR`, `XOR A, Bits-1`, `TEST B, B`, `MOV R11D, Bits` and `CMOVZ`. A must not alias B. B must be zero-extended from Bits. |
| `OpCountZeros` | `A=0 B=1 Bits=32 Arith=OpLeadingZeros` | 21 | Bits 8, 16 and 32 differ from 64 only in the immediates. |
| `OpCountZeros` | `A=8 B=9 Bits=16 Arith=OpTrailingZeros` | 14 | Extended registers use the REX byte that is already present. |
| `OpCountZeros` | `A=0 B=1 Bits=12 Arith=OpLeadingZeros` | rejected | Bits must be 8, 16, 32 or 64. |
| `OpCountZeros` | `A=0 B=1 Bits=64 Arith=OpAdd` | rejected | Arith must be OpLeadingZeros or OpTrailingZeros. |
| `OpDivide` | `A=1 B=6 C=7 Bits=64 Arith=OpDiv Imm=0` | 20 | `A = B / C`. Arith is OpDiv, OpDivSigned, OpRem or OpRemSigned; Imm is the `op.DivisionSemantics`. `DIV C` with B in RAX and RDX cleared; the quotient comes from RAX and the remainder from RDX. RAX and RDX are clobbered, and C must be neither. A checked semantics (Imm 0 or 1) first tests C and branches to the zero-divisor result: 0 for DivisionZero, all ones for DivisionAllOnes, B for a remainder. A may alias B or C. |
| `OpDivide` | `A=1 B=6 C=7 Bits=64 Arith=OpDivSigned Imm=1` | 39 | Signed 32- and 64-bit forms also branch on C = -1, where `IDIV` would trap on MIN: the quotient is `NEG B` (MIN stays MIN) and the remainder is 0. `CQO` sign-extends RAX into RDX. |
| `OpDivide` | `A=6 B=6 C=7 Bits=32 Arith=OpRemSigned Imm=0` | 20 | 32-bit form with `CDQ`; the result is zero-extended. A zero divisor leaves A = B in place. |
| `OpDivide` | `A=9 B=6 C=7 Bits=8 Arith=OpDivSigned Imm=1` | 28 | Bits 8 and 16 divide in 32 bits. Signed: `MOVSX EAX, B`, `MOVSX R11D, C`, `CDQ`, `IDIV R11D` and `MOVZX A, AL` (AX, or DL/DX for a remainder). R11 is clobbered too. MIN / -1 cannot overflow at 32 bits, so there is no -1 branch. B and C must be zero-extended from Bits, and so is A. |
| `OpDivide` | `A=9 B=6 C=7 Bits=16 Arith=OpRem Imm=2` | 9 | DivisionUnchecked emits the bare division, which raises #DE (SIGFPE) on a zero divisor or signed MIN / -1. |
| `OpDivide` | `A=3 B=0 C=1 Bits=64 Arith=OpDiv Imm=2` | 8 | B in RAX needs no move. |
| `OpDivide` | `A=1 B=6 C=2 Bits=64 Arith=OpDiv Imm=0` | rejected | C must not be RAX or RDX. |
| `OpDivide` | `A=1 B=6 C=7 Bits=12 Arith=OpDiv Imm=0` | rejected | Bits must be 8, 16, 32 or 64. |
| `OpDivide` | `A=1 B=6 C=7 Bits=64 Arith=OpDiv Imm=3` | rejected | Imm must be a defined `op.DivisionSemantics`. |
| `OpDivide` | `A=1 B=6 C=7 Bits=64 Arith=OpMul Imm=0` | rejected | |

## Extension and shifts

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpNarrow` | `A=0 B=1 Imm=8` | 4 | Zero-extends the low Imm bits: `MOVZX r32, r8` (with REX). Imm is the source width. |
| `OpNarrow` | `A=0 B=1 Imm=16` | 4 | `MOVZX r32, r16`. |
| `OpNarrow` | `A=0 B=0 Imm=32` | 3 | `MOV r32, r32`. Clears the upper half even when A is B. |
| `OpNarrow` | `A=0 B=1 Imm=64` | 3 | A plain 64-bit move. |
| `OpNarrow` | `A=0 B=0 Imm=64` | 0 | Emits nothing. |
| `OpNarrow` | `A=0 B=1 Imm=12` | rejected | Imm must be 8, 16, 32 or 64. |
| `OpNarrow` | `A=0 B=1 Imm=256` | rejected | Imm above 255 is rejected by `machine.Visit`. |
| `OpSignExtend` | `A=0 B=1 Imm=8` | 4 | `MOVSX r64, r8`. Imm is the source width. |
| `OpSignExtend` | `A=8 B=9 Imm=16` | 4 | `MOVSX r64, r16`. |
| `OpSignExtend` | `A=0 B=1 Imm=32` | 3 | `MOVSXD r64, r32`. |
| `OpSignExtend` | `A=0 B=1 Imm=64` | 3 | A plain move. A self-move is 0 bytes. |
| `OpSignExtend` | `A=0 B=1 Imm=0` | rejected | Imm must be 8, 16, 32 or 64. |
| `OpShift` | `A=0 B=0 Bits=64 Imm=3 Arith=OpShiftLeft` | 4 | Immediate shift (C1 /n ib). Imm is the count and must be below Bits. Arith is OpShiftLeft, OpShiftRight, OpShiftRightArithmetic, OpRotateLeft or OpRotateRight. |
| `OpShift` | `A=0 B=1 Bits=64 Imm=3 Arith=OpShiftRight` | 7 | When A differs from B, a 3-byte `MOV A, B` comes first. |
| `OpShift` | `A=0 B=0 Bits=16 Imm=3 Arith=OpShiftRightArithmetic` | 5 | The 16-bit form adds a 0x66 prefix. Bits 8 and 16 act on the low register part only. |
| `OpShift` | `A=0 B=0 Bits=8 Imm=7 Arith=OpRotateLeft` | 4 | The 8-bit form uses C0 /n ib with REX. |
| `OpShift` | `A=0 B=0 Bits=32 Imm=0 Arith=OpRotateRight` | 4 | A count of 0 is still encoded. The 32-bit form zero-extends. |
| `OpShift` | `A=0 B=0 Bits=32 Imm=32 Arith=OpShiftLeft` | rejected | Imm must be below Bits. |
| `OpShift` | `A=0 B=0 Imm=3 Arith=OpShiftLeft` | rejected | Bits must be 8, 16, 32 or 64. |
| `OpShift` | `A=0 B=0 Bits=64 Imm=3 Arith=OpAdd` | rejected | Arith must be a shift or rotate. |
| `OpShiftBy` | `A=0 B=0 C=1 Bits=64 Arith=OpShiftLeft` | 17 | `A = B shift C`. C is copied to RCX. Shifts use a 64-bit `SHL/SHR/SAR A, CL`, then `CMP RCX, Bits` and `CMOVAE` from R11, so that a count of Bits or more gives 0 (or the sign fill). RCX and R11 are clobbered. A must not be RCX. |
| `OpShiftBy` | `A=0 B=2 C=3 Bits=32 Arith=OpShiftRight` | 26 | Adds `MOV RCX, C`, `MOV A, B`, and a final `MOV A32, A32` for the zero extension. B must be zero-extended. |
| `OpShiftBy` | `A=0 B=0 C=1 Bits=64 Arith=OpShiftRightArithmetic` | 18 | R11 receives the sign fill (`MOV R11, A` and `SAR R11, 63`). |
| `OpShiftBy` | `A=0 B=0 C=1 Bits=8 Arith=OpShiftRightArithmetic` | 26 | Narrow arithmetic shifts sign-extend first and zero-extend the result. |
| `OpShiftBy` | `A=0 B=0 C=1 Bits=64 Arith=OpRotateRight` | 3 | Rotates use the hardware count mask (`ROR A, CL`). RCX is clobbered, but R11 is not. |
| `OpShiftBy` | `A=0 B=0 C=1 Bits=32 Arith=OpRotateLeft` | 6 | A 32-bit rotate is followed by `MOV A32, A32`. |
| `OpShiftBy` | `A=0 B=0 C=1 Bits=16 Arith=OpRotateRight` | 8 | A 16-bit rotate (0x66) is followed by `MOVZX`. |
| `OpShiftBy` | `A=0 B=0 C=1 Bits=64 Arith=OpAdd` | rejected | Arith must be a shift or rotate. |
| `OpShiftBy` | `A=0 B=0 C=1 Arith=OpShiftLeft` | rejected | Bits must be 8, 16, 32 or 64. |

## Selection

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpSelect` | `A=0 B=1 C=2 D=0` | 7 | `A = B != 0 ? C : D`, built from `TEST B, B` and `CMOVNE A, C`. Writes flags. |
| `OpSelect` | `A=0 B=1 C=2 D=3` | 10 | When A differs from D, a 3-byte `MOV A, D` comes first. A must not alias B or C unless A is D. |
| `OpSelect` | `A=8 B=9 C=10 D=11` | 10 | Extended registers add no bytes. |
| `OpSelect` | `A=0 B=1 C=2 D=16` | rejected | GP only. |
| `OpSelectFlags` | `A=0 C=1 D=2 Cond=Less` | unsupported | No flag-consuming select. |

## Memory

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpLoad64` | `A=0 B=1` | 3 | `MOV A, [B]`. |
| `OpLoad64` | `A=0 B=4` | 4 | A base of RSP or R12 needs a SIB byte. |
| `OpLoad64` | `A=0 B=13` | 4 | A base of RBP or R13 needs a zero disp8. |
| `OpLoad64` | `A=8 B=9` | 3 | Extended registers fit in the same REX byte. |
| `OpLoad64` | `A=0 B=1 BigEndian` | rejected | Only native (little-endian) order is supported. |
| `OpLoad64` | `A=0 B=16` | rejected | GP only. |
| `OpStore64` | `A=1 B=0` | 3 | `MOV [A], B`. A is the address and B is the value. |
| `OpStore64` | `A=12 B=0` | 4 | R12 base adds a SIB byte. |
| `OpStore64` | `A=5 B=0` | 4 | RBP base adds a zero disp8. |
| `OpLoad8` | `A=0 B=1` | 4 | `MOVZX A, byte [B]`. Zero-extends to 64 bits. |
| `OpLoad8` | `A=0 B=12` | 5 | RSP/R12 bases add a SIB byte, and RBP/R13 bases add a disp8. |
| `OpLoad8` | `A=0 B=1 BigEndian` | 4 | Byte order does not apply to a single byte. |
| `OpStore8` | `A=1 B=6` | 3 | `MOV byte [A], B8`. A REX prefix is always present, so SPL–DIL are addressable. |
| `OpStore8` | `A=5 B=0` | 4 | RBP base adds a zero disp8. |
| `OpLoad16` | `A=0 B=1` | 3 | `MOVZX A32, word [B]`. Zero-extends. There is no REX when both registers are below 8. |
| `OpLoad16` | `A=8 B=1` | 4 | An extended register adds REX. |
| `OpLoad16` | `A=0 B=13` | 5 | R13 base: REX plus a zero disp8. |
| `OpLoad16` | `A=0 B=1 BigEndian` | rejected | Only native order is supported. |
| `OpStore16` | `A=1 B=0` | 3 | `MOV word [A], B16` (0x66 prefix). A is the address and B is the value. |
| `OpStore16` | `A=12 B=0` | 5 | R12 base: REX plus SIB. |
| `OpLoad32` | `A=0 B=1` | 2 | `MOV A32, [B]`. Zero-extends. |
| `OpLoad32` | `A=0 B=5` | 3 | RBP base adds a zero disp8. |
| `OpLoad32` | `A=9 B=1` | 3 | An extended register adds REX. |
| `OpStore32` | `A=1 B=0` | 2 | `MOV dword [A], B32`. |
| `OpStore32` | `A=1 B=8` | 3 | An extended register adds REX. |
| `OpLoadOffset` | `A=0 B=1 Disp=0` | 3 | `MOV A, [B + Disp]`. A zero Disp uses the `OpLoad64` encoding. |
| `OpLoadOffset` | `A=0 B=1 Disp=8` | 4 | disp8 for a signed Disp in -128..127. |
| `OpLoadOffset` | `A=0 B=1 Disp=0xfffffffffffffff8` | 4 | Disp is a two's-complement signed value (here -8). |
| `OpLoadOffset` | `A=0 B=4 Disp=8` | 5 | RSP/R12 bases add a SIB byte. |
| `OpLoadOffset` | `A=0 B=1 Disp=0x100` | 7 | disp32 otherwise. |
| `OpLoadOffset` | `A=0 B=4 Disp=0x100` | 8 | SIB plus disp32. |
| `OpLoadOffset` | `A=0 B=1 Disp=0x80000000` | rejected | Disp must fit a signed 32-bit value. |
| `OpStoreOffset` | `A=1 B=0 Disp=16` | 4 | `MOV [A + Disp], B`. A is the base and B is the value. |
| `OpStoreOffset` | `A=12 B=0 Disp=0x200` | 8 | SIB plus disp32. |
| `OpStoreOffset` | `A=1 B=0 Disp=0x100000000` | rejected | Disp must fit a signed 32-bit value. |
| `OpLoadAddress` | `A=0 B=1 C=2 Imm=8` | 4 | `MOV A, [B + C*Imm + Disp]`. Imm (the scale) is 1, 2, 4 or 8. |
| `OpLoadAddress` | `A=0 B=1 C=2 Imm=8 Disp=8` | 5 | disp8 for a signed Disp in -128..127. |
| `OpLoadAddress` | `A=0 B=5 C=2 Imm=1` | 5 | RBP/R13 bases always carry a displacement. |
| `OpLoadAddress` | `A=8 B=9 C=10 Imm=4 Disp=0x80` | 8 | disp32 otherwise. |
| `OpLoadAddress` | `A=0 B=1 C=4 Imm=1` | rejected | The index C cannot be RSP. |
| `OpLoadAddress` | `A=0 B=1 C=2 Imm=3` | rejected | Imm must be 1, 2, 4 or 8. |
| `OpLoadAddress` | `A=0 B=1 C=2 Imm=1 Disp=0x80000000` | rejected | Disp must fit a signed 32-bit value. |
| `OpStoreAddress` | `A=1 B=2 C=0 Imm=8` | 4 | `MOV [A + B*Imm + Disp], C`. A is the base, B the index and C the value. |
| `OpStoreAddress` | `A=13 B=2 C=0 Imm=8` | 5 | R13 base adds a zero disp8. |
| `OpStoreAddress` | `A=1 B=4 C=0 Imm=8` | rejected | The index B cannot be RSP. |
| `OpScaled` | `A=0 B=1 C=2 Imm=8` | 4 | `LEA A, [B + C*Imm + Disp]`. Does not write flags. Imm is 1, 2, 4 or 8, C is not RSP, and Disp fits in signed 32 bits (see `Policy.CanScaled`). |
| `OpScaled` | `A=0 B=1 C=2 Imm=4 Disp=16` | 5 | disp8. |
| `OpScaled` | `A=0 B=13 C=2 Imm=1` | 5 | RBP/R13 bases always carry a displacement. |
| `OpScaled` | `A=0 B=1 C=2 Imm=2 Disp=0x1000` | 8 | disp32. |
| `OpScaled` | `A=0 B=16 C=2 Imm=1` | rejected | GP only. |
| `OpScaled` | `A=0 B=1 C=2 Imm=3` | rejected | The scale is 1, 2, 4 or 8. |
| `OpScaled` | `A=0 B=1 C=4 Imm=1` | rejected | RSP cannot be an index. |
| `OpScaled` | `A=0 B=1 C=2 Imm=1 Disp=0x80000000` | rejected | Disp is a signed 32-bit value. |

## Calls and frames

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpSyscall` | — | 2 | `SYSCALL`. Register setup is the caller's job. |
| `OpCallC` | — | 5 | `XOR EAX, EAX` (AL=0 vector arguments) then `CALL R11`. The target address must be in R11 (`Policy.CallTargetRegister`). |
| `OpCallGo` | — | 3 | `CALL R11`. |
| `OpSaveLink` | `Slot=0` | 0 | No link register. CALL pushes the return address. |
| `OpRestoreLink` | `Slot=0` | 0 | Emits nothing. |
| `OpSpill` | `A=0 Slot=0` | 8 | `MOV [RSP + Slot*8], A`. Always uses SIB plus disp32. |
| `OpSpill` | `A=15 Slot=100` | 8 | Same size for every slot and register. |
| `OpSpill` | `A=0 Slot=0x10000000` | rejected | Slot*8 must fit a signed 32-bit displacement (Slot ≤ 0x0FFFFFFD). |
| `OpSpill` | `A=16 Slot=0` | rejected | GP only. Use `OpFloatSpill` or `OpVectorSpill`. |
| `OpReload` | `A=0 Slot=0` | 8 | `MOV A, [RSP + Slot*8]`. |
| `OpReload` | `A=9 Slot=3` | 8 | Same size for every slot and register. |
| `OpReload` | `A=0 Slot=0x10000000` | rejected | Slot out of range. |
| `OpLoadIncoming` | `A=0 Imm=0` | unsupported | Not implemented. Incoming stack words are read with `OpLoadOffset`. |
| `OpFrameEnter` | `Imm=16` | 7 | `SUB RSP, imm32`. Imm is the frame size, from 0 to 1 MiB. Always uses imm32. |
| `OpFrameEnter` | `Imm=0x100000` | 7 | The maximum frame adjustment. |
| `OpFrameEnter` | `Imm=0x100001` | rejected | Above 1 MiB. |
| `OpFrameEnter` | `Imm=16 CallArea=16` | rejected | A separate call area is not supported. Fold it into Imm. |
| `OpFrameLeave` | `Imm=16` | 7 | `ADD RSP, imm32`. |
| `OpFrameLeave` | `Imm=0x100001` | rejected | Above 1 MiB. |
| `OpReturn` | — | 1 | `RET`. |
| `OpSaveCFloat` | `V=A:8 Slot=0` | unsupported | arm64-only. |
| `OpRestoreCFloat` | `V=A:8 Slot=0` | unsupported | arm64-only. |
| `OpSaveCVector` | `V=A:8 Slot=0` | unsupported | Not implemented. |
| `OpRestoreCVector` | `V=A:8 Slot=0` | unsupported | Not implemented. |
| `OpSaveCondition` | `Slot=0` | unsupported | Not implemented. |
| `OpRestoreCondition` | `Slot=0` | unsupported | Not implemented. |

## Compares and branches

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpCompareImmediate` | `A=0 Imm=1` | 7 | `CMP A, simm32` (64-bit). Always uses imm32. Bits is 0 or 64. Cmp may be Equality, Signed or Unsigned; the encoding is the same. |
| `OpCompareImmediate` | `A=8 SImm=-1 Cmp=Signed` | 7 | Imm is two's-complement. |
| `OpCompareImmediate` | `A=0 Imm=0x7fffffff Bits=64 Cmp=Unsigned` | 7 | The largest positive immediate. |
| `OpCompareImmediate` | `A=0 Imm=0x80000000` | rejected | Imm must fit a signed 32-bit value. |
| `OpCompareImmediate` | `A=0 Imm=1 Bits=32` | rejected | Narrow compares are not supported. Normalize operands to 64 bits. |
| `OpCompareRegister` | `A=0 B=1` | 3 | `CMP A, B` (flags of A - B). |
| `OpCompareRegister` | `A=8 B=15 Bits=64 Cmp=Unsigned` | 3 | Extended registers fit in the same REX byte. |
| `OpCompareRegister` | `A=0 B=1 Bits=32` | rejected | Only Bits 0 or 64. |
| `OpJump` | `Imm=0` | 5 | `JMP rel32` to the absolute PC in Imm. There is no short form, and Long is ignored. |
| `OpJump` | `Imm=0x10000 Long` | 5 | Same encoding. |
| `OpJump` | `Imm=0x80000005` | rejected | The displacement from the end of the instruction must fit rel32. |
| `OpJumpIf` | `Cond=Equal Imm=0` | 6 | `Jcc rel32`. Cond is Equal, NotEqual, Less, GreaterEqual, LessEqual, Greater, Below, AboveEqual, BelowEqual, Above, Sign or NotSign. |
| `OpJumpIf` | `Cond=Above Imm=64` | 6 | Unsigned conditions map to JA/JAE/JB/JBE. |
| `OpJumpIf` | `Cond=NotSign Imm=64` | 6 | JNS. |
| `OpJumpIf` | `Cond=Less Imm=64 Long Far` | 6 | Long and Far are ignored. rel32 always reaches. |
| `OpJumpIf` | `Cond=Carry Imm=64` | rejected | The Carry, NotCarry, Carry32 and NotCarry32 conditions are not encoded. |
| `OpJumpIf` | `Cond=Equal Imm=0x80000006` | rejected | The displacement must fit rel32. |
| `OpBranchZero` | `A=1 Bits=64 Cond=Equal Imm=100` | 2 | `JRCXZ rel8`. Only A=RCX, Bits=64 and Cond=Equal (branch if zero) are accepted. |
| `OpBranchZero` | `A=1 Bits=64 Imm=129` | 2 | The short form reaches -128..127 from the end of the instruction. |
| `OpBranchZero` | `A=1 Bits=64 Imm=130` | rejected | Out of rel8 range. Set Long. |
| `OpBranchZero` | `A=1 Bits=64 Imm=0x10000 Long` | 12 | `JRCXZ +5`, `JMP +5` and `JMP rel32`. Preserves flags. |
| `OpBranchZero` | `A=1 Bits=64 Cond=NotEqual Imm=100` | rejected | Branch-if-nonzero is not encoded. |
| `OpBranchZero` | `A=0 Bits=64 Imm=100` | rejected | A must be RCX. |
| `OpBranchZero` | `A=1 Bits=32 Imm=100` | rejected | Bits must be 64. |
| `OpBranchBit` | `A=0 Bits=64 Disp=3 Cond=Equal Imm=100` | unsupported | Not implemented. |
| `OpLoop` | `A=1 Imm=0` | 2 | `LOOP rel8`: decrements RCX and branches if it is nonzero. A is ignored, because RCX is implicit. |
| `OpLoop` | `A=1 Imm=0x10000 Long` | 11 | `LEA RCX, [RCX-1]`, `JRCXZ +5` and `JMP rel32`. Preserves flags. |
| `OpLoop` | `A=1 Imm=200` | rejected | Out of rel8 range. Set Long. |
| `OpSetCount` | `A=1 Bits=64` | unsupported | No dedicated loop-count register setup. `OpLoop` uses RCX directly. |

## Floating point

Scalar FP operands may be XMM registers or GP registers that hold the raw bit
pattern. A GP input is copied into an expansion temporary with `MOVD`/`MOVQ`
(XMM0, XMM1 or XMM2, by operand position). A GP destination is computed in
XMM0 and copied back. When any operand is a GP register, the XMM operands
must avoid XMM0–XMM2. Bits is 32 (single) or 64 (double).

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpFloatBinary` | `V=A:3,B:3,C:4 Bits=64 Arith=OpFAdd` | 4 | `ADDSD A, C` when A is B. Arith is OpFAdd, OpFSub, OpFMul or OpFDiv. |
| `OpFloatBinary` | `V=A:3,B:4,C:5 Bits=32 Arith=OpFSub` | 8 | When A differs from B, a 4-byte `MOVAPD A, B` comes first. A must not alias C unless A is B. |
| `OpFloatBinary` | `V=A:8,B:8,C:9 Bits=64 Arith=OpFMul` | 5 | XMM8–XMM15 add REX. |
| `OpFloatBinary` | `V=A:3,B:3 C=0 Bits=64 Arith=OpFDiv` | 9 | A GP right operand is moved into XMM1 (`MOVQ`, 5 bytes). |
| `OpFloatBinary` | `A=0 B=1 C=2 Bits=64 Arith=OpFAdd` | 19 | All GP: two `MOVQ` loads, the operation in XMM0, and `MOVQ` back. |
| `OpFloatBinary` | `A=0 B=1 C=2 Bits=32 Arith=OpFAdd` | 16 | 32-bit transfers use `MOVD` (4 bytes without REX). |
| `OpFloatBinary` | `V=A:3,B:3,C:4 Bits=16 Arith=OpFAdd` | rejected | Bits must be 32 or 64. |
| `OpFloatBinary` | `V=A:3,B:3,C:4 Bits=64 Arith=OpAdd` | rejected | Integer arithmetic is not accepted here. |
| `OpFloatBinary` | `V=A:16,B:3,C:4 Bits=64 Arith=OpFAdd` | rejected | Only XMM0–XMM15 exist. |
| `OpFloatFMA` | `V=A:3,B:3,C:4,D:5 Bits=64` | 5 | `A = B*C + D` as VEX `VFMADD213SD` when A is B or C, or `VFMADD231SD` when A is D. Arith is ignored. Encoded whatever the CPU profile, so lowering emits it only when the CPU has FMA3 (`Policy.SupportsFloatFMA` is false on the baseline). |
| `OpFloatFMA` | `V=A:3,B:4,C:5,D:3 Bits=64` | 5 | A is D: the 231 form. |
| `OpFloatFMA` | `V=A:3,B:4,C:5,D:6 Bits=32` | 9 | When A is none of the inputs, `MOVAPD A, D` comes first. |
| `OpFloatFMA` | `A=0 B=1 C=2 D=3 Bits=64` | 25 | GP inputs D, B and C go to XMM0, XMM1 and XMM2. The result is copied from XMM0. |
| `OpFloatFMA` | `V=A:3,B:3,C:4,D:5 Bits=16` | rejected | Bits must be 32 or 64. |
| `OpFloatUnary` | `V=A:3,B:4 Bits=64 Arith=OpFSqrt` | 4 | `SQRTSD A, B`. Arith is OpFSqrt or OpFConvert. |
| `OpFloatUnary` | `V=A:9,B:10 Bits=32 Arith=OpFSqrt` | 5 | `SQRTSS` with REX. |
| `OpFloatUnary` | `V=A:3,B:4 Bits=64 Arith=OpFConvert` | 4 | Bits is the result width: 64 means `CVTSS2SD` and 32 means `CVTSD2SS`. |
| `OpFloatUnary` | `A=0 B=1 Bits=32 Arith=OpFSqrt` | 12 | GP input and output use `MOVD` transfers through XMM0. |
| `OpFloatUnary` | `V=A:3,B:4 Bits=64 Arith=OpFAdd` | rejected | Only OpFSqrt and OpFConvert are accepted. |
| `OpFloatMove` | `V=A:3,B:4 Bits=64` | 4 | XMM to XMM `MOVAPD`. Bits is 32 or 64. |
| `OpFloatMove` | `V=A:3,B:3 Bits=64` | 0 | A self-move emits nothing. |
| `OpFloatMove` | `V=A:3 B=0 Bits=64` | 5 | GP to XMM: `MOVQ`. |
| `OpFloatMove` | `V=A:3 B=0 Bits=32` | 4 | GP to XMM: `MOVD` (the upper lanes are zeroed). |
| `OpFloatMove` | `A=0 V=B:3 Bits=64` | 5 | XMM to GP: `MOVQ`. |
| `OpFloatMove` | `V=A:11 B=9 Bits=64` | 5 | Extended registers fit in the same REX byte. |
| `OpFloatMove` | `A=0 B=1 Bits=64` | rejected | At least one operand must be an XMM register. |
| `OpFloatMove` | `V=A:3,B:4 Bits=16` | rejected | Bits must be 32 or 64. |
| `OpIntToFloat` | `V=A:3 B=6 Bits=64 Imm=64 Arith=OpFFromSigned` | 8 | `XORPS A, A` and `CVTSI2SD A, B`. Bits is the float width and Imm the integer width, 32 or 64. The conversion rounds in the MXCSR mode and does not write flags. A must be an XMM register and B a GP register. |
| `OpIntToFloat` | `V=A:9 B=12 Bits=32 Imm=32 Arith=OpFFromSigned` | 9 | `CVTSI2SS` from a 32-bit register, with REX. |
| `OpIntToFloat` | `V=A:3 B=6 Bits=64 Imm=32 Arith=OpFFromUnsigned` | 8 | A zero-extended unsigned 32-bit B converts with the 64-bit signed form. |
| `OpIntToFloat` | `V=A:3 B=6 Bits=64 Imm=64 Arith=OpFFromUnsigned` | 40 | Unsigned 64-bit: a nonnegative B converts directly. Otherwise R11 = (B>>1) OR (B AND 1) is converted and doubled with `ADDSD A, A`, which rounds as B would in every mode. R11 is clobbered, B must not be R11, and flags are written. No XMM register other than A is written. |
| `OpIntToFloat` | `V=A:3 B=6 Bits=16 Imm=64 Arith=OpFFromSigned` | rejected | Bits must be 32 or 64. |
| `OpIntToFloat` | `A=1 B=6 Bits=64 Imm=64 Arith=OpFFromSigned` | rejected | A GP destination is not accepted. |
| `OpIntToFloat` | `V=A:3 B=11 Bits=64 Imm=64 Arith=OpFFromUnsigned` | rejected | The unsigned 64-bit sequence uses R11. |
| `OpFloatToInt` | `A=9 V=B:10 Bits=64 Disp=32 Imm=1 Arith=OpFToSigned` | 5 | `CVTTSS2SI A, B` (`CVTTSD2SI` for Disp=64). Bits is the integer width and Disp the float width, 32 or 64. Imm is the `op.ConversionSemantics`; ConversionNative returns the integer indefinite value (MIN) for NaN and out-of-range input and does not write flags. A 32-bit result is zero-extended. |
| `OpFloatToInt` | `A=1 V=B:3 Bits=32 Disp=64 Imm=0 Arith=OpFToSigned` | 34 | ConversionSaturate: a MIN result (`CMP A, 1` overflows) is checked with `UCOMISD B, B` for NaN (0), and else the sign of B, moved into A with `MOVQ`, selects MIN or MAX. |
| `OpFloatToInt` | `A=1 V=B:3 Bits=64 Disp=32 Imm=0 Arith=OpFToSigned` | 40 | 64-bit saturation forms MAX or MIN with `NOT A` and `BTC A, 63`. |
| `OpFloatToInt` | `A=1 V=B:3 Bits=32 Disp=64 Imm=1 Arith=OpFToUnsigned` | 7 | Unsigned 32-bit: the 64-bit conversion, then `MOV A32, A32`. |
| `OpFloatToInt` | `A=1 V=B:3 Bits=32 Disp=64 Imm=0 Arith=OpFToUnsigned` | 39 | Saturating: when the 64-bit result's high half is not zero, B is NaN or negative (0) or too large (0xFFFFFFFF). |
| `OpFloatToInt` | `A=9 V=B:10 Bits=64 Disp=32 Imm=1 Arith=OpFToUnsigned` | 24 | Unsigned 64-bit: a negative signed result means B may be in [2^63, 2^64), whose value is its bits shifted left (40 for binary32, 11 for binary64) with bit 63 set. |
| `OpFloatToInt` | `A=1 V=B:3 Bits=64 Disp=64 Imm=0 Arith=OpFToUnsigned` | 62 | Saturating: the sign and exponent of B pick the shifted bits, MAX (`SBB A, A`) or 0. Only A and flags are written. |
| `OpFloatToInt` | `A=1 V=B:3 Bits=16 Disp=64 Imm=0 Arith=OpFToSigned` | rejected | Bits must be 32 or 64. |
| `OpFloatToInt` | `A=1 V=B:3 Bits=64 Disp=64 Imm=2 Arith=OpFToSigned` | rejected | Imm must be a defined `op.ConversionSemantics`. |
| `OpFloatToInt` | `V=A:1,B:3 Bits=64 Disp=64 Imm=0 Arith=OpFToSigned` | rejected | A must be a GP register. |
| `OpFloatCompare` | `A=1 V=B:2,C:3 Bits=64 Imm=6` | 9 | A = 1 when the Bits-wide floats in XMM B and C satisfy the `op.FloatCondition` in Imm, else 0: `XOR A32, A32; UCOMISD B, C; SETcc A8` (`UCOMISS` for Bits=32). FloatGreater is `SETA`, GreaterEqual `SETAE`, UnorderedLessEqual `SETBE`, UnorderedLess `SETB`, OrderedNotEqual `SETNE`, UnorderedEqual `SETE`, Ordered `SETNP` and Unordered `SETP`. Writes flags; no register other than A. |
| `OpFloatCompare` | `A=1 V=B:2,C:3 Bits=32 Imm=2` | 8 | FloatLess, LessEqual, UnorderedGreaterEqual and UnorderedGreater compare C with B (`UCOMISS C, B`) and use `SETA`, `SETAE`, `SETBE` and `SETB`. |
| `OpFloatCompare` | `A=1 V=B:2,C:3 Bits=64 Imm=0` | 12 | FloatEqual is (CF or ZF) minus CF: `SETBE A8; SBB A32, 0`. |
| `OpFloatCompare` | `A=1 V=B:2,C:3 Bits=64 Imm=1` | 12 | FloatNotEqual is (neither CF nor ZF) plus CF: `SETA A8; ADC A32, 0`. |
| `OpFloatCompare` | `A=6 V=B:9,C:3 Bits=64 Imm=11` | 11 | `SETcc SIL` needs REX, as do XMM8–XMM15. |
| `OpFloatCompare` | `A=11 V=B:12,C:15 Bits=64 Imm=0` | 16 | There is no temporary: A may be any GP register, R11 included. |
| `OpFloatCompare` | `A=1 V=B:2,C:3 Bits=16 Imm=0` | rejected | Bits must be 32 or 64. |
| `OpFloatCompare` | `A=1 V=B:2,C:3 Bits=64 Imm=14` | rejected | Imm must be an `op.FloatCondition`. |
| `OpFloatCompare` | `V=A:1,B:2,C:3 Bits=64 Imm=0` | rejected | A must be a GP register. |
| `OpFloatCompare` | `A=1 B=2 V=C:3 Bits=64 Imm=0` | rejected | B and C must be XMM registers. |
| `OpFloatBits` | `V=A:3,B:3 Bits=64 Arith=OpXor` | 4 | `XORPD A, XMM0` (or `ANDPD` for OpAnd). The mask must already be in XMM0, and lowering loads it with `OpLiteralLoad`. Imm is not read. A must be an XMM register from XMM3 up. |
| `OpFloatBits` | `V=A:3,B:4 Bits=32 Arith=OpAnd` | 8 | When A differs from B, `MOVAPD A, B` comes first. |
| `OpFloatBits` | `V=A:3 B=0 Bits=64 Arith=OpXor` | 13 | A GP input is moved to XMM1 and then to A. |
| `OpFloatBits` | `V=A:3,B:3 Bits=64 Arith=OpOr` | rejected | Arith must be OpAnd or OpXor. |
| `OpFloatBits` | `A=3 V=B:4 Bits=64 Arith=OpXor` | rejected | A GP destination would overwrite the XMM0 mask. |
| `OpFloatBits` | `V=A:0,B:4 Bits=64 Arith=OpXor` | rejected | |
| `OpFloatSpill` | `V=A:3 Slot=0` | 5 | `MOVSD [RSP + Slot*8], A`. Always stores 64 bits. Bits is 0, 32 or 64. |
| `OpFloatSpill` | `V=A:3 Slot=1 Bits=32` | 6 | disp8 for offsets up to 127. |
| `OpFloatSpill` | `V=A:9 Slot=1 Bits=64` | 7 | XMM8–XMM15 add REX. |
| `OpFloatSpill` | `V=A:3 Slot=16` | 9 | disp32 from offset 128. |
| `OpFloatSpill` | `A=0 Slot=0` | rejected | A must be an XMM register. |
| `OpFloatSpill` | `V=A:3 Slot=0 Bits=16` | rejected | Bits must be 0, 32 or 64. |
| `OpFloatReload` | `V=A:3 Slot=0` | 5 | `MOVSD A, [RSP + Slot*8]`. |
| `OpFloatReload` | `V=A:12 Slot=40 Bits=64` | 10 | REX plus SIB plus disp32. |
| `OpFloatReload` | `V=A:3 Slot=0x10000000` | rejected | Slot out of range. |

## Literals

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpLiteralLoad` | `V=A:3 Bits=64 Disp=64` | 8 | `MOVSD A, [RIP + rel32]`. Disp is the absolute PC of the pool entry. Bits 32 (`MOVSS`), 64 (`MOVSD`) or 128 (`MOVDQU`). |
| `OpLiteralLoad` | `V=A:3 Bits=32 Disp=64` | 8 | `MOVSS`. |
| `OpLiteralLoad` | `V=A:9 Bits=128 Disp=64` | 9 | `MOVDQU`. XMM8–XMM15 add REX. |
| `OpLiteralLoad` | `V=A:3 Bits=16 Disp=64` | rejected | Bits must be 32, 64 or 128. |
| `OpLiteralLoad` | `A=0 Bits=64 Disp=64` | rejected | A must be an XMM register. |
| `OpLiteralLoad` | `V=A:3 Bits=64 Disp=0x80000008` | rejected | The RIP-relative displacement must fit rel32. |
| `OpLiteralData` | `Data=000000000000f03f` | 8 | Raw pool bytes, emitted as given. The size is `len(Data)`. |
| `OpLiteralData` | `Data=00000080` | 4 | Any length. |

## Vectors

`OpVector` acts on whole 128-bit SSE2 registers. Bits gives the lane width
(8, 16, 32 or 64) and `VectorType` is not read. XMM0 is an expansion
temporary for extract, insert-zero and rotate.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpVectorMove` | `V=A:3,B:4` | 4 | `MOVDQA A, B`. |
| `OpVectorMove` | `V=A:3,B:12` | 5 | XMM8–XMM15 add REX. |
| `OpVectorMove` | `V=A:3,B:3` | 0 | A self-move emits nothing. |
| `OpVectorMove` | `V=A:3 B=0` | rejected | Both operands must be XMM registers. |
| `OpVectorSpill` | `V=A:3 Slot=0` | 5 | `MOVDQU [RSP + Slot*8], A`. The slot spans 16 bytes (two 8-byte slots). |
| `OpVectorSpill` | `V=A:3 Slot=2` | 6 | disp8. |
| `OpVectorSpill` | `V=A:10 Slot=32` | 10 | REX plus SIB plus disp32. |
| `OpVectorSpill` | `A=0 Slot=0` | rejected | A must be an XMM register. |
| `OpVectorReload` | `V=A:3 Slot=0` | 5 | `MOVDQU A, [RSP + Slot*8]`. |
| `OpVectorReload` | `V=A:3 Slot=0x10000000` | rejected | Slot out of range. |
| `OpVector` | `V=A:3 B=1 Arith=OpVLoad` | 4 | `MOVDQU A, [B]`. Alignment is not required. Bits is not read. |
| `OpVector` | `V=A:3 B=4 Arith=OpVLoad` | 5 | RSP/R12 bases add a SIB byte. |
| `OpVector` | `V=A:3 B=13 Arith=OpVLoad` | 6 | R13 base: REX plus a zero disp8. |
| `OpVector` | `V=A:3 B=1 Disp=16 Arith=OpVLoad` | rejected | Disp must be 0. |
| `OpVector` | `V=A:3 B=1 C=2 Indexed Arith=OpVLoad` | rejected | Indexed addressing is not supported. |
| `OpVector` | `A=1 V=B:3 Arith=OpVStore` | 4 | `MOVDQU [A], B`. A is the GP address and B is the vector. |
| `OpVector` | `V=A:3 Arith=OpVZero` | 4 | `PXOR A, A`. |
| `OpVector` | `A=0 V=B:3 Arith=OpVMaskBits` | 4 | `PMOVMSKB A32, B`: the byte sign mask. |
| `OpVector` | `V=A:3,B:3,C:4 Bits=8 Arith=OpAdd` | 4 | `PADDB/W/D/Q` by Bits. OpSub selects `PSUBB/W/D/Q`. |
| `OpVector` | `V=A:3,B:4,C:5 Bits=64 Arith=OpSub` | 8 | When A differs from B, `MOVDQA A, B` comes first. A must not alias C unless A is B. |
| `OpVector` | `V=A:3,B:3,C:4 Bits=64 Arith=OpAnd` | 4 | `PAND`, `POR` or `PXOR` for any valid Bits. |
| `OpVector` | `V=A:3,B:3,C:4 Bits=16 Arith=OpMul` | 4 | `PMULLW`. Only Bits=16 is supported for OpMul. |
| `OpVector` | `V=A:3,B:3,C:4 Bits=32 Arith=OpMul` | rejected | No 8-, 32- or 64-bit lane multiply in SSE2. |
| `OpVector` | `V=A:3,B:3,C:4 Arith=OpXor` | rejected | Element operations need Bits of 8, 16, 32 or 64. |
| `OpVector` | `V=A:16,B:3,C:4 Bits=8 Arith=OpAdd` | rejected | Only XMM0–XMM15 exist. |
| `OpVector` | `V=A:3 B=0 Bits=8 Arith=OpVSplat` | 18 | `MOVD`, `PUNPCKLBW`, `PSHUFLW` and `PSHUFD`: broadcast the low Bits of the GP register B. |
| `OpVector` | `V=A:3 B=0 Bits=16 Arith=OpVSplat` | 14 | `MOVD`, `PSHUFLW` and `PSHUFD`. |
| `OpVector` | `V=A:3 B=0 Bits=32 Arith=OpVSplat` | 9 | `MOVD` and `PSHUFD`. |
| `OpVector` | `V=A:3 B=0 Bits=64 Arith=OpVSplat` | 9 | `MOVQ` and `PUNPCKLQDQ`. |
| `OpVector` | `A=0 V=B:3 Bits=64 Imm=0 Arith=OpVExtract` | 9 | Lane Imm of B into the GP register A, through XMM0 (`MOVDQA`, an optional `PSRLDQ` and `MOVQ`). |
| `OpVector` | `A=0 V=B:3 Bits=32 Imm=1 Arith=OpVExtract` | 17 | A nonzero lane adds `PSRLDQ`. Narrow lanes are zero-extended in A. |
| `OpVector` | `A=0 V=B:3 Bits=8 Imm=15 Arith=OpVExtract` | 18 | Imm must be below 128/Bits. |
| `OpVector` | `A=0 V=B:3 Bits=32 Imm=4 Arith=OpVExtract` | rejected | The lane index is out of range. |
| `OpVector` | `V=A:3,B:3 C=0 Bits=16 Imm=2 Arith=OpVInsert` | 5 | `PINSRW A, C32, Imm`: replace lane Imm of B with the GP register C. Only Bits=16 is supported. |
| `OpVector` | `V=A:3,B:3 C=0 Bits=32 Imm=1 Arith=OpVInsert` | rejected | OpVInsert accepts only 16-bit lanes. Use OpVInsertZero. |
| `OpVector` | `V=A:3,B:3 C=0 Bits=32 Imm=0 Arith=OpVInsertZero` | 25 | Clears lane Imm with a mask built in XMM0, then ORs in the low Bits of C. Any Bits from 8 to 64 is accepted. |
| `OpVector` | `V=A:3,B:4 C=0 Bits=8 Imm=3 Arith=OpVInsertZero` | 49 | A nonzero lane and a narrow scalar add byte-shift steps. |
| `OpVector` | `V=A:3,B:3 C=0 Bits=64 Imm=1 Arith=OpVInsertZero` | 36 | 64-bit scalars use `MOVQ`. |
| `OpVector` | `V=A:3,B:3 Bits=32 Imm=3 Arith=OpShiftLeft` | 5 | `PSLLD A, Imm`. Immediate lane shifts support only Bits=32 (OpShiftLeft, OpShiftRight, OpRotateLeft and OpRotateRight). |
| `OpVector` | `V=A:3,B:4 Bits=32 Imm=3 Arith=OpShiftRight` | 9 | `MOVDQA A, B` then `PSRLD`. |
| `OpVector` | `V=A:3,B:3 Bits=32 Imm=32 Arith=OpShiftRight` | 4 | A shift count of 32 or more becomes `PXOR A, A`. |
| `OpVector` | `V=A:3,B:3 Bits=32 Imm=7 Arith=OpRotateLeft` | 18 | A rotate is two shifts and `POR`, with XMM0 as a temporary. The count is taken mod 32. |
| `OpVector` | `V=A:3,B:3 Bits=16 Imm=3 Arith=OpShiftLeft` | rejected | Only 32-bit lanes. |
| `OpVector` | `V=A:3,B:3 Bits=32 Imm=3 Arith=OpShiftRightArithmetic` | rejected | Arithmetic lane shifts are not implemented. |
| `OpVector` | `V=A:3,B:3,C:4 Bits=32 Arith=OpShiftLeftBy` | rejected | Variable-count lane shifts are not implemented. |
| `OpVector` | `V=A:3,B:3,C:4 Bits=64 Imm=1 Arith=OpVCarrylessMul` | 6 | `PCLMULQDQ A, C, sel`. Imm bit 0 selects the qword of B and bit 1 the qword of C. Encoded whatever the CPU profile, so it requires PCLMULQDQ. |
| `OpVector` | `V=A:3,B:4,C:11 Bits=64 Imm=3 Arith=OpVCarrylessMul` | 11 | `MOVDQA A, B` comes first. REX for XMM11. |
| `OpVector` | `V=A:3,B:3,C:4 Bits=32 Imm=1 Arith=OpVCarrylessMul` | rejected | Bits must be 64, and Imm must be 0–3. |
| `OpVector` | `V=A:3,B:3,C:4 Bits=8 Imm=0 Arith=OpVCompare` | 4 | `PCMPEQB`. Only Bits=8 with Imm=Equal (0). |
| `OpVector` | `V=A:3,B:3,C:4 Bits=32 Imm=0 Arith=OpVCompare` | rejected | Only byte equality compares. |

## Barriers

`OpBarrier` reads and writes no registers or flags. `Barrier` names the
guarantee; `Policy.Barrier` reports whether the target's instruction rests on
the instruction set or also on host configuration.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpBarrier` | `Barrier=Speculation` | 3 | `LFENCE`. Platform: the host must make LFENCE dispatch-serializing. |
| `OpBarrier` | `Barrier=Memory` | 3 | `MFENCE`. |
| `OpBarrier` | `Barrier=Instruction` | unsupported | Code written by another thread needs a serializing instruction, and none leaves registers intact. |
| `OpBarrier` | — | rejected | Barrier must name a guarantee. |
