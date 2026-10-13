# Cranelift harness

These are the same seven kernels as `../sljit/bench.c` and `../nyanjit/main.go`, written with Cranelift's `FunctionBuilder`. They are timed the way bench.c times sljit.

## Versions and build
- cranelift-codegen / -frontend / -jit / -module / -native **0.137.0** (the latest on crates.io on 2026-10-29), with regalloc2 0.15.2. `Cargo.lock` pins these.
- rustc 1.97.1. Release profile with `codegen-units = 1` and `lto = "thin"` (it builds cleanly).
- The ISA comes from `cranelift_native::builder()`. This detects LSE, PAuth and FP16, but `use_bti` and `sign_return_address` keep their default of off, so the code has no BTI landing pads and no PAC.

## Configs (`--config`)
- `speed`: `opt_level=speed` with the default `regalloc_algorithm=backtracking`.
- `fast`: `opt_level=none` with `regalloc_algorithm=single_pass`, the fastest allocator in 0.137.
- Both configs also set `enable_verifier=false`. The verifier is on by default, and it adds +73% (speed) or +160% (fast) to the compile time of chain1000. `--verify` turns it back on. They also set `is_pic=false` and `use_colocated_libcalls=false`, the same as `JITBuilder::with_flags` does on aarch64.

## What each phase measures
The program reuses one `OwnedTargetIsa`, one `codegen::Context` (cleared with `clear()` for each function) and one `FunctionBuilderContext`.
- **build**: `ctx.clear()`, then a new `Signature` (the default call convention, i64 parameters and result), then the IR through `FunctionBuilder` with Variables (`declare_var`/`def_var`/`use_var`), up to and including `finalize()`.
- **compile**: `ctx.compile(isa, ControlPlane::default())`. This covers egraph opts (in speed only), lowering, regalloc and emission into Cranelift's `MachBuffer`. The machine code ends up in `ctx.compiled_code()`. No kernel has relocations (this is asserted), and the f64 constants sit in the function's own constant pool.
- **bind** (default `--bind fresh`): a new `JITModule` (`JITBuilder::with_isa`, which uses the default `SystemMemoryProvider`), then `declare_anonymous_function`, `define_function_bytes(id, align, code)`, `finalize_definitions()` and `get_finalized_function`. That is `define_function` with its internal `ctx.compile` split out. For each function this does the following:
  - `mmap` of a new region rounded up to whole pages, plus a page fault when the code is copied in;
  - one `mprotect(R+X)`;
  - an icache clean/invalidate over the region;
  - one `membarrier(PRIVATE_EXPEDITED_SYNC_CORE)`.

  `JITModule::free_memory()` (`munmap`) then runs outside the timed region, like `sljit_free_code` in bench.c. Memory stays bounded.
- Other bind modes: `--bind shared` keeps one module for every function and `--bind arena` adds `ArenaMemoryProvider`. Both recreate the module every 1024 functions, outside the timed region. They are not cheaper: SystemMemoryProvider still mmaps once per function, and the arena calls mprotect twice. On tiny, bind took 8.6 µs (shared) and 8.9 µs (arena), against 8.2 µs for fresh.

## Timing and output
The method is the same as bench.c: 7 rounds of a 300 ms budget each, the median of each phase across rounds, and the run time as the best of 7 calls. The run calls a function compiled again through the same path after the timing loop. `total` is build + compile + bind (the sum of the medians), and `ns/op` is total divided by bench.c's `ops`. `bytes` is the length of `code_buffer()`.

```
./run.sh [CPU] [extra clbench flags]     # every kernel x config, one process each, taskset -c ${CPU:-0}
../.build/cranelift/release/clbench --config speed|fast -k <kernel> [--bind fresh|shared|arena] [-t ms] [-r rounds] [--verify] [--dump dir]
```
`run.sh` builds into `../.build/cranelift` with `cargo build --release --locked`. `--dump dir` writes, for each kernel and config:
- `.vcode`: Cranelift's own post-regalloc disassembly (`set_disasm`);
- `.clif`: the IR after compile;
- `.bin`: the raw bytes (disassemble with `objdump -D -b binary -m aarch64`).

## Kernel notes
The kernels use the same arithmetic, the same unsigned loop compares, and the same signed int to f64 conversion as bench.c. In fp, `fcmp gt` + `brif` gives "count unless unordered or ≤ 0.5". The fp constants are materialised once in the entry block, just as bench.c hoists them into FR2... With `CRANELIFT_FP_INLINE=1` they are emitted at their uses instead: speed code is unchanged, and fast runs about 4% faster with 88 fewer bytes.
