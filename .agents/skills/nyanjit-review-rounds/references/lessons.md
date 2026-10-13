# Bug classes found in follow-up rounds 3–10

Each class recurred, or had a sibling found later. When reviewing a round:
- probe the classes that the touched code could hit;
- re-check the earlier instances.

Round numbers refer to the handoff that asked for the fix.

## Validation and the compiler disagree

The commonest class. Validation either accepts IR that lowering then rejects
with a bare `invalid function`, or panics on, or the reverse.

Instances:
- **Stray predicate children** (`Compare.Left`, `Not.Right`) were accepted,
  then rejected by the walk (r6).
- **Predicate operands:**
  - operands missing from `step.Values` (r7);
  - operands referencing `OpSet`, stores or scope enter/leave (r7, r8);
  - non-values in `step.Values` / `Args` / `OtherArgs` (r8).
- **Non-value fields:** non-value Program/step `Result`/`ResultParts` and
  ScopeExit `Exports`/`Hidden` (r9).
- **Arity and params:** `Step.Arity` not checked against the segment's
  params, and `OpParam` outside the entry segment (r9). Both caused
  index-out-of-range panics on all archs.
- **Ignored but still read:** a field validation treats as *ignored*
  (`TermJump.Result`, predicates on non-branch steps) was still read by
  vector legalization, which panicked (found r10, present since before r8).
  Its sibling: `Args`/`OtherArgs`/`To`/`Other` on jump/return steps were
  accepted and then read, panicking or changing listings (found r11). Fuzz
  every field on every terminator kind, not just the one reported.

- **Over-rejection by a new rule:** r14 made Validate require
  `CallEffect.ResultRegister`, but 386 `PreflightCall` describes results only
  in `ResultLocation`, so the target's own effect was rejected. Check every new
  validation rule against what every target's public producers return
  (`abi.PreflightCall` for all six archs × both ABIs), not one target.
- **Shadow validators missing state:** r14 gave the amd64/arm64 text
  assemblers a private byte encoder for `Err()`, but the encoder read its pc
  only from `SetPC`, while the listing advanced its own. Listings driven
  through `machine.Visit` without `SetPC` then rejected short branches far into
  the listing. A shadow validator must mirror all state the real one reads.

**How to review:**
- Use one shared "produces a value" classification for every operand field
  (`ir.Opcode.ProducesValue`, since r10).
- For every step and instruction field, check it is either validated or
  never read by lowering. Use IR mutation to show it (verification.md §4).
- Errors wrap `ir.ErrInvalidProgram` and name the step, node and rule, with
  opcodes printed by name.

## Panics on unusual but supported input

- **amd64 params 7–8** with a scoped `Var` indexed a 6-entry register
  table (r9). Round 8 already failed such programs, so this was not a
  supported-path regression, but it was a panic.
- **Deep recursion:** selection (then `machine.SelectProgram`, now in
  `compile`) on long chains, and predicate trees of
  1M nodes, overflowed the stack (loong64 review, r3). Left-deep And/Or
  chains must be iterative, and depth limits must not reject ordinary
  fluent code (r4: a cap of 128 broke `p = p.And(...)` chains).
- **Table lookups:** a table indexed by ABI position must return
  `(value, ok)`, never panic (r9). It must also cover the real ABI bank
  (r10: the riscv64 Go bank had 9 of 16 entries).

## Shared mutable state behind public APIs

- **Cached values mutated by callers:** a cached `CallEffect` was appended
  into by concurrent preflight callers. The race showed only on arm64,
  whose slices had spare capacity (r7). Caches must store capacity-clipped
  slices, and callers must copy before modifying.
- **Package tables exposed as slices:** register tables were returned as
  slices of package arrays; mutating them changed later compiles (r7). They
  became immutable `machine.RegisterSet` values.
- **Mutable funcs:** `builder.Scalar`/`Vec`/`Mask` were exported `var`
  funcs. Sweep for exported vars and mutable package state.
- **Hidden fields:** `CPUProfile` gained a hidden pointer, which broke
  `DeepEqual`, and a cache hit returned the caller's backing array (r5).
- **Trusting public constructors:** a public constructor that takes
  ownership without copying (`execution.NewOwnedTemplate`, r9 → deleted in
  r10) breaks "immutable and safe for concurrent use". Fix it by making the
  data immutable (packed clones, immutable report values), not by a
  constructor that trusts the caller.

## Ownership and lifetime contracts

