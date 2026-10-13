package compile

import (
	"errors"
	"fmt"

	"github.com/moriyoshi/nyanjit/backend"
	"github.com/moriyoshi/nyanjit/backend/abi"
	"github.com/moriyoshi/nyanjit/backend/execution"
	"github.com/moriyoshi/nyanjit/backend/machine"
	"github.com/moriyoshi/nyanjit/ir"
)

var (
	ErrUnsupported          = execution.ErrUnsupported
	ErrInvalidFunction      = backend.ErrInvalidFunction
	ErrBackendLimit         = backend.ErrBackendLimit
	ErrUnboundSymbol        = machine.ErrUnboundSymbol
	ErrCodeBaseRequired     = machine.ErrCodeBaseRequired
	ErrUnsupportedABI       = errors.New("nyanjit: unsupported ABI flavor")
	ErrUnsupportedStackRole = errors.New("nyanjit: unsupported stack pointer role")
	ErrStackRequired        = errors.New("nyanjit: this call lowering requires a stack frame")
	ErrExternalCallOnly     = execution.ErrExternalCallOnly
	ErrPointerMapsRequired  = errors.New("nyanjit: managed pointer maps are unavailable; explicitly assert a pointer-free Go frame")
)

// ErrCPUUnsupported reports a CPU requirement exceeding the verified host.
// It also matches ErrBackendLimit through errors.Is for compatibility.
var ErrCPUUnsupported error = cpuUnsupportedError{}

// Preserve the historical classification without appending the unrelated
// register-allocation error message to CPU diagnostics.
type cpuUnsupportedError struct{}

func (cpuUnsupportedError) Error() string        { return "nyanjit: requested CPU is unsupported" }
func (cpuUnsupportedError) Is(target error) bool { return target == ErrBackendLimit }

type opcode = ir.Opcode
type instruction = ir.Instruction
type scopeEvent = ir.ScopeExit

const (
	opParam                = ir.OpParam
	opConst                = ir.OpConst
	opAdd                  = ir.OpAdd
	opSub                  = ir.OpSub
	opMul                  = ir.OpMul
	opAnd                  = ir.OpAnd
	opOr                   = ir.OpOr
	opXor                  = ir.OpXor
	opSelect               = ir.OpSelect
	opAddScaled            = ir.OpAddScaled
	opLoad64               = ir.OpLoad64
	opStore64              = ir.OpStore64
	opLoad16               = ir.OpLoad16
	opStore16              = ir.OpStore16
	opLoad32               = ir.OpLoad32
	opStore32              = ir.OpStore32
	opLoad8                = ir.OpLoad8
	opStore8               = ir.OpStore8
	opSyscall              = ir.OpSyscall
	opCallC                = ir.OpCallC
	opCallGo               = ir.OpCallGo
	opLabelParam           = ir.OpLabelParam
	opShiftRight           = ir.OpShiftRight
	opCastUnsigned         = ir.OpCastUnsigned
	opSignExtend           = ir.OpSignExtend
	opShiftLeft            = ir.OpShiftLeft
	opShiftRightArithmetic = ir.OpShiftRightArithmetic
	opRotateLeft           = ir.OpRotateLeft
	opRotateRight          = ir.OpRotateRight
)

func instructionBits(ins instruction) uint8 {
	if ins.Bits == 0 {
		return 64
	}
	return ins.Bits
}

// PreflightCall returns the effects of an outbound call without lowering a
// function. It wraps abi.PreflightCall's errors in this package's sentinels:
// ErrInvalidFunction (with abi.ErrInvalidSpec) for an invalid spec,
// ErrBackendLimit (with abi.ErrBackendLimit) for a call the target cannot
// make, ErrUnsupportedABI and ErrUnsupported. Too many arguments return a
// *LimitError of kind LimitArguments, which matches ErrBackendLimit but not
// abi.ErrBackendLimit.
func PreflightCall(spec abi.CallSpec) (abi.CallEffect, error) {
	effect, err := abi.PreflightCall(spec)
	switch {
	case errors.Is(err, abi.ErrInvalidSpec):
		return abi.CallEffect{}, fmt.Errorf("%w: %w", ErrInvalidFunction, err)
	case errors.Is(err, abi.ErrBackendLimit):
		if limit, detail := callArgumentLimit(spec); spec.ArgumentCount > limit {
			return abi.CallEffect{}, &LimitError{Kind: LimitArguments, Needed: spec.ArgumentCount, Limit: limit, Arch: spec.Architecture, Detail: detail}
		}
		return abi.CallEffect{}, fmt.Errorf("%w: %w", ErrBackendLimit, err)
	case errors.Is(err, abi.ErrUnsupportedABI):
		return abi.CallEffect{}, fmt.Errorf("%w: %w", ErrUnsupportedABI, err)
	case errors.Is(err, abi.ErrUnsupported):
		return abi.CallEffect{}, fmt.Errorf("%w: %w", ErrUnsupported, err)
	default:
		return effect, err
	}
}
