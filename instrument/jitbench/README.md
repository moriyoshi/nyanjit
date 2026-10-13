# JIT comparison harnesses

These harnesses compare NyanJIT's compile time and generated code with three other JIT compilers. Each one builds the same kernels through its engine's own API, times the same phases, and prints the same kind of line. The tables in the top-level README's [Performance](../../README.md#performance) section come from them.

| Directory | Engine | Version | Needs |
| --- | --- | --- | --- |
| `nyanjit/` | NyanJIT, both tiers | this tree | Go |
| `sljit/` | sljit | 39c508d (fetched by `build.sh`) | a C compiler, git |
| `cranelift/` | Cranelift | 0.137.0 (pinned by `Cargo.lock`) | cargo |
| `tpde/` | TPDE-LLVM with LLVM 19 IR | 9779acf (fetched by `build.sh`) | Docker, an arm64 host |

The TPDE harness drives TPDE's aarch64 compiler only. `tpde/VERSIONS` records the image, compiler and flags it was built with. Fetched sources, build outputs and results go in `.build/`, which git ignores.

## Running

```
instrument/jitbench/run.sh [-c CPU] [-o DIR] [ENGINE...]
```

It runs each engine's `run.sh`, writes `DIR/ENGINE.txt` for each one, and prints `DIR/summary.md`, the merged compile-time and run-time tables. Each script builds its harness first if needed. Without `ENGINE` arguments it runs every engine whose tools are present. `CPU` defaults to the first core with the highest `cpu_capacity`, which is a big core on a heterogeneous host. `DIR` defaults to `.build/results/<UTC time>`.

Each engine can also run on its own: `ENGINE/run.sh CPU [harness flags]`. The flags shorten a smoke test:

| Engine | Flags for a quick run |
| --- | --- |
| `nyanjit/run.sh 5 -t 20ms -r 1` | budget per round, rounds |
| `sljit/run.sh 5 20` | budget per round in ms |
| `cranelift/run.sh 5 -t 20 -r 1` | budget per round in ms, rounds |
| `TPDE_BENCH_FLAGS="-t 20" tpde/run.sh 5` | budget per round in ms |

`tpde/build.sh` builds the `nyanjit-bench-tpde:local` image if it is missing, and builds the harness inside it. Set `TPDE_BUILD_CPUS` (a cpuset such as `10-17`) to keep the build off the core you measure on. The containers run with `--rm` and are named `nyanjit-bench-tpde-*`.

## Kernels

| Kernel | What it does | Argument |
| --- | --- | --- |
| `tiny` | One addition and a return. NyanJIT adds a constant; the others add two arguments | 3 (and 4) |
| `chain100`, `chain1000` | 400 or 4,000 straight-line operations: multiply, shift, xor, add | 12345 |
| `loop` | A counted loop with one accumulator | 10,000,000 |
| `pressure` | A loop that updates 16 accumulators, then xors them | 1,000,000 |
| `collatz` | Collatz step counts for 1..n: nested loops with data-dependent branches | 100,000 |
| `fp` | A degree-8 float polynomial and an unordered-or-less-equal compare per iteration | 1,000,000 |

Every engine's kernel returns the same result, which each line prints last. Mutable locals are NyanJIT `Var`s, Cranelift `Variable`s, and sljit registers. TPDE gets LLVM IR in two forms: `alloca`, what a simple frontend emits, and `ssa`, the same function after mem2reg.

## What is timed

Each line is the median of 7 rounds of a 300 ms budget, divided by the number of compiles in the round. `run` is the best of several calls of a function compiled again after the timing loop.

| Engine | Columns | Compile, for comparison |
| --- | --- | --- |
| NyanJIT | `compile` (Builder through `Lower`), `template`, `bind` | `compile` |
| sljit | `emit` (sljit calls), `total` (emit plus `sljit_generate_code`) | `total` |
| TPDE | `build` (LLVM IR through IRBuilder), `compile`, `bind` | `build` + `compile` |
| Cranelift | `build` (IR through FunctionBuilder), `compile`, `bind` | `build` + `compile` |

"Compile, for comparison" runs from building the IR to finished machine code. `summarize.awk` uses it. It leaves out mapping the code executable (`bind`), which is a few microseconds of system calls for every engine except sljit, whose executable allocator reuses memory. NyanJIT's `template` (0.3–0.4 µs for the baseline tier's small kernels) is also left out, because the other engines have no counterpart.

To compare compilers without IR construction, use TPDE's `compile` column and NyanJIT's phase split below.

`ns/op` is the compile figure divided by the kernel's operation count. `tiny`'s run time is mostly call overhead: a call from Go into NyanJIT's code costs about 64 ns, and a direct C call 16 ns.

## Measurement rules

- **One kernel per process.** On the Cortex-X925 host these numbers came from, code bound at a recently used address ran 8 to 35 times slower. That made run times in a process that had already compiled other kernels meaningless. Every `run.sh` starts a new process for each kernel.
- **Pin to one big core, and keep the host quiet.** Background load such as a QEMU test matrix or a build shifts compile times noticeably, even on another core.
- **Compare like with like.** Rerun every engine in one session after an engine or host change, rather than mixing new numbers with old ones.

## Profiling NyanJIT's baseline compile

`nyanjit/phase_test.go` has `BenchmarkPhase/KERNEL/{all,setup,setup+build,setup+build+lower}`. `nyanjit/phases.sh KERNEL [CPU]` profiles `all` and prints each phase's share: setup, Builder calls, the segment hand-off, liveness, allocation, layout and encoding, Template, malloc and GC.

```
go test -C instrument/jitbench/nyanjit -run '^$' -bench 'BenchmarkPhase/loop' -benchmem
instrument/jitbench/nyanjit/phases.sh loop 5
```
