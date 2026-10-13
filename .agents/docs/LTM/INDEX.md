# Long-Term Memory Index

Durable, topic-organized project knowledge. Unlike `.agents/docs/JOURNAL.md` (append-only, chronological), these documents are meant to be **edited and refined** over time.

Start with [ARCHITECTURE.md](../ARCHITECTURE.md) for the consolidated compiler guide, historical measurement tables and archived validation/baseline data formerly under `docs/`. The public `docs/` pages describe consumer contracts and usage.

Documents arrive here two ways:

- `good-sleep` distills chronological `JOURNAL.md` entries into topic documents (the source table below).
- `deep-sleep` merges overlapping topic documents into broader synthesis documents (the synthesis table below).

`distill-memories` then promotes consumer contracts into `README.md` and `docs/`, internal implementation knowledge into `.agents/docs/ARCHITECTURE.md`, encoding contracts into per-target `FORMS.md`, and shared checks into `.agents/docs/QUALITY_GATE.md`.

## Synthesis Documents

| Document | Summary | Consolidates |
|----------|---------|--------------|
| [targets-and-encoders-synthesis.md](targets-and-encoders-synthesis.md) | Portable semantics, encoding legality, exact sizes and complete flag/register effects | [Profiles and ports](target-profiles-and-port-contracts.md), [Validation and encoders](validation-and-encoder-contracts.md), [Flags and code size](flag-models-and-code-size-selection.md) |
| [compilation-tiers-and-allocation-synthesis.md](compilation-tiers-and-allocation-synthesis.md) | Shared Builder dialect, tier strategy, allocation boundaries, FP/SIMD banks and literal layout | [Dual tiers](dual-tier-design-and-baseline-correctness.md), [Scopes and allocation](scopes-and-register-allocation.md), [FP and literals](floating-point-and-literal-layout.md), [SIMD and budgets](simd-lowering-and-expansion-budgets.md) |
| [data-and-managed-execution-synthesis.md](data-and-managed-execution-synthesis.md) | Data validity, roots, program-shared cell pins, stack metadata and Code lifetime | [Data and cells](data-views-cells-and-finalizers.md), [Managed execution](managed-execution-and-stack-safety.md) |
| [compile-time-and-scratch-memory-synthesis.md](compile-time-and-scratch-memory-synthesis.md) | Profile-guided allocation work, scratch histories, output ownership and packed data movement | [Compile costs](compile-cost-investigations.md), [Scratch retention](compile-scratch-retention-and-growth.md), [Packed physical program](packed-physical-program-and-store-forwarding.md) |

## Source Topic Documents

| Document | Summary |
|----------|---------|
| [architecture-and-api-decisions.md](architecture-and-api-decisions.md) | Package boundaries, public API policy, sealed views and ownership decisions |
| [scopes-and-register-allocation.md](scopes-and-register-allocation.md) | Deferred values, Var homes, register groups and online/batch allocation protocols |
| [target-profiles-and-port-contracts.md](target-profiles-and-port-contracts.md) | Portable integer semantics, CPU profiles and target ABI/support contracts |
| [floating-point-and-literal-layout.md](floating-point-and-literal-layout.md) | FP residency, exact products, literal relocation/reach, alignment and branch fusion |
| [simd-lowering-and-expansion-budgets.md](simd-lowering-and-expansion-budgets.md) | Native SIMD and scalar fallback, register sharing, full-width spills and expansion limits |
| [managed-execution-and-stack-safety.md](managed-execution-and-stack-safety.md) | Runtime registration, frame layout, call boundaries, pointer roots and code lifetime |
| [data-views-cells-and-finalizers.md](data-views-cells-and-finalizers.md) | Transferable Data views, checked indexing, narrow memory and program-shared cell finalizers |
| [optional-hardening-and-barrier-contracts.md](optional-hardening-and-barrier-contracts.md) | Blinding, randomized placement, bounds clamps, barrier guarantees and default-off cost |
| [validation-and-encoder-contracts.md](validation-and-encoder-contracts.md) | Validation/lowering agreement, public encoder rejection/size rules and bounded work |
| [compile-scratch-retention-and-growth.md](compile-scratch-retention-and-growth.md) | Scratch budgets, history/decay, poison leases, demand-aware growth and recurring large buffers |
| [compile-cost-investigations.md](compile-cost-investigations.md) | Allocation and size-query profiles, durable output ownership and measured compile improvements |
| [packed-physical-program-and-store-forwarding.md](packed-physical-program-and-store-forwarding.md) | Packed instruction side tables, in-place/batched decoding, store forwarding and pool-cap costs |
| [flag-models-and-code-size-selection.md](flag-models-and-code-size-selection.md) | Complete flag models, dead-flag rewrites, narrow masks, exact sizes and scaled selection |
| [dual-tier-design-and-baseline-correctness.md](dual-tier-design-and-baseline-correctness.md) | Shared Builder dialect, Var promotion, baseline liveness, parity and entry-loop correctness |
| [operation-semantics-and-reference-oracles.md](operation-semantics-and-reference-oracles.md) | Division/conversion/FP semantics, expanded operations and independent arithmetic oracles |
| [verification-benchmarking-and-review-practice.md](verification-benchmarking-and-review-practice.md) | Differential/mutation/snapshot gates, benchmark controls, emulator diagnosis and review discipline |

Reconciled against all 47 substantive journal entries on 2026-11-05. See [the coverage audit](../reconcile-journal-ltm-audit-2026-11-05.md) for entry-level evidence and the small gaps filled before journal cleanup.

Deep-sleep orientation added on 2026-11-05. All 16 source documents are preserved. Architecture/API decisions, optional hardening, operation semantics/reference oracles and verification/measurement remain standalone overviews; use the four syntheses to enter the other twelve topics.
