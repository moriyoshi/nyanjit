package abi

import (
	"encoding/binary"
	"strconv"
)

// PPC64CABI selects the representation and calling sequence of a C function.
// It is independent of the generated function's Go or C entry Flavor.
type PPC64CABI uint8

const (
	// PPC64CABIUnspecified requires explicit resolution on big-endian PPC64.
	// Go's executable ELF ABI alone does not describe an arbitrary C pointer.
	PPC64CABIUnspecified PPC64CABI = iota
	PPC64ELFv1
	PPC64ELFv2
)

// FunctionPointerKind describes whether a C callable address is executable
// code or a descriptor containing entry, TOC, and environment addresses.
type FunctionPointerKind uint8

const (
	FunctionPointerCode FunctionPointerKind = iota
	FunctionPointerDescriptor
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
	cABI         PPC64CABI
}

// NewPPC64Profile resolves a convention for ppc64 or ppc64le. Big-endian C
// profiles require an explicit ELF variant. Little-endian C defaults to ELFv2;
// Go profiles use ABIInternal and accept only an unspecified C variant.
func NewPPC64Profile(architecture string, flavor Flavor, convention PPC64CABI) (PPC64Profile, error) {
	if architecture != "ppc64" && architecture != "ppc64le" {
		return PPC64Profile{}, ErrUnsupported
	}
	if flavor != C && flavor != Go {
		return PPC64Profile{}, ErrUnsupportedABI
	}
	if flavor == Go {
		if convention != PPC64CABIUnspecified {
			return PPC64Profile{}, ErrInvalidSpec
		}
	} else {
		if architecture == "ppc64le" && convention == PPC64CABIUnspecified {
			convention = PPC64ELFv2
		}
		if convention != PPC64ELFv1 && convention != PPC64ELFv2 ||
			architecture == "ppc64le" && convention != PPC64ELFv2 {
			return PPC64Profile{}, ErrUnsupportedABI
		}
	}
	return PPC64Profile{architecture: architecture, flavor: flavor, cABI: convention}, nil
}

func (profile PPC64Profile) valid() bool {
	_, err := NewPPC64Profile(profile.architecture, profile.flavor, profile.cABI)
	return err == nil
}

// Architecture returns the Go target name.
func (profile PPC64Profile) Architecture() string { return profile.architecture }

// Flavor returns the entry or call convention selected by this profile.
func (profile PPC64Profile) Flavor() Flavor { return profile.flavor }

// CABI returns the resolved C ELF variant, or Unspecified for Go.
func (profile PPC64Profile) CABI() PPC64CABI { return profile.cABI }

// ByteOrder returns the target's native representation rather than the host's.
func (profile PPC64Profile) ByteOrder() binary.ByteOrder {
	if profile.architecture == "ppc64le" {
		return binary.LittleEndian
	}
	return binary.BigEndian
}

// FunctionPointer returns the callable-pointer representation.
func (profile PPC64Profile) FunctionPointer() FunctionPointerKind {
	if profile.flavor == C && profile.cABI == PPC64ELFv1 {
		return FunctionPointerDescriptor
	}
	return FunctionPointerCode
}

// Frame returns the linkage layout used when a frame is required.
func (profile PPC64Profile) Frame() PPC64FrameLayout {
	if profile.flavor == Go {
		return PPC64FrameLayout{Alignment: 8, FixedBytes: 32, ReturnAddressOffset: 0,
			ConditionOffset: 8, TOCOffset: 24, ParameterOffset: 32}
	}
	fixed := 32
	if profile.cABI == PPC64ELFv1 {
		fixed = 48
	}
	return PPC64FrameLayout{Alignment: 16, FixedBytes: fixed, ReturnAddressOffset: 16,
		ConditionOffset: 8, TOCOffset: 24, ParameterOffset: fixed, BackChain: true}
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

// ClassifyResult maps logical low/high words to ABI registers. A big-endian C
// 128-bit integer returns its high word in R3 and low word in R4. Go's present
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
		low, high := "R3", "R4"
		if profile.architecture == "ppc64" {
			low, high = high, low
		}
		return ValueLocation{Words: []WordLocation{{Storage: Register, Register: low}, {Storage: Register, Register: high}}}, nil
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
		PPC64CABI: profile.cABI, FunctionPointer: profile.FunctionPointer(),
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