- **Copy vs alias:**
  - one API returned a copy on some targets and an alias on others
    (`ByteAssembler.Bytes()`, `Relocations()`);
  - `ir.Instruction.Inputs()` and `SelectedInstruction.Inputs()` returned
    either depending on the opcode (r8, r9, r10).
- **Origin-dependent lifetime:** a Workspace-leased stream's results changed
  before the documented boundary, while results from `NewStream` didn't
  (r8). The contract must not depend on origin.
- **Nil vs empty:** `slices.Clone` changed nil vs `[]` in public reports.
  Pin the shape per entry point and field against a recorded baseline (r5,
  r6).

## Scratch reuse and retention

Introduced by the allocation work (pooled per-compile scratch) and tuned
over r7–r10.

- **No decay at all:** one giant compile pinned tens of MB per P (r7).
- **Decaying the whole scratch:**
  - all-or-nothing decay thrashed on big, tiny, tiny alternation (r8);
  - medium workloads never decayed, because of an all-time peak (r8).
- **Per-buffer decay:**
  - 1/16 per compile with a 2x band still thrashed at a 1:10 big/tiny gap
    (r9);
  - widening the band to 4x then kept medium → smaller-medium slack forever
    (r9 → r10: wide band only during alternation, 2x after 32 steady
    requests).
- **Budget trimming:** an over-budget trim that dropped everything above
  256 KiB was far below the budget (r9). Trim largest-first, down to the
  budget.
- **Accounting:** maps were undercounted by about 20% (r7), absent maps
  counted phantom storage (r9), and `Trim` and `RetainedBytes` disagreed (r8).
- **Results keeping slack:** sealed programs kept builder slack capacity
  (r7). Measure heap per held result, not B/op.

**Test every decay change** with:
- big/tiny alternations at 1:1, 1:2, 1:3, 1:10 (and longer);
- giant → medium and giant → tiny;
- medium → smaller-medium;
- GOMAXPROCS 1 and 8.

## Poison build blind spots

`-tags nyanjit_scratch_poison` must catch stale use after every boundary:
- **Missing buffers:** some reused buffers weren't poisoned at all — plan
  maps, control buffers, allocation temps, and machine selection/layout
  (r7).
- **Poison then reuse:** buffers were poisoned but then reused, so a stale
  holder read the *new* data (r9).
- **Detach and zero:** detaching *and zeroing* the struct stopped poison
  builds exercising reuse at all (r9 → r10). The fix is to detach the
  poisoned old array and hand the next lease a fresh, pre-poisoned array of
  the same capacity, keeping the decay history.

## Performance regressions hiding in API cleanups

- **Moving to immutable values:**
  - iterator-based access (`RegisterSet.All`) in hot paths cost 4–17% on
    Prepare/calls (r8). Index directly in hot paths.
  - copying constructors and report `Clone` added allocations to tiny
    Prepare (36 → 42 in r8). Packing clones into one backing array fixed
    it (→ 34 → 25).
  - deleting a trusting constructor made the template clone the whole
    report, including vector operations: +4.5% B/op on Prepare/vectors
    (r11). Check B/op on large reports, not only tiny.
- **Merging a wrapper into a public type (API phase 1):** folding
  `compile.CodeTemplate` into `execution.Template` made the public constructor
  clone the report it used to share: +2 allocs per template and +3.9% B/op on
  Prepare/vectors. The fix the user chose is a read-only `abi.Report` view
  (getter per field) shared by Unit and template. Deleting `Analyze` also
  deleted its report-only lowering, so `Unit.Report` paid for finishing
  literal pools (+11.6% B/op on sha256-simd) until pool finishing became lazy.
  After any API merge, compare B/op and allocs on large reports and on
  report-only paths against the previous tree.
- **Asymptotics:**
  - a `seen` map per `validExpression` call made building 12x slower (r3);
  - quadratic rescans in home planning and `Builder.Enter` were only caught
    once the scaling-gate sizes were large enough (r6 addendum). Verify a
    gate by reintroducing the old quadratic into a copy and checking it
    fails.
- **Hardening checks on hot paths:** the security audit put per-instruction
  validation inside arm64 `PhysicalSize` (re-entered many times during
  relaxation) and a mutex around every native call: +3–16% compile, +41%
  per call with Harden off (r12). The implementer's own before/after already
  included it, so only the comparison against the previous round's tree
  showed it. Always benchmark against the last reviewed tree, not just the
  implementer's `before/`.
- **Builder:** buffers that grow repeatedly (`terminateScopeEntries`) should
  reserve their known final size.

