//go:build linux && ppc64le

package compile_test

import (
	"strings"
	"syscall"
	"testing"
	"unsafe"

	"github.com/moriyoshi/nyanjit"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/compile"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestPPC64VariableShiftNativeForms(t *testing.T) {
	for _, width := range []uint8{8, 16, 32, 64} {
		for _, op := range []ir.Opcode{ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy} {
			b := nyanjit.NewBuilder()
			value, count := b.ParamBits(width), b.Param()
			b.Return(variableShift(b, op, value, count))
			program := finishPPC(t, b)
			listing, err := lowerListing(program, compile.TargetSpec{}, compile.Func{})
			if err != nil {
				t.Fatal(err)
			}
			mnemonic := "sld"
			switch op {
			case ir.OpShiftRightBy:
				mnemonic = "srd"
			case ir.OpShiftRightArithmeticBy:
				mnemonic = "srad"
			case ir.OpRotateLeftBy, ir.OpRotateRightBy:
				mnemonic = "rldcl"
			}
			if width == 32 {
				switch mnemonic {
				case "sld":
					mnemonic = "slw"
				case "srd":
					mnemonic = "srw"
				case "srad":
					mnemonic = "sraw"
				}
			}
			if width < 64 && (op == ir.OpRotateLeftBy || op == ir.OpRotateRightBy) {
				mnemonic = "rlwnm"
			}
			if strings.Count(listing, ": "+mnemonic+" ") != 1 {
				t.Fatalf("width%d opcode%d missing native %s:\n%s", width, op, mnemonic, listing)
			}
			if !strings.Contains(listing, "cmpldi ") && op != ir.OpRotateLeftBy && op != ir.OpRotateRightBy {
				t.Fatalf("count saturation guard missing:\n%s", listing)
			}
			if len(strings.Split(strings.TrimSpace(listing), "\n")) > 14 {
				t.Fatalf("variable shift expanded into a barrel network:\n%s", listing)
			}
		}
	}
}

func TestPPC64VariableShiftAcrossSpillsScopesAndLabelArguments(t *testing.T) {
	memory, err := syscall.Mmap(-1, 0, 4096, syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_PRIVATE|syscall.MAP_ANON)
	if err != nil {
		t.Fatal(err)
	}
	defer syscall.Munmap(memory)
	address := uint64(uintptr(unsafe.Pointer(&memory[0])))
	for _, op := range []ir.Opcode{ir.OpShiftLeftBy, ir.OpShiftRightBy, ir.OpShiftRightArithmeticBy, ir.OpRotateLeftBy, ir.OpRotateRightBy} {
		b := nyanjit.NewBuilder()
		input, count := b.Param(), b.Param()
		home := b.Param()
		b.Enter()
		values := make([]nyanjit.Value, 20)
		for i := range values {
			values[i] = b.Add(input, b.Const(uint64(i+1)))
			b.Store64(home, values[i])
		}
		result := variableShift(b, op, input, count)
		for _, v := range values {
			result = b.Xor(result, v)
		}
		b.Leave(result)
		done := b.NewLabel()
		// result crosses the edge by dominance.
		b.Jump(done)
		b.Bind(done)
		b.Return(result)
		program := finishPPC(t, b)
		template, err := lowerTemplate(program, compile.TargetSpec{}, compile.Func{Stack: compile.StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 65536}})
		if err != nil {
			t.Fatal(err)
		}
		if template.Report().SpillStorage() != abi.SpillStack || template.Report().StackBytes() < 256 {
			t.Fatalf("test did not cause register pressure: %+v", template.Report().Snapshot())
		}
		code, err := template.Bind(nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, count := range []uint64{0, 1, 31, 63, 64, 128, 1 << 63, ^uint64(0)} {
			input := uint64(0x8123456789abcdef)
			want := variableShiftWant(op, 64, input, count)
			for i := range values {
				want ^= input + uint64(i+1)
			}
			got, err := code.Call(uintptr(input), uintptr(count), uintptr(address))
			if err != nil || uint64(got) != want {
				t.Fatalf("opcode%d count%#x got%#x,%v; want%#x", op, count, got, err, want)
			}
		}
		code.Close()
	}
}
