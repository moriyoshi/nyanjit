# CPU profiles

`compile.TargetSpec.CPU` declares an ISA level and optional extensions.
`compile.NewTarget` normalizes the profile once per target, and lowering reports
it in `EmissionReport.CPU`.

| Architecture | Baseline | Other levels | Optional extensions used or recognized |
| --- | --- | --- | --- |
| amd64 | x86-64-v1 | x86-64-v2, x86-64-v3, x86-64-v4 | bmi1, bmi2, lzcnt, fma, pclmulqdq, AVX families |
| 386 | i686 + SSE2 | — | bmi1, bmi2, lzcnt, popcnt, pclmulqdq; explicit sse2 accepted |
| arm64 | armv8.0-a | — | lse, sve, sve2 |
| riscv64 | rv64gc | rva20u64, rva22u64, rva23u64 | zba, zbb, zbs, v and profile mandates |
| loong64 | la64v1.0 | — | lsx (128-bit integer SIMD), lasx (implies lsx; no 256-bit lowering) |
| ppc64le | power8 | power9, power10 | vsx, maddld, ca32 |

An explicit `Architecture` with an empty CPU profile selects that architecture's
baseline. Text emission never detects the CPU of a different architecture.
An implicit host architecture with an empty profile uses verified host features
(PPC keeps its POWER8 default). Explicit native profiles are checked before
executable allocation; unverified requirements produce
`compile.ErrCPUUnsupported`. For compatibility, these errors also match
`compile.ErrBackendLimit` through `errors.Is`, without using its generic
backend-limit message.
`cpuprofile.Host()` exposes the same conservative discovery result, detached
from internal state.

Discovery uses process-enabled feature bits from `x/sys/cpu`, CPUID on x86,
and Linux auxiliary-vector capabilities on POWER and LoongArch. LoongArch
uses the LSX/LASX HWCAP bits; failed discovery leaves an extension-free
`la64v1.0` profile. LASX implies LSX; native integer SIMD uses 128-bit LSX
instructions, including when LASX is available. Failed discovery keeps the
scalar baseline. RISC-V discovery does not infer full RVA conformance from Zbb/V:
reservation-set, cache-block and memory-access guarantees are not all exposed
by the kernel. Use `rv64gc` plus verified extensions for native execution when
full RVA mandates cannot be verified. RVA text profiles include the existing
Go RV64GC requirement for instruction-fetch synchronization.

`TargetSpec.Vectors` controls SIMD legalization separately: `VectorsAuto()` (the
zero value) enables supported SIMD from the resolved CPU; `VectorsOff()` forces
scalar SIMD legalization. Enabling SVE, V, or AVX512 does not claim a native
vector lowering that is not implemented. `Target.Vectors()` reports the
implemented 128-bit SSE2, NEON or LSX baseline, or scalar lowering. Native POWER
profiles check the
minimum CPU level as well as extensions, so POWER9 cannot accept POWER10.
`maddld` and `ca32` require an explicit POWER9-or-newer level; they are rejected
with POWER8, including its default profile.

AVX512 extensions imply AVX512F; AVX512F, AVX2, FMA and F16C imply AVX. AVX
implies XSAVE, with native execution still checked against process-enabled
capabilities. Profiles are normalized once when each AMD64 target/emitter is
created, and size/flag queries reuse that detached profile.

Request FMA on amd64 with `CPU.Extensions: []string{"fma"}`; native compilation
checks it against the host like any other extension.

Profile definitions follow the [x86-64 psABI](https://gitlab.com/x86-psABIs/x86-64-ABI)
and the ratified RISC-V [RVA20/22](https://github.com/riscv/riscv-profiles/blob/main/src/profiles.adoc)
and [RVA23](https://github.com/riscv/riscv-profiles/blob/main/src/rva23-profile.adoc)
specifications. A profile enables instruction selection; it is not a benchmark
or a hardware certification claim.

386 requires SSE2 for every generated function. The profile level remains `i686`;
explicit `sse2` is accepted for compatibility. Missing or failed SSE2 discovery
rejects native compilation with `compile.ErrCPUUnsupported`, including integer
functions. Cross-target assembly uses the declared baseline without host probing.

### 386 scalar floating point

The i686 baseline includes SSE2 even when `CPU` is empty. Scalar FP32/FP64
arithmetic uses SSE2, never x87 or softfloat. Native compilation fails with
`ErrCPUUnsupported` if SSE2 is absent or cannot be verified; cross-target
assembly assumes the resolved baseline. Spelling `sse2` explicitly is accepted.
FMA is not enabled on 386, including with multiply-add contraction hints.

The acceptance runner uses `qemu32,-sse3` and `max`. In the pinned QEMU build,
`qemu32` includes SSE2 and `pentium4` is not a CPU model, so the negative probe
uses `qemu32,-sse2`. That Go test driver is built with `GO386=softfloat` solely
to let the driver start before NyanJIT rejects native compilation.

The 386 inline math library is also supported. Its paired integer operations
can spill into workspace without using a platform frame. Native packed SSE2
integer SIMD is enabled by default; FMA remains unsupported. Optional
`pclmulqdq` is accepted in CPU profiles or `VectorsRequire` and implies SSE2.
CPUID discovery reports it for both x86 process widths. `VectorsOff()` forces
scalar vector lowering. XMM3–7 hold vectors, scalar FP values,
variable homes and label parameters; XMM0–2 remain expansion temporaries.
All XMM registers are caller-saved for C and Go ABI0 calls. Constants load
directly from a shared read-only literal pool. SSE2 uses absolute disp32 loads;
each binding relocates them before the executable seal, without changing the
CPU baseline. FP masks use the same pool and SSE2 bitwise instructions.

### LoongArch LSX

`la64v1.0` does not mandate LSX. With `VectorsAuto()`, the resolved profile
controls native SIMD: an implicit host target uses HWCAP discovery, while an
explicit baseline target retains scalar lanes. `VectorsOff()` forces
scalar legalization even when the CPU has LSX. `VectorsRequire("lsx")` enables 128-bit
integer lowering and adds the CPU requirement when the CPU is unspecified.
An explicitly supplied CPU profile without that extension returns
`ErrCPUUnsupported`. `lasx` implies `lsx`; legalization still emits 128-bit
LSX pieces, with 256/512-bit values split. Carryless multiplication remains
scalarized. LASX instructions are not emitted.
