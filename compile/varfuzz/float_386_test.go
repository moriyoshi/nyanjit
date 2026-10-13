//go:build linux && 386

package varfuzz

import (
	"math"
	"testing"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

// Test386FloatWorkspaceSweep complements the mixed AST sweep: its programs
// contain no paired integer shifts/multiplies/selects, so every configuration
// must compile and execute, including StackNoUse. No limit is tolerated here.
func Test386FloatWorkspaceSweep(t *testing.T) {
	count := int(envInt("NYANJIT_VAR_SWEEP", 16))
	cs := []Config{
		{name: "C/Platform", opt: compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}}},
		{name: "C/NoUse", opt: compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackNoUse}}},
		{name: "Go/Platform", opt: compile.Func{ABI: abi.Go, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 8192}}},
		{name: "Go/NoUse", opt: compile.Func{ABI: abi.Go, Stack: compile.StackPolicy{Practice: abi.StackNoUse}}},
	}
	for seed := 0; seed < count; seed++ {
		r := byteChoices{data: sweepBytes(uint64(seed))}
		width := uint8(32 + 32*(seed&1))
		round := func(x float64) float64 {
			if width == 32 {
				return float64(float32(x))
			}
			return x
		}
		bits := func(x float64) uint64 {
			if width == 32 {
				return uint64(math.Float32bits(float32(x)))
			}
			return math.Float64bits(x)
		}
		b := n.NewBuilder()
		b.ParamBits(64)
		b.ParamBits(32)
		b.ParamNative() // runConfig's standard three-argument entry contract
		constant := func(x float64) n.Value { return b.ConstBits(width, bits(x)) }
		vs := make([]n.Var, 1+r.Intn(12))
		want := make([]float64, len(vs))
		for i := range vs {
			want[i] = round(float64(int(r.Uint64()%33)-16) / 8)
			vs[i] = b.Var(constant(want[i]))
		}
		for step, steps := 0, 3+r.Intn(12); step < steps; step++ {
			i, j := r.Intn(len(vs)), r.Intn(len(vs))
			b.Enter()
			old := b.Get(vs[i])
			x, y := want[i], want[j]
			var result n.Value
			switch r.Intn(6) {
			case 0:
				result, want[i] = b.FAdd(old, b.Get(vs[j])), round(x+y)
			case 1:
				result, want[i] = b.FSub(old, b.Get(vs[j])), round(x-y)
			case 2:
				result, want[i] = b.FMul(old, b.Get(vs[j])), round(x*y)
			case 3:
				divisor := float64(1+r.Intn(7)) / 4
				result, want[i] = b.FDiv(old, constant(divisor)), round(x/divisor)
			case 4:
				abs := b.And(old, b.ConstBits(width, uint64(1)<<(width-1)-1))
				result, want[i] = b.FSqrt(abs), round(math.Sqrt(math.Abs(x)))
			case 5:
				result = b.FConvert(b.FConvert(old, 96-width), width)
				want[i] = float64(float32(x))
			}
			b.Set(vs[i], result)
			if r.Intn(2) == 0 {
				// A snapshot remains the old value after updating its home,
				// including the i == j case.
				b.Set(vs[j], b.FAdd(b.Get(vs[j]), old))
				want[j] = round(want[j] + x)
			}
			b.Leave()
		}
		index := r.Intn(len(vs))
		b.Return(b.CastUnsigned(b.Get(vs[index]), 64))
		p, err := b.Finish()
		if err != nil {
			t.Fatalf("seed%d: %v", seed, err)
		}
		for _, cfg := range cs {
			o := runConfig(p, cfg, [3]uint64{})
			if o.err != nil || o.skipped || o.got != bits(want[index]) {
				listing, _ := lowerListing(p, compile.TargetSpec{}, cfg.opt)
				t.Fatalf("seed%d width%d %s got%#x want%#x error=%v skipped=%t\n%s", seed, width, cfg.name, o.got, bits(want[index]), o.err, o.skipped, listing)
			}
		}
	}
	t.Logf("programs=%d configs=%d executions=%d limits=0 (C/Platform, C/NoUse, Go/Platform, Go/NoUse)", count, len(cs), count*len(cs))
}
