// Package irconform is a conformance suite for nyanjit IR. Each Case is a
// program with inputs; Check compiles it for a target, runs it natively and
// in the reference interpreter (instrument/irinterp), and compares the
// results and any memory the program writes.
//
// The corpus covers every opcode the interpreter can run without a host
// transition, at every width, with edge-case inputs: shift counts at and
// beyond the width, overflow, signed minimum, IEEE specials, vectors and
// masks, loads and stores in each byte order and alignment, Vars and scopes,
// multi-way control, critical edges and an irreducible loop, and raw-IR
// block parameters: edge arguments, loop-carried values and parallel
// copies. Calls are covered by compile/varfuzz, which uses the
// interpreter as an oracle; system calls are not covered.
//
// The package is instrumentation: tests and tools may import it, production
// code must not.
package irconform

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/instrument/irinterp"
	"github.com/moriyoshi/nyanjit/ir"
)

// Case is one conformance program and the inputs it runs with.
type Case struct {
	// Name identifies the case, as "group/detail".
	Name string
	// Build returns the program. Programs may depend on the host, for
	// example on its pointer width.
	Build func() (*ir.SealedProgram, error)
	// Inputs lists argument vectors. When Buffer is nonzero, each run gets
	// a fresh buffer and its address is the first argument, before the
	// input's values.
	Inputs [][]uint64
	// Buffer is the size in bytes of a scratch buffer the program reads
	// and writes, or zero. Check fills it with a fixed pattern before each
	// run and compares it afterwards.
	Buffer int
}

// crossBlockPrefix names cases that use a value in a later block it
// dominates, without a label argument.
const crossBlockPrefix = "cross-block/"

// stack is the frame budget each case is compiled with.
const stack = 1 << 14

// BaselineGap starts the skip message of a case that a TierBaseline target
// does not implement yet (compile.ErrUnsupported).
const BaselineGap = "baseline tier gap"

