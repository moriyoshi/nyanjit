# Backend operations and target contracts

This reference describes native operations, portable fallbacks and ABI restrictions across supported targets. CPU profiles and vector policy determine the selected forms. The Builder retains semantic operations until selection; allocation and spill scheduling handle fixed operands.

## CPU selection

Use `compile.TargetSpec.CPU`, described in [CPU profiles](cpu-profiles.md).
Explicit cross-target assembly uses a deterministic baseline. Native compilation
rejects extensions not verified for the executing process. `EmissionReport.CPU`
records the resolved profile. `TargetSpec.Vectors` controls vector
legalization independently.

## Native operations

| Operation | amd64 | arm64 | riscv64 | 386 |
| --- | --- | --- | --- | --- |
| Variable shifts | CL; BMI2 SHLX/SHRX/SARX | LSLV/LSRV/ASRV | SLL/SRL/SRA; word forms | CL; SHLD/SHRD for pairs |
| Variable rotates | ROL/ROR CL | RORV; negate for left | shift/or; Zbb ROL/ROR | ROL/ROR; paired SHLD/SHRD |
| Unsigned high multiply | MUL; BMI2 MULX | UMULH | MULHU | MUL for 32-bit words |
| Carry/borrow | ADC/SBB | ADCS/SBCS | add, SLTU, add | ADC/SBB |
| Leading/trailing zeros | BSR/BSF; LZCNT/TZCNT | CLZ/RBIT | portable fallback; Zbb CLZ/CTZ | portable fallback |
| Address folding | indexed memory / LEA | existing indexed loads/stores | Zba SH1ADD/SH2ADD/SH3ADD | existing LEA |
| Vector shuffle (128-bit chunk) | PSHUFD/SHUFPS/SHUFPD; PSHUFLW/PSHUFHW and PUNPCK* for 8/16-bit lanes, else lanes | DUP, ZIP/UZP/TRN, EXT, TBL/TBX | scalar lanes | as amd64 |
| Vector widening extend | PUNPCKL/H with zero or a PCMPGT sign mask | UXTL/SXTL (USHLL/SSHLL), the `2` forms | scalar lanes | as amd64 |
| Compare as a value (`OpCompareSet`) | CMP, SETcc, MOVZX | CMP, CSET; SXTB/SXTH for signed narrow | SLT/SLTU, XOR+SEQZ/SNEZ, XORI | CMP, SETcc or MOV+Jcc+INC; 64-bit as word compares |
| Select on a compare (`OpSelectFlags`) | CMOVcc | CSEL | compare value, then branch | CMOVcc |
| Overflow branch (`Builder.Overflow`) | JO/JNO after 32/64-bit ADD, SUB and 64-bit IMUL | B.VS/B.VC after ADDS/SUBS; SMULH compare for products | computed (XOR/AND/sign) | JO/JNO after 32-bit ADD, SUB and IMUL, and a pair's ADC/SBB |
| Byte reverse (`OpReverseBytes`) | BSWAP; ROL r16, 8 | REV, REV16 | Zbb REV8+SRLI; shifts and masks | BSWAP per word, words swapped |
| Addition whose flags no path reads (`FlagElider`) | LEA for ADD, SUB of an immediate, INC and DEC into another register | ADD, SUB, AND without the S bit | no flags | as amd64, at 32 bits; the AND that zero-extends an 8/16-bit result becomes MOVZX where the register has the width (every register for 16 bits, EAX..EBX for 8), after LEA for a masked addition into another register. A mask whose narrow zero a branch reads stays AND (5 bytes on EAX). The baseline reads no mask's flags and always takes MOVZX where the register allows |

Shifts saturate at the logical width using the **whole unsigned count**, including
bits above the machine's masked count field. Arithmetic right shifts saturate to
sign fill. Rotates reduce the count modulo the logical width. Narrow rotations
repeat the source pattern when the ISA has a wider native rotation.

amd64 instruction-local CL/RAX/RDX/R11 constraints relocate live values or spill
through the allocator. R10's workspace role is preserved. On 386, paired shifts
load their fixed operands from scheduler-managed homes or incoming argument
slots. The old paired carry/multiply visitors canonicalize to the shared
`OpWordCarry` / `OpMulHigh` path.

