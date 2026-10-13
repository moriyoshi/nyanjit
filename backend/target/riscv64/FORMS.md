# riscv64 instruction forms

This table lists the `machine.Instruction` forms that the riscv64 byte
assembler accepts, with their operand constraints and encoded sizes.

The table makes these assumptions:

- The assembler is `NewByteAssembler(abi.StackPlatform, 0)`. Stack slots are
  addressed from SP, the return address is at 0(SP), and there are no Go
  argument homes. Spill slot `n` is at `8*(n+1)`(SP).
- The CPU is the zero baseline (`abi.ResolvedCPU{}`), so there is no Zba or Zbb
  selection. Sizes are for uncompressed RV64IMFD words, 4 bytes each.
  `OpCountZeros` and `OpAndNot` always emit Zbb words; instruction selection
  gates them on the CPU profile.
- Each example is assembled alone at PC 0. Branch and literal targets are
  absolute PCs: `Imm` for branches and `Disp` for literal loads. A branch
  needs a target that is non-negative and 2-byte aligned.
- The encodings do not depend on the ABI flavor. The table assumes the C
  flavor for register roles.

GP register numbers are the architectural x0..x31, and `Policy.RegisterName`
prints them as `X<n>`. Register numbers 32 and above are not GP registers.
Floating-point registers are written as `backend.VectorRegister(n)` for
f0..f31 (`V=A:n` in the examples). `OpSaveCFloat` and `OpRestoreCFloat` are the
exception: they take the raw FP number in `A`. Expansions use x30 and x31 as
scratch registers, and the allocator never assigns them. `OpCallC` and
`OpCallGo` jump through x31. Float operations that have GP operands go through
f0..f2. RISC-V has no condition flags: `OpCompareImmediate` and
`OpCompareRegister` only record the operand pair (with `Imm` != 0, they also
load the constant into x31), and the next `OpJumpIf` encodes that pair into
its `B<cond>` instruction. An `OpJumpIf` assembled alone compares x0 with x0.
The comparison mode does not matter, because the branch condition chooses
signed or unsigned. `PhysicalSize` is the authority for the sizes of forms that
are not listed here.

