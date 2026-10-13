# Compile Time and Scratch Memory Synthesis

## Summary

Compile performance depends on transient allocation, retained pointer scanning, durable result ownership and data movement between representations. Profiles repeatedly found per-instruction sizing/formatting and buffer regrowth ahead of allocator algorithm costs. This document provides a decision sequence for reducing cost without changing output or hiding retained-memory regressions.

## Included Documents

| Document | Focus |
|----------|-------|
| [compile-cost-investigations.md](compile-cost-investigations.md) | Measured hotspots, allocation reductions and owned physical output |
| [compile-scratch-retention-and-growth.md](compile-scratch-retention-and-growth.md) | Buffer histories, decay, poison leases and recurring-demand retention |
| [packed-physical-program-and-store-forwarding.md](packed-physical-program-and-store-forwarding.md) | Durable compact storage, decode costs and above-cap pool regressions |

## Stable Knowledge

- Classify memory as scratch, immutable shared metadata or owned output before pooling. finishRegisters output becomes optimizedUnit.physical for lazy listing, encoding, Template and unwind consumers; it cannot return to scratch while the Unit lives.
- Record final demand before TrimPointers, Trim or Decay can discard it. Grow geometrically toward remembered demand and stop there; jumping directly to stale peak demand overallocates.
- Retention needs workload history. The established policy uses a 4x band under alternation, tightening to 2x after 32 consecutive below-half requests. Recurring large demand earns 16-lowering credit; larger pointer buffers release after 16-24 small lowerings within the 32 MiB entry budget.
- Poison leases detach old arrays and provide fresh poison-filled storage with the same capacity/history. Zeroing new leases can hide reuse bugs.
- A pool adds no reuse above its cap. Transfer unpoolable vector output directly rather than copying it solely to return an unusable buffer.
- Compact representation performance depends on stores and copies, not just byte count. Write packed slots and decoded destinations in place; batch full decodes and let narrow queries read only needed fields.

## Operational Guidance

Profile the current target/tier before attributing cost to the newest change. 386 recovery found an 848-byte FlagFacts frame, fresh size-query assemblers and plans formatting text even for bytes/sizes. The fixes improved optimized 386 20.3% and baseline 45.4%, mostly eliminating costs older than the motivating regression. Keep text formatting out of size/byte queries, and preserve checks while leasing verifier tables or skipping proven identity copies.

Check both cold/one-off and recurring/alternating demand. Lost stream history remembered 16,006 instructions for a roughly 124k-instruction workload, causing nine grows, about 70 MB allocated and 57 MB copied per lowering. Observing before trim and retaining recurring demand improved optimized 386 15.6% with 55% less B/op, but loong64 sign-aliases regressed 3-6% because scanning retained memory outweighed reuse. Retention is a measured tradeoff.

The packed program first saved 10-26% memory but slowed compilation 1.8-9.2%. Narrow field stores followed by 136-byte copies stalled store forwarding. In-place packing/decoding, batches of 16 and specialized FP/spill queries changed the outcome to 0.5-5.9% faster across targets while keeping the memory reduction. The cited 64-byte packed/136-byte located sizes describe the 64-bit build host; current `compile/physical_code_test.go` explicitly checks 52/88 bytes on a 32-bit host. Compiling a 386 target on a 64-bit host does not change the Go struct layout.

Keep baselines visible. The initial compact-only 10-26% memory result and the work-summary 18-38% reduction against 537ae6f use different comparisons. Likewise, older and later Cranelift ratios came from separate runs. Source documents retain the exact measurements; do not combine their percentages. BenchmarkZZW8 is a historical local harness absent from the checkout.

## Files

- `backend/machine/work.go`, `backend/machine/stream.go`: histories, leases and stream growth.
- `compile/physical_code.go`, `compile/physical_code_test.go`: packed output, side tables and host-width size checks.
- `compile/report_fp.go`, `compile/report_special.go`, `compile/report_writes.go`: specialized packed queries.
- `compile/vector_legalize.go`, `compile/literals.go`: output transfer and literal handling.
- [Compile-time baseline](../ARCHITECTURE.md#compile-time-baseline), `instrument/jitbench/`: baseline evidence and repeatable measurements.

## Tests

Focused checks from the repository root:

```sh
taskset -c 5 timeout 180s go test ./compile -run '^(TestPackedInsnCoversInstruction|TestPackedInsnIsCompactAndPointerFree|TestFloatWriteKeyCoversInputs|TestMediumCompileScratchReturnsToBaseline)$' -count=1
```

Pair snapshots/listing-report identity with poison builds, per-held-result retention tests and matched timing runs. Use the jitbench skill for alternating binaries on one quiet core 5 with GOMAXPROCS=1. Follow the review protocol for isolated scaling and retention gates; do not infer time or retained heap from allocs/op alone.

## Pitfalls

Zero-capacity slices can still retain backing arrays. B/op missed a +51% held-result regression. A three-build comparison (before / small fixes / everything) was needed to expose compact overhead hidden by an unrelated assembler gain. A capped legalizer pool added 7-20% overhead above 4 MiB. A size-query test must assert its actual guarantee (less allocation than a fresh assembler), not an unmeasured zero-allocation expectation. See [verification-benchmarking-and-review-practice.md](verification-benchmarking-and-review-practice.md) before making a performance claim.
