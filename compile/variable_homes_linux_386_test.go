//go:build linux && 386

package compile_test

import (
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"runtime"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

//go:embed testdata/variable-homes/*.json
var variableHomeFixtures embed.FS

// TestVariableHomesSurviveFallbackExpansions runs programs whose 64-bit count
// and high-product operations expand into paired Select or multiply sequences
// with fixed registers. A variable home in one of those registers used to be
// overwritten under StackPlatform, and the next use dereferenced it.
func TestVariableHomesSurviveFallbackExpansions(t *testing.T) {
	paths, err := fs.Glob(variableHomeFixtures, "testdata/variable-homes/*.json")
	if err != nil || len(paths) == 0 {
		t.Fatalf("fixtures: %v", err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			raw, err := variableHomeFixtures.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var p ir.Program
			if err := json.Unmarshal(raw, &p); err != nil {
				t.Fatal(err)
			}
			want := ""
			for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
				for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
					for _, harden := range []bool{false, true} {
						options := compile.Func{ABI: flavor, Stack: compile.StackPolicy{Practice: stack}, Harden: harden}
						if stack == abi.StackPlatform {
							options.Stack.BytesAvailable = 8192
						}
						got := runVariableHomeProgram(t, &p, options)
						if want == "" {
							want = got
						} else if got != want {
							t.Fatalf("stack %d abi %d harden %v: %s, want %s", stack, flavor, harden, got, want)
						}
					}
				}
			}
		})
	}
}

func runVariableHomeProgram(t *testing.T, p *ir.Program, options compile.Func) string {
	t.Helper()
	code, err := nyanjit.Compile(p.Clone(), compile.TargetSpec{}, options)
	if err != nil {
		t.Fatalf("%+v: %v", options, err)
	}
	defer code.Close()
	buf := make([]byte, 96)
	data := make([]byte, 64)
	for i := range buf {
		buf[i] = byte(i*37 + 11)
	}
	for i := range data {
		data[i] = byte(i*53 + 7)
	}
	var pin runtime.Pinner
	pin.Pin(&buf[0])
	pin.Pin(&data[0])
	defer pin.Unpin()
	var words []uintptr
	param := 0
	for _, in := range p.Instructions {
		if in.Op != ir.OpParam {
			continue
		}
		bits := in.Bits
		if bits == 0 {
			bits = 64
		}
		value := [...]uint64{0x0123456789abcdef, 0x9e3779b9, uint64(uintptr(unsafe.Pointer(&buf[32]))), uint64(uintptr(unsafe.Pointer(&data[0])))}[param]
		if bits < 64 {
			value &= ^uint64(0) >> (64 - bits)
		}
		words = append(words, uintptr(value))
		if bits == 64 {
			words = append(words, uintptr(value>>32))
		}
		param++
	}
	// Raw StackPlatform code runs on the goroutine stack; grow it past
	// StackBytesAvailable first.
	securityGrowStack(16)
	lo, hi, err := code.CallPair(words...)
	if err != nil {
		t.Fatalf("%+v: %v", options, err)
	}
	return fmt.Sprintf("result %#x buf %x data %x", execution.Wide(lo, hi), buf, data)
}
