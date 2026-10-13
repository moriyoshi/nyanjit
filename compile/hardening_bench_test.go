package compile_test

import (
	"errors"
	"fmt"
	"runtime"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func hardeningBenchmarkProgram() *ir.SealedProgram {
	b := n.NewBuilder()
	x := b.Param()
	for k := uint64(0); k < 16; k++ {
		x = b.Add(b.Xor(x, b.Const(0x3141592653589793+k*0x101010101010101)), b.Const(0x2718281828459045+k))
	}
	b.Return(x)
	p, err := b.Finish()
	if err != nil {
		panic(err)
	}
	return p
}

func BenchmarkHardenCompile(b *testing.B) {
	p := hardeningBenchmarkProgram()
	for _, harden := range []bool{false, true} {
		b.Run(fmt.Sprint(harden), func(b *testing.B) {
			fn := compile.Func{ABI: abi.C, Harden: harden}
			unit, err := lowerUnit(p, compile.TargetSpec{}, fn)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for k := 0; k < b.N; k++ {
				if _, err := lowerUnit(p, compile.TargetSpec{}, fn); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportMetric(float64(unit.Report().CodeBytes()), "code-B")
		})
	}
}

func BenchmarkHardenCall(b *testing.B) {
	p := hardeningBenchmarkProgram()
	for _, harden := range []bool{false, true} {
		b.Run(fmt.Sprint(harden), func(b *testing.B) {
			template, err := lowerTemplate(p, compile.TargetSpec{}, compile.Func{ABI: abi.C, Harden: harden})
			if err != nil {
				b.Fatal(err)
			}
			c, err := template.Bind(nil)
			if err != nil {
				b.Fatal(err)
			}
			defer c.Close()
			args := execution.Words(uint64(123))
			b.ReportAllocs()
			b.ResetTimer()
			for k := 0; k < b.N; k++ {
				if _, _, err := c.CallPair(args...); err != nil {
					b.Fatal(err)
				}
			}
			runtime.KeepAlive(args)
		})
	}
}

func BenchmarkHardenBind(b *testing.B) {
	p := hardeningBenchmarkProgram()
	for _, harden := range []bool{false, true} {
		b.Run(fmt.Sprint(harden), func(b *testing.B) {
			template, err := lowerTemplate(p, compile.TargetSpec{}, compile.Func{ABI: abi.C, Harden: harden})
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for k := 0; k < b.N; k++ {
				c, err := template.Bind(nil)
				if err != nil {
					b.Fatal(err)
				}
				if err = c.Close(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkHardenIndex(b *testing.B) {
	builder := n.NewBuilder()
	base, index, count := builder.ParamNative(), builder.ParamNative(), builder.ParamNative()
	bad := builder.NewLabel()
	builder.Return(builder.Load64(builder.IndexedAddress(base, index, count, 8, bad)))
	builder.Bind(bad)
	builder.Return(builder.Const(0))
	p, err := builder.Finish()
	if err != nil {
		b.Fatal(err)
	}
	for _, harden := range []bool{false, true} {
		b.Run(fmt.Sprint(harden), func(b *testing.B) {
			c, err := n.Compile(p, compile.TargetSpec{}, compile.Func{ABI: abi.C, Harden: harden, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 640}})
			if errors.Is(err, compile.ErrUnsupported) {
				b.Skip(err)
			}
			if err != nil {
				b.Fatal(err)
			}
			defer c.Close()
			data := new([2]uint64)
			data[1] = 42
			args := execution.Words(data, uintptr(1), uintptr(2))
			b.ReportAllocs()
			b.ResetTimer()
			for k := 0; k < b.N; k++ {
				if _, _, err := c.CallPair(args...); err != nil {
					b.Fatal(err)
				}
			}
			runtime.KeepAlive(args)
		})
	}
}
