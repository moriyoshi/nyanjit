# Cross-platform E2E tests

`./e2e/run.sh` builds a Debian trixie container image and runs the Starlark
suite in `e2e/matrix.star`. The image includes Go 1.26.9 and 1.27.2, QEMU user
and system emulators, Wine, the test harness, and prebuilt guest disks. The
harness is a separate Go module; its Starlark dependency is not in the
library module.

Run from the repository root:

```sh
./e2e/run.sh
./e2e/run.sh e2e/ppc64.star -os linux -arch ppc64le
./e2e/run.sh e2e/smoke.star -os openbsd
./e2e/run.sh e2e/matrix.star -target freebsd/arm64 -suite matrix
NYANJIT_E2E_VM_OSES=freebsd,netbsd ./e2e/run.sh e2e/smoke.star -os netbsd
NYANJIT_E2E_OPENBSD_ARCHS=amd64,i386 ./e2e/run.sh e2e/smoke.star -os openbsd
```

The first build downloads several gigabytes of release media and installs
guests under QEMU TCG, so allow hours. Docker BuildKit caches each VM stage
independently. `VM_OSES` and `OPENBSD_ARCHS` select stages; excluded targets
receive a `NO-RUNNER` marker. The final Dockerfile has one `COPY` layer per
guest, so unchanged guests stay cached and can be pulled independently.
Nothing downloads VM images at test time.

Docker, a Linux 6.7+ kernel, and permission to run a privileged
container are required. `/dev/kvm` is optional; `run.sh` passes it only when
present. The entrypoint registers QEMU in a private user and
mount namespace with the `F` binfmt flag. The tonistiigi registrations also use
`P` to preserve the original program name and every argument. Host binfmt registrations are left
alone. Runtime QEMU boots an overlay in the named `nyanjit-e2e-cache` volume
backed by a compressed, read-only qcow2 in the image. Keep room for the image,
overlays, Wine prefixes, and results. KVM is used for a same-architecture
guest when available; all other guests use TCG. Results are printed as a text
matrix and written to `/cache/results/junit.xml` in the volume. A failure
sets a nonzero exit status.

On the verified ARM64 build, `docker image inspect` reported a 15.36 GB image;
the guest `COPY` layers account for about 10.0 GB of that. Budget additional
space for BuildKit's downloaded media and intermediate disks during the first
build. The named volume holds only runtime overlays, build outputs, and results.

## Guest images

| Target | Image and runner | Build-time check |
| --- | --- | --- |
| OpenBSD/amd64, 386, arm64, riscv64 | OpenBSD 7.9, separate VM per architecture | Unattended serial install; SSH and `uname -m` on a fresh boot |
| OpenBSD/arm, ppc64 | Experimental OpenBSD 7.9 image stages | `NO-RUNNER` with the boot failure and serial tail if installation cannot complete |
| FreeBSD/amd64, arm64 | FreeBSD 15.1 BASIC-CLOUDINIT UFS images | NoCloud seed, SSH and `uname -m` on two boots |
| FreeBSD/386 | amd64 VM with `COMPAT_FREEBSD32` | Actual FreeBSD/386 Go probe |
| FreeBSD/arm | arm64 VM under TCG with AArch32-capable Cortex-A72 | Actual FreeBSD/arm Go probe; KVM is unsuitable on hosts without AArch32 EL0 |
| NetBSD/amd64, arm64, arm | NetBSD 11.0 images; arm uses its own armv7 VM | Serial configuration; SSH and `uname -m` on a fresh boot |
| NetBSD/386 | amd64 VM with `COMPAT_NETBSD32` | Actual NetBSD/386 Go probe |
| DragonFly/amd64 | DragonFly BSD 6.4.2 live USB image, used as a persistent writable system disk | Serial configuration; SSH and `uname -m` on a fresh boot |
| illumos/amd64 | OmniOS r151058 cloud image | NoCloud seed, SSH and `uname -m` on two boots |
| solaris/amd64 | **Solaris (illumos substitute)**, executed on the OmniOS VM | Same guest check; this is not Oracle Solaris |

DragonFly's live USB already has a writable UFS root. The builder grows its
partition and filesystem, then configures SSH and the serial console in
place. This avoids a second installer disk while preserving configuration
across boots.

