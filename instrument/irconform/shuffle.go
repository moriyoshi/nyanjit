package irconform

import (
	"math/rand/v2"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/ir"
)

// shuffleSet is a named list of shuffle indices, one per lane.
type shuffleSet struct {
	name    string
	indices []byte
}

// shuffleIndexSets returns named index sets of a shuffle of n lanes: the
// identity of each input, reversal, interleaving, even/odd and transposition
// patterns, rotations of the concatenation, chunk-local patterns and random
// sets over one or both inputs.
func shuffleIndexSets(t ir.Type) []shuffleSet {
	n := int(t.Lanes)
	var sets []shuffleSet
	add := func(name string, f func(i int) int) {
		indices := make([]byte, n)
		for i := range indices {
			indices[i] = byte(f(i))
		}
		sets = append(sets, shuffleSet{name, indices})
	}
	add("identity", func(i int) int { return i })
	add("identity-y", func(i int) int { return n + i })
	add("reverse", func(i int) int { return n - 1 - i })
	add("reverse-y", func(i int) int { return 2*n - 1 - i })
	add("interleave-low", func(i int) int { return i/2 + i%2*n })
	add("interleave-high", func(i int) int { return n/2 + i/2 + i%2*n })
	add("interleave-low-yx", func(i int) int { return i/2 + (1-i%2)*n })
	add("interleave-self", func(i int) int { return i / 2 })
	add("even", func(i int) int { return 2 * i })
	add("odd", func(i int) int { return 2*i + 1 })
	add("transpose-even", func(i int) int { return i&^1 + i%2*n })
	add("transpose-odd", func(i int) int { return i | 1 + i%2*n })
	add("rotate-1", func(i int) int { return (i + 1) % (2 * n) })
	add("rotate-half", func(i int) int { return (i + n/2) % (2 * n) })
	add("rotate-yx", func(i int) int { return (i + n + 1) % (2 * n) })
	add("rotate-self", func(i int) int { return (i + 1) % n })
	add("swap-pairs", func(i int) int { return i ^ 1 })
	add("halves-yx", func(i int) int { return (i + n + n/2) % (2 * n) })
	add("duplicate-low", func(i int) int { return i % (n / 2) })
	// Patterns that stay within each 128-bit chunk (lowered per chunk when
	// the vector is wider).
	chunk := 128 / int(t.LaneBits)
	if chunk < n {
		add("chunk-reverse", func(i int) int { return i/chunk*chunk + chunk - 1 - i%chunk })
		add("chunk-interleave", func(i int) int { return i/chunk*chunk + i%chunk/2 + i%2*n })
	}
	random := rand.New(rand.NewPCG(uint64(n), uint64(t.LaneBits)))
	for k := range 3 {
		add("random-"+itoa(int64(k)), func(int) int { return random.IntN(2 * n) })
	}
	add("random-x", func(int) int { return random.IntN(n) })
	add("random-y", func(int) int { return n + random.IntN(n) })
	return sets
}

