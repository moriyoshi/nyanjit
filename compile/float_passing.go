package compile

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

// Float passing (Func.Floats and the Imm of OpCallC/OpCallGo) is a boundary
// property: the IR is untyped and its values are bit patterns. The
// optimized tier lowers an entry's body with the integer convention and
// converts at the boundary: a prologue moves the ABI's FP and integer
// argument registers into the integer layout the body expects, and every
// return moves the integer result into the FP result register (on 386, a C
// result into x87 ST(0)). Calls place marked arguments in FP registers
// directly (see the call lowering in allocateAndEmitMode).

// validateEntryFloats checks Func.Floats against program: marks only for
// entry parameters of 32 or 64 bits, and with a float result, every
// returned value of that width and no 128-bit result.
func validateEntryFloats(program *ir.Program, paramBits []uint8, floats abi.FloatPassing) error {
	if floats == 0 {
		return nil
	}
	if floats&abi.FloatResult32 != 0 && floats&abi.FloatResult64 != 0 {
		return fmt.Errorf("Func.Floats marks both float result widths: %w", ErrInvalidFunction)
	}
	count := 0
	for i := range 62 {
		if !floats.Argument(i) {
			continue
		}
		count++
		if i >= len(paramBits) {
			return fmt.Errorf("Func.Floats marks parameter %d of %d: %w", i, len(paramBits), ErrInvalidFunction)
		}
		if paramBits[i] != 32 && paramBits[i] != 64 {
			return fmt.Errorf("float parameter %d has %d bits: %w", i, paramBits[i], ErrInvalidFunction)
		}
	}
	if count > 8 {
		return fmt.Errorf("%d float parameters exceed 8: %w", count, ErrBackendLimit)
	}
	bits := floats.ResultBits()
	if bits == 0 {
		return nil
	}
	width := func(id int) uint8 {
		if id < 0 || id >= len(program.Instructions) {
			return 0
		}
		if w := program.Instructions[id].Bits; w != 0 {
			return w
		}
		return 64
	}
	if program.Control == nil {
		if len(program.ResultParts) != 0 || width(program.Result) != bits {
			return fmt.Errorf("a %d-bit float result needs a %d-bit return value: %w", bits, bits, ErrInvalidFunction)
		}
		return nil
	}
	for _, step := range program.Control.Steps {
		if step.IsLabel || step.Term != ir.TermReturn {
			continue
		}
		if len(step.ResultParts) != 0 || width(step.Result) != bits {
			return fmt.Errorf("a %d-bit float result needs %d-bit return values: %w", bits, bits, ErrInvalidFunction)
		}
	}
	return nil
}

// entryParamBits returns the widths of the OpParams in ops, in order.
func entryParamBits(ops []instruction) []uint8 {
	var bits []uint8
	for i := range ops {
		if ops[i].Op == opParam {
			bits = append(bits, instructionBits(ops[i]))
		}
	}
	return bits
}

// floatRegisterNamed returns the FP register an abi register name denotes
// on architecture: XMMn on amd64 and 386, Vn on arm64, Fn elsewhere.
func floatRegisterNamed(architecture, name string) (physReg, bool) {
	prefix := "F"
	switch architecture {
	case "amd64", "386":
		prefix = "XMM"
	case "arm64":
		prefix = "V"
	}
	number, ok := strings.CutPrefix(name, prefix)
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(number)
	if err != nil || n < 0 || n >= 32 {
		return 0, false
	}
	return backend.VectorRegister(uint8(n)), true
}

// floatCallEffect is cachedCallEffect for a call with float passing floats,
// mapping a malformed float passing to ErrBackendLimit as the argument
// limits it breaks.
func floatCallEffect(spec abi.CallSpec) (abi.CallEffect, error) {
	effect, err := cachedCallEffect(spec)
	if err != nil && spec.Floats != 0 && errors.Is(err, abi.ErrInvalidSpec) {
		return effect, fmt.Errorf("float passing %#x for %d arguments: %w", uint64(spec.Floats), spec.ArgumentCount, ErrBackendLimit)
	}
	return effect, err
}

// entryFloatEffect returns the entry convention with float passing floats
// as an outbound call describes it: an entry's float convention is the
// call convention of its flavor.
func entryFloatEffect(target machineTarget, floats abi.FloatPassing, argumentWords []int) (abi.CallEffect, error) {
	spec := abi.CallSpec{Architecture: target.Architecture(), Kind: abi.FunctionCall, ABI: target.ABIFlavor(), ArgumentCount: len(argumentWords), Floats: floats}
	if target.Architecture() == "386" {
		spec.ArgumentWords = argumentWords
	}
	return floatCallEffect(spec)
}

