//go:build linux && ppc64le

package compile_test

import (
	"bytes"
	"encoding/binary"
	"math/bits"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/target/ppc64"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func finishPPC(t *testing.T, builder *nyanjit.Builder) *ir.Program {
	t.Helper()
	program, err := testFinishRaw(builder)
	if err != nil {
		t.Fatalf("Finish: %v", err)
	}
	return program
}

func compilePPC(t *testing.T, builder *nyanjit.Builder, f compile.Func) (*execution.Code, abi.EmissionReport) {
	t.Helper()
	prepared, err := lowerTemplate(finishPPC(t, builder), compile.TargetSpec{}, f)
	if err != nil {
		t.Fatalf("lower ABI %d stack %d: %v", f.ABI, f.Stack.Practice, err)
	}
	code, err := prepared.Bind(nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := code.Close(); err != nil {
			t.Error(err)
		}
	})
	return code, prepared.Report().Snapshot()
}

func TestPPC64ScalarWidthsAndConstantConstruction(t *testing.T) {
	for _, width := range []uint8{8, 16, 32, 64} {
		t.Run(strconv.Itoa(int(width)), func(t *testing.T) {
			b := nyanjit.NewBuilder()
			x, y := b.ParamBits(width), b.ParamBits(width)
			constant := uint64(0x8123456789abcdef) & (^uint64(0) >> (64 - width))
			b.Return(b.Xor(b.Add(b.Mul(x, y), b.ConstBits(width, constant)), x))
			code, _ := compilePPC(t, b, compile.Func{ABI: abi.C})
			mask := ^uint64(0) >> (64 - width)
			for _, operands := range [][2]uint64{{0, 0}, {7, 9}, {^uint64(0), 0x87654321}} {
				got, err := code.Call(uintptr(operands[0]), uintptr(operands[1]))
				want := (((operands[0]&mask)*(operands[1]&mask) + constant) ^ (operands[0] & mask)) & mask
				if err != nil || uint64(got) != want {
					t.Fatalf("Call(%#x, %#x) = %#x, %v; want %#x", operands[0], operands[1], got, err, want)
				}
			}
		})
	}
	for _, value := range []uint64{0, 32767, 32768, uint64(1) << 31, 0x7fffffff80000000, 0x8000000000000000, ^uint64(0)} {
		b := nyanjit.NewBuilder()
		b.Return(b.Const(value))
		code, _ := compilePPC(t, b, compile.Func{ABI: abi.C})
		if got, err := code.Call(); err != nil || uint64(got) != value {
			t.Fatalf("constant %#x = %#x, %v", value, got, err)
		}
	}
}

func TestPPC64SignedAndUnsignedPredicates(t *testing.T) {
	for _, test := range []struct {
		condition         ir.BranchCondition
		left, right, want uint64
		mnemonic          string
	}{
		{ir.BranchLess, ^uint64(0), 1, 1, "cmpd"},
		{ir.BranchBelow, ^uint64(0), 1, 0, "cmpld"},
		{ir.BranchGreaterEqual, uint64(1) << 63, 0, 0, "cmpd"},
		{ir.BranchAboveEqual, uint64(1) << 63, 0, 1, "cmpld"},
	} {
		b := nyanjit.NewBuilder()
		x, y := b.Param(), b.Param()
		yes, no := b.NewLabel(), b.NewLabel()
		b.Branch(b.Compare(x, test.condition, y), yes)
		b.Jump(no)
		b.Bind(yes)
		b.Return(b.Const(1))
		b.Bind(no)
		b.Return(b.Const(0))
		program := finishPPC(t, b)
		assembly, err := lowerListing(program, compile.TargetSpec{}, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}})
		if err != nil || !strings.Contains(assembly, test.mnemonic) {
			t.Fatalf("comparison listing = %q, %v", assembly, err)
		}
		code, err := nyanjit.Compile(program, compile.TargetSpec{}, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}})
		if err != nil {
			t.Fatal(err)
		}
		got, callErr := code.Call(uintptr(test.left), uintptr(test.right))
		if err := code.Close(); err != nil {
			t.Fatal(err)
		}
		if callErr != nil || uint64(got) != test.want {
			t.Fatalf("condition %d = %d, %v; want %d", test.condition, got, callErr, test.want)
		}
	}
}

