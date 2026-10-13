package i386

import (
	"github.com/moriyoshi/nyanjit/backend/op"
)

// The SSE2 planner for OpVShuffle and OpVExtend* is shared verbatim with
// the amd64 target; only the encoding of each step differs. A plan reads the
// sources B and C and writes A and the expansion temporary XMM0, comparing
// actual register numbers, so it is correct for every aliasing of A, B and
// C. Without SSSE3's PSHUFB, byte and halfword shuffles are encoded only
// for the patterns below; others are rejected and selection scalarizes them.

// sseStep is one register-to-register SSE2 instruction: prefix (0 for
// none), 0F opcode, ModRM reg (destination) and rm (source), an immediate
// byte when imm is not negative, and its mnemonic.
type sseStep struct {
	prefix, opcode byte
	dst, src       uint8
	imm            int16
	name           string
}

// ssePlan is a short fixed-capacity step list, so sizing does not allocate.
type ssePlan struct {
	steps [8]sseStep
	n     int
}

func (p *ssePlan) add(prefix, opcode byte, dst, src uint8, imm int, name string) {
	p.steps[p.n] = sseStep{prefix, opcode, dst, src, int16(imm), name}
	p.n++
}
func (p *ssePlan) move(dst, src uint8) {
	if dst != src {
		p.add(0x66, 0x6f, dst, src, -1, "movdqa")
	}
}

// binary writes op(x, y) of a destructive two-operand instruction to a.
func (p *ssePlan) binary(prefix, opcode byte, imm int, name string, a, x, y uint8) {
	switch {
	case a == x:
		p.add(prefix, opcode, a, y, imm, name)
	case a != y:
		p.move(a, x)
		p.add(prefix, opcode, a, y, imm, name)
	default:
		// a holds y: build the result in XMM0 first.
		p.move(0, x)
		p.add(prefix, opcode, 0, y, imm, name)
		p.move(a, 0)
	}
}

func shuffleSelector(e0, e1, e2, e3 int) int { return e0 | e1<<2 | e2<<4 | e3<<6 }

// planShuffle plans the 128-bit shuffle of Bits-wide lanes whose lane i is
// lane index i of B‖C. It reports false for an invalid index set or one
// SSE2 cannot encode.
func planShuffle(bits uint8, imm, disp uint64, a, b, c uint8) (ssePlan, bool) {
	var p ssePlan
	if bits != 8 && bits != 16 && bits != 32 && bits != 64 {
		return p, false
	}
	n := int(128 / bits)
	var buffer [16]int
	idx := buffer[:n]
	for i := range 16 {
		word, shift := imm, 8*i
		if i >= 8 {
			word, shift = disp, 8*(i-8)
		}
		x := int(word >> shift & 0xff)
		if i >= n {
			if x != 0 {
				return p, false
			}
			continue
		}
		if x >= 2*n {
			return p, false
		}
		if b == c {
			x %= n
		}
		idx[i] = x
	}
	return p, p.shuffle(idx, a, b, c)
}

func (p *ssePlan) shuffle(idx []int, a, b, c uint8) bool {
	n := len(idx)
	// A pattern that moves aligned pairs of lanes is a shuffle of lanes of
	// twice the width.
	if n > 2 {
		pairs := true
		for k := 0; k < n && pairs; k += 2 {
			pairs = idx[k]%2 == 0 && idx[k+1] == idx[k]+1
		}
		if pairs {
			var buffer [8]int
			wide := buffer[:n/2]
			for k := range wide {
				wide[k] = idx[2*k] / 2
			}
			return p.shuffle(wide, a, b, c)
		}
	}
	reg := func(x int) uint8 {
		if x < n {
			return b
		}
		return c
	}
	single := true
	for _, x := range idx {
		single = single && reg(x) == reg(idx[0])
	}
	var e [16]int
	for i, x := range idx {
		e[i] = x % n
	}
	identity := single
	for i := range n {
		identity = identity && e[i] == i
	}
	if identity {
		p.move(a, reg(idx[0]))
		return true
	}
	switch n {
	case 2:
		if single {
			p.add(0x66, 0x70, a, reg(idx[0]), shuffleSelector(2*e[0], 2*e[0]+1, 2*e[1], 2*e[1]+1), "pshufd")
		} else {
			p.binary(0x66, 0xc6, e[0]|e[1]<<1, "shufpd", a, reg(idx[0]), reg(idx[1]))
		}
		return true
	case 4:
		r0, r1, r2, r3 := reg(idx[0]), reg(idx[1]), reg(idx[2]), reg(idx[3])
		switch {
		case single:
			p.add(0x66, 0x70, a, r0, shuffleSelector(e[0], e[1], e[2], e[3]), "pshufd")
		case r0 == r1 && r2 == r3:
			p.binary(0, 0xc6, shuffleSelector(e[0], e[1], e[2], e[3]), "shufps", a, r0, r2)
		default:
			// XMM0 = [s2 s2 s3 s3] and A = [s0 s0 s1 s1] (or [s1 s1 s0 s0]
			// when A holds s1's source), then pick lanes 0 and 2 of each.
			if r2 == r3 {
				p.add(0x66, 0x70, 0, r2, shuffleSelector(e[2], e[2], e[3], e[3]), "pshufd")
			} else {
				p.move(0, r2)
				p.add(0, 0xc6, 0, r3, shuffleSelector(e[2], e[2], e[3], e[3]), "shufps")
			}
			final := shuffleSelector(0, 2, 0, 2)
			switch {
			case r0 == r1:
				p.add(0x66, 0x70, a, r0, shuffleSelector(e[0], e[0], e[1], e[1]), "pshufd")
			case a == r1:
				p.add(0, 0xc6, a, r0, shuffleSelector(e[1], e[1], e[0], e[0]), "shufps")
				final = shuffleSelector(2, 0, 0, 2)
			default:
				p.move(a, r0)
				p.add(0, 0xc6, a, r1, shuffleSelector(e[0], e[0], e[1], e[1]), "shufps")
			}
			p.add(0, 0xc6, a, 0, final, "shufps")
		}
		return true
	case 8:
		if single && p.halfwords(e[:n], a, reg(idx[0])) {
			return true
		}
		return p.interleave(idx, e[:n], 0x61, 0x69, "punpcklwd", "punpckhwd", a, reg)
	case 16:
		broadcast := single
		for _, x := range e[:n] {
			broadcast = broadcast && x == e[0]
		}
		if broadcast {
			// Double the byte into a halfword, then broadcast that.
			if e[0] < 8 {
				p.binary(0x66, 0x60, -1, "punpcklbw", a, reg(idx[0]), reg(idx[0]))
			} else {
				p.binary(0x66, 0x68, -1, "punpckhbw", a, reg(idx[0]), reg(idx[0]))
			}
			var words [8]int
			for k := range words {
				words[k] = e[0] % 8
			}
			return p.halfwords(words[:], a, a)
		}
		return p.interleave(idx, e[:n], 0x60, 0x68, "punpcklbw", "punpckhbw", a, reg)
	}
	return false
}

