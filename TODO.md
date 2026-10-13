# TODO

Unchecked entries track remaining work. Checked findings retain their original
pre-fix descriptions and historical API names; the accompanying resolution
describes what changed. Use the [README](README.md) and [IR specification](docs/ir.md)
for current contracts. Audit scratch paths, line numbers and local reports below
refer to the original review workspace and may be absent from a clean checkout.

## Security audit (2026-10-25)

Findings from the pre-release review. Details, evidence, the threat-model pass
and the areas found sound are in
`.agents-workspace/tmp/security-audit-20261025/journal.md` (IDs match);
reproducer sources are under `.agents-workspace/tmp/security-audit-20261025/poc/`.
IDs continue the 2026-10-18 numbering. Each fix needs a regression test that
fails before it.

### Blockers for publication

- [x] **F14 (High) arm64 control flow reads label-parameter slots nothing wrote.**
  Fixed: labels bound before one segment alias one block and now share its parameter homes (every target; slot homes were per label); `ir.Validate` requires aliased labels to declare the same parameters and counts incoming edges per block; TestAliasedLabelsShareParameterHomes, TestValidateAliasedLabelParameters.
  With scoped Vars and two Var-counted loops in sequence, the edge into the
  second loop writes one slot while the loop reads another, so stale stack or
  workspace contents reach results (C ABI, any stack mode, ContextForeign,
  with or without Harden; 68 of 500 random control programs). Make the edge
  write the slot the target fragment reads (`compile/emitter_control.go`).
  PoC: `poc/s5` (`BIND=1 IT=22 ./preserve control 1 1 20`, `red22.txt`).
- [x] **F15 (High) arm64 C-ABI entries clobber callee-saved x19–x27.**
  Fixed: FP→GPR float moves count as writes; control lowering checks every written callee-preserved register after emission and repeats the pass with it saved (all C-ABI targets). A ppc64le fixture that fitted 128 bytes only by writing r14 unsaved now needs its frame. TestCEntriesReportOnlyCallerSavedClobbers, TestARM64EntriesRestoreEveryPreservedRegister, TestNarrowMemorySmallFrameRegression.
  `compile/compiler.go:2951` drops FP→GPR `SelectedFloatMove` destinations
  from `written`; `compile/emitter_control.go:769` plans preservation before
  edge and predicate emission marks registers written. Record those writes,
  plan preservation after them, and assert every written non-volatile register
  is saved. PoC: `poc/s5` (`red-s128.txt`, `red22b.txt`).
- [x] **F16 (High) arm64 `Code.Call` leaves a stale frame pointer after a Go
  callee grows the stack**, crashing the runtime under `runtime/trace` or
  block/mutex profiling. The 16-byte gap in
  `backend/execution/call_linux_arm64.s` moves the trampoline's frame record
  off the callee's top word, so `copystack` does not adjust it. Enter managed
  code with Go's standard layout. PoC: `poc/s2/goreg/fpstale`, `fpwalk`;
  root cause `poc/s2/fixcheck-trampoline.patch`.
  Fixed: Go-managed code is entered through gap-free `callManaged`/`callPairManaged` trampolines on arm64, so the assembler's frame record is the managed frame's reserved top word; unmanaged code keeps the AAPCS64 gap. TestSecurityFramePointerChainAfterStackGrowth (stale in 64/64 calls before), TestSecurityFramePointerProfiling now grows fresh goroutine stacks.
- [x] **F17 (High, 386) `Code.Func` lets Go-ABI code overwrite the caller's
  frame.** The epilogue always stores 8 bytes but `checkFuncType`
  (`backend/execution/func.go:117`) accepts narrower or no results. Require
  one 8-byte result on 386 Go-ABI entries. PoC: `poc/s1/func386`.
  Fixed: `checkFuncType` requires one 8-byte integer result on 386; Func documents it. TestFuncResultWidthOn386.
- [ ] **F22 The root module cannot be fetched.** `go.mod` requires
  `backend v0.0.0` through `replace => ./backend`, which consumers ignore. Tag
  `backend/vX.Y.Z`, require it and drop the replace before tagging the root
  module. PoC: `poc/s6/s7`.

### Should fix before publication

- [x] **F18 `ResultParts` operands are checked for width, not kind**
  (`ir/program.go:158-166`, `:541-543`): a vector operand passes `Validate`
  and `Lower` panics at `compile/vector_legalize.go:80` (amd64/386/arm64) or
  returns invalid. Require a 64-bit scalar. PoC: `poc/s6/m`
  `TestResultPartsVectorPanics`.
  Fixed: both checks require a 64-bit scalar; TestValidateResultPartsRequireScalars, FuzzSecurityRawIR seed.
- [x] **F19 `CarrylessMul` expansion escapes IR size caps**
  (`compile/vector_legalize.go:530-554`): ~1.2 MB of compile work per op,
  16k ops reach 8.7 GB RSS. Budget legalized expansion (LimitError), add arm64
  PMULL, document the cost. PoC: `poc/s6/m/cmd/scale -shape clmul`.
  Fixed: vector legalization returns ErrBackendLimit past 64 instructions per IR instruction plus 131,072; the README trust model documents compile cost. TestCarrylessMulExpansionIsBudgeted. Native arm64 PMULL is L25.
- [x] **F20 amd64 `ElideDeadFlags` trusts the stream's flag options**
  (`backend/target/amd64/dead_flags.go`): size-only or legacy streams make it
  select LEA where flags are read. Use amd64's own flag facts or reject
  inexact streams; align `Stream.mark`'s legacy masks with Append. PoC:
  `poc/s3/elideopts`.
  Fixed: kills are the stream's flags intersected with what amd64 writes on every CPU (`FlagFactsWithCPU` with BMI2), and a candidate's flags include amd64's own facts; `ModifiedFlags`/`ModifiedFlagsAt` keep their conservative legacy meaning. TestElideDeadFlagsIgnoresInexactStreams; corpus output unchanged.
