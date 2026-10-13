package compile

import (
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

// Test-only exports for package compile_test. Tests that use fixtures built
// with the root package (or packages that import it, such as
// instrument/workload) cannot live in package compile: the root package
// imports compile. These names compile only into this package's tests.

var (
	HostBackend       = hostBackend
	PinCompileScratch = pinCompileScratch
	AssertUnitOffsets = assertUnitOffsets

	CheckGiantTinyScratchAlternation              = checkGiantTinyScratchAlternation
	CheckSealedLoweringBorrowsFactsWithoutCloning = checkSealedLoweringBorrowsFactsWithoutCloning
	CheckSealedConcurrentLoweringsKeepFacts       = checkSealedConcurrentLoweringsKeepFacts
	CheckUnitReportListingAreLazyAndDetached      = checkUnitReportListingAreLazyAndDetached
	CheckUnitConcurrentOutputs                    = checkUnitConcurrentOutputs
)

const MaxSpillSlots = maxSpillSlots

type CompileScratch = compileScratch

// Retained returns the bytes the scratch kept after its last release.
func (s *CompileScratch) Retained() int { return s.retained }

// KeepFlagWrites resolves spec into a target that lowers without dead-flag
// elision, so instructions keep their flag-writing forms. It is not cached.
func KeepFlagWrites(spec TargetSpec) (Target, error) {
	t, err := resolveTarget(spec)
	if err != nil {
		return nil, err
	}
	t.keepFlagWrites = true
	return t, nil
}

// EncodedBytes returns u's unlinked machine code.
func EncodedBytes(u *Unit) ([]byte, error) {
	e, err := u.encoded()
	return e.Bytes, err
}

// SelectProgram selects ops for target without a workspace, for tests that
// inspect selection decisions.
func SelectProgram(ops []ir.Instruction, leaves []ir.ScopeExit, arity, result int, exports []int, maxArgs int, target selectionTarget) (machine.SelectedProgram, error) {
	return selectProgramBuffers(ops, leaves, arity, result, exports, maxArgs, target, nil)
}

// WithoutVarPromotion lowers Vars through their homes for the rest of t, for
// tests of home storage and its limits. Tests that use it must not run in
// parallel.
func WithoutVarPromotion(t interface{ Cleanup(func()) }) {
	old := variablePromotion
	variablePromotion = false
	t.Cleanup(func() { variablePromotion = old })
}