// shuffleCases cover OpVShuffle with every lane width, broadcasts of
// several lanes, and both widening extends of every widenable shape,
// including an input of twice the result's lanes (the half form).
func shuffleCases() []Case {
	var cases []Case
	none := [][]uint64{{}}
	add := func(group string, t ir.Type, body func(next, x, y int) ([]ir.Instruction, int, int), detail ...any) {
		cases = append(cases, Case{Name: name("vector", append([]any{group, t.Lanes, t.LaneBits}, detail...)...), Buffer: vectorSize, Inputs: none, Build: vectorProgram(t, nil, body)})
	}
	for _, t := range append([]ir.Type{ir.Vec(4, 8), ir.Vec(8, 8), ir.Vec(4, 16), ir.Vec(2, 32), ir.Vec(32, 8), ir.Vec(16, 16)}, vectorTypes...) {
		for _, set := range shuffleIndexSets(t) {
			add("shuffle", t, func(next, x, y int) ([]ir.Instruction, int, int) {
				return []ir.Instruction{{Op: ir.OpVShuffle, Type: t, Bits: t.LaneBits, A: x, B: y, Data: set.indices}}, next, -1
			}, set.name)
		}
		for k, lane := range []uint16{0, 1, t.Lanes / 2, t.Lanes - 1} {
			if k > 1 && lane <= 1 || k == 3 && lane == t.Lanes/2 {
				continue
			}
			data := make([]byte, t.Lanes)
			for i := range data {
				data[i] = byte(lane)
			}
			add("broadcast", t, func(next, x, y int) ([]ir.Instruction, int, int) {
				return []ir.Instruction{{Op: ir.OpVShuffle, Type: t, Bits: t.LaneBits, A: y, B: y, Data: data}}, next, -1
			}, lane)
		}
	}
	for _, t := range []ir.Type{ir.Vec(2, 8), ir.Vec(8, 8), ir.Vec(16, 8), ir.Vec(32, 8), ir.Vec(4, 16), ir.Vec(8, 16), ir.Vec(16, 16), ir.Vec(2, 32), ir.Vec(4, 32), ir.Vec(8, 32)} {
		wide := ir.Vec(t.Lanes, 2*t.LaneBits)
		for _, op := range []ir.Opcode{ir.OpVExtendUnsigned, ir.OpVExtendSigned} {
			group := "widen-unsigned"
			if op == ir.OpVExtendSigned {
				group = "widen-signed"
			}
			add(group, t, func(next, x, y int) ([]ir.Instruction, int, int) {
				return []ir.Instruction{{Op: op, Type: wide, Bits: wide.LaneBits, A: y}}, next, -1
			})
			if t.Lanes%2 != 0 || t.Lanes < 4 {
				continue
			}
			half := ir.Vec(t.Lanes/2, 2*t.LaneBits)
			for imm := range uint64(2) {
				add(group, t, func(next, x, y int) ([]ir.Instruction, int, int) {
					return []ir.Instruction{{Op: op, Type: half, Bits: half.LaneBits, A: y, Imm: imm}}, next, -1
				}, "half", imm)
			}
		}
	}
	return cases
}

// shuffleVarCases carry a vector Var around a loop that shuffles,
// broadcasts and widens it, so programs with Vars (scalarized by the
// optimized tier, and by the baseline tier's Var lowering) are covered.
func shuffleVarCases() []Case {
	var cases []Case
	inputs := [][]uint64{{0}, {1}, {5}}
	for _, t := range vectorTypes {
		if t.LaneBits == 64 {
			continue
		}
		suffix := "-" + itoa(int64(t.Lanes)) + "x" + itoa(t.LaneBits)
		cases = append(cases, Case{Name: "control/vector-var-shuffle" + suffix, Buffer: vectorSize, Inputs: inputs, Build: built(func(b *nyanjit.Builder) {
			buffer, n := b.ParamNative(), b.Param()
			y := b.VLoad(t, b.Add(buffer, b.ConstNative(32)))
			v := b.Var(b.VLoad(t, buffer))
			i := b.Var(b.Const(0))
			rotate := make([]uint16, t.Lanes)
			for k := range rotate {
				rotate[k] = uint16(k+3) % (2 * t.Lanes)
			}
			loop, done := b.NewLabel(), b.NewLabel()
			b.Bind(loop)
			b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, n), done)
			s := b.ShuffleLanes(b.Get(v), y, rotate)
			b.Set(v, b.Add(s, b.BroadcastLane(b.Get(v), t.Lanes-1)))
			b.Set(i, b.Add(b.Get(i), b.Const(1)))
			b.Jump(loop)
			b.Bind(done)
			b.VStore(b.Add(buffer, b.ConstNative(64)), b.WidenLanesSigned(b.Get(v)))
			b.Return(b.CastUnsigned(b.ExtractLane(b.WidenLanes(b.Get(v)), 0), 64))
		})})
	}
	return cases
}