- [x] **F21 `regalloc.FunctionAllocator` places multi-word values out of the
  frame** (`backend/regalloc/function_alloc.go` `assignSlots`, `takeChunk`):
  single-slot reuse for `StorageWords ≥ 3`, two-slot edge temporaries, no
  validation. PoC: `poc/s5/poc/cmd/fnslots`.
  Fixed: values are validated (at most two register words, storage covering them, more than two storage words only for vectors), single-slot reuse is limited to one-word values, and edge temporaries are as wide as the widest stored value. TestFunctionAllocatorSlotsFitWideValues.
- [x] **L13 amd64 `FuseLoops` flag liveness is all-or-nothing**
  (`backend/target/amd64/loops.go:62-63`) and `ChangesFlags` panics on
  size-only streams. Use per-flag liveness with the elider's readers; make
  `ChangesFlags`/`ModifiedFlags` nil-safe. PoC: `poc/s3/fuseloop`, `fusepanic`.
  Fixed: per-flag liveness with the elider's readers and every-CPU kills; `Stream.ChangesFlags` reports no effect without flag metadata. TestFuseLoopsKeepsFlagsReadPastPartialWriters.
- [x] **L14 Sealed accessors return borrowed storage compile trusts**
  (`ir/sealed.go`, `compile/program_compile.go`), and `ProgramBuilder.Seal`
  aliases `Data`/`Effect`. Copy or document. PoC: `poc/s6/m`
  `TestSealedAccessorEntryRegNative`, `TestProgramBuilderAliasesData`.
  Fixed: `ProgramBuilder.Seal` clones each instruction, so it owns Args, Data and Effect (TestProgramBuilderSealCopiesData); the README trust model names borrowed sealed storage as a trusted escape hatch. Accessors keep lending storage without copying: the owner decided against a deep clone per call (2026-10-26).
- [x] **L15 amd64 managed frames do not reserve the top word**
  (`compile/backend_amd64_target.go:34-41`), which Go's `adjustframe` treats as
  a saved BP. Reserve it as on arm64 and port the top-word test.
  Fixed: managed amd64 frames count one more word, so `frame-8` lies above every home and spill. TestAMD64ManagedFramesLeaveTopWord.
- [x] **L17 386 `CompareImmediate` truncates out-of-range raw-IR immediates**
  (`backend/target/i386/byte.go:307-312`; `ir.Validate` does not range-check).
  PoC: `poc/s4/poc/narrowimm`.
  Fixed: `ir.Validate` rejects equality and unsigned comparisons of narrow operands against immediates outside `[0, 2^w−1]` (docs/ir.md §5.3). TestValidateNarrowCompareImmediates.
- [x] **L18 ppc64 Emitter panics on `OpSignExtend` Imm=64 with a FlagRequest**
  (`backend/target/ppc64/emitter.go`; OpNarrow has the guard). PoC:
  `poc/s4/poc/sxpanic`.
  Fixed: an in-place 64-bit extension with a flag request returns ErrUnsupportedOperation. TestSignExtendInPlaceFlagRequestFailsClosed.

### Hardening (after publication is fine)

- [ ] **L16 Size and encoding validation disagree on unused operand fields**
  (`backend/target/amd64/validation.go:82`, arm64 similar). Validate before
  encoding or ignore unused fields when sizing. PoC: `poc/s3/size0`.
  Deferred to hardening: a fix needs a per-op table of the fields each machine
  opcode uses on all six targets (sizing must ignore unused fields while invalid
  used fields keep size 0, per L1). Compile never sets stray fields and its
  per-instruction size check catches a mismatch.
- [ ] **L19** Normalize `CPU.Extensions` in the target cache key or cap the
  cache (`compile/target.go`). PoC: `poc/s5/poc/cmd/tcache`.
- [ ] **L20** Valid programs rejected (fail closed): ~~aliased labels with a
  cross-block use~~ (fixed with F14); 386 `VCompare` of a
  value with itself (`backend/regalloc/allocator.go:385`); `select_verify`
  narrow casts across blocks (`poc/s4/poc/cfg -seed 217`); 386 Harden pressure.
- [ ] **L21** Report the predicate budget as a `LimitError`
  (`ir/program.go:106`, `builder/builder_finish.go:86`).
- [ ] **L22** Use worklist liveness in `amd64/dead_flags.go`,
  `machine/zero_loops.go` and amd64 `fuseLoops` (quadratic on raw streams).
- [x] **L23** Remove the session path from
  `.agents/skills/nyanjit-review-rounds/assets/scripts/memwatch.sh`, extend the
  hygiene regex, and exclude `.agents-workspace/` and `*.test` in `.dockerignore`.
  Fixed: memwatch.sh takes `SCRATCH`; hygiene.sh also matches `-home-<user>-`; `.dockerignore` excludes both.
- [ ] **L25** Lower `CarrylessMul` with arm64 PMULL/PMULL2 when NEON has the
  crypto extension, instead of the ~500-instruction scalar fallback (F19).
- [ ] **L24** Smaller items: CallGo denylist misses exported `execution.Call*`
  helpers; riscv64/loong64 managed prologues store RA after adjusting SP;
  per-architecture `hardenRegisters`; ppc64le icache coherence comment; arm64
  `FuseZeroLoops` with register 31; stronger `FuzzEncoderVisit`;
  `FuzzScopedVars` exhausting the registration budget; update the review
  skill's `new-poc-module.sh` for the backend module (done, and SKILL.md now
  forbids `-mod=mod` in the repository).

## Security audit (2026-10-18)

