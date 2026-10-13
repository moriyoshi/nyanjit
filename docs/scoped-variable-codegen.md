# Scoped variable code generation

This records scoped-variable code generation and the correctness fixes from the 2026-10-14 review. The API and lifetime rules remain those in [virtual-register-design.md](virtual-register-design.md).

Instruction counts, commands and acceptance totals below are historical review
records. Later sections supersede earlier implementation stages, including the
386 FP residency and literal-pool work. Scratch paths and cached reports may
be absent from a clean checkout; use [the E2E guide](../e2e/README.md) for current
runner commands.

## Changes

- Get lends a register home to a snapshot when it is available. Ordinary arithmetic cannot overwrite that home. An adjacent Set can coalesce its producer into the home when the old snapshot is dead; a live snapshot is relocated into a spare register or spilled before the write.
- Jumps without arguments discard their bookkeeping result, eliminating zero materialization at loop entry and back edges.
- Scope lookahead accounts for ordinary temporary demand in each register bank. Outer homes remain fixed when capacity is sufficient; under pressure, inner variables take priority. A clean outer home is not written back at Leave.
- Variable slot offsets are reserved without touching memory. Frames and workspace are sized from actual accesses, including label edges and ABI saves. Direct register exports do not create phantom spill slots.
- FP variables, captures, and FP label parameters use FP registers. Integer bit patterns and native FP spill representations are distinguished on edges, preserving POWER single-precision conversion and RISC-V NaN boxing.
- Preserved GPRs become available under pressure for variable homes and ordinary selected values in loops. The allocator schedules entry saves and restores, including workspace-only returns. Runtime registers, expansion registers, and managed frame pointers remain reserved.
- Straight-line functions offer the result register to variable homes after every other candidate, so the final result can still be computed in place. Where the result register is also the first parameter register, a mutable parameter keeps it, and a variable whose value the function returns is homed there when the register is free. Control-flow functions keep the result register out of home planning.
- Addition and subtraction by zero become identity copies. POWER uses plain arithmetic when control-flow flag liveness proves CR0 and carry dead. amd64 uses the same liveness to compute an addition, increment or decrement into a register other than its first operand with one LEA instead of MOV and the flag-writing instruction.

## Correctness review fixes

- Edge-copy planning and emission use the same temporary-register pool, excluding variable homes and the workspace base. Register-cycle slots reload directly into GPR destinations. FP bit transfers preserve a scratch register that may already hold a completed destination.
- Scope transitions reserve the destination region before relocating live snapshots or restoring homes. Scope-only fragments include the FP bank when they restore FP homes.
- FP label parameters and compact-loop RCX pinning respect reserved variable homes.
- Straight-line workspace reports include the final emitted ABI saves and entry resources, using the same actual-access accounting as control-flow fragments.
- Home lookahead leaves room for instruction-local operands and results, including constants and Get/Set producers and the three FP operands of FMA. It checks that demand before extending the register pool. Leaf functions prefer volatile registers; functions with calls can prefer preserved homes.
- A scope's capacity limits the number of homes, without truncating its candidate list. This retains outer parameter homes even when those registers occur late in the candidate order.

## Instruction counts

Whole-function concrete assembly instruction counts; literal data directives are excluded. The 386 assembler groups frame operations with semicolons; each statement in those groups is counted separately. These are baseline CPU profiles: x86-64-v1, armv8.0-a, rv64gc, POWER8, and i686.

The review generator was built in a scratch copy of the repository. `idctvar` and `idctargs` use 64 samples and 8 coefficients; `sum` and `sumargs` traverse native words; the pressure programs retain 4 or 24 outer variables around an inner loop. The 386 generator itself was cross-built for 386 so native widths are 32 bits.