// halfwords plans a one-source halfword shuffle of x into a when each output
// quadword takes its four lanes from one source quadword: PSHUFD places the
// quadwords, then PSHUFLW and PSHUFHW order the lanes within them.
func (p *ssePlan) halfwords(e []int, a, x uint8) bool {
	q0, q1 := e[0]/4, e[4]/4
	for k := range 4 {
		if e[k]/4 != q0 || e[4+k]/4 != q1 {
			return false
		}
	}
	broadcast := true
	for _, lane := range e {
		broadcast = broadcast && lane == e[0]
	}
	if broadcast {
		// Repeat the lane within its quadword, then that doubleword.
		lane := shuffleSelector(e[0]%4, e[0]%4, e[0]%4, e[0]%4)
		if e[0] < 4 {
			p.add(0xf2, 0x70, a, x, lane, "pshuflw")
			p.add(0x66, 0x70, a, a, 0, "pshufd")
		} else {
			p.add(0xf3, 0x70, a, x, lane, "pshufhw")
			p.add(0x66, 0x70, a, a, shuffleSelector(2, 2, 2, 2), "pshufd")
		}
		return true
	}
	current := x
	if q0 != 0 || q1 != 1 {
		p.add(0x66, 0x70, a, current, shuffleSelector(2*q0, 2*q0+1, 2*q1, 2*q1+1), "pshufd")
		current = a
	}
	low := shuffleSelector(e[0]%4, e[1]%4, e[2]%4, e[3]%4)
	if low != shuffleSelector(0, 1, 2, 3) {
		p.add(0xf2, 0x70, a, current, low, "pshuflw")
		current = a
	}
	high := shuffleSelector(e[4]%4, e[5]%4, e[6]%4, e[7]%4)
	if high != shuffleSelector(0, 1, 2, 3) {
		p.add(0xf3, 0x70, a, current, high, "pshufhw")
		current = a
	}
	p.move(a, current)
	return true
}

// interleave plans a PUNPCKL or PUNPCKH pattern: output lane 2k is lane k
// (or n/2+k) of one source and lane 2k+1 the same lane of another.
func (p *ssePlan) interleave(idx, e []int, low, high byte, lowName, highName string, a uint8, reg func(int) uint8) bool {
	n := len(idx)
	x, y := reg(idx[0]), reg(idx[1])
	base := e[0]
	if base != 0 && base != n/2 {
		return false
	}
	for k := 0; k < n/2; k++ {
		if e[2*k] != base+k || e[2*k+1] != base+k || reg(idx[2*k]) != x || reg(idx[2*k+1]) != y {
			return false
		}
	}
	if base == 0 {
		p.binary(0x66, low, -1, lowName, a, x, y)
	} else {
		p.binary(0x66, high, -1, highName, a, x, y)
	}
	return true
}

// planExtend plans a widening extend of the low (half 0) or high (half 1)
// quadword of b into Bits-wide lanes of a. XMM0 holds zero or the sign mask.
func planExtend(operation op.Opcode, bits uint8, half uint64, a, b uint8) (ssePlan, bool) {
	var p ssePlan
	size := 0
	switch bits {
	case 16:
	case 32:
		size = 1
	case 64:
		size = 2
	default:
		return p, false
	}
	if half > 1 {
		return p, false
	}
	p.add(0x66, 0xef, 0, 0, -1, "pxor")
	if operation == op.OpVExtendSigned {
		p.add(0x66, 0x64+byte(size), 0, b, -1, [...]string{"pcmpgtb", "pcmpgtw", "pcmpgtd"}[size])
	}
	unpack, name := 0x60+byte(size), [...]string{"punpcklbw", "punpcklwd", "punpckldq"}[size]
	if half == 1 {
		unpack, name = 0x68+byte(size), [...]string{"punpckhbw", "punpckhwd", "punpckhdq"}[size]
	}
	p.move(a, b)
	p.add(0x66, unpack, a, 0, -1, name)
	return p, true
}

// appendSSE encodes a step: legacy prefix, 0F opcode, register ModRM and
// the optional immediate. 32-bit mode has only XMM0-XMM7.
func appendSSE(code []byte, s sseStep) []byte {
	if s.prefix != 0 {
		code = append(code, s.prefix)
	}
	code = append(code, 0x0f, s.opcode, 0xc0|s.dst<<3|s.src)
	if s.imm >= 0 {
		code = append(code, byte(s.imm))
	}
	return code
}
