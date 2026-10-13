# CPU profiles

`abi.CompileOptions.CPU` declares an ISA level and optional extensions. The
compiler normalizes the profile and reports it in `EmissionReport.CPU`.

| Architecture | Baseline | Other levels | Optional extensions used or recognized |
| --- | --- | --- | --- |
| amd64 | x86-64-v1 | x86-64-v2, x86-64-v3, x86-64-v4 | bmi1, bmi2, lzcnt, fma, AVX families |
| 386 | i686 | — | sse2, bmi1, bmi2, lzcnt, popcnt |
| arm64 | armv8.0-a | — | lse, sve, sve2 |
| riscv64 | rv64gc | rva20u64, rva22u64, rva23u64 | zba, zbb, zbs, v and profile mandates |
| ppc64 / ppc64le | power8 | power9, power10 | vsx, maddld, ca32 |

An explicit `Architecture` with an empty CPU profile selects that architecture's
baseline. Text emission never detects the CPU of a different architecture.
An implicit host architecture with an empty profile uses verified host features
(PPC keeps its POWER8 default). Explicit native profiles are checked before
executable allocation; unverified extensions produce `ErrBackendLimit`.
`cpuprofile.Host()` exposes the same conservative discovery result, detached
from internal state.

Discovery uses process-enabled feature bits from `x/sys/cpu`, CPUID on x86,
and Linux auxiliary-vector capabilities on POWER. Failed discovery keeps the
baseline. RISC-V discovery does not infer full RVA conformance from Zbb/V:
reservation-set, cache-block and memory-access guarantees are not all exposed
by the kernel. Use `rv64gc` plus verified extensions for native execution when
full RVA mandates cannot be verified. RVA text profiles include the existing
Go RV64GC requirement for instruction-fetch synchronization.

`Features` controls SIMD legalization separately: nil enables supported SIMD
from the resolved CPU; an empty slice forces scalar SIMD legalization. Enabling
SVE, V, or AVX512 does not claim a native vector lowering that is not implemented.
`Vectors` continues to report the implemented 128-bit SSE2/NEON baseline.
`PPC64CPU` is retained for compatibility, and must agree with `CPU.Level` if both
are provided; agreement is case-insensitive. Native POWER profiles check the
minimum CPU level as well as extensions, so POWER9 cannot accept POWER10.
`maddld` and `ca32` require an explicit POWER9-or-newer level; they are rejected
with POWER8, including its default profile.

AVX512 extensions imply AVX512F; AVX512F, AVX2, FMA and F16C imply AVX. AVX
implies XSAVE, with native execution still checked against process-enabled
capabilities. Profiles are normalized once when each AMD64 target/emitter is
created, and size/flag queries reuse that detached profile.

`AMD64FMA` remains as a deprecated compatibility field. True adds `"fma"` to an
amd64 profile; false leaves `CPU` unchanged. New callers should use
`CPU.Extensions: []string{"fma"}`. Both spellings retain the native capability
check.

Profile definitions follow the [x86-64 psABI](https://gitlab.com/x86-psABIs/x86-64-ABI)
and the ratified RISC-V [RVA20/22](https://github.com/riscv/riscv-profiles/blob/main/src/profiles.adoc)
and [RVA23](https://github.com/riscv/riscv-profiles/blob/main/src/rva23-profile.adoc)
specifications. A profile enables instruction selection; it is not a benchmark
or a hardware certification claim.