| Program | amd64 | arm64 | riscv64 | ppc64le | 386 |
|---|---:|---:|---:|---:|---:|
| idctvar | 453 → 361 | 376 → 289 | 506 → 398 | 616 → 514 | GP-resident FP (P1); not remeasured |
| idctargs | 428 → 375 | 333 → 312 | 446 → 420 | 587 → 537 | GP-resident FP (P1); not remeasured |
| sum | 29 → 10 | 30 → 10 | 28 → 10 | 33 → 11 | 52 → 42 |
| sumargs | 11 → 11 | 12 → 12 | 12 → 12 | 13 → 13 | 39 → 39 |
| pressure4 | 64 → 35 | 54 → 27 | 53 → 28 | 70 → 29 | 134 → 116 |
| pressure24 | 244 → 225 | 266 → 206 | 255 → 208 | 250 → 223 | 654 → 616 |

The Var sum loop has the same instruction count in its loop body as the label-argument version: five instructions for load, accumulation, pointer increment, counter decrement, and the back edge. Its complete listing is smaller on the four 64-bit backends, and needs neither a frame nor workspace. The 386 loop still uses its existing bytewise 32-bit load expansion and platform frame; the later SSE2 P1 implementation adds GP-resident scalar FP, without XMM homes.

The 64-bit `pressure4` example keeps all four outer variable homes across the inner scope and emits no spill at Enter. `pressure24` exceeds register capacity and spills. ABI preservation and genuine spills can still create a frame.

## Validation

The earlier **1,465 matrix checks with zero failures** did not establish correctness: the review found reproducible edge-copy, scope-transition, FP-home and workspace miscompiles missing from that coverage. That result is superseded by the regression and differential checks below.

The corrected implementation passes **1,465 Linux matrix checks, zero failures, 16 expected skips**. Native `go test ./...`, vet on all six architectures, and the harness unit tests pass. The matrix covers Go 1.26.9 and 1.27.2 and POWER8/9/10 for both PPC64 byte orders.

Long differential runs use seeds 0–2,999 plus seeds 3,000–3,099 per arithmetic mode and architecture, with both storage modes and two input sets. All six integer runs and all five FP runs pass: **34,100 generated seeds**, including **3,087 valid integer programs per architecture** and **3,082 valid mixed-FP programs per architecture**. Thirteen integer seeds and eighteen mixed-FP seeds generate invalid IR rejected by Finish and are counted separately. Valid cases require successful compilation and exact agreement with the reference interpreter; compilation failures are failures, rather than expected skips. This historical run predates 386 scalar FP; see the P1 validation below.

The high-pressure FMA operand test executes on arm64, riscv64 and ppc64le. amd64's v3 lowering is checked; execution is skipped on the baseline x86 CPU used by this run. The original reproducers also pass across the final package matrix; their native-width loads and C-call results follow each target's contract.

The review reproducers are now execution regressions for mixed variable/label homes, foreign and managed calls, multiple exits, large scope-exit grids, FP label conflicts, straight-line workspace saves, and compact counters. Little-endian memory inputs are explicitly encoded. Large managed frames that exceed existing registration limits are reported separately; the smaller reproducer cases must compile.

The original seeded generator has been replaced by the byte-stream harness described below. Its historical sweep counts above are not a claim that the new broader configurations passed.

Existing regression coverage includes direct home operands, snapshot overwrite, FP32/FP64 label constants and parallel-copy cycles, clean scope exit, native-width symbolic initializers, preserved-register restores with workspace, unused label slots accessed only by edges, and POWER carry liveness across a CR writer.

Commands run:

```sh
go test ./...
FZ_COUNT=3000 go test ./compile -run '^TestReviewFuzz$' -count=1
FZ_START=3000 FZ_COUNT=100 go test ./compile -run '^TestReviewFuzz$' -count=1
for arch in amd64 386 arm64 riscv64 ppc64 ppc64le; do
    GOOS=linux GOARCH="$arch" go vet ./...
done
go -C e2e test ./...
git diff --check

docker run --rm --privileged \
    -v "$PWD:/src:ro" -v nyanjit-e2e-cache:/cache -w /src \
    -e GOCACHE=/cache/backend-parity/build \
    -e GOMODCACHE=/cache/ppc64-foundation/mod -e GOTOOLCHAIN=local \
    nyanjit-e2e:ppc-elfv2 \
    go -C e2e run ./cmd/nyanjit-e2e -jobs 8 -deadline 45m -os linux \
    -junit /cache/results/var-review-linux-checked.xml e2e/matrix.star
```

