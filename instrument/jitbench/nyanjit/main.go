// Command nyanjit measures NyanJIT's tiers end to end on Var-based kernels
// matched with ../sljit/bench.c, ../cranelift and ../tpde: Builder through
// Unit (compile), Template, bind, run. See ../README.md.
package main

import (
	"flag"
	"fmt"
	"math"
	"os"
	"runtime/pprof"
	"sort"
	"time"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

type kernel struct {
	name  string
	ops   int
	build func(*nyanjit.Builder)
	arg   uint64
}

func tiny(b *nyanjit.Builder) { b.Return(b.Add(b.Param(), b.Const(4))) }

func chain(n int) func(*nyanjit.Builder) {
	return func(b *nyanjit.Builder) {
		x := b.Param()
		for i := 0; i < n; i++ {
			x = b.Mul(x, b.Const(3))
			x = b.Xor(x, b.ShiftRight(x, 5))
			x = b.Add(x, b.Const(uint64(i)))
		}
		b.Return(x)
	}
}

func loop(b *nyanjit.Builder) {
	n := b.Param()
	i, acc := b.Var(b.Const(0)), b.Var(b.Const(0))
	l, done := b.NewLabel(), b.NewLabel()
	b.Bind(l)
	b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, n), done)
	x := b.Get(i)
	b.Set(acc, b.Add(b.Get(acc), b.Xor(b.Mul(x, x), b.ShiftRight(x, 3))))
	b.Set(i, b.Add(x, b.Const(1)))
	b.Jump(l)
	b.Bind(done)
	b.Return(b.Get(acc))
}

func pressure(b *nyanjit.Builder) {
	const vars = 16
	n := b.Param()
	i := b.Var(b.Const(0))
	a := make([]nyanjit.Var, vars)
	for k := range a {
		a[k] = b.Var(b.Const(uint64(k + 1)))
	}
	l, done := b.NewLabel(), b.NewLabel()
	b.Bind(l)
	b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, n), done)
	for k := range a {
		b.Set(a[k], b.Add(b.Get(a[k]), b.Xor(b.Get(a[(k+1)%vars]), b.Get(i))))
	}
	b.Set(i, b.Add(b.Get(i), b.Const(1)))
	b.Jump(l)
	b.Bind(done)
	x := b.Get(a[0])
	for _, v := range a[1:] {
		x = b.Xor(x, b.Get(v))
	}
	b.Return(x)
}

func collatz(b *nyanjit.Builder) {
	n := b.Param()
	x, y, total := b.Var(b.Const(1)), b.Var(b.Const(0)), b.Var(b.Const(0))
	outer, inner, odd, next, done := b.NewLabel(), b.NewLabel(), b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.Bind(outer)
	b.Branch(b.Compare(b.Get(x), ir.BranchAbove, n), done)
	b.Set(y, b.Get(x))
	b.Bind(inner)
	b.Branch(b.CompareImmediate(b.Get(y), ir.BranchEqual, 1), next)
	b.Set(total, b.Add(b.Get(total), b.Const(1)))
	b.Branch(b.IsZero(b.And(b.Get(y), b.Const(1))).Not(), odd)
	b.Set(y, b.ShiftRight(b.Get(y), 1))
	b.Jump(inner)
	b.Bind(odd)
	b.Set(y, b.Add(b.Mul(b.Get(y), b.Const(3)), b.Const(1)))
	b.Jump(inner)
	b.Bind(next)
	b.Set(x, b.Add(b.Get(x), b.Const(1)))
	b.Jump(outer)
	b.Bind(done)
	b.Return(b.Get(total))
}

var coefficients = []float64{0.1, -0.2, 0.3, -0.4, 0.5, -0.6, 0.7, -0.8, 0.9}