## Frame and ABI sizing

- **Slot words:** 386 vector spill slots were counted as 2 words, not 4.
  This overran the saved EBP under StackPlatform and the reported workspace
  under StackNoUse (r3). Derive word counts from `RegisterBits`, never
  from 64-bit assumptions.
- **Limits:** diagnostics must be detailed and wrap `ErrBackendLimit`
  (r6 addendum, r10). Limits should count what is live, or document what
  they count (r6: declared Var words, not live).
- **Host vs target:** cross-target `Lower` laid out gateways symbolically and
  reported different sizes from a native lowering (r5). Results must not
  depend on the host.
- **Variable homes vs fixed-register expansions (r14):** the home planner
  (`compile/scoped_variables.go`) works on IR before selection. It blocked
  registers only for the IR op, so homes landed in registers that later
  sequences overwrite:
  - software fallbacks chosen during selection (386 64-bit count-zeros →
    paired Select, writing EAX–EBX and ESI; 386 MulHigh → 64-bit multiply);
  - Harden blinding after selection (immediate shift → register-count shift;
    scaled address → multiply), on 386 and amd64.

  The result was wrong code under StackPlatform (a pointer home overwritten,
  then dereferenced). `TestHomeExpansionCoversSelection` now selects one-op
  programs per target, stack practice and Harden setting, and checks the
  planner's exclusions against `ImplicitWrites` of what was selected. When a
  new fallback or blinding rewrite appears, extend that test.
- **Generated on one host, compiled for another:** generator programs built
  on arm64 use 64-bit `NativeBits` (e.g. call targets). Compiling them for
  386 gives clean `ErrBackendLimit`, not a defect (r14, scope seed
  2010000117). Generate on the target arch (under qemu) before reporting a
  386 rejection.

## Process lessons

- **Golden equivalence across binaries:** listings and bytes of programs with
  calls embed host gateway and callee addresses, which move whenever the test
  binary changes. Record and replay old vs new API in the same binary to
  separate real differences from address drift (API phase 1: 427 apparent
  differences, 0 in one binary).
- **Mechanical rewrites:** `gofmt -r 'Code -> execution.Code'` also rewrites
  field selectors such as `x.Code`; use a type-checked rewriter
  (`go/packages`, one pass per GOARCH and build tag). A rewriter that appends
  `.Snapshot()` to interface-typed `any` arguments also changes
  `reflect.ValueOf(view)` in tests that inspect the view itself; grep for it.

- **Interleaved tasks:** the user may give the implementer other tasks
  between rounds. Their changes are in the same tree, so diff against the
  implementer's `before/` snapshot.
- **Unconfirmed claims:** an implementer's "zero panics" or "actual ABI
  banks" claim was wrong in two rounds. Re-run everything.
- **Panic output:** panic stack traces differ by goroutine number. Record
  panics by message, or concurrency runs show false mismatches.
- **Fixed-byte gates on 32-bit:** a retention bound in bytes tuned on 64-bit
  failed on 386 (r11). Run retention gates under QEMU 386 with
  `NYANJIT_PERF_RETAINED=1`.
- **Interleaved-task regressions:** the narrow-memory task made 386 fail a
  program round 9 compiled (r11). Diff the interleaved changes against the
  previous round, not only the round against its own `before/`.
- **Short fuzz gates:** the implementer's 30 s `FuzzSecurityRawIR` gate
  missed a ScopeLeave-Args panic that the audit's own IR fuzzer found in
  180 s (r13). Run the audit fuzzer for 15+ minutes when validation changes.
- **Test configs too narrow:** a 386 fix was tested only under StackPlatform
  and stayed broken under StackNoUse (r13). Probe every stack practice × ABI.
- **Approval scope:** approval to hand off covers one round. Ask again for
  the next.
- **No implementer available:** when the Codex pane is gone, the user may ask
  the reviewer to do the round itself (r14). Same rules apply: snapshot
  `revN/pre` first, no commits, no new public API without asking, run every
  gate, and keep probe files out of the tree (move `zz*` probes to the
  scratchpad before running gates).
- **Execution sweeps find what listings cannot:** the 386 StackPlatform wrong
  code (r14) showed only when programs ran under qemu-i386 and results were
  compared across configs. Keep a random-program execution sweep
  (`zz386exec` harness, `ZZX_JSON` dumps crashing programs) in every round
  that touches 386 allocation, and include Harden configs: blinding changes
  register needs.