Findings from the pre-publication security audit. Details, evidence and the
list of areas found sound are in
`.agents-workspace/tmp/security-audit-20261018/journal.md` (finding IDs below
match it); reproducers are under `.agents-workspace/tmp/security-audit-20261018/poc/`.
Each fix should land with a regression test that fails before it.

### Blockers for publication

- [x] **F1 (Critical) Large JIT frames overflow the goroutine stack.**
  Fixed: Bind caps goroutine frames at 640/240 bytes and foreign frames at 4096; examples adjusted; TestSecurityGoroutineStackBudget.
  `Code.Call` and `execution.Func` run unmanaged `StackPlatform` code on the
  caller's goroutine stack through NOSPLIT trampolines, limited only by the
  caller-supplied `StackBytesAvailable` (`compile/emitter_control.go:747`).
  Frames over ~800 bytes corrupt neighbouring goroutine stacks or heap.
  - Cap unmanaged frames at the NOSPLIT-safe limit, or run them on a guarded
    stack / system stack, or grow the goroutine stack before entry.
  - Apply the same rule to `execution.Func` (`backend/execution/func.go:57`).
  - Review the 8 KiB / 64 KiB budgets in `examples/{aggregates,dctlogo,snake}`.
  - Check thread-stack guard probing for frames over 4 KiB on the foreign path.
  - PoC: `poc/exec-audit/stack`, `poc/compile-audit/stack`, `poc/enc2-audit` (`FRESHG=1`).
- [x] **F2 (High) arm64 spill offsets overflow imm12; `STR` becomes `LDR`.**
  Fixed: shared compiler capacity subtracts LR/homes and entry saves; encoder errors at invalid offsets; TestSecuritySpillOffsetBoundaries / TestSecurityARM64SpillCapacity.
  - `compile/compiler.go:848`: subtract `1 + goHomes`, not just 1, from `slotLimit`.
  - Check export slots (`compiler.go:3096`) and entry-save `endSlots`
    (`emitter_control.go:723-747`, `emitter.go:331`) against that limit.
  - `backend/target/arm64/byte.go:222-249` (`Spill`, `Reload`, `SaveCFloat`,
    `RestoreCFloat`): reject slots outside 0..4095 or fall back to an X16
    sequence like `floatMemoryPlan`.
  - PoC: `poc/enc-audit/spill`, `poc/compile-audit/slotovf`.
- [x] **F3 (High) Aggregate parameter types are not checked at call time.**
  Corrected by A1: Func checks exact process-local entry types; raw Call/CallPair use the caller type/layout contract and Words retains roots; TestSecurityAggregateTypeMismatch / TestSecurityTypedAggregateCall.
  Record each entry parameter's `reflect.Type` in `CodeOptions`
  for `checkFuncType` (`backend/execution/func.go`). `Words` validates transferable
  shapes and nil aggregate roots; raw `Call`/`CallPair` do not check entry types.
  PoC: `poc/exec-audit/typepoc`.
- [x] **F4 (High) arm64 managed frames overwrite the caller's saved FP.**
  Fixed: reserve caller FP extrasize word; TestSecuritySavedFramePointer / TestSecurityFramePointerProfiling.
  Reserve the top frame word (`fp-8`) in `compile/backend_arm64_target.go:34-41`
  / `backend/target/arm64/layout.go`; add a test that the caller's `-8(RSP)` is
  unchanged across a managed call, and a stress test under `runtime/trace`
  and block/mutex profiling. PoC: `poc/goreg-audit/fpclobber`, `fptrace`.
- [x] **F5 (High, DoS) Predicate DAGs expand exponentially in lowering.**
  Fixed bounded-work mitigation: program-wide 16384 expanded nodes; TestSecurityProgramPredicateBudget. Shared-node lowering remains a future optimization.
  Lower shared predicate nodes once; until then, enforce a program-wide
  expanded-node budget far below `1<<20` (`ir/predicate_limits.go:11`).
  PoC: `poc/ir-audit/cmd/predb`, `cmd/pred` (run with memory limits).
- [x] **Hygiene: add `.gitignore`** covering `.agents-workspace/`,
  Fixed: artifact ignores and ignore regression script; compile.test absent.
  `e2e/.build/`, `*.test`, and delete the stray `compile.test` at the repo root.

### Should fix before publication

- [x] **F6 Tighten `ir.Validate` for raw programs.** Every opcode and
  Fixed named audit gaps and added native mutation fuzz seeds; FuzzSecurityRawIR.
  terminator must reject fields it does not use; check structural ranges before
  semantic passes. Specific gaps:
  - [x] F6a: Var `Declaration` out of range panics inside `Validate` (`ir/variable.go:71`).
  Fixed: structural bounds precede Var indexing; FuzzSecurityRawIR/seed#5.
  - [x] F6b: `OpScopeLeave.Args` unvalidated, panics in `backend/machine/select.go:1137`.
  Fixed: ScopeLeave.Args participate in input and lexical validation; FuzzSecurityRawIR/seed#6.
  - [x] F6c: `Other`/`OtherArgs`/`Predicate` accepted on Jump/Return steps
  Fixed: reject unused predicate/other-edge fields on non-branches; FuzzSecurityRawIR/seed#7.
    (`ir/program.go` ~445); panics in `compile/emitter_control.go:584`.
  - [x] F6d: `OpLabelParam`/`EntryReg` unvalidated outside label context
  Fixed: LabelParam requires valid block context, index/type and unassigned EntryReg; FuzzSecurityRawIR/seed#8.
    (`ir/program.go:377`); leaks register contents. Make it compiler-internal.
  - [x] F6e: `OpConst.Imm` not checked against `Bits`; `Bits` not checked on
  Fixed: constant/load/sign-extension widths validated, symbol constants masked; FuzzSecurityRawIR/seed#9.
    `OpLoad64`/`OpLoad8`; `bindSymbols` (`compile/compile.go:203`) writes an
    unmasked `Imm`. Causes miscompiles.
  - [x] F6f: ScopeEnter/ScopeLeave `Type` unvalidated (`ir/program.go:130`);
  Fixed: scope markers require their canonical type/field shape; FuzzSecurityRawIR/seed#10.
    panics on loong64 at `compile/compiler.go:1316`.
  - [x] Port the raw-IR mutation fuzzer (`poc/ir-audit/cmd/fuzz`) into the repo
  Fixed: FuzzSecurityRawIR ports bounded mutators and all six audit seed categories.
    as a native Go fuzz target.
