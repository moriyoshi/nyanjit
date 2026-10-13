package builder

import "github.com/moriyoshi/nyanjit/ir"

// Checked addresses are borrows confined to one straight-line checked block.
// Neither control edges nor mutable storage may extend the borrow's lifetime.
type checkedAddress struct {
	block int
	epoch uint64
}

func (b *Builder) currentBlock() int {
	if b.control != nil {
		return b.control.start
	}
	return 0
}
func (b *Builder) markCheckedAddress(v Value) {
	if b.checkedAddresses == nil {
		b.checkedAddresses = make(map[int]checkedAddress)
	}
	b.checkedAddresses[v.id] = checkedAddress{b.currentBlock(), b.dataEpoch}
}
func (b *Builder) checkedAddress(v Value) bool {
	if len(b.checkedAddresses) == 0 {
		return false
	}
	if v.composite != nil {
		for _, part := range v.components() {
			if b.checkedAddress(part) {
				return true
			}
		}
		return false
	}
	_, ok := b.checkedAddresses[v.id]
	return v.builder == b && ok
}
func (b *Builder) inheritCheckedAddress(id int, ins *instruction) {
	if len(b.checkedAddresses) == 0 || !ins.Op.ProducesValue() {
		return
	}
	switch ins.Op {
	case ir.OpLoad8, ir.OpLoad16, ir.OpLoad32, ir.OpLoad64, ir.OpVLoad, ir.OpCallC, ir.OpCallGo, ir.OpSyscall:
		// Loaded scalar snapshots can be kept in Vars; raw loaded pointers are
		// already part of the trusted raw-memory contract.
		return
	}
	var inputs [4]int
	for _, input := range ins.InputsInto(inputs[:0]) {
		if borrow, ok := b.checkedAddresses[input]; ok {
			b.checkedAddresses[id] = borrow
			return
		}
	}
}
