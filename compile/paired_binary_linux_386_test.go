//go:build linux && 386

package compile_test

import (
	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/execution"
	"testing"
)

func Test386PairedBinaryWorkspaceSpills(t *testing.T) {
	for _, op := range []string{"add", "sub", "and", "or", "xor"} {
		b := nyanjit.NewBuilder()
		x, y := b.Param(), b.Param()
		var z nyanjit.Value
		switch op {
		case "add":
			z = b.Add(x, y)
		case "sub":
			z = b.Sub(x, y)
		case "and":
			z = b.And(x, y)
		case "or":
			z = b.Or(x, y)
		case "xor":
			z = b.Xor(x, y)
		}
		// Keep both inputs live past the operation so neither may be overwritten.
		b.Return(b.Xor(b.Xor(z, x), y))
		p, err := b.Finish()
		if err != nil {
			t.Fatal(err)
		}
		code, err := compile.Compile(p, abi.CompileOptions{Stack: abi.StackNoUse})
		if err != nil {
			t.Fatal(err)
		}
		for _, xy := range [][2]uint64{{0xffffffff, 1}, {0, 1}, {0x1234567800000000, 0xabcdef12ffffffff}, {^uint64(0), ^uint64(0)}, {0x8000000000000000, 1}} {
			x, y := xy[0], xy[1]
			want := uint64(0)
			switch op {
			case "add":
				want = x + y
			case "sub":
				want = x - y
			case "and":
				want = x & y
			case "or":
				want = x | y
			case "xor":
				want = x ^ y
			}
			want ^= x ^ y
			lo, hi, err := code.CallPair(execution.Words("LL", x, y)...)
			got := execution.Wide(lo, hi)
			if err != nil || got != want {
				t.Fatalf("%s (%#x,%#x) got%#x,%v want%#x", op, x, y, got, err, want)
			}
		}
		code.Close()
	}
}