NetBSD's ARM images mount `/tmp` as a 32 MiB tmpfs. The runner copies test
binaries to `/root` on its resized disk so parallel package tests do not fill
the tmpfs.

OpenBSD release sets are checked against a pinned SHA256.sig, verified with
the repository's OpenBSD 7.9 signify public key. FreeBSD and NetBSD media
use pinned hashes from their release checksum files. DragonFly's release
publishes MD5, and its compressed media also has an independently pinned
SHA256 in the Dockerfile. OmniOS is checked against its published SHA256.
Each image stage generates an ed25519 key, grants root access to that key
only, boots the resulting disk, and checks SSH and `uname -m`. The final image
contains the compressed disks, keys, `boot.json`, and serial logs; source media
and temporary installation disks stay in build stages. A failed required guest
check fails `docker build`.

The OpenBSD armv7 and powerpc64 stages are experimental. Their boot attempts
can produce a `NO-RUNNER` file with a reason and the last serial output;
the matrix reports those rows explicitly. In the current build, armv7 EDK2
could not reach the installer, and powernv9 stopped after its early firmware
message. Those targets require their own guest kernels. The exact QEMU CPU
model is recorded in `boot.json`. OpenBSD execute-only memory results under TCG,
especially amd64 PKU and arm64 protection, may differ from real hardware.

Oracle Solaris 11.4 CBE media requires an Oracle account and license
acceptance, so it is not baked or redistributed. The `solaris/amd64` row is
labelled as an OmniOS substitute. An optional bring-your-own Solaris 11.4
installer remains a TODO: its unattended install, SSH setup, and check boot
must be implemented before it can replace the substitute. No Oracle media is
downloaded by this build.

Linux foreign-architecture tests use QEMU user mode. Windows tests use
Hangover Wine on ARM64 hosts and Debian Wine on AMD64 hosts. Guest binaries
have unique paths per Go version and variant, so parallel runs do not
overwrite each other. The matrix runs `go vet`, compiles every package for
each selected target, then executes its test binaries. The smoke suite runs
`examples/add` and checks for `42` or the target's expected unsupported
result. `-jobs` limits concurrent work, and all waits have deadlines.

Linux/ppc64le expects native integer execution rather than the unsupported
stub. Big-endian Linux/ppc64 is not supported: it is cross-built only and has
no runner. The ppc64le package tests exercise both memory byte orders,
scope-driven spills, label arguments, large branches, symbol relocations,
128-bit returns and multiplication, and the portable snake example. The matrix
covers POWER8/9/10 with Go 1.26.9 and 1.27.2. Each CPU variant checks
explicit generated-code levels through `compile.TargetSpec.CPU.Level`,
including rejection of newer levels, native integer `MADDLD` execution,
aliased operands, and truncation of 8/16/32-bit arithmetic. `GOPPC64`
selects the Go compiler baseline; generated code independently defaults to POWER8. Outbound C calls, Linux
syscalls and registered pointer-free Go calls are covered, including twelve Go arguments, GC, stack copying, reentry and panic
unwinding. C entries preserve nonvolatile GPRs, FPRs, vectors and CR2-CR4.
C-created threads are supported when real cgo supplies runtime TLS and
thread attachment; the separate C integration module tests that path.
Variable-count shift tests cover all scalar value/count widths, oversized counts, register aliasing, forced spills and scope/label transfers; GNU assembler independently verifies the native POWER8 encodings. Wide arithmetic tests cover carry chains, native high products, shared carries, XER clobbers, identity-cast flag aliases, random operands and scope/label transfers. CR/XER tests cover every scalar width, composed predicates,
call/shift invalidation and truncating-extension aliases. CTR tests exercise
zero-count guards, observable counter restoration, long branches, nested-loop
contention, live flags, side entries, and bodies that must retain GPR counts. FP32/FP64 arithmetic, special values, subnormals,
FMA contraction and single-rounding edge cases are covered. QEMU validates
instruction behavior; hardware instruction cache publication still needs verification on a POWER machine.

The additional `ppc64.star` suite cross-compiles every package as PIE and
runs real C shared-library and callback tests, including concurrent
C-created threads that reenter registered JIT code, grow the Go stack, and
trigger GC. Its C tests live in their own module under `ppc64cgo/`.