- [x] **F7 Mark `Code.Call`/`CallPair` `//go:uintptrescapes`** so pointer
  Corrected by A1: Call(Words(&local)...) moves targets to the heap and retains roots across callbacks, including interface calls; raw methods accept every entry kind; TestSecurityCallPointerEscapes / TestSecurityManagedCallsAcceptWords.
  arguments escape and stay alive across Go callees; document that stack
  addresses must never reach managed code (`backend/execution/code.go:140`,
  `backend/goregister/register.go:33`). PoC: `poc/goreg-audit/stackptr`.
- [x] **F8 Bound managed registration cost.** Pool moduledata/code arenas or
  Fixed: 4096 permanent-registration budget, count/error API and lifetime docs; TestSecurityRegistrationBudget.
  batch functions per module; expose or enforce a registration budget; document
  that managed `Bind` never frees (`backend/goregister/register_linux_abi64.go:344,415`).
  PoC: `poc/goreg-audit/maps`.
- [x] **F9 Write a "Safety / trust model" section in README.md.** State that
  Fixed: README Safety / trust model describes checked boundaries and unsafe contracts.
  building or calling a program is equivalent to `unsafe`; list the unchecked
  APIs (`Load*`/`Store*`, `Data.Addr`, `NewCode`/`Map`, relocation bindings,
  `EntryAddress`, `Func`, `RuntimeEntry`, `CallEffect` clobbers) and the ones
  that carry checks.
- [x] **F10 `machine.RelaxBranches` called twice zeroes a Workspace stream**
  Fixed: swap layout scratch storage away from the live stream; TestSecurityRepeatedLayoutPasses.
  (`backend/machine/branches.go:222-266`, `backend/machine/work.go:170`). Stop aliasing
  `work.relax` with the stream's storage; audit other `ReplaceKeepingFacts`
  hand-offs. PoC: `poc/enc-audit/relax`.
- [x] **F11 `Data.Index` bounds checks are advisory.** `Data.Index` and
  `Builder.IndexedAddress` return the element address together with a bounds
  predicate. The element is usable in the current block, before any branch
  on that predicate (`builder/data.go:330` sets `blockStart` to the current
  block). A front end that drops the predicate (`elem, _ := s.Index(i)`)
  gets an unchecked out-of-bounds access, and `Finish` accepts it with no
  error. This is the same failure class as JIT bounds-check-elimination
  bugs, here caused by API misuse. Downstream compilers of end-user input
  rely on exactly this "safe-looking" API.
  Fixed: required out-of-bounds label emits the branch; indexed Data expires at calls/syscalls; TestCheckedIndexRequiresFailureEdge / TestIndexedDataExpiresAtCall / TestIndexedAddressBranchesBeforeLoad.
  - Make an unchecked element unusable until it has been passed through
    the in-bounds edge of a branch on its own predicate, or replace the pair
    with a checked form, e.g. `Index(i, outOfBounds Label)` that emits the
    branch itself. Unpublished, so change the API outright.
  - Reject at `Finish` (or at use) any access through an index-derived
    element that is not dominated by its bounds branch. Apply the same rule
    to `IndexedAddress`.
  - Consider Spectre-v1 hardening on the in-bounds edge (clamp or mask the
    index with a conditional select), at least behind an option, for hosts
    that rely on Data views to isolate tenants.
  - Related, documented (README.md:334, `backend/execution/func.go:42`): a callback
    that replaces a slice (any reallocating `append`) leaves element
    addresses held by generated code pointing at a backing array the GC may
    free. Consider making index-derived elements expire at any call or
    callback, forcing a fresh `Index`, as browser engines re-check lengths
    after side-effecting operations.
  - Check: `poc/data-index` (`Finish error ...: <nil>` today; must fail).
- [x] **F12 Backend regression tests shaped after real JIT CVEs.** See
  `threat-model.md` (class 4). Run on every target that can execute here
  (native arm64, qemu for the others), comparing against a Go reference:
  Fixed tests: TestCheckedIndexCompareImmediateEdges / TestCheckedIndexDirtyUpper32Bits / TestCheckedIndexNarrowSpillReload / TestCheckedDataIndexMemoryBoundaries; Go-reference execution on all six targets.
  - Bounds checks (`Data.Index`, `IndexedAddress`, compare-immediate
    branches) against constant counts at immediate-range edges: 4095/4096,
    2^31−1/2^31, 2^32−1/2^32, for every unsigned and signed condition. This
    is the CVE-2026-53036 shape: an off-by-one in an immediate range check
    flips a comparison or branch.
  - 32-bit indexes whose register upper bits are dirty, for example the
    result of a wrapping 32-bit add. Assert that the bounds compare and the
    element address both use the same zero-extended value. This is the
    CVE-2023-26489 / CVE-2026-34971 shape: address and check computed at
    different widths, or an addressing-mode fold that skips the
    extension.
  - Narrow (8/16/32-bit) values spilled and reloaded under register
    pressure, then widened or used as indexes. Assert that the extension
    survives the spill. This is the CVE-2021-32629 shape: a reload that
    sign-extends a value the selector assumed was zero-extended.
