# Cross-platform E2E tests

`./e2e/run.sh` builds a Debian trixie container image and runs the Starlark
suite in `e2e/matrix.star`. The image includes Go 1.26.9 and 1.27.2, QEMU user
and system emulators, Wine, the test harness, and prebuilt guest disks. The
harness is a separate Go module; its Starlark dependency is not in the
library module.

Run from the repository root:

```sh
./e2e/run.sh
./e2e/run.sh e2e/ppc64.star -os linux -arch ppc64
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

Linux/ppc64 and Linux/ppc64le now expect native integer execution rather than
the unsupported stub. Their package tests exercise both memory byte orders,
scope-driven spills, label arguments, large branches, symbol relocations,
128-bit returns and multiplication, and the portable snake example. The matrix
covers POWER8/9/10 with Go 1.26.9 and 1.27.2. Each CPU variant checks
explicit generated-code levels through `CompileOptions.PPC64CPU`,
including rejection of newer levels, native integer `MADDLD` execution,
aliased operands, and truncation of 8/16/32-bit arithmetic. `GOPPC64`
selects the Go compiler baseline; generated code independently defaults to POWER8. Outbound C calls, Linux
syscalls and registered pointer-free Go calls are covered, including twelve Go arguments, GC, stack copying, reentry and panic
unwinding. C entries preserve nonvolatile GPRs, FPRs, vectors and CR2-CR4.
Runtime-originating C callbacks are covered on both byte orders, including
both big-endian ELF pointer conventions, narrow signed/unsigned arguments,
nested calls, concurrent calls, GC/stack growth, panic unwinding and C code
that repurposes R30. Pure-Go big-endian callbacks require the nyanjit C
gateway and standard C stack back chains; C-created threads are supported when real cgo supplies runtime TLS and
thread attachment; the separate C integration module tests that path.
Native float/aggregate callback signatures are not supported on big-endian PPC64. Variable-count shift tests cover all scalar value/count widths, oversized counts, register aliasing, forced spills and scope/label transfers; GNU assembler independently verifies the native POWER8 encodings. Wide arithmetic tests cover carry chains, native high products, shared carries, XER clobbers, identity-cast flag aliases, random operands and scope/label transfers. CR/XER tests cover every scalar width, composed predicates,
call/shift invalidation and truncating-extension aliases. CTR tests exercise
zero-count guards, observable counter restoration, long branches, nested-loop
contention, live flags, side entries, and bodies that must retain GPR counts. FP32/FP64 arithmetic, special values, subnormals,
FMA contraction and single-rounding edge cases are covered. QEMU validates
instruction behavior; hardware instruction cache publication still needs verification on a POWER machine.

The additional `ppc64.star` suite cross-compiles every package as PIE and
runs real C shared-library and callback tests, including concurrent
C-created threads that reenter registered JIT code, grow the Go stack, and
trigger GC. Its C tests live in their own module under `ppc64cgo/`.
Big-endian Go 1.26 PIE/cgo are explicit skips because that Go release does
not support them.

Both PPC64 byte orders use a separate `QEMU_LD_PREFIX` sysroot. They share
the platform interpreter pathname `/lib64/ld64.so.2`, so placing both
loaders in the container root would select the wrong ELF byte order.
The big-endian sysroot uses the SHA256-pinned ELFv2 toolchain from
[musl.cc](https://musl.cc/); the little-endian sysroot uses Debian glibc.
Native container Go cross-compiles; QEMU executes target binaries and the
static i386-hosted big-endian C compiler. No Go compiler runs in a PPC VM.

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

The package matrix executes SIMD phase 1 on Linux/amd64, arm64, 386 and riscv64
with both Go toolchains. `compile` contains embedded assembly goldens for the
int32x4 add and byte-equality-mask examples. Tests compare native lowering and
`Features: []string{}` against independent integer lane oracles, including
unaligned buffers, 128/256/512-bit shapes, full shift counts, signed comparisons,
typed vector/mask loops, scope exports, complete vector spills and native C/Go
call boundaries. The 386 control emitter accepts incoming stack arguments;
its Go result slot follows the entry arguments.

The review's NEON byte-mask vector returns 9249. Floating SIMD, reductions,
conversions and scalable vectors belong to subsequent phases; the design records
the quiet-NaN, optional fusion, pairwise reduction and scalable-VL contracts.

For each architecture, the existing entrypoint runs the same tests:

```sh
./e2e/run.sh e2e/matrix.star -os linux -arch arm64
```

## Backend CPU acceptance suites

`amd64.star`, `arm64.star`, `riscv64.star`, and `386.star` run every package on
Go 1.26.9 and 1.27.2. The AMD64 profiles are `qemu64` and `max`; 386 uses
`qemu32` and `max`. RISC-V explicitly disables or enables Zba/Zbb/Zbs. ARM64
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
workspace spilling are checked. 386 FP is an explicit unsupported case.
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
  ./e2e/run.sh e2e/matrix.star -target linux/ppc64 -suite matrix

# Reuse it without rebuilding. Each PPC CI job selects one Go version.
NYANJIT_E2E_IMAGE=nyanjit-e2e:local NYANJIT_E2E_VAR_SWEEP=500 \
  ./e2e/run.sh e2e/ppc64.star -target linux/ppc64 -go-version 1.27.2

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