// floatEntryMoves returns the prologue that moves the entry's arguments
// from their float-passing ABI registers into the integer layout the body
// was lowered for, and the registers it writes. paramBits holds each
// parameter's width. 386 arguments stay in stack words.
func floatEntryMoves(target machineTarget, floats abi.FloatPassing, paramBits []uint8) ([]physicalInsn, error) {
	if floats&(abi.FloatResult32-1) == 0 || target.Architecture() == "386" {
		return nil, nil
	}
	for i := range 62 {
		if floats.Argument(i) && (i >= len(paramBits) || paramBits[i] != 32 && paramBits[i] != 64) {
			return nil, fmt.Errorf("float parameter %d is not a 32- or 64-bit parameter: %w", i, ErrInvalidFunction)
		}
	}
	words := make([]int, len(paramBits))
	for i := range words {
		words[i] = 1
	}
	effect, err := entryFloatEffect(target, floats, words)
	if err != nil {
		return nil, err
	}
	names := physicalRegistersByArchitecture[target.Architecture()]
	var moves []physicalInsn
	// Integer argument k moves to slot i ≥ k, so a descending walk writes
	// each destination after every move that reads it.
	for i := len(paramBits) - 1; i >= 0; i-- {
		dst, ok := target.ParamRegister(i)
		if !ok {
			return nil, ErrBackendLimit
		}
		name := effect.ArgumentRegisters[i]
		if floats.Argument(i) {
			src, ok := floatRegisterNamed(target.Architecture(), name)
			if !ok {
				return nil, ErrBackendLimit
			}
			moves = append(moves, physicalInsn{Op: machine.OpFloatMove, Bits: paramBits[i], A: dst, B: src})
			continue
		}
		src, ok := names[name]
		if !ok {
			return nil, ErrBackendLimit
		}
		if src != dst {
			moves = append(moves, physicalInsn{Op: pMove, A: dst, B: src})
		}
	}
	return moves, nil
}

// floatResultMoves returns the instructions that move an integer result in
// the target's result register (EAX:EDX on 386) to the FP result location
// of floats. A 386 C result goes through slot to x87 ST(0); a 386 Go result
// stays in the stack result slot.
func floatResultMoves(target machineTarget, floats abi.FloatPassing, slot int) []physicalInsn {
	bits := floats.ResultBits()
	if bits == 0 {
		return nil
	}
	if target.Architecture() == "386" {
		if target.ABIFlavor() != abi.C {
			return nil
		}
		xmm0 := backend.VectorRegister(0)
		move := physicalInsn{Op: machine.OpFloatMove, Bits: bits, A: xmm0, B: 0}
		if bits == 64 {
			move.HighB = 2
		}
		return []physicalInsn{move, {Op: machine.OpX87Result, Bits: bits, A: xmm0, Slot: slot}}
	}
	effect, err := entryFloatEffect(target, floats&^(abi.FloatResult32-1), nil)
	if err != nil {
		return nil
	}
	dst, ok := floatRegisterNamed(target.Architecture(), effect.ResultRegister)
	if !ok {
		return nil
	}
	return []physicalInsn{{Op: machine.OpFloatMove, Bits: bits, A: dst, B: target.ResultRegister()}}
}

// x87ResultSlots is the frame storage a 386 C float result needs.
func x87ResultSlots(target machineTarget, floats abi.FloatPassing) int {
	if target.Architecture() == "386" && target.ABIFlavor() == abi.C && floats.ResultBits() != 0 {
		return 2
	}
	return 0
}

// applyFloatReport rewrites an entry report's argument and result
// locations for its float passing.
func applyFloatReport(report *abi.EmissionReport, floats abi.FloatPassing) error {
	report.Floats = floats
	if floats == 0 {
		return nil
	}
	words := argumentLocationWords(report.ArgumentLocations)
	spec := abi.CallSpec{Architecture: report.Architecture, Kind: abi.FunctionCall, ABI: report.ABI, ArgumentCount: len(words), Floats: floats}
	if report.Architecture == "386" {
		spec.ArgumentWords = words
	}
	effect, err := floatCallEffect(spec)
	if err != nil {
		return err
	}
	if report.Architecture != "386" {
		report.ArgumentLocations = make([]abi.ValueLocation, len(effect.ArgumentRegisters))
		report.ArgumentRegisters = append([]string(nil), effect.ArgumentRegisters...)
		for i, name := range effect.ArgumentRegisters {
			report.ArgumentLocations[i] = abi.RegisterValue(name)
		}
	}
	if floats.ResultBits() != 0 && (report.Architecture != "386" || report.ABI == abi.C) {
		report.ResultLocation = effect.ResultLocation
		report.ResultRegister = abi.SingleRegister(effect.ResultLocation)
	}
	return nil
}
