//go:build linux && (amd64 || arm64 || riscv64 || ppc64le || 386 || loong64)

package irinterp_test

import (
	"bytes"
	"errors"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/goregister"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/instrument/irinterp"
	"github.com/moriyoshi/nyanjit/instrument/workload"
	"github.com/moriyoshi/nyanjit/ir"
)

// TestWorkloadsMatchNative runs every workload that can execute natively and
// compares its result, and the memory it writes, with the interpreter.
func TestWorkloadsMatchNative(t *testing.T) {
	if !goregister.Supported() {
		t.Skip("the workloads run as Go-managed entries, which need goregister")
	}
	// Workloads that take pointers name the buffer size behind each one.
	buffers := map[string][]int{"vectors": {16}, "sha256-scalar": {8 * 16, 16 * 16}, "sha256-simd": {8 * 16, 16 * 16}}
	for _, w := range workload.All() {
		t.Run(w.Name, func(t *testing.T) {
			if w.Foreign {
				t.Skip("calls a fixed fake address")
			}
			spec := compile.TargetSpec{}
			if w.Scalar {
				spec.Vectors = compile.VectorsOff()
			}
			target, err := compile.NewTarget(spec)
			if err != nil {
				t.Fatal(err)
			}
			// Code.Call bounds the native frame, so shrink a shape until it
			// fits; fixed-size shapes that never fit are skipped.
			var program *ir.SealedProgram
			var code *execution.Code
			for n := min(w.Size, 200); ; n /= 2 {
				program = w.Build(max(n, 1))
				code, err = bind(target, program)
				if !errors.Is(err, compile.ErrBackendLimit) {
					break
				}
				if n <= 1 || w.ScalingSize == 0 {
					t.Skip(err)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			// Go-managed code stays registered for the process lifetime.
			// Some workloads loop as many times as an argument says, so the
			// arguments stay small.
			for _, seed := range []uint64{0, 1, 7, 300} {
				var native, reference [][]byte
				args := make([]any, program.Arity())
				nativeArgs, referenceArgs := make([]uint64, program.Arity()), make([]uint64, program.Arity())
				for i := range args {
					x := seed + uint64(i)*3
					nativeArgs[i], referenceArgs[i] = x, x
					if sizes := buffers[w.Name]; sizes != nil {
						n, r := fill(sizes[i], seed), fill(sizes[i], seed)
						native, reference = append(native, n), append(reference, r)
						nativeArgs[i], referenceArgs[i] = uint64(uintptr(unsafe.Pointer(&n[0]))), uint64(uintptr(unsafe.Pointer(&r[0])))
					}
				}
				for i, ins := range parameters(program.Instructions()) {
					if ins == 64 {
						args[i] = nativeArgs[i]
					} else {
						args[i] = uintptr(nativeArgs[i])
					}
				}
				lo, hi, err := code.CallPair(execution.Words(args...)...)
				if err != nil {
					t.Fatal(err)
				}
				got := execution.Wide(lo, hi)
				want, err := irinterp.Run(program, referenceArgs, nil)
				if err != nil {
					t.Fatal(err)
				}
				if !want.Matches(got, 0) {
					t.Errorf("seed %#x: native %#x, interpreter %v", seed, got, want)
				}
				for i := range native {
					if !bytes.Equal(native[i], reference[i]) {
						t.Errorf("seed %#x: buffer %d differs: native % x, interpreter % x", seed, i, native[i], reference[i])
					}
				}
			}
		})
	}
}

// bind compiles program as a Go-managed entry, which runs on the goroutine
// stack.
func bind(target compile.Target, program *ir.SealedProgram) (*execution.Code, error) {
	unit, err := compile.Lower(target, program, compile.Func{ABI: abi.Go, Context: compile.ContextGoManaged,
		Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 1 << 14}, AssumePointerFree: true})
	if err != nil {
		return nil, err
	}
	template, err := unit.Template()
	if err != nil {
		return nil, err
	}
	return template.Bind(nil)
}

func parameters(ins []ir.Instruction) []uint8 {
	var bits []uint8
	for _, i := range ins {
		if i.Op == ir.OpParam {
			bits = append(bits, i.ValueType().LaneBits)
		}
	}
	return bits
}

// fill returns a 16-byte aligned buffer with a seed-dependent pattern.
func fill(size int, seed uint64) []byte {
	storage := make([]uint64, size/8+2)
	buffer := unsafe.Slice((*byte)(unsafe.Pointer(&storage[0])), len(storage)*8)
	if offset := int(uintptr(unsafe.Pointer(&buffer[0])) & 15); offset != 0 {
		buffer = buffer[16-offset:]
	}
	buffer = buffer[:size:size]
	for i := range buffer {
		buffer[i] = byte(uint64(i)*37 + seed*13 + 11)
	}
	return buffer
}
