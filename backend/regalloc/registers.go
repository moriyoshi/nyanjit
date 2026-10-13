package regalloc

import (
	"math/bits"

	"github.com/moriyoshi/nyanjit/backend"
)

// registerIndex is a Config's registers as indices: a table from register
// to index, and index bitsets for the Config's register sets. Index i
// is the i-th register of Config.Registers; the bitsets stay private so that
// callers name registers, not bit positions.
type registerIndex struct {
	count int
	// registers is Config.Registers by index.
	registers [64]backend.Register
	// index maps a physical register to its position in Config.Registers,
	// or -1.
	index                               [256]int8
	vector, blocked, scratch, preserved uint64
	// set is the Config.Registers indexed, once valid.
	set   backend.RegisterSet
	valid bool
}

func newRegisterIndex(config Config) registerIndex {
	f := registerIndex{count: config.Registers.Len()}
	for i := range f.index {
		f.index[i] = -1
	}
	for i := range f.count {
		r := config.Registers.At(i)
		f.registers[i] = r
		if f.index[r] < 0 {
			f.index[r] = int8(i)
		}
		if backend.IsVectorRegister(r) {
			f.vector |= 1 << uint(i)
		}
	}
	f.blocked = f.mask(config.Blocked)
	f.scratch = f.mask(config.Scratch)
	f.preserved = f.mask(config.Preserved)
	return f
}

// reindex makes f index config, keeping the index when Config.Registers
// is the set it already indexes.
func (f *registerIndex) reindex(config Config) {
	if !f.valid || f.set != config.Registers {
		*f = newRegisterIndex(config)
		f.set, f.valid = config.Registers, true
		return
	}
	f.blocked = f.mask(config.Blocked)
	f.scratch = f.mask(config.Scratch)
	f.preserved = f.mask(config.Preserved)
}

// mask returns the indices of the registers of set that Config.Registers
// holds; others have no index and cannot be chosen anyway.
func (f *registerIndex) mask(set backend.RegisterSet) uint64 {
	var m uint64
	for r := range set.All() {
		if i := f.index[r]; i >= 0 {
			m |= 1 << uint(i)
		}
	}
	return m
}

// all returns every index.
func (f *registerIndex) all() uint64 {
	if f.count == 64 {
		return ^uint64(0)
	}
	return 1<<uint(f.count) - 1
}

// group returns g's registers.
func (f *registerIndex) group(g indexGroup) Group {
	group := Group{Words: g.Words}
	for i := 0; i < int(g.Words); i++ {
		group.Registers[i] = f.registers[g.Indices[i]]
	}
	return group
}

// indices returns g by index, or false when one of its registers is not in
// Config.Registers or the group has more than two words.
func (f *registerIndex) indices(g Group) (indexGroup, bool) {
	if g.Words > 2 {
		return indexGroup{}, false
	}
	group := indexGroup{Indices: [2]int{-1, -1}, Words: g.Words}
	for i := 0; i < int(g.Words); i++ {
		index := f.index[g.Registers[i]]
		if index < 0 {
			return indexGroup{}, false
		}
		group.Indices[i] = int(index)
	}
	return group, true
}

// pins returns the indices of the pinned registers; pinned registers outside
// Config.Registers cannot be chosen anyway.
func (f *registerIndex) pins(p Pins) uint64 {
	var m uint64
	for word, set := range p.bits {
		for set != 0 {
			bit := bits.TrailingZeros64(set)
			set &= set - 1
			if i := f.index[word<<6|bit]; i >= 0 {
				m |= 1 << uint(i)
			}
		}
	}
	return m
}
