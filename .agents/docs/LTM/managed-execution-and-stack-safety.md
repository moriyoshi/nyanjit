# Managed Execution and Stack Safety

## Summary

Generated Go frames need runtime metadata and exact unwinder-compatible layouts, in addition to argument registers. Execution safety depends on stack caps, callable-target restrictions, pointer rooting and mapping lifetime together.

## Key Facts

- `goregister` constructs version-specific synthetic module metadata, with separate Go 1.26/1.27 layouts; it does not implement the proposed runtime/jit API.
- Bind caps goroutine frames at 640 bytes (240 on 386) and foreign frames at 4,096 bytes.
- Per-entry caps do not bound chains: callable `CallGo` targets must be fixed ordinary Go functions.
- `Code.Call`/`CallPair` remain raw; `execution.Words` handles roots/escape, and `Code.Func(fptr any) error` checks signatures.
- Managed registrations have a process-wide permanent budget of 4,096.

## Details

The initial arm64 registration used pointer-free frames and LR placement matching the unwinder. Matching argument registers alone was insufficient; synthetic linker-like metadata made registration possible under explicit Go-version constraints.

F1 showed unmanaged NOSPLIT trampolines could exhaust a goroutine's approximately 800-byte reserve. Main-goroutine stacks hid it; fresh goroutines exposed corruption. The 640/240-byte Bind caps and 4,096-byte foreign cap bound supported entry paths. V1 then bypassed the cap by chaining generated entries through `EntryAddress` and `CallGo`, corrupting runtime P state. Callable compilation rejects generated entries, runtime/purego trampolines and functions in package execution. This is a fixed-target/denylist policy, not a generated stack-guard prologue. Constant targets through Select/Vars remain in TODO.md.

Frame records must survive `copystack`, async unwinding, tracing and mutex/block profiling:

- F4: arm64 spills overwrote the caller-FP top word. It survived 131 million ordinary calls but failed under profiling. Managed frames reserve that word.
- F16: arm64 `Code.Call` had a 16-byte trampoline gap, so stack copying left a stale FP. Gap-free `callManaged`/`callPairManaged` enter managed code; unmanaged entry retains the AAPCS64 gap. The reproducer was stale in 64/64 calls before the fix.
- L15: amd64 also reserves the top word used by Go's `adjustframe`; 15 historical corpus programs changed.
- F17: 386 Go-ABI epilogues store eight result bytes. `Code.Func` therefore requires one eight-byte integer result, rejecting narrow or absent results.
- riscv64/loong64 FrameEnter stores RA below SP before changing SP. Large frames use X31/R20 beyond 2,048 bytes at unchanged size. The C-thread signal-window decision is tracked in TODO.md.

Pointer escape cannot rely on `//go:uintptrescapes` propagation through interfaces. `Words(&local)` moves local data to the heap and retains roots across callbacks. Raw hand-built uintptr addresses remain caller responsibility. `Words` rejects nil aggregate roots; `Func` checks exact process-local aggregate types and validates pointer signatures through a Go wrapper. Raw Call/CallPair deliberately do not validate entry types.

`Code.Func` replaced generic `execution.Func[F]`; it requires a nonnil pointer to a function variable and leaves it unchanged on signature failure. Function values retain Code and permanently quarantine its mapping. Close uses an atomic closed-bit/in-flight-count state, with deferred unmapping, allowing callback close/reentry without the old RWMutex deadlock. Registration and `plugin.Open` must be serialized by the application. `nyanjit_no_goregister` removes private runtime linknames for non-managed consumers.

JIT loops without calls are not preemptible. Hosts must emit their own execution limits; Go contexts/timeouts cannot interrupt generated loops. GC-setting changes and spin tests require the synchronization described in [verification practice](verification-benchmarking-and-review-practice.md).

## Files

- `backend/goregister/`: registration and Go frame contracts.
- `backend/execution/code.go`, `backend/execution/func.go`, `backend/execution/call_linux_arm64.s`: admission, lifetime and call trampolines.
- `compile/backend_*target.go`, `backend/target/*/`: frame sizing/prologues.
- `README.md`: safety/trust model and raw execution contracts.

## Test Coverage

Regression anchors include `TestSecurityGoroutineStackBudget`, `TestSecuritySavedFramePointer`, `TestSecurityFramePointerProfiling`, `TestSecurityFramePointerChainAfterStackGrowth`, `TestAMD64ManagedFramesLeaveTopWord`, `TestFuncResultWidthOn386`, `TestGeneratedGoCallNestingRejected`, `TestGeneratedGoCallRecursionRejected`, `TestGoCallTrampolinesRejected`, `TestGoCallTargetRejectsExecutionHelpers`, `TestSecurityCallPointerEscapes`, `TestWordsRejectsNilAggregatePointers`, `TestSecurityCloseAndReenterFromCallback`, `TestSecurityFuncSurvivesClose`, `TestSecurityRegistrationBudget` and `TestRegistrationDisabled`. Run on the relevant target, including fresh-goroutine stack growth and profiling.

## Pitfalls

Reproducer APIs from earlier audits may have been deleted. A test that ignores failed preparation and a nil caller proves nothing: the nested-call rejection test must assert `ErrInvalidFunction`. A package matrix cannot substitute for runtime frame/profiling checks.

## Sources

Consolidated from these headings in `.agents/docs/JOURNAL.md`:

- 2026-10-08 — First spike and the execution boundary
- 2026-10-09 — Registering generated Go frames
- 2026-10-14 — Scoped Vars, and a green matrix that proved nothing
- 2026-10-18 — An either/or question hid the option the user wanted
- 2026-10-18 — First security audit: frames, encodings and the call boundary
- 2026-10-19 — Optional hardening (Harden), and what the audit cost when it was off
- 2026-10-25 — Pre-release security audit: four high-severity bugs and their root causes
- 2026-10-25 — Second audit: the Medium and Low findings, and how the audit ran
- 2026-10-30 — Baseline parity items, and an entry-block loop that crashed
