# Per-surface checklists

Each section lists the scope, the invariants to check, the bugs the first
audit found there (regression anchors, by `TODO.md` ID), and techniques that
worked. Line numbers drift; search by function name.

## 1. Execution and the call boundary

**Scope:** `backend/execution/` (all `.go` and `.s`), `cell/`, `foreign/`, and the
`compile` → `Template.Bind` → `newCode` hand-off.

**Invariants**
- Mappings are RW, then copied, relocated and icache-flushed, then sealed RX.
  Never RWX, never written after sealing; every error path unmaps.
  Zero-length code is rejected. `mapCode` and `Arena` share `fillCode`; each
  arch's `makeExecutable` owns the icache flush and the RX seal.
- `execution.Arena` pages: free pages are `PROT_NONE` and `MADV_DONTNEED`;
  a page returns to the free list only after its last call has returned
  (the `Code` refcount); registered Go-managed and `Func`-exposed code never
  returns its pages; the range is unmapped only when closed and empty.
- arm64 trampolines run the callee 16 bytes below SP so an AAPCS64 push
  cannot overwrite Go's saved R29, repeat the saved LR at the new SP for the
  unwinder, and declare `NO_LOCAL_POINTERS`.
- Relocations are linked on a copy, bounds- and template-checked
  (`backend/machine/link.go`); zero-address symbols are rejected.
- **JIT frames must fit the stack they run on.** Trampolines are NOSPLIT on
  the goroutine stack, which has no guard page and guarantees only about
  800 bytes. Every entry path (`Code.Call`, `CallPair`,
  `Code.Func`) must bound the frame, or switch stacks or grow the stack
  first. Thread-stack (foreign) frames over 4 KiB need guard-page probing.
- Arity is checked against `abi.MaxArgumentsFor`, and trampolines pass
  exactly that many registers.
- Aggregate parameters (`PointerParam`/`ValueParam`/`SliceParam`) have exact
  signature checks in `Code.Func`. Raw `Code.Call`/`CallPair` accept every entry
  kind; matching aggregate types/layouts is the caller contract. `Words`
  validates transferable shapes and nil aggregate roots, not entry signatures.
- `Close` vs concurrent `Call`: no unmap while in flight, no deadlock when
  called from a callback, `Func` values cannot outlive the mapping.
- `uintptr` arguments: `Call(Words(&local)...)` moves pointer targets to the
  heap and retains the original argument frame through callbacks/stack growth.
  Do not rely on `//go:uintptrescapes` through an interface call or copy words
  and discard their owning frame. Hand-built addresses remain trusted.
- g / zero / TOC registers are preserved (arm64 R28, amd64 R14 and X15,
  ppc64le TOC); callee-saved registers are restored on every exit path.

**Regression anchors:** F1 (unbounded leaf frames), F3 (Func signature and raw caller type contract), F7 (stale stack addresses), L3 (Close deadlock), L4 (Func
outliving Close), L5 (amd64 arity), L6 (zero symbols).

**Techniques**
- Call from a **fresh goroutine**: the main goroutine's large stack hides
  frame overflows. Put canaries in other goroutines' stacks or neighbouring
  heap objects and check them after the call.
- Build programs with many simultaneously live values to force large
  frames, with `StackPlatform` and a large `Func.Stack.BytesAvailable`.
- For type checks: build with one `T` and request a mismatched `Code.Func`
  signature; it must reject. Raw `Call` matching is a caller contract. Test
  raw entry-kind/type acceptance with return-only fixtures, not wrong-sized
  native stores. Verify `Call(Words(&local)...)` through an interface and a
  growing callback stack, and make a root-less Words mutation fail safely.

## 2. Go runtime integration

**Scope:** `backend/goregister/`, the managed-call path in `compile/`
(`compile.ContextGoManaged`), `machine.Layout` (PC-to-instruction maps), every `//go:linkname`.

**Invariants**
- Unknown Go versions fail closed (`layoutForVersion`,
  `validRuntimeAnchor`). Re-diff the mirrored `moduledata`/`_func`/`pcHeader`
  layouts against the runtime source of every supported Go version.
- The library still links with default flags (`-checklinkname`) on every
  supported Go version. Non-managed users must not depend on `goregister`
  linking.
- Synthesized pcsp/pcfile/pcln tables cover exactly `[0,len)` and match the
  emitted prologue/epilogue for every frame form.
