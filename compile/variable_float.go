package compile

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

func markFloatingHomeGets(p *machineProgram) {
	if p.HomePlan == nil {
		return
	}
	for _, i := range p.Instructions {
		if i.Op == machine.SelectedHomeGet && p.HomePlan.FloatHomes[i.Variable] {
			p.Values[i.Dst].Class = backend.Float
		}
	}
}

func predicateNeedsFPBits(node *ir.PredicateExpr, ops []ir.Instruction, plan *machine.HomePlan) bool {
	if node == nil {
		return false
	}
	floating := func(id int) bool {
		if id < 0 || id >= len(ops) {
			return false
		}
		i := ops[id]
		if i.Op == ir.OpLabelParam && i.EntryReg >= 0 && backend.IsVectorRegister(physReg(i.EntryReg)) {
			return true
		}
		if i.Op == ir.OpGet && plan != nil && plan.FloatHomes[i.Variable] {
			return true
		}
		switch i.Op {
		case ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv, ir.OpFMA, ir.OpFSqrt, ir.OpFConvert, ir.OpFProductError:
			return true
		}
		return false
	}
	for comparison, err := range predicateComparisons(node) {
		if err != nil {
			return false
		}
		if floating(comparison.Value) || comparison.WithValue && floating(comparison.Other) {
			return true
		}
	}
	return false
}

func (e *Emitter) labelWidth(label ir.Label, param int) uint8 {
	if int(label) < len(e.labelBits) && param < len(e.labelBits[label]) {
		return e.labelBits[label][param]
	}
	return 64
}
