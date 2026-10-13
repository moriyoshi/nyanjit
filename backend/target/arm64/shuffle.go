package arm64

import (
	"fmt"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// shuffleIndices returns the lane indices of an OpVShuffle (byte i of Imm,
// then of Disp) and the lane count, or false when an index is out of range
// or a byte beyond the last lane is nonzero.
func shuffleIndices(ins *machine.Instruction) ([16]byte, int, bool) {
	var indices [16]byte
	if !validWidth(ins.Bits) {
		return indices, 0, false
	}
	n := 128 / int(ins.Bits)
	for i := range indices {
		word := ins.Imm
		if i >= 8 {
			word = ins.Disp
		}
		indices[i] = byte(word >> (8 * (i % 8)))
		if i < n && int(indices[i]) >= 2*n || i >= n && indices[i] != 0 {
			return indices, 0, false
		}
	}
	return indices, n, true
}

// validLaneMove checks the operands of a shuffle or widening extend.
func validLaneMove(i *machine.Instruction, vec func(r backend.Register) bool) bool {
	switch i.Arithmetic {
	case op.OpVShuffle:
		// V30 and V31 hold the index table and its constant.
		_, _, ok := shuffleIndices(i)
		scratch := func(r backend.Register) bool { n := backend.VectorNumber(r); return n == 30 || n == 31 }
		return ok && vec(i.A) && vec(i.B) && vec(i.C) && !scratch(i.A) && !scratch(i.B) && !scratch(i.C)
	case op.OpVExtendUnsigned, op.OpVExtendSigned:
		return (i.Bits == 16 || i.Bits == 32 || i.Bits == 64) && i.Imm <= 1 && vec(i.A) && vec(i.B)
	}
	return false
}

// lanePermutations are the single-instruction NEON permutes, each with the
// lane its output lane i reads from P‖Q.
var lanePermutations = [...]struct {
	word uint32
	name string
	lane func(i, n int) int
}{
	{0x4e003800, "zip1", func(i, n int) int { return i/2 + i%2*n }},
	{0x4e007800, "zip2", func(i, n int) int { return n/2 + i/2 + i%2*n }},
	{0x4e001800, "uzp1", func(i, n int) int { return 2 * i }},
	{0x4e005800, "uzp2", func(i, n int) int { return 2*i + 1 }},
	{0x4e002800, "trn1", func(i, n int) int { return i&^1 + i%2*n }},
	{0x4e006800, "trn2", func(i, n int) int { return i | 1 + i%2*n }},
}

// lanePlan appends the expansion of a shuffle or widening extend. B and C
// may alias A or each other. A shuffle uses, in order of preference, a move,
// DUP (element), ZIP/UZP/TRN, EXT, or TBL with an index table built in V31
// from X16; two sources add TBX, and a V30 constant when the first TBL
// writes A.
func lanePlan(plan *concretePlan, ins machine.Instruction, a, b, c uint32, text bool) error {
	emit := func(word uint32, f string, args ...any) {
		plan.parts[plan.n].word = word
		if text {
			plan.parts[plan.n].text = fmt.Sprintf(f, args...)
		}
		plan.n++
	}
	size, suffix, lane := vectorLanes(ins.Bits)
	if ins.Arithmetic != op.OpVShuffle {
		// USHLL/SSHLL #0 (UXTL/SXTL); the 2 forms read the high half.
		word, name := uint32(0x2f00a400), "uxtl"
		if ins.Arithmetic == op.OpVExtendSigned {
			word, name = 0x0f00a400, "sxtl"
		}
		half := map[uint8]string{8: "8B", 16: "4H", 32: "2S"}[ins.Bits/2]
		if ins.Imm == 1 {
			word |= 1 << 30
			name += "2"
			half = map[uint8]string{8: "16B", 16: "8H", 32: "4S"}[ins.Bits/2]
		}
		emit(word|uint32(ins.Bits/2)<<16|b<<5|a, "%s V%d.%s, V%d.%s", name, a, suffix, b, half)
		return nil
	}
	indices, n, ok := shuffleIndices(&ins)
	if !ok {
		return machine.ErrUnsupportedOperation
	}
	if b == c {
		for i := range n {
			indices[i] %= byte(n)
		}
	}
	all := func(f func(i int) int) bool {
		for i := range n {
			if int(indices[i]) != f(i) {
				return false
			}
		}
		return true
	}
	// Identity of either source.
	for k, src := range [2]uint32{b, c} {
		if all(func(i int) int { return k*n + i }) {
			if a != src {
				emit(0x4ea01c00|src<<16|src<<5|a, "mov V%d.16B, V%d.16B", a, src)
			}
			return nil
		}
	}
	// Broadcast of one lane.
	if all(func(int) int { return int(indices[0]) }) {
		src, k := b, uint32(indices[0])
		if int(k) >= n {
			src, k = c, k-uint32(n)
		}
		imm5 := k<<(size+1) | 1<<size
		emit(0x4e000400|imm5<<16|src<<5|a, "dup V%d.%s, V%d.%s[%d]", a, suffix, src, lane, k)
		return nil
	}
	pairs := [4][2]int{{0, 1}, {1, 0}, {0, 0}, {1, 1}}
	registers := [2]uint32{b, c}
	for _, p := range lanePermutations {
		for _, pair := range pairs {
			if all(func(i int) int {
				x := p.lane(i, n)
				return pair[x/n]*n + x%n
			}) {
				x, y := registers[pair[0]], registers[pair[1]]
				emit(p.word|size<<22|y<<16|x<<5|a, "%s V%d.%s, V%d.%s, V%d.%s", p.name, a, suffix, x, suffix, y, suffix)
				return nil
			}
		}
	}
	for _, pair := range pairs {
		for k := 1; k < n; k++ {
			if all(func(i int) int {
				x := i + k
				return pair[x/n]*n + x%n
			}) {
				x, y := registers[pair[0]], registers[pair[1]]
				bytes := uint32(k) * uint32(ins.Bits) / 8
				emit(0x6e000000|y<<16|bytes<<11|x<<5|a, "ext V%d.16B, V%d.16B, V%d.16B, #%d", a, x, y, bytes)
				return nil
			}
		}
	}
	// TBL over byte indices. Two sources take B's bytes with TBL (C's
	// indices, 16 and up, are out of range and give zero), then flip bit 4
	// of the table so TBX fills C's bytes and leaves B's. When A is C, C is
	// read first instead.
	width := int(ins.Bits / 8)
	var table [16]byte
	single, first, second := true, b, c
	for i := range n {
		single = single && int(indices[i])/n == int(indices[0])/n
		for j := range width {
			table[i*width+j] = byte(int(indices[i])/n*16 + int(indices[i])%n*width + j)
		}
	}
	switch {
	case single:
		first = registers[int(indices[0])/n]
		for i := range table {
			table[i] &= 15
		}
	case a == c:
		first, second = c, b
		for i := range table {
			table[i] ^= 16
		}
	}
	tableHalves := func(half int) uint64 {
		var v uint64
		for i := 7; i >= 0; i-- {
			v = v<<8 | uint64(table[half*8+i])
		}
		return v
	}
	low, high := tableHalves(0), tableHalves(1)
	move := func(v uint64) {
		parts := immediatePlan(v)
		for _, part := range parts.parts[:parts.n] {
			emit(part.word(16), "%s X16, #%d, lsl #%d", part.op, part.imm, part.shift)
		}
	}
	move(low)
	if low == high {
		emit(0x4e080c00|16<<5|31, "dup V31.2D, X16")
	} else {
		emit(0x4e081c00|16<<5|31, "ins V31.D[0], X16")
		move(high)
		emit(0x4e181c00|16<<5|31, "ins V31.D[1], X16")
	}
	if single {
		emit(0x4e000000|31<<16|first<<5|a, "tbl V%d.16B, {V%d.16B}, V31.16B", a, first)
		return nil
	}
	emit(0x4e000000|31<<16|first<<5|a, "tbl V%d.16B, {V%d.16B}, V31.16B", a, first)
	emit(0x4f00e400|16<<5|30, "movi V30.16B, #16")
	emit(0x6e201c00|30<<16|31<<5|31, "eor V31.16B, V31.16B, V30.16B")
	emit(0x4e001000|31<<16|second<<5|a, "tbx V%d.16B, {V%d.16B}, V31.16B", a, second)
	return nil
}