- **arm64: the top word of each frame (`fp-8`) belongs to the caller's saved
  frame pointer.** Spills and Go-arg homes must stay below it.
- Pointer-free requirement: frames have no stack maps. Check what the API
  enforces and what it only documents.
- Registration cost and lifetime: VMAs per Bind, whether anything is ever
  freed, cost of the module list for tracebacks.
- Interaction with `plugin.Open`, async preemption, SIGPROF tracebacks,
  panic/recover through JIT frames.

**Regression anchors:** F4 (arm64 saved FP), F8 (unbounded registration),
L7 (no preemption), L8 (plugin race), L9 (linkname coupling).

**Techniques**
- An asm probe that reads the caller's saved FP before and after a managed
  call, across frame shapes (odd and even spill counts).
- Stress: several goroutines calling managed code while a GC loop runs,
  **with `runtime/trace` and block/mutex profiling enabled**. Those use
  frame-pointer unwinding; plain CPU pprof did not reveal F4.
- Run under `GODEBUG=gccheckmark=1,clobberfree=1` and `-race`.
- Count VMAs (`/proc/self/maps`) and time per Bind as Bind counts grow.

## 3. Primary encoders and the machine layer

**Scope:** `backend/machine/` (stream, branches, assembler, work, link),
selection and verification in `compile/select*.go`, `backend/target/amd64/`,
`backend/target/arm64/`.

**Invariants**
- Every integer packed into an instruction field is range-checked
  (error) by the encoder itself, not only by an upstream policy. Search for
  `&0x...`, `uint32(...)<<`, `int8(`, `int32(`.
- Slot and offset arithmetic: the compiler's slot limit accounts for every
  word added before encoding (LR, Go homes, entry saves, export slots).
- Assembled size equals planned size before every instruction
  (`compile/assembler.go`).
- Branch layout converges, and the final `ResolveBranches` re-verifies every
  branch.
- amd64: RSP/R12 base needs SIB, RBP/R13 needs disp8 0, byte ops on
  SPL/BPL/SIL/DIL need REX, SIB index 4 means "none".
- arm64: register 31 is SP in some instructions and XZR in others; scaled
  versus unscaled offsets; logical-immediate encodability.
- Public `machine` APIs are safe to call repeatedly (buffer aliasing between
  workspace scratch and stream storage).

**Regression anchors:** F2 (arm64 imm12 spill overflow, `STR` encoded as
`LDR`), F10 (second `RelaxBranches` zeroes a stream), L1 (masking encoders),
L2 (assembler panics), F12 (CVE-shaped regression tests).

**Techniques**
- Differential decoding with `golang.org/x/arch` (`x86asm`, `arm64asm`):
  exhaustive register pairs, displacement boundaries, sampled immediates;
  compare against the text listing.
- On arm64, execute natively against a Go reference evaluator.
- Drive spill counts across the encodable boundary (around 4086–4096 slots
  with and without Go homes) and decode every spill.

## 4. Experimental backends

**Scope:** `backend/target/i386/`, `backend/target/riscv64/`, `backend/target/loong64/`,
`backend/target/ppc64/`, `compile/backend_*_target.go`, `backend/execution/memory_linux_*.go`.

**Invariants**
- Immediate fields: RISC-V I/S 12-bit, B 13-bit, J 21-bit, LUI/AUIPC + ADDI
  with the +0x800 carry; LoongArch si12/si16/si20/si26 and PCADDU12I pairs;
  PPC64 D/DS (DS multiple of 4) and addis @ha carry; i386 disp8/disp32, ESP
  SIB, EBP mod00.
- Long-branch fallbacks are used, never wrapped; `BranchFits` matches every
  form.
- Instruction-cache sync: riscv64 `riscv_flush_icache` for all harts,
  ppc64le dcbst/sync/icbi/isync, loong64 IBAR (comment the coherence
  assumption).
- Large frames: SP adjustments and far spill offsets go through scratch
  registers.
- Unsupported operations fail closed with an error, never emit garbage.

**Regression anchors:** F1 also reproduced on all four (frame overflow),
L1 (riscv64/loong64 masking), L2 (loong64 `ri` panic), L10 (icache comments).

**Techniques**
- Differential fuzzers run under qemu-user: random ALU ops with boundary
  immediates, loads/stores at ±120 KB displacements, 5000-op programs that
  force big frames, random CFGs with block parameters and ~300 KB padding
  to force long branches.