The container registers binfmt inside its private user namespace; host binfmt state is unchanged. Linux acceptance covers both Go 1.26.9 and 1.27.2, including POWER8/9/10 on both PPC64 byte orders. The full package matrix includes compile, builder, and DCT reference/rendering tests. JUnit and measured listings are saved under the ignored `e2e/.build/results/` directory; current listings, differential results and logs are in `var-review/`. The matrix report is `var-review-linux-checked.xml`.

## Broader byte-stream fuzzing (2026-10-14)

The earlier seeded differential results above are historical, not the current
acceptance claim. The broader AST harness found additional workspace, flag,
paired-register and managed-entry failures. The in-tree replacement is
`compile/varfuzz`, with one interpreter shared by the corpus, fixed sweeps,
coverage-guided fuzzing and the minimizer.

The follow-up changes are:

- `StackNoUse` allocation, label homes and predicate temporaries exclude the
  workspace base. The RISC-V Go call gate initializes X9 from the workspace
  argument. A borrowed predicate register is saved only while it holds a live
  home, and restored before the conditional branch.
- arm64 AND flag facts describe the instruction's actual encoding width. A
  64-bit encoding used to implement a 32-bit mask cannot supply the narrow
  sign predicate.
- 386 wide multiplication and selection reserve their fixed scratch registers
  against variable homes. Paired selection stages inputs sequentially. Paired
  arithmetic, identity copies and sign extensions preserve live snapshots
  without requiring two or three simultaneously resident register pairs.
  Labeled predicates compare high words first and unsigned low words second.
- POWER selection can reuse a dead operand, including the condition, after it
  has been tested.
- Managed-call options take effect before variable-home planning. amd64 Go
  call effects include R15, which runtime assembly can overwrite during GC.
  The execution regression forces GC while a variable occupies that register.
- When a destructive instruction lacks a separate result register, the spill
  scheduler can preserve an ordinary SSA operand and reuse its register. It
  does not overwrite a reserved variable home.
- Constant comparison operands become immediates instead of spilled edge
  values. Slot-copy scratch saves are emitted only when the scratch holds an
  already completed destination.

### Running the harness

Normal `go test ./...` replays the checked-in fuzz corpus and a small sweep.
The byte decoder controls the AST directly; it does not seed `math/rand`.
Fuzz arguments also select integer/FP generation, outbound calls, the
compiler/ABI/stack configuration and runtime inputs. Go-entry configurations
compare `Code.Func` with `Code.Call`.

```sh
# Native arm64 or amd64 coverage-guided fuzzing.
go test ./compile/varfuzz -run '^$' -fuzz '^FuzzScopedVars$' \
    -fuzztime=10m -fuzzminimizetime=2s -parallel=2 -timeout=12m

# Every applicable configuration, integer and mixed FP modes.
NYANJIT_VAR_SWEEP=3000 go test ./compile/varfuzz \
    -run '^TestScopedVarsSweep$' -count=1 -v -parallel=4
```

Each sweep index checks a leaf, a program with native C calls, and a program
with mixed Go/C calls. Registered Go modules cannot be unloaded, so large
sweeps use bounded subprocess batches. The subprocesses work with the
container's private binfmt registrations. E2E `matrix.star` adds 2,000 cases
per mode on each Linux backend with Go 1.27.2 and the baseline CPU profile;
all version and CPU-profile variants still replay the regression corpus.

Historical programs are stored once as compressed, byte-identical streams in
`compile/varfuzz/testdata/legacy/`. `TestLegacyCorpus` replays all 32 programs
under config indices 0–7: **256 seed/mode/config pairs**. It asserts that both
arithmetic modes for seeds 239, 421, 1949, 445, 510, 602, 6, 102, 166 and 217
remain present. 386 now also replays all 128 FP pairs using SSE2.