func fp(b *nyanjit.Builder) {
	n := b.Param()
	i, count := b.Var(b.Const(0)), b.Var(b.Const(0))
	l, miss, done := b.NewLabel(), b.NewLabel(), b.NewLabel()
	b.Bind(l)
	b.Branch(b.Compare(b.Get(i), ir.BranchAboveEqual, n), done)
	x := b.FMul(b.FFromSigned(b.Get(i), 64), b.Float64(0.001))
	y := b.Float64(coefficients[0])
	for _, c := range coefficients[1:] {
		y = b.FAdd(b.FMul(y, x), b.Float64(c))
	}
	b.Branch(b.FComparePredicate(y, ir.FloatUnorderedLessEqual, b.Float64(0.5)), miss)
	b.Set(count, b.Add(b.Get(count), b.Const(1)))
	b.Bind(miss)
	b.Set(i, b.Add(b.Get(i), b.Const(1)))
	b.Jump(l)
	b.Bind(done)
	b.Return(b.Get(count))
}

var kernels = []kernel{
	{"tiny", 1, tiny, 3},
	{"chain100", 400, chain(100), 12345},
	{"chain1000", 4000, chain(1000), 12345},
	{"loop", 6, loop, 10_000_000},
	{"pressure", 2*16 + 3, pressure, 1_000_000},
	{"collatz", 12, collatz, 100_000},
	{"fp", 2*len(coefficients) + 6, fp, 1_000_000},
}

func median(xs []time.Duration) time.Duration {
	sort.Slice(xs, func(i, j int) bool { return xs[i] < xs[j] })
	return xs[len(xs)/2]
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func main() {
	only := flag.String("k", "", "kernel")
	mode := flag.String("mode", "baseline", "unbound | optimized | baseline")
	budget := flag.Duration("t", 300*time.Millisecond, "time per round")
	rounds := flag.Int("r", 7, "rounds")
	prof := flag.String("cpuprofile", "", "profile")
	flag.Parse()
	if *prof != "" {
		out, err := os.Create(*prof)
		check(err)
		check(pprof.StartCPUProfile(out))
		defer pprof.StopCPUProfile()
	}
	f := compile.Func{ABI: abi.C}
	spec := compile.TargetSpec{}
	if *mode == "baseline" {
		spec.Tier = compile.TierBaseline
	}
	target, err := compile.NewTarget(spec)
	check(err)
	arena, err := execution.NewArena(64 << 20)
	check(err)
	compileOne := func(k kernel) *compile.Unit {
		if *mode == "unbound" {
			b := nyanjit.NewBuilder()
			k.build(b)
			p, err := b.Finish()
			check(err)
			u, err := compile.Lower(target, p, f)
			check(err)
			return u
		}
		b := nyanjit.NewBuilderFor(target, f)
		k.build(b)
		u, err := b.Lower()
		check(err)
		return u
	}
	fmt.Printf("%-10s %-9s %11s %9s %10s %10s %12s %s\n", "kernel", "mode", "compile", "ns/op", "template", "bind", "run", "result")
	for _, k := range kernels {
		if *only != "" && k.name != *only {
			continue
		}
		var cs, ts, bs []time.Duration
		for r := 0; r < *rounds; r++ {
			var c, tt, bb time.Duration
			n := 0
			for start := time.Now(); time.Since(start) < *budget || n == 0; n++ {
				t0 := time.Now()
				u := compileOne(k)
				t1 := time.Now()
				tmpl, err := u.Template()
				check(err)
				t2 := time.Now()
				code, err := tmpl.BindIn(arena, nil)
				check(err)
				t3 := time.Now()
				check(code.Close())
				c += t1.Sub(t0)
				tt += t2.Sub(t1)
				bb += t3.Sub(t2)
			}
			d := time.Duration(n)
			cs, ts, bs = append(cs, c/d), append(ts, tt/d), append(bs, bb/d)
		}
		u := compileOne(k)
		tmpl, err := u.Template()
		check(err)
		code, err := tmpl.Bind(nil)
		check(err)
		best := time.Duration(math.MaxInt64)
		var result uintptr
		for r := 0; r < 5; r++ {
			t0 := time.Now()
			result, err = code.Call(uintptr(k.arg))
			check(err)
			best = min(best, time.Since(t0))
		}
		check(code.Close())
		c := median(cs)
		fmt.Printf("%-10s %-9s %11v %9.1f %10v %10v %12v %d\n", k.name, *mode, c, float64(c.Nanoseconds())/float64(k.ops), median(ts), median(bs), best, result)
	}
}
