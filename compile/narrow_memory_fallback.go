package compile

import (
	"errors"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

// Native narrow operations change scheduling and register lifetimes relative
// to their byte expansions. Keep the native plan whenever it succeeds; retry
// the byte plan only when pressure exceeds the available registers/frame.
// lowerUnitInput lowers input and publishes the Unit's report view. The view
// shares the report's storage, which nothing modifies after this point.
func lowerUnitInput(input *loweringInput, options compileOptions, kind emissionKind, boundary callableBoundary) (*Unit, error) {
	u, err := lowerUnitPlan(input, options, kind, boundary)
	if err != nil {
		return nil, err
	}
	u.view = abi.ReportOf(u.report)
	return u, nil
}

func lowerUnitPlan(input *loweringInput, options compileOptions, kind emissionKind, boundary callableBoundary) (*Unit, error) {
	unit, err := lowerUnitInputOnce(input, options, kind, boundary)
	if input == nil {
		return unit, err
	}
	managedLimit := 0
	if boundary == boundaryManaged {
		if target := newMachineTarget(options); target != nil {
			managedLimit = target.ManagedFrameLimit()
		}
	}
	// Saving preserved registers can need more frame than the spills they
	// replace, since save slots are never reused. When a small frame budget
	// or the managed-frame limit is exceeded, allocate volatile registers only.
	if errors.Is(err, ErrStackRequired) || err == nil && managedLimit > 0 && unit.report.StackBytes > managedLimit {
		withheld := options
		withheld.withholdPreserved = true
		if retried, retryErr := lowerUnitInputOnce(input, withheld, kind, boundary); retryErr == nil &&
			!(managedLimit > 0 && retried.report.StackBytes > managedLimit) {
			unit, err = retried, nil
			options = withheld
		}
	}
	source := input.fallbackSource
	input.fallbackSource = nil
	// Template enforces the managed-frame limit after ordinary allocation.
	// Select a fitting retry here, before publishing a Unit, so Listing,
	// Report, encoding and Template all observe the same immutable plan.
	lateFrameLimit := err == nil && managedLimit > 0 && unit.report.StackBytes > managedLimit
	if source == nil || !lateFrameLimit && (err == nil ||
		!errors.Is(err, ErrStackRequired) && !(targetArchitecture(options) == "386" && errors.Is(err, ErrBackendLimit))) {
		return unit, err
	}
	hasNarrow := false
	for _, ins := range source.Instructions {
		hasNarrow = hasNarrow || ins.NarrowMemoryBits() != 0
	}
	if !hasNarrow {
		return unit, err
	}
	fallback, operations, fallbackErr := legalizeVectorsWithMemoryFallback(source, options, true)
	if fallbackErr != nil {
		return unit, err
	}
	facts := newProgramFacts(fallback)
	retry := &loweringInput{
		source: fallback, facts: facts, code: fallback.Instructions,
		leaves: fallback.ScopeExits, control: fallback.Control,
		arity: fallback.Arity, result: fallback.Result, resultParts: fallback.ResultParts,
		paramBits: input.paramBits, vectorOperations: operations, origin: input.origin,
	}
	retried, fallbackErr := lowerUnitInputOnce(retry, options, kind, boundary)
	if fallbackErr != nil || lateFrameLimit && (retried.report.StackBytes > managedLimit || retried.report.WorkspaceBytes != 0) {
		// Keep the original plan's diagnostic when neither plan fits.
		return unit, err
	}
	retried.report.LoweringStats.NarrowMemoryRetries++
	return retried, nil
}

// fallbackStore uses a widened native-address byte-store plan:
// widen once, shift native words, and narrow each byte before storing it.
// This can shorten register lifetimes enough to fit a small platform frame.
// On 386 the ordinary scalarStore keeps shifts single-word instead.
func (l *vectorLegalizer) fallbackStore(bits uint8, address, value int, order ir.ByteOrder) {
	// Widen the base before adding byte offsets. A 32-bit address can cross
	// the 4 GiB boundary on a 64-bit target; narrowing every offset would wrap.
	address = l.address(address, 0)
	width := l.bits
	value = l.cast(value, width)
	for offset := 0; offset < int(bits/8); offset++ {
		at := address
		if offset != 0 {
			at = l.binary(ir.OpAdd, width, address, l.constant(width, uint64(offset)))
		}
		part := value
		shift := offset * 8
		if order == ir.ByteOrderBigEndian {
			shift = int(bits) - 8 - shift
		}
		if shift != 0 {
			part = l.emit(ir.Instruction{Op: ir.OpShiftRight, Bits: width, A: value, Imm: uint64(shift)})
		}
		part = l.cast(part, 8)
		l.emit(ir.Instruction{Op: ir.OpStore8, Bits: 8, A: at, B: part})
	}
}
