package compile

import (
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/regalloc"
)

// allocationProgram presents one selected program to regalloc.Residency: a
// position per instruction, then a final position that reads the values
// live after the last one (the result and the exports). It copies the slices
// it needs instead of pointing at the program, which allocateAndEmitMode
// receives by value.
type allocationProgram struct {
	values        []machineValue
	instructions  []machineInstruction
	result        int
	resultHigh    int // -1 without a second result word
	exports       []int
	discardResult bool
	registerBits  uint8
}

func (p *allocationProgram) release() { *p = allocationProgram{} }

func (p *allocationProgram) Values() int { return len(p.values) }

func (p *allocationProgram) Value(id int) regalloc.Value {
	v := p.values[id]
	return regalloc.Value{Class: v.Class, Words: v.Words, StorageWords: uint8(valueStorageWords(v, p.registerBits)), Constant: v.Constant}
}

func (p *allocationProgram) Positions() int { return len(p.instructions) + 1 }

func (p *allocationProgram) Inputs(position int, dst []int) []int {
	if position < len(p.instructions) {
		return p.instructions[position].InputsInto(dst)
	}
	if !p.discardResult {
		dst = append(dst, p.result)
	}
	if p.resultHigh >= 0 {
		dst = append(dst, p.resultHigh)
	}
	return append(dst, p.exports...)
}

// valueStorageWords returns the spill-slot words a value needs with
// registers of registerBits.
func valueStorageWords(v machineValue, registerBits uint8) int {
	if v.Class == backend.Float {
		bits := v.Bits
		if bits == 0 {
			bits = 64
		}
		return (int(bits) + int(registerBits) - 1) / int(registerBits)
	}
	if v.Class == backend.Vector {
		return 128 / int(registerBits)
	}
	if v.Words == 0 {
		return 1
	}
	return int(v.Words)
}