PPC64LE uses a separate `QEMU_LD_PREFIX` sysroot built from Debian glibc,
so its `/lib64/ld64.so.2` interpreter stays out of the container root.
Native container Go cross-compiles; QEMU executes target binaries. No Go
compiler runs in a PPC VM.

The library pins purego commit
[`eafd1cca3a52`](https://github.com/ebitengine/purego/commit/eafd1cca3a52a3b6fb17f36baf96a46a0e33c53d)
for its PPC64LE PIE trampoline and callback entry fixes.


To add a target, add its supported `GOOS/GOARCH` pair and variants in
`targets.star`, add a verified media and guest stage in
`container/Dockerfile`, record its runtime boot settings in `boot.json`,
update `harness/backend.go` if it needs a new runner mapping, and flip the
capability expectation when native nyanjit support lands. A compile pass
alone does not count as an execution pass.

## Portable SIMD acceptance

The package matrix executes the implemented integer SIMD subset on Linux/amd64,
arm64, 386, riscv64, ppc64le and loong64 with both Go toolchains. Native lowering
uses SSE2, NEON or optional LSX; unsupported operations and programs with labels
or Vars use scalar lanes. `compile` contains embedded assembly goldens for the
int32x4 add and byte-equality-mask examples. Tests compare native lowering and
`compile.VectorsOff()` against independent integer lane oracles, including
unaligned buffers, 128/256/512-bit shapes, full shift counts, signed comparisons,
typed vector/mask loops, scope exports, complete vector spills and native C/Go
call boundaries. The 386 control emitter accepts incoming stack arguments;
its Go result slot follows the entry arguments.

The review's NEON byte-mask vector returns 9249. Floating SIMD, reductions,
conversions and scalable vectors belong to subsequent phases; the design records
their proposed quiet-NaN, optional fusion, pairwise reduction and scalable-VL
contracts in [SIMD design](../docs/simd-design.md).

For each architecture, the existing entrypoint runs the same tests:

```sh
./e2e/run.sh e2e/matrix.star -os linux -arch arm64
```

## Backend CPU acceptance suites

`amd64.star`, `arm64.star`, `riscv64.star`, and `386.star` run every package on
Go 1.26.9 and 1.27.2. The AMD64 profiles are `qemu64` and `max`; 386 uses
`qemu32,-sse3` (SSE2) and `max`. A softfloat Go probe runs with
`qemu32,-sse2` and verifies `ErrCPUUnsupported` before native emission. RISC-V explicitly disables or enables Zba/Zbb/Zbs. ARM64
uses `cortex-a72` and `max` on foreign hosts; on a native ARM64 host both runs
use the host CPU and the explicit baseline/profile compiler tests.

For example, after building the image:

```sh
docker run --rm --privileged --device /dev/kvm \
  -v "$PWD":/src -v nyanjit-e2e-cache:/cache nyanjit-e2e \
  nyanjit-e2e -jobs 4 -suite amd64-baseline e2e/amd64.star
```

`run(..., env = ["QEMU_CPU=..."])` overrides the default emulated CPU for that
process. The suite shares completed builds between profiles, so an executable
is never overwritten while another profile is running it. Host binfmt entries
are not changed. See [backend parity evidence](../docs/backend-parity.md).

### Scoped-variable differential fuzzing

The Linux package matrix replays `compile/varfuzz`'s compact native Go fuzz
corpus and all 256 legacy seed/config pairs on supported backends. Legacy
programs are stored once as compressed streams; minimized fuzz inputs each
check every applicable configuration. Go 1.27.2 baseline-profile runs also execute 2,000
fixed sweep cases per arithmetic mode, covering leaf, native C-call and mixed
Go/C-call programs. Go-entry cases compare `Func` with `Call`; both stack and
workspace spilling are checked. 386 now runs both integer and mixed-FP modes,
including all 128 formerly skipped legacy FP/config pairs. Scalar SSE2 and
native packed integer SSE2 are supported, including inline math/accuracy tests,
dctlogo, and native/scalar SHA-256 digests. The baseline profile rejects explicit
PCLMULQDQ requests; max exercises the instruction. XMM residency,
mixed FP/vector pressure, FP variable/label homes, caller-saved XMM
preservation across C/Go calls, and overflow label cycles
are covered on 386 for both FP widths and stack/workspace storage. Literal
pools are shared and aligned; rebinding tests keep two mappings live and check
that each load points into its own pool. Mapping tests verify patching happens
while writable, before executable sealing and runtime registration. Paired integer
shifts, multiplication and Select now use workspace without a platform frame.
Insufficient stack budgets and managed-frame limits are counted separately from
executions; arbitrary backend errors still fail.
The scoped-variable fuzzer currently generates integer and FP scalar programs,
not vector programs. Native vector coverage comes from differential tests for
all immediate shift counts, lane shapes, carryless products, register pressure,
GP-pair edges, and the SHA-256 example.

The 386 FP acceptance sweep can be run with:

```sh
GOARCH=386 NYANJIT_VAR_FP_ONLY=1 NYANJIT_VAR_SWEEP=3000 \
  go test ./compile/varfuzz -run '^TestScopedVarsSweep$' -v -count=1 -timeout=90m
```

Use the E2E container's private binfmt registrations on a foreign host. Set
`NYANJIT_VAR_JOBS=8` together with `-parallel=8` to raise bounded sweep
concurrency on larger hosts; the default remains four subprocesses.
Large sweeps reexecute the test binary in bounded subprocess batches, so they
also check that the container's private binfmt registrations support children.
No host binfmt registrations are needed.

Run coverage-guided fuzzing directly on native arm64 or amd64:

```sh
go test ./compile/varfuzz -run '^$' -fuzz '^FuzzScopedVars$' \
    -fuzztime=10m -fuzzminimizetime=2s -parallel=2 -timeout=12m
```

Keep new failing corpus files in
`compile/varfuzz/testdata/fuzz/FuzzScopedVars/` with their fixes. See
[scoped-variable code generation](../docs/scoped-variable-codegen.md#broader-byte-stream-fuzzing-2026-10-14)
for fixed sweeps, subprocess minimization, configuration limits and results.

## CI

Two GitHub Actions workflows run on ephemeral hosted Ubuntu 24.04 runners:

| Tier | Gate | Execution |
| --- | --- | --- |
| `ci.yml` (every push and PR) | Formatting, six Linux architecture vet passes, harness tests | AMD64 host |
| `ci.yml` | Native package tests, both Go 1.26.9 and 1.27.2 | AMD64 and ARM64 hosts; AMD64 also executes 386 directly with cgo disabled |
| `ci.yml` | All `targets.star` pairs and variants compile, including examples and test binaries | Go cross-compilation on AMD64; no container or VM |
| `e2e-linux.yml` (Go/E2E PR changes, main pushes, manual dispatch) | Package matrix and scoped-variable sweep | Linux AMD64, 386, RISC-V, PPC64LE and PPC64 in one Linux-only image |
| `e2e-linux.yml` | PIE packages and real C/callback integration | Both PPC byte orders; separate jobs per Go version, each retaining POWER8/9/10 |

ARM64 executes natively in `ci.yml`; it is not a QEMU job. Linux E2E uses
`VM_OSES=""`, `OPENBSD_ARCHS=""` and `LINUX_ONLY=true`: no VM downloads,
Wine or system emulators. Excluded guests contain `NO-RUNNER` markers.
BuildKit caches the cross-toolchain and runtime stage groups separately.
The image is built once and transferred as a compressed artifact, so fork
PRs do not need GHCR push access. Per-job JUnit artifacts, annotations and
job summaries use the read-only workflow token.

The image build's `setup-qemu-action` registers interpreters on the disposable
GitHub runner to support foreign build stages. **Do not use that action or
host binfmt installation on a development or self-hosted machine.** The test
entrypoint uses `unshare -Urm` and its own binfmt mount inside a privileged
container; a probe checks this before tests start. GitHub's Ubuntu AppArmor
behavior must be confirmed on the first hosted run; local validation cannot
verify that runner policy. Linux jobs do not require KVM.

PRs use 500 sweep programs per mode/config, main and manual runs use 2000.
Override `NYANJIT_E2E_VAR_SWEEP` locally (positive integer; default 2000):

```sh
# Build a Linux-only image, then run exactly the selected CI package gate.
NYANJIT_E2E_VM_OSES= NYANJIT_E2E_OPENBSD_ARCHS= \
NYANJIT_E2E_LINUX_ONLY=true NYANJIT_E2E_VAR_SWEEP=500 \
  ./e2e/run.sh e2e/matrix.star -target linux/ppc64le -suite matrix

# Reuse it without rebuilding. Each PPC CI job selects one Go version.
NYANJIT_E2E_IMAGE=nyanjit-e2e:local NYANJIT_E2E_VAR_SWEEP=500 \
  ./e2e/run.sh e2e/ppc64.star -target linux/ppc64le -go-version 1.27.2

./scripts/vet-all.sh
./e2e/ci/crossbuild.sh -go-version 1.27.2
```

`NYANJIT_E2E_IMAGE` skips the build and uses that image tag;
`NYANJIT_E2E_CACHE=/absolute/path` stores results and Go caches in a host
bind directory instead of the named volume. A bind cache must be writable by
the container’s mapped UID 0; CI creates a dedicated temporary directory with
mode `0777` before entering the private namespace. Explicitly empty VM selections
stay empty. `VM_OSES` also gates OpenBSD media independently of its arch list.
Outside the image the harness requests each target's exact Go toolchain
through `GOTOOLCHAIN`; the first compile may download it. The target pairs,
Go versions and CPU variants have a single source of truth in `targets.star`.
Add a pair there, update its capability expectation, and the cross-build gate
picks it up. Add a Linux runner job when its backend becomes executable.

Nightly full VM E2E and coverage-guided fuzz workflows are planned future work.
Oracle Solaris media remains excluded from every image.

## Linux/loong64

`./e2e/run.sh e2e/loong64.star -target linux/loong64` runs every package on
Go 1.26.9 and 1.27.2 under QEMU 10.2.3, with `la464,lsx=off,lasx=off` and
`max`. The property names were checked against the bundled user emulator.
The ordinary matrix defaults to that baseline; smoke now expects `42`.
CI includes the target in cross-build/vet and in the Linux E2E matrix.

L0–L3 are implemented. Its scoped-variable differential sweep uses the shared
AST/interpreter, both leaf entry ABIs, and stack/workspace spills. Set
`NYANJIT_VAR_SWEEP=3000` for the leaf acceptance sweep. The original integer
legacy streams also run with calls enabled. `NYANJIT_LOONG_CALL_SWEEP=500`
expands the separate mixed C/Go/callback call sweep. Known small-stack-budget
and 640-byte managed-frame limits are counted separately from passes; other
compile errors and mismatches fail the suite. The generic scoped-variable suite
now includes integer and FP paths, original legacy seeds, native C calls, and
mixed Go/C calls. Set `NYANJIT_VAR_SWEEP=3000` when invoking the test binary
for the larger acceptance sweep; every accepted program is replayed across
the applicable ABI, stack and workspace configurations.

The container uses musl 1.2.5 from the upstream `loong64/cross-tools`
[20260912 release](https://github.com/loong64/cross-tools/releases/tag/20260912).
Separate SHA256 values pin its ARM64- and AMD64-hosted archives. Only libc and
its COPYRIGHT notice are copied into `/opt/nyanjit/sysroots/loong64`.
Compatibility sonames satisfy Go/purego's ELF imports; `QEMU_LD_PREFIX`
is scoped to loong64 test processes and inherited by their children.
The target libc from both archives passes on strict-baseline QEMU. The
Loongson glibc 2.42 toolchain was tested and rejected: its loader uses LSX
unconditionally and faults before `main` when LSX is disabled.

Runtime tests include sixteen-argument Go calls, argument homes across stack
growth, GC, panic unwinding, JIT reentry, mixed C/Go calls, real libc calls,
128-bit C results in a0/a1, and a pthread entering a Go callback that reenters
registered JIT code. `StackNoUse` leaf execution remains covered; outbound
calls require `StackPlatform`, matching the existing execution contract.
Baseline scalar FP32/FP64, FMA, FP residency, literal pools and inline math
are supported on both CPUs. `max` also executes native LSX integer vectors;
the baseline and `compile.VectorsOff()` execute scalar lanes. The shared
differential suite covers all immediate counts, full scalar and per-lane counts,
byte masks, carryless scalar fallback, overlapping buffers and complete spills.
LoongArch-specific cases force destination aliasing, mixed FP/vector pressure,
and live vectors across C calls and Go calls with GC/stack growth. SHA-256 runs
in LSX and scalar modes and reports both listing instruction counts.
See [completion record](../docs/loong64-completion.json).

### LP64 call-boundary regressions

The loong64 and riscv64 package suites include independent C stubs checking
16-byte stack alignment, all eight register arguments and both integer results,
plus a compiled C fixture with a volatile, 16-byte-aligned `__int128` local.
They cover `execution.CallForeign`, generated foreign calls and managed C calls
from C and Go entries. The execution gateway realigns SP after the runtime's
foreign-stack transition and restores the exact incoming SP on return.
The tests also check LP64 sign extension of 32-bit C arguments and ABI-dependent
callee-preservation reports. The fixtures need no cross C compiler at test time;
see [their regeneration notes](../compile/testdata/cabi/README.md).

Both private-binfmt execution and explicit QEMU fallback preserve the guest's
original `argv[0]`. The fallback passes the executable name twice because the
container uses tonistiigi's preserve-argv0 QEMU patch. This is necessary for
`-test.run` and `-test.timeout` to reach Go's test runner.

### Managed-frame and long-chain checks

LoongArch and RISC-V managed frames permit 640 bytes, including outgoing Go
argument homes, spills, and C-entry register preservation. The former 512-byte
bound was a conservative backend policy, not a runtime metadata format limit.
For Go 1.26/1.27, 640 leaves 160 bytes of the runtime's 800-byte NOSPLIT reserve.
The invocation gateway consumes 40 bytes; LoongArch's automatically NOSPLIT
leaf functions consume at most 120. `morestack` switches to g0 without
allocating a user-stack frame. This follows Go's indirect-call convention;
per-frame limits do not prove the aggregate bound of arbitrary manually
NOSPLIT or generated-to-generated call chains.

Managed leaf entries save only C-preserved registers they actually use.
An entry with a Go call retains the full C-preserved set because the Go callee
can overwrite it. Boundary tests execute an exact 640-byte frame with live
integer/FP values, GC and stack growth, and reject 656-byte frames.

The normal suite checks instruction and deferred-expression chains in a
subprocess with a 1 MiB Go stack limit. To reproduce the larger review case:

```sh
NYANJIT_LONG_CHAIN_CHILD=1 NYANJIT_LONG_CHAIN=1200000 \
  NYANJIT_LONG_CHAIN_STYLE=instructions \
  go test ./compile -run '^TestLongValueChainCompilation$' -count=1 -v
```

`NYANJIT_LONG_CHAIN_STYLE=expressions` checks deferred value trees. Selection,
reachability, expression validation and expression materialization use worklists.
The QEMU argv regressions include test filters and repeat counts. The round 2
audit conservatively reruns amd64, 386, riscv64 and loong64 suites on both Go
versions through explicit QEMU, including the missing-SSE2 negative probe.

The Builder currently exposes native/little-endian memory helpers, without a
big-endian selector. Explicit big-endian operations remain available through
public IR and machine instructions; LoongArch execution tests cover that path.

### Optional LoongArch GC stress suite

An intermittent LoongArch `runtime.Pinner`/GC-checker failure remains under
investigation. Passing the normal package matrix does not resolve it. The
separate suite repeats the complete `compile/varfuzz` package three times per
process with `GOGC=10` and `GODEBUG=gccheckmark=1`, on both Go versions and
both LoongArch CPU profiles. Repeating in one process also exercises the
process-lifetime metadata registry as it grows. Failures are recorded in JUnit;
the suite does not retry a failing process or classify GC failures as skips.

```sh
NYANJIT_E2E_IMAGE=nyanjit-e2e:loong64-l2 ./e2e/run.sh e2e/loong64_gc.star \
  -target linux/loong64 -jobs 4 -deadline 70m \
  -junit /cache/results/loong64-gc.xml
```

Use `-go-version 1.27.2` or `-suite loong64-gc-max` to narrow the run. Each test
process has a 20-minute deadline; the harness allows another minute for
shutdown. This is a diagnostic suite, separate from the default acceptance
matrix. It uses the existing container and private binfmt namespace, with no
host binfmt changes.

The GC-checker failure also reproduces in the standard-library-only
[`probes/gcmap`](probes/gcmap/main.go) program, which neither imports NyanJIT or
purego nor emits or executes native code. It creates nested maps concurrently.
This separates an environment/runtime failure from the JIT, but does not prove
that the original Pinner panic has the same cause. To run the independent
control using the existing image:

```sh
docker run --rm --privileged -v "$PWD:/src:ro" \
  -v nyanjit-e2e-cache:/cache -e GOCACHE=/cache/go-build \
  nyanjit-e2e:loong64-l2 sh -ec '
    GOOS=linux GOARCH=loong64 CGO_ENABLED=0 /opt/go1.27.2/bin/go build \
      -o /cache/gcmap /src/e2e/probes/gcmap/main.go
    QEMU_CPU=max GOGC=10 GODEBUG=gccheckmark=1 /cache/gcmap
  '
```

The failure is intermittent. Diagnostic comparisons with
`GOEXPERIMENT=nogreenteagc` or `GOMAXPROCS=1` passed six runs each; these are
observations, not changes to the acceptance defaults or a proven fix. Go already
disables async preemption when `gccheckmark=1`; the explicit
`asyncpreemptoff=1` comparison is therefore a repeat sample, not evidence about
preemption. The native-code stress suite keeps its original GC settings and
reports failures normally.

### Selecting the LoongArch emulator

`NYANJIT_E2E_LOONG64_QEMU=debian` selects the image's stock Debian LoongArch
emulator. The default is `tonistiigi`, matching the other user-mode runners.
The image preserves the Debian binary under `/opt/nyanjit/qemu-debian/` before
copying the tonistiigi binaries. Rebuild older images before using this option.
The entrypoint prints the selected Debian QEMU version. Its registration is
confined to the container's private binfmt namespace and uses `F`; stock QEMU
supplies argv[0] itself. Explicit dispatch uses the same convention. Child test
processes inherit the selected interpreter through the private registration.

```sh
NYANJIT_E2E_IMAGE=nyanjit-e2e:local NYANJIT_E2E_LOONG64_QEMU=debian \
  ./e2e/run.sh e2e/loong64_gc.star -target linux/loong64 -jobs 4 \
  -deadline 70m -junit /cache/results/loong64-gc-debian.xml
```

With the same standard-Go executable and GC settings, tonistiigi 10.2.3 failed
7 of 40 standalone runs, while Debian 10.0.13 and a locally built, unpatched
upstream 10.2.3 each passed 40 runs. Native arm64 passed 30 runs on each Go
version. These results identify an emulator-build difference, rather than a
failure shared by all QEMU 10.2.3 builds. They do not yet isolate a patch,
compiler, or libc cause. The Debian comparison also passed the four JIT
GC-stress profiles. The original failing results remain recorded.

`max` reflects the selected emulator's supported CPU features, so include its
version when comparing results. The baseline stays explicitly
`la464,lsx=off,lasx=off`. Choosing the Debian runner does not disable Green Tea,
GC checking, or concurrent execution.

The harness's default explicit QEMU dispatch assumes the image's patched
preserve-argv0 binaries, including when finding QEMU on `PATH`. A stock qemu-user
on `PATH` is not interchangeable: it would expose the repeated binary name as
an extra guest argument. Use the bundled runners, or the documented
`NYANJIT_E2E_LOONG64_QEMU=debian` selection for stock LoongArch QEMU.

The harness discovers the library module, the `backend` toolkit module and the
separate `examples/snake_termbox` and `examples/stackvm` modules, and builds
every package of each for every target (`nestedModules` in `harness/script.go`). It builds and executes the terminal example's
tests on its supported targets (Linux amd64, arm64 and riscv64); its terminal
dependencies are not part of the root module. External Docker `FROM` images are
pinned to multi-platform manifest digests. Update the tags and digests together,
verifying that the new index includes every platform used by the Dockerfile.
