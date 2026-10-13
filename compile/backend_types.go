package compile

import (
	"errors"

	"github.com/moriyoshi/nyanjit/abi"
	"github.com/moriyoshi/nyanjit/execution"
	"github.com/moriyoshi/nyanjit/ir"
	"github.com/moriyoshi/nyanjit/machine"
	"github.com/moriyoshi/nyanjit/regalloc"
)

type CompileOptions = abi.CompileOptions
type EmissionReport = abi.EmissionReport
type ABIFlavor = abi.Flavor
type StackPractice = abi.StackPractice
type ABIValueLocation = abi.ValueLocation
type ABIWordLocation = abi.WordLocation
type Relocation = machine.Relocation
type CallSpec = abi.CallSpec
type CallEffect = abi.CallEffect

const (
	ABIC                 = abi.C
	ABIGo                = abi.Go
	StackNoUse           = abi.StackNoUse
	StackPlatform        = abi.StackPlatform
	StackPointerReserved = abi.StackPointerReserved
	StackPointerOrdinary = abi.StackPointerOrdinary
	SpillNone            = abi.SpillNone
	SpillWorkspace       = abi.SpillWorkspace
	SpillStack           = abi.SpillStack
	ABIRegister          = abi.Register
	ABIStack             = abi.Stack
	FunctionCall         = abi.FunctionCall
	SystemCall           = abi.SystemCall
)

var (
	ErrUnsupported          = execution.ErrUnsupported
	ErrInvalidFunction      = regalloc.ErrInvalidFunction
	ErrBackendLimit         = regalloc.ErrBackendLimit
	ErrUnboundSymbol        = machine.ErrUnboundSymbol
	ErrUnsupportedABI       = errors.New("nyanjit: unsupported ABI flavor")
	ErrUnsupportedStackRole = errors.New("nyanjit: unsupported stack pointer role")
	ErrStackRequired        = errors.New("nyanjit: this call lowering requires a stack frame")
	ErrExternalCallOnly     = errors.New("nyanjit: outbound calls require an external execution boundary")
	ErrPointerMapsRequired  = errors.New("nyanjit: managed pointer maps are unavailable; explicitly assert a pointer-free Go frame")
)

type op = ir.Opcode
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

func PreflightCall(spec CallSpec) (CallEffect, error) {
	effect, err := abi.PreflightCall(spec)
	switch {
	case errors.Is(err, abi.ErrInvalidSpec):
		return CallEffect{}, ErrInvalidFunction
	case errors.Is(err, abi.ErrBackendLimit):
		return CallEffect{}, ErrBackendLimit
	case errors.Is(err, abi.ErrUnsupportedABI):
		return CallEffect{}, ErrUnsupportedABI
	case errors.Is(err, abi.ErrUnsupported):
		return CallEffect{}, ErrUnsupported
	default:
		return effect, err
	}
}

// preflightWithEffect resolves ABI identity independently of effect narrowing.
func preflightWithEffect(spec CallSpec, effect *CallEffect) (CallEffect, error) {
	if effect != nil && spec.Kind == FunctionCall && spec.ABI == ABIC {
		spec.PPC64CABI = effect.PPC64CABI
	}
	return PreflightCall(spec)
}

func ppc64EntryConvention(target machineTarget) abi.PPC64CABI {
	if profile, ok := target.(interface{ CABI() abi.PPC64CABI }); ok {
		return profile.CABI()
	}
	return abi.PPC64CABIUnspecified
}

func validateEntryConvention(options CompileOptions) error {
	if options.PPC64CABI == abi.PPC64CABIUnspecified {
		return nil
	}
	architecture := targetArchitecture(options)
	if options.ABI != ABIC || architecture != "ppc64" && architecture != "ppc64le" {
		return ErrUnsupportedABI
	}
	if _, err := abi.NewPPC64Profile(architecture, options.ABI, options.PPC64CABI); err != nil {
		return ErrUnsupportedABI
	}
	return nil
}
