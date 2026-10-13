package execution

import (
	"runtime"
	"strconv"
	"sync"
	"unsafe"

	"github.com/moriyoshi/nyanjit/backend/abi"
)

// floatCall is the pointer-free block a float-passing trampoline reads and
// writes: the target, the integer argument registers in the convention's
// order (386: the cdecl stack words), the FP argument registers in the
// target's register representation, and the results. Bits is the width of
// an x87 ST(0) result the 386 trampolines pop, or zero.
type floatCall struct {
	target   uintptr
	gp       ArgumentWords
	fp       [maxFloatArguments]uint64
	bits     uintptr
	lo, hi   uintptr
	fpResult uint64
}

// maxFloatArguments is the number of FP argument registers an entry uses.
const maxFloatArguments = 8

// The trampolines retain the block's address across CALL, so a local block
// would escape on every invocation.
var floatCallBlocks = sync.Pool{New: func() any { return new(floatCall) }}

// floatPlan routes an entry's argument words for its float passing. Only
// entries whose float passing changes a register get one: on 386 that is a C
// entry with a float result, which returns it in x87 ST(0).
type floatPlan struct {
	result uint8 // the float result width, or zero
	x87    bool  // 386: the result is popped from ST(0)
	// Per argument: the integer register index, or the FP register index for
	// a marked argument with fp set, and the marked argument's width.
	index [maxArguments]int8
	fp    [maxArguments]bool
	bits  [maxArguments]uint8
}

// validFloatPassing checks floats against an entry's arity and parameter
// widths: marks only for existing parameters of 32 or 64 bits, one result
// width and at most eight float parameters.
func validFloatPassing(floats abi.FloatPassing, arity int, paramBits []uint8) bool {
	if floats&abi.FloatResult32 != 0 && floats&abi.FloatResult64 != 0 {
		return false
	}
	count := 0
	for i := range 62 {
		if !floats.Argument(i) {
			continue
		}
		if i >= arity || paramBits != nil && paramBits[i] != 32 && paramBits[i] != 64 {
			return false
		}
		count++
	}
	return count <= maxFloatArguments
}

// newFloatPlan returns the routing for options, nil when the float passing
// changes no register, or ErrUnsupported where the host has no convention.
func newFloatPlan(options CodeOptions) (*floatPlan, error) {
	floats := options.Floats
	if floats == 0 {
		return nil, nil
	}
	plan := &floatPlan{result: floats.ResultBits()}
	if runtime.GOARCH == "386" {
		// Arguments and Go ABI0 results stay in stack words.
		if options.ABI != abi.C || plan.result == 0 {
			return nil, nil
		}
		plan.x87 = true
		return plan, nil
	}
	spec := abi.CallSpec{Architecture: runtime.GOARCH, Kind: abi.FunctionCall, ABI: options.ABI, ArgumentCount: options.Arity}
	integer, err := abi.PreflightCall(spec)
	if err != nil {
		return nil, ErrUnsupported
	}
	spec.Floats = floats
	routed, err := abi.PreflightCall(spec)
	if err != nil || len(routed.ArgumentRegisters) != options.Arity {
		return nil, ErrInvalidCodeOptions
	}
	next := int8(0)
	for i, name := range routed.ArgumentRegisters {
		plan.bits[i] = 64
		if options.ParamBits != nil && options.ParamBits[i] == 32 {
			plan.bits[i] = 32
		}
		if floats.Argument(i) {
			plan.index[i], plan.fp[i] = next, true
			next++
			continue
		}
		plan.index[i] = -1
		for n, register := range integer.ArgumentRegisters {
			if register == name {
				plan.index[i] = int8(n)
			}
		}
		if plan.index[i] < 0 {
			return nil, ErrInvalidCodeOptions
		}
	}
	return plan, nil
}

