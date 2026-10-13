# Verification, Benchmarking and Review Practice

## Summary

Package matrices, differential execution, mutation tests, snapshots and benchmarks answer different questions. Use independent oracles, matched baselines and bounded isolated jobs, and preserve durable evidence outside scratch directories.

## Key Facts

- A green package/build/smoke matrix is not a compiler-correctness gate.
- Snapshots establish output equivalence; they do not establish the original output's correctness or performance.
- Benchmark against the last reviewed tree, using matched pairs and both orders.
- Confirm a test selection actually ran tests; count `--- PASS` lines when appropriate.
- Diagnose emulation-only runtime failures with a standalone program containing no NyanJIT.

## Details

### Independent execution and mutation

The first scoped-Var matrix passed 1,465 checks, then review found edge-copy, scope-transition, FP-home and workspace-report miscompiles. A 34,100-seed differential and byte-stream `compile/varfuzz` harness replaced confidence from build checks. The harness shares one AST interpreter across corpus, fixed sweeps, FuzzScopedVars and subprocess minimization. Each sweep exercises leaf, C-call and mixed Go/C configurations and compares Func with Call for Go entries.

It found workspace-base allocation under StackNoUse, wrong-width ARM AND flags, 386 wide scratch clobbers and missing amd64 Go-call R15 clobbers. It also exposed Go registration's inclusive endpoint overlap: managed mappings reserve space past text and Register rejects touching ranges.

Historical results were 450,627 execution checks plus 80,934 separately counted limits; native arm64 coverage sessions executed 46,832 and 124,175 cases. Corpus cleanup changed 256 per-config files (5.9 MB) into 32 compressed legacy streams plus five minimized entries (176 KB), preserving decompressed streams. TestLegacyCorpus replays 256 seed/mode/config pairs.

The second security audit used 500 control, 500 straight-line and 300 hardened programs per target. F14 originally failed 68/500 control cases; after repairs there were zero mismatches/clobbers. A callee-saved-register oracle was as important as result comparison.

Audit methods are historical: the first audit used six reviewers, native arm64 and QEMU for 386/riscv64/loong64/ppc64le, with amd64 decoded only. The second executed amd64 under qemu-x86_64 using a static purego stub. Early IR DoS probes reached 7.5 GB RSS, prompting timeout, GOMAXPROCS=4, GOMEMLIMIT=4GiB, ulimit -v, -p 2 and bounded 60-second fuzz runs. Those were resource caps, not adequate long-term fuzz acceptance. The threat-model pass mapped 13 JIT vulnerability classes; absence of speculation/deoptimization optimizers does not prevent unsafe API outcomes.

### Output, ownership and scaling gates

Rounds 6-10 compared 15,605 frozen fixtures and fresh before/after/poison differentials with 3 generators x 1,000 seeds x 91 configurations (273,000 cases/mode). Strict validation invalidated 1,004 fixtures with 31,322 non-value Hidden IDs. Fixtures were rewritten with unchanged listing/hash evidence; the temporary legacy switch was deleted under the no-compatibility policy.

Golden corpora later went stale: 320 ARM frame fixtures and nine rejected-field fixtures at round 11; 74 failures even on an unchanged tree by October 26. Compare fresh pre/post output when stored goldens are stale; approved changes must eventually be recorded for a corpus to remain a gate. Original fixtures numbered 71,555, but cleanup left only 15,605 survivors.

The later deterministic snapshot records listings and reports for six architectures x two tiers x three function setups. Compare with cmp. For intended code changes, enumerate changed entries and use growth.py to expose every increase. Reviewed compile-time work in the October 30-November 5 period was snapshot-identical.

Retention needs per-held-result heap checks, poison builds and alternating workload sizes. Cache ownership tests caught shared capacity in CallEffect (2,893/24,000 contaminated results), mutable register tables and zero-capacity slices that still retained backing arrays. Clip immutable cache slice capacities; do not mistake cap zero for release of ownership.

The scaling gate uses 24 shapes below 2.3x per doubling. Scope/loop sizes moved from 50..400 to 200..1600 when O(depth^2) regressions slipped through at 2.298x. Two ancestor walks fit 2.189/2.185x at old sizes but 2.668/2.623x at larger sizes; injected regressions then failed at 2.6-4.3x. Intermediate allocation variants failed at 2.306-2.399x; final trim/geometric growth passed 24/24 pinned fits (1.868-2.239x) and 24/24 unpinned. Run this gate alone on a pinned CPU.

