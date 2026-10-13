# Compiler Architecture and Implementation Evidence

This is the internal guide for changing NyanJIT's compiler, targets and runtime integration. Consumer contracts live in [README.md](../../README.md), the [IR specification](../../docs/ir.md), [compilation tiers](../../docs/dual-tier-design.md), [sealed programs](../../docs/sealed-programs.md), [CPU profiles](../../docs/cpu-profiles.md), [backend operations](../../docs/backend-parity.md), [SIMD](../../docs/simd-design.md) and [hardening](../../docs/optional-hardening-design.md). Target encoder contracts remain in `backend/target/*/FORMS.md`.

Implementation decisions and historical acceptance evidence formerly spread across `docs/` are consolidated here. Current mechanisms are described first; dated measurements and archived checklists establish what was tested at the time, not a fresh acceptance claim. Pending statuses in archived records are historical: use [TODO.md](../../TODO.md) for current follow-ups and recheck code before acting on an old roadmap.

## Contents

- [Compilation tiers](#compilation-tiers)
- [Scopes and variable homes](#scopes-and-variable-homes)
- [Floating-point lowering](#floating-point-lowering)
- [SIMD lowering](#simd-lowering)
- [Layout, effects and execution](#layout-effects-and-execution)
- [Verification](#verification)
- [Historical measurements](#historical-measurements)
- [Backend parity validation](#backend-parity-validation)
- [Target validation records](#target-validation-records)
- [Compile-time baseline](#compile-time-baseline)
- [Document provenance](#document-provenance)

## Compilation tiers

The root Builder uses one scope/dominance/Var dialect. Label arguments remain available to raw IR front ends; Builder merges use Vars. The tier belongs to the cached `compile.Target`, not to `compile.Func`. A bound Builder drives `compile.Stream` with reusable `ir.Segment` storage. Unbound Finish produces a reusable sealed program accepted by either tier.

`builder` depends on `ir`; `compile` depends on `ir` and the public backend module. A per-operation sink was rejected because pointers/slices escaped through it (14-39 ns plus an allocation per operation in the prototype), it froze a wide protocol, and importing compile from builder introduced cycles. Block-segment handoff measured about 0.2 ns per operation. The early proposal's 16-byte per-value record became 20 bytes because literals and Add/Sub operands also support Carry and address folding across segments.

The optimized path seals, promotes eligible Vars, threads cross-block uses, selects instructions, allocates registers/spills, lays out physical code and assembles. Var promotion uses Braun-style construction: Set defines a value, Get reads the reaching definition, and merges become internal block parameters. It preserves homes for loop-carried state under the per-block allocator, FP state crossing blocks, and Get feeding a CallGo target; backend-limit failures retry with homes. Internal `NYANJIT_INTERNAL_VAR_PROMOTION=0` disables promotion and `strict` makes its errors fatal. These switches are implementation diagnostics.

The baseline records compact operations with payload arenas, computes block/dominance/loop information and cross-block liveness, then allocates and encodes in layout order. Within blocks it uses next-use eviction with clean values first, rematerializes eligible constants, and chooses GP/FP banks from definitions and consumers. Vars receive slots; weighted hot Vars are pinned and others cached. First arrival fixes label state, loop headers use their forward entry, and later edges reconcile through `regalloc.PlanEdgeTransfer`, with out-of-line taken-edge stubs when needed. Handwritten sizes must match the exact emitted operand-dependent forms.

Baseline emission calls target ByteAssembler methods directly, reusing target constraints, frame/literal finishing, reports and Templates. This avoids the optimized physical Stream/Visit/layout path. Vectors and masks are scalarized with the shared legalizer; scalar segments avoid that work until needed. Raw vector/mask label parameters are unsupported by a baseline Stream. Baseline managed frames and hardening use the optimized tier's acceptance and runtime contracts, with different code. On 386 baseline 64-bit values live in XMM registers; optimized integer lowering uses GP pairs.

The earlier proposal said indexed views would stop using private label parameters and that baseline managed calls were absent. Implemented behavior supersedes both claims: checked indexing retains the success-edge base/index parameters for hardened clamping, and baseline supports managed calls on all six targets. Scope-entry splits now happen when a block is emitted; dominated values cross without synthesized capture Vars. Do not restore removed JumpArgs/BindParams APIs or proposed dual-mapped arenas from old design discussions. Batched strict W^X `Arena.BindAll` was chosen first.

The backend's reusable Online/Batch allocator protocol is separate from compile's faster direct allocation path. The experimental compile port exceeded its +5% cost gate (+5..+14%) and was stopped. See [scopes/allocation memory](LTM/scopes-and-register-allocation.md) before revisiting that decision.

The original tier investigation measured a 1,270-program corpus: 88% of values had one use, 82% were consumed by the next instruction, and 0.7% crossed a block outside edge arguments. A naive cache without liveness generated 6.3 times the frame traffic; exact use counts cut that traffic by 74%, with true liveness/next-use eviction best. Those observations motivated compact recording plus analysis rather than a separate user-managed register API, Drop hints or a purely streaming register cache. They are historical design evidence, not universal workload statistics.

Key files: `builder/segment.go`, `compile/stream.go`, `compile/var_promotion.go`, `compile/cross_block.go`, `compile/baseline_analysis.go`, `compile/baseline_alloc.go`, `compile/baseline_emit.go` and `compile/baseline_*target.go`.

## Scopes and variable homes

A Get may lend a register home to its snapshot; destructive arithmetic must relocate or spill a still-live snapshot before Set overwrites that home. Adjacent Set can coalesce with a producer only when the old snapshot is dead. Add/subtract zero becomes an identity copy. Jumps without arguments must not materialize a bookkeeping zero.

Home planning accounts for ordinary temporaries separately in each bank, including constants, Get/Set producers and all three FMA operands. Inner homes take priority under pressure; unused outer homes can spill at Enter and reload at Leave. Clean homes need no redundant writeback. Straight-line return preferences must not displace an occupied parameter home; control-flow home planning keeps result resources available for its exit machinery.

Scope transitions reserve the destination region before relocating snapshots or restoring homes, including FP homes in scope-only fragments. Edge planning and emission use the same scratch pool, excluding homes and workspace bases. Cycle slots reload directly to destinations; FP bank copies must preserve scratch that already holds a completed destination. Entry saves, workspace-only returns, edge accesses and ABI resources all contribute to actual-access reporting. Merely reserving a home slot does not touch memory or imply a frame.

Historical compact HomePlan representations used sparse per-region `RegisterOverrides` and `HomePlan.Lookup(region, variable)` rather than copying dense Regions. Agreement tests caught an uncached-compact fallback reading only Regions. Public names and representations may evolve; consult current `backend/machine` and `backend/regalloc` before changing a representation. Scope/next-use/FP-home analyses are computed incrementally or once, with lazy capture eligibility and ownership lists avoiding whole-function rescans.

Every declared Var word has fixed spill storage, including register homes and disjoint sibling scopes; slots are not reused between scopes. The documented allocator budget is 4,096 logical words (4,095 for ARM64 control flow), shared with parameters and temporary spills. A 64-bit Var needs two words on 386. Thus 4,100 sibling one-word Vars can exceed storage even if only one is live, and raising Stack.BytesAvailable does not raise this limit. Managed-frame and edge/ABI requirements impose separate limits. Reuse was rejected during the earlier allocation work because it changes lifetimes, scheduling and listing offsets; the follow-up lives in TODO.md.

Critical regressions include reserved homes overwritten by 386 multiply/select scratch, ARM AND facts taken at the wrong encoding width, missing amd64 Go-call R15 clobbers during GC, and loss of the workspace base under StackNoUse. Parameters seven/eight on amd64 must use fallible ABI lookup, not index a six-register array; the RISC-V Go argument bank has sixteen registers, not the earlier nine.

Key files: `compile/variable_codegen.go`, `compile/home_candidates.go`, `compile/scoped_home_test.go`, `compile/allocation_online_homes.go`, `backend/regalloc/` and `compile/varfuzz/`.

## Floating-point lowering

Scalar FP uses IEEE bit-pattern Values; native ABI FP passing is a separate boundary feature. The original improvement sequence was exact-product selection, shared literals, then FP residency. `ir.OpFProductError(A,B,C)` uses FMA(A,B,-C) when the profile permits, otherwise separately rounded Dekker steps. C must be the correctly rounded product of finite well-scaled inputs without split/intermediate overflow or underflow; validation checks structure, not those numeric preconditions. Hidden Builder provenance was rejected because Programs are public.

Use analysis assigns resident FP results to the bank shared with SIMD. Bit-preserving GPR copies are cached by value ID and consuming width; FConvert uses its source width. Integer operations, predicates, calls and returns introduce bank boundaries. Wrong-bank hints must be rejected even when the hinted register is free. Sign/absolute operations use native FP or mask forms for FP consumers; general bit manipulation stays integer.

Expansion temporaries remain reserved. The original volatile home sets were XMM3-14, ARM V0-7/V19-29, RISC-V F3-7/F10-17/F28-31 and POWER F3-13; Go's XMM15 zero register and partially preserved ARM V8-15 are excluded. On 386 homes use XMM3-7 and XMM0-2 scratch. FP32 storage is one four-byte 386 slot and FP64 two; XMM residency does not change that width. Label cycles use reserved XMM scratch rather than truncating GP copies. Calls conservatively spill FP/vector state.

### Literal pools and reach

Pools deduplicate read-only constants after the final return. Scalar four/eight-byte literals are naturally aligned; vector/mask operands retain their required alignment. Older measurements below used the original sixteen-byte pool scheme. Data/padding directives are excluded from instruction budgets, but grouped frame statements and spills are counted. Bytes become read-only with RX publication.

| Target | Scalar pool access |
|--------|--------------------|
| amd64 | RIP-relative MOVSS/MOVSD |
| 386 | Absolute MOVSS/MOVSD, rebased per binding |
| arm64 | Literal LDR S/D; LDR Q for SIMD |
| riscv64 | AUIPC X31 plus FLW/FLD |
| ppc64le | LR-preserving PC acquisition through R11/R12, then LFS/LFD |
| loong64 | PCADDU12I plus FLD.S/FLD.D |

POWER8 needs six instructions to acquire PC without a TOC assumption. Managed literal leaves reserve a frame so the unwinder finds saved LR while the sequence temporarily puts local PC in LR. One base per function is deferred until allocation, call survival and LR scheduling can be handled together.

ARM literal LDR has a signed nineteen-bit word offset. Far access is ADR/MOVZ/MOVK/ADD-SXTW/LDR using a signed 32-bit relative offset, independent of mapping/page base; larger reach fails with ErrBackendLimit. Compact once, then alternate literal-load growth and out-of-range short-branch growth until neither changes. Both promotions are monotone. RelaxBranches itself only shrinks branches. Resolve branches/pool offsets after convergence; finishLiterals must not resize existing instructions. The regression puts a pool sixteen bytes inside reach before TBZ growth pushes it outside; check backward edges, growth before the load, stable repeated PCs and byte/text sizes.

386 `Reloc386Literal` records identify code-local pool offsets. Each Bind links symbols, maps RW, patches a fresh copy to that mapping, then seals RX/registers the frame. Check load shape, complete literal footprint and 32-bit address range. External resolvers receive no code-local pseudo-symbols. Template.Bytes returns ErrCodeBaseRequired without a final base; Bind, LinkAt, machine.LinkRelocationsAt and execution.MapWithRelocations supply the appropriate binding paths.

### Representation-specific corrections

POWER8 XSCVDPSPN defines word zero, leaving the following words undefined, so `xxsldwi vs0,vs0,vs0,3` precedes MFVSRWZ. POWER9/10 can use the short form. Source references are [ISA 2.07B, printed page 412](https://ibm.ent.box.com/shared/static/jd5w15gz301s5b5dt375mshpq9c3lh4u.pdf) and [ISA 3.0C, printed page 541](https://files.openpowerfoundation.org/s/eRGYZi56BXmCSY7/download). RISC-V binary32 copies retain NaN boxing; POWER stores singles in double-format FPRs. RISC-V sign injection lists fmv/fneg/fabs when sources match, otherwise fsgnj/fsgnjn/fsgnjx with S/D suffixes.

386 P2 streams paired shifts/rotations, multiply and Select through durable homes with only three volatile GPRs; EBP remains workspace. SHLD/SHRD handle cross-word shifts, multiply retains low results in a reusable slot, and Select processes words through a full-width mask. Wide symbols retain both immediate halves; memory addresses use the low native pointer word. Baseline math digests compare 56 function/width combinations over 145 inputs with canonical NaNs and retained signed zero; do not confuse earlier helper-call IDCT counts with later inline cosine counts.

Key files: `compile/literals.go`, `compile/report_fp.go`, `compile/math_budget_test.go`, `backend/machine/`, `backend/target/*/` and the 386 FP/relocation execution tests.

## SIMD lowering

Consumer shapes, semantics, feature queries and examples remain in [SIMD documentation](../../docs/simd-design.md). Internally, chunks share the scalar FP residency bank, split logically rather than requiring consecutive physical registers, and spill with full-vector width/alignment. Allocators own preservation; emitters do not blanket-save clobbers. The conservative call/syscall policy spills native vectors. AAPCS64 preserves only low halves of V8-V15; LP64D preserves only low halves of f24-f31, so those aliases cannot preserve a live 128-bit vector.

Native constants use aligned pools and user memory uses unaligned-safe accesses. amd64 XMM15 is never an expansion temporary. 386 two-address plans preserve an aliased right input in XMM1; XMM0 supports rotate/extract/insert and XMM2 GP-pair edges. FP/vector pressure competes for the five XMM3-7 homes. ARM distinct-destination immediate rotations can use two instructions (USHR+SLI or SHL+SRI); aliased destinations use opposite shifts/OR through V30.

Current control-flow/Var legalization scalarizes vectors and masks; native label homes, vector ABI boundaries, scalable predicates, wider native ISAs and floating/reduction extensions remain separate roadmap subjects, not implemented promises. Planned preserved-bit call data and Windows/SVE/RVV state rules in the former design are requirements for future ports, not claims of support.

LoongArch LSX aliases vr0-31 low halves with f0-31. f8-23 are shared homes, f0-2 expansion scratch, f3-7 excluded and f24-31 cannot keep full vectors across calls. Each vector takes two 64-bit storage words. Native forms include unaligned loads/stores, PC-relative constants, lane transfers, arithmetic/bitwise/multiply, all integer immediate shifts/rotations and guarded variable shifts. Shift saturation checks the full unsigned count before hardware masking. Byte equality/masks use VSEQ.B and VMSKLTZ.B/VPICKVE2GR.WU; unsupported comparisons/carryless products scalarize. Reports use shared F-register names and actual temporary writes. Go assembler goldens and QEMU pin LSX words because x/arch loong64asm v0.32.0 has no LSX decoder; the Volume 2 manual was a TBD stub in the recorded review.

Tests need independent lane oracles, native/forced-scalar modes, all-ones/INT_MIN/zero lanes, in-place and partial overlap, high-bit counts, result feedback, reload pressure and call clobbering. Shift tests batch all counts 0-255 across widths and lane shapes. Carryless selectors must cover all four combinations at 128/256/512 bits, including low/high polynomial word order. Scoped-var fuzzing does not generate vectors; dedicated tests remain necessary. Planned floating operations require NaN-class comparisons, signed-zero distinctions, fused/separate-rounding contracts and fixed-tree reduction oracles.

### SIMD roadmap contracts

The former public design mixed implemented operations with the proposals below. These remain internal planning material, not callable API promises:

- Additional integer operations: AndNot, Neg, Abs, signed/unsigned Min/Max, saturating Add/Sub and signed/unsigned MulHigh.
- Floating lanes: FAdd/FSub/FMul/FDiv/FSqrt/FMA/FMulAdd, FMin/FMax, relaxed minimum/maximum and FAbs/FNeg; general mask And/Or/Not, AnyTrue/AllTrue and Blend; dynamic Swizzle; narrowing/saturating narrowing, integer/float conversions and Bitcast; integer and FP reductions; partial/gather/scatter memory; native vector entry/exit ABI.
- Wider AVX2/LASX, POWER VSX lane-number translation, RVV and armv7 integer NEON; later AVX-512/SVE predicate allocation, checked scalable vector length and masked memory. Recognition of a CPU extension does not establish these lowerings.

Proposed floating semantics are round-to-nearest ties-to-even with subnormals, NaNs compared by quiet class rather than payload/sign, strict minimum preferring -0 and maximum +0, and strict NaN propagation. Relaxed min/max may select either operand for NaNs or mixed-sign zero. Float-to-int rounds toward zero, saturates overflow and maps NaN to zero; relaxed conversion leaves out-of-range results unspecified. FMA requires single rounding and must reject unavailable fusion until exact software support exists; FMulAdd may fuse or round separately. Lowering must establish and restore the required FP environment, with calls invalidating assumptions; armv7 NEON flushing cannot satisfy the strict rule and needs scalar VFP/software fallback.

FReduceAdd's proposed order is a fixed pairwise tree: add adjacent lanes, repeat on partial sums, carry an unmatched final lane unchanged, and add no synthetic +0 leaf. It is not a left fold. Integer reductions wrap at lane width. Narrowing keeps lane count and halves lane width; Swizzle zeroes out-of-range lanes. Partial loads zero inactive lanes without accessing their addresses; partial stores touch active lanes only, with runtime count clamped to lane count.

Proposed SVE code records assumed vector length in bytes and may emit an rdvl/trap entry check; without a check the caller must ensure that length every invocation. NEON/SVE interchange is never inferred solely from feature flags. Preserved-bit call data must model AAPCS64 low halves, Windows x64's low 128 bits of XMM6-15 plus unwind records, destructive wider AVX calls/vzeroupper, SVE predicate/upper-Z clobbers and RVV configuration ownership. These are requirements for future ports, not present support.

The earlier illustrative future examples were SAXPY with FMulAdd/scalable tails; byte reduction through widening plus uaddlv/psadbw; saturating conversions (NEON fcvtzs maps NaN, 3e9, -3e9, 1.9 to 0, INT32_MAX, INT32_MIN, 1, with x86 overflow/NaN fixups); and strict/relaxed floating minima. Their PCs, register choices and performance were not established. Implemented integer examples remain in the consumer SIMD reference.

## Layout, effects and execution

Selection keeps semantic operations until the target can choose immediate/fixed-register forms. The physical emitter owns order, PC accounting and flag facts; byte/text visitors encode that same plan. Invalid used fields must fail before truncation, sizes must agree, and effects include hidden expansion writes. Calls/syscalls kill their actual flag facts; MOVZX masks do not create flags. 386 OF liveness protects ADD feeding JO.

Selection propagates fixed call-register preferences through eligible producers, reuses dead operands, folds addresses and leaves fixed CL/RAX/RDX or paired-386 constraints to allocation. Preserved GPRs follow volatile ones; a C entry first evicts a value already backed by a slot rather than immediately paying entry saves. A live-across-call value moves to a free preserved register before spilling. Exposed homes reload after callees may mutate them. Wide carry/high products stay semantic until selection: eligible POWER carry chains use addc/adde or subfc/subfe, consume scalar carry's low bit, and fuse single-use carry extraction only when provenance still matches. Shared carries remain explicit values.

POWER tracks CR0 and XER independently. Logical operations preserve carry; immediate arithmetic right shifts kill carry but preserve CR0, while register-count shift guards overwrite CR0 and arithmetic forms also kill carry. Register-count rotations preserve both; comparisons preserve XER. Carry tests copy XER to volatile CR7 while preserving CR0 (POWER8 mfxer/srdi/mtcrf; POWER9/10 mcrxrx and CA32). Identity/widening aliases are width-proven; truncation must not invent them, and high-word sign/zero facts must not invent binary carry.

PPC PreferCompactLoops uses CTR only for natural countdown loops whose counter uses, dedicated-counter effects and individual CFG flag liveness permit it. External entries initialize CTR, backedges bypass initialization and fallthrough restores the GPR counter to zero. Calls, overlapping CTR loops, side entries, early exits and observable counters retain the original form. Out-of-range bdnz relaxes through flag-preserving bdz/b. ARM can use SUB/CBNZ when outgoing flags are dead, and RISC targets use direct register branches; amd64 LOOP selection has its own register/flag constraints.

Preserved-register planning must account for FP-to-GPR and late edge writes; repeat lowering with required saves when an unsaved write appears. Branch/literal passes return scratch on success and error, avoiding aliasing that lets a later clear erase live instructions. New source fields must participate in packed storage, side tables, decode and tests; packed and located sizes are Go-host properties (64/136 bytes on a 64-bit host, 52/88 on a 32-bit host).

Narrow-memory lowering may retry once with bytes when a native plan exceeds register/frame limits. Widen a 32-bit address before adding byte offsets on a 64-bit target. Discarded loads still perform the access. Go Data alignment promises and public unaligned-access semantics are independent of this retry policy.

Managed frames reserve runtime-required top words and argument homes; ARM gap-free managed entry and RISC-V/LoongArch save-RA-before-SP ordering are unwinder requirements. C-thread signal-window and real POWER/LoongArch cache-publication questions remain in TODO.md. Execute stack-growth/trace/profiling regressions on fresh goroutines; ordinary call loops can hide runtime corruption.

For scratch/output ownership, buffer histories and packed store-forwarding pitfalls, start with [compile-time synthesis](LTM/compile-time-and-scratch-memory-synthesis.md). For program-wide pins, borrowed Data and registration/Code lifetime, use [managed-execution synthesis](LTM/data-and-managed-execution-synthesis.md).

## Verification

Use [e2e/README.md](../../e2e/README.md) for current cross-platform commands and [verification memory](LTM/verification-benchmarking-and-review-practice.md) for review/benchmark methods. Old commands naming removed tests, big-endian execution or cached container images are historical evidence, not the current gate. Heavy jobs use taskset/timeout and owned containers use --rm, a nyanjit- name and assigned CPU sets; leave host binfmt and other projects alone.

### Scoped-variable fuzzing

`compile/varfuzz` shares one byte-stream AST interpreter across corpus replay, fixed sweeps, coverage fuzzing and subprocess minimization. Each index checks leaf, native-C-call and mixed-Go/C-call programs in every applicable configuration; Go entries compare Func and Call. Count known stack/frame limits separately from successful execution. Unexpected backend errors are failures, not blanket skips.

```sh
taskset -c 5 timeout 12m go test ./compile/varfuzz -run '^$' -fuzz '^FuzzScopedVars$' -fuzztime=10m -fuzzminimizetime=2s -parallel=1 -timeout=12m
taskset -c 5 timeout 12m env NYANJIT_VAR_SWEEP=3000 go test ./compile/varfuzz -run '^TestScopedVarsSweep$' -count=1 -v -parallel=1
```

Legacy streams are compressed once under `compile/varfuzz/testdata/legacy/`; TestLegacyCorpus replays 32 programs at configurations 0-7 (256 pairs). The recorded corpus shrank from 5.9 MB to 176 KB on disk (31,828 bytes of file contents) without changing decompressed SHA-256 streams. Config 255 replays minimized workspace/narrow-sign/paired-multiply/POWER-select/managed-entry shapes across configurations. Preserve native Go fuzz files for future crashers and update decoder/corpus together when the format changes.

Use `NYANJIT_VAR_MINIMIZE` with raw decision bytes under `.agents-workspace/tmp/`, `NYANJIT_VAR_CONFIG` for the configuration, `NYANJIT_VAR_FP=1` for FP and `NYANJIT_VAR_LAUNCH` for an explicit emulator prefix. The minimizer runs bounded subprocesses and emits Builder code. Extract compressed cases under the same session scratch directory. Managed registration is permanent, so large sweeps use bounded batches and respect the registration budget.

### Scaling and adversarial coverage

Enable TestPrepareScaling/TestBuilderScaling with `NYANJIT_PERF_SCALING=1`. Scope/nested-loop sizes are 200, 400, 800 and 1,600; fit log-log slopes over medians of five interleaved runs against a 2.3x-per-doubling threshold. Run alone on a pinned core. Per-held-result retention, poison leases, race checks and alternating big/small demand cover different failures from B/op or allocation counts.

Predicate validation/copying/rebasing and Builder value collection use worklists. Budget expanded DAG nodes, not only unique nodes, at `ir.MaxPredicateNodes` (16,384); no separate depth limit is needed. Distinguish structural invalidity from numeric LimitError reporting. Mutation tests must include every opcode and ignored field, plus vector programs and hardened/unhardened modes. Confirm -run selections actually executed tests.

Frame tests combine calls, GC, tracing, stack growth and independent callee-saved checks. Pure-Go control probes isolate QEMU runtime failures before blaming generated code. gccheckmark already implies asyncpreemptoff; adding that setting is not an independent control. The archived LoongArch investigation records the emulator-build comparison without identifying a particular patch.

## Historical measurements

The tables below preserve the moved documents' numerical evidence. They are instruction counts or historical timings, not current performance promises. Dates, CPU/tier/width definitions and counting rules matter; stage-specific observations must not be combined into a new ratio. The current README and jitbench procedure own consumer comparisons.

### Scoped-variable instruction counts

Whole-function counts at baseline profiles (x86-64-v1, armv8.0-a, rv64gc, POWER8, i686); the generator used 64 samples/eight IDCT coefficients and was built for 386 to obtain native 32-bit widths. Data directives are excluded and grouped frame statements counted individually. These predate full inline-cosine 386 counts.

| Program | amd64 | arm64 | riscv64 | ppc64le | 386 |
|---|---:|---:|---:|---:|---:|
| idctvar | 453 → 361 | 376 → 289 | 506 → 398 | 616 → 514 | GP-resident FP (P1); not remeasured |
| idctargs | 428 → 375 | 333 → 312 | 446 → 420 | 587 → 537 | GP-resident FP (P1); not remeasured |
| sum | 29 → 10 | 30 → 10 | 28 → 10 | 33 → 11 | 52 → 42 |
| sumargs | 11 → 11 | 12 → 12 | 12 → 12 | 13 → 13 | 39 → 39 |
| pressure4 | 64 → 35 | 54 → 27 | 53 → 28 | 70 → 29 | 134 → 116 |
| pressure24 | 244 → 225 | 266 → 206 | 255 → 208 | 250 → 223 | 654 → 616 |

### Floating-point improvement stages

Binary64 Pow/Sin/Exp/Log/Floor counts, with labels/data excluded and expansions/spills included. The FMA row enables only that extra extension; POWER is POWER8. Budgets were lowered after each stage, never raised.

| Backend | Before | After A | After B | After C |
| --- | --- | --- | --- | --- |
| amd64 | 1214 / 444 / 172 / 205 / 45 | 1217 / 444 / 172 / 205 / 45 | 1170 / 419 / 158 / 189 / 45 | 683 / 328 / 102 / 120 / 44 |
| amd64 + FMA | 1214 / 444 / 172 / 205 / 45 | 904 / 444 / 172 / 205 / 45 | 866 / 419 / 158 / 189 / 45 | 578 / 328 / 102 / 120 / 44 |
| arm64 | 1193 / 412 / 183 / 211 / 31 | 882 / 412 / 183 / 211 / 31 | 774 / 343 / 148 / 171 / 31 | 429 / 249 / 87 / 100 / 31 |
| riscv64 | 1549 / 585 / 262 / 309 / 64 | 1220 / 585 / 262 / 309 / 64 | 1019 / 458 / 195 / 230 / 64 | 650 / 357 / 134 / 159 / 64 |
| PPC64LE | 1518 / 540 / 238 / 291 / 65 | 1192 / 540 / 238 / 291 / 65 | 1192 / 550 / 236 / 289 / 65 | 821 / 445 / 171 / 214 / 65 |

POWER8 binary32 extraction adds one instruction per affected transfer: Pow/Sin/Exp/Log/Floor 833/452/179/221/72 -> 834/453/180/222/73; binary64 is unchanged.

### 386 FP phases

| 386 fixture | P2 | P3 | P4 | GP/XMM `movd`, P2 → P3 → P4 |
| --- | ---: | ---: | ---: | ---: |
| Ten-add FP64 chain | 114 | 31 | 31 | 60 → 4 → 4 |
| FP64 recurrence with Var homes | 115 | 69 | 61 | 24 → 8 → 4 |

The recurrence uses a 64-bit counter throughout. Native-width inline-cosine IDCT is 1,687/2,070 instructions (idctvar/idctargs) at P3 and 1,556/1,943 at P4, excluding data. The constant-free chain is bounded at 31 instructions; one shared-FP64 multiply/add is bounded at thirteen instructions and one pool load.

### 386 mixed-FP acceptance by configuration

P1 distinguishes existing paired-integer scratch limits from FP correctness. P2 removes those workspace limits; P3/P4 preserve its execution/limit counts. StackPlatform128 and Go-managed limits remain separate. The strict FP-only sweep executes 3,000 programs under four configurations, 12,000 checks with no limits.

P1:

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

P2-P4:

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

### Parity math instruction counts

Binary64 at explicit baseline CPU profiles, including moves, normalization, spills and return:

| Function | amd64 before → after | arm64 before → after | riscv64 before → after |
| --- | --- | --- | --- |
| FSin | 826 → 444 | 704 → 412 | 987 → 585 |
| FFloor | 85 → 45 | 63 → 31 | 108 → 64 |
| FPow | 1295 → 1214 | 1255 → 1193 | 1637 → 1549 |

### Initial SIMD acceptance

Go 1.26.9/1.27.2 package/build/smoke gates, not individual Go tests:

| Target | Pass | Skip | Fail |
| --- | ---: | ---: | ---: |
| linux/amd64 | 85 | 0 | 0 |
| linux/arm64 | 85 | 0 | 0 |
| linux/386 | 77 | 0 | 0 |
| linux/riscv64 | 83 | 0 | 0 |

386 SHA-256 was 48,001 scalar versus 6,777 native-SSE2 instructions, excluding pool bytes; this is not a runtime comparison.

### Tier implementation measurements

ARM64 Cortex-X925. The first table preserves the original pre-implementation baseline and proposed targets (the cells marked with inequalities). The second table records implemented results, with one kernel per process. Use current README measurements for consumer comparisons.

Original design targets:

| Kernel | Today | Baseline target | sljit |
|---|---|---|---|
| chain1000 compile | 2.52 ms (630 ns/op) | ≤ 240 µs (≤ 60 ns/op) | 46 µs |
| loop compile | 41 µs | ≤ 8 µs before batched binding, ≤ 2 µs after | 0.63 µs |
| loop run | 5.6 ms (Online 3.9 ms) | ≤ 1.5 × TierOptimized | 3.4 ms |
| pressure run | 7.7 ms (Online 1.6 ms) | ≤ 1.5 × TierOptimized | 1.3 ms |

Measured implementation:

| Kernel | Compile before (optimized, end to end) | Baseline compile | Baseline run vs optimized run | sljit compile |
|---|---|---|---|---|
| chain1000 (4000 ops) | 2.6 ms (648 ns/op) | 233 µs (58 ns/op) | 1.3 µs vs 1.3 µs | 47 µs |
| loop | 39 µs | 5.2 µs | 2.6 ms vs 2.6–2.9 ms | 0.68 µs |
| pressure (16 Vars) | 138 µs | 12.9 µs | 1.29 ms vs 1.29 ms | 1.28 µs |
| collatz | 73 µs | 8.0 µs | 10.1 ms vs 9.8 ms | 0.80 µs |
| fp (Horner + FCompare) | 88 µs | 11.2 µs | 1.75 ms vs 1.77 ms | 1.0 µs |

Original Bind cost was 5-8 us/function; batched Arena.BindAll cost 0.4-1.3 us/function at batch sizes 8-64. Earlier proposed 40-60 ns/operation and 1.5x runtime were design targets, not guarantees.


## Backend parity validation

The original parity acceptance ran Go 1.26.9 and 1.27.2 across baseline/enabled profiles: 462 package/build checks (120 amd64, 120 arm64, 116 riscv64, 106 386), with ARM native and foreign targets under QEMU. Later FP/parity review recorded 1,465 checks and sixteen expected unsupported-port skips; scoped-variable fuzz acceptance recorded 1,473 passes and sixteen skips in its own matrix. These are different historical harness sets, not counts of individual Go tests.

Review fixed narrow ARM subtract-immediate carry from a 32-bit ADDS used for a negative 8/16-bit literal, with 65,790 nonzero immediates scanned. Integer arithmetic/shift/rotate and Select width agreement was tightened. POWER10 minimum-level checks reject POWER9 even when selected optional extensions match. Binary32 budgets use measured counts plus rounded-up ten-percent headroom (Trunc32 25 -> 28, Floor32 36 -> 40), not a flat allowance. Selected-op assertions must inspect actual operations rather than search for the word select.

Historical reports include `e2e/.build/results/backend-parity-*.xml`, `parity-review-{linux,amd64,386,riscv64}.xml`, `fp-perf-linux.xml`, `fp-perf-amd64-profiles.xml`, `fp-review-{linux,amd64}.xml`, `var-review/` and `var-fuzz/`. Scratch/cache files may be absent. Big-endian PPC64/ELFv1 was removed on 2026-10-15; records mentioning both byte orders are not supported-target claims.

The first green scoped-variable matrix missed edge-copy, home/FP and workspace bugs. Independent sweeps checked 34,100 generated seeds, followed by 450,627 successful byte-stream executions plus 80,934 separately reported limits. Native ARM fuzz sessions executed 46,832 and 124,175 inputs. The 386 strict FP sweep executed 12,000 cases without limits; mixed P1 had 29,461 successes/18,860 limits, and P2-P4 38,555/9,766. Preserve the distinction between a checked limit and an execution pass.

## Target validation records

The archived JSON below preserves every field of the six former completion records, including evidence paths, CPU/version matrices, phase results, roadmap items and emulator controls. It is a historical data archive, not a machine-read completion gate. References inside snapshots intentionally retain their original paths; current documentation locations are listed under [document provenance](#document-provenance).

### amd64 validation record

<details>
<summary>Historical amd64-completion.json snapshot</summary>

```json
{
  "scope": "handoff-backend-parity.md Phases 0\u20135; not the broader PPC roadmap",
  "architecture": "amd64",
  "status": "complete",
  "cpu_profiles": true,
  "features": [
    "CL shifts/rotates with fixed-register scheduling",
    "MUL, ADC/SBB",
    "BMI2 SHLX/SHRX/SARX/MULX/RORX",
    "BSR/BSF and LZCNT/TZCNT",
    "independent S/Z/CF facts; INC/DEC and BMI2 flag preservation",
    "existing LOOP fusion"
  ],
  "evidence": [
    "compile/backend_parity_test.go",
    "compile/backend_budget_test.go",
    "compile/shift_by_linux_test.go",
    "compile/mul_high_linux_test.go",
    "compile/word_carry_linux_test.go",
    "compile/count_zeros_linux_test.go",
    "compile/profile_instructions_test.go",
    "e2e/amd64.star",
    "e2e/.build/results/backend-parity-amd64.xml"
  ],
  "acceptance": {
    "go_versions": [
      "1.26.9",
      "1.27.2"
    ],
    "checks": 120,
    "failures": 0,
    "runner": "QEMU user-mode baseline/max"
  },
  "limitations": [
    "FP residency and large compare immediates are outside the parity handoff"
  ]
}
```

</details>

### arm64 validation record

<details>
<summary>Historical arm64-completion.json snapshot</summary>

```json
{
  "scope": "handoff-backend-parity.md Phases 0\u20135; not the broader PPC roadmap",
  "architecture": "arm64",
  "status": "complete",
  "cpu_profiles": true,
  "features": [
    "LSLV/LSRV/ASRV/RORV",
    "UMULH, ADCS/SBCS",
    "CLZ/RBIT",
    "S/Z/C facts and no-borrow polarity",
    "flag-liveness-safe SUB/CBNZ loops"
  ],
  "evidence": [
    "compile/backend_parity_test.go",
    "compile/backend_budget_test.go",
    "compile/shift_by_linux_test.go",
    "compile/mul_high_linux_test.go",
    "compile/word_carry_linux_test.go",
    "compile/count_zeros_linux_test.go",
    "compile/profile_instructions_test.go",
    "e2e/arm64.star",
    "e2e/.build/results/backend-parity-arm64.xml"
  ],
  "acceptance": {
    "go_versions": [
      "1.26.9",
      "1.27.2"
    ],
    "checks": 120,
    "failures": 0,
    "runner": "native ARM64 (explicit compiler profiles)"
  },
  "limitations": [
    "FP residency and large compare immediates are outside the parity handoff"
  ]
}
```

</details>

### riscv64 validation record

<details>
<summary>Historical riscv64-completion.json snapshot</summary>

```json
{
  "scope": "handoff-backend-parity.md Phases 0\u20135; not the broader PPC roadmap",
  "architecture": "riscv64",
  "status": "complete",
  "cpu_profiles": true,
  "features": [
    "baseline variable shifts and rotates",
    "MULHU and flag-free carry chains",
    "Zbb ROL/ROR/CLZ/CTZ/ANDN",
    "Zba SH1ADD/SH2ADD/SH3ADD",
    "direct zero-branch countdowns"
  ],
  "evidence": [
    "compile/backend_parity_test.go",
    "compile/backend_budget_test.go",
    "compile/shift_by_linux_test.go",
    "compile/mul_high_linux_test.go",
    "compile/word_carry_linux_test.go",
    "compile/count_zeros_linux_test.go",
    "compile/profile_instructions_test.go",
    "e2e/riscv64.star",
    "e2e/.build/results/backend-parity-riscv64.xml"
  ],
  "acceptance": {
    "go_versions": [
      "1.26.9",
      "1.27.2"
    ],
    "checks": 116,
    "failures": 0,
    "runner": "QEMU user-mode baseline/max"
  },
  "limitations": [
    "FP residency and large compare immediates are outside the parity handoff",
    "Native full RVA profile validation remains conservative when Linux does not expose every mandate"
  ]
}
```

</details>

### 386 validation record

<details>
<summary>Historical 386-completion.json snapshot</summary>

```json
{
  "scope": "handoff-backend-parity.md Phases 0\u20135; not the broader PPC roadmap",
  "architecture": "386",
  "status": "complete",
  "cpu_profiles": true,
  "features": [
    "CL scalar shifts, SHLD/SHRD paired shifts and rotates",
    "native 32-bit MUL/ADC/SBB",
    "legacy visitors canonicalized to shared carry/high-product operations",
    "portable bit-count fallbacks"
  ],
  "evidence": [
    "compile/backend_parity_test.go",
    "compile/backend_budget_test.go",
    "compile/shift_by_linux_test.go",
    "compile/mul_high_linux_test.go",
    "compile/word_carry_linux_test.go",
    "compile/count_zeros_linux_test.go",
    "compile/profile_instructions_test.go",
    "e2e/386.star",
    "e2e/.build/results/backend-parity-386.xml"
  ],
  "acceptance": {
    "go_versions": [
      "1.26.9",
      "1.27.2"
    ],
    "checks": 106,
    "failures": 0,
    "runner": "QEMU user-mode baseline/max"
  },
  "limitations": [
    "FP residency and large compare immediates are outside the parity handoff"
  ],
  "floating_point": {
    "scope": "handoff-386-fp.md P0-P4; native integer SIMD tracked separately below",
    "status": "complete",
    "completed_phases": [
      "P0 SSE2 baseline and fail-closed native verification",
      "P1 GP-resident SSE2 scalar arithmetic",
      "P2 inline math, accuracy and dctlogo; workspace paired integer operations",
      "P3 XMM residency, typed FP variable homes and FP label parameters",
      "P4 shared absolute-address FP literal pool and per-mapping relocation"
    ],
    "pending_phases": [],
    "abi": "IEEE bit-pattern integer slots: one word for FP32, two low-first words for FP64; cdecl EAX:EDX or Go ABI0 stack result",
    "operations": [
      "FAdd",
      "FSub",
      "FMul",
      "FDiv",
      "FSqrt",
      "FConvert",
      "integer sign/absolute masks",
      "FProductError via Dekker",
      "all 28 inline math entry points, both FP widths"
    ],
    "fma": false,
    "evidence": [
      "target/i386/float_test.go",
      "machine/fp_writes_i386_test.go",
      "compile/float_i386_test.go",
      "compile/float_linux_386_test.go",
      "compile/exact_product_linux_test.go",
      "compile/scoped_vars_linux_test.go",
      "compile/varfuzz/legacy_corpus_test.go",
      "compile/varfuzz/scoped_fuzz_test.go",
      "abi/entry_386_test.go",
      "compile/sse2_linux_386_test.go",
      "cpuprofile/x86_baseline_test.go",
      "compile/varfuzz/float_386_test.go",
      "target/i386/paired_test.go",
      "compile/word_memory_linux_386_test.go",
      "compile/math_cross_test.go",
      "compile/math_linux_test.go",
      "compile/math_accuracy_linux_test.go",
      "examples/dctlogo/main_test.go",
      "target/i386/float_memory_test.go",
      "compile/fp_i386_residency_test.go",
      "compile/fp_residency_linux_386_test.go",
      "compile/fp_residency_linux_test.go",
      "compile/fp_hint_review_linux_test.go",
      "machine/vector_test.go",
      "target/i386/literal_test.go",
      "machine/literal_386_relocation_test.go",
      "execution/relocation_linux_test.go",
      "execution/literal_linux_386_test.go",
      "compile/literal_linux_386_test.go",
      "compile/literals_test.go"
    ],
    "limitations": [
      "Managed frames are limited to 240 bytes; larger cases remain explicit limits",
      "386 FMA and native packed SIMD remain outside P0-P4"
    ],
    "strict_fp_sweep": {
      "programs_per_config": 3000,
      "configs": [
        "C/StackPlatform",
        "C/StackNoUse",
        "Go/StackPlatform",
        "Go/StackNoUse"
      ],
      "executions": 12000,
      "limits": 0,
      "failures": 0,
      "go_version": "1.27.2",
      "runner": "QEMU qemu32,-sse3",
      "phase": "P4"
    },
    "e2e": {
      "go_versions": [
        "1.26.9",
        "1.27.2"
      ],
      "checks_per_version": 64,
      "failures": 0,
      "cpu_profiles": [
        "qemu32,-sse3",
        "max"
      ],
      "negative_probe": "GO386=softfloat driver, qemu32,-sse2 rejects with ErrCPUUnsupported",
      "phase": "P4",
      "junit": "e2e/.build/results/386-fp-p4/e2e.xml"
    },
    "validation_architectures": [
      "arm64",
      "amd64",
      "386",
      "riscv64",
      "ppc64le"
    ],
    "mixed_fp_sweep": {
      "phase": "P4",
      "accepted_cases": 3000,
      "successful_checks": 38555,
      "known_stack_frame_limits": 9766,
      "fp_allocation_pressure_failures": 0,
      "unexpected_errors": 0,
      "mismatches": 0,
      "per_configuration": {
        "Compile/C/NoUse": {
          "executed": 4547,
          "known_limits": 0
        },
        "Compile/C/Platform": {
          "executed": 4547,
          "known_limits": 0
        },
        "Compile/C/Platform/Compact": {
          "executed": 4547,
          "known_limits": 0
        },
        "Compile/C/Platform128": {
          "executed": 1005,
          "known_limits": 3542
        },
        "Compile/Go/NoUse": {
          "executed": 4547,
          "known_limits": 0
        },
        "Compile/Go/Platform+Func": {
          "executed": 4547,
          "known_limits": 0
        },
        "Foreign/C/Platform": {
          "executed": 3039,
          "known_limits": 0
        },
        "Go/C/Platform": {
          "executed": 5888,
          "known_limits": 3112
        },
        "Go/Go/Platform+Func": {
          "executed": 5888,
          "known_limits": 3112
        }
      },
      "matches_P3_counts": true
    },
    "evidence_directory": "e2e/.build/results/386-fp-p4",
    "p1_mixed_fp_sweep": {
      "accepted_cases": 3000,
      "variants_per_case": [
        "leaf",
        "native C call",
        "mixed Go/C call"
      ],
      "successful_checks": 29461,
      "known_stack_frame_limits": 18860,
      "fp_allocation_pressure_failures": 0,
      "unexpected_errors": 0,
      "mismatches": 0,
      "per_configuration": {
        "Compile/C/NoUse": {
          "executed": 0,
          "known_limits": 4547
        },
        "Compile/C/Platform": {
          "executed": 4547,
          "known_limits": 0
        },
        "Compile/C/Platform/Compact": {
          "executed": 4547,
          "known_limits": 0
        },
        "Compile/C/Platform128": {
          "executed": 1005,
          "known_limits": 3542
        },
        "Compile/Go/NoUse": {
          "executed": 0,
          "known_limits": 4547
        },
        "Compile/Go/Platform+Func": {
          "executed": 4547,
          "known_limits": 0
        },
        "Foreign/C/Platform": {
          "executed": 3039,
          "known_limits": 0
        },
        "Go/C/Platform": {
          "executed": 5888,
          "known_limits": 3112
        },
        "Go/Go/Platform+Func": {
          "executed": 5888,
          "known_limits": 3112
        }
      },
      "discarded_candidates": {
        "fp range": 4
      }
    },
    "baseline_math_determinism": {
      "function_width_pairs": 56,
      "samples_per_pair": 145,
      "reference_target": "amd64 (SSE2 baseline, no FMA)",
      "result": "matching AMD64 and P3 SHA256 digests",
      "nan_payloads": "canonicalized",
      "signed_zero": "preserved",
      "phase": "P4"
    },
    "residency": {
      "registers": "XMM3-7",
      "temporaries": "XMM0-2",
      "GP_ABI_words": "FP32=1, FP64=2",
      "spill_slots_386": "FP32=1, FP64=2 (4 bytes per slot)",
      "call_preservation": "All XMM registers are caller-saved for cdecl and Go ABI0",
      "literals": "Deduplicated 16-byte-aligned read-only code-local pool; absolute disp32 loads patched before RX and runtime registration"
    },
    "instruction_counts": {
      "counting": "Concrete statements, including RET and each grouped frame statement; literal data excluded",
      "FP64_ten_add_chain": {
        "P2": 114,
        "P3": 31,
        "movd_P2": 60,
        "movd_P3": 4,
        "P4": 31,
        "movd_P4": 4
      },
      "FP64_var_recurrence_64bit_counter": {
        "P2": 115,
        "P3": 69,
        "movd_P2": 24,
        "movd_P3": 8,
        "P4": 61,
        "movd_P4": 4
      },
      "native_width_IDCT_64samples_8coefficients": {
        "idctvar": 1556,
        "idctargs": 1943,
        "P3": {
          "idctvar": 1687,
          "idctargs": 2070
        }
      },
      "FP64_shared_constant_multiply_add": {
        "P4": 13,
        "pool_loads": 1
      }
    },
    "p2_mixed_fp_sweep": {
      "phase": "P2",
      "accepted_cases": 3000,
      "successful_checks": 38555,
      "known_stack_frame_limits": 9766,
      "fp_allocation_pressure_failures": 0,
      "unexpected_errors": 0,
      "mismatches": 0,
      "per_configuration": {
        "Compile/C/NoUse": {
          "executed": 4547,
          "known_limits": 0
        },
        "Compile/C/Platform": {
          "executed": 4547,
          "known_limits": 0
        },
        "Compile/C/Platform/Compact": {
          "executed": 4547,
          "known_limits": 0
        },
        "Compile/C/Platform128": {
          "executed": 1005,
          "known_limits": 3542
        },
        "Compile/Go/NoUse": {
          "executed": 4547,
          "known_limits": 0
        },
        "Compile/Go/Platform+Func": {
          "executed": 4547,
          "known_limits": 0
        },
        "Foreign/C/Platform": {
          "executed": 3039,
          "known_limits": 0
        },
        "Go/C/Platform": {
          "executed": 5888,
          "known_limits": 3112
        },
        "Go/Go/Platform+Func": {
          "executed": 5888,
          "known_limits": 3112
        }
      }
    },
    "raw_emission": {
      "missing_base_error": "compile.ErrCodeBaseRequired",
      "automatic_mapping": "Prepare().Bind",
      "known_base": "EmissionTemplate.LinkAt / machine.LinkRelocationsAt",
      "low_level_mapping": "execution.MapWithRelocations"
    }
  },
  "native_simd": {
    "scope": "handoff-386-simd.md P5",
    "status": "complete",
    "baseline": "i686 + SSE2",
    "optional_extensions": [
      "pclmulqdq"
    ],
    "register_homes": "XMM3-XMM7 shared with scalar FP; XMM0-XMM2 reserved expansion temporaries",
    "spills": "four 32-bit words (16 bytes), MOVDQU; EBP workspace or ESP + outgoing call area",
    "gp_edges": "64-bit lanes use low/high GPR pairs via MOVD/PUNPCKLDQ and MOVD/PSHUFD",
    "lowering": "128-bit integer native/expanded; wider vectors split; labels/Vars and unsupported operations scalarize; empty non-nil Features forces scalar lanes",
    "operations": [
      "load/store",
      "zero and pooled constants",
      "splat/extract/halfword insert/insert-zero",
      "add/subtract/and/or/xor",
      "32-bit lane immediate shifts/rotates",
      "16-bit lane multiply",
      "byte equality and mask bits",
      "PCLMULQDQ with all four selectors"
    ],
    "evidence": [
      "target/i386/vector.go",
      "target/i386/vector_test.go",
      "target/i386/vector_linux_386_test.go",
      "compile/vector_report_test.go",
      "compile/vector_linux_386_test.go",
      "compile/vector_shift_linux_test.go",
      "compile/vector_clmul_linux_test.go",
      "abi/cpu_profile_test.go",
      "cpuprofile/x86_test.go",
      "examples/sha256/main_test.go",
      "e2e/.build/results/386-p5/summary.md"
    ],
    "acceptance": {
      "go_versions": [
        "1.26.9",
        "1.27.2"
      ],
      "runner": "QEMU 10.2.3 qemu32,-sse3 and max, private container binfmt namespace",
      "profile_checks": 134,
      "missing_sse2_probe": "included in profile checks; GO386=softfloat driver",
      "final_assembler_checks": 4,
      "final_pressure_and_call_checks": 4,
      "sha256_digest_modes": [
        "SSE2",
        "forced scalar"
      ],
      "sha256_instructions": {
        "scalar": 48001,
        "SSE2": 6777,
        "pool_data_counted": false
      },
      "varfuzz": "No vector grammar; existing integer/FP scalar fuzz/legacy regressions run in both profiles and versions. Dedicated native/scalar SIMD differentials cover vector lowering.",
      "failures": 0,
      "smoke_checks": 4,
      "native_go_test": "pass",
      "cross_target_vet": "pass",
      "harness_unit_tests": "pass",
      "diff_check": "pass"
    },
    "limitations": [
      "No FMA, AVX/VEX or native vector width above 128 bits",
      "Managed Go calls retain the existing 240-byte frame limit"
    ]
  }
}
```

</details>

### POWER validation record

<details>
<summary>Historical ppc-completion.json snapshot</summary>

```json
{
  "objective": "all the PPC work is done.",
  "scope": "All PPC implementation, optimization, integration and validation items discussed in the thread; no item is waived by this checklist.",
  "big_endian": "Big-endian linux/ppc64 (and ELFv1) support was removed on 2026-10-15; it is now an unsupported, cross-build-only target. Validation records named ppc64-*.xml and byte-order variants below are historical.",
  "hardware": {
    "available": false,
    "evidence": "User answered that no POWER machine is available; hardware validation remains unverified."
  },
  "requirements": [
    {
      "id": "elfv2-userspace",
      "requirement": "Separate LE sysroot; verified ELFv2 loader; PIE and real-C/cgo/shared-library tests",
      "status": "complete",
      "evidence": [
        "Separate Debian LE sysroot in container image (the former musl BE loader was removed with big-endian ppc64)",
        "e2e/ppc64.star: ppc64le POWER8/9/10, Go 1.26.9 and 1.27.2; PIE and actual shared-library/cgo callbacks pass.",
        "e2e/.build/results contains the four JUnit reports; total 803 passes, six expected skips, zero failures.",
        "Native go test ./... and harness unit tests pass; Linux/amd64, /386 and /riscv64 full QEMU suites pass after upstream purego update."
      ]
    },
    {
      "id": "hardware-publication",
      "requirement": "Validate executable-memory publication and cache synchronization on real POWER hardware",
      "status": "needs-hardware",
      "evidence": []
    },
    {
      "id": "managed-stacks",
      "requirement": "Generated stack-growth checks, frames beyond 640 bytes, pointer maps and runtime compatibility",
      "status": "pending",
      "evidence": []
    },
    {
      "id": "abi-shapes",
      "requirement": "Native FP/aggregate/variadic/stack arguments and wider call results; twelve Go entry arguments",
      "status": "pending",
      "evidence": []
    },
    {
      "id": "foreign-callbacks",
      "requirement": "Native FP/aggregate callback signatures and callbacks on foreign-created threads",
      "status": "in-progress",
      "evidence": [
        "C-created threads work when real cgo supplies TLS and runtime attachment; the nil-g rejection was removed from the real-cgo callback path.",
        "e2e/ppc64cgo tests concurrent foreign threads, repeated callbacks, registered JIT reentry, Go stack growth, GC and recovered callback panics in exe and PIE."
      ]
    },
    {
      "id": "ctr-loops",
      "requirement": "Select CTR countdown loops and report CTR constraints/clobbers",
      "status": "complete",
      "evidence": [
        "PreferCompactLoops selects typed CTR countdown loops after allocation and before range relaxation. Each external entry initializes CTR; the backedge bypasses initialization; exit restores the GPR count to zero.",
        "Safety analysis checks every counter use, calls/CTR consumers, overlapping loops, side entries, early exits and per-flag liveness through partial writers. Scheduled work between DEC and compare is supported.",
        "PreflightRegisterScopes exposes optional CTR/BDNZ opportunities; emitted special-resource reports include actual CTR clobbers without inventing a GPR ordinal.",
        "BDNZ relaxes from BDZ/B while preserving flags; an executing 36 KB loop exercises the long form.",
        "Native tests cover zero entry, observable counter restoration and body-count fallback; unit tests cover nested contention and CFG/flag barriers. All twelve CPU/Go/byte-order variants pass."
      ]
    },
    {
      "id": "arithmetic-flags",
      "requirement": "Reuse CR/XER facts from arithmetic in predicates",
      "status": "complete",
      "evidence": [
        "machine.Stream accepts exact per-flag effects from the physical emitter and preserves independent facts; snapshots detach aliases.",
        "PPC CR0 sign/zero and XER carry/borrow are reused at matching value widths; POWER9/10 can use CA32. Composed predicates transfer XER through CR7 without clobbering CR0; POWER8 fallback and MCRXRX have independent GNU assembler encoding checks.",
        "Narrowing/sign-extension aliases are width-proven. Calls and arithmetic right shifts invalidate only applicable facts; unrelated comparisons and logical operations preserve XER. Immediate and truncating-extension edge cases are covered.",
        "Countdown predicates emit addic. and bne without a redundant compare. Branch compaction preserves independently reachable jumps.",
        "e2e/.build/results/ppc64-ctr-loops.xml and ppc64le-ctr-loops.xml contain passing flag tests in every POWER8/9/10 and Go 1.26.9/1.27.2 variant, both byte orders; native, amd64, 386 and riscv64 regressions pass."
      ]
    },
    {
      "id": "variable-shifts",
      "requirement": "Direct register-count shifts/rotations with portable width/count semantics",
      "status": "complete",
      "evidence": [
        "Scalar value-count shifts remain semantic IR until selection. PPC emits SLD/SRD/SRAD or SLW/SRW/SRAW with a full unsigned count clamp; rotations use RLDCL/RLWNM, NEG for right rotation and repeated narrow patterns for 8/16-bit values.",
        "Register reuse supports either dead input; arithmetic right shifts report CR0/XER effects and rotations preserve flag facts. Byte/text sizes, PCs, reserved temporaries and independent GNU POWER8 encoding goldens are tested.",
        "Execution tests cover every value/count width, oversized and high-bit counts, live source/count operands, forced spills, scope exports and label arguments. Targets without a native register-count operation receive a verified portable expansion, including narrow rotations and paired counts on 386.",
        "e2e/.build/results/ppc64-variable-shifts.xml and ppc64le-variable-shifts.xml: 500 passes, zero failures; each new execution test ran in all twelve POWER/Go/byte-order variants. Native go test ./..., go vet ./..., harness tests and full Linux/amd64,/386,/riscv64 QEMU suites pass."
      ]
    },
    {
      "id": "wide-arithmetic",
      "requirement": "Native carry chains and high-half multiply for 128-bit lowering",
      "status": "complete",
      "evidence": [
        "Builder wide add/sub/multiply retain scalar carry/borrow and high-product IR. Public IR includes OpCarryAdd/OpBorrowSub, OpAddWithCarry/OpSubWithBorrow and OpMulHigh; portable selection preserves width semantics on every existing target, including register pairs on 386.",
        "Single-use carry extraction fuses into selected high-word operations. Allocation reuses a low-word XER fact only when operation, width and operands match; otherwise emission recomputes it. Wide add/sub select ADDC/ADDE and SUBFC/SUBFE chains; observed carry values are materialized. Incoming scalar carry operands use their low bit.",
        "PPC MULHDU/MULHWU supply native unsigned high products. Wide multiplication uses MULHDU for its low-word high half and can contract cross-products to MADDLD on POWER9/10. The new instruction encodings were independently checked with GNU POWER8 assembler.",
        "Carry chains report CR0/XER effects without inventing binary carry predicates. Width-proven identity/widening aliases preserve result flags; CTR safety analysis recognizes carry consumers and all five fused operands.",
        "Execution tests cover carry/borrow boundaries, 200 deterministic random wide products and sums, operand aliasing, scope/label transfers, shared carries, overwritten XER, high-result predicate reuse, all scalar widths and full unsigned carry operands.",
        "e2e/.build/results/ppc64-wide-arithmetic.xml and ppc64le-wide-arithmetic.xml: 500 passes, zero failures. All seven new execution tests pass in every POWER8/9/10, Go 1.26.9/1.27.2 and byte-order variant; native go test ./..., go vet ./..., harness tests and full Linux/amd64,/386,/riscv64 QEMU regression suites pass."
      ]
    },
    {
      "id": "cpu-levels",
      "requirement": "Explicit POWER8/9/10 capabilities and POWER9 integer multiply-add",
      "status": "complete",
      "evidence": [
        "abi.PPC64CPU and CompileOptions.PPC64CPU select POWER8/9/10; default is POWER8. Emission reports preserve the resolved level in straight-line and control-flow functions.",
        "Native CPU gating uses Linux HWCAP2, verified under POWER8/9/10 QEMU for both byte orders and Go 1.26.9/1.27.2; text and scoped preflight support explicit cross-target levels.",
        "POWER9/10 select MADDLD for single-use arbitrary and scaled integer products; shared products stay materialized. Narrow results retain their modular width; destination/operand aliasing is covered.",
        "MADDLD encoding 0x106429b3 independently checked against Go tool asm; byte/text sizes, implicit writes and preservation of CR/XER/FPSCR covered.",
        "e2e/.build/results/ppc64-cpu-levels.xml and ppc64le-cpu-levels.xml: 440 pass, zero failures; native go test ./..., go vet ./..., harness tests and Linux/amd64,/386,/riscv64 QEMU regression suites pass."
      ]
    },
    {
      "id": "fp-residency",
      "requirement": "Allocate floating intermediates to FPRs across operations",
      "status": "complete",
      "evidence": [
        "Scalar FP register residency in F3-F13, eight-byte spill homes, bit-preserving GPR boundaries, call spills, scopes/labels, native FNEG/FABS and shared PIC literal pools.",
        "docs/fp-performance.md records all three optimization stages and before/after counts; FPow POWER8 drops from 1518 to 821 instructions."
      ]
    },
    {
      "id": "memory-and-allocation",
      "requirement": "Native narrow loads/stores, better addressing, C callee-saved allocation",
      "status": "pending",
      "evidence": [
        "Address-fold guards preserve pointer-width truncation on wider targets; scaled arithmetic now truncates every expansion path. Native narrow load/store opcodes and C callee-saved allocation remain pending."
      ]
    },
    {
      "id": "far-branches",
      "requirement": "Flag-preserving branches beyond direct branch range",
      "status": "pending",
      "evidence": []
    },
    {
      "id": "openbsd-port",
      "requirement": "OpenBSD/ppc64 implementation and VM acceptance",
      "status": "pending",
      "evidence": []
    },
    {
      "id": "aix-port",
      "requirement": "AIX/ppc64 implementation and execution acceptance",
      "status": "pending",
      "evidence": []
    }
  ],
  "latest_validation": {
    "date": "2026-10-13",
    "checks": {
      "ppc64-elfv2-integration.xml": {
        "pass": 123,
        "skip": 6,
        "fail": 0
      },
      "ppc64-standard-after-elfv2.xml": {
        "pass": 223,
        "skip": 0,
        "fail": 0
      },
      "ppc64le-elfv2-integration.xml": {
        "pass": 240,
        "skip": 0,
        "fail": 0
      },
      "ppc64le-standard-after-elfv2.xml": {
        "pass": 217,
        "skip": 0,
        "fail": 0
      }
    },
    "image_bytes": 16088538354
  },
  "cpu_validation": {
    "date": "2026-10-13",
    "checks": {
      "ppc64-cpu-levels.xml": {
        "pass": 223,
        "skip": 0,
        "fail": 0
      },
      "ppc64le-cpu-levels.xml": {
        "pass": 217,
        "skip": 0,
        "fail": 0
      }
    }
  },
  "next_work": "Implement native narrow memory operations and C callee-saved allocation, then continue every remaining requirement; hardware cache validation still has no POWER runner.",
  "loop_and_flag_validation": {
    "date": "2026-10-13",
    "checks": {
      "ppc64-ctr-loops.xml": {
        "pass": 253,
        "skip": 0,
        "fail": 0
      },
      "ppc64le-ctr-loops.xml": {
        "pass": 247,
        "skip": 0,
        "fail": 0
      }
    }
  },
  "variable_shift_validation": {
    "date": "2026-10-13",
    "checks": {
      "ppc64-variable-shifts.xml": {
        "pass": 253,
        "skip": 0,
        "fail": 0
      },
      "ppc64le-variable-shifts.xml": {
        "pass": 247,
        "skip": 0,
        "fail": 0
      }
    }
  },
  "wide_arithmetic_validation": {
    "date": "2026-10-13",
    "checks": {
      "ppc64-wide-arithmetic.xml": {
        "pass": 253,
        "skip": 0,
        "fail": 0
      },
      "ppc64le-wide-arithmetic.xml": {
        "pass": 247,
        "skip": 0,
        "fail": 0
      }
    }
  }
}
```

</details>

### LoongArch validation record

<details>
<summary>Historical loong64-completion.json snapshot</summary>

```json
{
  "architecture": "loong64",
  "platform": "linux",
  "scope": "handoff-loong64.md L0 through L4",
  "status": "L0/L1/L2/L3/L4 complete; LASX lowering out of scope",
  "phases": {
    "L0": "complete",
    "L1": "complete",
    "L2": "complete",
    "L3": "complete: scalar FP32/FP64, baseline FMA, residency, Var/label FP homes, literals, inline math",
    "L4": "complete: 128-bit LSX integer lowering, shared FP/vector allocation, calls, spills, feature gates and reports"
  },
  "features": [
    "la64v1.0 baseline with process HWCAP LSX/LASX discovery",
    "shared byte/text plans and exact visitor sizing",
    "up to four instruction constants and validated symbol relocations",
    "16/21/26-bit scaled branches with long/far relaxation",
    "leaf C and Go entries, 128-bit C results",
    "stack and caller workspace spills, scoped mutable variables",
    "full-count shifts/rotates, carry/borrow, high multiply, CLZ/CTZ",
    "ALSL.D scaled arithmetic, BSTRPICK.D, branch-free selection",
    "indexed memory, explicit byte order, direct zero-branch countdowns",
    "C calls and runtime callbacks via purego plus a project-owned 16-byte-aligned C trampoline",
    "Go ABIInternal calls with sixteen integer register arguments and caller-owned homes",
    "pointer-free Go 1.26/1.27 runtime registration and PC/SP unwind metadata",
    "C-entry GPR/FPR preservation across managed Go calls",
    "LP64D sign extension for typed 32-bit outbound C arguments",
    "foreign pthread callback reentry into registered JIT code with GC and stack growth",
    "baseline FP32/FP64 arithmetic, square root and width conversion",
    "hardware FMA with direct binary32 rounding and deferred contraction",
    "f8-f23 allocation and scoped variable/label homes; f0-f2 expansion temporaries",
    "shared PCADDU12I + FLD literal pools and exact GP/FP clobber reporting",
    "all 28 inline math functions at both widths with measured budgets",
    "FCSR0 effects separated from integer flags and non-signaling bank copies",
    "128-bit LSX integer arithmetic, including VMUL.D, shifts/rotates and byte equality masks",
    "LSX unaligned/indexed vector memory and PCADDU12I + VLD literal constants",
    "shared sixteen-home f8-f23 FP/vector bank and full 128-bit call spills",
    "CPU-profile/HWCAP LSX gating, LASX implies LSX, forced scalar feature mode"
  ],
  "abi_sources": [
    "Go 1.26.9 and 1.27.2 internal/abi/abi_loong64.go",
    "Go cmd/internal/obj/loong64/{a.out,cpu}.go and runtime/asm_loong64.s",
    "https://github.com/loongson/la-abi-specs/blob/release/lapcs.adoc"
  ],
  "acceptance": {
    "go_versions": [
      "1.26.9",
      "1.27.2"
    ],
    "runner": "QEMU 10.2.3 la464,lsx=off,lasx=off and max",
    "hardware_cache_publication": "not validated",
    "original_fp_call_legacy_paths": "all 32 original integer/FP call programs x 8 config indices, on both CPUs",
    "native_go_test": "pass",
    "cross_target_vet": "pass",
    "harness_unit_tests": "pass",
    "diff_check": "pass",
    "profile_checks": 130,
    "smoke_checks": 4,
    "failures": 0,
    "integer_sweep": {
      "programs_per_profile": 3000,
      "profiles": 2,
      "configurations_per_program": 4,
      "executions": 24000,
      "mismatches": 0,
      "waived_compile_limits": 0
    },
    "legacy_integer_replays": 128,
    "l1_evidence": "e2e/.build/results/loong64-l1/summary.md",
    "evidence": "e2e/.build/results/loong64-l3/summary.md",
    "mixed_integer_call_sweep": {
      "programs_per_profile": 500,
      "profiles": 2,
      "successful_executions_per_profile": 2364,
      "expected_limits_per_profile": 197,
      "limit_reasons": [
        "128-byte test stack budget",
        "512-byte managed frame limit"
      ],
      "unexpected_compile_errors": 0,
      "mismatches": 0
    },
    "original_integer_call_legacy": {
      "programs": 16,
      "profiles": 2,
      "successful_executions_per_profile": 64,
      "expected_limits_per_profile": 10
    },
    "sysroot": {
      "publisher": "https://github.com/loong64/cross-tools/releases/tag/20260912",
      "libc": "musl 1.2.5",
      "arm64_archive_sha256": "3f4c46cc5b7247f5498ce3b3d48e4f923cade5fe13859839d806504cfc82a8ce",
      "amd64_archive_sha256": "425cba4b4edb9555a7268a6bb78dcbb9dbde83905031b0d144ce1adc76be29aa",
      "both_target_libcs_baseline_tested": true,
      "image_built_for": "linux/arm64",
      "rejected_candidate": "Loongson glibc 2.42 loader uses LSX unconditionally"
    },
    "l2_evidence": "e2e/.build/results/loong64-l2/summary.md",
    "l3_profile_checks": 130,
    "l3_smoke_checks": 4,
    "l3_assembler_final_checks": 4,
    "l3_sweep_go_version": "1.27.2",
    "l3_sweep": {
      "baseline": {
        "integer_programs": 3000,
        "fp_programs": 3000,
        "successful_executions": 89281,
        "expected_limits": 7366,
        "results": {
          "limit:Compile/C/Platform128": 4318,
          "limit:Go/C/Platform": 2891,
          "limit:Go/Go/Platform+Func": 157,
          "pass:Compile/C/NoUse": 9095,
          "pass:Compile/C/Platform": 9095,
          "pass:Compile/C/Platform/Compact": 9095,
          "pass:Compile/C/Platform128": 4777,
          "pass:Compile/Go/NoUse": 9095,
          "pass:Compile/Go/Platform+Func": 9095,
          "pass:Foreign/C/Platform": 6077,
          "pass:Go/C/Platform": 15109,
          "pass:Go/Go/Platform+Func": 17843
        },
        "legacy_pairs": 256,
        "legacy_expected_limits": 36,
        "legacy_passes": 220,
        "mismatches": 0,
        "unexpected_compile_errors": 0
      },
      "max": {
        "integer_programs": 3000,
        "fp_programs": 3000,
        "successful_executions": 89281,
        "expected_limits": 7366,
        "results": {
          "limit:Compile/C/Platform128": 4318,
          "limit:Go/C/Platform": 2891,
          "limit:Go/Go/Platform+Func": 157,
          "pass:Compile/C/NoUse": 9095,
          "pass:Compile/C/Platform": 9095,
          "pass:Compile/C/Platform/Compact": 9095,
          "pass:Compile/C/Platform128": 4777,
          "pass:Compile/Go/NoUse": 9095,
          "pass:Compile/Go/Platform+Func": 9095,
          "pass:Foreign/C/Platform": 6077,
          "pass:Go/C/Platform": 15109,
          "pass:Go/Go/Platform+Func": 17843
        },
        "legacy_pairs": 256,
        "legacy_expected_limits": 36,
        "legacy_passes": 220,
        "mismatches": 0,
        "unexpected_compile_errors": 0
      }
    },
    "l3_math_counts": "e2e/.build/results/loong64-l3/math-counts.md",
    "l3_expected_limit_reasons": [
      "128-byte test stack budget",
      "512-byte managed frame limit"
    ]
  },
  "review_round_1": {
    "items": "1-6 implemented",
    "fixes": [
      "Realign LoongArch and RISC-V C callees after the runtime foreign-stack transition, preserving exact incoming SP and both integer results",
      "Include R21 in the LoongArch Go-call clobber report",
      "Report preservation according to C versus Go entry ABI on LoongArch and RISC-V",
      "Sign-extend 32-bit RISC-V C arguments consistently with LP64 psABI, retaining LoongArch behavior",
      "Require StackPlatform for outbound calls/syscalls in labeled and straight-line code on every backend",
      "Reject out-of-range LoongArch 12-bit immediates instead of silently truncating",
      "Preserve test flags when invoking the container's patched QEMU explicitly"
    ],
    "independent_C_fixtures": "SP mod 16 and eighth-argument leaf; volatile aligned __int128 local compiled by GCC",
    "regression_cpus": [
      "la464,lsx=off,lasx=off",
      "max",
      "rv64"
    ],
    "go_versions": [
      "1.26.9",
      "1.27.2"
    ],
    "final_acceptance": {
      "loong64": 130,
      "riscv64": 140,
      "smoke": 8,
      "failures": 0,
      "native_tests": "pass",
      "harness_tests": "pass",
      "cross_target_vet": "pass",
      "diff_check": "pass"
    },
    "initial_acceptance_failure": "LoongArch max / Go 1.27.2 varfuzz: runtime.Pinner leak panic. Three isolated full package repeats and a full profile-matrix rerun passed. Retained rather than omitted from evidence.",
    "additional_gc_stress": "GOGC=10 GODEBUG=gccheckmark=1 on LoongArch max / Go 1.27.2 found an unmarked object; cause unresolved. See evidence for controlled comparison against the previous gateway.",
    "evidence": "e2e/.build/results/loong64-review/summary.md",
    "previous_gateway_gc_comparison": "Temporary direct-purego overlay: four full package runs passed with GOGC=10 GODEBUG=gccheckmark=1; this does not establish the cause or age of the current-gateway GC failure."
  },
  "review_round_2": {
    "status": "required items 1-3 implemented; optional item 4 Builder API gap documented",
    "qemu_argv": "Round 1 fix retained; multi-architecture argv unit tests added; amd64/386/riscv64/loong64 suites rerun using explicit patched QEMU, including the 386 missing-SSE2 negative probe",
    "long_chains": {
      "selector": "iterative reachability and suspended-node scheduling; folding/order preserved",
      "builder": "iterative deferred-expression materialization, validation and invalid-value descent",
      "reproduction_before": "10000 operations with 1 MiB Go stack: fatal stack overflow in prior selector",
      "reproduction_after": "1200000 instruction operations and 300000 deferred-expression operations compile and execute with 1 MiB Go stack",
      "regressions": [
        "subprocess long-chain test for instruction and expression forms",
        "shared expression DAG validation/materialization",
        "narrow parameters with garbage upper bits",
        "320 live GP/FP chain prefixes with spill offsets above 2047"
      ]
    },
    "managed_frames": {
      "loong64_limit_bytes": 640,
      "riscv64_limit_bytes": 640,
      "runtime_versions": [
        "1.26.9",
        "1.27.2"
      ],
      "reserve_justification": "800-byte Go NOSPLIT reserve minus 160 bytes: 40-byte invocation gateway plus at most 120-byte automatic LoongArch NOSPLIT leaf. morestack allocates no user-stack frame before switching to g0. Outgoing homes are included in the generated frame. Indirect calls retain Go linker assumptions about splittable callees; aggregate arbitrary NOSPLIT/generated call chains are not proven by a per-frame cap.",
      "leaf_saves": "drop unconditional full C save sets for managed leaf entries; keep actual allocated GPR saves. Allocation/expansion FPRs are C-volatile. Functions with Go calls keep all C-preserved GPR/FPR saves.",
      "boundary_tests": "execute exact 640-byte managed frames with three Go calls, live GP/FP values, GC and stack growth; compiler and registrar reject 656 bytes"
    },
    "limit_comparison": {
      "go_version": "1.27.2",
      "start_index": 100000,
      "accepted_cases_per_integer_or_fp_mode": 128,
      "variants": [
        "leaf",
        "native C calls",
        "mixed calls"
      ],
      "Go/C/Platform_attempts_per_cpu": 768,
      "before_limits_per_cpu": 125,
      "after_limits_per_cpu": 0,
      "before_limit_percent": 16.276041666666664,
      "after_limit_percent": 0,
      "cpus": [
        "la464,lsx=off,lasx=off",
        "max"
      ],
      "other_limits": "128-byte test stack limits still counted separately; not waived as passes",
      "mismatches": 0
    },
    "public_byte_order_memory": {
      "builder_big_endian_selector": false,
      "public_ir_and_machine_support": true,
      "machine_execution_coverage": "target/loong64 TestNativeMemoryAddressPlans exercises native and big-endian operations"
    },
    "evidence": "e2e/.build/results/loong64-review2/summary.md",
    "acceptance": {
      "native_go_test_all": "pass",
      "harness_go_test_all": "pass",
      "cross_target_vet": "pass",
      "diff_check": "pass",
      "go_versions": [
        "1.26.9",
        "1.27.2"
      ],
      "qemu_dispatch": "explicit patched QEMU; private binfmt retained for reexec",
      "full_checks": {
        "loong64": 130,
        "riscv64": 140,
        "amd64": 144,
        "386": 134
      },
      "initial_386_failures": {
        "count": 4,
        "cause": "round 1 syscall fixture used ConstNative(39), 32 bits on 386; Syscall requires 64 bits",
        "fix": "use Const(39)",
        "replay": "all four compiler-package CPU/version executions pass; initial failures retained in 386.xml"
      },
      "final_refresh": {
        "checks": 30,
        "failures": 0,
        "coverage": "builder package on loong64/riscv64/amd64/386 and compiler package on 386, both CPU profiles and both Go versions"
      },
      "qemu_argv_probes": {
        "checks": 6,
        "failures": 0,
        "selected_test_runs_per_probe": 2,
        "excluded_test_runs": 0
      },
      "remaining_failures_in_required_round_2_gates": 0,
      "round_1_gc_stress_observation": "remains unresolved; not reproduced or claimed fixed by round 2"
    }
  },
  "gc_investigation": {
    "status": "GC-checker failure reproduced in standalone standard Go under QEMU; original Pinner panic still unresolved",
    "production_fix": false,
    "standalone_probe": "e2e/probes/gcmap/main.go",
    "probe_dependencies": "standard library only; no NyanJIT, purego, unsafe, C calls, metadata registration, or emitted code",
    "probe_workload": "20 goroutines, 400000 nested maps per process",
    "control_runs": [
      {
        "config": "go127-max",
        "cpu": "max",
        "overrides": {},
        "exit_codes": [
          0,
          0,
          2,
          0,
          0,
          0
        ],
        "passes": 5,
        "failures": 1
      },
      {
        "config": "go127-baseline",
        "cpu": "la464,lsx=off,lasx=off",
        "overrides": {},
        "exit_codes": [
          0,
          0,
          0,
          2,
          0,
          0
        ],
        "passes": 5,
        "failures": 1
      },
      {
        "config": "go127-nogreentea-max",
        "cpu": "max",
        "overrides": {},
        "exit_codes": [
          0,
          0,
          0,
          0,
          0,
          0
        ],
        "passes": 6,
        "failures": 0
      },
      {
        "config": "go127-noasync-max",
        "cpu": "max",
        "overrides": {
          "GODEBUG": "gccheckmark=1,asyncpreemptoff=1"
        },
        "exit_codes": [
          0,
          0,
          0,
          0,
          0,
          0
        ],
        "passes": 6,
        "failures": 0
      },
      {
        "config": "go127-oneproc-max",
        "cpu": "max",
        "overrides": {
          "GOMAXPROCS": "1"
        },
        "exit_codes": [
          0,
          0,
          0,
          0,
          0,
          0
        ],
        "passes": 6,
        "failures": 0
      },
      {
        "config": "go126-max",
        "cpu": "max",
        "overrides": {},
        "exit_codes": [
          0,
          0,
          0,
          2,
          0,
          0
        ],
        "passes": 5,
        "failures": 1
      },
      {
        "config": "go126-baseline",
        "cpu": "la464,lsx=off,lasx=off",
        "overrides": {},
        "exit_codes": [
          0,
          0,
          0,
          0,
          0,
          0
        ],
        "passes": 6,
        "failures": 0
      }
    ],
    "native_arm64_control": "one run passes",
    "current_full_package_gc_repeats": 5,
    "preserved_round_1_sweep_repeats": 10,
    "compiler_only_gc_repeats": 3,
    "optional_suite": "e2e/loong64_gc.star",
    "stress_matrix": {
      "initial": "three profile processes passed; Go 1.26.9 baseline exited 2; unwritable JUnit path lost full output; summary retained",
      "isolated_baseline_repeat": "Go 1.26.9 baseline: build and three-repeat execution pass",
      "recorded_matrix_repeat": {
        "checks": 6,
        "failures": 1,
        "failure_profile": "Go 1.27.2 / la464,lsx=off,lasx=off",
        "reason": "checkmark found unmarked object",
        "evidence": "e2e/.build/results/loong64-gc/profiles-repeat.xml"
      },
      "settings": "GOGC=10 GODEBUG=gccheckmark=1; three complete package repeats per process; no retries or skip reclassification"
    },
    "conclusion": "Standalone failures are reproduced with the published tonistiigi 10.2.3 build; Debian 10.0.13 and unpatched upstream 10.2.3 GCC/glibc controls each pass 40 runs. This narrows the issue to an emulator-build difference without identifying a particular patch, compiler or libc cause. The original Pinner panic is not independently reproduced.",
    "native_tests": "pass",
    "harness_tests": "pass",
    "cross_target_vet": "pass",
    "diff_check": "pass",
    "evidence": "e2e/.build/results/loong64-gc2/summary.md",
    "control_interpretation": "gccheckmark=1 already sets asyncpreemptoff=1 in both Go runtimes. The explicit noasync sample is redundant and supplies no preemption evidence.",
    "emulator_comparison": {
      "host": "native linux/arm64",
      "guest_executable": "identical standalone standard-library-only map-only-1.27.2 binary across user-mode emulators",
      "conditions": "GOGC=10 GODEBUG=gccheckmark=1; baseline la464,lsx=off,lasx=off and max; 20 fresh processes per CPU",
      "published_and_debian": [
        {
          "config": "tonistiigi-baseline",
          "cpu": "la464,lsx=off,lasx=off",
          "exit_codes": [
            0,
            2,
            0,
            0,
            0,
            2,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0
          ],
          "passes": 18,
          "failures": 2
        },
        {
          "config": "debian-baseline",
          "cpu": "la464,lsx=off,lasx=off",
          "exit_codes": [
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0
          ],
          "passes": 20,
          "failures": 0
        },
        {
          "config": "tonistiigi-max",
          "cpu": "max",
          "exit_codes": [
            2,
            0,
            0,
            2,
            0,
            0,
            0,
            0,
            2,
            2,
            2,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0
          ],
          "passes": 15,
          "failures": 5
        },
        {
          "config": "debian-max",
          "cpu": "max",
          "exit_codes": [
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0
          ],
          "passes": 20,
          "failures": 0
        }
      ],
      "unpatched_upstream": [
        {
          "config": "stock102-baseline",
          "cpu": "la464,lsx=off,lasx=off",
          "exit_codes": [
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0
          ],
          "passes": 20,
          "failures": 0
        },
        {
          "config": "stock102-max",
          "cpu": "max",
          "exit_codes": [
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0
          ],
          "passes": 20,
          "failures": 0
        }
      ],
      "upstream_source": "https://github.com/qemu/qemu/tree/v10.2.3",
      "source_archive_sha256": "d0ae37de1bb6df9dd6824078cb41903ecce8734f33696b5f717ceab66e99e4f0",
      "upstream_build": "GCC 14.2.0, GNU ld 2.44, glibc; loongarch64-linux-user only, no binfmt patches",
      "native_arm64": [
        {
          "go_version": "1.26.9",
          "exit_codes": [
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0
          ],
          "passes": 30,
          "failures": 0
        },
        {
          "go_version": "1.27.2",
          "exit_codes": [
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0,
            0
          ],
          "passes": 30,
          "failures": 0
        }
      ],
      "pinner_only_control": [
        {
          "config": "toni-pin-max",
          "exit_codes": [
            0,
            0,
            0,
            0,
            0,
            0
          ],
          "passes": 6,
          "failures": 0
        },
        {
          "config": "toni-pin-baseline",
          "exit_codes": [
            0,
            0,
            0,
            0,
            0,
            0
          ],
          "passes": 6,
          "failures": 0
        },
        {
          "config": "debian-pin-max",
          "exit_codes": [
            0,
            0,
            0,
            0,
            0,
            0
          ],
          "passes": 6,
          "failures": 0
        }
      ]
    },
    "selectable_runner": {
      "option": "NYANJIT_E2E_LOONG64_QEMU=debian",
      "default": "tonistiigi",
      "stock_binary": "/opt/nyanjit/qemu-debian/qemu-loongarch64",
      "private_binfmt_flags": "F; no P since stock QEMU constructs argv[0]",
      "direct_dispatch": "stock QEMU receives executable once; tonistiigi receives executable then explicit original argv[0]",
      "container_build": "repository e2e/container/Dockerfile with VM_OSES= OPENBSD_ARCHS= LINUX_ONLY=true, image nyanjit-e2e:loong64-gc-full",
      "tested_versions": [
        "1.26.9",
        "1.27.2"
      ],
      "acceptance": {
        "debian-runner.xml": {
          "checks": 6,
          "failures": 0
        },
        "private-probes.xml": {
          "checks": 10,
          "failures": 0
        },
        "explicit-probes.xml": {
          "checks": 10,
          "failures": 0
        },
        "debian-matrix.xml": {
          "checks": 130,
          "failures": 0
        }
      },
      "regressions": "runner mode validation, explicit/private argv conventions, settings forwarded by run.sh, ELF registration match and flags, failed setup preserves previous handler",
      "disabled_gc_features": []
    }
  },
  "lsx_lowering": {
    "native_bits": 128,
    "integer_lanes_bits": [
      8,
      16,
      32,
      64
    ],
    "allocation": {
      "shared_FP_and_vector_homes": "f8-f23 (sixteen total)",
      "expansion_temporaries": "f0-f2",
      "excluded_partial_preserved_aliases": "vr24-vr31: LP64D preserves only their low 64-bit FPR values",
      "call_policy": "spill complete live vectors across C and Go calls",
      "spill_slot_bytes": 16,
      "managed_frame_limit_bytes": 640
    },
    "operations": [
      "unaligned VLD/VST, signed 12-bit offsets, VLDX/VSTX indexed forms",
      "PCADDU12I + VLD literal constants",
      "splat, extract, insert, zero, insert-zero",
      "add, subtract, multiply, bitwise AND/OR/XOR",
      "immediate and scalar/per-lane variable shifts and rotations with full unsigned-count semantics",
      "byte equality and VMSKLTZ.B/VPICKVE2GR.WU logical masks"
    ],
    "native_64_bit_multiply": "VMUL.D verified in Go opcode tables and QEMU; covered by differential and aliasing tests",
    "feature_policy": "nil follows resolved profile/HWCAP; empty non-nil forces scalar; explicit LSX/LASX requirements reject incompatible explicit CPU profiles with ErrCPUUnsupported; LASX implies LSX but emits no LASX instructions",
    "reports": "Native/Expanded/Split/Scalarized; exact temp writes from the common vector plan; per-lane right rotation reports Native",
    "fallbacks": [
      "carryless multiplication",
      "non-byte-equality comparisons",
      "short vector shapes",
      "scoped/label vector homes"
    ],
    "sources": [
      "Go 1.26.9/1.27.2 cmd/internal/obj/loong64/instOp.go, asm.go and assembler encoding tests",
      "Go cmd/compile/abi-internal.md: no callee-save registers",
      "https://cdn.kernel.org/doc/html/latest/arch/loongarch/introduction.html",
      "https://github.com/loongson/la-abi-specs/blob/release/lapcs.adoc",
      "https://github.com/qemu/qemu/blob/v10.2.3/target/loongarch/insns.decode"
    ],
    "reference_limitations": {
      "published_manual": "Volume 1 is base architecture; the published Volume 2 vector manual is a two-page TBD stub",
      "x_arch": "golang.org/x/arch v0.32.0 loong64asm has no LSX decoder. Scalar expansion words are decoded; LSX words are pinned to Go/QEMU references and executed against independent lane oracles."
    },
    "validation": {
      "native_go_test_all": "pass",
      "cross_target_vet": "pass",
      "diff_check": "pass",
      "container_image": "nyanjit-e2e:loong64-gc-full",
      "qemu": "tonistiigi 10.2.3, private binfmt namespace",
      "cpus": [
        "la464,lsx=off,lasx=off",
        "max"
      ],
      "go_versions": [
        "1.26.9",
        "1.27.2"
      ],
      "full_package_and_e2e_matrix": {
        "checks_per_Go_version": 66,
        "total_checks": 132,
        "failures": 0,
        "skips": 0,
        "junit": "/cache/results/lsx-loong64-final.xml"
      },
      "randomized_vectors": {
        "programs": 200,
        "configurations": [
          "baseline/default",
          "baseline/forced-scalar",
          "max/default-LSX",
          "max/forced-scalar"
        ],
        "matching_execution_records": 2816,
        "mismatches": 0,
        "final_native_replay_records": 704,
        "final_native_replay_mismatches": 0,
        "reference": "amd64 forced-scalar fingerprints"
      },
      "direct_aliasing": "all integer lane widths, both source aliases and a distinct destination, scalar and per-lane counts including overshifts and high 64-bit counts",
      "mixed_pressure": "24 vectors and 12 FP values for leaf stack/workspace; 6 vectors and 6 FP values around foreign C, managed C and Go calls with GC/stack growth, both FP widths",
      "managed_frame_boundary": "22 live vectors execute in an exact 640-byte frame across Go stack growth/GC on both Go versions; 656-byte frame rejected",
      "indexed_memory": "unaligned VLDX/VSTX execution and guards on both Go versions",
      "vector_fuzzer": "shared varfuzz remains scalar-only; separate randomized vector differential replay and lane-oracle tests cover vectors",
      "sha256": {
        "scalar_listing_instructions": 41770,
        "LSX_listing_instructions": 2586,
        "instruction_reduction_percent": 93.8,
        "digests": "all match crypto/sha256 in native LSX and scalar modes on both Go versions",
        "hardware_timing": "not measured"
      }
    }
  }
}
```

</details>


## Compile-time baseline

The former perf-baseline.json records post-round-6 measurements before allocation reductions: Go 1.27.2 linux/arm64 on CPU 5, GOMAXPROCS=1, denominator source IR instructions before vector legalization. The complete original samples are retained below; later improvement tables in LTM use different baselines. The recording command was:

```sh
taskset -c 5 env GOMAXPROCS=1 go test ./compile -run '^$' -bench '^BenchmarkPrepare$' -benchmem -benchtime=300ms -count=5 -cpu=1
```

| Workload | ns/op | B/op | allocs/op | Source instructions/op | ns/instruction | B/instruction | allocs/instruction |
|----------|-------|------|-----------|------------------------|----------------|---------------|--------------------|
| calls | 104611501.0 | 95703000.0 | 1164721.0 | 10004.0 | 10457.0 | 9566.0 | 116.4 |
| chain | 12306693.0 | 41592583.0 | 73597.0 | 8025.0 | 1534.0 | 5183.0 | 9.171 |
| fp | 76226702.0 | 93948180.0 | 793515.0 | 12025.0 | 6339.0 | 7813.0 | 65.99 |
| loops | 1981299055.0 | 557929400.0 | 345001.0 | 12005.0 | 165039.0 | 46475.0 | 28.74 |
| pressure | 20714686.0 | 68494821.0 | 136400.0 | 16001.0 | 1295.0 | 4281.0 | 8.524 |
| scopes | 9263542665.0 | 28484464.0 | 46878.0 | 8003.0 | 1157509.0 | 3559.0 | 5.858 |
| sha256-scalar | 191683190.0 | 266392808.0 | 387045.0 | 3187.0 | 60145.0 | 83587.0 | 121.4 |
| sha256-simd | 15534973.0 | 21178774.0 | 173301.0 | 3187.0 | 4874.0 | 6645.0 | 54.38 |
| small | 62990.0 | 168056.0 | 618.0 | 44.0 | 1432.0 | 3819.0 | 14.05 |
| tiny | 8504.0 | 13200.0 | 169.0 | 3.0 | 2835.0 | 4400.0 | 56.33 |
| vectors | 57798474.0 | 68966684.0 | 663528.0 | 9004.0 | 6419.0 | 7660.0 | 73.69 |

### Full baseline samples

<details>
<summary>Historical perf-baseline.json, including all samples and recording metadata</summary>

```json
{
  "schema": 1,
  "go": "go version go1.27.2 linux/arm64",
  "host": "aarch64",
  "gomaxprocs": 1,
  "affinity_cpu": 5,
  "command": "taskset -c 5 env GOMAXPROCS=1 go test ./compile -run '^$' -bench '^BenchmarkPrepare$' -benchmem -benchtime=300ms -count=5 -cpu=1",
  "denominator": "source IR instructions, before vector legalization",
  "workloads": {
    "chain": {
      "median": {
        "ns/op": 12306693.0,
        "B/insn": 5183.0,
        "allocs/insn": 9.171,
        "insns/op": 8025.0,
        "ns/insn": 1534.0,
        "B/op": 41592583.0,
        "allocs/op": 73597.0
      },
      "samples": [
        {
          "ns/op": 12066950.0,
          "B/insn": 5183.0,
          "allocs/insn": 9.171,
          "insns/op": 8025.0,
          "ns/insn": 1504.0,
          "B/op": 41592603.0,
          "allocs/op": 73597.0
        },
        {
          "ns/op": 12306693.0,
          "B/insn": 5183.0,
          "allocs/insn": 9.171,
          "insns/op": 8025.0,
          "ns/insn": 1534.0,
          "B/op": 41592582.0,
          "allocs/op": 73597.0
        },
        {
          "ns/op": 12415473.0,
          "B/insn": 5183.0,
          "allocs/insn": 9.171,
          "insns/op": 8025.0,
          "ns/insn": 1547.0,
          "B/op": 41592584.0,
          "allocs/op": 73597.0
        },
        {
          "ns/op": 12338020.0,
          "B/insn": 5183.0,
          "allocs/insn": 9.171,
          "insns/op": 8025.0,
          "ns/insn": 1537.0,
          "B/op": 41592583.0,
          "allocs/op": 73597.0
        },
        {
          "ns/op": 12134573.0,
          "B/insn": 5183.0,
          "allocs/insn": 9.171,
          "insns/op": 8025.0,
          "ns/insn": 1512.0,
          "B/op": 41592583.0,
          "allocs/op": 73597.0
        }
      ]
    },
    "pressure": {
      "median": {
        "ns/op": 20714686.0,
        "B/insn": 4281.0,
        "allocs/insn": 8.524,
        "insns/op": 16001.0,
        "ns/insn": 1295.0,
        "B/op": 68494821.0,
        "allocs/op": 136400.0
      },
      "samples": [
        {
          "ns/op": 21439181.0,
          "B/insn": 4281.0,
          "allocs/insn": 8.524,
          "insns/op": 16001.0,
          "ns/insn": 1340.0,
          "B/op": 68494832.0,
          "allocs/op": 136400.0
        },
        {
          "ns/op": 20967788.0,
          "B/insn": 4281.0,
          "allocs/insn": 8.524,
          "insns/op": 16001.0,
          "ns/insn": 1310.0,
          "B/op": 68494820.0,
          "allocs/op": 136400.0
        },
        {
          "ns/op": 20355742.0,
          "B/insn": 4281.0,
          "allocs/insn": 8.524,
          "insns/op": 16001.0,
          "ns/insn": 1272.0,
          "B/op": 68494816.0,
          "allocs/op": 136400.0
        },
        {
          "ns/op": 20714686.0,
          "B/insn": 4281.0,
          "allocs/insn": 8.524,
          "insns/op": 16001.0,
          "ns/insn": 1295.0,
          "B/op": 68494821.0,
          "allocs/op": 136400.0
        },
        {
          "ns/op": 20686509.0,
          "B/insn": 4281.0,
          "allocs/insn": 8.524,
          "insns/op": 16001.0,
          "ns/insn": 1293.0,
          "B/op": 68494822.0,
          "allocs/op": 136400.0
        }
      ]
    },
    "fp": {
      "median": {
        "ns/op": 76226702.0,
        "B/insn": 7813.0,
        "allocs/insn": 65.99,
        "insns/op": 12025.0,
        "ns/insn": 6339.0,
        "B/op": 93948180.0,
        "allocs/op": 793515.0
      },
      "samples": [
        {
          "ns/op": 76226702.0,
          "B/insn": 7812.0,
          "allocs/insn": 65.99,
          "insns/op": 12025.0,
          "ns/insn": 6339.0,
          "B/op": 93938886.0,
          "allocs/op": 793514.0
        },
        {
          "ns/op": 76884298.0,
          "B/insn": 7813.0,
          "allocs/insn": 65.99,
          "insns/op": 12025.0,
          "ns/insn": 6394.0,
          "B/op": 93948180.0,
          "allocs/op": 793515.0
        },
        {
          "ns/op": 75543357.0,
          "B/insn": 7813.0,
          "allocs/insn": 65.99,
          "insns/op": 12025.0,
          "ns/insn": 6282.0,
          "B/op": 93948226.0,
          "allocs/op": 793515.0
        },
        {
          "ns/op": 74376047.0,
          "B/insn": 7813.0,
          "allocs/insn": 65.99,
          "insns/op": 12025.0,
          "ns/insn": 6185.0,
          "B/op": 93946320.0,
          "allocs/op": 793515.0
        },
        {
          "ns/op": 77408810.0,
          "B/insn": 7813.0,
          "allocs/insn": 65.99,
          "insns/op": 12025.0,
          "ns/insn": 6437.0,
          "B/op": 93948230.0,
          "allocs/op": 793515.0
        }
      ]
    },
    "loops": {
      "median": {
        "ns/op": 1981299055.0,
        "B/insn": 46475.0,
        "allocs/insn": 28.74,
        "insns/op": 12005.0,
        "ns/insn": 165039.0,
        "B/op": 557929400.0,
        "allocs/op": 345001.0
      },
      "samples": [
        {
          "ns/op": 1991741133.0,
          "B/insn": 46475.0,
          "allocs/insn": 28.74,
          "insns/op": 12005.0,
          "ns/insn": 165909.0,
          "B/op": 557929592.0,
          "allocs/op": 345005.0
        },
        {
          "ns/op": 1975348462.0,
          "B/insn": 46475.0,
          "allocs/insn": 28.74,
          "insns/op": 12005.0,
          "ns/insn": 164544.0,
          "B/op": 557929400.0,
          "allocs/op": 345001.0
        },
        {
          "ns/op": 1987404431.0,
          "B/insn": 46475.0,
          "allocs/insn": 28.74,
          "insns/op": 12005.0,
          "ns/insn": 165548.0,
          "B/op": 557929384.0,
          "allocs/op": 345000.0
        },
        {
          "ns/op": 1977398035.0,
          "B/insn": 46475.0,
          "allocs/insn": 28.74,
          "insns/op": 12005.0,
          "ns/insn": 164715.0,
          "B/op": 557929416.0,
          "allocs/op": 345001.0
        },
        {
          "ns/op": 1981299055.0,
          "B/insn": 46475.0,
          "allocs/insn": 28.74,
          "insns/op": 12005.0,
          "ns/insn": 165039.0,
          "B/op": 557929368.0,
          "allocs/op": 345000.0
        }
      ]
    },
    "calls": {
      "median": {
        "ns/op": 104611501.0,
        "B/insn": 9566.0,
        "allocs/insn": 116.4,
        "insns/op": 10004.0,
        "ns/insn": 10457.0,
        "B/op": 95703000.0,
        "allocs/op": 1164721.0
      },
      "samples": [
        {
          "ns/op": 105422039.0,
          "B/insn": 9566.0,
          "allocs/insn": 116.4,
          "insns/op": 10004.0,
          "ns/insn": 10538.0,
          "B/op": 95703000.0,
          "allocs/op": 1164721.0
        },
        {
          "ns/op": 104843570.0,
          "B/insn": 9566.0,
          "allocs/insn": 116.4,
          "insns/op": 10004.0,
          "ns/insn": 10480.0,
          "B/op": 95703010.0,
          "allocs/op": 1164721.0
        },
        {
          "ns/op": 101768885.0,
          "B/insn": 9566.0,
          "allocs/insn": 116.4,
          "insns/op": 10004.0,
          "ns/insn": 10173.0,
          "B/op": 95703005.0,
          "allocs/op": 1164721.0
        },
        {
          "ns/op": 104611501.0,
          "B/insn": 9566.0,
          "allocs/insn": 116.4,
          "insns/op": 10004.0,
          "ns/insn": 10457.0,
          "B/op": 95702938.0,
          "allocs/op": 1164720.0
        },
        {
          "ns/op": 103504086.0,
          "B/insn": 9566.0,
          "allocs/insn": 116.4,
          "insns/op": 10004.0,
          "ns/insn": 10346.0,
          "B/op": 95702944.0,
          "allocs/op": 1164720.0
        }
      ]
    },
    "vectors": {
      "median": {
        "ns/op": 57798474.0,
        "B/insn": 7660.0,
        "allocs/insn": 73.69,
        "insns/op": 9004.0,
        "ns/insn": 6419.0,
        "B/op": 68966684.0,
        "allocs/op": 663528.0
      },
      "samples": [
        {
          "ns/op": 57160017.0,
          "B/insn": 7660.0,
          "allocs/insn": 73.69,
          "insns/op": 9004.0,
          "ns/insn": 6348.0,
          "B/op": 68966676.0,
          "allocs/op": 663528.0
        },
        {
          "ns/op": 58254178.0,
          "B/insn": 7660.0,
          "allocs/insn": 73.69,
          "insns/op": 9004.0,
          "ns/insn": 6470.0,
          "B/op": 68966688.0,
          "allocs/op": 663529.0
        },
        {
          "ns/op": 57753879.0,
          "B/insn": 7660.0,
          "allocs/insn": 73.69,
          "insns/op": 9004.0,
          "ns/insn": 6414.0,
          "B/op": 68966668.0,
          "allocs/op": 663528.0
        },
        {
          "ns/op": 57798474.0,
          "B/insn": 7660.0,
          "allocs/insn": 73.69,
          "insns/op": 9004.0,
          "ns/insn": 6419.0,
          "B/op": 68966693.0,
          "allocs/op": 663529.0
        },
        {
          "ns/op": 58238991.0,
          "B/insn": 7660.0,
          "allocs/insn": 73.69,
          "insns/op": 9004.0,
          "ns/insn": 6468.0,
          "B/op": 68966684.0,
          "allocs/op": 663528.0
        }
      ]
    },
    "scopes": {
      "median": {
        "ns/op": 9263542665.0,
        "B/insn": 3559.0,
        "allocs/insn": 5.858,
        "insns/op": 8003.0,
        "ns/insn": 1157509.0,
        "B/op": 28484464.0,
        "allocs/op": 46878.0
      },
      "samples": [
        {
          "ns/op": 9225027796.0,
          "B/insn": 3559.0,
          "allocs/insn": 5.858,
          "insns/op": 8003.0,
          "ns/insn": 1152696.0,
          "B/op": 28484528.0,
          "allocs/op": 46879.0
        },
        {
          "ns/op": 9327543749.0,
          "B/insn": 3559.0,
          "allocs/insn": 5.858,
          "insns/op": 8003.0,
          "ns/insn": 1165506.0,
          "B/op": 28484464.0,
          "allocs/op": 46878.0
        },
        {
          "ns/op": 9266198175.0,
          "B/insn": 3559.0,
          "allocs/insn": 5.858,
          "insns/op": 8003.0,
          "ns/insn": 1157841.0,
          "B/op": 28484464.0,
          "allocs/op": 46878.0
        },
        {
          "ns/op": 9263542665.0,
          "B/insn": 3559.0,
          "allocs/insn": 5.858,
          "insns/op": 8003.0,
          "ns/insn": 1157509.0,
          "B/op": 28484480.0,
          "allocs/op": 46878.0
        },
        {
          "ns/op": 9263148721.0,
          "B/insn": 3559.0,
          "allocs/insn": 5.858,
          "insns/op": 8003.0,
          "ns/insn": 1157460.0,
          "B/op": 28484464.0,
          "allocs/op": 46878.0
        }
      ]
    },
    "sha256-scalar": {
      "median": {
        "ns/op": 191683190.0,
        "B/insn": 83587.0,
        "allocs/insn": 121.4,
        "insns/op": 3187.0,
        "ns/insn": 60145.0,
        "B/op": 266392808.0,
        "allocs/op": 387045.0
      },
      "samples": [
        {
          "ns/op": 194186599.0,
          "B/insn": 83587.0,
          "allocs/insn": 121.4,
          "insns/op": 3187.0,
          "ns/insn": 60931.0,
          "B/op": 266392832.0,
          "allocs/op": 387045.0
        },
        {
          "ns/op": 192169918.0,
          "B/insn": 83587.0,
          "allocs/insn": 121.4,
          "insns/op": 3187.0,
          "ns/insn": 60298.0,
          "B/op": 266392800.0,
          "allocs/op": 387045.0
        },
        {
          "ns/op": 190541292.0,
          "B/insn": 83587.0,
          "allocs/insn": 121.4,
          "insns/op": 3187.0,
          "ns/insn": 59787.0,
          "B/op": 266392792.0,
          "allocs/op": 387045.0
        },
        {
          "ns/op": 191683190.0,
          "B/insn": 83587.0,
          "allocs/insn": 121.4,
          "insns/op": 3187.0,
          "ns/insn": 60145.0,
          "B/op": 266392808.0,
          "allocs/op": 387045.0
        },
        {
          "ns/op": 189748252.0,
          "B/insn": 83587.0,
          "allocs/insn": 121.4,
          "insns/op": 3187.0,
          "ns/insn": 59538.0,
          "B/op": 266392808.0,
          "allocs/op": 387045.0
        }
      ]
    },
    "sha256-simd": {
      "median": {
        "ns/op": 15534973.0,
        "B/insn": 6645.0,
        "allocs/insn": 54.38,
        "insns/op": 3187.0,
        "ns/insn": 4874.0,
        "B/op": 21178774.0,
        "allocs/op": 173301.0
      },
      "samples": [
        {
          "ns/op": 15726377.0,
          "B/insn": 6645.0,
          "allocs/insn": 54.38,
          "insns/op": 3187.0,
          "ns/insn": 4935.0,
          "B/op": 21178765.0,
          "allocs/op": 173301.0
        },
        {
          "ns/op": 15530459.0,
          "B/insn": 6645.0,
          "allocs/insn": 54.38,
          "insns/op": 3187.0,
          "ns/insn": 4873.0,
          "B/op": 21178756.0,
          "allocs/op": 173301.0
        },
        {
          "ns/op": 15534973.0,
          "B/insn": 6645.0,
          "allocs/insn": 54.38,
          "insns/op": 3187.0,
          "ns/insn": 4874.0,
          "B/op": 21178774.0,
          "allocs/op": 173301.0
        },
        {
          "ns/op": 15076069.0,
          "B/insn": 6645.0,
          "allocs/insn": 54.38,
          "insns/op": 3187.0,
          "ns/insn": 4730.0,
          "B/op": 21178790.0,
          "allocs/op": 173301.0
        },
        {
          "ns/op": 15613059.0,
          "B/insn": 6645.0,
          "allocs/insn": 54.38,
          "insns/op": 3187.0,
          "ns/insn": 4899.0,
          "B/op": 21178796.0,
          "allocs/op": 173301.0
        }
      ]
    },
    "tiny": {
      "median": {
        "ns/op": 8504.0,
        "B/insn": 4400.0,
        "allocs/insn": 56.33,
        "insns/op": 3.0,
        "ns/insn": 2835.0,
        "B/op": 13200.0,
        "allocs/op": 169.0
      },
      "samples": [
        {
          "ns/op": 8706.0,
          "B/insn": 4400.0,
          "allocs/insn": 56.33,
          "insns/op": 3.0,
          "ns/insn": 2902.0,
          "B/op": 13200.0,
          "allocs/op": 169.0
        },
        {
          "ns/op": 8533.0,
          "B/insn": 4400.0,
          "allocs/insn": 56.33,
          "insns/op": 3.0,
          "ns/insn": 2844.0,
          "B/op": 13200.0,
          "allocs/op": 169.0
        },
        {
          "ns/op": 8474.0,
          "B/insn": 4400.0,
          "allocs/insn": 56.33,
          "insns/op": 3.0,
          "ns/insn": 2825.0,
          "B/op": 13200.0,
          "allocs/op": 169.0
        },
        {
          "ns/op": 8504.0,
          "B/insn": 4400.0,
          "allocs/insn": 56.33,
          "insns/op": 3.0,
          "ns/insn": 2835.0,
          "B/op": 13200.0,
          "allocs/op": 169.0
        },
        {
          "ns/op": 8488.0,
          "B/insn": 4400.0,
          "allocs/insn": 56.33,
          "insns/op": 3.0,
          "ns/insn": 2829.0,
          "B/op": 13200.0,
          "allocs/op": 169.0
        }
      ],
      "baseline_stage": "post round 6, before allocation reductions"
    },
    "small": {
      "median": {
        "ns/op": 62990.0,
        "B/insn": 3819.0,
        "allocs/insn": 14.05,
        "insns/op": 44.0,
        "ns/insn": 1432.0,
        "B/op": 168056.0,
        "allocs/op": 618.0
      },
      "samples": [
        {
          "ns/op": 62990.0,
          "B/insn": 3819.0,
          "allocs/insn": 14.05,
          "insns/op": 44.0,
          "ns/insn": 1432.0,
          "B/op": 168056.0,
          "allocs/op": 618.0
        },
        {
          "ns/op": 62650.0,
          "B/insn": 3819.0,
          "allocs/insn": 14.05,
          "insns/op": 44.0,
          "ns/insn": 1424.0,
          "B/op": 168056.0,
          "allocs/op": 618.0
        },
        {
          "ns/op": 62559.0,
          "B/insn": 3819.0,
          "allocs/insn": 14.05,
          "insns/op": 44.0,
          "ns/insn": 1422.0,
          "B/op": 168056.0,
          "allocs/op": 618.0
        },
        {
          "ns/op": 64180.0,
          "B/insn": 3819.0,
          "allocs/insn": 14.05,
          "insns/op": 44.0,
          "ns/insn": 1459.0,
          "B/op": 168056.0,
          "allocs/op": 618.0
        },
        {
          "ns/op": 70274.0,
          "B/insn": 3819.0,
          "allocs/insn": 14.05,
          "insns/op": 44.0,
          "ns/insn": 1597.0,
          "B/op": 168056.0,
          "allocs/op": 618.0
        }
      ],
      "baseline_stage": "post round 6, before allocation reductions"
    }
  }
}
```

</details>


## Document provenance

| Former document | Consolidated location |
|-----------------|-----------------------|
| docs/fp-performance.md | [Floating-point lowering](#floating-point-lowering), historical stage tables and target records |
| docs/scoped-variable-codegen.md | [Scopes and variable homes](#scopes-and-variable-homes), fuzz/scaling guidance and historical tables |
| docs/dual-tier-design.md implementation proposal | [Compilation tiers](#compilation-tiers); the public file now describes current usage |
| docs/virtual-register-design.md allocation policies and target flag/loop details | [Scopes and variable homes](#scopes-and-variable-homes) and [layout/effects](#layout-effects-and-execution); public value/lifetime/call contracts remain |
| docs/backend-parity.md instruction evidence, acceptance and review history | [Backend parity validation](#backend-parity-validation) and historical tables |
| docs/simd-design.md allocation internals, proposed APIs, phased roadmap and acceptance/test history | [SIMD lowering](#simd-lowering), roadmap contracts, verification and historical tables; implemented consumer semantics/examples remain public |
| docs/{amd64,arm64,riscv64,386,ppc,loong64}-completion.json | [Target validation records](#target-validation-records), with full historical fields |
| docs/perf-baseline.json | [Compile-time baseline](#compile-time-baseline), with all samples |

Read [LTM/INDEX.md](LTM/INDEX.md) for broader implementation investigations and later compile-cost measurements. Architectural follow-ups belong to TODO.md; append new findings to JOURNAL.md and refine this guide when mechanisms change.
