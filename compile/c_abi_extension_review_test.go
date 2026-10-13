package compile

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

func TestCArgumentExtensionAcrossArchitectures(t *testing.T) {
	for _, arch := range []string{"loong64", "riscv64", "amd64", "arm64"} {
		for _, managed := range []bool{false, true} {
			for _, bits := range []uint8{32, 64} {
				p := machineProgram{Values: []machineValue{{Bits: 64, Words: 1, Constant: true, Imm: 12345}, {Bits: bits, Words: 1, Param: 0}, {Bits: 64, Words: 1}}, Instructions: []machineInstruction{{Op: mCallC, Dst: 2, Args: []int{0, 1}}}}
				context := ContextNative
				if managed {
					context = ContextGoManaged
				}
				opts := mustTarget(t, TargetSpec{Architecture: arch}).options(Func{Context: context, Stack: StackPolicy{Practice: abi.StackPlatform, BytesAvailable: 4096}})
				if err := bridgeManagedCCalls(&p, opts, emitText); err != nil {
					t.Fatal(err)
				}
				extension := arch == "loong64" || arch == "riscv64"
				extension = extension && bits == 32
				found := 0
				for _, i := range p.Instructions {
					if i.Op == mSignExtend {
						found++
						if i.Imm != 32 || i.A != 1 || p.Values[i.Dst].Bits != 64 {
							t.Fatalf("bad extension %+v", i)
						}
					}
				}
				if (found == 1) != extension {
					t.Fatalf("%s managed%v bits%d extensions%d want%v", arch, managed, bits, found, extension)
				}
				call := p.Instructions[len(p.Instructions)-1]
				arg := call.Args[len(call.Args)-1]
				if extension && arg == 1 || !extension && arg != 1 {
					t.Fatalf("%s call bypassed width normalization %+v", arch, call)
				}
				if managed && call.Op != mCallGo || !managed && call.Op != mCallC {
					t.Fatal("wrong transition")
				}
			}
		}
	}
}