func TestPPC64ShiftAndRotationWidthRules(t *testing.T) {
	for _, width := range []uint8{8, 16, 32, 64} {
		mask := ^uint64(0) >> (64 - width)
		for _, operation := range []string{"left", "right", "arithmetic", "rotate-left", "rotate-right"} {
			for _, count := range []uint8{0, 1, width - 1, width, width + 1} {
				b := nyanjit.NewBuilder()
				x := b.ParamBits(width)
				var result nyanjit.Value
				switch operation {
				case "left":
					result = b.ShiftLeft(x, count)
				case "right":
					result = b.ShiftRight(x, count)
				case "arithmetic":
					result = b.ShiftRightArithmetic(x, count)
				case "rotate-left":
					result = b.RotateLeft(x, count)
				case "rotate-right":
					result = b.RotateRight(x, count)
				}
				b.Return(result)
				code, _ := compilePPC(t, b, compile.Func{ABI: abi.C})
				for _, input := range []uint64{0, mask, uint64(1)<<(width-1) | 3} {
					var want uint64
					switch operation {
					case "left":
						if count < width {
							want = input << count & mask
						}
					case "right":
						if count < width {
							want = input >> count
						}
					case "arithmetic":
						signed := int64(input<<(64-width)) >> (64 - width)
						want = uint64(signed>>count) & mask
					case "rotate-left", "rotate-right":
						amount := count % width
						if operation == "rotate-right" {
							amount = (width - amount) % width
						}
						if amount == 0 {
							want = input
						} else {
							want = (input<<amount | input>>(width-amount)) & mask
						}
					}
					got, err := code.Call(uintptr(input))
					if err != nil || uint64(got) != want {
						t.Fatalf("width %d %s %d of %#x = %#x, %v; want %#x", width, operation, count, input, got, err, want)
					}
				}
			}
		}
	}
}

func TestPPC64ScopesSpillToWorkspaceOrFrame(t *testing.T) {
	for _, flavor := range []abi.Flavor{abi.C, abi.Go} {
		for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
			b := nyanjit.NewBuilder()
			x := b.Param()
			b.Enter()
			values := make([]nyanjit.Value, 32)
			for i := range values {
				values[i] = b.Mul(x, b.Const(uint64(i+2)))
			}
			b.Leave(values...)
			result := values[0]
			for _, value := range values[1:] {
				result = b.Add(result, value)
			}
			b.Return(result)
			budget := 0
			if stack == abi.StackPlatform {
				budget = 4096
			}
			code, report := compilePPC(t, b, compile.Func{ABI: flavor, Stack: compile.StackPolicy{Practice: stack, BytesAvailable: budget}})
			if stack == abi.StackNoUse && (report.WorkspaceBytes == 0 || report.StackBytes != 0) {
				t.Fatalf("workspace report = %+v", report)
			}
			if stack == abi.StackPlatform && (report.StackBytes == 0 || report.WorkspaceBytes != 0) {
				t.Fatalf("stack report = %+v", report)
			}
			for _, input := range []uint64{0, 7, ^uint64(0)} {
				if got, err := code.Call(uintptr(input)); err != nil || uint64(got) != input*560 {
					t.Fatalf("ABI %d stack %d input %#x = %#x, %v; want %#x", flavor, stack, input, got, err, input*560)
				}
			}
		}
	}
}

func TestPPC64LabelArgumentLoop(t *testing.T) {
	b := nyanjit.NewBuilder()
	n := b.Param()
	loop, done := b.NewLabel(), b.NewLabel()
	acc, left := b.Var(b.Const(0)), b.Var(n)
	b.Bind(loop)
	accumulator, remaining := b.Get(acc), b.Get(left)
	b.Branch(b.IsZero(remaining), done)
	b.Set(acc, b.Add(accumulator, remaining))
	b.Set(left, b.Sub(remaining, b.Const(1)))
	b.Jump(loop)
	b.Bind(done)
	b.Return(accumulator)
	code, report := compilePPC(t, b, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}})
	for _, input := range []uint64{0, 1, 100} {
		if got, err := code.Call(uintptr(input)); err != nil || uint64(got) != input*(input+1)/2 {
			t.Fatalf("sum(%d) = %d, %v", input, got, err)
		}
	}
	if !slices.Contains(report.ClobberedSpecialRegisters, "CR0") {
		t.Fatalf("comparison effects missing CR0: %+v", report)
	}
}

