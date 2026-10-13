package compile

import (
	"fmt"
	"testing"

	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

func TestSpillFootprintsUseNativeWords(t *testing.T) {
	for _, arch := range []string{"386", "amd64", "arm64", "riscv64", "ppc64le", "loong64"} {
		word := newMachineTarget(mustTarget(t, TargetSpec{Architecture: arch}).options(Func{})).RegisterBits()
		for _, op := range []machine.Opcode{machine.OpVectorSpill, machine.OpVectorReload, machine.OpSaveCVector, machine.OpRestoreCVector, machine.OpSaveCFloat, machine.OpRestoreCFloat} {
			bits := 128
			if op == machine.OpSaveCFloat || op == machine.OpRestoreCFloat {
				bits = 64
			}
			if got := referencedSpillSlots([]machine.Instruction{{Op: op, Slot: 7}}, word); got != 7+bits/int(word) {
				t.Errorf("%s op%d: end slot=%d want%d", arch, op, got, 7+bits/int(word))
			}
		}
	}
}

// Build explicit target-width addresses so this also tests 386 on a 64-bit host.
func vectorSpillFrameProgram(word uint8, live int, call bool) *ir.Program {
	p := &ir.Program{Arity: 2, Instructions: []ir.Instruction{{Op: ir.OpParam, Bits: word}, {Op: ir.OpParam, Bits: word}}}
	emit := func(i ir.Instruction) int { p.Instructions = append(p.Instructions, i); return len(p.Instructions) - 1 }
	address := func(base, offset int) int {
		c := emit(ir.Instruction{Op: ir.OpConst, Bits: word, Imm: uint64(offset)})
		return emit(ir.Instruction{Op: ir.OpAdd, Bits: word, A: base, B: c})
	}
	values := make([]int, live)
	for k := range values {
		values[k] = emit(ir.Instruction{Op: ir.OpVLoad, Type: ir.Vec(4, 32), Bits: 32, A: address(0, 16*k)})
	}
	result := -1
	if call {
		target := emit(ir.Instruction{Op: ir.OpConst, Bits: word, Imm: 0x1000})
		result = emit(ir.Instruction{Op: ir.OpCallC, Bits: 64, Args: []int{target}})
	}
	for k, v := range values {
		emit(ir.Instruction{Op: ir.OpVStore, Type: ir.Vec(4, 32), Bits: 32, A: address(1, 16*k), B: v})
	}
	if result < 0 {
		result = emit(ir.Instruction{Op: ir.OpConst, Bits: word})
	}
	p.Result = result
	return p
}

func TestVectorSpillsFitStorage(t *testing.T) {
	for _, arch := range []string{"386", "amd64", "arm64", "riscv64", "ppc64le", "loong64"} {
		for _, stack := range []abi.StackPractice{abi.StackNoUse, abi.StackPlatform} {
			for _, live := range []int{6, 7, 8, 9, 24} {
				for _, call := range []bool{false, true} {
					if call && stack == abi.StackNoUse {
						continue
					}
					t.Run(fmt.Sprintf("%s/stack%d/live%d/call%v", arch, stack, live, call), func(t *testing.T) {
						spec := TargetSpec{Architecture: arch}
						if arch == "loong64" {
							spec.CPU = abi.CPUProfile{Extensions: []string{"lsx"}}
						}
						f := Func{ABI: abi.C, Stack: StackPolicy{Practice: stack}}
						if stack == abi.StackPlatform {
							f.Stack.BytesAvailable = 65536
						}
						o := mustTarget(t, spec).options(f)
						target := newMachineTarget(o)
						word := int(target.RegisterBits() / 8)
						input, err := inputFromProgram(vectorSpillFrameProgram(target.RegisterBits(), live, call), o)
						if err != nil {
							t.Fatal(err)
						}
						selected, err := selectProgramResultParts(input.code, input.leaves, input.arity, input.result, input.resultParts, target)
						if err != nil {
							t.Fatal(err)
						}
						emitter := newMachineEmitter(o)
						physical, workspace, _, err := allocateAndEmit(selected, target, emitter)
						if err != nil {
							t.Fatal(err)
						}
						for _, located := range physical.insns {
							i := located.ins
							if i.Op != machine.OpVectorSpill && i.Op != machine.OpVectorReload {
								continue
							}
							end, limit := i.Slot*word+16, workspace
							if stack == abi.StackPlatform {
								limit = physical.stackBytes
								switch arch {
								case "386":
									end += emitter.(*physicalEmitter).callAreaBytes
									limit -= 16 // four saved GPRs
								case "amd64":
									end += emitter.(*physicalEmitter).vectorHomes * word
								case "arm64":
									end += (1 + emitter.(*physicalEmitter).vectorHomes) * word
								case "loong64":
									end += (1 + loong64GoHomes(o)) * word
								}
							}
							if end > limit {
								t.Fatalf("vector slot %d ends at%d beyond storage%d", i.Slot, end, limit)
							}
						}
						// Also assemble to check that byte and sizing plans agree.
						if _, err := assembleProgram(physical, newMachineAssembler(o, emitNative)); err != nil {
							t.Fatal(err)
						}
					})
				}
			}
		}
	}
}
