package compile

import (
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/regalloc"
	"strconv"
)

// FP is a residency choice, not a different public scalar type. Keep mixed
// integer/FP variables in the integer bank; stores and ABI edges can copy bits.
func floatingHomes(p *ir.Program) []bool {
	homes := make([]bool, len(p.Variables))
	values := make([]bool, len(p.Instructions))
	for id, i := range p.Instructions {
		switch i.Op {
		case ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv, ir.OpFMA, ir.OpFSqrt, ir.OpFConvert, ir.OpFProductError:
			values[id] = true
		}
	}
	changed := true
	for changed {
		changed = false
		mark := func(v int) {
			if !homes[v] && p.Variables[v].Type.Kind == ir.ScalarKind && (p.Variables[v].Type.LaneBits == 32 || p.Variables[v].Type.LaneBits == 64) {
				homes[v] = true
				changed = true
			}
		}
		for id, i := range p.Instructions {
			if i.Op == ir.OpGet && homes[i.Variable] {
				values[id] = true
			}
			if values[id] {
				for _, x := range i.Inputs() {
					if p.Instructions[x].Op == ir.OpGet {
						mark(p.Instructions[x].Variable)
					}
				}
			}
			if i.Op == ir.OpSet && values[i.A] {
				mark(i.Variable)
			}
		}
		for v, desc := range p.Variables {
			if desc.Immutable && values[desc.Initial[0]] {
				mark(v)
			}
		}
	}
	for _, i := range p.Instructions {
		switch i.Op {
		case ir.OpAdd, ir.OpSub, ir.OpMul, ir.OpAnd, ir.OpOr, ir.OpXor, ir.OpShiftLeft, ir.OpShiftRight, ir.OpShiftRightArithmetic, ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeft, ir.OpRotateRight, ir.OpRotateLeftBy, ir.OpRotateRightBy:
			for _, x := range i.Inputs() {
				if p.Instructions[x].Op == ir.OpGet {
					homes[p.Instructions[x].Variable] = false
				}
			}
		}
	}
	return homes
}

func homeRegisterName(t machineTarget, r physReg) string {
	if !machine.IsVectorRegister(r) {
		return t.RegisterName(r)
	}
	prefix := "F"
	if t.Architecture() == "amd64" {
		prefix = "XMM"
	}
	if t.Architecture() == "arm64" {
		prefix = "V"
	}
	return prefix + strconv.Itoa(int(machine.VectorNumber(r)))
}
func markFloatingHomeGets(p *machineProgram) {
	if p.HomePlan == nil {
		return
	}
	for _, i := range p.Instructions {
		if i.Op == machine.SelectedHomeGet && p.HomePlan.FloatHomes[i.Variable] {
			p.Values[i.Dst].Class = regalloc.Float
		}
	}
}

func floatingLabelParams(p *ir.Program) [][]bool {
	if p.Control == nil {
		return nil
	}
	values := make([]bool, len(p.Instructions))
	ids := make([][]int, len(p.Control.Params))
	labels := make([][]bool, len(ids))
	for label, count := range p.Control.Params {
		ids[label] = make([]int, count)
		labels[label] = make([]bool, count)
		for n := range ids[label] {
			ids[label][n] = -1
		}
	}
	currentLabel := -1
	for _, step := range p.Control.Steps {
		if step.IsLabel {
			currentLabel = step.Label
			continue
		}
		for pos := step.Start; pos < step.End; pos++ {
			i := p.Instructions[pos]
			if i.Op == ir.OpLabelParam && currentLabel >= 0 && int(i.Imm) < len(ids[currentLabel]) {
				ids[currentLabel][i.Imm] = pos
			}
		}
	}
	for id, i := range p.Instructions {
		switch i.Op {
		case ir.OpFAdd, ir.OpFSub, ir.OpFMul, ir.OpFDiv, ir.OpFMA, ir.OpFSqrt, ir.OpFConvert, ir.OpFProductError:
			values[id] = true
		}
	}
	changed := true
	for changed {
		changed = false
		mark := func(id int) {
			if id >= 0 && !values[id] && (instructionBits(p.Instructions[id]) == 32 || instructionBits(p.Instructions[id]) == 64) {
				values[id] = true
				changed = true
			}
		}
		for id, i := range p.Instructions {
			if values[id] {
				for _, x := range i.Inputs() {
					if p.Instructions[x].Op == ir.OpLabelParam {
						mark(x)
					}
				}
			}
		}
		for _, step := range p.Control.Steps {
			if step.IsLabel {
				continue
			}
			for _, edge := range []struct {
				label int
				args  []int
			}{{step.To, step.Args}, {step.Other, step.OtherArgs}} {
				if edge.label < 0 || edge.label >= len(ids) {
					continue
				}
				for n, source := range edge.args {
					if n < len(ids[edge.label]) && values[source] {
						mark(ids[edge.label][n])
					}
				}
			}
		}
	}
	for label, list := range ids {
		for n, id := range list {
			labels[label][n] = id >= 0 && values[id]
		}
	}
	return labels
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
		if i.Op == ir.OpLabelParam && i.EntryReg >= 0 && machine.IsVectorRegister(physReg(i.EntryReg)) {
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
	if node.Kind == ir.PredicateCompare {
		return floating(node.Value) || node.WithValue && floating(node.Other)
	}
	return predicateNeedsFPBits(node.Left, ops, plan) || predicateNeedsFPBits(node.Right, ops, plan)
}

func (e *Emitter) labelWidth(label Label, param int) uint8 {
	if int(label) < len(e.labelBits) && param < len(e.labelBits[label]) {
		return e.labelBits[label][param]
	}
	return 64
}
