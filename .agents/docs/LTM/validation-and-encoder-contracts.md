# Validation and Encoder Contracts

## Summary

Validation must cover every field lowering reads, while lowering must ignore every field validation intentionally ignores. Public encoders require independent adversarial coverage because compile produces only a subset of their inputs.

## Key Facts

- Use `ir.Opcode.ProducesValue` to classify operand-producing instructions consistently.
- Check structural ranges before semantic traversal or indexing.
- Invalid used encoding fields reject rather than truncate; invalid size plans return zero.
- Per-op `operandFields` tables identify fields to validate; unused fields are cleared consistently for size/encoding.
- Bound expanded work, not just input IR length.

## Details

Rounds 6-10 repeatedly found validation/lowering disagreement: stray predicate children; predicate inputs absent from step.Values or referring to OpSet; stores/scope markers used as operands; invalid Step.Arity and non-entry OpParam; vector legalization reading ignored TermJump.Result. Round 11 rejected unused control metadata before traversing predicates, preserving documented ignored jump/label results and both 0/-1 empty-destination conventions. Its review included 708 mutations with cyclic/over-budget unused predicate graphs.

Raw-IR audit F6 covered out-of-range Var declarations, ScopeLeave.Args, Other/OtherArgs/Predicate on nonbranches, label-parameter context/EntryReg, constant and load widths, and malformed scope marker shapes. ScopeLeave.Args initially remained unvalidated and panicked every backend. Width checks also require matching arithmetic, shifts/rotations and Select arms. ResultParts must contain 64-bit scalars, not merely width-compatible vectors (F18). Narrow equality/unsigned compare immediates must fit `[0, 2^w-1]` (L17).

F5 caps predicate expansion at 16,384 nodes program-wide using cycle-safe traversal. One depth-12 three-branch PoC improved 23.1 ms/31 MiB -> 0.72 ms/under 1 MiB as an early error (single observations). Builder Finish and bound Lower report `LimitPredicateNodes`, not ErrInvalidFunction. Vector legalization has its separate 64n + 131,072 cap.

FORMS auditing exposed forms ordinary compile seldom emits:

| Target/form | Failure and correction |
|---|---|
| ppc64 MulHigh32 | mulhwu leaves upper bits undefined; append clrldi |
| MulWide outside 386 | Visit rewrote it as high multiply; on loong64 it overwrote thread-pointer r2; reject outside 386 |
| amd64/386 FloatBits to GP | XMM0 mask overwritten; require an XMM destination other than XMM0 |
| amd64 Inc PreserveFlags | Reject unless IncrementNoFlags is available |
| amd64 BinaryImmediate Bits=0 | Size reported four but encoding rejected; size now follows legality |
| riscv64 Far without Long | Size 12 vs emitted 4; no x30 write and size four |
| ppc64 LiteralData | Reject non-word lengths consistently |
| amd64/386 Scaled | Reject invalid scales, SP index and amd64 displacement above 32 bits |
| 386 fields | Reject truncated Const64/displacements, frame below 16, bad arithmetic, ignored Bits64 and GP-as-XMM operands |
| loong64 zero-scale address | Imm=0/Disp=0 means base B, not B+C |
| arm64 frame | Require 16-byte alignment |
| ppc64 FloatUnary/FloatBits | Reject unsupported arithmetic rather than default fsqrt/fneg |
| riscv64 WordCarry/LiteralLoad | Reject CarryFlags and 128-bit literal loads rather than silently choosing another form |

