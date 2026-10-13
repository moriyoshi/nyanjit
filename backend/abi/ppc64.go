package abi

import (
	"encoding/binary"
	"strconv"
)

// PPC64FrameLayout describes fixed fields relative to the appropriate stack
// pointer. C LR saves belong to the caller's linkage area; Go saves LR in the
// generated function's own frame. Outgoing arguments and spills are additional.
type PPC64FrameLayout struct {
	Alignment, FixedBytes                int
	ReturnAddressOffset, ConditionOffset int
	TOCOffset, ParameterOffset           int
	BackChain                            bool
}

// PPC64Profile is a resolved entry/call convention. It is portable: creating
// or querying a profile never depends on the executing host architecture.
type PPC64Profile struct {
	architecture string
	flavor       Flavor
}

// NewPPC64Profile resolves a convention for ppc64le. C profiles use ELFv2;
// Go profiles use ABIInternal. Big-endian ppc64 is not supported.
func NewPPC64Profile(architecture string, flavor Flavor) (PPC64Profile, error) {
	if architecture != "ppc64le" {
		return PPC64Profile{}, ErrUnsupported
	}
	if flavor != C && flavor != Go {
		return PPC64Profile{}, ErrUnsupportedABI
	}
	return PPC64Profile{architecture: architecture, flavor: flavor}, nil
}

func (profile PPC64Profile) valid() bool {
	_, err := NewPPC64Profile(profile.architecture, profile.flavor)
	return err == nil
}

// Architecture returns the Go target name.
func (profile PPC64Profile) Architecture() string { return profile.architecture }

// Flavor returns the entry or call convention selected by this profile.
func (profile PPC64Profile) Flavor() Flavor { return profile.flavor }

// ByteOrder returns the target's native representation rather than the host's.
func (profile PPC64Profile) ByteOrder() binary.ByteOrder { return binary.LittleEndian }

// Frame returns the linkage layout used when a frame is required.
func (profile PPC64Profile) Frame() PPC64FrameLayout {
	if profile.flavor == Go {
		return PPC64FrameLayout{Alignment: 8, FixedBytes: 32, ReturnAddressOffset: 0,
			ConditionOffset: 8, TOCOffset: 24, ParameterOffset: 32}
	}
	return PPC64FrameLayout{Alignment: 16, FixedBytes: 32, ReturnAddressOffset: 16,
		ConditionOffset: 8, TOCOffset: 24, ParameterOffset: 32, BackChain: true}
}

func (profile PPC64Profile) argumentRegisters() []string {
	registers := []string{"R3", "R4", "R5", "R6", "R7", "R8", "R9", "R10"}
	if profile.flavor == Go {
		registers = append(registers, "R14", "R15", "R16", "R17")
	}
	return registers
}

// ClassifyEntry assigns scalar uint64 argument words to entry registers.
// The portable Go profile covers all twelve ABIInternal integer registers;
// execution boundaries may impose a smaller supported arity.
func (profile PPC64Profile) ClassifyEntry(arity int) (EntryLayout, error) {
	if !profile.valid() {
		return EntryLayout{}, ErrInvalidSpec
	}
	registers := profile.argumentRegisters()
	if arity < 0 || arity > len(registers) {
		return EntryLayout{}, ErrBackendLimit
	}
	words := make([]int, arity)
	for i := range words {
		words[i] = 1
	}
	return ClassifyEntry(profile.architecture, profile.flavor, words, len(registers),
		func(index int) string { return registers[index] }, "R3")
}

// ClassifyResult maps logical low/high words to ABI registers. A C 128-bit
// integer returns its low word in R3 and high word in R4. Go's present
// nyanjit interface supports scalar results only.
func (profile PPC64Profile) ClassifyResult(bits uint8) (ValueLocation, error) {
	if !profile.valid() {
		return ValueLocation{}, ErrInvalidSpec
	}
	switch bits {
	case 8, 16, 32, 64:
		return registerValue("R3"), nil
	case 128:
		if profile.flavor != C {
			return ValueLocation{}, ErrBackendLimit
		}
		return ValueLocation{Words: []WordLocation{{Storage: Register, Register: "R3"}, {Storage: Register, Register: "R4"}}}, nil
	default:
		return ValueLocation{}, ErrInvalidSpec
	}
}

// PreflightCall reports conservative callee effects without allocating a frame
// or scheduling preservation. System calls describe the raw Linux sc boundary.
func (profile PPC64Profile) PreflightCall(kind CallKind, arity int) (CallEffect, error) {
	if !profile.valid() || kind != FunctionCall && kind != SystemCall {
		return CallEffect{}, ErrInvalidSpec
	}
	if kind == SystemCall && profile.flavor != C {
		return CallEffect{}, ErrUnsupportedABI
	}
	registers := profile.argumentRegisters()
	limit := len(registers)
	if kind == SystemCall {
		limit = 6
	}
	if arity < 0 || arity > limit {
		return CallEffect{}, ErrBackendLimit
	}
	effect := CallEffect{Architecture: profile.architecture, Kind: kind, ABI: profile.flavor,
		ArgumentRegisters: append([]string(nil), registers[:arity]...), ResultRegister: "R3",
		ResultLocation: registerValue("R3"), FlagsClobbered: true,
		MayReadMemory: true, MayWriteMemory: true, Precision: EffectConservative}
	effect.ArgumentLocations = make([]ValueLocation, arity)
	for i, register := range effect.ArgumentRegisters {
		effect.ArgumentLocations[i] = registerValue(register)
	}
	if kind == SystemCall {
		effect.NumberRegister = "R0"
		effect.ClobberedRegisters = []string{"R0", "R3", "R4", "R5", "R6", "R7", "R8", "R9", "R10", "R11", "R12"}
		effect.ClobberedSpecialRegisters = []string{"CR0", "XER"}
		effect.Precision = EffectPlatformGuaranteed
		return effect, nil
	}
	effect.RequiresCallStack, effect.StackAlignment = true, profile.Frame().Alignment
	effect.ClobberedRegisters = []string{"R0", "R2", "R3", "R4", "R5", "R6", "R7", "R8", "R9", "R10", "R11", "R12"}
	effect.ClobberedSpecialRegisters = []string{"LR", "CTR", "CR0", "CR1", "CR5", "CR6", "CR7", "XER", "FPSCR"}
	fpLimit, vectorLimit := 14, 20
	if profile.flavor == Go {
		// R0 is restored to zero at the boundary. R2 is conservatively
		// included to cover Go calls through dynamically linked entries.
		effect.ClobberedRegisters = effect.ClobberedRegisters[1:]
		for reg := 14; reg < 30; reg++ {
			effect.ClobberedRegisters = append(effect.ClobberedRegisters, "R"+strconv.Itoa(reg))
		}
		effect.ClobberedRegisters = append(effect.ClobberedRegisters, "R31")
		effect.ClobberedSpecialRegisters = []string{"LR", "CTR", "CR0", "CR1", "CR2", "CR3", "CR4", "CR5", "CR6", "CR7", "XER", "FPSCR"}
		fpLimit, vectorLimit = 32, 32
	}
	for reg := 0; reg < fpLimit; reg++ {
		effect.ClobberedFPRegisters = append(effect.ClobberedFPRegisters, "F"+strconv.Itoa(reg))
	}
	for reg := 0; reg < vectorLimit; reg++ {
		effect.ClobberedFPRegisters = append(effect.ClobberedFPRegisters, "V"+strconv.Itoa(reg))
	}
	return effect, nil
}