Only EAX..EBX have byte registers on 386. The optimized tier's allocators
prefer them, when one is free, for 8-bit results that are masked, values
stored with an 8-bit store or narrowed to 8 bits, and SETcc destinations; an
8-bit store of a value in ESI, EDI or EBP copies it into a free byte register
before borrowing one through a spill. It is a preference only: it never
evicts. The baseline allocates only EBX of them, so it computes an 8-bit
result bound for ESI, EDI or EBP in EAX and moves it there with MOVZX.

`Builder.LeadingZeros` and `Builder.TrailingZeros` accept scalar widths 8, 16,
32 and 64. They retain the input width, and return that width for a zero input.
`x & (y ^ all_ones)` contracts to RISC-V ANDN when Zbb is enabled and the
complement has one use. Immediate rotations can use amd64 RORX.

## Flags and loops

amd64, arm64 and 386 report S/Z/C facts independently. INC/DEC preserve x86 CF.
A 386 pair's high-word ADC/SBB gives the 64-bit sign and carry but no zero.
MULX/RORX preserve flags. ARM subtraction records that C means **no borrow**.
ARM uses S forms for inexpensive arithmetic so subsequent carry and result
predicates can use NZCV. Width-preserving truncation and proven signed widening
retain the applicable facts; unsigned widening cannot inherit a narrow sign
predicate.

A `Switch` with enough dense runs becomes `machine.OpJumpTable`: a branchless
index clamp (CMOV, CSEL, ISEL, or an SLTU-derived mask on RISC-V and
LoongArch) and an indirect jump through 32-bit table-relative entries that
follow the jump. Each target's `FORMS.md` lists its sequence; PPC64 writes
CTR, so a CTR loop never contains one.

With `PreferCompactLoops`, ARM countdown loops can use SUB/CBNZ after outgoing
flag liveness proves the decrement's flags dead. RISC countdown loops use direct
register branches without a zero-compare pseudo-op. Existing AMD64 LOOP and
PPC64 CTR selection remain available.

## LoongArch integer and runtime port

Linux/loong64 implements the L0/L1 integer portion: full-count variable shifts,
ROTR/ROTRI, unsigned MULH.DU, SLTU carry/borrow, CLZ/CTZ, ALSL.D, BSTRPICK.D,
MASKEQZ/MASKNEZ selection, and indexed loads/stores. Countdown compaction uses
BNEZ. Sixteen-bit conditional, 21-bit zero-test and 26-bit jump displacements
are scaled by four; long/far forms participate in the shared layout pass.
Byte and text assemblers use the same concrete expansion plans and sizing.

The default CPU is `la64v1.0`, whose baseline uses scalar vector lanes.
With `VectorsAuto()`, a profile containing LSX enables 128-bit integer SIMD;
LASX implies LSX, but no LASX instructions are emitted. Leaf entries use
a0–a7 (R4–R11), results use a0/a1, R23 carries
the workspace, and R12/R19/R20 are expansion temporaries. R22/R29/R30 remain
reserved for Go. Preserved allocation candidates are R24–R28/R31; the spill
scheduler preserves any written C callee-saved homes.