### Benchmark controls

State target, tier and comparable columns first. Compile-only cross modes use NyanJIT -arch amd64|386 and Cranelift --isa x86_64 with Cargo x86 support; Cranelift has no 386 backend. TPDE measurements include IR build plus compile. The verdict is the median of matched after/before ratios, not the quotient of separately printed medians.

Use isolated 16-pair acceptance in both orders when the review protocol requires it. BenchmarkZZW8's w8 comparison used six alternating rounds on core 5, GOMAXPROCS=1. General project benchmarking uses one quiet Cortex-X925 big core (5), taskset and timeout, with GOMAXPROCS=1. Do not run concurrent heavy jobs on the same cores. Recently reused code addresses produced 8-35x runtime slowdowns on this host; jitbench runs one kernel per process, and the allocator study used minima across many runtime repetitions to address placement bimodality. Follow the jitbench skill for the specific comparison's statistic.

`BenchmarkZZW8` is a historical local harness name from the journal, not a benchmark currently present in the checkout. Use the jitbench skill and current tracked benchmarks for reproducible new comparisons.

Setup must match: an uncached target on only one side invalidated an initial LEA overhead claim (+2.7% vs corrected +6.7%). Split combined changes with an intermediate build when one may hide another. Output-equivalent unverified work later passed every correctness gate but still produced pool and compact-form slowdowns.

Rounds 11-13 also retracted a claimed scope-time improvement over round 8: it did not exist. Preserve that correction alongside the separate HeapAlloc-noise correction rather than repeating the original performance claim.

### Runtime and emulator diagnosis

LoongArch gccheckmark failed with the published tonistiigi QEMU 10.2.3 build. `e2e/probes/gcmap/main.go` reproduced it using only standard library: 20 goroutines and 400,000 nested maps. Published build failed 2/20 runs; Debian 10.0.13 and unpatched upstream 10.2.3 passed 40 each. This isolates an emulator build, not a named patch. gccheckmark already implies asyncpreemptoff, so an explicit noasync control adds no evidence. A separate one-off Pinner leak panic was not independently reproduced and remains in TODO.md.

QEMU riscv64 10.2.3 lost a sync.Mutex wakeup in TestCallRacesClose. A pure-Go admission/count/closed-bit reproducer hung 71/100 on CPUs 13-16, 1/100 on 10-12 and 0/60 with GOMAXPROCS=1. The original test hung 23/211 and 1/300 on those respective CPU sets. Dumps showed a parked waiter with no holder; no native failure was observed. LoongArch showed one similar hang whose scratch evidence was lost.

Atomics spin tests can deadlock the runtime even natively after SetGCPercent(-1): injectglist wakes waiters before mark termination's forEachP(flushProcCaches), while a JIT spin loop never reaches a safe point. Call runtime.ReadMemStats after changing GC settings; its worldsema acquisition waits for the cycle to complete. QEMU store-buffer litmus violations absent in 15 million hardware runs are logged rather than failed under emulation.

### Review and shared-workspace discipline

Check the exact question behind reported user approval. The CallValues either/or question omitted the user's desired Words extension; final Call/CallPair remain raw. Conversely, Codex's Cell addition had direct user authorization unknown to an older standing note. Authorization is established by session evidence, not an agent-specific assumption.

Historical parallel work used isolated copies from a named base, assigned cores/container names, no git writes and per-task rules. git merge-file interleaved adjacent opcode insertions; preserving each side's insertion as a unit helped, but semantic enum checks were still required. Session restart loses context, not edits in surviving copies. Scratch evidence is expendable; durable designs belong in code/docs/journal.

Never git stash in the main tree or use -mod=mod for repository module commands (a helper once mutated backend/e2e go.sum). Use cooperative STOP_REQUESTED files for long runners. Stop watchers you start. Leave other projects' containers alone; owned containers use --rm, nyanjit- names and pinned CPUs. Exclude .agents-workspace/ and *.test from Docker context: a missing exclusion once sent about 81 GB. Keep scratch paths out of tracked scripts; memwatch.sh accepts SCRATCH, and hygiene checks include encoded home paths. External Docker FROM images and binfmt references are digest-pinned with a regression test; cached-image tests do not establish that the complete multi-OS image was rebuilt.

