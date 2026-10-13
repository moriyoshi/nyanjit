# Target Profiles and Port Contracts

## Summary

Portable operations retain their semantic meaning until target selection. CPU profiles and ABI rules determine legal encodings; emulation establishes execution evidence but cannot establish real instruction-cache coherence.

## Key Facts

- Shifts saturate using the entire unsigned count; arithmetic shifts sign-fill, and rotates reduce modulo width.
- Explicit cross-target CPU selection is deterministic and never probes the host.
- Native compilation rejects unverified extensions. RISC-V Zbb/V discovery does not establish full RVA conformance.
- Big-endian linux/ppc64 and ELFv1 were removed from supported execution on 2026-10-15; later records are historical/cross-build-only.
- 386 requires i686 plus SSE2, including integer-only generated functions.

## Details

Backend parity brought variable shifts/rotates, unsigned high multiply, carry/borrow, zero counts, independent S/Z/C facts and compact countdown loops from POWER to amd64, arm64, riscv64 and 386. Selection/allocation handles CL/RAX/RDX and paired fixed operands. CPU selection is a profile; vector legalization has its own control. POWER9/POWER10 may expose the same optional features, so minimum-level checks must compare levels explicitly.

The first snapshot (5ba43cd) had five targets, with experimental POWER8 and big-endian outbound C support. The PPC amendments added FP/FMA, callbacks, PIE and C-created-thread reentry while preserving C nonvolatile GPRs, FP/vector registers and CR2-CR4. ELFv1 descriptors differed from ELFv2 calls. No `runtime.iscgo` mutation was used. The historical matrix reported 803 passes and six skips. POWER9+ could contract multiply-add into `MADDLD`. This does not restore removed big-endian support.

Cross-target listings became possible by replacing host-build-tagged files with `_<arch>_target.go` files, such as `compile/backend_amd64_target.go`. `compile/cross_assembly_test.go` exercised listing foreign Linux targets from any host.

LoongArch L0-L4 cover integer execution, C/Go calls, runtime registration/unwinding, scalar FP/FMA/math and 128-bit LSX. LASX is recognized but never emitted. Go register assignments were checked against 1.26.9 and 1.27.2 sources. LP64D only preserves the low 64 bits of f24-f31: live vectors cannot survive in vr24-vr31 across calls. The port uses musl 1.2.5 from loong64/cross-tools; the glibc 2.42 loader was rejected for unconditional LSX.

LoongArch and RISC-V C-call paths realign after runtime foreign-stack transitions and sign-extend typed unsigned 32-bit C arguments per LP64. Go calls clobber R21 on LoongArch. Outbound calls require `StackPlatform` on every backend. Managed loong64/riscv64 frames permit 640 bytes: 800-byte NOSPLIT reserve minus a 40-byte gateway and up to 120 bytes of NOSPLIT leaf. Managed leaves no longer save the full C preserved set unconditionally.

In the port review, raising those managed-frame limits reduced expected `Go/C/Platform` limits from 16.3% of attempts to zero. This records acceptance coverage, not a runtime speedup.

Iterative reachability, scheduling and deferred materialization replaced recursion that overflowed a 1 MiB Go stack at 10,000 chained operations; 1,200,000 operations then compiled. A 386 syscall fixture using `ConstNative(39)` was wrong because Syscall requires 64 bits while native width is 32.

## Files

- `docs/cpu-profiles.md`, `docs/backend-parity.md`: portable semantics and profile contracts.
- [Target validation archive](../ARCHITECTURE.md#target-validation-records): all six historical completion records, including CPU/version and emulator evidence.
- `backend/target/`, `compile/*target.go`: target encoders and lowering.
- `.github/workflows/ci.yml`, `.github/workflows/e2e-linux.yml`, `e2e/crossbuild.star`: native/version/cross-build coverage.

## Test Coverage

Parity acceptance recorded 462 package/build checks (120 amd64, 120 arm64, 116 riscv64, 106 386), then 1,465 Linux matrix checks with 16 skips on Go 1.26.9/1.27.2. LoongArch's integer/FP sweep ran 178,562 executions. Missing SSE2 is negatively tested with a `GO386=softfloat` driver on `qemu32,-sse2`. These counts describe historical harness coverage, not independent test cases.

## Pitfalls

Do not infer hardware cache publication from QEMU. Outstanding hardware evidence and stale completion checklists are tracked in TODO.md. Emulator-specific GC and mutex failures have separate pure-Go reproducers; see [verification practice](verification-benchmarking-and-review-practice.md).

## Sources

Consolidated from these headings in `.agents/docs/JOURNAL.md`:

- 2026-10-13 — What the tree held at the first snapshot, and the same-day PPC64 work
- 2026-10-14 — Backend parity with PPC64, and CPU profiles
- 2026-10-15 — CI, and big-endian PPC64 dropped
- 2026-10-15 to 2026-10-16 — 386 floating point (P0-P4) and native SSE2 SIMD (P5)
- 2026-10-16 — LoongArch port (L0-L4), and a GC failure traced to the emulator build
