# Floating-point lowering improvements

Implemented in order: exact products (A), literal pools (B), FP residency (C).
The measurement and acceptance sections record the runs made for each change;
their counts are historical baselines.
The Builder remains target-independent and floating Values retain their public
IEEE bit-pattern interface and integer-register entry/call/return ABI.

## 386: scalar SSE2, inline math, XMM residency and literal pools (P0–P4)

386 requires i686 + SSE2 for every generated function. FP32/FP64 arithmetic
keeps values in XMM3–7, while XMM0–2 remain expansion temporaries. A floating
value occupies one XMM register independently of its GP/ABI word count: FP32
uses one integer word and FP64 uses two, low first. FP variables, snapshots
and label parameters share the FP bank, with parallel XMM moves on edges.

Spills use scalar `movss` / `movsd` at native 4-byte slot offsets from ESP or
the EBP workspace base. FP64 uses two adjacent slots without requiring aligned
storage. FP32 uses exactly one slot, including home transitions and overflow
label edges. Calls spill live values and variable homes through the ordinary
scheduler: all XMM0–7 are caller-saved for C and Go ABI0.

Add/subtract/multiply/divide, sqrt and width conversions use SSE2. Sign/absolute
masks stay in the FP bank when feeding FP consumers; masks use SSE2 ANDPD/XORPD
with a 16-byte pool load. Exact-product error uses separately rounded Dekker
steps. FMA and contraction remain disabled. Constants now load directly into
XMM registers from a deduplicated, 16-byte-aligned pool after the final return;
no GPR temporary is borrowed.

386 has no RIP-relative load. Its MOVSS/MOVSD/MOVDQU absolute loads carry
`Reloc386Literal` records with a code-local pool offset. `Template.Bind` links
external symbols, maps RW, patches local references to that mapping, then seals
RX and registers any Go frame. Each Bind patches a fresh copy; neither a prior
mapping nor the template's bytes are reused as the address source. The linker
checks the load shape, complete literal footprint and 32-bit address range.
Text listings show `[literal + offset]` and retain relocation metadata without
requiring a process address.

`Unit.Template()` returns an `execution.Template`. Its `Bytes` method cannot
supply a final address for these loads and returns `ErrCodeBaseRequired`.
`Template.Bind` handles that automatically; `Template.LinkAt` and
`machine.LinkRelocationsAt` support a known final base. Low-level callers can use
`execution.MapWithRelocations` to patch their
bytes before executable sealing. External symbol resolvers receive only symbol
records; code-local records never appear as empty symbol names.
The A–C measurements below describe the 64-bit backends; the 386 measurements
are listed separately here.

| 386 fixture | P2 | P3 | P4 | GP/XMM `movd`, P2 → P3 → P4 |
| --- | ---: | ---: | ---: | ---: |
| Ten-add FP64 chain | 114 | 31 | 31 | 60 → 4 → 4 |
| FP64 recurrence with Var homes | 115 | 69 | 61 | 24 → 8 → 4 |

Counts include `ret` and each statement in grouped frame operations. The
recurrence fixture uses a 64-bit counter in both measurements. The two entry
and two return `movd` instructions are the only bank transfers in the chain;
`Test386FloatChainInstructionBudget` bounds its complete listing at 31.
`Test386FPVarLoopStaysInXMM` checks both FP widths and both storage modes,
requiring no GP bank transfers inside the arithmetic loop.

The original review's native-width generator now emits **1,556 instructions**
for `idctvar` and **1,943** for `idctargs` (64 samples, eight coefficients;
P3 counts were 1,687 and 2,070).
Literal data is excluded. These include the full inline cosine expansion,
so they are separate from the historical helper-call-era IDCT table. The Var
coefficient loop keeps arithmetic in XMM; transfers at coefficient loads remain
memory edges. Listings are in `e2e/.build/results/386-fp-p4/`.
`Test386FloatLiteralInstructionBudget` bounds a multiply/add with one shared
FP64 constant at 13 instructions and exactly one pool load.

`Test386FloatWorkspaceSweep` checks 3,000 generated FP32/FP64 programs under
both C/Go entries and StackPlatform/StackNoUse: 12,000 executions, zero limits,
and exact agreement with separately rounded references. It includes pressure,
scopes and snapshots across Set. The mixed AST sweep additionally exercises
integer bit tricks and calls, with known limits counted separately.

P2 streams paired immediate shifts/rotations, multiplication and Select through
scheduler-managed homes using only three volatile GP registers. Immediate
cross-word shifts use SHLD/SHRD. Multiplication retains its low result in a
reusable spill slot while computing the cross terms; Select computes a full-word
mask and processes each result word. These expansions work with StackNoUse.
They do not allocate a platform frame or use EBP as scratch.

