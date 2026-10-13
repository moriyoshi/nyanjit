// Package nyanjit exposes the target-independent Builder API.
package nyanjit

import "github.com/moriyoshi/nyanjit/builder"

type Value = builder.Value
type Predicate = builder.Predicate
type Label = builder.Label
type BranchCondition = builder.BranchCondition

type ABIFlavor = builder.ABIFlavor
type StackPractice = builder.StackPractice
type StackPointerRole = builder.StackPointerRole
type CompileOptions = builder.CompileOptions
type SpillStorage = builder.SpillStorage
type ABIStorage = builder.ABIStorage
type ABIWordLocation = builder.ABIWordLocation
type ABIValueLocation = builder.ABIValueLocation
type EmissionReport = builder.EmissionReport
type CallKind = builder.CallKind
type CallSpec = builder.CallSpec
type EffectPrecision = builder.EffectPrecision
type CallEffect = builder.CallEffect

const NativeBits = builder.NativeBits

const (
	ABIC                     = builder.ABIC
	ABIGo                    = builder.ABIGo
	StackNoUse               = builder.StackNoUse
	StackPlatform            = builder.StackPlatform
	StackPointerReserved     = builder.StackPointerReserved
	StackPointerOrdinary     = builder.StackPointerOrdinary
	SpillNone                = builder.SpillNone
	SpillWorkspace           = builder.SpillWorkspace
	SpillStack               = builder.SpillStack
	ABIRegister              = builder.ABIRegister
	ABIStack                 = builder.ABIStack
	FunctionCall             = builder.FunctionCall
	SystemCall               = builder.SystemCall
	EffectConservative       = builder.EffectConservative
	EffectPlatformGuaranteed = builder.EffectPlatformGuaranteed
	BranchEqual              = builder.BranchEqual
	BranchNotEqual           = builder.BranchNotEqual
	BranchLess               = builder.BranchLess
	BranchGreaterEqual       = builder.BranchGreaterEqual
	BranchLessEqual          = builder.BranchLessEqual
	BranchGreater            = builder.BranchGreater
	BranchBelow              = builder.BranchBelow
	BranchAboveEqual         = builder.BranchAboveEqual
	BranchBelowEqual         = builder.BranchBelowEqual
	BranchAbove              = builder.BranchAbove
)

var (
	ErrInvalidFunction = builder.ErrInvalidFunction
	ErrBackendLimit    = builder.ErrBackendLimit
)