## Constants, symbols and moves

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpConst` | `A=10 Imm=3` | 4 | `addi` for a signed 12-bit value |
| `OpConst` | `A=10 Imm=0xffffffffffffffff` | 4 | -1 fits `addi` |
| `OpConst` | `A=10 Imm=0x1000` | 4 | `lui` alone when the low 12 bits are zero |
| `OpConst` | `A=10 Imm=0x12345` | 8 | signed 32-bit values: `lui` + `addiw` |
| `OpConst` | `A=10 Imm=0x80000000` | 8 | wider values recurse on the upper part, then `slli 12` (+ `addi`) |
| `OpConst` | `A=10 Imm=0x123456789` | 16 | |
| `OpConst` | `A=10 Imm=0x123456789abcdef0` | 32 | uses only A |
| `OpConst` | `A=32 Imm=1` | rejected | A must be a GP register |
| `OpSymbol` | `A=10 Sym=foo` | 60 | fixed `addi` + 7×(`slli 8`, `ori`) sequence patched by `RelocRISCV64ByteSequence` |
| `OpMove` | `A=10 B=11` | 4 | `addi A, B, 0` |
| `OpMove` | `A=10 B=10` | 0 | a self-move emits nothing |
| `OpMove` | `V=A:1 B=10` | rejected | GP only; use `OpFloatMove` for FP |

## Integer arithmetic

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpBinary` | `A=10 B=11 C=12 Bits=64 Arith=OpAdd` | 4 | A = B op C; Arith is OpAdd, OpSub, OpMul, OpAnd, OpOr or OpXor |
| `OpBinary` | `A=10 B=10 C=11 Bits=32 Arith=OpMul` | 4 | Bits is 0, 8, 16, 32 or 64; the full 64-bit op is always emitted (no `*w` form) |
| `OpBinary` | `A=10 B=11 C=12 Bits=12 Arith=OpAdd` | rejected | invalid width |
| `OpBinary` | `A=10 B=11 C=12 Bits=64 Arith=OpShiftLeft` | rejected | shifts use `OpShift`/`OpShiftBy` |
| `OpBinary` | `A=10 B=11 C=12 Bits=64 Arith=OpAdd PreserveFlags` | unsupported | no flags to preserve, so no flag-neutral variant |
| `OpBinaryImmediate` | `A=10 B=11 Imm=2047 Bits=64 Arith=OpAdd` | 4 | `addi`/`andi`/`ori`/`xori`; Imm is signed 12-bit |
| `OpBinaryImmediate` | `A=10 B=11 SImm=-2048 Bits=64 Arith=OpAnd` | 4 | |
| `OpBinaryImmediate` | `A=10 B=11 Imm=2048 Bits=64 Arith=OpSub` | 4 | OpSub negates Imm, so its range is -2047..2048 |
| `OpBinaryImmediate` | `A=10 B=11 Imm=2048 Bits=64 Arith=OpAdd` | rejected | outside 12-bit range |
| `OpBinaryImmediate` | `A=10 B=11 Imm=3 Bits=64 Arith=OpMul` | rejected | no multiply-immediate |
| `OpBinaryImmediate` | `A=10 B=11 Imm=3 Bits=64 Arith=OpAdd PreserveFlags` | unsupported | no flag-neutral variant |
| `OpBinaryCarry` | `A=10 B=11 C=12 Arith=OpAdd` | unsupported | becomes `OpWordCarry` with Bits=32, which this target rejects |
| `OpInc` | `A=10 B=10` | unsupported | use `OpBinaryImmediate` |
| `OpDec` | `A=10 B=10` | 4 | `addi A, B, -1` |
| `OpDec` | `A=10 B=10 PreserveFlags` | rejected | no flag-neutral variant (none needed) |
| `OpMulAdd` | `A=10 B=11 C=12 D=13` | 8 | A = B + C*D through x30 |
| `OpMulAdd` | `A=10 B=11 C=12 D=32` | rejected | GP only |
| `OpMulHigh` | `A=10 B=11 C=12 Bits=64` | 4 | `mulhu`: high 64 bits of unsigned B*C |
| `OpMulHigh` | `A=10 B=11 C=12 Bits=32` | 8 | Bits 8/16/32: `mul` + `srli Bits`; inputs must be zero-extended |
| `OpMulHigh` | `A=10 B=11 C=12 Bits=12` | rejected | invalid width |
| `OpMulHighSigned` | `A=10 B=11 C=12 Bits=64` | 4 | `mulh`: high 64 bits of signed B*C |
| `OpMulHighSigned` | `A=10 B=11 C=12 Bits=16` | 16 | Bits 8/16/32: `slli 64-Bits` of B and C into x30 and x31, `mulh`, then `srli 64-Bits`; inputs must be zero-extended |
| `OpMulHighSigned` | `A=10 B=11 C=12 Bits=12` | rejected | invalid width |
| `OpMulWide` | `A=5` | rejected | 386 only: `machine.Visit` returns `ErrUnsupportedOperation`. Use `OpMulHigh`. |
| `OpCarryBit` | `A=10 B=11 C=12 Bits=64 Arith=OpAdd` | 8 | A = carry of B+C (`add x31`, `sltu`) |
| `OpCarryBit` | `A=10 B=11 C=12 Bits=64 Arith=OpSub` | 4 | A = borrow of B-C (`sltu`) |
| `OpCarryBit` | `A=10 B=11 C=12 Bits=32 Arith=OpAdd` | rejected | Bits must be 64 |
| `OpCarryBit` | `A=10 B=11 C=12 Bits=64 Arith=OpAdd Carry=3` | rejected | no carry flags to read (CarryFlags) |
| `OpWordCarry` | `A=10 B=11 C=12 D=13 Bits=64 Arith=OpAdd` | 12 | A = B op C op (D&1); Carry=0/1 take the carry bit from D |
| `OpWordCarry` | `A=10 B=11 C=12 D=13 E=14 Bits=64 Arith=OpAdd Carry=2` | 16 | CarryOperands: carry from low words D+E |
| `OpWordCarry` | `A=10 B=11 C=12 D=13 E=14 Bits=64 Arith=OpSub Carry=2` | 12 | borrow from low words D-E |
| `OpWordCarry` | `A=10 B=11 C=12 D=13 Bits=64 Arith=OpMul` | rejected | Arith is OpAdd or OpSub; uses x30 and x31 |
| `OpWordCarry` | `A=10 B=11 C=12 D=13 Bits=64 Arith=OpAdd Carry=3` | rejected | CarryFlags: riscv64 has no carry flags |
| `OpAndNot` | `A=10 B=11 C=12 Bits=64` | 4 | Zbb `andn`: A = B &^ C |
| `OpAndNot` | `A=10 B=11 C=12 Bits=0` | rejected | Bits is 8, 16, 32 or 64 |
| `OpCountZeros` | `A=10 B=11 Bits=64 Arith=OpLeadingZeros` | 4 | Zbb `clz`/`ctz` |
| `OpCountZeros` | `A=10 B=11 Bits=32 Arith=OpTrailingZeros` | 4 | Bits ≤ 32 uses `clzw`/`ctzw` |
| `OpCountZeros` | `A=10 B=11 Bits=16 Arith=OpLeadingZeros` | 8 | narrower leading counts subtract the excess with `addi` |
| `OpCountZeros` | `A=10 B=11 Bits=64 Arith=OpShiftLeft` | rejected | Arith is OpLeadingZeros or OpTrailingZeros |
| `OpDivide` | `A=10 B=11 C=12 Bits=64 Arith=OpDiv Imm=1` | 4 | A = B / C; Imm=1 (DivisionAllOnes) is what `div`/`divu`/`rem`/`remu` compute natively |
| `OpDivide` | `A=10 B=11 C=12 Bits=64 Arith=OpDivSigned Imm=2` | 4 | Imm=2 (DivisionUnchecked) is the same bare instruction |
| `OpDivide` | `A=10 B=11 C=12 Bits=64 Arith=OpDivSigned Imm=0` | 12 | Imm=0 (DivisionZero): `seqz x31, C`, `div`, `add x31` turns the all-ones x/0 into 0 |
| `OpDivide` | `A=10 B=10 C=10 Bits=64 Arith=OpRemSigned Imm=0` | 4 | remainders are the same for every semantics; A may alias B and C |
| `OpDivide` | `A=10 B=11 C=12 Bits=32 Arith=OpDiv Imm=1` | 12 | narrow unsigned: 64-bit `divu`, then x/0's all ones is zero-extended to Bits |
| `OpDivide` | `A=10 B=11 C=12 Bits=8 Arith=OpDiv Imm=1` | 8 | 8-bit masks use `andi` |
| `OpDivide` | `A=10 B=11 C=12 Bits=32 Arith=OpRem Imm=0` | 4 | |
| `OpDivide` | `A=10 B=11 C=12 Bits=32 Arith=OpDivSigned Imm=1` | 12 | signed 32: `divw`/`remw`, then zero-extend the sign-extended result |
| `OpDivide` | `A=10 B=11 C=12 Bits=32 Arith=OpDivSigned Imm=0` | 20 | |
| `OpDivide` | `A=10 B=11 C=12 Bits=16 Arith=OpDivSigned Imm=0` | 28 | signed 8/16: B, C shifted to the top of x30/x31, 64-bit `div`, mask to Bits |
| `OpDivide` | `A=10 B=11 C=12 Bits=8 Arith=OpRemSigned Imm=1` | 16 | the scaled remainder is shifted back with `srli` |
| `OpDivide` | `A=10 B=11 C=12 Bits=12 Arith=OpDiv Imm=1` | rejected | Bits is 8, 16, 32 or 64 |
| `OpDivide` | `A=10 B=11 C=12 Bits=64 Arith=OpMul Imm=1` | rejected | Arith is OpDiv, OpDivSigned, OpRem or OpRemSigned |
| `OpDivide` | `A=10 B=11 C=12 Bits=64 Arith=OpDiv Imm=3` | rejected | Imm is a DivisionSemantics |
| `OpDivide` | `V=A:1 B=11 C=12 Bits=64 Arith=OpDiv Imm=1` | rejected | GP only |