The mutation corpus contains one entry each for minimized workspace,
narrow-sign, paired-multiply, POWER Select and managed-entry shapes. Config
255 makes these entries replay every applicable configuration; ordinary
mutated trees select one configuration. Genuine future crashers keep their
native Go fuzz encoding. All cases share the AST interpreter, while exact
reviewer Builders remain separate execution regressions.

Corpus storage fell from **5.9 MB to 176 KB on disk** (31,828 bytes of file
contents). Every decompressed legacy stream has the same SHA-256 as before
cleanup. The generated-file attributes cover the native fuzz corpus.

A failing Go fuzz run writes a corpus file under
`compile/varfuzz/testdata/fuzz/FuzzScopedVars/`. Replay it with the command Go
prints, then retain that file with the fix so ordinary tests cover it. Fuzz
input format changes require updating the decoder and existing corpus
alongside one another.

For crash or hang minimization, supply the raw AST decision bytes:

```sh
NYANJIT_VAR_MINIMIZE=/tmp/case.bytes NYANJIT_VAR_CONFIG=0 \
    go test ./compile/varfuzz -run '^TestScopedVarsMinimize$' -v
```

A stored legacy input can be extracted with
`gzip -dc compile/varfuzz/testdata/legacy/seed-239-false.gz > /tmp/case.bytes`.
`NYANJIT_VAR_FP=1` selects FP mode. The helper reduces the AST in subprocesses
with deadlines and prints the corresponding Builder code. Set
`NYANJIT_VAR_LAUNCH` to a command prefix when explicit QEMU invocation is
needed; normal private-binfmt execution needs no prefix. Minimization does
not run by default.

386 uses GP words/pairs for FP until XMM residency is implemented. Insufficient caller stack budgets and
managed frames exceeding registration limits are reported separately from
execution passes; arbitrary `ErrBackendLimit` errors are failures. Managed
frame errors report required bytes and the limit, preserving `errors.Is`.

The larger managed-entry sweep also found page-sized registered text ranges
that touch at their endpoints. Go's plugin registrar checks those endpoints
inclusively. Managed mappings now reserve space beyond the actual text while
registering the original code length. `goregister.Register` rejects touching
ranges before publishing metadata, and a regression checks adjacent pages.
This changes mapping size, not instructions or frame layout.

### Final validation

- Native `go test ./...`, vet on amd64/386/arm64/riscv64/ppc64/ppc64le, and
  the E2E harness tests pass.
- The final Linux matrix has **1,473 passes, 16 expected skips, zero failures**
  across Go 1.26.9/1.27.2 and POWER8/9/10 variants.
- Standalone sweeps run 3,000 accepted indices per arithmetic mode. Each index
  generates leaf, native-call and mixed-call programs and checks every
  applicable configuration. All six integer sweeps and all five mixed-FP
  sweeps pass: **450,627 successful execution checks** and **80,934 reported
  stack/frame limits**, with no unexpected errors or result mismatches.
  This historical count predates 386 scalar FP. These limits are not counted as
  successful executions.
- Two native arm64 coverage-guided sessions each ran for ten minutes, with
  **46,832** and **124,175** executions and no failures. The latter includes
  the final destructive-operand reuse and comparison-folding changes.
- Sum, pressure and IDCT instruction counts are unchanged from the table above
  on all six backends, including both PPC64 byte orders.
- Corpus cleanup replays all 256 legacy pairs on every FP-capable backend;
  386 now runs both sets of 128 pairs.

The fixed-sweep logs, configuration counts, unchanged instruction counts,
corpus checksums and matrix summary are retained in the ignored
`e2e/.build/results/var-fuzz/` directory. The verified JUnit report is
`/cache/results/var-fuzz-linux-verified.xml` in the E2E cache volume. No host
binfmt registrations, packages or services were changed, and no commits were
made by this task.

### 386 scalar FP (P1)

