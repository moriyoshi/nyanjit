package main

import (
	"fmt"
	"strings"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/backend/target/amd64"
	"github.com/moriyoshi/nyanjit/backend/target/arm64"
	"github.com/moriyoshi/nyanjit/backend/target/i386"
	"github.com/moriyoshi/nyanjit/backend/target/loong64"
	"github.com/moriyoshi/nyanjit/backend/target/ppc64"
	"github.com/moriyoshi/nyanjit/backend/target/riscv64"
)

// policy is the part of a target Policy this front end uses.
type policy interface {
	RegisterBits() uint8
	Registers() backend.RegisterSet
	ScratchRegister() backend.Register
	ParamRegister(int) (backend.Register, bool)
	ResultRegister() backend.Register
	RegisterName(backend.Register) string
	BranchFits(ins machine.Instruction, sourcePC, targetPC, size int) bool
}

// assembler is a target's byte or text assembler for one function.
type assembler interface {
	SetPC(int)
	Emit(machine.Instruction) error
	Output() (code []byte, text string, err error)
}

// target gathers what the code generator needs from one target package.
// Every generated function uses the C calling convention and a platform
// stack frame, so execution.Code can call it through the foreign boundary.
type target struct {
	arch       string
	policy     policy
	size       func(machine.Instruction) int
	frameBytes func(slots int) int
	// incoming gives the entry stack offset of a parameter that the ABI
	// passes in memory (386 cdecl). Register parameters never use it.
	incoming     func(index int) int
	newAssembler func(text bool) assembler
}

const stack = abi.StackPlatform

func newTarget(arch string) (*target, error) {
	switch arch {
	case "amd64":
		return &target{arch: arch, policy: amd64.NewPolicy(abi.C, stack),
			size:       func(i machine.Instruction) int { return amd64.PhysicalSize(i, stack, 0) },
			frameBytes: func(slots int) int { return amd64.FrameBytes(slots, false) },
			newAssembler: func(text bool) assembler {
				if text {
					return &visitor{text: true, a: amd64.NewTextAssembler(stack, 0)}
				}
				return &visitor{a: amd64.NewByteAssembler(stack, 0)}
			}}, nil
	case "arm64":
		return &target{arch: arch, policy: arm64.NewPolicy(abi.C, stack),
			size:       func(i machine.Instruction) int { return arm64.PhysicalSize(i, stack, 0) },
			frameBytes: func(slots int) int { return arm64.FrameBytes(slots, false) },
			newAssembler: func(text bool) assembler {
				if text {
					return &visitor{text: true, a: arm64.NewTextAssembler(stack, 0)}
				}
				return &visitor{a: arm64.NewByteAssembler(stack, 0)}
			}}, nil
	case "386":
		return &target{arch: arch, policy: i386.NewPolicy(abi.C, stack),
			size:       func(i machine.Instruction) int { return i386.PhysicalSize(i, stack, 0) },
			frameBytes: func(slots int) int { return i386.FrameBytes(slots, false) },
			// cdecl: the return address is at 0(SP) and arguments follow.
			incoming: func(index int) int { return 4 + 4*index },
			newAssembler: func(text bool) assembler {
				if text {
					return &visitor{text: true, a: i386.NewTextAssembler(stack)}
				}
				return &visitor{a: i386.NewByteAssembler(stack)}
			}}, nil
	case "riscv64":
		return &target{arch: arch, policy: riscv64.NewPolicy(abi.C, stack),
			size:       func(i machine.Instruction) int { return riscv64.PhysicalSize(i, stack, 0) },
			frameBytes: func(slots int) int { return riscv64.FrameBytes(slots, false) },
			newAssembler: func(text bool) assembler {
				if text {
					return &visitor{text: true, a: riscv64.NewTextAssembler(stack, 0)}
				}
				return &visitor{a: riscv64.NewByteAssembler(stack, 0)}
			}}, nil
	case "loong64":
		return &target{arch: arch, policy: loong64.NewPolicy(abi.C, stack),
			size:       func(i machine.Instruction) int { return loong64.PhysicalSize(i, stack, 0) },
			frameBytes: func(slots int) int { return loong64.FrameBytes(slots, false) },
			newAssembler: func(text bool) assembler {
				if text {
					return &visitor{text: true, a: loong64.NewTextAssembler(stack, 0)}
				}
				return &visitor{a: loong64.NewByteAssembler(stack, 0)}
			}}, nil
	case "ppc64le":
		// ppc64 expands each machine instruction into POWER instructions
		// itself, then encodes or prints those.
		profile, err := abi.NewPPC64Profile(arch, abi.C)
		if err != nil {
			return nil, err
		}
		p, err := ppc64.NewPolicy(profile, stack)
		if err != nil {
			return nil, err
		}
		emitter, err := ppc64.NewEmitter(profile, stack)
		if err != nil {
			return nil, err
		}
		return &target{arch: arch, policy: p, size: emitter.Size,
			frameBytes: func(slots int) int { return emitter.FrameBytes(slots, false) },
			newAssembler: func(text bool) assembler {
				if text {
					return &expander{emitter: emitter, a: ppc64.NewTextAssembler()}
				}
				return &expander{emitter: emitter, a: ppc64.NewByteAssembler()}
			}}, nil
	}
	return nil, fmt.Errorf("stackvm: unsupported architecture %q", arch)
}

var architectures = []string{"amd64", "arm64", "386", "riscv64", "loong64", "ppc64le"}

// visitor drives a machine.Assembler with machine.Visit.
type visitor struct {
	text bool
	a    interface {
		machine.Assembler
		Err() error
	}
}

func (v *visitor) SetPC(pc int) { v.a.SetPC(pc) }
func (v *visitor) Emit(ins machine.Instruction) error {
	if err := machine.Visit(ins, v.a); err != nil {
		return err
	}
	return v.a.Err()
}
func (v *visitor) Output() ([]byte, string, error) {
	if v.text {
		return nil, v.a.(interface{ Text() string }).Text(), v.a.Err()
	}
	return v.a.(interface{ Bytes() []byte }).Bytes(), "", v.a.Err()
}

// expander drives the ppc64 emitter and a ppc64.Assembler.
type expander struct {
	emitter *ppc64.Emitter
	a       ppc64.Assembler
	pc      int
}

func (e *expander) SetPC(pc int) { e.pc = pc }
func (e *expander) Emit(ins machine.Instruction) error {
	sequence, err := e.emitter.Expand(ins, e.pc)
	if err != nil {
		return err
	}
	for _, native := range sequence {
		if err := e.a.Visit(native); err != nil {
			return err
		}
	}
	return nil
}
func (e *expander) Output() ([]byte, string, error) {
	switch a := e.a.(type) {
	case *ppc64.ByteAssembler:
		return a.Bytes(), "", nil
	case *ppc64.TextAssembler:
		return nil, a.Text(), nil
	}
	return nil, "", fmt.Errorf("stackvm: assembler has no output")
}

// trimListing drops the trailing newline the text assemblers add.
func trimListing(text string) string { return strings.TrimRight(text, "\n") }