## Width changes and shifts

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpNarrow` | `A=10 B=11 Imm=32` | 8 | zero-extend low Imm bits (`slli` + `srli`); Imm is 1..64 |
| `OpNarrow` | `A=10 B=11 Imm=64` | 8 | still two shifts by 0 |
| `OpNarrow` | `A=10 B=11 Imm=0` | rejected | |
| `OpSignExtend` | `A=10 B=11 Imm=32` | 4 | `addiw A, B, 0` |
| `OpSignExtend` | `A=10 B=11 Imm=8` | 8 | other widths: `slli` + `srai`; Imm is 1..64 |
| `OpSignExtend` | `A=10 B=11 Imm=65` | rejected | |
| `OpShift` | `A=10 B=11 Bits=64 Imm=3 Arith=OpShiftLeft` | 4 | immediate count in Imm, Imm < Bits; Bits is 8, 16, 32 or 64 |
| `OpShift` | `A=10 B=11 Bits=32 Imm=3 Arith=OpShiftRight` | 4 | 64-bit `srli`; source must be zero-extended |
| `OpShift` | `A=10 B=11 Bits=32 Imm=3 Arith=OpShiftRightArithmetic` | 8 | Bits < 64: `slli x30` + `srai` |
| `OpShift` | `A=10 B=11 Bits=64 Imm=3 Arith=OpShiftRightArithmetic` | 4 | |
| `OpShift` | `A=10 B=11 Bits=64 Imm=5 Arith=OpRotateLeft` | 12 | baseline rotate: two shifts into x30/x31 + `or` (Zbb: one `rori`) |
| `OpShift` | `A=10 B=11 Bits=32 Imm=0 Arith=OpRotateRight` | 4 | zero count is a move |
| `OpShift` | `A=10 B=10 Bits=32 Imm=0 Arith=OpRotateRight` | 0 | zero count in place emits nothing |
| `OpShift` | `A=10 B=11 Bits=64 Imm=64 Arith=OpShiftLeft` | rejected | Imm must be < Bits |
| `OpShift` | `A=10 B=11 Bits=0 Imm=1 Arith=OpShiftLeft` | rejected | invalid width |
| `OpShiftBy` | `A=10 B=11 C=12 Bits=64 Arith=OpShiftLeft` | 16 | register count C; counts ≥ Bits give 0 (sign fill for arithmetic) |
| `OpShiftBy` | `A=10 B=11 C=12 Bits=32 Arith=OpShiftRight` | 24 | Bits < 64 add a 2-word zero-extension of A |
| `OpShiftBy` | `A=10 B=11 C=12 Bits=64 Arith=OpShiftRightArithmetic` | 16 | |
| `OpShiftBy` | `A=10 B=11 C=12 Bits=32 Arith=OpShiftRightArithmetic` | 32 | |
| `OpShiftBy` | `A=10 B=11 C=12 Bits=64 Arith=OpRotateLeft` | 20 | rotate count is taken modulo Bits |
| `OpShiftBy` | `A=10 B=11 C=12 Bits=32 Arith=OpRotateRight` | 28 | |
| `OpShiftBy` | `A=10 B=11 C=12 Bits=8 Arith=OpRotateLeft` | 44 | 8/16-bit sources are replicated to 32 bits first |
| `OpShiftBy` | `A=10 B=11 C=12 Bits=12 Arith=OpShiftLeft` | rejected | Bits is 8, 16, 32 or 64; uses x30 and x31; A may alias B or C |
| `OpShiftBy` | `A=10 B=11 C=12 Bits=64 Arith=OpAdd` | rejected | Arith is a shift or rotate |

## Addressing and selection

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpScaled` | `A=10 B=11 C=12 Imm=1` | 4 | A = B + C*Imm; scale 1 is one `add` |
| `OpScaled` | `A=10 B=11 C=12 Imm=8` | 8 | power-of-two scale: `slli x30` + `add` (Zba: one `shNadd`) |
| `OpScaled` | `A=10 B=11 C=12 Imm=3` | 12 | other scales: constant in x30, `mul`, `add` |
| `OpScaled` | `A=10 B=11 C=12 Imm=8 Disp=8` | rejected | Disp must be 0 |
| `OpSelect` | `A=10 B=11 C=12 D=13` | 16 | A = B != 0 ? C : D (`beq`, `mv`, `j`, `mv`) |
| `OpSelect` | `A=10 B=11 C=12 D=32` | rejected | GP only |
| `OpSelectFlags` | `A=10 C=12 D=13 Cond=Equal` | unsupported | no flags; use `OpSelect` |

