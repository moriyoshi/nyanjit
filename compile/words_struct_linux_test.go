//go:build linux && (amd64 || arm64 || 386 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"testing"
	"unsafe"

	n "github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
)

type wordsInput struct {
	A uint32
	B uint64
	C struct{ D uint16 }
}

type wordsOutput struct {
	Pad uint8
	Sum uint64
}

// Generated code reads a struct passed by value and writes through a struct
// pointer, both supplied by execution.Words.
func TestWordsStructArguments(t *testing.T) {
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		b := n.NewBuilder()
		in, out := b.ParamNative(), b.ParamNative()
		field := func(base n.Value, offset uintptr) n.Value {
			return b.Add(base, b.ConstNative(offset))
		}
		a := b.CastUnsigned(b.LoadBits(field(in, unsafe.Offsetof(wordsInput{}.A)), 32), 64)
		x := b.LoadBits(field(in, unsafe.Offsetof(wordsInput{}.B)), 64)
		d := b.CastUnsigned(b.LoadBits(field(in, unsafe.Offsetof(wordsInput{}.C)), 16), 64)
		sum := b.Add(b.Add(a, x), d)
		b.StoreBits(field(out, unsafe.Offsetof(wordsOutput{}.Sum)), sum)
		b.Return(sum)
		program, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		code, err := n.Compile(program, compile.TargetSpec{}, compile.Func{ABI: flavor, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}})
		if err != nil {
			t.Fatal(err)
		}
		input := wordsInput{A: 0x80000001, B: 0x1234567800000000}
		input.C.D = 0xfffe
		var output wordsOutput
		output.Pad = 0xa5
		lo, hi, err := code.CallPair(execution.Words(input, &output)...)
		want := uint64(0x80000001 + 0x1234567800000000 + 0xfffe)
		if got := execution.Wide(lo, hi); err != nil || got != want || output.Sum != want || output.Pad != 0xa5 {
			t.Fatalf("ABI %v: result %#x output %+v err %v, want %#x", flavor, got, output, err, want)
		}
		code.Close()
	}
}
