# Backend parity with PPC64

This document records the October 2026 parity work and the current target contracts.
Dated acceptance counts and reports below describe those runs; they are not a
claim that the current tree has just been retested.

The parity work adds native variable shifts, unsigned high products,
word carry/borrow, CPU profiles, and arithmetic flag facts to the other backends.
The Builder keeps semantic operations until selection; fixed operands are handled
by register allocation and the spill scheduler.

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

Shifts saturate at the logical width using the **whole unsigned count**, including
bits above the machine's masked count field. Arithmetic right shifts saturate to
sign fill. Rotates reduce the count modulo the logical width. Narrow rotations
repeat the source pattern when the ISA has a wider native rotation.

amd64 instruction-local CL/RAX/RDX/R11 constraints relocate live values or spill
through the allocator. R10's workspace role is preserved. On 386, paired shifts
load their fixed operands from scheduler-managed homes or incoming argument
slots. The old paired carry/multiply visitors canonicalize to the shared
`OpWordCarry` / `OpMulHigh` path.

`Builder.LeadingZeros` and `Builder.TrailingZeros` accept scalar widths 8, 16,
32 and 64. They retain the input width, and return that width for a zero input.
`x & (y ^ all_ones)` contracts to RISC-V ANDN when Zbb is enabled and the
complement has one use. Immediate rotations can use amd64 RORX.

## Flags and loops

amd64 and arm64 report S/Z/C facts independently. INC/DEC preserve amd64 CF.
MULX/RORX preserve flags. ARM subtraction records that C means **no borrow**.
ARM uses S forms for inexpensive arithmetic so subsequent carry and result
predicates can use NZCV. Width-preserving truncation and proven signed widening
retain the applicable facts; unsigned widening cannot inherit a narrow sign
predicate.

With `PreferCompactLoops`, ARM countdown loops can use SUB/CBNZ after outgoing
flag liveness proves the decrement's flags dead. RISC countdown loops use direct
register branches without a zero-compare pseudo-op. Existing AMD64 LOOP and
PPC64 CTR selection remain available.

## Instruction evidence

Counts below use binary64 inputs and explicit baseline CPU profiles. Listings
include normalization, moves, spills, and the return.

| Function | amd64 before → after | arm64 before → after | riscv64 before → after |
| --- | --- | --- | --- |
| FSin | 826 → 444 | 704 → 412 | 987 → 585 |
| FFloor | 85 → 45 | 63 → 31 | 108 → 64 |
| FPow | 1295 → 1214 | 1255 → 1193 | 1637 → 1549 |

`compile/math_budget_test.go` limits every math expansion at both 32 and 64 bits,
with about 10% instruction headroom and a 256-byte workspace limit.
`TestBackendCapabilityMatrix` prevents native selection from disappearing;
`TestBackendInstructionBudgets` bounds small arithmetic probes. Semantic tests
cover widths, full counts, live inputs, carry inputs, paired arithmetic, signed
aliases, byte/text sizes, and control-flow flag liveness.

## Acceptance

Run `e2e/{amd64,arm64,riscv64,386}.star` using the container harness. Each suite
builds once per target/version and runs baseline and enabled profiles. Both Go
1.26.9 and 1.27.2 are covered. QEMU profiles are process-local; the private
container binfmt namespace leaves host registrations unchanged.

The acceptance run has 462 successful package/build checks and zero failures:
120 amd64, 120 arm64, 116 riscv64, and 106 386. ARM64 was native on the ARM64
host; its two runner profiles use the same hardware, with explicit compiler
profile tests. AMD64/386/RISC-V were executed under QEMU. Full PPC64 and PPC64LE
POWER8 regression runs also passed on Go 1.27.2.

JUnit reports are retained under `e2e/.build/results/backend-parity-*.xml`.
Per-architecture completion records are in `docs/{amd64,arm64,riscv64,386}-completion.json`.
These records cover this parity handoff; the broader PPC roadmap and real POWER
cache-publication validation remain separate.

## Review corrections

The review was rechecked after [FP residency](fp-performance.md) landed. Its
ARM64 narrow subtract-immediate borrow bug still reproduced: a sign-extended
16-bit constant could flip SUB to a 32-bit ADDS whose carry did not represent
the narrow subtraction. Flag reporting now discards that invalid carry fact;
instruction selection uses a register SUB for these negative narrow literals
so carry branches also retain the fast, frameless path. Runtime tests scan all
65,790 nonzero 8-/16-bit immediates and check the affected unsigned comparisons.

IR validation now requires matching widths for ordinary scalar integer
arithmetic, immediate shifts/rotations and both Select arms. Explicit casts,
sign extensions, memory addresses, Select conditions and variable shift counts
retain their independent contracts. The malformed 8-bit XOR/16-bit constant
review example is rejected; explicitly narrowing its operand produces 0xe5.

The POWER minimum-level check had already been repaired during FP acceptance;
new native tests cover `CPU.Level`. CPU-profile dependency and
case-insensitive level rules are documented in
[CPU profiles](cpu-profiles.md). AMD64 flag queries use the cached profile and
perform no allocations, including RORX feature checks.

Binary32 math budgets now use their independently measured instruction counts
plus rounded-up 10% headroom, replacing the flat 20-instruction allowance.
For example, ARM64 Trunc32 has a budget of 28 for 25 instructions, and Floor32
has a budget of 40 for 36 instructions. Selected-op assertions cover shifts,
rotations, high multiply and carry extraction directly and reject software
Select fallbacks; they do not depend on assembly containing the word "select".

Review acceptance passed on Go **1.26.9 and 1.27.2**: the full Linux matrix
has 1,465 harness checks, zero failures and 16 expected unsupported-port skips.
Separate baseline/enabled CPU suites passed 120 amd64, 106 386 and 116 RISC-V
checks. POWER8/9/10 were exercised in both byte orders; ARM64 ran natively,
including the exhaustive borrow scan. Math accuracy and FP residency tests
remain unchanged, and the measured FP instruction counts match the FP work's
final counts. Native `go test ./...`, `go vet ./...`, harness unit tests and
`git diff --check` pass. Reports are retained as
`e2e/.build/results/parity-review-{linux,amd64,386,riscv64}.xml`.

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
Vars; wider vectors split into 128-bit pieces. Control-flow programs and
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
Arbitrary-alignment accesses on strict baselines and explicit big-endian narrow
accesses are expanded into byte operations by legalization, before selection
and allocation. The graph remapper preserves block edges, scope metadata and
memory ordering. All supported targets are little-endian; invalid byte-order
enums remain errors. Selection in `compile` rejects accesses still requiring
legalization. Selected/physical narrow operations use a materialized address
register; 64-bit folded-address visitors retain their 64-bit meaning. Target
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
