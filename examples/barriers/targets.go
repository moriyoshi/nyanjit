package main

import (
	"fmt"

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

// policy is the part of a target Policy this example uses.
type policy interface {
	Barrier(machine.Barrier) machine.BarrierContract
	ParamRegister(int) (backend.Register, bool)
	ResultRegister() backend.Register
	ScratchRegister() backend.Register
}

// target assembles straight-line machine code with one target package.
// Generated functions use the C convention without a frame.
type target struct {
	arch   string
	policy policy
	// incoming gives the entry stack offset of a parameter that the ABI
	// passes in memory (386 cdecl).
	incoming func(index int) uint64
	assemble func(code []machine.Instruction, text bool) ([]byte, string, error)
}

const stack = abi.StackPlatform

var architectures = []string{"amd64", "386", "arm64", "ppc64le", "riscv64", "loong64"}

func newTarget(arch string) (*target, error) {
	switch arch {
	case "amd64":
		return &target{arch: arch, policy: amd64.NewPolicy(abi.C, stack), assemble: visitor(
			func(i machine.Instruction) int { return amd64.PhysicalSize(i, stack, 0) },
			func() assembler { return amd64.NewByteAssembler(stack, 0) },
			func() assembler { return amd64.NewTextAssembler(stack, 0) })}, nil
	case "386":
		return &target{arch: arch, policy: i386.NewPolicy(abi.C, stack),
			incoming: func(index int) uint64 { return uint64(4 + 4*index) },
			assemble: visitor(
				func(i machine.Instruction) int { return i386.PhysicalSize(i, stack, 0) },
				func() assembler { return i386.NewByteAssembler(stack) },
				func() assembler { return i386.NewTextAssembler(stack) })}, nil
	case "arm64":
		return &target{arch: arch, policy: arm64.NewPolicy(abi.C, stack), assemble: visitor(
			func(i machine.Instruction) int { return arm64.PhysicalSize(i, stack, 0) },
			func() assembler { return arm64.NewByteAssembler(stack, 0) },
			func() assembler { return arm64.NewTextAssembler(stack, 0) })}, nil
	case "riscv64":
		return &target{arch: arch, policy: riscv64.NewPolicy(abi.C, stack), assemble: visitor(
			func(i machine.Instruction) int { return riscv64.PhysicalSize(i, stack, 0) },
			func() assembler { return riscv64.NewByteAssembler(stack, 0) },
			func() assembler { return riscv64.NewTextAssembler(stack, 0) })}, nil
	case "loong64":
		return &target{arch: arch, policy: loong64.NewPolicy(abi.C, stack), assemble: visitor(
			func(i machine.Instruction) int { return loong64.PhysicalSize(i, stack, 0) },
			func() assembler { return loong64.NewByteAssembler(stack, 0) },
			func() assembler { return loong64.NewTextAssembler(stack, 0) })}, nil
	case "ppc64le":
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
		return &target{arch: arch, policy: p, assemble: expander(emitter)}, nil
	}
	return nil, fmt.Errorf("barriers: unsupported architecture %q", arch)
}

type assembler interface {
	machine.Assembler
	Err() error
}

// visitor assembles with machine.Visit, which dispatches OpBarrier to the
// target assembler's Barrier method.
func visitor(size func(machine.Instruction) int, bytes, text func() assembler) func([]machine.Instruction, bool) ([]byte, string, error) {
	return func(code []machine.Instruction, listing bool) ([]byte, string, error) {
		a := bytes()
		if listing {
			a = text()
		}
		pc := 0
		for _, ins := range code {
			a.SetPC(pc)
			if err := machine.Visit(ins, a); err != nil {
				return nil, "", err
			}
			pc += size(ins)
		}
		if err := a.Err(); err != nil {
			return nil, "", err
		}
		if listing {
			return nil, a.(interface{ Text() string }).Text(), nil
		}
		return a.(interface{ Bytes() []byte }).Bytes(), "", nil
	}
}

// expander assembles ppc64, which first expands each machine instruction
// into POWER instructions.
func expander(emitter *ppc64.Emitter) func([]machine.Instruction, bool) ([]byte, string, error) {
	return func(code []machine.Instruction, listing bool) ([]byte, string, error) {
		var a ppc64.Assembler = ppc64.NewByteAssembler()
		if listing {
			a = ppc64.NewTextAssembler()
		}
		pc := 0
		for _, ins := range code {
			sequence, err := emitter.Expand(ins, pc)
			if err != nil {
				return nil, "", err
			}
			for _, native := range sequence {
				if err := a.Visit(native); err != nil {
					return nil, "", err
				}
			}
			pc += 4 * len(sequence)
		}
		if t, ok := a.(*ppc64.TextAssembler); ok {
			return nil, t.Text(), nil
		}
		return a.(*ppc64.ByteAssembler).Bytes(), "", nil
	}
}
