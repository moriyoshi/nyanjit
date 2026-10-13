# Verification methods

The scripts are in `../assets/scripts/` and the Go harness is in `../assets/harness/`.
- The harness files are test files that go into `compile/varfuzz` of **both**
  the pre and the post copy. They must stay identical on both sides.
- Adapt them only where an API change forces it, and make the same edit on
  both sides.
- They are build-tagged `linux && arm64`; the host is linux/arm64.
- `assets/meas/*` go into `compile/` (benchmarks) or the module root
  (retention); they use `instrument/workload`.

Rules for every run:
- Pin CPUs with `taskset`.
- Use a per-workdir `GOCACHE` and `GOTOOLCHAIN=local`.
- Run anything longer than a few minutes detached, writing a DONE marker.
- Foreground `sleep` is blocked; poll with
  `timeout 600 sh -c 'until grep -q DONE f; do sleep 30; done'`.
- **Memory:** three agents running builds, race tests, mutation sweeps and
  QEMU containers at once OOM-killed the host twice in round 13, taking the
  user's sessions with them. Put non-race processes under `ulimit -v
  12000000` and `GOMEMLIMIT=4GiB`, run race jobs one at a time, give every
  `docker run` `--memory 6g --memory-swap 6g`, keep builds at `-p 2`, and
  run the perf/QEMU agent after the other two. Run `memwatch.sh` (kills
  runaway review processes and logs them) for the whole review.
  The two round-13 kills were (1) a raw `machine.Visit` fuzz feeding the
  arm64 text assembler a ~7e18 frame size (unbounded expansion), and (2) the
  audit IR fuzzer's own mutator (`poc/ir-audit/cmd/fuzz/main.go` case 15
  loops `Params[l]` times, up to 1<<62). Cap that loop in your copy. qemu-user
  can't run under `ulimit -v` (it reserves a 4 GB guest space).
- If the session is interrupted, detached jobs die with it. Resume agents
  with SendMessage and have them check partial outputs before restarting.
- `pkill -f <pattern>` kills your own shell when the pattern appears in your
  command line, so use pgrep and then kill by PID.

## 1. Listing differential (`setup-diff.sh`, `run-diff.sh`, `classify.sh`)

`TestZZDiff` builds programs from a seed with one of five generators:

| generator | what it builds |
|---|---|
| `work` | workload shapes |
| `rand` | random straight-line and control IR |
| `varfuzz` | scoped variables |
| `scope` | nested scopes |
| `pscope` | parameter-heavy scopes |

For each program it renders the listing (`compile.Lower` with
`ContextExternal`, or `ContextGoManaged` for the managed configs, then
`Unit.ListingSymbolic`) on every arch × config. In "wide" mode that is 19
configs per arch: C/Go ABI × platform/no-use/managed stack, CPU profiles,
`VectorsOff`, ... On the host arch it also hashes external-context template
bytes and the `Unit.Report`. Each output line is
`gen seed arch cfg OK <sha>` or `... ERR <text>`.

Typical sizes per round:
- work 350 seeds;
- rand 10k;
- varfuzz 6k;
- scope 4k;
- pscope 15k.

Together that is about 3M lines, plus a poison build of post at about 1/4
the size. Use fresh seed ranges each round.

`classify.sh pre.txt post.txt` buckets every line into:
- identical;
- `(pins …)`-only error changes;
- other error-text changes;
- **listing diffs**;
- **OK↔ERR flips**;
- only-pre/only-post keys.

Every listing diff and every flip must be explained. Usually there must be
none.

Error-text changes are fine when intended (better diagnostics). Panic
messages carry goroutine numbers and stack traces, so compare panics by
message only.

## 2. Native comparison (`TestZZNative`)

This test:
- uses the default target (`compile.TargetSpec{}`) on the host;
- prints full `%+v` reports from `Unit.Report`, from an external-context
  template's `Bytes`, and from `Unit.Template` in the native, Go-managed and
  foreign contexts;
- hashes the bound code bytes.

Compare about 72k lines pre vs post. A random constant can collide with a
code address in one test binary only, and then the masker zeroes it on one
side. Confirm that kind of difference by comparing the bound bytes.

## 3. Concurrency determinism (`run-conc.sh`)

The test compares serial listings against listings produced by 16 goroutines
in random order, over several rounds. Run it in normal, poison, `-race` and
poison+`-race` builds.

Expect 0 mismatches and 0 `DATA RACE`. Shared caches (call effects,
register tables, CPU profiles) are where races appeared before.

## 4. IR mutation (`run-mut.sh`, `mutsum.py`)

This takes generator output and mutates raw `ir.Program` fields. Then it
calls every entry point under `recover`:
- Validate and Seal;
- `Unit.ListingSymbolic` and `Unit.Listing` (`compile.Lower` with the
  config's context: `ContextExternal` or `ContextGoManaged`);
- `Unit.Report`, `Unit.Template` and `Unit.RegisterScopes` (which needs a
  successful `Lower`);
- on arm64, `Unit.Template` in `ContextNative` and the external template's
  `Bytes`.

The mutation tests:

| test | mutates |
|---|---|
| `TestZZMut` | predicates and `step.Values` |
| `TestZZMutArgs` | `Args`/`OtherArgs` |
| `TestZZMut9` | Program.Result/ResultParts, step Result/ResultParts, ScopeExit Hidden/Exports, Step.Arity, OpParam placement, and jump Results in vector programs |

**Extend it every round** to whatever fields or ops the round touched. If the
generators never emit a new op, add a generator variant that does.

Post must have:
- **0 panics**;
- **0 accepted-then-invalid results** (Validate passes, then lowering
  fails with `ErrInvalidFunction` or "no materializable source");
- **no over-rejection.** The unmutated program and semantic no-ops (duplicate
  or reordered Hidden IDs, moving an ignored field) must still be accepted.

Over-rejection is also checked from the other side: dump the round's
pre-accepted mutants and replay them on post. Every new rejection must belong
to an intended class.

## 5. Builder output and frozen corpus

- **Builder output:** check that every Builder-produced program from every
  generator validates on post. When a round tightens validation, show that
  normalizing old Builder output (e.g. stripping now-invalid metadata) yields
  identical listings.
- **Frozen corpus:**
  - The frozen listing corpus lives at
    `.agents-workspace/tmp/compiler-alloc-20261016/listings-surviving`;
    replay it with
    `NYANJIT_LISTING_CORPUS=<dir> go test ./compile -run '^TestPerformanceListingCorpus$'`.
  - It must match exactly in normal and poison builds, with no env switches.
  - If fixtures are rewritten, verify that only Program JSON changed and that
    the listing/sha fields did not.

## 6. Benchmarks (`focus.sh`, `cmp3.py`)

The host is shared and often loaded (load average 20–36 with other users'
builds). So:
- **One process per sample.** Run before and after alternately, then repeat
  the whole run in the reversed order.
- Use `GOMAXPROCS=1` and pin to one CPU (17).
- **The "focus" method gives the verdicts:** each process runs count=5 ×
  300 ms and keeps its minimum. Run 16–24 processes per side.
  `cmp3.py base.log new.log` pairs process i with process i and prints:
  - the paired-ratio median;
  - the IQR;
  - min..max;
  - B/op and allocs/op.
- **A wide 20-pair matrix of every benchmark is only indicative.**
- **Calling a regression real:** it must show in both orders, with an IQR
  mostly on one side of 1.0. A change seen in one order only is
  inconclusive.
- **Comparisons:** compare against the previous round **and** against the
  round-6 baseline tree, because small regressions accumulate.
- **Benchmarks to cover:**
  - `Prepare/*`, `Build/*`, `BuildScopes/*`;
  - `BenchmarkZZPrepareAlternating` (`assets/meas/zzaltbench_test.go`):
    big/tiny alternation at 1:1, 1:2, 1:3 and 1:10. Add 1:40 and 1:100
    when decay logic changes.
- **Profiles:** confirm a suspected cause with a CPU profile diff
  (`-cpuprofile`, `go tool pprof -diff_base`) before naming it in a
  handoff.

## 7. Retention

- **Gates:** with `NYANJIT_PERF_RETAINED=1`, run
  - `TestGiantCompileScratchDecays*`,
  - `TestGiantTinyScratchAlternation`,
  - `TestScratchTotalBudget*`,
  - `TestSealedProgramRetention`, and the medium → smaller-medium gate.

  With `NYANJIT_PERF_SCALING=1`, also run the scaling gates
  (`Test(Prepare|Builder)Scaling`, fitted doubling ≤ 2.3x) on a quiet
  pinned CPU.
- **Steady-state patterns** (`assets/meas/zzret9_test.go`, `ZZ9_PRIOR` /
  `ZZ9_STEADY`): one compile of size A, then 800 compiles of size B,
  measuring heap excess over a B-only baseline. Run it at GOMAXPROCS=1 and 8.
  - Cases: giant → {medium, tiny}, medium → smaller-medium (1000 → 300,
    700 → 300) and 1000 → 700.
  - The implementer's own gates usually cover only giant → medium.
- **Sealed programs:** measure heap retained per held `SealedProgram` after
  two GCs. B/op doesn't show slack capacity kept inside the result.

## 8. QEMU (`qemu-run-one.sh`)

1. **Cross-build** each package's test binary per config with `go test -c`:
   - `GOARCH=386`;
   - amd64;
   - riscv64;
   - loong64;
   - ppc64le with `GOPPC64=power8|9|10`;
   - plus `examples/add`, and a `GO386=softfloat` compile test for the
     no-SSE2 probe.
2. **Run each binary in an existing image:**
   - `nyanjit-e2e:linux-gha` for 386, amd64, riscv64 and ppc64le;
   - `nyanjit-e2e:loong64-gc-full` for loong64.

   Use `docker run --entrypoint sh --cpuset-cpus …`.
3. **Invoke qemu directly** with an **absolute argv0**, setting:
   - `QEMU_CPU`;
   - `QEMU_LD_PREFIX=/opt/nyanjit/sysroots/<arch>` for ppc64le and loong64.

   **Never use `--privileged`.** `e2e/run.sh` needs it, so reproduce its
   steps by hand instead.

Configs (11):

| arch | `QEMU_CPU` values |
|---|---|
| 386 | `qemu32,-sse3`; `max` |
| amd64 | `qemu64`; `max` |
| riscv64 | `rv64,zba=false,zbb=false,zbs=false`; `…=true` |
| loong64 | `la464,lsx=off,lasx=off`; `max` |
| ppc64le | `power8`; `power9`; `power10` |

Extras:
- the scoped-variable sweep (`NYANJIT_VAR_SWEEP=500`);
- 386 `NYANJIT_PERF_RETAINED=1 TestScratchTotalBudget*`;
- the 386 no-SSE2 rejection (`QEMU_CPU=qemu32,-sse2`, expect
  `ErrCPUUnsupported`);
- the ppc64 cgo/callback integration (`e2e/ppc64cgo`) in exe and PIE modes;
- `examples/add` prints 42.

Record `ls /proc/sys/fs/binfmt_misc` and its interpreter lines before and
after, and confirm the host binfmt state is unchanged.

**Native 386 test binaries** are dynamically linked. Host binfmt then fails
because `/lib/ld-linux.so.2` is missing. Copy an i386 sysroot out of an
existing image (unprivileged container) and run `qemu-i386 -L <sysroot>`.
Subprocess re-exec tests still fail that way; that is an environment limit.
