# Targets and Encoders Synthesis

## Summary

Target work must preserve portable operation semantics, encoder legality, exact sizes and register/flag effects together. Compile exercises only part of the public encoder API, so successful generated programs cannot establish the raw encoder contract. This document connects profile selection, adversarial encoding checks and code-size rewrites.

## Included Documents

| Document | Focus |
|----------|-------|
| [target-profiles-and-port-contracts.md](target-profiles-and-port-contracts.md) | Portable semantics, CPU profiles, ABI constraints and supported-target history |
| [validation-and-encoder-contracts.md](validation-and-encoder-contracts.md) | Validation/lowering agreement, used fields, bounded work and size agreement |
| [flag-models-and-code-size-selection.md](flag-models-and-code-size-selection.md) | Flag dependencies, dead-flag rewrites, narrow forms and operand-dependent sizing |

## Stable Knowledge

- Width is semantic: shifts saturate using the entire unsigned count; arithmetic shifts sign-fill; rotates reduce modulo width. An ISA's masked shift count is insufficient.
- Explicit cross-target CPU selection uses a deterministic profile without host probing. Native selection rejects unverified extensions. RISC-V Zbb/V does not establish full RVA conformance; POWER levels need explicit ordering even when their optional feature sets match.
- Validate every field consumed downstream, and ignore fields the contract declares unused. Per-op `operandFields` tables align sizing and encoding; invalid used fields reject, and invalid size plans return zero. Reach-sensitive branches/literals need layout checks as well.
- Flag models enumerate every writer and reader. CallC, CallGo and Syscall kill facts; partial writers do not kill unrelated live flags. MOVZX produces no flags, and 386 OF liveness must protect ADD feeding JO.
- Public size-only streams may lack exact flag metadata. Elision must incorporate target facts rather than assuming compile's stream configuration.
- Handwritten baseline sizes must follow the chosen encoding and operand, including short immediates. Implicit writes belong in reports even when an expansion hides them.

## Operational Guidance

Start from the target's FORMS table and CPU/ABI profile. Check that the byte encoder, size function, listing, modified flags and implicit writes describe the same form. Mutation coverage must include fields ordinary compile never sets: wrong banks, invalid arithmetic kinds, zero widths, extreme offsets and noncanonical unused fields.

For a compact form, establish its flag behavior before teaching selection to use it. 386's MOVZX masks saved about 1.7% corpus bytes at both tiers, but facts for an unmasked arithmetic result could not be reinterpreted as facts for the masked value. Short immediate changes required `i386ImmediateSize` updates in roughly a dozen baseline sites. The source document retains exact per-change savings and the six-round core-5/GOMAXPROCS=1 w8 cost comparison.

Check output differences per function as well as in aggregate. A reviewed 61 KB reduction still grew three amd64 functions by four bytes when jrcxz became jbe. No isolated multiply benchmark exists for that amend series.

Use [optional-hardening-and-barrier-contracts.md](optional-hardening-and-barrier-contracts.md) for speculation guarantees and [operation-semantics-and-reference-oracles.md](operation-semantics-and-reference-oracles.md) for arithmetic reference behavior. Hardware-cache publication and remaining raw register-31/R0 questions belong to TODO.md; QEMU cannot establish hardware coherence.

## Files

- `backend/target/*/FORMS.md`: legal forms and sizes, checked row by row.
- `backend/target/*/`: encoders, operand fields, CPU-aware sizes and flag models.
- `backend/machine/branches.go`, `backend/machine/work.go`: layout and scratch ownership on success/error.
- `backend/target/amd64/dead_flags.go`, `backend/machine/zero_loops.go`: flag-dependent rewrites and worklist liveness.
- `compile/baseline_*target.go`, `docs/cpu-profiles.md`: baseline sizing and profile semantics.

## Tests

From the repository root, focused checks include:

```sh
taskset -c 5 timeout 180s go -C backend test ./target -run '^(TestDocumentedForms|TestSizeAgreesWithEncoding)$' -count=1
taskset -c 5 timeout 180s go -C backend test ./target/amd64 ./target/i386 -run '^TestElideDeadFlagsIgnoresInexactStreams$' -count=1
```

`FuzzEncoderVisit` covers raw fields and size/write/flag queries; `FuzzSecurityRawIR` covers validation consumers. `TestI386FlagsAcrossCalls` needs a matching 386 execution environment. Run cross-target irconform and the E2E matrix for semantic changes, with cases that actually consume the suspect flag after the writer. See [verification-benchmarking-and-review-practice.md](verification-benchmarking-and-review-practice.md) for the full gate.

## Pitfalls

Missing writers remain invisible until a reader crosses them. A form can have a correct byte count while encoding the wrong operation: overflowing ARM spill offsets once changed STR into LDR. A zero-width raw rotate once hung a fuzz worker despite printed PASS. Repeated layout calls must release scratch on errors as well as success, or a later clear can erase the live stream.