FP32 and FP64 variables, snapshots and label arguments now execute through the
ordinary GP-home/word-pair machinery. Each arithmetic operation transfers its
inputs through SSE2 temporaries; persistent XMM variable homes remain a later
phase. Scoped call preservation, snapshot-overwrite and label-cycle tests now
include 386. At P1, the workspace-pressure test used FP32 on 386 (retaining
FP64 on 64-bit targets), avoiding the paired-Select platform-frame requirement;
separate FP64 snapshot and label-cycle tests exercised both StackPlatform and
StackNoUse. P2 below restores FP64 workspace-pressure coverage on 386.

`NYANJIT_VAR_FP_ONLY=1 NYANJIT_VAR_SWEEP=3000` runs the FP acceptance sweep.
Every accepted index generates leaf, native-call and mixed-call programs and
tries every applicable configuration. Known stack/frame limits are reported
separately; unexpected compilation errors and value mismatches fail the test.

The companion `Test386FloatWorkspaceSweep` omits paired integer arithmetic that
requires preserved scratch registers. Its 3,000 programs × four configurations
(C/Go entry × platform/workspace) all execute: **12,000 exact matches and zero
limits**. Programs vary FP32/FP64 width, up to 12 live variables, arithmetic,
sqrt/conversion, nested scope entry/exit, and old snapshots after Set. This
separates workspace FP correctness from the mixed generator's integer limits.

The mixed 386 P1 sweep also completes: **3,000 accepted cases**, generating
leaf, native-C-call and mixed-Go/C-call variants, with **29,461 successful
checks**, **18,860 known stack/frame limits**, zero unexpected errors and
zero mismatches. Limits are not successful executions. The counts are:

| Configuration | Executed | Expected limits | Limit rate |
| --- | ---: | ---: | ---: |
| Compile/C/NoUse | 0 | 4,547 | 100.0% |
| Compile/C/Platform | 4,547 | 0 | 0.0% |
| Compile/C/Platform/Compact | 4,547 | 0 | 0.0% |
| Compile/C/Platform128 | 1,005 | 3,542 | 77.9% |
| Compile/Go/NoUse | 0 | 4,547 | 100.0% |
| Compile/Go/Platform+Func | 4,547 | 0 | 0.0% |
| Foreign/C/Platform | 3,039 | 0 | 0.0% |
| Go/C/Platform | 5,888 | 3,112 | 34.6% |
| Go/Go/Platform+Func | 5,888 | 3,112 | 34.6% |

Workspace limits in the mixed corpus come from existing paired integer
multiply/shift/Select scratch requirements, not SSE2 staging. The small-frame
configuration exceeds its 128-byte budget; managed cases exceed the existing
240-byte registration bound. FP-specific allocation-pressure failures are zero.
The strict FP-only sweep above requires every configuration to execute.
Full validation also passes on native ARM64, emulated AMD64/RISC-V/PPC64LE,
and 386 baseline/max on Go 1.26.9 and 1.27.2 (64 checks per version). Final vet
covers those five architectures. Big-endian PPC64 validation is omitted at the
user's request. Logs and commands are in `e2e/.build/results/386-fp/`.

### 386 inline math and workspace word operations (P2)

P2 removes the P1 workspace restrictions for paired immediate shifts/rotations,
integer multiplication and Select. Their spill scheduler reserves durable homes
and streams the words through three GPRs; EBP remains the workspace base.
The original FP64 workspace-pressure test now runs on 386 as well. All math
entry points, both widths, pass execution and unchanged ULP-bound accuracy tests;
dctlogo passes its direct-DCT comparisons through all 594 coefficients. Baseline
AMD64/386 outputs match over 56 function/width digests (145 samples each,
canonical NaN payloads and preserved signed zeros).

The P2 3,000-case mixed-FP sweep executes every NoUse case successfully. This
supersedes P1's paired-integer scratch limits; the small caller-frame budget and
managed-frame registration limits remain separate from successful executions.