- [x] **F13 Extend the trust-model section (README.md, from F9) with embedder
  guidance** for hosts that compile end-user input:
  Fixed documentation: README covers NaN canonicalization, callback slice lifetimes, host-controlled options/contracts and emitted execution limits.
  - NaN payload bits are not canonical across targets
    (`docs/simd-design.md:144`). NaN-boxing engines must canonicalize NaNs
    themselves before treating float bits as tagged values.
  - Callbacks must not reallocate or replace slices whose elements generated
    code is using (any growing `append` counts). Cross-reference F11.
  - `compile.TargetSpec`/`compile.Func`, symbol bindings and `CallEffect` descriptions must never
    be derived from end-user input. Only IR built through the Builder (and
    Data views, once F11 lands) carries checks.
  - JIT loops cannot be preempted (L7). Hosts need their own execution
    limits, for example an iteration counter emitted into loops by the front
    end, since a Go context or timeout cannot interrupt generated code.

### Follow-ups from fix verification (2026-10-18)

Adversarial verification of the F1–F13 fixes found the gaps below. Details are
in the "Fix verification" section of the journal; reproducers are under
`poc/verify-call/` and `poc/verify-compile/` (separate modules, `replace` to
the repo). V1 and V2 are blockers for publication.

- [x] **V1 (Blocker, High) F1 bypass: chained generated calls exceed the stack
  cap.** The 640-byte cap is per entry (`backend/execution/code.go` entry check,
  `backend/goregister/register_linux_abi64.go` frame check). Generated code can call
  generated code through `Code.EntryAddress` + `CallGo` (as
  `examples/snake_termbox` does), and nothing bounds the chain.
  Fixed: callable CallGo checks fixed ordinary Go entries and rejects known trampolines; splittable host-target contract documented; TestGeneratedGoCallNestingRejected / TestGeneratedGoCallRecursionRejected / TestGoCallTrampolinesRejected; full six-target Go matrices green.
  - Two nested frames that each pass (640 + 576 bytes) both bind:
    `poc/verify-call` `TestF1NestedChainAccepted`.
  - A self-recursive managed function (464-byte frame) on a fresh goroutine:
    depth 300 → `fatal error: unexpected signal during runtime execution`;
    depth 1000 → `fatal error: limiterEvent.stop: found wrong event in p's
    limiter event slot` (runtime P state corrupted): `poc/verify-call/cmd/chain`.
  - A foreign entry (4096 cap) reached by `CallGo` would also run on the
    goroutine stack.
  - `execution.Call`, `CallWithArity`, `CallPairWithArity`
    (`backend/execution/execution.go`) on an `EntryAddress` skip every frame, managed
    and aggregate-type check, and their Godoc does not say they are trusted.
  - Fix options: emit a stack-guard compare (against `g.stackguard0`, failing
    or growing) in every generated prologue that can run on a goroutine stack;
    or forbid `CallGo` targets that are generated entries; at minimum, mark the
    raw entry-address call paths as trusted in Godoc and README. The
    regression test must cover nesting and recursion from a fresh goroutine.
- [x] **V2 (Blocker, High) F11 bypass: view expiry is checked in program order,
  not along control flow.** `dataEpoch` is bumped when a call/syscall is
  appended (`builder/builder.go`, `append`), and `Data.valid`/`Rebase`
  (`builder/data.go`) compare epochs, which is linear program order.
  Fixed: indexed views/addresses and arithmetic aliases are block-local borrows; control edges and Vars reject them; TestIndexedViewLoopBackEdgeRejected / TestIndexedViewLoopReindexesAfterCallback.
  - Pattern: `Index` → `JumpArgs(loop, elem.Base())` → loop head
    `elem.Rebase(p).Load()` → `CallGo(cb)` → back-edge carrying `p`. Passes
    `Finish` and `CompileGo`.
  - Run: the callback replaced `Holder.Items`; the result was 14 (old backing
    read twice), not 1006. Once the old array is unreachable this is a GC
    use-after-free. `poc/verify-call` `TestF11ExpiryLoopBackEdge`,
    `TestF11ExpiryLoopBackEdgeRun`.
  - Fix: make expiry a control-flow property, e.g. a `Finish`-time data-flow
    pass over the CFG rejecting any use of an index-derived base reachable from
    a call or syscall without an intervening `Index`; or forbid carrying
    indexed views across block boundaries (BranchArgs/JumpArgs) and require a
    fresh `Index` in the target block. Cover `IndexedAddress` too.
- [x] **V3 (Should fix, Medium) Typed nil roots pass the aggregate checks.**
  `CallValues((*Big)(nil), i)` matches the exact type, `Words` turns nil into 0,
  and `PointerParam` adds no nil check, so a checked in-bounds `Index` on a
  large array (or `Field` beyond 64 KiB) reads absolute low addresses.
  Corrected by A1: Words rejects nil aggregate roots and Func pointer signatures validate in a Go wrapper; raw Call/CallPair deliberately bypass type/nil checks; TestNilAggregatePointerRejected / TestWordsRejectsNilAggregatePointers.
  Original reproducer: `poc/verify-call` `TestF11NilPointerParam` (read a Go
  global in a non-PIE binary). A1 resolution: reject nil aggregate roots in
  `Words`/`Func`; raw `Call`/`CallPair` do not validate pointers.
