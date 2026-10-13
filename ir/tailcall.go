package ir

// validateTailCall checks a TermTailCall step's call operands: a 32- or
// 64-bit scalar target and scalar arguments of at most 64 bits, as OpCallC
// takes them. The step's Args are already known to be available values.
func validateTailCall(spec *Program, stepIndex int, step ControlStep) error {
	target := spec.Instructions[step.Args[0]].ValueType()
	if target.Kind != ScalarKind || target.LaneBits != 32 && target.LaneBits != 64 {
		return invalidRule("step %d: tail call target %d must be a 32- or 64-bit scalar", stepIndex, step.Args[0])
	}
	for _, id := range step.Args[1:] {
		if t := spec.Instructions[id].ValueType(); t.Kind != ScalarKind || t.LaneBits > 64 {
			return invalidRule("step %d: tail call argument %d must be a scalar of at most 64 bits", stepIndex, id)
		}
	}
	return nil
}