- Link statically with a stub for purego so binaries run under qemu without
  a sysroot.

## 5. Compiler, stack and register safety

**Scope:** `compile/`, `backend/regalloc/`, `backend/abi/`.

**Invariants**
- Frame size is bounded and checked against what the execution context
  really provides (see surface 1), for straight-line and labeled code.
- Spill slots do not overlap, stay inside the frame, and are aligned for
  their type; frames are aligned at call sites.
- Callee-saved registers (integer and FP/vector) are preserved on every exit.
- `CallEffect` from the caller is validated structurally; clobber
  under-reporting is either rejected or documented as memory-unsafe.
- Caches (`call_cache.go`, `entry_cache.go`) are keyed by every input that
  affects their value, never mutate shared slices, and do not leak state
  between compilations.
- Narrow values keep their extension invariant through spills and moves.
- There is no integer division in the IR; re-check if one is ever added
  (amd64/386 trap on divide by zero and on INT_MIN/-1).

**Regression anchors:** F1, F2, F12.

**Techniques**
- An asm harness that loads sentinels into callee-saved registers, paints
  memory below SP, places the workspace against a PROT_NONE page with
  canaries, and calls random programs checked against a Go evaluator.
- `go test -race ./compile -run 'Concurrent|Cache|Isolation|Poison|Scratch'`.
- `FuzzScopedVars` in `compile/varfuzz` (60 s, `-parallel=2`).

## 6. IR boundary, Builder and Data views

**Scope:** root package, `builder/`, `ir/` (sealed programs, `Analysis`,
`SealOwned`, `Analyze`), where `compile` detaches and validates `ir.Input`,
and the borrowed sealed accessors the compiler reads.

**Invariants**
- `ir.Validate` never panics. Every opcode and terminator rejects fields it
  does not use; structural ranges are checked before semantic passes.
- Malformed raw IR is rejected, never compiled into code that breaks the
  compiler's own invariants (narrow `Imm` fits `Bits`; `OpLabelParam` and
  `EntryReg` are internal; scope markers carry no type).
- Compile cost is bounded across the whole program, not only per construct
  (predicate DAGs: shared nodes must not expand as trees).
- No deep recursion on user-controlled depth. A Go stack overflow is fatal.
- Sealed programs are immutable: no shared slice or pointer lets a caller
  change validated IR.
- **Cross-block values follow dominance.** Validation accepts a use from
  another block only when the defining block dominates it (unreachable
  blocks are dominated by every block). `compile` threads such values
  through internal label parameters before lowering and re-seals the result;
  unreachable blocks get zero values. Dominators and liveness use explicit
  stacks, never recursion. A program that validation accepts must not be
  rejected by lowering as invalid.
- **Data views are actually safe:** an element from `Data.Index` or
  `IndexedAddress` cannot be used unless its bounds branch guards it; bounds
  and address use the same value at the same width; elements do not survive
  calls or callbacks that might replace the slice.

**Regression anchors:** F5 (predicate DAG), F6a–f (validation gaps),
F11 (advisory bounds checks), F13 (embedder guidance).

**Techniques**
- A mutation fuzzer over `Clone()`s of valid programs that perturbs fields,
  opcodes, widths, labels, scopes and predicates, then runs `Validate`,
  `Lower` and `Listing` on all six architectures with a hang watchdog. Filter
  known classes and rerun to find new ones.
- Scaling curves: double the input and watch time and RSS. Deep expression
  chains, nested scopes, many labels, shared predicate subtrees.
- Builder-only API-misuse checks (`Finish` must fail): drop a bounds
  predicate, use an element across a call, mix values between blocks.

## 7. Repository and supply chain

Run `scripts/hygiene.sh`, then check:
- `.gitignore` covers `.agents-workspace/`, `e2e/.build/`, `*.test`; no stray
  binaries or large untracked trees would be published.
- No secrets, tokens, keys or home-directory paths in files to be published.
- CI: actions pinned to commit SHAs, `permissions: contents: read`, no
  `pull_request_target`, no untrusted-input interpolation into `run:`.
- `e2e/container`: downloaded media verified by hash/signature; base images
  tag- or digest-pinned; `--privileged` confined to ephemeral runners.
- `go.mod`: pre-release or pseudo-version dependencies (purego), and
  example-only dependencies in the main module (termbox-go).