Wide symbols use consecutive low/high immediate loads and a checked 64-bit
relocation. The inline 4/π table and user-supplied FP bit patterns therefore keep
both halves. A wide address is truncated to the native 32-bit pointer at a
memory operation; 64-bit loads and stores stream their words through the same
scheduler. Other integer operations, including the high-product fallback,
remain available to the inline math expansions.

All 28 math entry points execute for both widths, including full-range
trigonometric reduction, random-input accuracy checks and exact integer powers.
The existing ULP bounds are unchanged. All 56 AMD64/386 baseline output digests
match over 145 inputs each; NaN payloads are canonicalized and signed zeros are
retained. Dctlogo's direct-DCT and reconstruction tests now include 386.


## A: exact product error

Inline `twoProd` emits a rounded multiply followed by semantic
`ir.OpFProductError(A, B, C)`, where C is the rounded product. Selection uses
FMA(A, B, -C) when the CPU profile allows fusion; other targets expand the
separately rounded Dekker steps. Expansion is deferred for this operation so
one Program can compile for both amd64 CPU profiles. The operation's contract
requires well-scaled finite operands, avoiding splitting overflow. The existing
math algorithms already arrange this. Independent 256-bit product/remainder
oracles and unchanged accuracy and integer-exponent tests cover both paths.

## B: shared read-only literal pools

The physical emitter appends deduplicated, 16-byte-aligned literals after the
final return. It resolves their offsets after branch layout. Scalar FP consumers
load directly into the FP file; native SIMD constants use the same pool.
Constants and padding appear as `.byte` directives in listings and are excluded
from instruction-count budgets. All bytes belong to the executable object and
become read-only when its mapping becomes RX. The 64-bit backends need no
absolute-pointer rebinding. The 386 backend patches absolute pool loads for
each new mapping before sealing, as described above.

| Backend | Scalar load |
| --- | --- |
| amd64 | `movss` / `movsd` relative to RIP |
| 386 | absolute `movss` / `movsd`, rebased before RX |
| arm64 | literal `ldr S` / `ldr D` (`ldr Q` for SIMD) |
| riscv64 | `auipc X31` plus `flw` / `fld` |
| ppc64le | LR-preserving PC acquisition with R11/R12, then `lfs` / `lfd` |
| loong64 | `pcaddu12i` plus `fld.s` / `fld.d` |

POWER8 has no ordinary PC-relative FP load. Its six-instruction sequence saves
LR, obtains the current PC, computes the pool address, loads and restores LR.
This avoids a TOC or assumptions about R12's entry value. Managed PPC literal
leaves reserve a frame so asynchronous Go unwinding can use the saved LR while
the pool sequence temporarily holds a local PC in LR. It is longer than
materializing some small constants; the B measurements show that trade-off.
Literal bytes use the target's native byte order.

## C: FP residency

Use analysis assigns FP arithmetic results to `backend.Float`, sharing physical
ownership with the SIMD bank. Cached bit-preserving copies cross into the GPR
bank at integer operations, integer Selects, predicates, scalar label homes,
integer ABI calls and returns. Parameters and loads acquire FP copies at their
first FP use. Repeated consumers reuse those copies. Both binary32 and binary64
values stay resident across arithmetic, sqrt, conversion and FMA operations.

Sign and absolute-value bit patterns feeding FP operations use FP instructions:
ARM FNEG/FABS, RISC-V sign injection, POWER FNEG/FABS, and SSE XORPD/ANDPD masks.
General bit manipulation continues to use integer operations. CopySign remains
its bitwise expansion. No public floating-point kind or native FP call ABI was
introduced.

The allocator uses volatile registers only: XMM3–14, ARM V0–7/V19–29,
RISC-V F3–7/F10–17/F28–31, and POWER F3–13. XMM15 remains reserved for Go's zero
register. Existing scalar expansion temporaries remain reserved for bank
transfers and fixes. Callee-saved FP registers are excluded, including ARM's
partially preserved V8–15. FP and SIMD values cannot simultaneously own an
aliased physical register. FP spills use eight-byte homes; full vectors retain
16-byte homes/alignment. The spill scheduler handles pressure and conservatively
evicts FP/vector values across calls and syscalls. Constants rematerialize from
their pool. Binary32 representation rules are preserved: RISC-V NaN boxing and
POWER's single values held in double-format FPRs, with non-signaling bit transfers.

## Instruction counts