- [x] **V4 (Should fix, Medium) F10 bypass: a failed `RelaxBranches` re-aliases
  the scratch buffer.** In `backend/machine/branches.go`, `defer func(){ work.kept =
  kept }()` in `CompactBranches` undoes the later `work.kept = previous[:0]`, so
  scratch stays aliased with the stream; an early error return in
  `RelaxBranches` leaves the full-length `work.relax = insns` in place, and the
  next `workAppend` `clear()`s the live stream (`backend/machine/work.go`). Sequence
  Fixed: scratch ownership is returned on every exit and swapped only after commit; TestSecurityRepeatedLayoutPasses now interleaves failing Relax/Compact calls.
  Compact, Relax, Compact, Relax, failing Relax (labels `[]int{12345}`), Relax:
  the last call returns nil with every instruction zeroed (expected ops
  `0 37 0 33`, got `0 0 0 0`). Reproducer: `poc/verify-compile/relax`, argument
  `CRCRXR`. Fix: un-alias on every exit path, including errors; remove the
  conflicting defer; extend `TestSecurityRepeatedLayoutPasses` with failing
  calls interleaved.
- [x] **V5 (Should fix, Low–Medium) L2 incomplete: public encoders still panic,
  and one hangs.** Reachable through `ByteAssembler` / `machine.Visit`, not
  from IR. Reproducers: `poc/verify-compile/visitfuzz`, `rot`.
  Fixed: width guards, bounded FP plans and sticky operand errors, including i386 register/condition checks; TestI386InvalidOperands / TestEncoderInvalidOperandTable; FuzzEncoderVisit now covers all six backends; full six-target Go matrices green.
  - Infinite loop: rotate with `Bits=0` in `backend/target/arm64/shift_by.go` and
    `backend/target/riscv64/shift_by.go` (`for s := width; s < 32; s *= 2`), also hit
    by `PhysicalSize`; ends in a fatal out-of-memory error. Reject width 0.
  - Panics: arm64 `SelectFlags` with condition > 11 (index out of range);
    arm64/riscv64/loong64 `FloatBinary` `panic("invalid floating operation")`;
    arm64 `FloatSpill` with a huge slot overflowing `parts[12]`; amd64
    registers > 15 indexing 16-entry name tables (BinaryCarry, MulWide, Float*,
    ShiftBy, MulHigh, CarryBit, WordCarry, CountZeros, FloatBits).
  - Turn them into sticky errors, and keep a bounded `machine.Visit` fuzz test
    in the repo that must never panic or hang.
- [x] **V6 (Should fix) Close the F12 coverage gaps.**
  Fixed tests: TestCompareImmediateRegisterDifferential covers 18144 immediate/register/Go-reference cases; TestArithmetic32SpillReloadIndex confirms the indexed arithmetic result spills/reloads and executes both bounds edges.
  - Negative immediates (arm64 CMN path, imm32 at −2^31−1) and narrow-width
    (8/16/32) compares against immediates.
  - `TestCheckedIndexNarrowSpillReload`: the dirty high bits live in memory and
    are never loaded. Add a case where a 32-bit arithmetic result (e.g. riscv
    `addw`, which sign-extends) is spilled, reloaded and used as an index;
    assert that the value is actually spilled; take the failure edge too.
  - A native differential of immediate versus register compares (widths
    64/32/16/8, 29 immediates including negatives, 18,144 cases) found no
    mismatches (`poc/verify-compile/cmpdiff`); port a bounded version into the
    repo as a regression test.

### API correction to the F3/F7 fixes (2026-10-18)

- [x] **A1 (Blocker) One raw call boundary on `Code`; remove `CallValues`,
  `CallPairValues` and `CallInto`.** The F3/F7 fixes added a second family of
  call methods and made `Call`/`CallPair` reject managed and aggregate-typed
  entries (`backend/execution/code.go:213-221`). A caller then has to pick a method
  based on how the code was compiled, and a wrong pick only fails at run
  time. The intended boundary was the existing `execution.Words`.
  Fixed: removed the three methods; raw Call/CallPair accept all entry kinds; Words retains roots, Func checks signatures; escape/mutation checks and full six-target Go matrices pass.
  - `Code.Call` and `Code.CallPair` are always raw escape hatches. They accept
    every entry kind and never reject by entry kind or parameter type.
  - Delete `CallValues`/`CallPairValues` (`backend/execution/call_values.go`) and
    `CallInto` outright, and move their callers to
    `c.Call(execution.Words(...)...)`. `CallInto` is redundant: Go heap
    objects do not move, `Words` moves stack values to the heap and keeps its
    roots alive, and `Call` keeps the `Words` frame alive. Call sites include
    `backend/cpuprofile/x86.go:29` (`Words(&data, leaf, 0)`),
    `examples/dctlogo/main.go:225-231` (drop the `Pinner`),
    `examples/snake_termbox`, the README, `docs/virtual-register-design.md`,
    `.agents/skills/nyanjit-security-review/references/checklists.md`, and the
    tests.
  - F7 stays fixed through `Words`: `-gcflags=-m` shows `Words(&local)` moves
    `local` to the heap. Keep `Words`' scalar-pointer and nil-aggregate-root
    checks (V3). Hand-built `uintptr` addresses remain the caller's
    responsibility.
  - F3: `Code.Func` keeps its signature check (A2 replaces the generic shape). For `Call`, matching aggregate
    types becomes a documented caller contract. Ask the user before adding any
    checked dynamic path.
  - Update the README trust model, the Godoc, and `report.txt` (F3/F7
    entries). Rewrite the regression tests `TestSecurityCallPointerEscapes`,
    `TestSecurityManagedCallsRequireValues` and
    `TestSecurityTypedAggregateCall` so they use `Call(Words(...)...)`. The
    stack-move test must fail if `Words` stops moving stack values to the
    heap.
  - Add no new public methods or functions without asking the user.