## Memory

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpLoad64` | `A=10 B=11` | 4 | `ld A, 0(B)` |
| `OpLoad64` | `A=10 B=11 BigEndian` | rejected | only native little-endian order |
| `OpStore64` | `A=11 B=10` | 4 | `sd B, 0(A)`: A is the address, B the value |
| `OpLoad8` | `A=10 B=11` | 4 | `lbu`: zero-extends |
| `OpLoad8` | `A=10 B=11 BigEndian` | 4 | byte order is ignored for byte accesses |
| `OpStore8` | `A=11 B=10` | 4 | `sb B, 0(A)` |
| `OpLoad16` | `A=10 B=11` | 4 | `lhu`: A is the destination, B the address |
| `OpLoad16` | `A=10 B=11 BigEndian` | rejected | little-endian only |
| `OpStore16` | `A=11 B=10` | 4 | `sh`: A is the address, B the value |
| `OpLoad32` | `A=10 B=11` | 4 | `lwu`: zero-extends |
| `OpStore32` | `A=11 B=10` | 4 | `sw` |
| `OpStore32` | `A=32 B=10` | rejected | GP only |
| `OpLoadOffset` | `A=10 B=11 Disp=2047` | 4 | `ld A, Disp(B)`; Disp is signed 64-bit |
| `OpLoadOffset` | `A=10 B=11 Disp=0xfffffffffffff800` | 4 | -2048 fits the 12-bit field |
| `OpLoadOffset` | `A=10 B=11 Disp=2048` | 16 | far Disp: constant in x30 (x31 when B is x30), `add`, `ld` |
| `OpLoadOffset` | `A=10 B=30 Disp=4096` | 12 | |
| `OpLoadOffset` | `A=10 B=11 Disp=0x123456789` | 24 | |
| `OpStoreOffset` | `A=11 B=10 Disp=8` | 4 | `sd B, Disp(A)` |
| `OpStoreOffset` | `A=11 B=10 Disp=4096` | 12 | |
| `OpLoadAddress` | `A=10 B=11 C=12 Imm=8 Disp=16` | 12 | load from B + C*Imm + Disp; address built in x30 as for `OpScaled` |
| `OpLoadAddress` | `A=10 B=11 C=12 Imm=1` | 8 | |
| `OpLoadAddress` | `A=10 B=11 C=12 Imm=8 Disp=4096` | 20 | far Disp adds a constant in x31 + `add` |
| `OpStoreAddress` | `A=11 B=12 C=10 Imm=8 Disp=16` | 12 | A base, B index, C value |
| `OpStoreAddress` | `A=11 B=12 C=32 Imm=8` | rejected | GP only |
| `OpLiteralLoad` | `V=A:1 Bits=64 Disp=64` | 8 | `auipc x31` + `fld`; Disp is the absolute pool PC |
| `OpLiteralLoad` | `V=A:1 Bits=32 Disp=64` | 8 | `flw` |
| `OpLiteralLoad` | `A=10 Bits=64 Disp=64` | rejected | A must be an FP register |
| `OpLiteralLoad` | `V=A:1 Bits=16 Disp=64` | rejected | Bits is 32 or 64 |
| `OpLiteralLoad` | `V=A:1 Bits=128 Disp=64` | rejected | |
| `OpLiteralLoad` | `V=A:1 Bits=64 Disp=0x80000000` | rejected | beyond `auipc` reach |
| `OpLiteralData` | `Data=0011223344556677` | 8 | raw pool bytes |
| `OpLiteralData` | `Data=001122` | rejected | length must be a multiple of 4 |

## Stack frame, slots and calls

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpFrameEnter` | `Imm=32` | 8 | `sd ra, -Imm(sp)` + `addi sp, sp, -Imm`: RA is stored before SP moves |
| `OpFrameEnter` | `Imm=0` | 4 | only stores RA |
| `OpFrameEnter` | `Imm=2048` | 12 | one word per 2032 bytes plus one; a frame over 2048 bytes is addressed through X31, which stores RA and then moves SP once; leading NOPs pad |
| `OpFrameEnter` | `Imm=1048576` | 2072 | 1 MiB maximum |
| `OpFrameEnter` | `Imm=1048577` | rejected | over 1 MiB |
| `OpFrameEnter` | `Imm=32 CallArea=16` | rejected | no call-area form; CallArea must be 0 |
| `OpFrameLeave` | `Imm=32` | 8 | `ld ra, 0(sp)` + `addi sp, sp, Imm` |
| `OpFrameLeave` | `Imm=2048` | 12 | |
| `OpFrameLeave` | `Imm=1048577` | rejected | |
| `OpReturn` | — | 4 | `jalr x0, 0(ra)` |
| `OpSaveLink` | `Slot=0` | 0 | StackPlatform keeps RA in the frame; nothing to emit |
| `OpSaveLink` | `Slot=0xffffffffffffffff` | rejected | Slot must be non-negative |
| `OpRestoreLink` | `Slot=0` | 0 | |
| `OpSpill` | `A=10 Slot=0` | 4 | `sd A, 8*(Slot+1)(sp)` |
| `OpSpill` | `A=10 Slot=254` | 4 | largest slot in the 12-bit range |
| `OpSpill` | `A=10 Slot=255` | 16 | far offset through x30 |
| `OpSpill` | `V=A:1 Slot=0` | rejected | GP only; use `OpFloatSpill` |
| `OpReload` | `A=10 Slot=0` | 4 | |
| `OpReload` | `A=10 Slot=255` | 16 | |
| `OpLoadIncoming` | `A=10 Imm=16` | 4 | `ld A, Imm(sp)`: Imm is the SP offset at entry |
| `OpLoadIncoming` | `A=10 Imm=4096` | 12 | far offset through x30 |
| `OpSaveCFloat` | `A=8 Slot=0` | 4 | `fsd f<A>, 8*(Slot+1)(sp)`; A is the raw FP number 0..31 |
| `OpSaveCFloat` | `A=8 Slot=255` | 16 | |
| `OpSaveCFloat` | `V=A:8 Slot=0` | rejected | not a `VectorRegister` |
| `OpRestoreCFloat` | `A=8 Slot=0` | 4 | `fld` |
| `OpRestoreCFloat` | `A=8 Slot=255` | 16 | |
| `OpSaveCVector` | — | unsupported | |
| `OpRestoreCVector` | — | unsupported | |
| `OpSaveCondition` | — | unsupported | no condition register |
| `OpRestoreCondition` | — | unsupported | |
| `OpCallC` | — | 4 | `jalr ra, 0(x31)`: target in x31 |
| `OpCallGo` | — | 4 | same as `OpCallC` |
| `OpSyscall` | — | 4 | `ecall` |

