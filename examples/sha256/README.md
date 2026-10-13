# SHA-256 with portable SIMD

```sh
go run ./examples/sha256
go run ./examples/sha256 abc "a second message"
go run ./examples/sha256 -scalar abc
go run ./examples/sha256 -assembly amd64 > /tmp/sha256-amd64.s
go run ./examples/sha256 -assembly arm64 > /tmp/sha256-arm64.s
```

Each lane of `Vec(4,32)` hashes one independent message. Go handles padding,
transposes big-endian message bytes into native `uint32` words, and iterates
blocks. The Builder emits the complete message schedule, 64 rounds, and state
feed-forward. Messages may have different lengths; a lane's final digest is
captured before the remaining lanes continue. Arguments are processed in groups
of four, with unused lanes ignored. No padded copy of a whole message is needed.

The generated compression function takes pointers to `[8][4]uint32` state and
`[16][4]uint32` scratch. It overwrites the message scratch with its rolling
schedule. Both buffers are pinned Go heap allocations for the duration of the
calls. They contain no Go pointers. Input transposition and digest serialization
make the example independent of host byte order.

SSE2 on AMD64 uses `paddd`, `pand`, `pxor`, `psrld` and `pslld`; rotations combine
packed shifts and `por`. ARM64 NEON uses `add`, `and`, `eor`, `ushr` and `shl`,
with rotations using `ushr` + `sli` (right) or `shl` + `sri` (left) when source
and destination differ. Aliased rotations preserve the input through V30 and
combine opposite shifts with `orr`. This also adds native lowering for 32-bit
vector immediate logical shifts/rotations to the library. Logical counts at
least 32 yield zero; rotation counts are reduced modulo 32. Other lane widths
and value-count shifts retain their existing scalar fallback on those targets. Rotation reports
are `Expanded`; the clobber report includes XMM0 on AMD64 and V30 only for
aliased ARM64 rotations, excluding rotations by a multiple of 32.

The round loop is unrolled when constructing the program because the current
legalizer scalarizes programs with labels or mutable Vars. The 16-word schedule
lives in caller memory to bound live vector state. Each round uses `Enter`/`Leave`
to materialize and export its two updated state words; its other temporaries
expire immediately. LoongArch uses LSX packed arithmetic, logical shifts and
`vrotri.w` when the CPU profile includes LSX. The baseline without LSX, other
scalar backends, and `-scalar` use portable scalar lanes. `Code.Call` supplies workspace when
allocation needs spills. The program is compiled once per invocation and reused
for every group and block.

This layout exploits parallel messages; it does not shorten the dependency chain
of a single message. The example does not currently emit Intel SHA or ARM SHA2
instructions. Native measurements below compare it with both the standard
library's SHA2 path and its generic fallback. A streaming single-message API and
dedicated SHA instructions can be added separately.

The algorithm and constants follow FIPS 180-4 sections 4.1.2, 4.2.2, 5.1.1,
5.3.3 and 6.2.2. The default output includes the standard empty and `abc` digests.

## Tests

```sh
go test ./examples/sha256 -run '^TestSHA256$'
```

The ordinary test suite checks both SIMD and forced-scalar modes against
`crypto/sha256`. It covers lengths 0, 1, 55, 56, 63, 64, 65, 119, 120, 128 and
1000, mixed batches of one through four messages, and eleven messages through
the same grouping helper used by the CLI. Backends without native SIMD run the
scalar cases. The tests execute on all six supported Linux architectures.

## Benchmarks

Reproduce the measurements with:

```sh
# Set CPU to one available physical core; omit taskset on other platforms.
CPU=5
GOMAXPROCS=1 taskset -c "$CPU" go test ./examples/sha256 -run '^$' \
  -bench '^BenchmarkSHA256' -benchtime=500ms -count=5 -cpu=1

# ARM64: disable SHA2 only for this process to measure Go's generic fallback.
GODEBUG=cpu.sha2=off GOMAXPROCS=1 taskset -c "$CPU" \
  go test ./examples/sha256 -run '^$' \
  -bench '^BenchmarkSHA256$/.*/GoStdlib$' -benchtime=500ms -count=5 -cpu=1
```

`BenchmarkSHA256` measures four complete hashes per operation, including the
JIT example's pinning, padding, transposition, calls, buffers and result
allocation. Compilation is excluded. Go's inputs are converted to byte slices
before timing. Every JIT case checks all four digests against `crypto/sha256`
before measurement. `MB/s` counts actual input bytes across the batch, excludes
padding, and uses decimal megabytes. All methods run sequentially on one core.

### Native ARM64 baseline (2026-10-15; before shift-and-insert rotations)

Go 1.27.2, Linux/arm64, Cortex-X925, process affinity CPU 5, 3.9 GHz, one Go P.
Medians of five 500 ms samples; these are hardware measurements, with no QEMU.
The default Go implementation uses the host's SHA2 instructions; the generic
column comes from the process-local `GODEBUG=cpu.sha2=off` run.