| Configuration | Executed | Expected limits | Limit rate |
| --- | ---: | ---: | ---: |
| Compile/C/NoUse | 4,547 | 0 | 0.0% |
| Compile/C/Platform | 4,547 | 0 | 0.0% |
| Compile/C/Platform/Compact | 4,547 | 0 | 0.0% |
| Compile/C/Platform128 | 1,005 | 3,542 | 77.9% |
| Compile/Go/NoUse | 4,547 | 0 | 0.0% |
| Compile/Go/Platform+Func | 4,547 | 0 | 0.0% |
| Foreign/C/Platform | 3,039 | 0 | 0.0% |
| Go/C/Platform | 5,888 | 3,112 | 34.6% |
| Go/Go/Platform+Func | 5,888 | 3,112 | 34.6% |

Total: **38,555 successful checks**, **9,766 known limits**, zero unexpected
errors or mismatches. The strict FP sweep also repeats with 12,000 executions
and zero limits. Full package tests and vet pass on ARM64, AMD64, 386, RISC-V
and PPC64LE. The 386 E2E suite passes 64 checks per version on Go 1.26.9 and
1.27.2. P2 evidence is in `e2e/.build/results/386-fp-p2/`.

### 386 XMM residency (P3)

FP32 and FP64 variables can now have XMM3–7 homes. Home storage width is
independent of register residency: FP32 uses one 4-byte slot and FP64 uses two.
Scope transitions preserve that width; ordinary temporaries compete in the
same FP bank while EBP remains the StackNoUse workspace base. C and Go ABI0
calls spill live FP values and homes because all XMM registers are caller-saved.

FP label parameters can use XMM registers even when their integer ABI width is
two words. Edge copies use `movapd`; cycles on 386 use a reserved XMM temporary,
never a truncating GPR copy. Overflow-slot paths preserve both words of FP64
and touch only one word for FP32. Regression coverage includes mixed integer/FP
branch hints, scope exports, native/managed calls and six-value label cycles
with one overflow parameter, in both storage modes.

Current whole-function measurements include each grouped frame statement and
`ret`: a ten-add FP64 chain changes from **114 to 31 instructions**, with bank
copies falling from 60 to four `movd` instructions (entry/return only). A Var
recurrence changes from **115 to 69 instructions**, with bank copies falling
from 24 to eight. The recurrence uses a 64-bit counter in both measurements.
The review's native-386 generator, using 32-bit pointers/counters and full inline
cosine, emits **1,687 instructions** for `idctvar` and **2,070** for `idctargs`.
These new inline-math counts are distinct from the historical table above.
The Var coefficient loop contains no arithmetic-result GP round-trips; its
coefficient loads still cross from the integer memory interface into XMM.
Constants remain GP-materialized until P4 supplies the absolute-address pool.

P3 repeats the 3,000-case mixed-FP sweep with **38,555 successful checks**,
**9,766 known frame-budget limits**, and zero FP allocation-pressure failures,
unexpected errors or mismatches. Counts per configuration match P2 exactly;
NoUse still executes every case under both entry ABIs. The strict FP sweep
also passes all **12,000 executions** without limits. Full packages pass on
ARM64, AMD64, 386, RISC-V and PPC64LE, and the vet script and nested harness
unit tests pass. Evidence is in `e2e/.build/results/386-fp-p3/`.

The 386 E2E suite passes **64 checks per version** on Go 1.26.9 and 1.27.2,
with zero failures or skips. All 56 baseline math digests also match AMD64 and
P2. P4's absolute-address FP literal pool remains pending.

### 386 literal pools (P4)

FP constants now load directly into XMM from a shared, aligned pool. Constants
in FP variable homes and edge destinations no longer require GP word-pair
staging. Sign/absolute masks can remain in FP when their consumers are FP.
Scope and call spill widths remain FP32=one slot and FP64=two slots.

Absolute 386 pool references are code-local relocation records. Each Bind
patches its own RW mapping before sealing RX or registering a Go frame. Tests
keep two bindings live and check both pool addresses, unchanged template bytes,
alignment, deduplication, range errors and external symbol resolution. Raw
emission that lacks a mapping base returns `ErrCodeBaseRequired`; text assembly
keeps symbolic pool offsets, and `Template.Bind` links automatically after `Unit.Template()`.