## Compares and branches

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpCompareRegister` | `A=10 B=11` | 0 | records A, B for the next `OpJumpIf` |
| `OpCompareRegister` | `A=10 B=11 Cmp=Unsigned` | 0 | any comparison mode |
| `OpCompareRegister` | `A=10 B=11 Bits=32` | rejected | Bits is 0 or 64; operands are pre-normalized |
| `OpCompareImmediate` | `A=10 Imm=0` | 0 | compares A with x0 |
| `OpCompareImmediate` | `A=10 SImm=-1 Cmp=Signed` | 4 | other immediates: `OpConst` sequence into x31 |
| `OpCompareImmediate` | `A=10 Imm=0x12345` | 8 | |
| `OpCompareImmediate` | `A=10 Imm=5 Bits=32` | rejected | Bits is 0 or 64 |
| `OpCompareSet` | `A=10 B=11 C=12 Bits=64 Cond=Less` | 4 | A = 1 when the Bits-wide integers in B and C satisfy Cond, else 0: `slt` (`sltu` for unsigned conditions), with B and C swapped for Greater, LessEqual, Above and BelowEqual. A may alias B or C. |
| `OpCompareSet` | `A=10 B=11 C=12 Bits=64 Cond=GreaterEqual` | 8 | GreaterEqual, LessEqual, AboveEqual and BelowEqual negate with `xori A, A, 1`. |
| `OpCompareSet` | `A=10 B=11 C=12 Bits=16 Cond=Equal` | 8 | `xor A, B, C` then `seqz A, A` (`snez` for NotEqual). Equality and unsigned conditions compare zero-extended narrow operands directly. |
| `OpCompareSet` | `A=10 B=11 C=12 Bits=32 Cond=Less` | 12 | Signed 8-, 16- and 32-bit conditions first shift B and C to the top of x30 and x31 (`slli` by 64-Bits), which orders them as Bits-wide values. Writes x30 and x31. |
| `OpCompareSet` | `A=10 B=11 C=12 Bits=8 Cond=LessEqual` | 16 | |
| `OpCompareSet` | `A=10 B=11 C=30 Bits=8 Cond=Less` | rejected | C must not be x30 when the operands are shifted: x30 is written first. |
| `OpCompareSet` | `A=10 B=11 C=12 Bits=12 Cond=Less` | rejected | Bits must be 8, 16, 32 or 64. |
| `OpCompareSet` | `A=10 B=11 C=12 Bits=64 Cond=Sign` | rejected | Cond must be Equal through Above. |
| `OpReverseBytes` | `A=10 B=11 Bits=16` | 24 | A = B with its bytes reversed. Without Zbb (this profile) each byte is shifted out of B (`srli`, `andi`) through x30, shifted into place and gathered in x31 (`slli`, `or`), then moved to A. Selection uses shifts and masks instead. With Zbb: `rev8 A, B` and `srli A, A, 64-Bits` (none at 64 bits). |
| `OpReverseBytes` | `A=10 B=11 Bits=32` | 56 | |
| `OpReverseBytes` | `A=10 B=11 Bits=64` | 120 | |
| `OpReverseBytes` | `A=10 B=30 Bits=32` | rejected | Without Zbb, B must be neither x30 nor x31. |
| `OpReverseBytes` | `A=10 B=11 Bits=8` | rejected | Bits must be 16, 32 or 64. |
| `OpJump` | `Imm=8` | 4 | `jal x0`, ±1 MiB |
| `OpJump` | `Imm=1048574` | 4 | |
| `OpJump` | `Imm=1048576` | rejected | out of `jal` range |
| `OpJump` | `Imm=1048576 Long` | 8 | `auipc x30` + `jalr`, ±2 GiB |
| `OpJump` | `Imm=3` | rejected | target must be 2-byte aligned |
| `OpJumpIf` | `Cond=Equal Imm=8` | 4 | `B<cond>` on the compared pair, ±4 KiB |
| `OpJumpIf` | `Cond=Less Imm=4094` | 4 | signed and unsigned orders swap operands for LessEqual/Greater |
| `OpJumpIf` | `Cond=Above Imm=4096` | rejected | short form is out of range |
| `OpJumpIf` | `Cond=Above Imm=4096 Long` | 8 | inverted `B<cond>` over `jal`, ±1 MiB |
| `OpJumpIf` | `Cond=Equal Imm=1048580 Long` | rejected | out of `jal` range |
| `OpJumpIf` | `Cond=Equal Imm=1048580 Long Far` | 12 | inverted `B<cond>` over `auipc x30` + `jalr`; Far requires Long |
| `OpJumpIf` | `Cond=Equal Imm=8 Far` | 4 | Far without Long is ignored |
| `OpJumpIf` | `Cond=Sign Imm=8` | rejected | conditions Equal..Above only; no flag conditions |
| `OpJumpIf` | `Cond=Carry Imm=8` | rejected | |
| `OpBranchZero` | `A=10 Bits=64 Cond=Equal Imm=8` | 4 | `beq A, x0` / `bne A, x0`; tests the full register |
| `OpBranchZero` | `A=10 Bits=32 Cond=NotEqual Imm=4096 Long` | 8 | |
| `OpBranchZero` | `A=10 Bits=64 Cond=NotEqual Imm=0x200000 Long Far` | 12 | |
| `OpBranchZero` | `A=10 Bits=0 Cond=Equal Imm=8` | rejected | Bits is 8, 16, 32 or 64 |
| `OpBranchZero` | `A=10 Bits=64 Cond=Less Imm=8` | rejected | Equal or NotEqual only |
| `OpBranchBit` | `A=10 Bits=64 Disp=3 Cond=NotEqual Imm=8` | unsupported | |
| `OpLoop` | `Imm=8` | unsupported | no loop counter; zero loops are fused by `FuseLoops` |
| `OpSetCount` | `A=10` | unsupported | |
| `OpJumpTable` | `A=10 B=11 Table=64,128,192` | 64 | Bounded switch: `li x31, n` (n = len(Table)-1), then a branchless clamp (`sltu b, a, x31`; `sub x30, a, x31`; `neg b, b`; `and x30, x30, b`; `add b, x31, x30`) so entry n is the default, `auipc x30, 0`; `addi x30, x30, 28`; `slli b, b, 2`; `add b, x30, b`; `lw b, 0(b)`; `add b, x30, b`; `jr b`, then one 32-bit table-relative entry per target. Writes B, x30 and x31; A is preserved. Targets are 2-byte aligned. |
| `OpJumpTable` | `A=10 B=30 Table=64,128` | rejected | x30 and x31 are the sequence's temporaries; x0 is no operand. |
| `OpJumpTable` | `A=10 B=11 Table=64,129` | rejected | Targets must be 2-byte aligned. |
| `OpJumpTable` | `A=10 B=11 Table=64` | rejected | The table needs a case and the default. |

## Floating point

FP operands are `VectorRegister(n)` for f<n>. A GP operand is also accepted.
Each GP input is moved into a scratch register f0, f1 or f2 (`fmv.w.x` or
`fmv.d.x`). A GP destination receives the result through `fmv.x.w` or
`fmv.x.d`, and a 32-bit result is then zero-extended with two more words.
`Bits` is 32 or 64.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpFloatBinary` | `V=A:1,B:2,C:3 Bits=64 Arith=OpFAdd` | 4 | OpFAdd, OpFSub, OpFMul, OpFDiv; dynamic rounding |
| `OpFloatBinary` | `V=A:1,B:2 C=10 Bits=64 Arith=OpFSub` | 8 | GP input C goes through f1 |
| `OpFloatBinary` | `A=10 B=11 C=12 Bits=32 Arith=OpFMul` | 24 | all-GP operands, 32-bit result |
| `OpFloatBinary` | `V=A:1,B:2,C:3 Bits=64 Arith=OpFSqrt` | rejected | unary op |
| `OpFloatBinary` | `V=A:1,B:2,C:3 Bits=16 Arith=OpFAdd` | rejected | Bits is 32 or 64 |
| `OpFloatFMA` | `V=A:1,B:2,C:3,D:4 Bits=64` | 4 | `fmadd`: A = B*C + D |
| `OpFloatFMA` | `A=10 B=11 C=12 D=13 Bits=32` | 28 | |
| `OpFloatFMA` | `V=A:1,B:2,C:3,D:4 Bits=16` | rejected | |
| `OpFloatUnary` | `V=A:1,B:2 Bits=64 Arith=OpFSqrt` | 4 | |
| `OpFloatUnary` | `V=A:1,B:2 Bits=64 Arith=OpFConvert` | 4 | Bits is the destination width (`fcvt.d.s`, `fcvt.s.d`) |
| `OpFloatUnary` | `A=10 B=11 Bits=64 Arith=OpFSqrt` | 12 | |
| `OpFloatUnary` | `V=A:1,B:2 Bits=64 Arith=OpFAdd` | rejected | OpFSqrt or OpFConvert only |
| `OpFloatBits` | `V=A:1,B:2 Bits=64 Arith=OpXor` | 4 | OpXor flips the sign (`fsgnjn`) |
| `OpFloatBits` | `V=A:1,B:2 Bits=32 Arith=OpAnd` | 4 | OpAnd clears the sign (`fsgnjx`) |
| `OpFloatBits` | `A=10 B=11 Bits=64 Arith=OpXor` | 12 | |
| `OpFloatBits` | `V=A:1,B:2 Bits=64 Arith=OpOr` | rejected | OpAnd or OpXor only |
| `OpFloatMove` | `V=A:1,B:2 Bits=64` | 4 | `fsgnj` |
| `OpFloatMove` | `V=A:1,B:1 Bits=64` | 0 | a self-move emits nothing |
| `OpFloatMove` | `V=A:1 B=10 Bits=64` | 4 | GP to FP: `fmv.d.x` |
| `OpFloatMove` | `A=10 V=B:1 Bits=32` | 12 | FP to GP: `fmv.x.w`, then zero-extend |
| `OpFloatMove` | `A=10 B=11 Bits=64` | rejected | at least one operand must be FP |
| `OpFloatMove` | `V=A:1,B:2` | rejected | Bits is 32 or 64 |
| `OpFloatSpill` | `V=A:1 Slot=0 Bits=64` | 4 | `fsd` at the `OpSpill` slot address; always 64-bit |
| `OpFloatSpill` | `V=A:1 Slot=255 Bits=32` | 16 | far offset through x30 |
| `OpFloatSpill` | `A=10 Slot=0 Bits=64` | rejected | A must be an FP register |
| `OpFloatReload` | `V=A:1 Slot=0 Bits=64` | 4 | `fld` |
| `OpFloatReload` | `V=A:1 Slot=0 Bits=16` | rejected | Bits is 0, 32 or 64 |
| `OpIntToFloat` | `V=A:1 B=10 Bits=64 Imm=64 Arith=OpFFromUnsigned` | 4 | `fcvt.d.lu`, dynamic rounding; Bits is the float width, Imm the integer width |
| `OpIntToFloat` | `V=A:1 B=10 Bits=32 Imm=32 Arith=OpFFromSigned` | 4 | `fcvt.s.w` reads the low word; the binary32 result is NaN-boxed |
| `OpIntToFloat` | `A=10 B=11 Bits=64 Imm=64 Arith=OpFFromSigned` | rejected | A must be an FP register and B a GP register |
| `OpIntToFloat` | `V=A:1 B=10 Bits=64 Imm=16 Arith=OpFFromSigned` | rejected | Imm is 32 or 64 |
| `OpFloatToInt` | `A=10 V=B:1 Bits=64 Disp=64 Arith=OpFToSigned Imm=1` | 4 | Imm=1 (ConversionNative): `fcvt.l.d` with rtz; Disp is the float width |
| `OpFloatToInt` | `A=10 V=B:1 Bits=32 Disp=32 Arith=OpFToUnsigned Imm=1` | 12 | 32-bit results are sign-extended by the hardware, then zero-extended |
| `OpFloatToInt` | `A=10 V=B:1 Bits=64 Disp=32 Arith=OpFToSigned Imm=0` | 16 | Imm=0 (ConversionSaturate): the hardware saturates; `feq x31`, `sub`, `and` turn NaN into 0 |
| `OpFloatToInt` | `A=10 V=B:1 Bits=32 Disp=64 Arith=OpFToSigned Imm=0` | 20 | the 32-bit mask in x31 also zero-extends the result |
| `OpFloatToInt` | `A=10 V=B:1 Bits=64 Disp=64 Arith=OpFToSigned Imm=2` | rejected | Imm is a ConversionSemantics |
| `OpFloatToInt` | `A=10 B=11 Bits=64 Disp=64 Arith=OpFToSigned Imm=0` | rejected | B must be an FP register |
| `OpFloatCompare` | `A=10 V=B:1,C:2 Bits=64 Imm=0` | 4 | A = 1 when B, C satisfy the FloatCondition in Imm, else 0. Imm=0 (FloatEqual): `feq.d A, B, C`; Bits=32 uses the `.s` forms |
| `OpFloatCompare` | `A=10 V=B:1,C:2 Bits=32 Imm=2` | 4 | FloatLess `flt`, FloatLessEqual `fle`; FloatGreater and FloatGreaterEqual swap B and C |
| `OpFloatCompare` | `A=10 V=B:1,C:2 Bits=64 Imm=1` | 8 | odd conditions negate their even partner with `xori A, A, 1` (NaN makes every compare 0) |
| `OpFloatCompare` | `A=10 V=B:1,C:2 Bits=64 Imm=10` | 12 | FloatOrdered: `feq A, B, B`, `feq x31, C, C`, `and A, A, x31` |
| `OpFloatCompare` | `A=10 V=B:1,C:2 Bits=32 Imm=12` | 12 | FloatOrderedNotEqual: `flt A, B, C`, `flt x31, C, B`, `or A, A, x31` |
| `OpFloatCompare` | `A=10 V=B:1,C:2 Bits=64 Imm=13` | 16 | FloatUnordered and FloatUnorderedEqual append `xori`; Imm 10..13 write x31 |
| `OpFloatCompare` | `A=31 V=B:1,C:2 Bits=64 Imm=9` | 8 | single-compare conditions may write x31 |
| `OpFloatCompare` | `A=31 V=B:1,C:2 Bits=64 Imm=10` | rejected | A must not be x31 for Imm 10..13 |
| `OpFloatCompare` | `A=10 V=B:1,C:2 Bits=16 Imm=0` | rejected | Bits is 32 or 64 |
| `OpFloatCompare` | `A=10 V=B:1,C:2 Bits=64 Imm=14` | rejected | Imm is a FloatCondition |
| `OpFloatCompare` | `A=10 B=11 V=C:2 Bits=64 Imm=0` | rejected | B and C must be FP registers |
| `OpFloatCompare` | `V=A:3,B:1,C:2 Bits=64 Imm=0` | rejected | A must be a GP register |