| Bytes per message | JIT NEON | JIT scalar | Go SHA2 | Go generic |
| --- | ---: | ---: | ---: | ---: |
| 32 B | 203.83 MB/s | 22.08 MB/s | 559.87 MB/s | 179.91 MB/s |
| 64 B | 258.05 MB/s | 24.70 MB/s | 922.10 MB/s | 195.80 MB/s |
| 1 KiB | 680.05 MB/s | 48.28 MB/s | 2,206.74 MB/s | 397.21 MB/s |
| 16 KiB | 748.95 MB/s | 51.81 MB/s | 2,419.27 MB/s | 424.03 MB/s |
| Mixed: 0, 55, 56, 1024 B | 200.51 MB/s | 13.25 MB/s | 1,707.49 MB/s | 347.50 MB/s |

For four 16 KiB messages, NEON takes 87.50 microseconds per batch. It is 1.77x
faster than Go's generic path and 3.23x slower than Go's SHA2 implementation.
The mixed batch demonstrates idle-lane cost: the JIT runs all four lanes for the
longest message's block count and is slower than Go generic in that case.
Grouping messages by padded block count should improve utilization.

`BenchmarkSHA256Compression` excludes preparation and padding, using persistent
pinned state and transposed scratch. It includes `Code.Call`; the state and
schedule evolve without resetting between iterations. NEON measures 307.9 ns
per four blocks (831.35 MB/s), versus 4,973 ns (51.48 MB/s) for scalar JIT.

The baseline NEON body is 16,784 bytes with **zero spill workspace**. The scalar body is
169,924 bytes and requires 1,056 workspace bytes. Under the example's default
`StackNoUse` policy, `Code.Call` maps and unmaps that workspace on every block
batch. Those syscalls are included in the scalar timings, and their mapped
bytes are not counted by Go's heap allocation metrics. The scalar/native ratio
therefore includes workspace costs as well as the instruction difference.

At 1 KiB per message, the JIT wrapper allocates 784 bytes in 20 allocations per
batch; at 16 KiB it allocates 4,624 bytes in 260 allocations. Compression alone
allocates 16 bytes per call for the variadic arguments. Go's `Sum256` comparison
allocates zero bytes. Reusing pinned buffers and reducing per-block call and
marshalling costs are useful follow-up work; this benchmark leaves the example
unchanged.

Startup is measured separately:

| Operation | Median | Go heap bytes/op |
| --- | ---: | ---: |
| Construct and finish Builder program | 0.938 ms | 2,335,559 |
| Lower, map and close native NEON code | 16.57 ms | 22,281,505 |
| Lower, map and close forced-scalar code | 191.24 ms | 266,351,397 |

Compilation reuses the immutable program and includes executable allocation and
`Close`, so native Builder construction plus compilation costs about 17.5 ms.
Compile once and reuse the code. These results describe this CPU and Go version;
AMD64 performance has not been measured on hardware.

### ARM64 shift-and-insert rotations

The rotate refinement uses two instructions when destination and source differ;
aliased rotations retain the three-instruction V30 form. The SHA-256 kernel's
576 rotations all use the two-instruction form. Before/after listing counts
exclude literal-pool data:

| Target | Before | After |
| --- | ---: | ---: |
| ARM64 instructions | 3,938 | 3,362 |
| AMD64 instructions | 5,935 | 5,935 |
| ARM64 code and literal bytes | 16,784 | 14,480 |

A fresh before/after `BenchmarkSHA256Compression/JITSIMD` run on the same
Cortex-X925 core, Go 1.27.2, `GOMAXPROCS=1`, CPU affinity 5, five 500 ms
samples per version, gives these medians:

| Metric (four blocks per operation) | Before | After |
| --- | ---: | ---: |
| Time | 319.1 ns/op | 304.8 ns/op |
| Throughput | 802.14 MB/s | 839.95 MB/s |
| Heap allocation | 16 B/op, 1 alloc/op | 16 B/op, 1 alloc/op |
| Spill workspace / stack | 0 / 0 bytes | 0 / 0 bytes |

That is about 4.5% less time and 14.6% fewer instructions in this kernel.
The earlier full-hash table is retained as a baseline; these new measurements
cover compression only. AMD64 instruction counts are from cross-emitted
listings, without an AMD64 hardware timing claim.

The compression measurement command was:

```sh
GOMAXPROCS=1 taskset -c 5 go test ./examples/sha256 -run '^$' \
  -bench '^BenchmarkSHA256Compression$/JITSIMD$' -benchtime=500ms -count=5 -cpu=1
```

### LoongArch LSX acceptance

Under the existing QEMU 10.2.3 container on Go 1.26.9 and 1.27.2, the same
compression program emits **41,770 scalar instructions** and **2,586 LSX
instructions**, excluding literal data. That is 93.8% fewer instructions.
`TestSHA256` verifies every digest against `crypto/sha256`, including grouped
messages and padding boundaries, with LSX enabled and with scalar lanes forced.
The `la464,lsx=off,lasx=off` profile verifies the scalar baseline, and `max`
verifies both paths. These are instruction counts and QEMU correctness results;
no LoongArch hardware timing was measured.

```sh
NYANJIT_E2E_IMAGE=nyanjit-e2e:loong64-gc-full ./e2e/run.sh \
  e2e/loong64.star -target linux/loong64 -jobs 4
# In the container, with a cross-compiled LoongArch SHA test executable:
QEMU_CPU=max QEMU_LD_PREFIX=/opt/nyanjit/sysroots/loong64 \
  ./sha.test -test.run 'Test(SHA256|Loong64SHA256InstructionCounts)$' -test.v
```