Each row lists **Pow / Sin / Exp / Log / Floor**, measured for binary64.
amd64 uses x86-64-v1; the additional row enables just the FMA extension.
PPC64LE counts use POWER8 (big-endian ppc64 has since been removed). Both data directives
and labels are excluded; instruction expansions and pressure spills are counted.

| Backend | Before | After A | After B | After C |
| --- | --- | --- | --- | --- |
| amd64 | 1214 / 444 / 172 / 205 / 45 | 1217 / 444 / 172 / 205 / 45 | 1170 / 419 / 158 / 189 / 45 | 683 / 328 / 102 / 120 / 44 |
| amd64 + FMA | 1214 / 444 / 172 / 205 / 45 | 904 / 444 / 172 / 205 / 45 | 866 / 419 / 158 / 189 / 45 | 578 / 328 / 102 / 120 / 44 |
| arm64 | 1193 / 412 / 183 / 211 / 31 | 882 / 412 / 183 / 211 / 31 | 774 / 343 / 148 / 171 / 31 | 429 / 249 / 87 / 100 / 31 |
| riscv64 | 1549 / 585 / 262 / 309 / 64 | 1220 / 585 / 262 / 309 / 64 | 1019 / 458 / 195 / 230 / 64 | 650 / 357 / 134 / 159 / 64 |
| PPC64LE | 1518 / 540 / 238 / 291 / 65 | 1192 / 540 / 238 / 291 / 65 | 1192 / 550 / 236 / 289 / 65 | 821 / 445 / 171 / 214 / 65 |

Budgets in `compile/math_budget_test.go` were lowered after each item, keeping
approximately 10% headroom and never raising an existing budget. A ten-operation
FP chain now needs exactly two bank transfers: entry and return.

## Validation

Tests cover unchanged math accuracy, integer Pow exponents, exact product errors,
FP chains, 40 live FP values under pressure, integer/Select boundaries, both
managed entry ABIs with stack growth and GC, labels/scopes, literal alignment and
deduplication, SIMD constants and FP/SIMD coexistence. Byte/text plans share the
same expansion. QEMU execution covers amd64 with and without FMA, riscv64 and
both POWER byte orders; ARM executes natively.

The final Linux matrix passed on **Go 1.26.9 and 1.27.2**: 1,465 harness checks,
zero failures and 16 expected unsupported-port skips. This includes 386,
amd64, arm64, riscv64 and both POWER byte orders at POWER8/9/10. The separate
amd64 qemu64/max profile suite passed all 120 checks, exercising both Dekker and
FMA paths. These are harness checks (package build/run and smoke gates), rather
than counts of individual Go test cases. The remaining Linux ports run their
stub-package checks and skip native emission as expected.

Acceptance also uncovered a CPU-profile ordering regression: POWER9 and POWER10
have the same currently selected optional extensions, but a POWER10 minimum
must still be rejected on POWER9. Profile comparison now enforces that ordering,
with every host/request POWER level pair covered for both byte orders.

Reports are saved in the ignored `e2e/.build/results/` directory:
`fp-perf-linux.xml` and `fp-perf-amd64-profiles.xml`. Final `go test ./...`,
`go vet ./...`, E2E harness unit tests and `git diff --check` pass. Additional
POWER9 runs on both Go versions cover the final scope-remapping and large FP
spill-offset regressions. All emulation used the container's private binfmt
namespace; no host system state was changed. Physical POWER instruction-cache
behavior remains unverified because no POWER hardware is available.

## FP review corrections

1. **Register hints:** zero-branch and ABI hints for a resident FP value now
   follow its GPR copy. The allocator checks the physical register bank even
   when honoring an otherwise free hinted register. Other hint producers were
   audited: result/call/export preferences are integer homes, and shared FP/SIMD
   ownership remains subject to the same bank filter. Regression tests cover
   the amd64 crash, both widths, zero/equality predicates, and wrong-bank hints
   on every FP backend.
2. **Literal reach:** ARM keeps the single literal LDR within its signed 19-bit
   word offset. Far loads use ADR of the current PC, MOVZ/MOVK of a signed
   32-bit offset, ADD with SXTW, and an ordinary LDR. This five-instruction form
   works at any instruction-aligned mapping base, without page-alignment or
   absolute-address assumptions. A layout pass grows loads and rebases labels
   before branch relaxation; byte/text visitors share the same plan. Beyond
   signed 32-bit reach, the error is `ErrBackendLimit`. RISC-V AUIPC/FLD and
   POWER ADDIS/load offsets are checked before truncating their immediate
   fields. The 200,000-leaf review program executes correctly in two separate
   ARM64 mappings, and far-form words were checked against GNU as.