- [x] **A2 Move `execution.Func[F]` onto `Code` as `Code.Func(fptr any) error`.**
  Fixed: Code.Func validates the destination and signature before assignment; generic Func is deleted, all 24 callers/docs are migrated, native/race/vet and both-version six-target matrices pass, and all 15,605 listings are identical.
  Go methods cannot have type parameters, so the caller passes a pointer to
  its own function variable, as `purego.RegisterFunc` does:
  `var sum func(a, b uint64) uint64; err := code.Func(&sum)`. The user
  approved this method and its name (2026-10-18).
  - `fptr` must be a non-nil pointer to a func-typed variable; anything else
    returns `ErrSignature` (or `ErrArgumentType`, whichever fits the existing
    errors better). On error, leave `*fptr` unchanged.
  - Everything else stays as in `Func[F]` (`backend/execution/func.go:43`): the
    same `checkFuncType` signature check, Go ABI / no workspace / one result
    word restrictions, the checked wrapper for pointer signatures with nil
    aggregate rejection, and permanent quarantine of the mapping once a
    function value exists.
  - Delete `execution.Func[F]` outright (no wrapper) and move its call sites
    (about 22), the README, Godoc and docs to `code.Func(&f)`.
  - Tests: the existing `Func` signature, lifetime and nil-root tests move to
    the method. Add cases for a nil pointer, a non-pointer, a pointer to a
    non-func, and a mismatched signature leaving `*fptr` unchanged.
  - No other new public API.

### Hardening (after publication is fine)

- [x] L1: make encoders reject out-of-range fields instead of masking (arm64,
  amd64, riscv64, loong64), following ppc64 `Encode`.
  Fixed: byte-encoder register banks, opcode/width/condition fields, vector lanes, slots, PCs and immediate narrowing are checked; invalid plans report zero size. Native-width x86 extension copies now agree with their size plans. Tests: TestEncoderFieldsRejectBeforeEncoding, TestEncoderDirectFieldsAndState, TestInvalidEncoderPlansHaveZeroSize, TestCPUSizePlansRejectInvalidFields, TestExtensionWidthsAndNativeCopy.
- [x] L2: return errors instead of panicking in arm64 `BinaryImmediate` and
  Fixed: sticky encoding errors returned by machine.Visit; TestSecurityEncodingRanges / TestImmediateOutOfRangeRejected.
  loong64 `ri`.
- [x] L3: replace the RWMutex in `Code.Call`/`Close` with an in-flight counter
  Fixed: in-flight counter and deferred unmap; TestSecurityCloseAndReenterFromCallback.
  and closed flag, so `Close` from a callback cannot deadlock.
- [x] L4: keep `Code` alive from `Func` values, or quarantine freed ranges.
  Fixed: Func retains Code and permanently quarantines its mapping; TestSecurityFuncSurvivesClose.
- [x] L5: validate public `NewCode` arity against `abi.MaxArgumentsFor`
  Fixed: public constructor uses target entry arity limit; TestSecurityNewCodeArity.
  (amd64 trampolines pass only 6 arguments).
- [x] L6: reject zero-address symbol bindings in `backend/machine/link.go:60` and
  Fixed: zero symbol/foreign addresses reject before native entry; TestSecurityForeignZeroAddress (symbol coverage also in paired/relocation tests).
  `foreign_words_linux.go`.
- [x] L7: document that JIT loops without calls cannot be preempted; consider
  Fixed documentation: unbounded JIT loops are not preemptible; polling remains optional future work.
  a back-edge stackguard poll.
- [x] L8: document or guard the race between registration and `plugin.Open`.
  Fixed documentation: applications must serialize registration and plugin.Open.
- [x] L9: decouple `compile` from `goregister` (build tag or optional package)
  Fixed: nyanjit_no_goregister build tag excludes private runtime linknames; TestRegistrationDisabled. Existing anchor-layout checks retained.
  so a future Go linkname lockdown does not break non-managed users; consider
  validating moduledata layout beyond the version string.
- [x] L10: comment the icache-coherence assumptions on loong64 and arm64.
  Fixed: ARM64 and LoongArch publication/cache coherence assumptions documented beside mapping code.
- [ ] L11: replace the pinned purego pseudo-version with a tagged release
  after verifying PPC64LE callback and nonvolatile-vector preservation.
  The `termbox-go` module split is complete: snake_termbox has a separate
  module with CI, vet and cross/E2E discovery. The original audit found that
  v0.11.2 failed the V20 callback probe while the pinned revision passed.
  Evidence: remaining-work/purego-vector-{stable,current}.log.
- [x] Consider digest-pinning the Docker base images in `e2e/container/Dockerfile`.
  Fixed: every external FROM image uses a verified multi-platform manifest digest; the workflow binfmt image matches. TestExternalBaseImagesUseDigests rejects mutable bases. Tests still use cached images; the complete multi-OS image was not rebuilt.
