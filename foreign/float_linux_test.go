//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || loong64)

package foreign

import (
	"errors"
	"math"
	"testing"

	"github.com/ebitengine/purego"
	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
)

func TestNewRejectsFloatPassing(t *testing.T) {
	nine := abi.FloatPassing(0)
	for i := range 9 {
		nine |= abi.FloatArgument(i)
	}
	effect := &abi.CallEffect{}
	for name, c := range map[string]struct {
		count  int
		floats abi.FloatPassing
		effect *abi.CallEffect
	}{
		"beyond arguments": {1, abi.FloatArgument(1), nil},
		"both results":     {1, abi.FloatResult32 | abi.FloatResult64, nil},
		"nine floats":      {8, nine, nil},
		"effect differs":   {1, abi.FloatArgument(0), effect},
	} {
		if _, err := New(1, c.count, c.floats, c.effect); !errors.Is(err, backend.ErrInvalidFunction) {
			t.Errorf("%s: %v, want ErrInvalidFunction", name, err)
		}
	}
}

// TestCallFloatLibm calls libm ldexp(double, int) through a ForeignFunc
// with float passing.
func TestCallFloatLibm(t *testing.T) {
	handle, err := purego.Dlopen("libm.so.6", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		t.Skipf("libm: %v", err)
	}
	defer purego.Dlclose(handle)
	address, err := purego.Dlsym(handle, "ldexp")
	if err != nil {
		t.Skip(err)
	}
	ldexp, err := New(address, 2, abi.FloatArgument(0)|abi.FloatResult64, nil)
	if err != nil {
		t.Fatal(err)
	}
	b := nyanjit.NewBuilder()
	x, e := b.Param(), b.Param()
	r, err := Call(b, ldexp, x, e)
	if err != nil {
		t.Fatal(err)
	}
	b.Return(r)
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{ABI: abi.C, Context: compile.ContextForeign,
		Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 512}})
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	got, err := code.Call(uintptr(math.Float64bits(1.5)), 4)
	if err != nil || math.Float64frombits(uint64(got)) != 24 {
		t.Fatalf("ldexp(1.5, 4) = %v, %v", math.Float64frombits(uint64(got)), err)
	}
}
