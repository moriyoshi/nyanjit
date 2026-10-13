package builder

import (
	"testing"

	"github.com/moriyoshi/nyanjit/ir"
)

// The segment benchmarks compare the Builder's own cost in segment mode,
// with an emit that does nothing, against recording mode with Finish. The
// ns/instr metric divides by the instructions each function has.

func benchAddChain(n int) generator {
	return func(b *Builder) error {
		var c e
		x := c.v(b.Param())
		y := c.v(b.Param())
		for i := 0; i < n; i++ {
			x = c.v(b.Add(x, y))
		}
		c.do(b.Return(x))
		return c.err
	}
}

// benchMixedChain repeats x = x*3; x ^= x>>5; x += i.
func benchMixedChain(n int) generator {
	return func(b *Builder) error {
		var c e
		x := c.v(b.Param())
		three := c.v(b.Const(3))
		for i := 0; c.err == nil && i < n; i++ {
			x = c.v(b.Mul(x, three))
			x = c.v(b.Xor(x, c.v(b.ShiftRight(x, 5))))
			x = c.v(b.Add(x, c.v(b.Const(uint64(i)))))
		}
		c.do(b.Return(x))
		return c.err
	}
}

// benchVarLoop is a counted loop whose body updates two Vars n times.
func benchVarLoop(n int) generator {
	return func(b *Builder) error {
		var c e
		count := c.v(b.Param())
		i, acc := c.x(b.Var(c.v(b.Const(0)))), c.x(b.Var(c.v(b.Const(1))))
		loop, done := b.NewLabel(), b.NewLabel()
		c.do(b.Bind(loop))
		c.do(b.Branch(c.p(b.Compare(c.v(b.Get(i)), ir.BranchAboveEqual, count)), done))
		for k := 0; k < n; k++ {
			c.do(b.Set(acc, c.v(b.Add(c.v(b.Mul(c.v(b.Get(acc)), c.v(b.Get(i)))), c.v(b.Const(uint64(k)))))))
		}
		c.do(b.Set(i, c.v(b.Add(c.v(b.Get(i)), c.v(b.Const(1))))))
		c.do(b.Jump(loop))
		c.do(b.Bind(done))
		c.do(b.Return(c.v(b.Get(acc))))
		return c.err
	}
}

// benchScopes nests n scopes, each computing from an outer value and
// leaving with an export.
func benchScopes(n int) generator {
	return func(b *Builder) error {
		var c e
		x := c.v(b.Param())
		for i := 0; c.err == nil && i < n; i++ {
			c.do(b.Enter())
			y := c.v(b.Add(x, c.v(b.Const(uint64(i)))))
			z := c.v(b.Xor(y, x))
			c.do(b.Leave(z))
			x = z
		}
		c.do(b.Return(x))
		return c.err
	}
}

// benchDeferredChain composes n deferred operations and materializes them
// once.
func benchDeferredChain(n int) generator {
	return func(b *Builder) error {
		var c e
		x := c.v(b.Param())
		y := c.v(b.Param())
		v := x
		for i := 0; i < n; i++ {
			switch i % 3 {
			case 0:
				v = v.Add(y)
			case 1:
				v = v.Xor(x.ShiftRight(3))
			default:
				v = v.MulConst(5)
			}
		}
		c.do(b.Return(c.v(b.materialize(v))))
		return c.err
	}
}

var segmentBenchmarks = []struct {
	name string
	gen  generator
}{
	{"add256", benchAddChain(256)},
	{"chain4000", benchMixedChain(1000)},
	{"varloop", benchVarLoop(64)},
	{"diamonds32", segmentDiamonds(32)},
	{"scopes", benchScopes(64)},
	{"deferred", benchDeferredChain(300)},
}

func instructionCount(b *testing.B, gen generator) int {
	r := runRecording(gen)
	if r.callErr != nil || r.endErr != nil {
		b.Fatal(r.callErr, r.endErr)
	}
	return len(r.program.Instructions())
}

func BenchmarkSegmentBuilder(b *testing.B) {
	for _, bench := range segmentBenchmarks {
		n := instructionCount(b, bench.gen)
		b.Run(bench.name+"/segment", func(b *testing.B) {
			b.ReportAllocs()
			emit := func(*ir.Segment) error { return nil }
			for b.Loop() {
				builder := NewSegmentBuilder(emit)
				if err := bench.gen(builder); err != nil {
					b.Fatal(err)
				}
				if err := builder.End(); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(n), "ns/instr")
		})
		b.Run(bench.name+"/finish", func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				builder := NewBuilder()
				if err := bench.gen(builder); err != nil {
					b.Fatal(err)
				}
				if _, err := builder.Finish(); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(n), "ns/instr")
		})
	}
}