// Check compiles c for target as a C-ABI entry with a platform stack, runs
// every input natively and in the interpreter, and reports differences
// through t. It skips a case the target cannot run, such as a 128-bit result
// on a 32-bit host, and on a TierBaseline target a case the tier does not
// implement yet, with a message starting with BaselineGap.
func Check(t testing.TB, target compile.Target, c Case) {
	t.Helper()
	program, err := c.Build()
	if err != nil {
		if strings.HasPrefix(c.Name, crossBlockPrefix) && errors.Is(err, ir.ErrInvalidProgram) {
			t.Skipf("%s: pending cross-block support: %v", c.Name, err)
		}
		t.Fatalf("%s: build: %v", c.Name, err)
	}
	var result ir.Instruction
	if control := program.Control(); control != nil {
		for _, step := range control.Steps {
			if !step.IsLabel && step.Term == ir.TermReturn {
				result = program.Instructions()[step.Result]
				if len(step.ResultParts) != 0 {
					result.Type = ir.Type{Kind: ir.ScalarKind, Lanes: 1, LaneBits: 128}
				}
				break
			}
		}
	} else {
		result = program.Instructions()[program.Result()]
		if len(program.ResultParts()) != 0 {
			result.Type = ir.Type{Kind: ir.ScalarKind, Lanes: 1, LaneBits: 128}
		}
	}
	if result.Type.LaneBits == 128 && unsafe.Sizeof(uintptr(0)) == 4 {
		t.Skipf("%s: 128-bit results need two 64-bit words", c.Name)
	}
	// A platform stack frame larger than Code.Call's limit (240 bytes on 386)
	// cannot be called; spill to Code.Call's workspace instead.
	var code *execution.Code
	for _, f := range []compile.Func{
		{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: stack}},
		{ABI: abi.C},
	} {
		unit, err := compile.Lower(target, program, f)
		if err == nil {
			var template *execution.Template
			if template, err = unit.Template(); err == nil {
				code, err = template.Bind(nil)
			}
		}
		if errors.Is(err, compile.ErrCPUUnsupported) {
			t.Skipf("%s: %v", c.Name, err)
		}
		if errors.Is(err, compile.ErrUnsupported) && target.Spec().Tier == compile.TierBaseline {
			t.Skipf("%s: %s: %v", c.Name, BaselineGap, err)
		}
		if err == nil || !errors.Is(err, compile.ErrBackendLimit) {
			if err != nil {
				t.Fatalf("%s: %v", c.Name, err)
			}
			break
		}
		if f.Stack.Practice != abi.StackPlatform {
			// The target cannot run this operation at all, for example FMA
			// on 386.
			t.Skipf("%s: %v", c.Name, err)
		}
	}
	defer code.Close()
	var widths []uint8
	for _, ins := range program.Instructions() {
		if ins.Op == ir.OpParam {
			widths = append(widths, ins.ValueType().LaneBits)
		}
	}
	contract := false
	for _, ins := range program.Instructions() {
		contract = contract || ins.Contract
	}
	for _, input := range c.Inputs {
		var nativeMemory, referenceMemory []byte
		nativeArgs, referenceArgs := input, input
		if c.Buffer != 0 {
			nativeMemory, referenceMemory = pattern(c.Buffer), pattern(c.Buffer)
			nativeArgs = append([]uint64{address(nativeMemory)}, input...)
			referenceArgs = append([]uint64{address(referenceMemory)}, input...)
		}
		if len(nativeArgs) != len(widths) {
			t.Fatalf("%s: %d arguments for %d parameters", c.Name, len(nativeArgs), len(widths))
		}
		words := make([]any, len(nativeArgs))
		for i, x := range nativeArgs {
			words[i] = argument(widths[i], x)
		}
		nativeLo, nativeHi, err := code.CallPair(execution.Words(words...)...)
		if err != nil {
			t.Fatalf("%s%v: call: %v", c.Name, input, err)
		}
		lo, hi := uint64(nativeLo), uint64(nativeHi)
		if result.ValueType().LaneBits == 64 && unsafe.Sizeof(uintptr(0)) == 4 {
			lo, hi = execution.Wide(nativeLo, nativeHi), 0
		}
		var nativeAfter []byte
		if nativeMemory != nil {
			nativeAfter = bytes.Clone(nativeMemory)
		}
		want, err := irinterp.Run(program, referenceArgs, nil)
		if err != nil {
			t.Fatalf("%s%v: interpreter: %v", c.Name, input, err)
		}
		matched := want.Matches(lo, hi) && (want.Unspecified || bytes.Equal(nativeAfter, referenceMemory))
		if !matched && contract {
			// Targets with FMA fuse contractible products; others round
			// them separately. Either is conforming.
			if nativeMemory != nil {
				referenceMemory = pattern(c.Buffer)
				referenceArgs[0] = address(referenceMemory)
			}
			fused, err := irinterp.Run(program, referenceArgs, &irinterp.Options{Contract: true})
			if err == nil && fused.Matches(lo, hi) && (fused.Unspecified || bytes.Equal(nativeAfter, referenceMemory)) {
				matched = true
			}
		}
		if !matched {
			t.Errorf("%s%s on %s: native %#x:%#x, interpreter %v%s", c.Name, hexes(input), target.Architecture(), hi, lo, want, memoryDiff(nativeAfter, referenceMemory))
		}
	}
}

func argument(bits uint8, x uint64) any {
	switch {
	case bits == 64:
		return x
	case bits == 32 && unsafe.Sizeof(uintptr(0)) == 4:
		return uintptr(x)
	case bits == 32:
		return uint32(x)
	case bits == 16:
		return uint16(x)
	default:
		return uint8(x)
	}
}

// pattern returns a buffer with a fixed, position-dependent fill. Its
// backing array is 16-byte aligned, as vector cases expect for offset 0.
func pattern(size int) []byte {
	storage := make([]uint64, (size+31)/8)
	buffer := unsafe.Slice((*byte)(unsafe.Pointer(&storage[0])), len(storage)*8)
	if offset := int(uintptr(unsafe.Pointer(&buffer[0])) & 15); offset != 0 {
		buffer = buffer[16-offset:]
	}
	buffer = buffer[:size:size]
	for i := range buffer {
		buffer[i] = byte(i*37 + 11)
	}
	return buffer
}

func address(buffer []byte) uint64 { return uint64(uintptr(unsafe.Pointer(&buffer[0]))) }

func hexes(input []uint64) string {
	parts := make([]string, len(input))
	for i, x := range input {
		parts[i] = "0x" + strconv.FormatUint(x, 16)
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func memoryDiff(native, reference []byte) string {
	for i := range native {
		if native[i] != reference[i] {
			return fmt.Sprintf("; memory differs from byte %d: native % x, interpreter % x", i, native[i:min(i+16, len(native))], reference[i:min(i+16, len(reference))])
		}
	}
	return ""
}
