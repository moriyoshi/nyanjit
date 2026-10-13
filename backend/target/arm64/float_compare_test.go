package arm64

import (
	"math"
	"strings"
	"testing"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/op"
)

// Expected words are GNU aarch64-linux-gnu-as 2.42 (Ubuntu binutils) output, read back with
// aarch64-linux-gnu-objdump -d, for "fcmp Sa, Sb" (Da, Db for 64 bits)
// followed by "cset Wd, cond", or by "cset Wd, mi; csinc Wd, Wd, wzr, le"
// (OrderedNotEqual) and "cset Wd, eq; csinc Wd, Wd, wzr, vc"
// (UnorderedEqual). Conditions in op.FloatCondition order are eq, ne, mi,
// pl, ls, hi, gt, le, ge, lt, vc, vs.
func TestFloatCompareEncoding(t *testing.T) {
	for _, test := range []struct {
		c       op.FloatCondition
		w       uint8
		d, a, b uint8
		words   []uint32
	}{
		{0, 32, 0, 1, 2, []uint32{0x1e222020, 0x1a9f17e0}},
		{1, 32, 0, 1, 2, []uint32{0x1e222020, 0x1a9f07e0}},
		{2, 32, 0, 1, 2, []uint32{0x1e222020, 0x1a9f57e0}},
		{3, 32, 0, 1, 2, []uint32{0x1e222020, 0x1a9f47e0}},
		{4, 32, 0, 1, 2, []uint32{0x1e222020, 0x1a9f87e0}},
		{5, 32, 0, 1, 2, []uint32{0x1e222020, 0x1a9f97e0}},
		{6, 32, 0, 1, 2, []uint32{0x1e222020, 0x1a9fd7e0}},
		{7, 32, 0, 1, 2, []uint32{0x1e222020, 0x1a9fc7e0}},
		{8, 32, 0, 1, 2, []uint32{0x1e222020, 0x1a9fb7e0}},
		{9, 32, 0, 1, 2, []uint32{0x1e222020, 0x1a9fa7e0}},
		{10, 32, 0, 1, 2, []uint32{0x1e222020, 0x1a9f67e0}},
		{11, 32, 0, 1, 2, []uint32{0x1e222020, 0x1a9f77e0}},
		{12, 32, 0, 1, 2, []uint32{0x1e222020, 0x1a9f57e0, 0x1a9fd400}},
		{13, 32, 0, 1, 2, []uint32{0x1e222020, 0x1a9f17e0, 0x1a9f7400}},
		{0, 64, 0, 1, 2, []uint32{0x1e622020, 0x1a9f17e0}},
		{1, 64, 0, 1, 2, []uint32{0x1e622020, 0x1a9f07e0}},
		{2, 64, 0, 1, 2, []uint32{0x1e622020, 0x1a9f57e0}},
		{3, 64, 0, 1, 2, []uint32{0x1e622020, 0x1a9f47e0}},
		{4, 64, 0, 1, 2, []uint32{0x1e622020, 0x1a9f87e0}},
		{5, 64, 0, 1, 2, []uint32{0x1e622020, 0x1a9f97e0}},
		{6, 64, 0, 1, 2, []uint32{0x1e622020, 0x1a9fd7e0}},
		{7, 64, 0, 1, 2, []uint32{0x1e622020, 0x1a9fc7e0}},
		{8, 64, 0, 1, 2, []uint32{0x1e622020, 0x1a9fb7e0}},
		{9, 64, 0, 1, 2, []uint32{0x1e622020, 0x1a9fa7e0}},
		{10, 64, 0, 1, 2, []uint32{0x1e622020, 0x1a9f67e0}},
		{11, 64, 0, 1, 2, []uint32{0x1e622020, 0x1a9f77e0}},
		{12, 64, 0, 1, 2, []uint32{0x1e622020, 0x1a9f57e0, 0x1a9fd400}},
		{13, 64, 0, 1, 2, []uint32{0x1e622020, 0x1a9f17e0, 0x1a9f7400}},
		{0, 32, 17, 31, 9, []uint32{0x1e2923e0, 0x1a9f17f1}},
		{1, 32, 17, 31, 9, []uint32{0x1e2923e0, 0x1a9f07f1}},
		{2, 32, 17, 31, 9, []uint32{0x1e2923e0, 0x1a9f57f1}},
		{3, 32, 17, 31, 9, []uint32{0x1e2923e0, 0x1a9f47f1}},
		{4, 32, 17, 31, 9, []uint32{0x1e2923e0, 0x1a9f87f1}},
		{5, 32, 17, 31, 9, []uint32{0x1e2923e0, 0x1a9f97f1}},
		{6, 32, 17, 31, 9, []uint32{0x1e2923e0, 0x1a9fd7f1}},
		{7, 32, 17, 31, 9, []uint32{0x1e2923e0, 0x1a9fc7f1}},
		{8, 32, 17, 31, 9, []uint32{0x1e2923e0, 0x1a9fb7f1}},
		{9, 32, 17, 31, 9, []uint32{0x1e2923e0, 0x1a9fa7f1}},
		{10, 32, 17, 31, 9, []uint32{0x1e2923e0, 0x1a9f67f1}},
		{11, 32, 17, 31, 9, []uint32{0x1e2923e0, 0x1a9f77f1}},
		{12, 32, 17, 31, 9, []uint32{0x1e2923e0, 0x1a9f57f1, 0x1a9fd631}},
		{13, 32, 17, 31, 9, []uint32{0x1e2923e0, 0x1a9f17f1, 0x1a9f7631}},
		{0, 64, 17, 31, 9, []uint32{0x1e6923e0, 0x1a9f17f1}},
		{1, 64, 17, 31, 9, []uint32{0x1e6923e0, 0x1a9f07f1}},
		{2, 64, 17, 31, 9, []uint32{0x1e6923e0, 0x1a9f57f1}},
		{3, 64, 17, 31, 9, []uint32{0x1e6923e0, 0x1a9f47f1}},
		{4, 64, 17, 31, 9, []uint32{0x1e6923e0, 0x1a9f87f1}},
		{5, 64, 17, 31, 9, []uint32{0x1e6923e0, 0x1a9f97f1}},
		{6, 64, 17, 31, 9, []uint32{0x1e6923e0, 0x1a9fd7f1}},
		{7, 64, 17, 31, 9, []uint32{0x1e6923e0, 0x1a9fc7f1}},
		{8, 64, 17, 31, 9, []uint32{0x1e6923e0, 0x1a9fb7f1}},
		{9, 64, 17, 31, 9, []uint32{0x1e6923e0, 0x1a9fa7f1}},
		{10, 64, 17, 31, 9, []uint32{0x1e6923e0, 0x1a9f67f1}},
		{11, 64, 17, 31, 9, []uint32{0x1e6923e0, 0x1a9f77f1}},
		{12, 64, 17, 31, 9, []uint32{0x1e6923e0, 0x1a9f57f1, 0x1a9fd631}},
		{13, 64, 17, 31, 9, []uint32{0x1e6923e0, 0x1a9f17f1, 0x1a9f7631}},
	} {
		a, b := backend.VectorRegister(test.a), backend.VectorRegister(test.b)
		e := NewByteAssembler(abi.StackPlatform, 0)
		e.FloatCompare(test.c, test.w, backend.Register(test.d), a, b)
		if e.Err() != nil || len(e.words) != len(test.words) {
			t.Fatalf("condition %d, %d bits: %v, %d words", test.c, test.w, e.Err(), len(e.words))
		}
		for i, word := range test.words {
			if e.words[i] != word {
				t.Errorf("condition %d, %d bits, W%d: word %d = %#08x, want %#08x", test.c, test.w, test.d, i, e.words[i], word)
			}
		}
		ins := machine.Instruction{Op: machine.OpFloatCompare, Bits: test.w, A: backend.Register(test.d), B: a, C: b, Imm: uint64(test.c)}
		if size := PhysicalSize(ins, abi.StackPlatform, 0); size != 4*len(test.words) {
			t.Errorf("condition %d, %d bits: size %d, want %d", test.c, test.w, size, 4*len(test.words))
		}
		text := NewTextAssembler(abi.StackPlatform, 0)
		text.FloatCompare(test.c, test.w, backend.Register(test.d), a, b)
		if text.Err() != nil || strings.Count(text.Text(), "\n") != len(test.words) || !strings.Contains(text.Text(), "fcmp") {
			t.Errorf("condition %d, %d bits: listing %q", test.c, test.w, text.Text())
		}
	}
	for _, bad := range []struct {
		c       op.FloatCondition
		w       uint8
		d, a, b backend.Register
	}{
		{op.FloatUnorderedEqual + 1, 64, 0, backend.VectorRegister(1), backend.VectorRegister(2)},
		{op.FloatEqual, 16, 0, backend.VectorRegister(1), backend.VectorRegister(2)},
		{op.FloatEqual, 64, 0, 1, backend.VectorRegister(2)},
		{op.FloatEqual, 64, backend.VectorRegister(0), backend.VectorRegister(1), backend.VectorRegister(2)},
	} {
		e := NewByteAssembler(abi.StackPlatform, 0)
		e.FloatCompare(bad.c, bad.w, bad.d, bad.a, bad.b)
		if e.Err() == nil {
			t.Errorf("accepted condition %d, %d bits, %d, %d, %d", bad.c, bad.w, bad.d, bad.a, bad.b)
		}
	}
}

