---
name: nyanjit-jit-bench
description: Run and interpret the JIT comparison benchmarks in instrument/jitbench (NyanJIT baseline and optimized tiers against sljit, TPDE-LLVM and Cranelift). Use when asked how NyanJIT's compile or run time compares with other JITs, to check a compile-time change against them, to profile where the baseline tier's compile time goes, or to refresh the comparison tables in README.md.
---

# NyanJIT JIT comparison benchmarks

`instrument/jitbench/` holds one harness per engine and a driver. Read its
`README.md` first: it lists the kernels, the columns each harness prints and
which columns are comparable. This skill is the procedure and the mistakes
to avoid.

## Ground rules

- **The machine is shared.** Other projects' Docker containers and tmux
  sessions run on it. Only stop containers this skill started; they are
  named `nyanjit-bench-tpde-*` and run with `--rm`. Never run
  `docker kill`, `docker rm` or `docker system prune` on containers or images
  you did not create.
- **Do not commit results.** They go in `instrument/jitbench/.build/`, which
  git ignores. Commit only when the user asks.
- **Measure on a quiet host.** Before a run, check for heavy load
  (`uptime`; `pgrep -a qemu-`; running `go test` or builds). If a test
  matrix or build is running, wait or tell the user the numbers are noisy.
  Smoke tests are fine under load; reported numbers are not.

## Procedure

1. **Pick the core.** `run.sh` picks the first core with the highest
   `cpu_capacity`. Say which core you used. On the Cortex-X925 host the big
   cores are 5–9 and 15–19, and earlier tables use core 5.
2. **Smoke-test once after changing a harness** with short budgets (the
   flags are in the harness README), to check that it builds and that every
   engine prints the same `result` per kernel.
3. **Run everything in one session:**

   ```
   instrument/jitbench/run.sh -c 5
   ```

   The first TPDE build clones TPDE with its submodules and links LLVM
   statically in Docker; it takes far longer than the run. Pass
   `TPDE_BUILD_CPUS=10-17` (or other cores) so it stays off the measured
   core. Later runs reuse `.build/`.
4. **Read `summary.md`**, and the per-engine files for other columns (bind,
   template, emit, code bytes).

To measure only NyanJIT after a compiler change, run
`instrument/jitbench/run.sh -c 5 nyanjit` and compare with the previous
`nyanjit.txt` from the same host. Rerun the other engines too if the host,
kernel or toolchain changed since their files were made.

## Interpreting

- **Compile figures include IR construction for every engine.** NyanJIT's
  `compile` is Builder calls through `Lower`; TPDE's and Cranelift's
  comparable figure is `build` + `compile`; sljit's is `total`. That is
  what `summary.md` shows.
- **Engine against engine, without IR construction, needs different
  columns:** TPDE's `compile` alone against NyanJIT's engine time from
  `nyanjit/phases.sh` (liveness + allocation + finish). Do not compare one
  side's total with the other side's engine-only figure. That mistake was
  made once, and the user had to be given a correction.
- **Run times:** `tiny` and `chain*` run times are dominated by call
  overhead (Go into NyanJIT code costs about 64 ns, a C call 16 ns); use the
  loop kernels to compare code quality.
- **Code bound at a recently used address can run many times slower** on
  this host. The scripts start a process per kernel to avoid it; never time
  several kernels' code in one process, and distrust a single outlier
  run time.
- **Report ratios with their range** (for example "1.4 to 2.1 times") and
  name the kernels at each end. Check every ratio in prose against the
  table it describes before reporting it.

## Profiling the baseline compile

```
instrument/jitbench/nyanjit/phases.sh KERNEL 5
go test -C instrument/jitbench/nyanjit -run '^$' -bench 'BenchmarkPhase/KERNEL' -benchmem
```

`phases.sh` prints the share of setup, Builder calls, the segment
hand-off, analyze, allocate, finish, Template, malloc and GC, and leaves the
profile in `.build/nyanjit/` for `go tool pprof`. Use
`-sample_index=alloc_objects` on a `-test.memprofile` from the same test
binary to find allocation sites. A compile-time change that should not
change code is checked by comparing listings and reports before and after,
not by benchmark numbers.

## Updating README.md

The tables under "Comparison with other JIT compilers" in `README.md` come
from one full run. When refreshing them:

- convert units (compile in µs, run in ms for the loop kernels) and keep
  three significant digits;
- update the versions and the host named in the paragraph above the tables;
- recheck each ratio in the paragraph below the tables, and the
  "compiles N to M times faster" range in the `Tier` description under
  "Targets, units and templates".

## Changing engine versions

The pins are `SLJIT_COMMIT` in `sljit/build.sh`, `TPDE_COMMIT` in
`tpde/build.sh` (plus `tpde/VERSIONS` and the LLVM version in
`tpde/Dockerfile` and `tpde/harness/CMakeLists.txt`), and
`cranelift/Cargo.toml` with `Cargo.lock`. After a bump, delete the matching
build directory under `.build/`, smoke-test, and rerun every engine.