func TestPPC64NativeAndLittleEndianMemory(t *testing.T) {
	const value = uint64(0x8123456789abcdef)
	for _, native := range []bool{false, true} {
		b := nyanjit.NewBuilder()
		address := b.ParamNative()
		if native {
			b.Store64(address, b.Const(value))
			b.Return(b.Load64(address))
		} else {
			b.StoreBits(address, b.Const(value))
			b.Return(b.LoadBits(address, 64))
		}
		code, _ := compilePPC(t, b, compile.Func{ABI: abi.C})
		output := make([]byte, 16)
		got, err := code.Call(execution.Words(&output[0])...)
		if err != nil || uint64(got) != value {
			t.Fatalf("native %v memory result = %#x, %v", native, got, err)
		}
		want := make([]byte, 8)
		if native {
			binary.NativeEndian.PutUint64(want, value)
		} else {
			binary.LittleEndian.PutUint64(want, value)
		}
		if !bytes.Equal(output[:8], want) || !bytes.Equal(output[8:], make([]byte, 8)) {
			t.Fatalf("native %v memory = %x; want %x", native, output, want)
		}
	}
	b := nyanjit.NewBuilder()
	address := b.ParamNative()
	wide := b.ParamBits(128)
	b.StoreBits(address, wide)
	loaded := b.LoadBits(address, 128)
	b.Return(b.Xor(b.ExtractBits(loaded, 0, 64), b.ExtractBits(loaded, 64, 64)))
	code, _ := compilePPC(t, b, compile.Func{ABI: abi.C})
	output := make([]byte, 24)
	low, high := uint64(0x0123456789abcdef), uint64(0xfedcba9876543210)
	got, err := code.Call(execution.Words(&output[0], low, high)...)
	if err != nil || uint64(got) != low^high {
		t.Fatalf("wide memory result = %#x, %v", got, err)
	}
	want := make([]byte, 24)
	binary.LittleEndian.PutUint64(want, low)
	binary.LittleEndian.PutUint64(want[8:], high)
	if !bytes.Equal(output, want) {
		t.Fatalf("wide memory = %x, want %x", output, want)
	}
}

func TestPPC64LogicalPairOrderAndWideMultiply(t *testing.T) {
	for _, multiply := range []bool{false, true} {
		b := nyanjit.NewBuilder()
		x := b.ParamBits(128)
		if multiply {
			x = b.Mul(x, b.ParamBits(128))
		}
		b.Return(x)
		code, report := compilePPC(t, b, compile.Func{ABI: abi.C, Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}})
		low, high := uint64(0x1122334455667788), uint64(0x8877665544332211)
		args := []uintptr{uintptr(low), uintptr(high)}
		wantLow, wantHigh := low, high
		if multiply {
			yLow, yHigh := uint64(0x1020304050607080), uint64(0xfedcba9876543210)
			args = append(args, uintptr(yLow), uintptr(yHigh))
			productHigh, productLow := bits.Mul64(low, yLow)
			wantLow, wantHigh = productLow, productHigh+low*yHigh+high*yLow
		}
		gotLow, gotHigh, err := code.CallPair(args...)
		if err != nil || uint64(gotLow) != wantLow || uint64(gotHigh) != wantHigh {
			t.Fatalf("pair multiply %v = %#x:%#x, %v; want %#x:%#x", multiply, gotHigh, gotLow, err, wantHigh, wantLow)
		}
		if report.ResultLocation.Words[0].Register != "R3" {
			t.Fatalf("logical low result location = %+v", report.ResultLocation)
		}
	}
}

func TestPPC64LinkedSymbolsAreExecutableAndIndependent(t *testing.T) {
	b := nyanjit.NewBuilder()
	b.Return(b.Symbol("answer"))
	prepared, err := lowerTemplate(finishPPC(t, b), compile.TargetSpec{}, compile.Func{ABI: abi.C})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []uint64{42, 0x8123456789abcdef} {
		code, err := prepared.Bind(map[string]uint64{"answer": want})
		if err != nil {
			t.Fatal(err)
		}
		got, callErr := code.Call()
		if err := code.Close(); err != nil {
			t.Fatal(err)
		}
		if callErr != nil || uint64(got) != want {
			t.Fatalf("bound result = %#x, %v; want %#x", got, callErr, want)
		}
	}
}