- [x] L12: complete control-flow hardening support on riscv64, loong64 and
  ppc64le after verifying each target's speculation-protection contract.
  Fixed: `machine.OpBarrier` names a guarantee (speculation, memory, instruction)
  and each target `Policy.Barrier` reports an architectural, platform or
  unavailable contract. ppc64le hardens control flow with `ori 31,31,0` (Linux's
  `barrier_nospec`, platform: needs updated firmware) and an ISEL clamp, borrowing
  r3-r5. riscv64 and loong64 keep `ErrUnsupported`: RISC-V has no ratified
  speculation barrier (the kernel's FENCE.I use is empirical) and LoongArch has
  none (Loongson claims built-in mitigation). TestBarrierContractsAndEncodings,
  TestHardenControlBarrierPerTarget; hardening tests pass under QEMU on POWER8/9/10,
  amd64, 386, riscv64 and loong64. See [optional hardening](docs/optional-hardening-design.md).

## Encoder findings from the target forms audit (2026-10-23)

Writing `backend/target/*/FORMS.md` checked every documented form against the
byte assemblers and size functions. The documented rows all agree. These
forms were left out of the tables because the encoder or size function is
wrong for them. Except where noted, compile does not produce them; they
matter for other front ends using the encoder API.

- [x] ppc64 `OpMulHigh` with `Bits=32` emits `mulhwu`, whose upper 32 result
  Fixed: compile keeps narrow values zero-extended, so `mulhwu` is followed by `clrldi`; TestUnsignedHighMultiplyEncodingsAndEffects.
  bits are undefined in 64-bit mode; 8- and 16-bit forms zero-extend.
  `Policy.CanMulHigh(32)` is true, so compile can select it. Check whether a
  32-bit high product can reach a 64-bit use, and zero-extend if so.
- [x] `OpMulWide` (a 386 opcode) is accepted elsewhere because `machine.Visit`
  Fixed: Visit dispatches to the 386 `MulWide` method and returns ErrUnsupportedOperation elsewhere; arm64/riscv64 sizes are 0; TestMulWideIsI386Only.
  rewrites it to a 32-bit `OpMulHigh` with A=2, B=0: the size functions
  disagree on amd64 (0 vs 11 bytes), arm64 and riscv64 (4 vs 8), and on
  loong64 it overwrites r2, the thread pointer. Reject it outside 386.
- [x] Size function disagrees with the encoder: amd64 `OpBinaryImmediate`
  Fixed: amd64 sizes follow `CanBinaryImmediate`, riscv64 ignores Far without Long (size 4, no x30 write), ppc64 sizes only whole words; TestSizeOfRejectedForms.
  with `Bits=0` (size 4, rejected); riscv64 `OpJumpIf`/`OpBranchZero` with
  `Far` but not `Long` (size 12, emits 4); ppc64 `OpLiteralData` whose
  length is not a multiple of 4 (size reports it, `Data` rejects it).
- [x] Missing operand validation:
  Fixed: each item below is rejected by the encoder (loong64 zero scale is computed as B); new FORMS.md rejection rows, TestZeroScaleIndexedMemory.
  - amd64 and 386 `OpScaled` accept any scale (encoded as 1), an SP index
    (encoded as no index) and, on amd64, a displacement above 32 bits
    (truncated).
  - 386 truncates 64-bit `OpConst` immediates and `OpLoadOffset`
    displacements, accepts `OpFrameEnter` below 16 bytes, encodes
    unsupported `Arithmetic` values as `add`, ignores `Bits=64`, and accepts
    GP numbers as XMM registers in float operations.
  - loong64 `OpLoadAddress`/`OpStoreAddress` with `Imm=0, Disp=0` access
    B+C instead of B.
  - arm64 accepts frame sizes that are not 16-byte aligned.
  - ppc64 `OpFloatUnary` emits `fsqrt` for any `Arithmetic` except
    `OpFConvert`, and `OpFloatBits` emits `fneg` for any except `OpAnd`.
  - riscv64 accepts `OpWordCarry` with `CarryFlags` (treated as a value) and
    `OpLiteralLoad` with `Bits=128` (loads 64 bits).
- [x] Wrong results with a GP destination: amd64 and 386 `OpFloatBits`
  Fixed: the destination must be an XMM register other than XMM0; Visit rejects `OpInc` with PreserveFlags unless the target has `IncrementNoFlags`.
  compute in XMM0 and overwrite the mask (compile always uses an XMM
  destination). amd64 `OpInc` ignores `PreserveFlags`.
- [x] `execution.TestCallRacesClose` occasionally hangs under QEMU riscv64.
  Explained, not a defect in `Code`: a reproducer with no nyanjit code (the
  same atomic count with a closed bit and the same mutex handoff) hangs the
  same way. Under qemu-riscv64 10.2.3 on this aarch64 host it hung in 71 of
  100 runs pinned to CPUs 13–16 and 1 of 100 on CPUs 10–12, and 0 of 60
  with `GOMAXPROCS=1`; the test itself hung 23 of 211 and 1 of 300 on the
  same CPU sets. Every dump shows a goroutine parked in `sync.Mutex.Lock`
  with no holder, so the wakeup is lost in emulation. Never seen natively.
  Reproducer, loop script and a dump:
  `.agents-workspace/tmp/qemu-riscv64-mutex-hang-20261024/`.
  Also seen once on QEMU loong64 (la464 without LSX) on 2026-10-23 with
  the same dump (its scratchpad evidence was lost in a host reboot).

## Compile-time scaling found in review (2026-10-24)

- [x] `machine.Stream.AliasFlagValue` and `AliasSignExtendedValue` prepend
  each alias to a copy of the list, so a chain of n identity copies or sign
  extensions costs O(n²) time and allocation. Fixed: the stream keeps alias
  lists oldest first and appends (also in `AliasCarryValue` and the
  sign-extension zero alias in `BindFlagValue`); `Stream.Flags()`, which
  already copied, reverses into the unchanged newest-first order. A chain of
  8,192 went from 24,600 to 73 allocations (`TestFlagAliasChainsAppend`);
  the `sign-aliases` workload from 0.065 to 0.0034 allocations per
  instruction.
- [x] Lowering one long block grows faster than linearly: a block of 8,000
  to 64,000 sign extensions goes from about 775 to 855 ns and from 1,700 to
  2,750 bytes per instruction (about 2.1x per doubling).
  Explained, not an algorithmic defect: at 64,000 instructions the stream's
  instruction buffer exceeds the workspace retention budget, so each compile
  regrows it (amortized linear; `Stream.Append` allocates 4.7 MB per 10
  compiles at 8,000 and 335 MB at 64,000), and the rest is cache effects.
  The benchmark-only `sign-aliases` workload in
  `compile/perf_sign_alias_test.go` shows it and stays out of the scaling
  gate because of it.