ARM spill slot 4086 originally overflowed imm12/opc after frame overhead: STR X7,[SP,#32768] became LDR X7,[SP]. Capacity subtracts LR, Go homes and entry saves; byte encoders reject unencodable offsets. Raw rotations with Bits=0 looped indefinitely in arm64/riscv64/loong64; sticky errors and width guards replace hangs/panics. Bounded FP plans and register/condition checks also protect raw Visit and size queries. An in-place ppc64 SignExtend64 with requested flags fails closed.

Repeated Compact/Relax layout operations must release scratch on success and error. A conflicting defer and an early-return alias once let the next workAppend clear the live stream. The regression interleaves failing Relax/Compact calls (`CRCRXR`), not just successful repeats.

F14 requires aliased labels to declare identical parameters and share parameter homes; 68/500 control programs previously read unwritten slots. F15 counts FP-to-GPR writes and repeats control lowering if edge emission wrote unsaved callee-preserved registers. F21 validates wide allocation storage, limits single-slot reuse to one-word values and sizes edge temporaries for the widest stored value. StorageWords >=3 previously escaped frames.

## Files

- `ir/program.go`, `ir/variable.go`, `ir/predicate_limits.go`: IR validation and bounded expansion.
- `compile/vector_legalize.go`, `compile/emitter_control.go`: consumers and aliased homes/preservation.
- `backend/target/*/FORMS.md`, target `validation.go`/encoders: public forms and used-field contracts.
- `backend/machine/branches.go`, `backend/machine/work.go`: layout scratch ownership.
- `backend/regalloc/`: wide storage validation.

## Test Coverage

`FuzzSecurityRawIR` mutates all opcodes, including vector programs, without panic. `FuzzEncoderVisit` varies every operand, stack practices and size/write/flag queries on all six backends, including the ppc64 Emitter. Tests include `TestDocumentedForms`, `TestSizeAgreesWithEncoding`, `TestEncoderFieldsRejectBeforeEncoding`, `TestEncoderDirectFieldsAndState`, `TestInvalidEncoderPlansHaveZeroSize`, `TestCPUSizePlansRejectInvalidFields`, `TestExtensionWidthsAndNativeCopy`, `TestMulWideIsI386Only`, `TestSizeOfRejectedForms`, `TestZeroScaleIndexedMemory`, `TestI386InvalidOperands`, `TestEncoderInvalidOperandTable`, `TestSecurityRepeatedLayoutPasses`, `TestValidateResultPartsRequireScalars`, `TestValidateNarrowCompareImmediates`, `TestAliasedLabelsShareParameterHomes`, `TestValidateAliasedLabelParameters`, `TestCEntriesReportOnlyCallerSavedClobbers`, `TestARM64EntriesRestoreEveryPreservedRegister` and `TestNarrowMemorySmallFrameRegression`. The journal's `TestFunctionAllocatorSlotsFitWideValues` is now `TestBatchSlotsFitWideValues` in `backend/regalloc/function_alloc_slots_test.go`.

Bounds tests cover immediate-range edges 4095/4096, 2^31-1/2^31 and 2^32-1/2^32, signed/unsigned conditions and dirty upper 32-bit inputs. `TestCompareImmediateRegisterDifferential` covers 18,144 immediate/register/reference cases; `TestArithmetic32SpillReloadIndex` asserts that the arithmetic result really spills/reloads and executes both bounds edges. Related tests are `TestCheckedIndexCompareImmediateEdges`, `TestCheckedIndexDirtyUpper32Bits`, `TestCheckedIndexNarrowSpillReload` and `TestCheckedDataIndexMemoryBoundaries`. Earlier inputs masked away dirty bits, and one spill path only ran on riscv64/loong64, so merely naming a test after the bug was insufficient.

## Pitfalls

Size agreement alone does not prove instruction semantics. A hanging fuzz worker once still resulted in printed PASS. Confirm bounded completion, and arrange tests so the suspect field is consumed. Checked findings in TODO.md intentionally retain old API names and are historical evidence, not current contracts.

## Sources

Consolidated from these headings in `.agents/docs/JOURNAL.md`:

- 2026-10-14 — Backend parity with PPC64, and CPU profiles
- 2026-10-17 to 2026-10-18 — Review rounds 6-10: scratch retention, poison builds, and validation that lowering trusts
- 2026-10-18 — First security audit: frames, encodings and the call boundary
- 2026-10-19 — Rounds 11-13: retries, report ownership, and measurement noise
- 2026-10-23 to 2026-10-24 — Writing FORMS.md found encoder bugs compile never hits; a QEMU hang that was not ours
- 2026-10-25 — Pre-release security audit: four high-severity bugs and their root causes
- 2026-10-25 — Second audit: the Medium and Low findings, and how the audit ran