// fill places args in block.
func (p *floatPlan) fill(block *floatCall, args []uintptr) {
	if p.x87 {
		copy(block.gp[:], args)
		block.bits = uintptr(p.result)
		return
	}
	for i, word := range args {
		if p.fp[i] {
			block.fp[p.index[i]] = floatArgument(p.bits[i], uint64(word))
		} else {
			block.gp[p.index[i]] = word
		}
	}
}

// results returns the result words of a finished call.
func (p *floatPlan) results(block *floatCall) (lo, hi uintptr) {
	if p.result == 0 {
		return block.lo, block.hi
	}
	bits := block.fpResult
	if strconv.IntSize == 32 {
		return uintptr(uint32(bits)), uintptr(bits >> 32)
	}
	if p.result == 32 {
		if runtime.GOARCH == "ppc64le" {
			return uintptr(narrowDouble(bits)), 0
		}
		return uintptr(uint32(bits)), 0
	}
	return uintptr(bits), 0
}

// floatArgument converts a binary32 or binary64 bit pattern to the target's
// FP register representation: riscv64 NaN-boxes binary32, ppc64le holds it
// in double format, and the other targets in the low 32 bits.
func floatArgument(bits uint8, x uint64) uint64 {
	if bits == 64 {
		return x
	}
	x = uint64(uint32(x))
	switch runtime.GOARCH {
	case "riscv64":
		return x | 0xffffffff<<32
	case "ppc64le":
		return widenSingle(uint32(x))
	}
	return x
}

// widenSingle returns the double-format representation of binary32 x, as
// lfs loads it: exact, with NaN payloads (signaling ones included) kept.
func widenSingle(x uint32) uint64 {
	sign := uint64(x>>31) << 63
	exponent, mantissa := uint64(x>>23&0xff), uint64(x&0x7fffff)
	switch {
	case exponent == 0xff:
		return sign | 0x7ff<<52 | mantissa<<29
	case exponent == 0 && mantissa == 0:
		return sign
	case exponent == 0:
		// A subnormal becomes a normal double.
		exponent = 897
		for mantissa&0x800000 == 0 {
			mantissa <<= 1
			exponent--
		}
		return sign | exponent<<52 | (mantissa&0x7fffff)<<29
	}
	return sign | (exponent+896)<<52 | mantissa<<29
}

// narrowDouble reverses widenSingle for a double-format binary32 value, as
// stfs stores it.
func narrowDouble(x uint64) uint32 {
	sign := uint32(x>>63) << 31
	exponent, mantissa := x>>52&0x7ff, x&(1<<52-1)
	switch {
	case exponent == 0x7ff:
		return sign | 0xff<<23 | uint32(mantissa>>29)
	case exponent == 0:
		return sign
	case exponent > 896:
		return sign | uint32(exponent-896)<<23 | uint32(mantissa>>29)
	case exponent < 874:
		return sign
	}
	return sign | uint32((mantissa|1<<52)>>(926-exponent))
}

// callFloats enters code whose float passing routes words through FP
// registers or x87 ST(0).
func (c *Code) callFloats(entry uintptr, args []uintptr, workspace uintptr) (lo, hi uintptr) {
	block := floatCallBlocks.Get().(*floatCall)
	block.target = entry
	c.floats.fill(block, args)
	enterFloat(c.floats, c.options.GoManaged, c.options.ABI, block, workspace)
	lo, hi = c.floats.results(block)
	if strconv.IntSize == 64 && c.options.ResultWords != 2 {
		hi = 0
	}
	*block = floatCall{}
	floatCallBlocks.Put(block)
	return lo, hi
}

// callForeignFloats calls a C ABI entry with float passing through the
// runtime-aware foreign transition, like CallForeignPair.
func callForeignFloats(address uintptr, plan *floatPlan, args []uintptr) (lo, hi uintptr) {
	block := floatCallBlocks.Get().(*floatCall)
	block.target = address
	plan.fill(block, args)
	enterForeignFloat(uintptr(unsafe.Pointer(block)))
	runtime.KeepAlive(block)
	lo, hi = plan.results(block)
	*block = floatCall{}
	floatCallBlocks.Put(block)
	return lo, hi
}