3. **POWER8 binary32 extraction:** XSCVDPSPN is followed by
   `xxsldwi vs0,vs0,vs0,3` before MFVSRWZ. ISA 2.07B, printed page 412,
   defines the result in word 0 and leaves words 1–3 undefined; ISA 3.0C,
   printed page 541, also defines word 1. Consequently POWER9/10 retain the
   short form. This was verified in the official
   [ISA 2.07B manual](https://ibm.ent.box.com/shared/static/jd5w15gz301s5b5dt375mshpq9c3lh4u.pdf)
   and [ISA 3.0C manual](https://files.openpowerfoundation.org/s/eRGYZi56BXmCSY7/download).
   Encoding/listing tests cover both endian variants and all three profiles;
   GNU as independently confirms the transfer words. This adds one instruction
   per affected transfer. Binary64 counts are unchanged; POWER8 binary32
   Pow/Sin/Exp/Log/Floor move from **833/452/179/221/72** to
   **834/453/180/222/73**. Existing budgets pass without being raised.
4. **Exact-product domain:** `OpFProductError` remains public, matching the
   library's public, mutable IR. Its opcode documentation now explicitly
   requires the correctly rounded product C and finite, well-scaled A/B, with
   no split/intermediate overflow or underflow. Validation checks structure,
   rather than runtime numeric preconditions. Results outside that domain are
   unspecified and may differ by CPU profile; large split-overflow inputs are
   examples. This uses the review's documented-contract option instead of
   introducing hidden Builder provenance into public Programs.
5. **Bank-copy widths:** GPR-to-FP copies use the consuming operation's width;
   FConvert uses its source width. FP producer metadata is inferred from the
   operation, and copies are cached by both value ID and width. Tests inject
   zero/64-bit metadata into binary32 consumers and verify NaN-boxing/format
   conversion widths are retained.
6. **RISC-V listings:** sign injection now decodes as `fmv`, `fneg`, or `fabs`
   when both sources agree, otherwise `fsgnj`, `fsgnjn`, or `fsgnjx`, with the
   S/D suffix. Golden instruction words and shared-plan tests cover these
   spellings without changing code bytes.

The review acceptance passed on Go **1.26.9 and 1.27.2**: the full Linux matrix
has 1,465 harness checks, zero failures and 16 expected unsupported-port skips;
the separate amd64 qemu64/max suite passes all 120 checks. Math accuracy,
instruction budgets, `go test ./...`, `go vet ./...`, harness unit tests and
`git diff --check` pass. Reports are
`e2e/.build/results/fp-review-{linux,amd64}.xml`.

### Follow-up: POWER literal-base reuse

Computing a pool base once for functions/segments with multiple literals could
replace repeated six-instruction PC-acquisition sequences and reduce loop
reloads. This is deferred: the base must participate in allocation/spilling and
survive call boundaries, while preserving the managed frame's asynchronous
unwind and LR guarantees. Reserving another GPR or hoisting the current LR
sequence without that scheduling would add a new correctness risk.

## ARM64 joint layout follow-up

Control-flow layout now reaches a joint fixed point for literal loads and
branches. The existing `machine.RelaxBranches` shrinks long branches; it does
not grow them as the follow-up review initially assumed. Layout first performs
that compaction once, then alternates literal-load promotion and promotion of
out-of-range short branches. Labels follow instruction indices through each
pass. Both forms only grow during this loop, so it terminates without
oscillating between short and long forms. Final branch resolution and pool
placement run after convergence. The five-instruction far literal form is
unchanged.

The regression starts a pool 16 bytes inside literal LDR reach: growing a TBZ
between the load and pool moves the aligned pool over the limit, requiring a
second layout round. It failed with the previous two-pass ordering. Tests also
cover backward edges, growth before the load, literal growth that invalidates
a previously compacted branch, stable label positions on a repeated layout,
and matching byte/text assembly sizes.

The straight-line path already finalizes frame, return and instruction sizes
before literal relaxation. `finishLiterals` only assigns load offsets and
appends pool/padding bytes; it does not resize existing instructions. A
borderline-pool regression checks every existing PC and size across that step
and assembles both byte and text outputs.

Acceptance was rerun after the native-word runtime call conversion: native
`go test ./...` and `go vet ./...` pass against the current workspace without
overlays. The ARM64 matrix passes on Go 1.26.9 (45 checks) and Go 1.27.2
(44 checks), with no failures or skips. Both fixed-point regression tests pass.