## Vector

The riscv64 target has no vector (RVV) support.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpVector` | `V=A:1,B:2,C:3 Bits=32 Arith=OpAdd Vec=4x32` | unsupported | |
| `OpVector` | `V=A:1,B:2,C:3 Bits=8 Imm=0x0706050403020100 Disp=0x08090a0b0c0d0e0f Arith=OpVShuffle` | unsupported | Shuffles and broadcasts are scalarized. |
| `OpVector` | `V=A:1,B:2 Bits=16 Arith=OpVExtendUnsigned` | unsupported | Widening extends are scalarized. |
| `OpVector` | `V=A:1,B:2 Bits=64 Imm=1 Arith=OpVExtendSigned` | unsupported | |
| `OpVectorMove` | `V=A:1,B:2` | unsupported | |
| `OpVectorSpill` | `V=A:1 Slot=0` | unsupported | |
| `OpVectorReload` | `V=A:1 Slot=0` | unsupported | |

## Barriers

`OpBarrier` reads and writes no registers or flags. `Barrier` names the
guarantee; `Policy.Barrier` reports whether the target's instruction rests on
the instruction set or also on host configuration.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpBarrier` | `Barrier=Speculation` | unsupported | No ratified instruction stops speculation. |
| `OpBarrier` | `Barrier=Memory` | 4 | `fence rw, rw` |
| `OpBarrier` | `Barrier=Instruction` | 4 | `fence.i` (Zifencei, part of RV64G) |
| `OpBarrier` | — | rejected | Barrier must name a guarantee. |

