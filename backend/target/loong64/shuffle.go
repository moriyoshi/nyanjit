package loong64

import (
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// lsxSuffixes names the element of each size index (8 to 64 bits).
var lsxSuffixes = [4]string{"b", "h", "w", "d"}

// shuffleIndices unpacks the lane indices of an OpVShuffle: lane i's index
// is byte i of Imm, and byte i-8 of Disp from lane 8 on. Bytes beyond the
// last lane must be zero and each index must be below twice the lane count.
func shuffleIndices(i machine.Instruction) ([]int, bool) {
	if !validWidth(i.Bits) {
		return nil, false
	}
	n := 128 / int(i.Bits)
	indices := make([]int, n)
	for lane := range 16 {
		var x uint64
		if lane < 8 {
			x = i.Imm >> (8 * lane) & 0xff
		} else {
			x = i.Disp >> (8 * (lane - 8)) & 0xff
		}
		if lane >= n {
			if x != 0 {
				return nil, false
			}
			continue
		}
		if x >= uint64(2*n) {
			return nil, false
		}
		indices[lane] = int(x)
	}
	return indices, true
}

// shufflePlan emits an OpVShuffle into a: one instruction whose fixed
// pattern matches the indices at the lane width or a finer one (a move,
// vreplvei, vilvl/vilvh, vpickev/vpickod or vshuf4i), or else vshuf.b with
// the byte index table built in the reserved vr0 through r19. Every
// instruction reads all its sources before writing a, so any aliasing works.
func (e *ByteAssembler) shufflePlan(i machine.Instruction, a, b, c uint32, write func(uint32)) error {
	indices, ok := shuffleIndices(i)
	if !ok {
		return machine.ErrUnsupportedOperation
	}
	n := len(indices)
	if b == c {
		for k := range indices {
			indices[k] %= n
		}
	}
	original := indices
	reg := func(index, n int) uint32 {
		if index < n {
			return b
		}
		return c
	}
	size := uint32(0)
	for 8<<size != uint32(i.Bits) {
		size++
	}
	for {
		n := len(indices)
		suffix := lsxSuffixes[size]
		if e.shuffleMatch(indices, size, suffix, a, b, c, reg, write) {
			return nil
		}
		if size == 3 && (a == b || a == c) {
			e.shuffle4d(indices, a, b, c, write)
			return nil
		}
		if size == 0 {
			break
		}
		// Retry at half the lane width: each lane becomes two.
		finer := make([]int, 2*n)
		for k, x := range indices {
			lane, source := x%n, x/n*2*n
			finer[2*k], finer[2*k+1] = source+2*lane, source+2*lane+1
		}
		indices = finer
		size--
	}
	if i.Bits == 64 {
		e.shuffle4d(original, a, b, c, write)
		return nil
	}
	// vshuf.b reads index byte k as vk byte k below 16 and vj byte k-16 at or
	// above it: B is vk and C is vj, as in B‖C.
	var table [2]uint64
	for k, x := range indices {
		table[k/8] |= uint64(x) << (8 * (k % 8))
	}
	if table[0] == table[1] {
		e.Constant(19, table[0])
		e.emit((0x1ca7c0+3)<<10|19<<5, "vreplgr2vr.d vr0, r19")
	} else {
		for half, word := range table {
			e.Constant(19, word)
			e.emit(0x72ebf000|uint32(half)<<10|19<<5, "vinsgr2vr.d vr0, r19, %d", half)
		}
	}
	write(0)
	e.emit(0x0d500000|0<<15|b<<10|c<<5|a, "vshuf.b vr%d, vr%d, vr%d, vr0", a, c, b)
	write(a)
	return nil
}

// shuffle4d emits a 64-bit shuffle as vshuf4i.d, whose selector picks
// each result lane from vd‖vj: vd holds B (or C when a is C), so a is first
// set to B unless it already holds an input.
func (e *ByteAssembler) shuffle4d(indices []int, a, b, c uint32, write func(uint32)) {
	vj := c
	if a == c && a != b {
		// a holds C: number C's lanes first.
		vj = b
		indices = []int{indices[0] ^ 2, indices[1] ^ 2}
	} else if a != b {
		e.emit(0xe24d<<15|b<<10|b<<5|a, "vor.v vr%d, vr%d, vr%d", a, b, b)
	}
	selector := uint32(indices[0] | indices[1]<<2)
	e.emit(0x739c0000|selector<<10|vj<<5|a, "vshuf4i.d vr%d, vr%d, %d", a, vj, selector)
	write(a)
}

// shuffleMatch emits the single instruction whose fixed pattern produces
// indices (of 8<<size-bit lanes), if there is one.
func (e *ByteAssembler) shuffleMatch(indices []int, size uint32, suffix string, a, b, c uint32, reg func(int, int) uint32, write func(uint32)) bool {
	n := len(indices)
	same := func(f func(k int) int) bool {
		for k, x := range indices {
			if f(k) != x {
				return false
			}
		}
		return true
	}
	// A move of one input.
	for _, source := range []int{0, n} {
		if same(func(k int) int { return source + k }) {
			if r := reg(source, n); r != a {
				e.emit(0xe24d<<15|r<<10|r<<5|a, "vor.v vr%d, vr%d, vr%d", a, r, r)
				write(a)
			}
			return true
		}
	}
	// vreplvei repeats one lane.
	if same(func(int) int { return indices[0] }) {
		base := []uint32{0x72f78000, 0x72f7c000, 0x72f7e000, 0x72f7f000}[size]
		lane := uint32(indices[0] % n)
		e.emit(base|lane<<10|reg(indices[0], n)<<5|a, "vreplvei.%s vr%d, vr%d, %d", suffix, a, reg(indices[0], n), lane)
		write(a)
		return true
	}
	// Two-source patterns, vd = f(vj, vk), for each choice of vj and vk.
	type pattern struct {
		base uint32
		name string
		// lane returns the source lane of result lane k: from vk when low.
		lane func(k int) (index int, fromK bool)
	}
	half := n / 2
	patterns := [...]pattern{
		{0x711a0000, "vilvl", func(k int) (int, bool) { return k / 2, k%2 == 0 }},
		{0x711c0000, "vilvh", func(k int) (int, bool) { return half + k/2, k%2 == 0 }},
		{0x711e0000, "vpickev", func(k int) (int, bool) { return 2 * (k % half), k < half }},
		{0x71200000, "vpickod", func(k int) (int, bool) { return 2*(k%half) + 1, k < half }},
	}
	for _, p := range patterns {
		for _, j := range []int{0, n} {
			for _, kk := range []int{0, n} {
				if !same(func(k int) int {
					index, fromK := p.lane(k)
					if fromK {
						return kk + index
					}
					return j + index
				}) {
					continue
				}
				vj, vk := reg(j, n), reg(kk, n)
				e.emit(p.base+size<<15|vk<<10|vj<<5|a, "%s.%s vr%d, vr%d, vr%d", p.name, suffix, a, vj, vk)
				write(a)
				return true
			}
		}
	}
	// vshuf4i permutes each group of four lanes of one input.
	if size < 3 {
		for _, source := range []int{0, n} {
			var selector uint32
			ok := true
			for k, x := range indices {
				lane := x - source
				if lane < 0 || lane >= n || lane&^3 != k&^3 {
					ok = false
					break
				}
				if k < 4 {
					selector |= uint32(lane&3) << (2 * k)
				} else if uint32(lane&3) != selector>>(2*(k&3))&3 {
					ok = false
					break
				}
			}
			if !ok {
				continue
			}
			r := reg(source, n)
			e.emit(0x73900000|size<<18|selector<<10|r<<5|a, "vshuf4i.%s vr%d, vr%d, %d", suffix, a, r, selector)
			write(a)
			return true
		}
	}
	return false
}

// extendPlan widens half Imm of b's lanes to Bits-wide lanes of a:
// vsllwil (shift 0) for the low half, vexth for the high half.
func (e *ByteAssembler) extendPlan(i machine.Instruction, a, b uint32, write func(uint32)) error {
	if i.Imm > 1 {
		return machine.ErrUnsupportedOperation
	}
	var size uint32
	switch i.Bits {
	case 16:
		size = 0
	case 32:
		size = 1
	case 64:
		size = 2
	default:
		return machine.ErrUnsupportedOperation
	}
	unsigned := i.Arithmetic == op.OpVExtendUnsigned
	wide, narrow := lsxSuffixes[size+1], lsxSuffixes[size]
	if unsigned {
		wide, narrow = wide+"u", narrow+"u"
	}
	if i.Imm == 0 {
		word := uint32(0x73080000) | 0x2000<<size
		if unsigned {
			word |= 0x40000
		}
		e.emit(word|b<<5|a, "vsllwil.%s.%s vr%d, vr%d, 0", wide, narrow, a, b)
	} else {
		word := uint32(0x729ee000) | size<<10
		if unsigned {
			word |= 0x1000
		}
		e.emit(word|b<<5|a, "vexth.%s.%s vr%d, vr%d", wide, narrow, a, b)
	}
	write(a)
	return nil
}
