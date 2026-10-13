package compile

import (
	"testing"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
)

func TestAMD64EmitterCachesDetachedCPUProfile(t *testing.T) {
	options := CompileOptions{Architecture: "amd64", CPU: abi.CPUProfile{Extensions: []string{"bmi2"}}}
	emitter := newAMD64MachineEmitter(options).(*physicalEmitter)
	i := physicalInsn{Op: machine.OpShift, Arithmetic: ir.OpRotateRight, Bits: 64, A: 3, B: 5, Imm: 7}
	size := emitter.size(i)
	if emitter.flagEffect(i) || emitter.flagFacts(i).Modified != 0 {
		t.Fatal("BMI2 RORX must preserve flags")
	}
	options.CPU.Extensions[0] = "invalid"
	if emitter.size(i) != size || emitter.flagEffect(i) || emitter.flagFacts(i).Modified != 0 {
		t.Fatal("emitter re-resolved a mutable caller profile during size/flag queries")
	}
	if n := testing.AllocsPerRun(100, func() { emitter.flagFacts(i); emitter.flagEffect(i) }); n != 0 {
		t.Fatalf("cached flag queries allocate %g times", n)
	}
}