## Stack areas

`OpSlotAddress` writes to GP register A the address of spill slot Slot, as
`OpSpill` addresses it: 8*(Slot+1)(sp), above the Go homes when there are
any, or 8*Slot(x9) without a platform stack. A far offset is built in A and
added to the base, so no scratch register is written.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpSlotAddress` | `A=10 Slot=0` | 4 | `addi A, sp, 8*(Slot+1)` |
| `OpSlotAddress` | `A=10 Slot=254` | 4 | largest slot in the 12-bit range |
| `OpSlotAddress` | `A=10 Slot=255` | 12 | `lui`/`addi` of the offset into A, then `add A, sp, A` |
| `OpSlotAddress` | `A=2 Slot=0` | rejected | A must not be the base register sp |
| `OpSlotAddress` | `A=0 Slot=0` | rejected | A must not be x0 |
| `OpSlotAddress` | `V=A:1 Slot=0` | rejected | GP only |

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
| `OpPrefetch` | `A=10 Imm=0` | 4 | Zicbop `prefetch.r 0(a0)`, which is `ori x0, a0, 1`. A core without Zicbop runs it as a no-op, so no CPU profile is needed. |
| `OpPrefetch` | `A=10 Imm=1` | 4 | `prefetch.w 0(a0)`, which is `ori x0, a0, 3`. |
| `OpPrefetch` | `A=0 Imm=0` | 4 | A base of x0 prefetches address 0. |
| `OpPrefetch` | `A=10 Imm=2` | rejected | Imm must be an `op.PrefetchKind`. |
| `OpPrefetch` | `V=A:1 Imm=0` | rejected | A must be a GP register. |


## Tail calls

`OpTailCall` ends a function with a C tail call. The arguments are in place
and the target is in the call-target register, as for `OpCallC`. It leaves
a frame of Imm bytes, as `OpFrameLeave` does (none when Imm is zero), and
jumps to the target, so the callee returns to this function's caller.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpTailCall` | `Imm=0` | 4 | `jalr x0, 0(t6)`. |
| `OpTailCall` | `Imm=32` | 12 | `ld ra, 0(sp); addi sp, sp, 32; jalr x0, 0(t6)`. |
| `OpTailCall` | `Imm=2097152` | rejected | Frame adjustments are bounded to 1 MiB. |

## Atomics

`OpAtomic` performs the sequentially consistent atomic `Arith`
(`OpAtomicLoad` … `OpAtomicCompareSwap`) at width `Bits` on the naturally
aligned address in B. A receives the old value, zero-extended; C is the
operand (the expected value for compare-and-swap) and D the replacement. `Imm`
is the `op.MemoryOrder` (only 0, sequential). A store reads no A and a load
no C. A must differ from every input it is not, and must not be x0. The
mappings follow the RVWMO table (unprivileged spec, Table A.6): loads are
`fence rw, rw; l; fence r, rw`, stores `fence rw, w; s`, 32- and 64-bit
read-modify-writes one `amo<op>.aqrl`, and compare-and-swap an
`lr.aqrl`/`sc.rl` loop. `lr.w` and `amo*.w` sign-extend, so 32-bit results are
zero-extended again, and the 32-bit compare-and-swap compares against a
sign-extended copy of C in x31. Byte and halfword read-modify-writes work on
the aligned word: And, Or and Xor with one word AMO whose other bytes are the
identity, Add, Swap and compare-and-swap with a masked `lr.w`/`sc.w` loop.
Every loop is an RVWMO constrained LR/SC loop of at most seven instructions,
with setup and result extraction outside it.

Besides x30 and x31, the masked loops write fixed allocatable temporaries,
which no operand may name: 8/16-bit Add and Swap use x5, x6 and x7, and 8/16-bit
compare-and-swap x5, x6, x7 and x28. `ImplicitWrites` reports them (with
x30/x31) from `Arith` and `Bits` alone.

