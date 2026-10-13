# NyanJIT Development Journal

Append-only working log. New entries go at the **end** of the file, under a `## YYYY-MM-DD — <short title>` heading. Do not edit or delete existing sections; the `reconcile-journal-ltm` skill is the only sanctioned remover, and only for entries already consolidated into `.agents/docs/LTM/`.

What belongs here:

- design decisions and the alternatives you rejected, with the reason
- bug classes found, and which test layer did or did not catch them
- benchmark results, before and after
- peer code review findings and their resolution
- anything a future agent would otherwise have to rediscover

What does not belong here: routine "ran the gate, it passed" notes, and open follow-ups (those go to `TODO.md`).

Entries accumulate until `good-sleep` distills them into topic documents under `.agents/docs/LTM/`, at which point a `## LTM Consolidation Record` section records the mapping.

---

## LTM Consolidation Record

Audited against the bodies of all 16 LTM topic documents and `TODO.md` on 2026-11-05. All 47 substantive entries are covered; six small detail gaps were filled in existing documents before the consolidated entries were removed. Open follow-ups remain in `TODO.md`.

See [the entry-by-entry coverage audit](reconcile-journal-ltm-audit-2026-11-05.md) for the evidence and corrections. The original journal headings are retained below as provenance; their durable content now lives in LTM.

