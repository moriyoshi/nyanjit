//go:build linux

package builder_test

import (
	"errors"
	"runtime"
	"testing"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/builder"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func expressionValue(t *testing.T) func(builder.Value, error) builder.Value {
	return func(value builder.Value, err error) builder.Value {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
}

func requireExpressionBackend(t *testing.T, err error) {
	t.Helper()
	if errors.Is(err, compile.ErrUnsupported) {
		t.Skipf("native execution unavailable on %s/%s: %v", runtime.GOOS, runtime.GOARCH, err)
	}
	if err != nil {
		t.Fatal(err)
	}
}

func TestDeferredExpressionTree(t *testing.T) {
	b := builder.NewBuilder()
	must := expressionValue(t)
	x := must(b.ParamNative())
	y := must(b.ParamNative())
	shift := must(b.LiteralBits(builder.NativeBits, 3))
	mask := must(b.LiteralBits(builder.NativeBits, 0xff))
	// Builder arithmetic also preserves the tree when given an expression.
	scaled := x.Add(y.MulConst(8))
	sum := must(b.Add(scaled, shift))
	result := sum.Xor(x.And(mask)).RotateLeft(3)
	if !b.Valid(result) || result.Bits() != builder.NativeBits {
		t.Fatal("deferred expression is not a valid native-width value")
	}
	if err := b.Return(result); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(program.Instructions) != 9 {
		t.Fatalf("got %d instructions, want 9: %+v", len(program.Instructions), program.Instructions)
	}
	wantOps := []ir.Opcode{ir.OpParam, ir.OpParam, ir.OpAddScaled, ir.OpConst, ir.OpAdd, ir.OpConst, ir.OpAnd, ir.OpXor, ir.OpRotateLeft}
	for i, want := range wantOps {
		if got := program.Instructions[i].Op; got != want {
			t.Fatalf("instruction %d = %v, want %v", i, got, want)
		}
	}
	code, err := compile.Compile(program, abi.CompileOptions{ABI: abi.C, Stack: abi.StackNoUse})
	requireExpressionBackend(t, err)
	defer code.Close()
	for _, pair := range [][2]uint64{{0, 0}, {7, 9}, {^uint64(0), 2}} {
		got, err := code.Call(uintptr(pair[0]), uintptr(pair[1]))
		if err != nil {
			t.Fatal(err)
		}
		widthMask := ^uint64(0) >> (64 - builder.NativeBits)
		x, y := pair[0]&widthMask, pair[1]&widthMask
		intermediate := ((x + y*8 + 3) ^ (x & 0xff)) & widthMask
		want := (intermediate<<3 | intermediate>>(builder.NativeBits-3)) & widthMask
		if uint64(got) != want {
			t.Fatalf("Call(%#x, %#x) = %#x, want %#x", x, y, got, want)
		}
	}
}

func TestDeferredExpressionReuseAndScope(t *testing.T) {
	b := builder.NewBuilder()
	must := expressionValue(t)
	x := must(b.Param())
	if err := b.Enter(); err != nil {
		t.Fatal(err)
	}
	local := must(b.Const(1))
	shared := x.Add(local)
	result := shared.Xor(shared)
	if err := b.Leave(shared); err != nil {
		t.Fatal(err)
	}
	if !b.Valid(shared) || !b.Valid(result) || b.Valid(local) {
		t.Fatal("exported expression became invalid")
	}
	if err := b.Return(result); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	adds := 0
	for _, ins := range program.Instructions {
		if ins.Op == ir.OpAdd {
			adds++
		}
	}
	if adds != 1 {
		t.Fatalf("shared subtree emitted %d additions, want one", adds)
	}
}

func TestDeferredExpressionRejectsHiddenAndForeignLeaves(t *testing.T) {
	b := builder.NewBuilder()
	must := expressionValue(t)
	if err := b.Enter(); err != nil {
		t.Fatal(err)
	}
	x := must(b.Const(10))
	expression := x.Add(x)
	if err := b.Leave(); err != nil {
		t.Fatal(err)
	}
	if b.Valid(expression) || !errors.Is(b.Return(expression), builder.ErrInvalidFunction) {
		t.Fatal("expression used a hidden value")
	}
	other := builder.NewBuilder()
	y := must(other.Const(1))
	if mixed := x.Add(y); mixed.Bits() != 0 {
		t.Fatal("expression accepted values from different builders")
	}
}

func TestDeferredExpressionInPredicateAndEdge(t *testing.T) {
	b := builder.NewBuilder()
	must := expressionValue(t)
	entry, yes, no := b.NewLabel(), b.NewLabel(), b.NewLabel()
	if err := b.Bind(entry); err != nil {
		t.Fatal(err)
	}
	x := must(b.ParamNative())
	one := must(b.LiteralBits(builder.NativeBits, 1))
	condition, err := b.CompareImmediate(x.Sub(one), builder.BranchEqual, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := b.BranchArgs(condition, yes, []builder.Value{x.Add(one)}, no, []builder.Value{x.Sub(one)}); err != nil {
		t.Fatal(err)
	}
	yesValues, err := b.BindParamsBits(yes, []uint8{builder.NativeBits})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(yesValues[0]); err != nil {
		t.Fatal(err)
	}
	noValues, err := b.BindParamsBits(no, []uint8{builder.NativeBits})
	if err != nil {
		t.Fatal(err)
	}
	if err := b.Return(noValues[0]); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOARCH == "386" {
		// The 386 control backend does not lower this conditional edge shape.
		return
	}
	code, err := compile.Compile(program, abi.CompileOptions{ABI: abi.C, Stack: abi.StackPlatform})
	requireExpressionBackend(t, err)
	defer code.Close()
	for _, input := range []uint64{0, 1, 5} {
		got, err := code.Call(uintptr(input))
		widthMask := ^uint64(0) >> (64 - builder.NativeBits)
		want := input - 1&widthMask
		if input == 1 {
			want = input + 1
		}
		if err != nil || uint64(got) != want {
			t.Fatalf("Call(%d) = %d, %v; want %d", input, got, err, want)
		}
	}
}

func TestDeferredExpressionInMemoryAndCallOperands(t *testing.T) {
	b := builder.NewBuilder()
	must := expressionValue(t)
	base := must(b.ParamNative())
	address := base.Add(must(b.LiteralBits(builder.NativeBits, 8)))
	byteValue := must(b.LiteralBits(8, 0x7f)).Xor(must(b.LiteralBits(8, 1)))
	if err := b.StoreBits(address, byteValue); err != nil {
		t.Fatal(err)
	}
	loaded := must(b.LoadBits(address, 8))
	argument := loaded.CastUnsigned(64).Add(must(b.Literal(1)))
	target := base.Add(must(b.LiteralBits(builder.NativeBits, 64)))
	called := must(b.CallC(target, argument))
	if err := b.Return(called); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if err := program.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestDeferredVariableShiftExpression(t *testing.T) {
	b := builder.NewBuilder()
	must := expressionValue(t)
	value := must(b.ParamNative())
	count := must(b.ParamNative())
	result := value.ShiftLeftBy(count).Xor(value.RotateRight(1))
	if err := b.Return(result); err != nil {
		t.Fatal(err)
	}
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOARCH == "386" {
		return
	}
	code, err := compile.Compile(program, abi.CompileOptions{ABI: abi.C, Stack: abi.StackPlatform, StackBytesAvailable: 65536})
	requireExpressionBackend(t, err)
	defer code.Close()
	for _, pair := range [][2]uint64{{1, 0}, {7, 3}, {9, uint64(builder.NativeBits)}} {
		got, err := code.Call(uintptr(pair[0]), uintptr(pair[1]))
		mask := ^uint64(0) >> (64 - builder.NativeBits)
		value := pair[0] & mask
		shifted := uint64(0)
		if pair[1] < uint64(builder.NativeBits) {
			shifted = value << pair[1] & mask
		}
		rotated := value>>1 | value<<(builder.NativeBits-1)&mask
		want := shifted ^ rotated
		if err != nil || uint64(got) != want {
			t.Fatalf("Call(%#x, %d) = %#x, %v; want %#x", pair[0], pair[1], got, err, want)
		}
	}
}

func TestDeferredExpressionThroughFacade(t *testing.T) {
	b := nyanjit.NewBuilder()
	x := b.ParamNative()
	b.Return(x.Add(b.LiteralBits(nyanjit.NativeBits, 2)))
	program, err := b.Finish()
	if err != nil {
		t.Fatal(err)
	}
	code, err := compile.Compile(program, abi.CompileOptions{ABI: abi.C, Stack: abi.StackNoUse})
	requireExpressionBackend(t, err)
	defer code.Close()
	got, err := code.Call(40)
	if err != nil || got != 42 {
		t.Fatalf("Call(40) = %d, %v; want 42", got, err)
	}
}
