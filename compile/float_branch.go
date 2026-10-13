package compile

import (
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

// terminalFloatBranch recognizes an FP comparison whose boolean is used only
// by the next predicate. ARM64 can discard the boolean through WZR. AMD64
// keeps SETcc but reuses UCOMISx's flags instead of testing the boolean again.
func terminalFloatBranch(arch string, ops []instruction, result int, resultReg physReg, fragment []physicalInsn, branch emissionStep) (condition ir.BranchCondition, discard, valid bool) {
	node := branch.predicate
	for node != nil && node.Kind == ir.PredicateNot {
		node = node.Left
	}
	if node == nil || node.Kind != ir.PredicateCompare || node.WithValue || node.Immediate != 0 ||
		node.Condition != ir.BranchEqual && node.Condition != ir.BranchNotEqual || node.Value != result ||
		result < 0 || result != len(ops)-1 || ops[result].Op != ir.OpFCompare || len(fragment) == 0 {
		return 0, false, false
	}
	for _, ids := range [2][]int{branch.args, branch.otherArgs} {
		for _, id := range ids {
			if id == result {
				return 0, false, false
			}
		}
	}
	last := &fragment[len(fragment)-1]
	if last.Op != machine.OpFloatCompare || last.A != resultReg || last.Imm != ops[result].Imm || last.Bits != ops[result].Bits {
		return 0, false, false
	}
	switch arch {
	case "arm64":
		if last.Imm >= uint64(len(arm64FloatBranch)) || !arm64FloatBranch[last.Imm].ok {
			return 0, false, false
		}
		condition, discard = arm64FloatBranch[last.Imm].c, true
	case "amd64":
		if last.Imm >= uint64(len(amd64FloatBranch)) || !amd64FloatBranch[last.Imm].ok {
			return 0, false, false
		}
		condition = amd64FloatBranch[last.Imm].c
	default:
		return 0, false, false
	}
	if node.Condition == ir.BranchEqual {
		condition ^= 1
	}
	return condition, discard, true
}