| Section | LTM Document |
|---------|-------------|
| 2026-10-08 — Deferred values, scopes and call effects | [scopes-and-register-allocation.md](LTM/scopes-and-register-allocation.md) |
| 2026-10-08 — First spike and the execution boundary | [architecture-and-api-decisions.md](LTM/architecture-and-api-decisions.md), [managed-execution-and-stack-safety.md](LTM/managed-execution-and-stack-safety.md) |
| 2026-10-09 — Registering generated Go frames | [managed-execution-and-stack-safety.md](LTM/managed-execution-and-stack-safety.md) |
| 2026-10-10 — Widths, register groups and package boundaries | [architecture-and-api-decisions.md](LTM/architecture-and-api-decisions.md), [scopes-and-register-allocation.md](LTM/scopes-and-register-allocation.md) |
| 2026-10-13 — What the tree held at the first snapshot, and the same-day PPC64 work | [target-profiles-and-port-contracts.md](LTM/target-profiles-and-port-contracts.md) |
| 2026-10-14 — Backend parity with PPC64, and CPU profiles | [flag-models-and-code-size-selection.md](LTM/flag-models-and-code-size-selection.md), [floating-point-and-literal-layout.md](LTM/floating-point-and-literal-layout.md), [target-profiles-and-port-contracts.md](LTM/target-profiles-and-port-contracts.md), [validation-and-encoder-contracts.md](LTM/validation-and-encoder-contracts.md) |
| 2026-10-14 — FP lowering: exact products, literal pools, FP residency | [floating-point-and-literal-layout.md](LTM/floating-point-and-literal-layout.md) |
| 2026-10-14 — Portable SIMD, phase 1 | [simd-lowering-and-expansion-budgets.md](LTM/simd-lowering-and-expansion-budgets.md) |
| 2026-10-14 — Scoped Vars, and a green matrix that proved nothing | [flag-models-and-code-size-selection.md](LTM/flag-models-and-code-size-selection.md), [managed-execution-and-stack-safety.md](LTM/managed-execution-and-stack-safety.md), [scopes-and-register-allocation.md](LTM/scopes-and-register-allocation.md), [verification-benchmarking-and-review-practice.md](LTM/verification-benchmarking-and-review-practice.md) |
| 2026-10-15 to 2026-10-16 — 386 floating point (P0-P4) and native SSE2 SIMD (P5) | [floating-point-and-literal-layout.md](LTM/floating-point-and-literal-layout.md), [simd-lowering-and-expansion-budgets.md](LTM/simd-lowering-and-expansion-budgets.md), [target-profiles-and-port-contracts.md](LTM/target-profiles-and-port-contracts.md) |
| 2026-10-15 — CI, and big-endian PPC64 dropped | [target-profiles-and-port-contracts.md](LTM/target-profiles-and-port-contracts.md) |
| 2026-10-16 — Compile allocations, round 6 and steps 1-3, and the baseline they were measured from | [compile-cost-investigations.md](LTM/compile-cost-investigations.md), [compile-scratch-retention-and-growth.md](LTM/compile-scratch-retention-and-growth.md), [verification-benchmarking-and-review-practice.md](LTM/verification-benchmarking-and-review-practice.md) |
| 2026-10-16 — LoongArch port (L0-L4), and a GC failure traced to the emulator build | [simd-lowering-and-expansion-budgets.md](LTM/simd-lowering-and-expansion-budgets.md), [target-profiles-and-port-contracts.md](LTM/target-profiles-and-port-contracts.md), [verification-benchmarking-and-review-practice.md](LTM/verification-benchmarking-and-review-practice.md) |
| 2026-10-16 — Standing API rules: everything public, nothing kept for compatibility | [architecture-and-api-decisions.md](LTM/architecture-and-api-decisions.md) |
| 2026-10-17 to 2026-10-18 — Review rounds 6-10: scratch retention, poison builds, and validation that lowering trusts | [architecture-and-api-decisions.md](LTM/architecture-and-api-decisions.md), [compile-cost-investigations.md](LTM/compile-cost-investigations.md), [compile-scratch-retention-and-growth.md](LTM/compile-scratch-retention-and-growth.md), [scopes-and-register-allocation.md](LTM/scopes-and-register-allocation.md), [validation-and-encoder-contracts.md](LTM/validation-and-encoder-contracts.md), [verification-benchmarking-and-review-practice.md](LTM/verification-benchmarking-and-review-practice.md) |
| 2026-10-17 — Two API policies in one night, and where internal/ went | [architecture-and-api-decisions.md](LTM/architecture-and-api-decisions.md), [verification-benchmarking-and-review-practice.md](LTM/verification-benchmarking-and-review-practice.md) |
| 2026-10-18 — An either/or question hid the option the user wanted | [data-views-cells-and-finalizers.md](LTM/data-views-cells-and-finalizers.md), [managed-execution-and-stack-safety.md](LTM/managed-execution-and-stack-safety.md), [verification-benchmarking-and-review-practice.md](LTM/verification-benchmarking-and-review-practice.md) |
| 2026-10-18 — Data views for Go values, and native 16/32-bit memory | [data-views-cells-and-finalizers.md](LTM/data-views-cells-and-finalizers.md) |
| 2026-10-18 — First security audit: frames, encodings and the call boundary | [data-views-cells-and-finalizers.md](LTM/data-views-cells-and-finalizers.md), [managed-execution-and-stack-safety.md](LTM/managed-execution-and-stack-safety.md), [validation-and-encoder-contracts.md](LTM/validation-and-encoder-contracts.md), [verification-benchmarking-and-review-practice.md](LTM/verification-benchmarking-and-review-practice.md) |
| 2026-10-19 — Optional hardening (Harden), and what the audit cost when it was off | [managed-execution-and-stack-safety.md](LTM/managed-execution-and-stack-safety.md), [optional-hardening-and-barrier-contracts.md](LTM/optional-hardening-and-barrier-contracts.md), [verification-benchmarking-and-review-practice.md](LTM/verification-benchmarking-and-review-practice.md) |
| 2026-10-19 — Rounds 11-13: retries, report ownership, and measurement noise | [compile-scratch-retention-and-growth.md](LTM/compile-scratch-retention-and-growth.md), [data-views-cells-and-finalizers.md](LTM/data-views-cells-and-finalizers.md), [validation-and-encoder-contracts.md](LTM/validation-and-encoder-contracts.md), [verification-benchmarking-and-review-practice.md](LTM/verification-benchmarking-and-review-practice.md) |
| 2026-10-20 — API consolidation, and where the user overrode the written plan | [architecture-and-api-decisions.md](LTM/architecture-and-api-decisions.md) |
| 2026-10-23 to 2026-10-24 — Writing FORMS.md found encoder bugs compile never hits; a QEMU hang that was not ours | [compile-cost-investigations.md](LTM/compile-cost-investigations.md), [compile-scratch-retention-and-growth.md](LTM/compile-scratch-retention-and-growth.md), [validation-and-encoder-contracts.md](LTM/validation-and-encoder-contracts.md), [verification-benchmarking-and-review-practice.md](LTM/verification-benchmarking-and-review-practice.md) |
| 2026-10-24 — A whole-function allocator beside the online one, not instead of it | [scopes-and-register-allocation.md](LTM/scopes-and-register-allocation.md), [verification-benchmarking-and-review-practice.md](LTM/verification-benchmarking-and-review-practice.md) |
| 2026-10-25 — Dead-flag LEA on amd64, and a benchmark that inflated its own baseline | [flag-models-and-code-size-selection.md](LTM/flag-models-and-code-size-selection.md), [optional-hardening-and-barrier-contracts.md](LTM/optional-hardening-and-barrier-contracts.md), [verification-benchmarking-and-review-practice.md](LTM/verification-benchmarking-and-review-practice.md) |
| 2026-10-25 — Pre-release security audit: four high-severity bugs and their root causes | [managed-execution-and-stack-safety.md](LTM/managed-execution-and-stack-safety.md), [validation-and-encoder-contracts.md](LTM/validation-and-encoder-contracts.md), [verification-benchmarking-and-review-practice.md](LTM/verification-benchmarking-and-review-practice.md) |
| 2026-10-25 — Second audit: the Medium and Low findings, and how the audit ran | [flag-models-and-code-size-selection.md](LTM/flag-models-and-code-size-selection.md), [managed-execution-and-stack-safety.md](LTM/managed-execution-and-stack-safety.md), [optional-hardening-and-barrier-contracts.md](LTM/optional-hardening-and-barrier-contracts.md), [simd-lowering-and-expansion-budgets.md](LTM/simd-lowering-and-expansion-budgets.md), [validation-and-encoder-contracts.md](LTM/validation-and-encoder-contracts.md), [verification-benchmarking-and-review-practice.md](LTM/verification-benchmarking-and-review-practice.md) |
| 2026-10-26 — Allocations down 42–99%, and a frozen corpus that had stopped meaning anything | [architecture-and-api-decisions.md](LTM/architecture-and-api-decisions.md), [compile-cost-investigations.md](LTM/compile-cost-investigations.md), [scopes-and-register-allocation.md](LTM/scopes-and-register-allocation.md), [verification-benchmarking-and-review-practice.md](LTM/verification-benchmarking-and-review-practice.md) |
| 2026-10-27 — A register allocator protocol, and porting compile onto it | [compile-cost-investigations.md](LTM/compile-cost-investigations.md), [scopes-and-register-allocation.md](LTM/scopes-and-register-allocation.md), [verification-benchmarking-and-review-practice.md](LTM/verification-benchmarking-and-review-practice.md) |
| 2026-10-28 — sljit gap analysis, and the first semantic gaps filled | [operation-semantics-and-reference-oracles.md](LTM/operation-semantics-and-reference-oracles.md) |
| 2026-10-29 — Two tiers: one Builder dialect, a baseline engine on six targets | [dual-tier-design-and-baseline-correctness.md](LTM/dual-tier-design-and-baseline-correctness.md), [verification-benchmarking-and-review-practice.md](LTM/verification-benchmarking-and-review-practice.md) |
| 2026-10-30 — Baseline parity items, and an entry-block loop that crashed | [dual-tier-design-and-baseline-correctness.md](LTM/dual-tier-design-and-baseline-correctness.md), [managed-execution-and-stack-safety.md](LTM/managed-execution-and-stack-safety.md), [optional-hardening-and-barrier-contracts.md](LTM/optional-hardening-and-barrier-contracts.md) |
| 2026-10-30 — Eleven sljit-gap items in one wave, and what seven parallel merges taught | [dual-tier-design-and-baseline-correctness.md](LTM/dual-tier-design-and-baseline-correctness.md), [operation-semantics-and-reference-oracles.md](LTM/operation-semantics-and-reference-oracles.md), [simd-lowering-and-expansion-budgets.md](LTM/simd-lowering-and-expansion-budgets.md), [verification-benchmarking-and-review-practice.md](LTM/verification-benchmarking-and-review-practice.md) |
| 2026-10-31 — 386 flag facts, and a miscompile the old flag model allowed | [dual-tier-design-and-baseline-correctness.md](LTM/dual-tier-design-and-baseline-correctness.md), [flag-models-and-code-size-selection.md](LTM/flag-models-and-code-size-selection.md) |
| 2026-11-01 — 386 code size, one encoding at a time | [flag-models-and-code-size-selection.md](LTM/flag-models-and-code-size-selection.md), [verification-benchmarking-and-review-practice.md](LTM/verification-benchmarking-and-review-practice.md) |
| 2026-11-01 — Optimized-tier compile time against Cranelift, and two corrections from the user | [compile-cost-investigations.md](LTM/compile-cost-investigations.md), [verification-benchmarking-and-review-practice.md](LTM/verification-benchmarking-and-review-practice.md) |
| 2026-11-01 — Optimized-tier takeover: FP branches and literal packing | [compile-cost-investigations.md](LTM/compile-cost-investigations.md), [floating-point-and-literal-layout.md](LTM/floating-point-and-literal-layout.md) |
| 2026-11-01 — Scaled multiplication and a shared stack VM | [flag-models-and-code-size-selection.md](LTM/flag-models-and-code-size-selection.md), [operation-semantics-and-reference-oracles.md](LTM/operation-semantics-and-reference-oracles.md), [scopes-and-register-allocation.md](LTM/scopes-and-register-allocation.md) |
| 2026-11-02 — Cell's changing lifetime contract | [data-views-cells-and-finalizers.md](LTM/data-views-cells-and-finalizers.md) |
| 2026-11-04 — 386 compile-cost recovery: most of the cost was older than w8 | [compile-cost-investigations.md](LTM/compile-cost-investigations.md) |
| 2026-11-04 — Cells take the shape of Data, and finalizers are refcounted by program | [data-views-cells-and-finalizers.md](LTM/data-views-cells-and-finalizers.md) |
| 2026-11-04 — Reviewing Codex's amends | [compile-cost-investigations.md](LTM/compile-cost-investigations.md), [flag-models-and-code-size-selection.md](LTM/flag-models-and-code-size-selection.md), [floating-point-and-literal-layout.md](LTM/floating-point-and-literal-layout.md), [verification-benchmarking-and-review-practice.md](LTM/verification-benchmarking-and-review-practice.md) |
| 2026-11-04 — Stream regrowth: the history was lost, not just the buffer | [compile-scratch-retention-and-growth.md](LTM/compile-scratch-retention-and-growth.md) |
| 2026-11-05 — Store forwarding, not field count, made the compact form slow | [packed-physical-program-and-store-forwarding.md](LTM/packed-physical-program-and-store-forwarding.md) |
| 2026-11-05 — The located copy is the Unit's output, and what could be removed instead | [compile-cost-investigations.md](LTM/compile-cost-investigations.md), [packed-physical-program-and-store-forwarding.md](LTM/packed-physical-program-and-store-forwarding.md) |
| 2026-11-05 — Unverified fixes, and the benchmark that caught two regressions | [packed-physical-program-and-store-forwarding.md](LTM/packed-physical-program-and-store-forwarding.md), [simd-lowering-and-expansion-budgets.md](LTM/simd-lowering-and-expansion-budgets.md), [verification-benchmarking-and-review-practice.md](LTM/verification-benchmarking-and-review-practice.md) |
| 2026-11-05 — Work summary: 386 parity and compile cost, 2026-10-30 to 2026-11-05 | [compile-cost-investigations.md](LTM/compile-cost-investigations.md), [dual-tier-design-and-baseline-correctness.md](LTM/dual-tier-design-and-baseline-correctness.md), [packed-physical-program-and-store-forwarding.md](LTM/packed-physical-program-and-store-forwarding.md), [verification-benchmarking-and-review-practice.md](LTM/verification-benchmarking-and-review-practice.md) |

