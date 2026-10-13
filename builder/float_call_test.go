package builder

import (
	"errors"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/ir"
)

// TestCallWithFloatsRecordsPassing records the float passing in Imm and a
// binary32 result's width in Bits.
func TestCallWithFloatsRecordsPassing(t *testing.T) {
	for _, result := range []abi.FloatPassing{0, abi.FloatResult32, abi.FloatResult64} {
		for _, op := range []ir.Opcode{ir.OpCallC, ir.OpCallGo} {
			b := NewBuilder()
			x, _ := b.ParamBits(32)
			n, _ := b.Param()
			target, _ := b.ConstNative(0x1000)
			floats := abi.FloatArgument(0) | result
			call := b.CallCWithFloats
			if op == ir.OpCallGo {
				call = b.CallGoWithFloats
			}
			value, err := call(target, floats, x, n)
			if err != nil {
				t.Fatal(err)
			}
			want := uint8(64)
			if result == abi.FloatResult32 {
				want = 32
			}
			if value.Type().LaneBits != want {
				t.Errorf("%v %#x: result has %d bits", op, uint64(floats), value.Type().LaneBits)
			}
			if err := b.Return(value); err != nil {
				t.Fatal(err)
			}
			p, err := b.Finish()
			if err != nil {
				t.Fatal(err)
			}
			for _, ins := range p.Instructions() {
				if ins.Op == op && abi.FloatPassing(ins.Imm) != floats {
					t.Errorf("%v: Imm %#x, want %#x", op, ins.Imm, uint64(floats))
				}
			}
		}
	}
}

func TestCallWithFloatsRejects(t *testing.T) {
	b := NewBuilder()
	x, _ := b.ParamBits(16)
	y, _ := b.Param()
	target, _ := b.ConstNative(0x1000)
	nine := abi.FloatPassing(0)
	args := make([]Value, 9)
	for i := range args {
		nine |= abi.FloatArgument(i)
		args[i] = y
	}
	for name, c := range map[string]struct {
		floats abi.FloatPassing
		args   []Value
		want   error
	}{
		"beyond arguments": {abi.FloatArgument(1), []Value{y}, ErrInvalidFunction},
		"16-bit argument":  {abi.FloatArgument(0), []Value{x}, ErrInvalidFunction},
		"both results":     {abi.FloatResult32 | abi.FloatResult64, []Value{y}, ErrInvalidFunction},
		"nine floats":      {nine, args, ErrBackendLimit},
	} {
		if _, err := b.CallGoWithFloats(target, c.floats, c.args...); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	// CallCWithEffect takes its float passing from the effect.
	effect, err := abi.PreflightCall(abi.CallSpec{Kind: abi.FunctionCall, ABI: abi.C, ArgumentCount: 1, Floats: abi.FloatArgument(0) | abi.FloatResult64})
	if err != nil {
		t.Fatal(err)
	}
	value, err := b.CallCWithEffect(target, effect, y)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(value); err != nil {
		t.Fatal(err)
	}
	p, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if ins := p.Instructions()[value.id]; abi.FloatPassing(ins.Imm) != effect.Floats {
		t.Errorf("CallCWithEffect Imm %#x, want %#x", ins.Imm, uint64(effect.Floats))
	}
}