// The emitted sequences give Go's IEEE 754 answer for less, equal, greater
// and unordered operands. The check models FCMP's NZCV and evaluates each
// CSINC by the ARM ARM ConditionHolds table.
func TestFloatCompareSemantics(t *testing.T) {
	holds := func(cond uint32, n, z, c, v bool) bool {
		var r bool
		switch cond >> 1 {
		case 0:
			r = z
		case 1:
			r = c
		case 2:
			r = n
		case 3:
			r = v
		case 4:
			r = c && !z
		case 5:
			r = n == v
		case 6:
			r = n == v && !z
		default:
			r = true
		}
		if cond&1 == 1 && cond != 15 {
			r = !r
		}
		return r
	}
	nan := math.NaN()
	pairs := [][2]float64{{1, 2}, {2, 2}, {3, 2}, {nan, 2}, {2, nan}, {nan, nan}}
	for c := op.FloatEqual; c <= op.FloatUnorderedEqual; c++ {
		for _, p := range pairs {
			x, y := p[0], p[1]
			var n, z, cf, v bool
			switch {
			case x != x || y != y:
				cf, v = true, true
			case x < y:
				n = true
			case x == y:
				z, cf = true, true
			default:
				cf = true
			}
			words := floatCompareWords(c, 64, 3, backend.VectorRegister(1), backend.VectorRegister(2))
			regs := [32]uint64{3: 0xdeadbeef}
			for _, word := range words[1:] {
				if word&0xffe00c00 != 0x1a800400 || word&31 != 3 {
					t.Fatalf("condition %d: unexpected word %#08x", c, word)
				}
				rm, rn, cond := word>>16&31, word>>5&31, word>>12&15
				read := func(r uint32) uint64 {
					if r == 31 {
						return 0
					}
					return regs[r]
				}
				if holds(cond, n, z, cf, v) {
					regs[3] = read(rn) & 0xffffffff
				} else {
					regs[3] = (read(rm) + 1) & 0xffffffff
				}
			}
			var want bool
			lt, eq, gt, uno := x < y, x == y, x > y, x != x || y != y
			switch c {
			case op.FloatEqual:
				want = eq
			case op.FloatNotEqual:
				want = !eq
			case op.FloatLess:
				want = lt
			case op.FloatUnorderedGreaterEqual:
				want = !lt
			case op.FloatLessEqual:
				want = lt || eq
			case op.FloatUnorderedGreater:
				want = !(lt || eq)
			case op.FloatGreater:
				want = gt
			case op.FloatUnorderedLessEqual:
				want = !gt
			case op.FloatGreaterEqual:
				want = gt || eq
			case op.FloatUnorderedLess:
				want = !(gt || eq)
			case op.FloatOrdered:
				want = !uno
			case op.FloatUnordered:
				want = uno
			case op.FloatOrderedNotEqual:
				want = lt || gt
			case op.FloatUnorderedEqual:
				want = !(lt || gt)
			}
			if got := regs[3]; got != map[bool]uint64{false: 0, true: 1}[want] {
				t.Errorf("condition %d on %v, %v: %d, want %v", c, x, y, got, want)
			}
		}
	}
}