The Var recurrence fixture changes from **69 to 61 instructions** and from
eight to four GP/XMM `movd` instructions. The constant-free ten-add chain stays
at 31 instructions and four bank transfers. A multiply/add using one shared
FP64 constant is bounded at **13 instructions** and one pool load. The original
native-width 386 IDCT generator changes from **1,687 to 1,556** instructions
for `idctvar` and from **2,070 to 1,943** for `idctargs`; literal data is excluded.
P4 evidence is in `e2e/.build/results/386-fp-p4/`.

P4 passes full packages on ARM64, AMD64, 386, RISC-V and PPC64LE, final vet on
those targets, and the nested harness tests. The 386 E2E matrix passes **64
checks per version** on Go 1.26.9 and 1.27.2 with zero failures. Final rebinding
and resolver tests also pass on both versions. The strict FP sweep executes
**12,000 checks** with zero limits; the mixed 3,000-case sweep repeats P3's
**38,555 successful checks**, **9,766 known frame-budget limits**, and zero
unexpected errors or mismatches. All 56 baseline math digests match AMD64 and
P3. P0–P4 are complete; FMA and native packed SIMD on 386 remain out of scope.

### Predicate bounds

Builder branches and public IR compilation accept predicate graphs up to
`ir.MaxPredicateNodes` (16,384) nodes after expanding shared subtrees, with
no independent depth limit. Larger predicates return `ErrBackendLimit` before
lowering. IR validation reports
`ir.ErrPredicateLimit`. The entry checks, public graph copying/rebasing, and
Builder value collection use worklists, so rejecting an arbitrarily deep tree
also works with a small Go stack. The expanded-size bound prevents compact DAGs
from generating exponential short-circuit branch sequences.

### Scope planning and spill-storage limits

Home planning keeps sparse per-region register overrides and fixed per-variable spill slots. `machine.HomePlan.Lookup(region, variable)` supplies the complete visible home, including inherited spill homes. Compiler-private compact plans store overrides in the additive `RegisterOverrides` field and leave `Regions` nil; public dense plans retain complete region maps in `Regions` with its original meaning. Per-region register masks avoid scanning every Var during allocation. Scope entry/exit, next-use queries and FP-home presence are computed incrementally or once per function. Builder capture eligibility is recorded lazily at entry, and ownership lists avoid rescanning the whole function at Leave.

The current backend allocator supports 4096 logical spill words per allocation (4095 for every ARM64 control-flow program, including leaf functions, to retain scaled 12-bit stack addressing). Every declared Var receives fixed home slots for all its target-sized words, even when it lives in a disjoint sibling scope or has a register home. Slots are not reused between scopes. Thus the limit counts total declared Var words, not peak live Var words: 4100 sibling scopes with one word-sized Var each exceed it even if only one of those Vars is live at a time. A 64-bit Var consumes two words on 386 and one on the 64-bit targets.

Entry parameters and temporary SSA spills also consume this budget; edge and ABI storage can impose additional frame limits. The 4000-Var stress fixture reserves 4001 words for homes/parameters, then exceeds the remaining budget with live snapshots. Depth 8000 with one Var per level exceeds the budget from its homes alone. These are spill-storage limits, not a shortage of allocatable registers; a larger `Func.Stack.BytesAvailable` does not raise them. `ManagedFrameLimit()` is a separate, usually smaller runtime registration limit.

`TestPrepareScaling` and `TestBuilderScaling` cover ordinary SSA code, many Vars, deep scopes, nested loops, and loops inside nested scopes. The scope and nested-loop workloads use sizes 200, 400, 800 and 1600 so small-constant quadratic ancestor walks become visible. They fit log-log timing slopes across four doubling-spaced sizes (an eightfold span), using the median of five interleaved runs against the unchanged 2.3x per-doubling threshold. This reduces sensitivity to one noisy endpoint; the gate also checks that it rejects the measured pre-fix quadratic Var case. Enable both with `NYANJIT_PERF_SCALING=1`; CI runs both gates.
