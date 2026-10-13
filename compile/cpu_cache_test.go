package compile

import (
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestAMD64EmitterCachesDetachedCPUProfile(t *testing.T) {
	// A fresh, unshared target: the test mutates the options afterwards.
	options := uncheckedTarget(TargetSpec{Architecture: "amd64", CPU: abi.CPUProfile{Extensions: []string{"bmi2"}}}).options(Func{})
	emitter := new(physicalEmitter)
	initAMD64MachineEmitter(emitter, options)
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
