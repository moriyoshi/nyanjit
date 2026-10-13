# Scoped variable code generation

This records scoped-variable code generation and the correctness fixes from the 2026-10-14 review. The API and lifetime rules remain those in [virtual-register-design.md](virtual-register-design.md).

## Changes

- Get lends a register home to a snapshot when it is available. Ordinary arithmetic cannot overwrite that home. An adjacent Set can coalesce its producer into the home when the old snapshot is dead; a live snapshot is relocated into a spare register or spilled before the write.
- Jumps without arguments discard their bookkeeping result, eliminating zero materialization at loop entry and back edges.
- Scope lookahead accounts for ordinary temporary demand in each register bank. Outer homes remain fixed when capacity is sufficient; under pressure, inner variables take priority. A clean outer home is not written back at Leave.
- Variable slot offsets are reserved without touching memory. Frames and workspace are sized from actual accesses, including label edges and ABI saves. Direct register exports do not create phantom spill slots.
- FP variables, captures, and FP label parameters use FP registers. Integer bit patterns and native FP spill representations are distinguished on edges, preserving POWER single-precision conversion and RISC-V NaN boxing.
- Preserved GPRs become available under pressure for variable homes and ordinary selected values in loops. The allocator schedules entry saves and restores, including workspace-only returns. Runtime registers, expansion registers, and managed frame pointers remain reserved.
- Addition and subtraction by zero become identity copies. POWER uses plain arithmetic when control-flow flag liveness proves CR0 and carry dead.

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
| idctvar | 453 → 361 | 376 → 289 | 506 → 398 | 616 → 514 | FP unavailable |
| idctargs | 428 → 375 | 333 → 312 | 446 → 420 | 587 → 537 | FP unavailable |
| sum | 29 → 10 | 30 → 10 | 28 → 10 | 33 → 11 | 52 → 42 |
| sumargs | 11 → 11 | 12 → 12 | 12 → 12 | 13 → 13 | 39 → 39 |
| pressure4 | 64 → 35 | 54 → 27 | 53 → 28 | 70 → 29 | 134 → 116 |
| pressure24 | 244 → 225 | 266 → 206 | 255 → 208 | 250 → 223 | 654 → 616 |

The Var sum loop has the same instruction count in its loop body as the label-argument version: five instructions for load, accumulation, pointer increment, counter decrement, and the back edge. Its complete listing is smaller on the four 64-bit backends, and needs neither a frame nor workspace. The 386 loop still uses its existing bytewise 32-bit load expansion and platform frame; this work does not add an FP backend for 386.

The 64-bit `pressure4` example keeps all four outer variable homes across the inner scope and emits no spill at Enter. `pressure24` exceeds register capacity and spills. ABI preservation and genuine spills can still create a frame.

## Validation

The earlier **1,465 matrix checks with zero failures** did not establish correctness: the review found reproducible edge-copy, scope-transition, FP-home and workspace miscompiles missing from that coverage. That result is superseded by the regression and differential checks below.

The corrected implementation passes **1,465 Linux matrix checks, zero failures, 16 expected skips**. Native `go test ./...`, vet on all six architectures, and the harness unit tests pass. The matrix covers Go 1.26.9 and 1.27.2 and POWER8/9/10 for both PPC64 byte orders.

Long differential runs use seeds 0–2,999 plus seeds 3,000–3,099 per arithmetic mode and architecture, with both storage modes and two input sets. All six integer runs and all five FP runs pass: **34,100 generated seeds**, including **3,087 valid integer programs per architecture** and **3,082 valid mixed-FP programs per architecture**. Thirteen integer seeds and eighteen mixed-FP seeds generate invalid IR rejected by Finish and are counted separately. Valid cases require successful compilation and exact agreement with the reference interpreter; compilation failures are failures, rather than expected skips. 386 has no FP run.

The high-pressure FMA operand test executes on arm64, riscv64 and both PPC64 byte orders. amd64's v3 lowering is checked; execution is skipped on the baseline x86 CPU used by this run. The original reproducers also pass across the final package matrix; their native-width loads and C-call results follow each target's contract, and Go 1.26 big-endian PPC64 uses a real ELFv1 descriptor for its nested C call.

The review reproducers are now execution regressions for mixed variable/label homes, foreign and managed calls, multiple exits, large scope-exit grids, FP label conflicts, straight-line workspace saves, and compact counters. Little-endian memory inputs are explicitly encoded so the tests also run on big-endian PPC64. Large managed frames that exceed existing registration limits are reported separately; the smaller reproducer cases must compile.

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

The container registers binfmt inside its private user namespace; host binfmt state is unchanged. Linux acceptance covers both Go 1.26.9 and 1.27.2, including POWER8/9/10 on both PPC64 byte orders. The full package matrix includes compile, builder, and DCT reference/rendering tests. JUnit and measured listings are saved under the ignored `e2e/.build/results/` directory; current listings, differential results and logs are in `var-review/`. The matrix report is `var-review-linux-checked.xml`. Big-endian PPC64 final counts are 514/537 for IDCT, 11/13 for sum, and 32/246 for the pressure programs; its explicit little-endian loads require extra instructions.

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
compare `execution.Func` with `Code.Call`.

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
remain present. On 386, the same 128 FP pairs are explicitly unsupported.

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

FP on 386 is explicitly unsupported. Insufficient caller stack budgets and
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
  The 386 FP backend remains unsupported. These limits are not counted as
  successful executions.
- Two native arm64 coverage-guided sessions each ran for ten minutes, with
  **46,832** and **124,175** executions and no failures. The latter includes
  the final destructive-operand reuse and comparison-folding changes.
- Sum, pressure and IDCT instruction counts are unchanged from the table above
  on all six backends, including both PPC64 byte orders.
- Corpus cleanup replays all 256 legacy pairs on every FP-capable backend;
  386 runs 128 integer pairs and explicitly skips 128 FP pairs.

The fixed-sweep logs, configuration counts, unchanged instruction counts,
corpus checksums and matrix summary are retained in the ignored
`e2e/.build/results/var-fuzz/` directory. The verified JUnit report is
`/cache/results/var-fuzz-linux-verified.xml` in the E2E cache volume. No host
binfmt registrations, packages or services were changed, and no commits were
made by this task.