| Opcode | Example | Bytes | Constraints |
| --- | --- | --- | --- |
| `OpAtomic` | `A=17 B=14 Bits=8 Arith=OpAtomicLoad` | 12 | `fence rw, rw; lbu; fence r, rw`. Naturally aligned bytes and halfwords are single-copy atomic. |
| `OpAtomic` | `A=17 B=14 Bits=16 Arith=OpAtomicLoad` | 12 | `lhu`. |
| `OpAtomic` | `A=17 B=14 Bits=32 Arith=OpAtomicLoad` | 12 | `lwu`. |
| `OpAtomic` | `A=17 B=14 Bits=64 Arith=OpAtomicLoad` | 12 | `ld`. |
| `OpAtomic` | `B=14 C=15 Bits=8 Arith=OpAtomicStore` | 8 | `fence rw, w; sb`. |
| `OpAtomic` | `B=14 C=15 Bits=16 Arith=OpAtomicStore` | 8 | `sh`. |
| `OpAtomic` | `B=14 C=15 Bits=32 Arith=OpAtomicStore` | 8 | `sw`. |
| `OpAtomic` | `B=14 C=15 Bits=64 Arith=OpAtomicStore` | 8 | `sd`. |
| `OpAtomic` | `A=17 B=14 C=15 Bits=8 Arith=OpAtomicAdd` | 68 | Masked `lr.w`/`sc.w` loop. Writes x5, x6, x7, x30, x31. |
| `OpAtomic` | `A=17 B=14 C=15 Bits=16 Arith=OpAtomicAdd` | 72 | As 8-bit; the halfword mask takes `lui`/`addiw`. |
| `OpAtomic` | `A=17 B=14 C=15 Bits=32 Arith=OpAtomicAdd` | 12 | `amoadd.w.aqrl`, then zero-extension. |
| `OpAtomic` | `A=17 B=14 C=15 Bits=64 Arith=OpAtomicAdd` | 4 | `amoadd.d.aqrl A, C, (B)`. |
| `OpAtomic` | `A=17 B=14 C=15 Bits=8 Arith=OpAtomicAnd` | 52 | `amoand.w.aqrl` on the aligned word with `C << shift \| ^(0xff << shift)`. Writes x30, x31. |
| `OpAtomic` | `A=17 B=14 C=15 Bits=16 Arith=OpAtomicAnd` | 60 | As 8-bit. |
| `OpAtomic` | `A=17 B=14 C=15 Bits=32 Arith=OpAtomicAnd` | 12 | `amoand.w.aqrl`, then zero-extension. |
| `OpAtomic` | `A=17 B=14 C=15 Bits=64 Arith=OpAtomicAnd` | 4 | `amoand.d.aqrl`. |
| `OpAtomic` | `A=17 B=14 C=15 Bits=8 Arith=OpAtomicOr` | 36 | `amoor.w.aqrl` on the aligned word with `C << shift`. Writes x30, x31. |
| `OpAtomic` | `A=17 B=14 C=15 Bits=16 Arith=OpAtomicOr` | 40 | As 8-bit. |
| `OpAtomic` | `A=17 B=14 C=15 Bits=32 Arith=OpAtomicOr` | 12 | `amoor.w.aqrl`. |
| `OpAtomic` | `A=17 B=14 C=15 Bits=64 Arith=OpAtomicOr` | 4 | `amoor.d.aqrl`. |
| `OpAtomic` | `A=17 B=14 C=15 Bits=8 Arith=OpAtomicXor` | 36 | `amoxor.w.aqrl` on the aligned word. Writes x30, x31. |
| `OpAtomic` | `A=17 B=14 C=15 Bits=16 Arith=OpAtomicXor` | 40 | As 8-bit. |
| `OpAtomic` | `A=17 B=14 C=15 Bits=32 Arith=OpAtomicXor` | 12 | `amoxor.w.aqrl`. |
| `OpAtomic` | `A=17 B=14 C=15 Bits=64 Arith=OpAtomicXor` | 4 | `amoxor.d.aqrl`. |
| `OpAtomic` | `A=17 B=14 C=15 Bits=8 Arith=OpAtomicSwap` | 64 | Masked loop. Writes x5, x6, x7, x30, x31. |
| `OpAtomic` | `A=17 B=14 C=15 Bits=16 Arith=OpAtomicSwap` | 68 | As 8-bit. |
| `OpAtomic` | `A=17 B=14 C=15 Bits=32 Arith=OpAtomicSwap` | 12 | `amoswap.w.aqrl`. |
| `OpAtomic` | `A=17 B=14 C=15 Bits=64 Arith=OpAtomicSwap` | 4 | `amoswap.d.aqrl`. |
| `OpAtomic` | `A=17 B=14 C=15 D=16 Bits=8 Arith=OpAtomicCompareSwap` | 72 | Masked loop comparing `old & mask` with `C << shift` and storing `old ^ ((C ^ D) << shift)`; A holds the SC status inside. Writes x5, x6, x7, x28, x30, x31. |
| `OpAtomic` | `A=17 B=14 C=15 D=16 Bits=16 Arith=OpAtomicCompareSwap` | 76 | As 8-bit. |
| `OpAtomic` | `A=17 B=14 C=15 D=16 Bits=32 Arith=OpAtomicCompareSwap` | 28 | `addiw x31, C, 0; lr.w.aqrl x30; bne x30, x31; sc.w.rl A, D; bnez A`; then zero-extends x30 into A. |
| `OpAtomic` | `A=17 B=14 C=15 D=16 Bits=64 Arith=OpAtomicCompareSwap` | 20 | `lr.d.aqrl x30; bne x30, C; sc.d.rl x31, D; bnez x31; mv A, x30`. |
| `OpAtomic` | `A=17 B=14 C=15 Bits=12 Arith=OpAtomicAdd` | rejected | Bits must be 8, 16, 32 or 64. |
| `OpAtomic` | `A=17 B=14 C=15 Bits=32 Imm=1 Arith=OpAtomicAdd` | rejected | Imm must be a defined `op.MemoryOrder`. |
| `OpAtomic` | `A=17 B=14 C=15 Bits=32 Arith=OpAdd` | rejected | `Arith` must be an atomic operation. |
| `OpAtomic` | `V=B:1 A=17 C=15 Bits=32 Arith=OpAtomicAdd` | rejected | The address must be a GP register. |
| `OpAtomic` | `A=15 B=14 C=15 Bits=32 Arith=OpAtomicAdd` | rejected | A must differ from the inputs. |
| `OpAtomic` | `A=0 B=14 C=15 Bits=32 Arith=OpAtomicAdd` | rejected | A must not be x0. |
| `OpAtomic` | `A=17 B=30 C=15 Bits=32 Arith=OpAtomicAdd` | rejected | x30 and x31 are scratch. |
| `OpAtomic` | `A=17 B=5 C=15 Bits=8 Arith=OpAtomicAdd` | rejected | An input of a masked loop cannot be one of its temporaries. |
| `OpAtomic` | `A=17 B=14 C=15 D=28 Bits=16 Arith=OpAtomicCompareSwap` | rejected | x28 is a temporary of the 8/16-bit compare-and-swap. |
| `OpAtomic` | `A=17 B=14 Bits=64 Arith=OpAtomicLoad BigEndian` | rejected | Atomics use native byte order. |
