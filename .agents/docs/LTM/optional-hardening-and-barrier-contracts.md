# Optional Hardening and Barrier Contracts

## Summary

Hardening is user-approved, opt-in defense in depth through constant blinding, randomized entry placement and speculative bounds protection. Its guarantees depend on target barrier contracts, and default-off compilation still needs performance checks.

## Key Facts

- Historical `CompileOptions.Harden`/`execution.CodeOptions.Harden` introduced the option; later APIs include `compile.Func.Harden`.
- Blinding occurs after selection and before allocation with keys from `crypto/rand`.
- Reader failure never falls back to unhardened code.
- Hardening promises neither secrecy nor isolation.
- `machine.BarrierContract` distinguishes Unavailable, Architectural and Platform guarantees.

## Details

Entry placement chooses one of 256 sixteen-byte-aligned offsets from 16 through 4,096, with a trap-filled prefix. Bounds protection clamps on the success edge of Data.Index/IndexedAddress and adds a speculation barrier at hardened labels.

Two late correctness findings require independent comparison facts. ARM addition carry proved the bounds branch with opposite polarity to the clamp's comparison, so index MaxUint+3 with count 3 selected element zero. Hardened comparisons now set their own flags. Constant-bound folding had also created immediate compares that bypassed clamping.

Native arm64 historical off/on costs:

| Measurement | Off | On |
|---|---|---|
| Lower | 58.5 us | 98.7 us |
| Body | 644 B | 1,284 B |
| Call | 78.6 ns | 98.3 ns |
| Index | 64.7 ns | 133.9 ns |

Other target QEMU numbers are emulator timings. The larger surprise was audit overhead with Harden off: NativeArithmeticCall +40.8% (44 -> 62 ns) from two mutex acquisitions, Prepare +11-17% from repeated ARM size checks, and Build/chain +12.5% from a separate validation walk. Fixes used one atomic admission word, cached size checks, checks folded into existing validation with a zero-metadata fast path, and inlineable append helpers. The former helpers cost 129/146 against Go's inlining budget of 80. Final Call overhead was +4.23%; sha256-simd improved 44% because the old tree built full vector plans on every size query. Round 13 also removed per-instruction defer in machine.Visit.

`machine.OpBarrier` names speculation, memory or instruction guarantees. Each target's Policy reports its contract. x86 instruction barriers are Unavailable rather than implicit: cross-modified code needs a serializing instruction. PPC uses Linux barrier_nospec (`ori 31,31,0`) plus ISEL clamp and borrowed r3-r5; its guarantee is Platform and needs updated firmware. RISC-V and LoongArch hardened control flow remain `ErrUnsupported`: RISC-V has no ratified speculation barrier (kernel FENCE.I is empirical), and Loongson claims built-in mitigation without an exposed architectural barrier. Other hardening can have different support.

## Files

- `docs/optional-hardening-design.md`: public guarantees and lowering.
- `backend/machine/`, `backend/target/*/`: barrier vocabulary and contracts.
- `compile/`: blinding, clamp and hardening register selection.

## Test Coverage

`TestBarrierContractsAndEncodings` and `TestHardenControlBarrierPerTarget` exercise contracts; historical QEMU runs covered POWER8/9/10, amd64, 386, riscv64 and loong64. Raw-IR mutation fuzzing should run with Harden both off and on. Audit guidance calls for at least 15 minutes when validation changes; a 30-second gate missed ScopeLeave.Args, found by the audit fuzzer in 180 seconds.

## Pitfalls

Compare against the last reviewed tree: an implementer's before tree may already contain the regression. A three-pair performance candidate passed while the isolated 16-pair both-order acceptance failed many-vars-loop by 10-13%. Test every stack practice and ABI, including 386 Store8 under StackNoUse, rather than only StackPlatform.

## Sources

Consolidated from these headings in `.agents/docs/JOURNAL.md`:

- 2026-10-19 — Optional hardening (Harden), and what the audit cost when it was off
- 2026-10-25 — Second audit: the Medium and Low findings, and how the audit ran
- 2026-10-25 — Dead-flag LEA on amd64, and a benchmark that inflated its own baseline
- 2026-10-30 — Baseline parity items, and an entry-block loop that crashed