The Go register assignments were checked against both Go 1.26.9 and 1.27.2
(`internal/abi/abi_loong64.go`, `cmd/internal/obj/loong64`, and the runtime).
They agree with the handoff. C register tables follow the
[LoongArch procedure call standard](https://github.com/loongson/la-abi-specs/blob/release/lapcs.adoc).
LP64 unsigned 32-bit C values are sign-extended in their 64-bit ABI registers.
Typed 32-bit outbound C arguments are now normalized before either a direct
call or the managed uintptr gateway. Generated entry returns retain the
library's uint64 bit-pattern contract. Go ABIInternal has sixteen
integer and sixteen FP argument registers; the current leaf invocation API
still accepts at most eight native argument words.

L2 supports C calls/callbacks and registered pointer-free Go calls, with
sixteen Go argument registers, their caller-owned homes, and a 640-byte managed
frame limit, including outgoing call space and register preservation. C entries
restore R23–R31 and F24–F31 across Go calls; Go's R22
remains reserved. Tests cover GC, stack growth, panic unwinding, reentry, and a
real pthread entering a Go callback before reentering registered generated code.
The existing call policy requires `StackPlatform` for outbound calls;
`StackNoUse` remains available for leaf code and spills to caller workspace.
Scalar FP32/FP64 and FMA use baseline LA64 instructions. Allocated FP values,
variables and label parameters use f8–f23; f0–f2 are expansion temporaries,
and f24–f31 retain their C callee-save role. Bank transfers normalize FP32
results to zero-extended bit patterns. Shared literal pools use PCADDU12I +
FLD with a fixed two-instruction load, keeping pool and branch layout stable.
All inline math functions are covered at both widths with measured instruction
budgets. Reports distinguish FCSR0 modification from integer flags.
Native LSX handles supported integer vectors in straight-line programs without
Vars; wider vectors split into 128-bit pieces. Shuffles use vreplvei, vilv/vpick, vshuf4i or
vshuf.b, and widening extends vsllwil/vexth. Control-flow programs and
unsupported operations use scalar lanes. See [SIMD design](simd-design.md#loongarch-lsx-lowering).
Executable mappings use RW then RX,
apply relocations before protection, and execute IBAR 0 before publishing.
QEMU validates instruction semantics; real LoongArch hardware is still needed
to validate instruction-cache publication across cores.

## 16/32-bit scalar memory

`Load16` and `Load32` return `Scalar(16)` and `Scalar(32)`, respectively;
loads zero-extend in their physical register. Use `CastUnsigned(value, 64)`
before combining a load with a 64-bit scalar. `Store16`/`Store32` truncate a
matching-width or 64-bit scalar and leave surrounding bytes untouched.
`LoadBits`/`StoreBits` use the same operations. Addresses may have native or
64-bit width (386 uses the low address word). These APIs allow any byte
alignment in ordinary user memory; they promise neither atomic accesses nor
support for device memory.

| Target | Native 16/32-bit instructions | General addresses |
| --- | --- | --- |
| amd64 / 386 | MOVZX word / MOV dword; MOV word/dword stores | native, including unaligned |
| arm64 | LDRH / LDR W; STRH / STR W | native, including unaligned Normal memory with alignment checks disabled by the user environment |
| riscv64 | LHU / LWU; SH / SW | byte fallback unless known aligned |
| ppc64le | LHZ / LWZ; STH / STW | conservative byte fallback unless known aligned, on POWER8/9/10 |
| loong64 | LD.HU / LD.WU; ST.H / ST.W | byte fallback unless known aligned |

Go-layout `Data` fields, array elements and slice header words are naturally
aligned and use one native access on every target. `Data.Rebase` preserves this
contract: its base must point to storage with the original Go type's alignment.
The read-only/write rules of `Data` still apply.

The IR uses explicit `OpLoad16`, `OpLoad32`, `OpStore16`, `OpStore32` opcodes,
with `Bits` matching the access width and an `Aligned` promise (2 or 4 bytes).
Separate opcodes follow the existing 8/64-bit representation and prevent the
memory width from being confused with a value's physical register width.
Lowering uses byte operations when a target cannot directly handle an access,
and can retry with a byte expansion when the native plan exceeds its register
or frame budget. The optimized tier's graph remapper preserves block edges,
scope metadata and memory ordering. All supported targets are little-endian;
invalid byte-order enums remain errors. Selected/physical narrow operations use
a materialized address register; 64-bit folded-address visitors retain their
64-bit meaning. Target
assemblers expose `NarrowMemory(machine.Instruction) error` (POWER uses its
concrete emitter), and loads/stores participate in memory-effect reports.

No CPU extension is required or enabled by this change. In particular, the
baseline does not assume RISC-V Zicclsm or LoongArch UAL. RISC-V permits an
execution environment to fault or emulate misaligned loads/stores; LoongArch
requires both hardware and environment support. POWER also documents alignment
interrupt handling, so the general-address path conservatively avoids that
dependency. See the [RISC-V ISA](https://riscv.github.io/riscv-isa-manual/snapshot/spec/),
[LoongArch manual, §2.1.8](https://loongson.github.io/LoongArch-Documentation/LoongArch-Vol1-EN.html#_unaligned_memory_access),
[IBM load/store rules](https://www.ibm.com/docs/en/aix/7.3.0?topic=processor-fixed-point-load-store-update-instructions)
and [Arm architecture reference manual](https://developer.arm.com/documentation/ddi0487/latest/).

## Stack areas

`OpStackAddress` lowers to `machine.OpSlotAddress` at both tiers: the address
of the area's first reserved slot, formed as `OpSpill` addresses slots. The
entry zeroes every area word; a 16-byte area adds its address's bit 3 at run
time, because Go-ABI stacks are only 8-byte aligned on most targets.

| Target | Platform stack | Workspace (`StackNoUse`) | Far offsets |
| --- | --- | --- | --- |
| amd64 | `LEA r, [RSP+disp32]` | `LEA r, [R10+disp32]` | none needed |
| 386 | `LEA r, [ESP+disp32]` (4-byte alignment at most) | `LEA r, [EBP+disp32]` | none needed |
| arm64 | `ADD Xd, SP, #imm` | `ADD Xd, X8, #imm` | `ADD …, LSL #12` first |
| riscv64 | `addi rd, sp, imm` | `addi rd, x9, imm` | offset built in rd, then `add` |
| ppc64le | `addi rd, r1, imm` | `addi rd, r29, imm` | `addis` first |
| loong64 | `addi.d rd, r3, imm` | `addi.d rd, r23, imm` | offset built in rd, then `add.d` |

## Float passing at call boundaries

`abi.FloatPassing` (a call's `Imm`, `compile.Func.Floats`) moves marked
binary32/binary64 values into each convention's FP registers. Integer and
FP arguments take independent sequences except as noted.

| Target | C arguments / result | Go ABIInternal arguments / result | Notes |
| --- | --- | --- | --- |
| amd64 | XMM0–7 / XMM0 | X0–X14 / X0 | C calls set AL to the XMM count (`OpCallC` Imm); X15 stays zero |
| arm64 | V0–V7 / V0 | F0–F15 / F0 | d8–d15 preservation unchanged |
| riscv64 | fa0–fa7 / fa0 | F10–F17, F8, F9, F18–F23 / F10 | binary32 NaN-boxed |
| ppc64le | f1–f13 / f1, skipping the positional GPR | F1–F12 / F1 | binary32 in double format |
| loong64 | fa0–fa7 / fa0 | F0–F15 / F0 | |
| 386 | cdecl stack words / x87 ST(0) | ABI0 stack words / result slot | `OpX87Result`; FLD quiets a signaling NaN |

v1 marks at most 8 arguments and passes none on the stack except on 386.
The optimized tier lowers an entry's body with the integer layout and moves
arguments and the result at the boundary; calls move marked arguments from
memory or literals straight into their FP registers. A Go-managed C call
with float passing calls a generated ContextForeign adapter through the
integer runtime gateway.

## Prefetch

`Builder.Prefetch(address, kind)` and `OpPrefetch` hint that an address will
soon be read (`PrefetchRead`) or written (`PrefetchWrite`). A prefetch never
faults, even for nil or an unmapped address, and has no architectural effect.
It is ordered like a memory operation, so lowering never removes or moves it,
but memory-effect reports do not count it as a read or a write. There are no
locality hints: every target prefetches into its first-level cache.

| Target | Read | Write |
| --- | --- | --- |
| amd64 / 386 | PREFETCHT0 | PREFETCHT0 (PREFETCHW needs PRFCHW, which the baselines do not promise) |
| arm64 | PRFM PLDL1KEEP | PRFM PSTL1KEEP |
| riscv64 | Zicbop `prefetch.r` | Zicbop `prefetch.w` |
| ppc64le | `dcbt 0, rA` | `dcbtst 0, rA` |
| loong64 | `preld 0` | `preld 8` |

The Zicbop hints are encoded as `ori x0, rs1, 1` and `ori x0, rs1, 3`, so a
core without Zicbop runs them as no-ops; no CPU profile enables them. A 64-bit
address on 386 uses its low word, as the other memory operations do.

## Tail calls

`Builder.TailCallC(target, args...)` and `ir.TermTailCall` end a block with a
C call whose result is the function's own. The callee reuses the caller's
frame: a chain of tail calls runs in one frame, at any depth. Both tiers place
the target and arguments as for `CallC`, restore the callee-saved registers
the entry saved, then emit `machine.OpTailCall`, which leaves the frame and
jumps:

| Target | Jump |
| --- | --- |
| amd64 | `add RSP, frame`; `xor EAX, EAX` (as `CallC` sets AL for a variadic callee); `jmp R11` |
| arm64 | the frame restores FP and LR; `BR X16` |
| riscv64 | `ld ra`; `add sp`; `jalr x0, 0(t6)` |
| ppc64le | the frame restores LR and r2; `mtctr r12; bctr` with r12 the callee's global entry. The original caller restores its TOC after its own indirect call. |
| loong64 | `ld.d ra`; `addi.d sp`; `jirl r0, r20, 0` |
| 386 | the arguments, stored to the outgoing area as for a call, are copied to the incoming argument words through ECX; the frame pops the callee-saved registers; `jmp EAX` |

Tail calls are accepted only for a C ABI `Func` in `ContextForeign` or
`ContextExternal`: Go-managed frames are registered with the Go runtime, and a
Go-ABI tail call needs its own design. Arguments must fit in the C argument
registers; on 386 the callee may take at most as many stack words as the
entry's own parameters. Floating-point arguments are a later extension.

## Atomics and fences

`AtomicLoad`, `AtomicStore`, `AtomicAdd`/`And`/`Or`/`Xor`/`Swap`,
`AtomicCompareSwap` and `Fence` are sequentially consistent at 8, 16, 32 and
64 bits on every target and at both tiers. Each atomic is one
`machine.OpAtomic` expansion, so no spill can enter a reservation loop; the
registers beyond its operands are the target's expansion temporaries and the
fixed registers below, which the allocators keep free (`FixedClobbers` and
`ImplicitWrites`). `Fence` is `OpBarrier` with `BarrierMemory`.

| Target | Load / store | Read-modify-write | Compare-and-swap | Fixed allocatable registers |
| --- | --- | --- | --- | --- |
| amd64 | MOV/MOVZX; XCHG | LOCK XADD, XCHG; LOCK CMPXCHG loop for and/or/xor | LOCK CMPXCHG | R11 (store, add, swap); RAX, R11 (and/or/xor); RAX (CAS) |
| 386, 8–32 bits | as amd64 | as amd64, through DL/DX/EDX | LOCK CMPXCHG | EDX; EAX and EDX for and/or/xor and CAS |
| 386, 64 bits | SSE2 MOVQ; MOVQ + MFENCE | LOCK CMPXCHG8B loop | LOCK CMPXCHG8B | operands in scratch slots; EAX, ECX, EDX, EBX, ESI |
| arm64 | LDAR; STLR | LDAXR/op/STLXR loop (v8.0) | LDAXR/CMP/STLXR loop | none (X16, X17) |
| riscv64 | fence; lbu/lhu/lwu/ld; fence; fence; sb–sd | amo*.{w,d}.aqrl; aligned-word AMO (8/16 and/or/xor); masked lr.w/sc.w (8/16 add, swap) | lr/sc loop; masked for 8/16 | x5–x7 (8/16 add, swap); x5–x7, x28 (8/16 CAS) |
| ppc64le | sync; l*z; cmp; bne; isync; sync; st* | l*arx/op/st*cx. loop (lbarx/lharx, ISA 2.06) | l*arx/cmp/st*cx. loop | none (r11, r12) |
| loong64 | dbar; ld.*u/ld.d; dbar; dbar; st.*; dbar | am*_db.{w,d}; aligned-word AM (8/16 and/or/xor); masked ll.w/sc.w (8/16 add, swap) | ll/sc loop; masked for 8/16 | r13 (8/16 add, swap); r13, r14 (8/16 CAS) |

Results are zero-extended: the 32-bit forms of riscv64 and loong64, which
sign-extend, zero-extend afterwards, and their 32-bit compare-and-swap compares
with a sign-extended copy of the expected value. The RISC-V reservation loops
are constrained LR/SC loops. arm64 LSE (`LDADDAL`, `CASAL`, …) and RISC-V
Zabha/Zacas are not selected: the arm64 encoders hold the LSE forms, but the
assemblers take no CPU profile yet.

## System calls

`Builder.Syscall` and `OpSyscall` make a raw Linux system call at both tiers
on every target, in `ContextExternal` with a platform stack, and return the
raw result with errors as `-errno`. On 386 the call is `INT 0x80`: the number
goes to EAX and arguments 1 to 6 to EBX, ECX, EDX, ESI, EDI and EBP, low words
only; the kernel preserves every register but EAX. Both tiers save EBP to a
frame slot before a sixth argument overwrites it and restore it right after
`INT 0x80`; fewer arguments leave EBP alone. The 64-bit result is EAX
sign-extended (EDX = EAX >> 31, arithmetic), so the unsigned test
`r >= -4095` detects an error on every target. `abi.PreflightCall` reports
EAX as the only clobber, with no call stack.
