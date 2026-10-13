//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || loong64)

package compile_test

import (
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/instrument/irinterp"
	"github.com/moriyoshi/nyanjit/ir"
)

// The generated code receives raw addresses, so its memory must not move with
// a goroutine stack.
var aliasedLabelMemory, aliasedLabelOutput [64]uint64

func TestAliasedLabelsShareParameterHomes(t *testing.T) {
	mem, out := aliasedLabelMemory[:], aliasedLabelOutput[:]
	for i := range mem {
		mem[i] = uint64(i)*0x9e3779b97f4a7c15 + 0x1234
	}
	target, err := compile.NewTarget(compile.TargetSpec{})
	if err != nil {
		t.Fatal(err)
	}
	platform := compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 640}
	for _, tc := range []struct {
		name  string
		build func() (*ir.SealedProgram, error)
		args  []uintptr
	}{
		{"sequential var loops", sequentialVarLoops, []uintptr{uintptr(unsafe.Pointer(&mem[0])), 0x5555aaaa12345678, uintptr(unsafe.Pointer(&out[0]))}},
		{"aliased label cross-block", aliasedLabelCrossBlock, []uintptr{12345}},
	} {
		p, err := tc.build()
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		words := make([]uint64, len(tc.args))
		for i, a := range tc.args {
			words[i] = uint64(a)
		}
		want, err := irinterp.Run(p, words, nil)
		if err != nil {
			t.Fatalf("%s: interpreter: %v", tc.name, err)
		}
		for context, f := range map[string]compile.Func{
			"C/StackPlatform": {ABI: abi.C, Stack: platform},
			"C/StackNoUse":    {ABI: abi.C},
			"Go":              {ABI: abi.Go, Stack: platform},
		} {
			unit, err := compile.Lower(target, p, f)
			if err != nil {
				t.Fatalf("%s %s: %v", tc.name, context, err)
			}
			tmpl, err := unit.Template()
			if err != nil {
				t.Fatalf("%s %s: %v", tc.name, context, err)
			}
			code, err := tmpl.Bind(nil)
			if err != nil {
				t.Fatalf("%s %s: %v", tc.name, context, err)
			}
			got, err := code.Call(tc.args...)
			code.Close()
			if err != nil || !want.Matches(uint64(got), 0) {
				t.Errorf("%s %s: got %#x, %v; want %v", tc.name, context, got, err, want)
			}
		}
	}
}