func TestPPC64LargeImmediateAndConditionalBranch(t *testing.T) {
	const threshold = int64(0x123456789abcdef)
	b := nyanjit.NewBuilder()
	x := b.Param()
	yes, no := b.NewLabel(), b.NewLabel()
	b.Branch(b.CompareImmediate(x, ir.BranchBelow, threshold), yes)
	b.Jump(no)
	b.Bind(yes)
	b.Return(b.Const(1))
	b.Bind(no)
	b.Return(b.Const(0))
	code, _ := compilePPC(t, b, compile.Func{ABI: abi.C})
	for _, value := range []uint64{0, uint64(threshold), ^uint64(0)} {
		want := uint64(0)
		if value < uint64(threshold) {
			want = 1
		}
		if got, err := code.Call(uintptr(value)); err != nil || uint64(got) != want {
			t.Fatalf("large immediate compare %#x = %d, %v", value, got, err)
		}
	}

	memory, err := execution.Scratch(8)
	if err != nil {
		t.Fatal(err)
	}
	defer execution.FreeScratch(memory)
	b = nyanjit.NewBuilder()
	x = b.Param()
	far, work := b.NewLabel(), b.NewLabel()
	b.Branch(b.IsZero(x), far)
	b.Jump(work)
	b.Bind(work)
	address := b.ConstNative(uintptr(unsafe.Pointer(&memory[0])))
	for i := 0; i < 8200; i++ {
		b.Load8(address)
	}
	b.Return(b.Const(7))
	b.Bind(far)
	b.Return(b.Const(42))
	longCode, report := compilePPC(t, b, compile.Func{ABI: abi.C})
	if report.CodeBytes <= 32768 {
		t.Fatalf("long branch test generated only %d bytes", report.CodeBytes)
	}
	for _, value := range []uint64{0, 1} {
		want := uint64(7)
		if value == 0 {
			want = 42
		}
		if got, err := longCode.Call(uintptr(value)); err != nil || uint64(got) != want {
			t.Fatalf("long conditional input %d = %d, %v; want %d", value, got, err, want)
		}
	}
}

func TestPPC64IndexedAddressingAndSpecialReportSnapshots(t *testing.T) {
	memory, err := execution.Scratch(32)
	if err != nil {
		t.Fatal(err)
	}
	defer execution.FreeScratch(memory)
	for i := 0; i < 4; i++ {
		binary.NativeEndian.PutUint64(memory[i*8:], uint64(100+i))
	}
	for _, scaled := range []bool{false, true} {
		b := nyanjit.NewBuilder()
		base, index := b.ParamNative(), b.ParamNative()
		if scaled {
			index = index.MulConst(8)
		}
		b.Return(b.Load64(base.Add(index)))
		program := finishPPC(t, b)
		assembly, err := lowerListing(program, compile.TargetSpec{}, compile.Func{ABI: abi.C})
		if err != nil {
			t.Fatal(err)
		}
		if !scaled && !strings.Contains(assembly, "ldx") {
			t.Fatalf("indexed load did not fold: %s", assembly)
		}
		prepared, err := lowerTemplate(program, compile.TargetSpec{}, compile.Func{ABI: abi.C})
		if err != nil {
			t.Fatal(err)
		}
		mapped, err := prepared.Bind(nil)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 4; i++ {
			index := uint64(i)
			if !scaled {
				index *= 8
			}
			got, err := mapped.Call(uintptr(unsafe.Pointer(&memory[0])), uintptr(index))
			if err != nil || uint64(got) != uint64(100+i) {
				t.Fatalf("indexed load %d scaled %v = %d, %v", i, scaled, got, err)
			}
		}
		if err := mapped.Close(); err != nil {
			t.Fatal(err)
		}
	}
	b := nyanjit.NewBuilder()
	b.Return(b.ShiftRightArithmetic(b.Param(), 3))
	prepared, err := lowerTemplate(finishPPC(t, b), compile.TargetSpec{}, compile.Func{ABI: abi.C})
	if err != nil {
		t.Fatal(err)
	}
	report := prepared.Report()
	if !slices.Contains(report.ClobberedSpecialRegisters(), "XER") {
		t.Fatal("SRADI status modification is not reported")
	}
	report.ClobberedSpecialRegisters()[0] = "changed"
	if slices.Contains(prepared.Report().ClobberedSpecialRegisters(), "changed") {
		t.Fatal("report accessor aliases the template")
	}
}

func TestPPC64CallBoundaryAlignsStack(t *testing.T) {
	assembler := ppc64.NewByteAssembler()
	for _, ins := range []ppc64.Instruction{{Op: ppc64.Andi, A: 3, B: 1, Imm: 15}, {Op: ppc64.Blr}} {
		if err := assembler.Visit(ins); err != nil {
			t.Fatal(err)
		}
	}
	code, err := execution.NewCode(assembler.Bytes(), execution.CodeOptions{ABI: abi.C}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer code.Close()
	if got, err := code.Call(); err != nil || got != 0 {
		t.Fatalf("C entry SP modulo 16 = %d, %v", got, err)
	}
}