See [the full LTM index](LTM/INDEX.md).

---

## 2026-11-05 — Deep Sleep Consolidation Record

Created four compact orientation documents over twelve related source topics. All sixteen source LTM documents are preserved. The synthesis table in [LTM/INDEX.md](LTM/INDEX.md) now identifies each cluster; the original source table and prior journal record are unchanged.

| Synthesis | Source LTM documents |
|-----------|----------------------|
| [targets-and-encoders-synthesis.md](LTM/targets-and-encoders-synthesis.md) | [target-profiles-and-port-contracts.md](LTM/target-profiles-and-port-contracts.md), [validation-and-encoder-contracts.md](LTM/validation-and-encoder-contracts.md), [flag-models-and-code-size-selection.md](LTM/flag-models-and-code-size-selection.md) |
| [compilation-tiers-and-allocation-synthesis.md](LTM/compilation-tiers-and-allocation-synthesis.md) | [dual-tier-design-and-baseline-correctness.md](LTM/dual-tier-design-and-baseline-correctness.md), [scopes-and-register-allocation.md](LTM/scopes-and-register-allocation.md), [floating-point-and-literal-layout.md](LTM/floating-point-and-literal-layout.md), [simd-lowering-and-expansion-budgets.md](LTM/simd-lowering-and-expansion-budgets.md) |
| [data-and-managed-execution-synthesis.md](LTM/data-and-managed-execution-synthesis.md) | [data-views-cells-and-finalizers.md](LTM/data-views-cells-and-finalizers.md), [managed-execution-and-stack-safety.md](LTM/managed-execution-and-stack-safety.md) |
| [compile-time-and-scratch-memory-synthesis.md](LTM/compile-time-and-scratch-memory-synthesis.md) | [compile-cost-investigations.md](LTM/compile-cost-investigations.md), [compile-scratch-retention-and-growth.md](LTM/compile-scratch-retention-and-growth.md), [packed-physical-program-and-store-forwarding.md](LTM/packed-physical-program-and-store-forwarding.md) |

