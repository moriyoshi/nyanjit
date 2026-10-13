package main

import (
	"math/rand"
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
)

// benchPrograms are the example programs plus a large structured one.
func benchPrograms() []*Program {
	b := &structuredProgram{r: rand.New(rand.NewSource(3)), locals: 10}
	b.statements(400, 0, 7)
	b.expression(2)
	b.emit(Ret, 0)
	return append(Programs, &Program{Name: "large", Params: 2, Locals: 10, Code: b.code})
}

// BenchmarkCompile measures each path's compile time for the host, per
// bytecode instruction: cold with a new Compiler for every program, warm
// with one Compiler reused, as a long-lived JIT would.
func BenchmarkCompile(b *testing.B) {
	t, err := newTarget(runtime.GOARCH)
	if err != nil {
		b.Skip(err)
	}
	for _, c := range compilers {
		for _, p := range benchPrograms() {
			for _, warm := range []bool{false, true} {
				name := c.name + "/" + p.Name + "/cold"
				if warm {
					name = c.name + "/" + p.Name + "/warm"
				}
				b.Run(name, func(b *testing.B) {
					b.ReportAllocs()
					cc := &Compiler{Alloc: c.alloc()}
					for b.Loop() {
						if !warm {
							cc = &Compiler{Alloc: c.alloc()}
						}
						if _, err := cc.Compile(p, t, false); err != nil {
							b.Fatal(err)
						}
					}
					b.ReportMetric(float64(b.Elapsed().Nanoseconds())/float64(b.N)/float64(len(p.Code)), "ns/insn")
				})
			}
		}
	}
}

// BenchmarkRun measures the generated loops on the host.
func BenchmarkRun(b *testing.B) {
	if runtime.GOOS != "linux" {
		b.Skip("no native execution")
	}
	t, err := newTarget(runtime.GOARCH)
	if err != nil {
		b.Skip(err)
	}
	for _, c := range compilers {
		for _, q := range Programs {
			if q.Name != "squares" && q.Name != "nested" && q.Name != "locals" {
				continue
			}
			{
				b.Run(c.name+"/"+q.Name, func(b *testing.B) {
					f, err := (&Compiler{Alloc: c.alloc()}).Compile(q, t, false)
					if err != nil {
						b.Fatal(err)
					}
					code, err := execution.NewCode(f.Code, execution.CodeOptions{
						ABI: abi.C, Foreign: true, Arity: q.Params, StackBytes: f.FrameBytes,
					}, nil)
					if err != nil {
						b.Fatal(err)
					}
					defer code.Close()
					b.ReportMetric(float64(len(f.Code)), "code-bytes")
					for b.Loop() {
						if _, err := code.Call(10000); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		}
	}
}
