# Backend parity with PPC64

The October 2026 parity work adds native variable shifts, unsigned high products,
word carry/borrow, CPU profiles, and arithmetic flag facts to the other backends.
The Builder keeps semantic operations until selection; fixed operands are handled
by register allocation and the spill scheduler.

## CPU selection

Use `abi.CompileOptions.CPU`, described in [CPU profiles](cpu-profiles.md).
Explicit cross-target assembly uses a deterministic baseline. Native compilation
rejects extensions not verified for the executing process. `EmissionReport.CPU`
records the resolved profile. SIMD `Features` continues to control vector
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
new native tests cover both `PPC64CPU` and `CPU.Level`. CPU-profile dependency,
case-insensitive agreement and deprecated FMA-option rules are documented in
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