Intentionally standalone: [architecture-and-api-decisions.md](LTM/architecture-and-api-decisions.md), [optional-hardening-and-barrier-contracts.md](LTM/optional-hardening-and-barrier-contracts.md), [operation-semantics-and-reference-oracles.md](LTM/operation-semantics-and-reference-oracles.md) and [verification-benchmarking-and-review-practice.md](LTM/verification-benchmarking-and-review-practice.md). Each already provides a cohesive overview.

Historical fixed Var homes versus later SSA promotion and benchmark results from different baselines are explicitly distinguished. Packed physical instruction sizes are qualified by Go host width: 64/136 bytes packed/located on a 64-bit host and 52/88 on a 32-bit host, as checked in `compile/physical_code_test.go`; cross-compiling 386 does not change host struct layout.

Candidates for a future `distill-memories` pass (flagged only):

- `.agents/docs/QUALITY_GATE.md` does not currently exist. The verification overview supplies candidate conventions: confirm test selection executed, snapshot listings and reports, test a flag reader across clobbers, exercise raw encoder fields, test cold/recurring/above-cap scratch demands, and isolate matched performance/scaling runs.
- `docs/sealed-programs.md` already assigns output caches to Units. Consider extending its ownership rationale with the durable physical program's lazy consumers, packed decode strategy and host-width layout qualification.
- `docs/backend-parity.md` and target `FORMS.md` can be checked for complete flag/implicit-write contracts and the rule that form changes update sizes and effects together. This pass adds no encoding forms; promote only gaps found against the existing canonical text.