The period's verification set included native default/backend/online/strict-promotion gates, irconform on both tiers including hardened, 2,000 varfuzz sweeps, poison, retention and race; QEMU ran 84 package checks plus six sweeps across amd64 max/qemu64, 386, riscv64, ppc64le and loong64. Historical passes do not substitute for testing a new change.

## Files

- `compile/varfuzz/`, `instrument/irinterp/`, `instrument/irconform/`, `instrument/abicheck/`: independent oracles.
- `instrument/jitbench/`, `.agents/skills/nyanjit-review-rounds/`, `.agents/skills/nyanjit-jit-bench/`: procedures and comparison tools.
- `e2e/probes/gcmap/main.go`, `e2e/README.md`: standalone runtime probe and matrix.
- `TODO.md`, `.agents/docs/JOURNAL.md`: follow-ups and original evidence.

## Test Coverage

`TestLegacyCorpus`, `FuzzScopedVars`, `FuzzSecurityRawIR`, `FuzzEncoderVisit`, `TestSealedProgramRetention`, `TestReportSliceShapesAreConsistent`, `TestScopedVarsSurviveRegistrationBudget`, `TestExternalBaseImagesUseDigests` and target-specific execution regressions cover complementary boundaries. Managed fuzzing skips exhausted registration-budget configurations rather than terminating all scoped-Var testing. Confirm requested tests matched, completed and exercised the pre-fix failure.

For focused checks on the host, run from the appropriate module root, for example:

```sh
taskset -c 5 timeout 180s go test ./compile -run '^TestFloatWriteKeyCoversInputs$' -count=1
taskset -c 5 timeout 180s go -C backend test ./regalloc -run '^TestBatchSlotsFitWideValues$' -count=1
taskset -c 5 timeout 180s go -C backend test ./target/... -run '^TestDocumentedForms$' -count=1
```

These examples cover host-compatible tests only; architecture-tagged execution needs the matching host or the E2E/QEMU matrix. Apply the review/jitbench skill's full gate for code changes, rather than treating these focused commands as acceptance.

## Pitfalls

Do not label backend/frame limits as passes. Do not mask dirty upper bits out of differential inputs or assume a spill path ran: assert the actual spill and execute both bounds edges. A test written but never run encodes an expectation, not observed behavior. Raw scratch artifacts and deleted APIs can make historical reproducers unusable; keep mechanisms and test names in durable memory.

## Sources

Consolidated from these headings in `.agents/docs/JOURNAL.md`:

- 2026-10-14 — Scoped Vars, and a green matrix that proved nothing
- 2026-10-16 — LoongArch port (L0-L4), and a GC failure traced to the emulator build
- 2026-10-16 — Compile allocations, round 6 and steps 1-3, and the baseline they were measured from
- 2026-10-17 — Two API policies in one night, and where internal/ went
- 2026-10-17 to 2026-10-18 — Review rounds 6-10: scratch retention, poison builds, and validation that lowering trusts
- 2026-10-18 — An either/or question hid the option the user wanted
- 2026-10-18 — First security audit: frames, encodings and the call boundary
- 2026-10-19 — Optional hardening (Harden), and what the audit cost when it was off
- 2026-10-19 — Rounds 11-13: retries, report ownership, and measurement noise
- 2026-10-23 to 2026-10-24 — Writing FORMS.md found encoder bugs compile never hits; a QEMU hang that was not ours
- 2026-10-24 — A whole-function allocator beside the online one, not instead of it
- 2026-10-25 — Pre-release security audit: four high-severity bugs and their root causes
- 2026-10-25 — Second audit: the Medium and Low findings, and how the audit ran
- 2026-10-25 — Dead-flag LEA on amd64, and a benchmark that inflated its own baseline
- 2026-10-26 — Allocations down 42–99%, and a frozen corpus that had stopped meaning anything
- 2026-10-27 — A register allocator protocol, and porting compile onto it
- 2026-10-29 — Two tiers: one Builder dialect, a baseline engine on six targets
- 2026-10-30 — Eleven sljit-gap items in one wave, and what seven parallel merges taught
- 2026-11-01 — 386 code size, one encoding at a time
- 2026-11-01 — Optimized-tier compile time against Cranelift, and two corrections from the user
- 2026-11-04 — Reviewing Codex's amends
- 2026-11-05 — Unverified fixes, and the benchmark that caught two regressions
- 2026-11-05 — Work summary: 386 parity and compile cost, 2026-10-30 to 2026-11-05
