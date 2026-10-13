# Optional native hardening

`compile.Func.Harden` enables constant blinding, randomized executable entry placement, and
bounds-edge speculation protection. It defaults to false. A `Unit` retains its
randomized lowering choices, so its listing and bytes describe the same body.
Separate compilations may produce different bytes and resource reports.

| Target | Blinding and randomized mapping | Control-flow programs with Harden |
| --- | --- | --- |
| amd64 | Supported | Clamp plus LFENCE |
| 386 | Supported, including paired uint64 values | Clamp plus LFENCE |
| arm64 | Supported | Clamp plus DSB SY / ISB |
| riscv64 | Supported | `ErrUnsupported` |
| loong64 | Supported | `ErrUnsupported` |
| ppc64le | Supported | Clamp plus `ori 31,31,0` |

Hardened control flow emits `machine.OpBarrier` with `BarrierSpeculation`, and
each target's `Policy.Barrier` reports what that barrier rests on. Harden
accepts an architectural barrier (arm64) or a platform one (x86, POWER), and
fails during lowering, before mapping or entry, on targets with neither; it
does not silently omit the protection. `compile.Emitter` also uses this
control-flow path. Ordinary memory-ordering fences are not treated as
substitutes.

Platform barriers need host configuration the code cannot check. On x86, the
host must provide dispatch-serializing LFENCE behavior, including any
processor/OS configuration required by its CPU. On POWER, `ori 31,31,0` is the
barrier Linux uses for `barrier_nospec`; it stops speculation only once host
firmware enables it, and is otherwise a no-op. RISC-V has no ratified
speculation barrier: the speculation-barrier task group's work is unratified,
and the kernel's use of FENCE.I is empirical. LoongArch defines none; Loongson
states that its cores mitigate bounds-check bypass internally, which is a
vendor claim rather than an ISA contract. Both report `BarrierUnavailable`.

## Constants

Blinding runs after immediate/address selection and before bank assignment and
allocation. Each scalar constant gets a fresh random XOR key from `crypto/rand`;
the encoded value and key are materialized, then decoded as a runtime value.
Selected arithmetic immediates, shift counts, folded scales and displacements
are converted back to runtime operands. Scalar FP constants retain their exact
bits. Native vector constants use independently randomized pool data and an XOR
operation; scalarized vectors use the scalar path. No decoded numeric constant
is marked rematerializable or eligible for the FP literal-pool optimization.
The allocator accounts for decode values, scratch pressure and flags. Scoped
instruction boundaries are rebased after inserted operations.

Control comparisons that occur after allocation borrow saved registers and
reserved frame/workspace words, decode their immediate, compare, and restore
the borrowed registers without changing the comparison flags. These writes and
storage count toward preservation and resource limits. Symbol relocations stay
ordinary host-supplied addresses; trusted ABI offsets, instruction selectors,
and compiler-generated zero/one constants are not concealed.

This frustrates constructing chosen instruction streams from numeric payloads;
it does not encrypt constants. Keys and encoded values are readable together.
It does not guarantee that every coincidental occurrence of a byte pattern has
been removed, prevent all gadgets, or protect arbitrary raw machine code.
Random-reader failures do not produce an unhardened fallback.

## Entry placement

Callable templates propagate the setting to `execution.CodeOptions.Harden`.
This execution-level field controls mapping placement only: `NewCode` cannot
blind or bounds-check raw bytes. Byte emission APIs have no mapping to randomize;
raw-byte consumers must request placement when constructing their `Code`.

Each binding chooses one of 256 sixteen-byte-aligned offsets, from 16 through
4096 bytes, using fresh random input. The prefix contains target trap
instructions. This adds eight bits of placement entropy within the mapping,
with up to 4096 prefix bytes plus page rounding. `Report.CodeBytes` describes the
body, excluding mapping padding. Local relocations, the callable entry, cache
publication, and Go text/SP metadata use the shifted body. `Code` retains the
whole allocation for reclamation. Registered-frame and `Func` lifetime rules
are unchanged; repeated `Bind` calls choose placement independently.

## Bounds edges

On the simple unsigned-less success edge emitted by `Data.Index` and
`IndexedAddress`, a flag-dependent CSEL/CMOV/ISEL clamps the exported index to
zero when the comparison fails. Paired 386 indexes clamp both words. The
bounds comparison and address calculation retain the same normalized index.
Hardened comparisons establish their own flags instead of reusing arithmetic
carry facts, whose polarity can differ. Constant array bounds also retain the
clamp.

Hardened control labels, including internal predicate/edge labels, also begin
with the target barrier. This covers nil and zero-length inputs, which have no
valid element at index zero, and handles general predicate shapes. The clamp
is an additional dependency; it is not used alone to claim protection for an
empty backing store. Ordinary architectural failure edges and checked-borrow
lifetimes remain mandatory. The x86 clamp uses CMOV, already required by the
supported x86 baseline; arm64 uses CSEL. Clamps and blinded comparisons borrow
three saved GPRs: EAX/ECX/EDX on x86, X0-X2 on arm64 and r3-r5 on POWER, where
r0-r2 are the ISEL zero source, SP and TOC.

This addresses bounds-branch speculation on the supported configurations. It
is not process isolation, a general Spectre defense, a defense against
speculative indirect entry, or a substitute for processor mitigations. Raw
addresses, native callees, symbols, compile options, termination and callback
memory lifetimes remain trusted host contracts.

## Verification and costs

Regression coverage includes scalar/FP/vector execution, scoped values, paired
386 operands and literal relocations, nil/empty Data views, immediate comparison
boundaries, emitted clamp/barrier inspection, deterministic placement of every
slot, randomness failure, Go callback unwinding/GC, and Func lifetime after
Close. The frozen default-off corpus contains 15,605 surviving fixtures and
retains listing identity.

`BenchmarkHardenCompile` measures lowering of sixteen uint64 XOR/add rounds;
`BenchmarkHardenCall` measures repeated calls to that body. `BenchmarkHardenBind`
measures binding and closing a mapping, and `BenchmarkHardenIndex` measures one
checked uint64 lookup. Benchmarks report compile allocations and body bytes.
QEMU timings measure the emulator and must not be interpreted as hardware
performance or as evidence of speculation resistance. The audit report records
per-target measurements and commands.

The barrier choices follow Arm's [speculation guidance](https://documentation-service.arm.com/static/6287b49b41e28926d04306e8)
the kernel's [x86 barrier implementation](https://github.com/torvalds/linux/blob/master/arch/x86/include/asm/barrier.h)
and its [POWER `barrier_nospec`](https://github.com/torvalds/linux/blob/master/arch/powerpc/include/asm/barrier.h).
The [RISC-V speculation-barrier work](https://riscv.atlassian.net/wiki/spaces/SXXX/pages/272629762/Speculation%2BBarriers%2B-%2BProposal%2Bof%2BWork)
and [LoongArch barrier definitions](https://loongson.github.io/LoongArch-Documentation/LoongArch-Vol1-EN.html#_dbar)
explain why a memory fence alone is insufficient justification for those targets.