README already describes the current cell release, indexed-borrow, stack-budget and recurring-scratch contracts; no duplicate user-facing promotion was proposed. Canonical docs and source LTM documents were not edited by this pass.

---

## 2026-11-05 — Consumer references and internal architecture

At the user's request, consolidated internal implementation and historical evidence from `docs/` into [ARCHITECTURE.md](ARCHITECTURE.md). Removed the old scoped-variable/FP implementation reports and six completion JSON files plus the allocation baseline JSON from `docs/` after preserving their content: implementation invariants and review mechanisms are synthesized by subsystem, numerical tables retain their measurement context, and all seven JSON payloads are embedded as collapsed historical snapshots with every original field/sample. Keeping those as separate consumer pages or dropping their measurement evidence was rejected.

Replaced the long tier implementation proposal with a current consumer guide at its existing public path, covering tier selection, bound/unbound Builders, segment ownership, results/errors and tier-up. Its internal design and alternatives now live in ARCHITECTURE. Moved backend parity and SIMD acceptance/review/roadmap history into the internal guide while preserving public operation/ABI contracts, feature queries and examples. SIMD carryless documentation now consistently identifies ARM PMULL/PMULL2 as implemented; its earlier closing sentence still called them future work despite the preceding implemented description.

Also separated proposed SIMD operators/floating/scalable-memory contracts from implemented consumer operations and moved register-allocation policies plus target flag/loop internals out of the public value/call reference. Public references keep value lifetimes, calls, limits, feature behavior and current examples; the internal guide retains design rationale and explicitly labeled roadmap contracts.

Updated AGENTS, README, E2E links, open TODO references, LTM navigation and the distill-memories destination guidance. Checked TODO descriptions and existing journal sections were preserved. Architecture separates historical design targets from measured tier results and marks archived pending roadmap statuses as historical; TODO.md remains the active work list.

Validation compares all embedded JSON objects with the originals, checks local links/anchors and moved-file references, confirms untouched consumer specifications and Go files, and checks Markdown whitespace/punctuation. This is a documentation reorganization; no code or exported API changed, and no benchmarks or Go test suites were run.
