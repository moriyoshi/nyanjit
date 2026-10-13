# Operation Semantics and Reference Oracles

## Summary

Portable operations specify exceptional behavior before target encoding, and interpreter oracles must implement that meaning independently. New operations also stress shared opcode tables, target reports and enum correspondence during integration.

## Key Facts

- DivisionZero is the division default; DivisionAllOnes and DivisionUnchecked are explicit alternatives.
- ConversionSaturate is the conversion default; ConversionNative chooses target behavior.
- FP comparisons have 14 FloatConditions with both value and predicate forms.
- Do not inherit host-runtime arithmetic errors into the reference interpreter.
- Derive enums that must agree from one another and use explicit valid-term lists.

## Details

The sljit comparison used commit 39c508d to identify division, int/float conversions, compare-as-value, FP comparison, atomics, indirect jumps, float call ABI, local stack areas and other gaps. Public additions were proposed with exact signatures before implementation.

The initial six-target work added division modes, conversion modes, FCompare/FComparePredicate and signed high multiply. 386 64-bit division uses inline Hacker's Delight divDU; signed high multiply is native on all 64-bit targets and at 32 bits on 386. Later approved coverage included compare/branch forms, atomics, switch/jump tables, float ABI, stack areas, tail calls, prefetch and SIMD extensions, across both tiers.

Two independent-oracle corrections matter:

- Go 1.27.2 on 386 misrounds float32(int64) and float32(uint64) for about 1.5% of random inputs by rounding through float64. irinterp rounds to odd at 53 bits before the final conversion instead of blindly agreeing with the runtime.
- FMA32 is verified as a single rounding with big.Float and a conformance triple exposing double rounding.

Parallel opcode additions share op/ir/machine enumerations, String tables, baselineShapes, interpreter dispatch, irconform corpus, IR docs, README and every FORMS table. A clean textual merge still broke switches: builderTerm had independent iota values, so after TermTailCall and TermSwitch landed, builderSwitch=3 while ir.TermSwitch=4. Each builder constant now derives from its ir.Term counterpart. Range checks such as op > TermReturn also became explicit valid-term lists.

Tests for new tail calls exposed unsaved 386 baseline C-leaf EBX/ESI/EDI, while shift/branch cases exposed narrow SAR normalization and SIMD work exposed missing ARM X16 clobber reporting. These failures are independent of the new operation's semantic implementation and belong in both operation and ABI/report testing.

## Files

- `docs/ir.md`, `ir/`, `builder/`: operation contracts and term definitions.
- `instrument/irinterp/`: independent reference semantics.
- `instrument/irconform/`: cross-target operation cases.
- `backend/target/*/FORMS.md`: encoding contracts for public operation forms.

## Test Coverage

Execute irconform on both tiers, with relevant hardening and CPU profiles. Include division by zero, overflow/conversion boundaries, NaN conditions, double-rounding inputs and mixed operation/control programs. Operation tests do not replace ABI clobber litmus tests or raw encoder tests.

## Pitfalls

An interpreter agreeing with its host can be wrong on both sides. Byte listings validate selection but not arithmetic meaning; Go comparisons need independent rounding where the runtime is known wrong. Merge success provides no evidence that numeric enums, String tables and dispatch tables still correspond.

## Sources

Consolidated from these headings in `.agents/docs/JOURNAL.md`:

- 2026-10-28 — sljit gap analysis, and the first semantic gaps filled
- 2026-10-30 — Eleven sljit-gap items in one wave, and what seven parallel merges taught
- 2026-11-01 — Scaled multiplication and a shared stack VM
